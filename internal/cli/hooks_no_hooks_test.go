package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `--no-hooks` is the per-command form of the off switch. Three things hold it together, and each is a case
// below: it skips both halves of a transition and both roots, because "not this time" is about scripts rather
// than about which root they came from; it says so, in the array a machine already reads and on stderr, because
// a hand-off that skipped the suite must not be reportable as one that ran it; and it stops there, because a
// person saying their machine should not run scripts today says nothing about what those scripts do, so it
// cannot be the answer to "has anybody read these?".
//
// It is on `check` and `change integrate` as well, in the same direction the other commands have it: the verdict
// is what a person reads to find out what is wrong, and a hook that fails should not also stop the gate answering
// its other questions. What is skipped is reported rather than hidden, and the position question is untouched —
// the off switch says nothing about what the scripts do. The job that lands a change runs `git pair check` without
// either flag and verifies the merged tree with its own command.

func TestNoHooksSkipsTheRunAndSaysSo(t *testing.T) {
	t.Run("a trusted hook does not run, and the marker still stands", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		dir := scratch(t)
		putHook(t, f, "tracked", "pre-ready", "01-suite", touch(dir, "RAN"))
		trustHooks(t, f)

		res := runIn(t, f.Dir(), "change", "ready", "--no-hooks", "--json").
			mustSucceed(t, "change", "ready", "--no-hooks", "--json")

		if _, err := os.Stat(filepath.Join(dir, "RAN")); err == nil {
			t.Error("the pre-ready hook ran although the command said --no-hooks")
		}
		entries := res.jsonList(t, "hooks")
		if len(entries) != 1 {
			t.Fatalf("hooks = %v, want one entry naming what did not run", entries)
		}
		entry := entries[0].(map[string]any)
		if entry["status"] != "skipped-by-flag" || entry["point"] != "pre-ready" || entry["name"] != "01-suite" {
			t.Errorf("hook entry = %v, want a skipped-by-flag pre-ready entry named 01-suite", entry)
		}
		// The transition happened: the flag is about scripts, not about the hand-off.
		if res.json(t)["state"] != "READY" {
			t.Errorf("state = %v, want ready", res.json(t)["state"])
		}
	})

	t.Run("the clone's own hooks are skipped too", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		dir := scratch(t)
		// The per-clone root needs no acceptance, so this is the case a reviewer's own check is in.
		putHook(t, f, "clone", "pre-ready", "my-suite", touch(dir, "RAN_CLONE"))

		runIn(t, f.Dir(), "change", "ready", "--no-hooks").mustSucceed(t, "change", "ready", "--no-hooks")

		if _, err := os.Stat(filepath.Join(dir, "RAN_CLONE")); err == nil {
			t.Error("the clone's own hook ran although the command said --no-hooks")
		}
	})

	// The half git's own --no-verify keeps. A post-hook refuses nothing, but its failure still makes the
	// command exit nonzero, so a notify hook left broken by a repository the reviewer cannot edit holds the
	// hand-off open. One flag, one predictable outcome: nothing runs.
	t.Run("the post-hooks are skipped as well", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "post-ready", "01-notify", failHook("notify is broken"))
		trustHooks(t, f)

		if res := runIn(t, f.Dir(), "change", "ready"); res.code == 0 {
			t.Fatal("the failing post-ready hook let the command exit 0, so this case proves nothing")
		}
	})

	t.Run("the flag is what the failing post-ready hook needs", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "post-ready", "01-notify", failHook("notify is broken"))
		trustHooks(t, f)

		runIn(t, f.Dir(), "change", "ready", "--no-hooks").mustSucceed(t, "change", "ready", "--no-hooks")
	})

	t.Run("the refusal it does not clear is the position, and it says so", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		dir := scratch(t)
		// Configured and trusted by nobody: the tree the clone has taken no position on.
		putHook(t, f, "tracked", "pre-ready", "01-suite", touch(dir, "RAN"))

		res := runIn(t, f.Dir(), "change", "ready", "--no-hooks")
		if res.code != exitRefusal {
			t.Fatalf("exit = %d, want %d: skipping the scripts is not reading them\nstdout: %s\nstderr: %s",
				res.code, exitRefusal, res.stdout, res.stderr)
		}
		if !strings.Contains(res.stdout+res.stderr, "git pair hooks --trust") {
			t.Errorf("the refusal does not name the way that answers the question: %s", res.stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, "RAN")); err == nil {
			t.Error("the refused command ran the hook")
		}
	})

	t.Run("a reviewer's verdict over a refusing pre-submit hook", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		ready(t, f)
		putHook(t, f, "clone", "pre-submit", "01-policy", failHook("no review today"))

		blocked := runIn(t, f.Dir(), "review", "submit", "--approve")
		if blocked.code == 0 {
			t.Fatal("the refusing pre-submit hook let the submission through, so this case proves nothing")
		}

		res := runIn(t, f.Dir(), "review", "submit", "--approve", "--no-hooks", "--json").
			mustSucceed(t, "review", "submit", "--approve", "--no-hooks", "--json")
		if res.json(t)["outcome"] != "approve" {
			t.Errorf("outcome = %v, want approve", res.json(t)["outcome"])
		}
		found := false
		for _, e := range res.jsonList(t, "hooks") {
			entry := e.(map[string]any)
			if entry["point"] == "pre-submit" && entry["status"] == "skipped-by-flag" {
				found = true
			}
		}
		if !found {
			t.Errorf("hooks = %v, want the skipped pre-submit entry", res.jsonList(t, "hooks"))
		}
	})

	// The reason the flag is on the gate at all. A repository's `pre-check` suite that is broken on one machine
	// should cost that person their suite, not their ability to find out whether anything else is wrong — and the
	// verdict a skipped run produces cannot land work, because the integrate job asks for its own.
	t.Run("the gate reports the rest of itself when a hook is broken", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-check", "01-suite", failHook("the suite is broken on this machine"))
		trustHooks(t, f)
		ready(t, f)
		submit(t, f, "approve")

		blocked := runIn(t, f.Dir(), "check", "--json")
		if blocked.json(t)["ready"] != false {
			t.Fatalf("check answered ready with a failing pre-check hook, so this case proves nothing")
		}
		mustContain(t, blocked.stdout+blocked.stderr, "the suite is broken on this machine",
			"the verdict has to carry the hook's own refusal")

		res := runIn(t, f.Dir(), "check", "--no-hooks", "--json").mustSucceed(t, "check", "--no-hooks", "--json")
		if res.json(t)["ready"] != true {
			t.Errorf("check --no-hooks still refuses: %v", res.json(t)["reasons"])
		}
		if strings.Contains(fmt.Sprint(res.json(t)["reasons"]), "the suite is broken") {
			t.Errorf("the skipped hook still refuses the verdict it did not run: %v", res.json(t)["reasons"])
		}
		entries := res.jsonList(t, "hooks")
		if len(entries) != 1 {
			t.Fatalf("hooks = %v, want one entry naming the hook that did not run", entries)
		}
		if entry := entries[0].(map[string]any); entry["point"] != "pre-check" || entry["status"] != "skipped-by-flag" {
			t.Errorf("hook entry = %v, want a skipped-by-flag pre-check entry", entry)
		}
	})

	// Skipping is not reading. The flag says the scripts should not run here today; the position question is
	// about whether anybody read them, and a skipped run cannot answer that — which is why the gate keeps
	// refusing, and keeps naming the two commands that would answer it.
	t.Run("the gate still asks for the position the flag cannot answer", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-check", "01-suite", passHook)
		ready(t, f)
		submit(t, f, "approve")

		res := runIn(t, f.Dir(), "check", "--no-hooks", "--json").mustSucceed(t, "check", "--no-hooks", "--json")
		if res.json(t)["ready"] != false {
			t.Errorf("check --no-hooks answered ready over a tree this clone never recorded; reasons: %v", res.json(t)["reasons"])
		}
		mustContain(t, fmt.Sprint(res.json(t)["reasons"]), "have not been recorded in this clone",
			"the position has to be asked by the command that would have run the script")
	})

	// The declaration is the author asking, and the approval underneath the declaration is what permits the
	// merge. A notify hook that has fallen over must not strand an approved head that the integrate job would
	// verify for itself anyway.
	t.Run("the declaration does not rest on a hook that did not run", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "clone", "pre-integrate", "01-announce", failHook("announce is down"))
		ready(t, f)
		submit(t, f, "approve")

		if res := runIn(t, f.Dir(), "change", "integrate"); res.code == 0 {
			t.Fatal("the declaration went through with a refusing pre-integrate hook, so this case proves nothing")
		}

		res := runIn(t, f.Dir(), "change", "integrate", "--no-hooks", "--json").
			mustSucceed(t, "change", "integrate", "--no-hooks", "--json")
		if res.json(t)["recorded"] != true {
			t.Errorf("the declaration was not recorded: %v", res.json(t))
		}
		found := false
		for _, e := range res.jsonList(t, "hooks") {
			entry := e.(map[string]any)
			if entry["point"] == "pre-integrate" && entry["status"] == "skipped-by-flag" {
				found = true
			}
		}
		if !found {
			t.Errorf("hooks = %v, want the skipped pre-integrate entry", res.jsonList(t, "hooks"))
		}
	})
}
