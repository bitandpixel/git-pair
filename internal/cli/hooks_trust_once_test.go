package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
	"gitpair/internal/hooks"
)

// `--trust-hooks-once` answers the one refusal `--no-hooks` deliberately leaves standing: the tracked tree this clone
// has taken no position on. The scripts run, because that is the answer the gate is looking for, and nothing is
// written, because recording a position is a statement about what a person read and a flag cannot make that
// statement on their behalf.
//
// The case it exists for is a review of the hooks themselves. A changeset that edits `.git-pair/hooks/` puts
// content in the reviewer's worktree that their clone has not recorded, and the submission that would record an
// opinion about it is refused until they accept it — which asks the reviewer to approve the thing they were
// asked to review. `--trust-hooks-once` lets them read it, run it, and say what they think.

func TestTrustOnceRunsTheUnrecordedTreeAndRecordsNothing(t *testing.T) {
	// trustedRecordPath is where `git pair hooks --trust` would have written. Absence is the assertion: the
	// flag runs the scripts and leaves the clone's position exactly as it was.
	trustedRecordPath := func(t *testing.T, f *gittest.Fixture) string {
		t.Helper()
		gitDir := strings.TrimSpace(f.MustGit("rev-parse", "--absolute-git-dir"))
		return hooks.TrustedRecordPath(gitDir)
	}

	// The refusal is where a person meets the problem, and it is where they should meet the way through it. The
	// reviewer who hit this case had to guess that a flag existed; a sentence that names `--trust` and `--decline`
	// and not the third option asks the person to read the manual to get their own work submitted. The clause is
	// scoped in its own words to the person whose task is to read the scripts, because that is the case the flag
	// exists for and it is not a general invitation to run untrusted content. Both states are pinned because they
	// are two sentences, and only a shared constant keeps them saying the same thing.
	for _, state := range []struct {
		name  string
		want  string
		setup func(t *testing.T, f *gittest.Fixture, dir string)
	}{
		{
			name: "an unseen script",
			want: "have not been recorded in this clone",
			setup: func(t *testing.T, f *gittest.Fixture, dir string) {
				putHook(t, f, "tracked", "pre-ready", "01-suite", touch(dir, "RAN"))
			},
		},
		{
			name: "a script that moved after it was recorded",
			want: "changed since this clone recorded them",
			setup: func(t *testing.T, f *gittest.Fixture, dir string) {
				putHook(t, f, "tracked", "pre-ready", "01-suite", "#!/bin/sh\nexit 0\n")
				trustHooks(t, f)
				f.Append(filepath.Join(".git-pair", "hooks", "pre-ready", "01-suite"), "echo escalated\n")
				f.Commit("escalate the hook")
			},
		},
	} {
		t.Run("the refusal names the one command, for "+state.name, func(t *testing.T) {
			f, _ := newChangeset(t, "checkout-flow", "main")
			dir := scratch(t)
			state.setup(t, f, dir)

			res := runIn(t, f.Dir(), "change", "ready")
			if res.code != exitRefusal {
				t.Fatalf("exit = %d, want %d — and nothing should be written while no position is recorded\nstdout: %s\nstderr: %s",
					res.code, exitRefusal, res.stdout, res.stderr)
			}
			got := res.stdout + res.stderr
			mustContain(t, got, state.want, "the refusal has to say what is missing")
			for _, phrase := range []string{"git pair hooks --trust", "git pair hooks --decline", "--trust-hooks-once", "--no-hooks", "nothing is recorded"} {
				mustContain(t, got, phrase, "the refusal names its ways out")
			}
			if _, err := os.Stat(filepath.Join(dir, "RAN")); err == nil {
				t.Error("the refusal came with a hook that ran; naming the flag in the sentence is not the same as running it")
			}
		})
	}

	t.Run("the unrecorded hook runs, and the hand-off says it was unrecorded", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		dir := scratch(t)
		putHook(t, f, "tracked", "pre-ready", "01-suite", touch(dir, "RAN"))

		res := runIn(t, f.Dir(), "change", "ready", "--trust-hooks-once", "--json").
			mustSucceed(t, "change", "ready", "--trust-hooks-once", "--json")

		if _, err := os.Stat(filepath.Join(dir, "RAN")); err != nil {
			t.Errorf("the tracked hook did not run: %v", err)
		}
		entries := res.jsonList(t, "hooks")
		if len(entries) != 1 {
			t.Fatalf("hooks = %v, want the one hook that ran", entries)
		}
		entry := entries[0].(map[string]any)
		if entry["status"] != "passed" || entry["consent"] != "once" {
			t.Errorf("hook entry = %v, want a passed entry whose consent is `once`", entry)
		}
		if _, err := os.Stat(trustedRecordPath(t, f)); err == nil {
			t.Error("--trust-hooks-once wrote the trust record; recording a position is `git pair hooks --trust`'s act")
		}
		// The next command asks again, which is the whole point of recording nothing.
		again := runIn(t, f.Dir(), "hooks")
		if !strings.Contains(again.stderr, "NOT trusted") && !strings.Contains(again.stderr, "not trusted") {
			t.Errorf("after --trust-hooks-once the clone still reads as having recorded nothing; got: %s", again.stderr)
		}
	})

	t.Run("a failing hook still refuses", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-ready", "01-suite", failHook("the suite says no"))

		res := runIn(t, f.Dir(), "change", "ready", "--trust-hooks-once")
		if res.code != exitRefusal {
			t.Fatalf("exit = %d, want %d: the flag says these may run, not that they pass\nstdout: %s\nstderr: %s",
				res.code, exitRefusal, res.stdout, res.stderr)
		}
		if !strings.Contains(res.stdout+res.stderr, "the suite says no") {
			t.Errorf("the refusal did not carry the hook's own text: %s", res.stderr)
		}
	})

	// The reported trap, end to end: the changeset under review is the one that rewrote the reviewer's own
	// submission checks. A hook at some other point no longer blocks a submission at all — that is the scoping
	// rule, pinned in `TestThePositionIsAskedOnlyWhereTheCommandRuns` — so this is the case the flag is for.
	t.Run("the reviewer of a hook change can submit", func(t *testing.T) {
		f, _ := newChangeset(t, "tighten-hooks", "main")
		putHook(t, f, "tracked", "pre-submit", "01-policy", passHook)
		trustHooks(t, f)
		ready(t, f)

		// The changeset rewrites the script the submission is about to run. The clone's position is about the
		// old content.
		putHook(t, f, "tracked", "pre-submit", "01-policy", "#!/bin/sh\nexit 0\n# rewritten\n")

		blocked := runIn(t, f.Dir(), "review", "submit", "--feedback", "-m", "the rewritten hook is wrong")
		if blocked.code == 0 {
			t.Fatal("the submission went through without the flag, so this case proves nothing")
		}
		if !strings.Contains(blocked.stdout+blocked.stderr, "changed since this clone recorded them") {
			t.Fatalf("the refusal is not the position refusal: %s", blocked.stderr)
		}

		runIn(t, f.Dir(), "review", "submit", "--feedback", "-m", "the rewritten hook is wrong", "--trust-hooks-once", "--json").
			mustSucceed(t, "review", "submit", "--feedback", "--trust-hooks-once")

		// And the verdict the reviewer came for is what landed.
		out := runIn(t, f.Dir(), "review", "history", "--json").mustSucceed(t, "review", "history", "--json")
		entries := out.jsonList(t, "reviews")
		if len(entries) == 0 {
			t.Fatalf("reviews = %v, want the submission the flag let through", entries)
		}
		if first := entries[0].(map[string]any); first["outcome"] != "feedback" {
			t.Errorf("newest review outcome = %v, want feedback", first["outcome"])
		}
	})

	t.Run("the gate runs the unrecorded checks instead of refusing them", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		dir := scratch(t)
		putHook(t, f, "tracked", "pre-check", "01-suite", touch(dir, "RAN_CHECK"))

		// The hand-off and the submission run no script from this tree at all, so neither asks — see
		// `TestThePositionIsAskedOnlyWhereTheCommandRuns`. The gate is the command that does.
		ready(t, f)
		submit(t, f, "approve")

		if res := runIn(t, f.Dir(), "check", "--json"); res.json(t)["ready"] != false {
			t.Fatalf("check answered ready with no position recorded, so this case proves nothing")
		}

		res := runIn(t, f.Dir(), "check", "--trust-hooks-once", "--json").mustSucceed(t, "check", "--trust-hooks-once", "--json")
		if res.json(t)["ready"] != true {
			t.Errorf("ready = %v, want true now the checks ran; reasons = %v", res.json(t)["ready"], res.jsonList(t, "reasons"))
		}
		if _, err := os.Stat(filepath.Join(dir, "RAN_CHECK")); err != nil {
			t.Error("the pre-check hook never ran under --trust-hooks-once")
		}
		found := false
		for _, e := range res.jsonList(t, "hooks") {
			entry := e.(map[string]any)
			if entry["point"] == "pre-check" && entry["consent"] == "once" {
				found = true
			}
		}
		if !found {
			t.Errorf("hooks = %v, want the pre-check entry marked `once`", res.jsonList(t, "hooks"))
		}
	})

	t.Run("the two flags together are a usage error, wherever both are offered", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-ready", "01-suite", passHook)

		// Every command that runs the repository's scripts now carries both flags, so the pair has to be refused in
		// one place rather than eight — which is what `hookOverridesAgree` in `PersistentPreRunE` is. It answers
		// before any command body runs, so the gate and the declaration are refused here as ordinary commands are:
		// nobody buys a cleared position by asking to run nothing. `check` and `change integrate` are the cases the
		// widening made reachable, so they are in the list.
		for _, args := range [][]string{
			{"change", "ready"},
			{"check"},
			{"change", "integrate"},
		} {
			withFlags := append(append([]string{}, args...), "--no-hooks", "--trust-hooks-once")
			res := runIn(t, f.Dir(), withFlags...)
			if res.code != exitUsage {
				t.Errorf("%v: exit = %d, want %d\nstdout: %s\nstderr: %s",
					args, res.code, exitUsage, res.stdout, res.stderr)
				continue
			}
			if !strings.Contains(res.stdout+res.stderr, "opposite") {
				t.Errorf("%v: the refusal says what is wrong: %s", args, res.stderr)
			}
		}
	})
}
