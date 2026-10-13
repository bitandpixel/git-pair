package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// A reviewer's own checks, on the surface reviewers use. The reviewer-side points differ from the author's
// in two ways that matter. They run after staging, so the hook sees the index the review commit is about to
// carry — the source edits made while reading, the thread files, the ABOUT.md edit — rather than the tree as
// it was left. And they are the client's own: a reviewer reviewing a repository they cannot edit attaches
// them under their own git directory, where there is nothing to accept and nobody to ask.

// reviewerFixture is a changeset with work in it, offered to a reviewer who has not submitted yet.
func reviewerFixture(t *testing.T) (*gittest.Fixture, string) {
	t.Helper()
	return newChangeset(t, "checkout-flow", "main")
}

func TestPreSubmitHooksSeeTheIndex(t *testing.T) {
	t.Run("the hook sees the reviewer's edit staged for the review commit", func(t *testing.T) {
		f, _ := reviewerFixture(t)
		// The reviewer fixed a naming problem while reading. It is in the working tree, unstaged,
		// exactly as it is when they type `a` in the review screen. It has to differ from what the
		// fixture committed, or there is nothing in the index for the hook to see.
		f.Write("handler.go", "package main\n\nfunc ServeHTTP() {}\n")
		dir := scratch(t)
		staged := filepath.Join(dir, "STAGED")
		putHook(t, f, "clone", "pre-submit", "01-style",
			fmt.Sprintf("#!/bin/sh\ngit diff --cached --name-only > %s\nexit 0\n", strconv.Quote(staged)))

		runIn(t, f.Dir(), "review", "submit", "--approve").mustSucceed(t, "review", "submit", "--approve")

		got := strings.TrimSpace(readFile(t, staged))
		if !strings.Contains(got, "handler.go") {
			t.Errorf("the hook saw %q staged, want the reviewer's edit: a hook that ran before staging would "+
				"approve a submission it could not see", got)
		}
	})

	t.Run("a hook that rejects the body blocks the approval until the body is fixed", func(t *testing.T) {
		f, _ := reviewerFixture(t)
		// The ordinary reviewer-side policy: no approval without a reason. This is the reviewer
		// guarding their own verdict, not the repository guarding the author.
		policy := `#!/bin/sh
if [ ! -f "${GIT_PAIR_REVIEW_BODY_FILE:-}" ] || ! grep -qi 'because' "$GIT_PAIR_REVIEW_BODY_FILE"; then
  echo "an approval needs a reason: say why" >&2
  exit 1
fi
exit 0
`
		putHook(t, f, "clone", "pre-submit", "01-reason", policy)

		res := runIn(t, f.Dir(), "review", "submit", "--approve")
		if res.code != exitRefusal {
			t.Fatalf("an approval without a reason exited %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
		}
		mustContain(t, res.stderr, "an approval needs a reason", "the hook's own words are the point of it")
		if subject := f.Subject(f.Head()); strings.HasPrefix(subject, "review:") {
			t.Error("a refused submission still wrote a review commit")
		}

		// The same command, with the body the policy asked for.
		runIn(t, f.Dir(), "review", "submit", "--approve", "-m", "clear because the tests cover it").
			mustSucceed(t, "review", "submit", "--approve")
		if subject := f.Subject(f.Head()); !strings.HasPrefix(subject, "review: approve ") {
			t.Errorf("head is %q, want the approval", subject)
		}
	})

	t.Run("the reviewer environment names the outcome, the body, and the head", func(t *testing.T) {
		f, _ := reviewerFixture(t)
		dir := scratch(t)
		out := strconv.Quote(filepath.Join(dir, "ENV"))
		putHook(t, f, "clone", "pre-submit", "01-env", strings.Join([]string{
			"#!/bin/sh",
			"{",
			`printf 'point=%s\n' "$GIT_PAIR_HOOK_POINT"`,
			`printf 'outcome=%s\n' "$GIT_PAIR_REVIEW_OUTCOME"`,
			`printf 'head=%s\n' "$GIT_PAIR_REVIEW_HEAD"`,
			`printf 'body=%s\n' "$(cat "${GIT_PAIR_REVIEW_BODY_FILE:-/nonexistent}")"`,
			`} > ` + out,
			`exit 0`,
		}, "\n")+"\n")
		head := f.Head()

		runIn(t, f.Dir(), "review", "submit", "--feedback", "-m", "naming only").
			mustSucceed(t, "review", "submit", "--feedback")

		env := readFile(t, filepath.Join(dir, "ENV"))
		for _, want := range []string{"point=pre-submit", "outcome=feedback", "head=" + head, "body=naming only"} {
			if !strings.Contains(env, want) {
				t.Errorf("reviewer environment is missing %q, got:\n%s", want, env)
			}
		}
	})

	t.Run("a refusal says the reviewer's content is still staged", func(t *testing.T) {
		f, _ := reviewerFixture(t)
		f.Write("handler.go", "package main\n\nfunc ServeHTTP() {}\n")
		putHook(t, f, "clone", "pre-submit", "01-block", failHook("no"))

		res := runIn(t, f.Dir(), "review", "submit", "--approve")
		if res.code != exitRefusal {
			t.Fatalf("exit = %d, want %d", res.code, exitRefusal)
		}
		// This state has no other symptom. The reviewer's edits are staged and there is no commit, so the
		// next plain `git commit` would land the reviewer's source edits as ordinary work with no verdict
		// attached — and nothing on the screen would say so.
		mustContain(t, res.stderr, "still staged", "the refusal must say where the reviewer's work is")
		mustContain(t, res.stderr, "not recorded", "and that the review did not happen")
	})
}

// TestAllowHookFailureRecordsAndSaysSo is the reviewer-side override, and the reason it exists while `check`
// has none. A reviewer's hook is their own script on their own machine; when it misbehaves it must not strand
// a verdict they have decided to record. The override is loud in both output forms, because an override that
// reads like a pass is how a repository ends up believing its checks ran.
func TestAllowHookFailureRecordsAndSaysSo(t *testing.T) {
	f, _ := reviewerFixture(t)
	putHook(t, f, "clone", "pre-submit", "01-broken", failHook("the hook itself crashed"))

	res := runIn(t, f.Dir(), "review", "submit", "--approve", "--allow-hook-failure", "--json").
		mustSucceed(t, "review", "submit", "--approve")

	out := res.json(t)
	if out["overridden"] != true {
		t.Errorf("overridden = %v, want true", out["overridden"])
	}
	if !strings.HasPrefix(f.Subject(f.Head()), "review: approve ") {
		t.Errorf("head is %q, want the submission recorded", f.Subject(f.Head()))
	}
	hooks := res.jsonList(t, "hooks")
	if len(hooks) == 0 {
		t.Fatal("hooks is empty: an override that leaves no trace in the answer is a lie by omission")
	}
	blocked := false
	for _, e := range hooks {
		entry := e.(map[string]any)
		if entry["status"] == "failed" && entry["name"] == "01-broken" {
			blocked = true
		}
	}
	if !blocked {
		t.Errorf("hooks = %v, want the refusing hook reported as failed", hooks)
	}
	mustContain(t, res.stderr, "records it anyway", "and the human stream has to say the override happened")
}

// TestSubmitPointsInBothRoots is the per-clone half of the reviewer contract. A reviewer cannot commit a
// check into the repository they are reviewing, so the root under their own git directory is the one that
// makes the feature usable for them at all — and it needs no consent, because it is their own file.
func TestSubmitPointsInBothRoots(t *testing.T) {
	t.Run("a per-clone hook runs where nothing is tracked", func(t *testing.T) {
		f, _ := reviewerFixture(t)
		dir := scratch(t)
		putHook(t, f, "clone", "pre-submit", "mine", touch(dir, "RAN_CLONE"))

		runIn(t, f.Dir(), "review", "submit", "--approve").mustSucceed(t, "review", "submit", "--approve")

		if _, err := os.Stat(filepath.Join(dir, "RAN_CLONE")); err != nil {
			t.Error("the reviewer's own hook did not run")
		}
	})

	t.Run("a tracked hook needs acceptance, and runs once accepted", func(t *testing.T) {
		f, _ := reviewerFixture(t)
		dir := scratch(t)
		putHook(t, f, "tracked", "pre-submit", "01-repo", touch(dir, "RAN_TRACKED"))

		runIn(t, f.Dir(), "review", "submit", "--approve").mustSucceed(t, "review", "submit", "--approve")

		if _, err := os.Stat(filepath.Join(dir, "RAN_TRACKED")); err == nil {
			t.Error("a tracked hook from the repository under review ran unaccepted")
		}

		// The same verdict once the reviewer has read the script they are being asked to run. A second
		// submission supersedes the first, which is ordinary: the point of the case is the acceptance, and
		// trust lives with the clone, not with the changeset.
		trustHooks(t, f)
		runIn(t, f.Dir(), "review", "submit", "--approve").mustSucceed(t, "review", "submit", "--approve")
		if _, err := os.Stat(filepath.Join(dir, "RAN_TRACKED")); err != nil {
			t.Error("the accepted tracked hook did not run")
		}
	})

	t.Run("both roots run, tracked first", func(t *testing.T) {
		f, _ := reviewerFixture(t)
		dir := scratch(t)
		order := filepath.Join(dir, "ORDER")
		// The tracked tree has to be accepted for its half to run, which is the same rule as everywhere
		// else: a committed script is code that arrived from somewhere.
		putHook(t, f, "tracked", "pre-submit", "01-repo",
			fmt.Sprintf("#!/bin/sh\necho repo >> %s\nexit 0\n", strconv.Quote(order)))
		trustHooks(t, f)
		putHook(t, f, "clone", "pre-submit", "02-mine",
			fmt.Sprintf("#!/bin/sh\necho clone >> %s\nexit 0\n", strconv.Quote(order)))

		runIn(t, f.Dir(), "review", "submit", "--approve").mustSucceed(t, "review", "submit", "--approve")

		got := strings.Fields(strings.TrimSpace(readFile(t, order)))
		if len(got) != 2 || got[0] != "repo" || got[1] != "clone" {
			t.Errorf("order = %v, want [repo clone]", got)
		}
	})
}

// TestPostSubmitFailureLeavesTheVerdict is the post-hook rule on the surface where it matters most. A
// submission is a published verdict: taking it back would be a write the contract does not account for, and
// leaving it invisible would hide the failure from the reviewer who has to act on it.
func TestPostSubmitFailureLeavesTheVerdict(t *testing.T) {
	f, _ := reviewerFixture(t)
	putHook(t, f, "clone", "post-submit", "01-notify", failHook("webhook refused"))

	res := runIn(t, f.Dir(), "review", "submit", "--approve")
	if res.code != exitRefusal {
		t.Fatalf("exit = %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
	}
	subject := f.Subject(f.Head())
	if !strings.HasPrefix(subject, "review: approve ") {
		t.Errorf("head is %q, want the approval to stand", subject)
	}
	mustContain(t, res.stderr, "APPROVE review recorded", "the message must say the verdict is in")
	mustContain(t, res.stderr, "nothing was undone", "and that nothing was reversed")
	mustContain(t, res.stderr, "webhook refused", "with the hook's own output")
}
