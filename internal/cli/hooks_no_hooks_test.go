package cli_test

import (
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
// It is deliberately absent from `check` and `change integrate`. Those two are where a green answer has to mean
// the repository's checks ran.

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
}
