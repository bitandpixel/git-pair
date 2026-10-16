package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The position question, asked about the scripts the command actually runs.
//
// `Trust` decides over the whole tracked tree — a position is about content, and a check nobody has read is
// still what `accepted` and `declined` exist to answer — but the *question* has to be proportional to the
// demand, or it becomes the pressure that teaches people to accept without reading. A repository whose only
// hook is a `pre-ready` suite has nothing to say about `git pair check`, which runs `pre-check` and
// `post-check`: stopping the merge to make somebody read a script no merge executes is a refusal about the
// wrong content, and it is what stranded a reviewer of a hook change who was trying to record an opinion about
// exactly that script.
//
// Each case below is one hook, at one point, with the clone having recorded nothing: the commands that would
// execute it refuse, and the ones that would not go about their business.

func TestThePositionIsAskedOnlyWhereTheCommandRuns(t *testing.T) {
	const unrecorded = "have not been recorded in this clone"

	asked := func(t *testing.T, res result, command string) {
		t.Helper()
		if res.code != exitRefusal {
			t.Fatalf("`git pair %s` exited %d, want %d: the command runs this hook and should ask\nstdout: %s\nstderr: %s",
				command, res.code, exitRefusal, res.stdout, res.stderr)
		}
		mustContain(t, res.stdout+res.stderr, unrecorded, "the refusal is the position refusal")
	}
	free := func(t *testing.T, res result, command string) {
		t.Helper()
		if res.code == exitRefusal && strings.Contains(res.stdout+res.stderr, unrecorded) {
			t.Fatalf("`git pair %s` refused for a hook it does not run\nstdout: %s\nstderr: %s",
				command, res.stdout, res.stderr)
		}
	}

	t.Run("the hand-off asks about its own points", func(t *testing.T) {
		for _, point := range []string{"pre-ready", "post-ready"} {
			t.Run("asks about "+point, func(t *testing.T) {
				f, _ := newChangeset(t, "checkout-flow", "main")
				putHook(t, f, "tracked", point, "01-suite", passHook)
				asked(t, runIn(t, f.Dir(), "change", "ready"), "change ready")
			})
		}
		for _, point := range []string{"pre-check", "pre-submit", "pre-integrate"} {
			t.Run("says nothing about "+point, func(t *testing.T) {
				f, _ := newChangeset(t, "checkout-flow", "main")
				putHook(t, f, "tracked", point, "01-suite", passHook)
				free(t, runIn(t, f.Dir(), "change", "ready"), "change ready")
			})
		}
	})

	t.Run("the submission asks about its own points", func(t *testing.T) {
		for _, point := range []string{"pre-submit", "post-submit"} {
			t.Run("asks about "+point, func(t *testing.T) {
				f, _ := newChangeset(t, "checkout-flow", "main")
				ready(t, f)
				putHook(t, f, "tracked", point, "01-policy", passHook)
				asked(t, runIn(t, f.Dir(), "review", "submit", "--approve"), "review submit")
			})
		}
		t.Run("says nothing about pre-ready", func(t *testing.T) {
			f, _ := newChangeset(t, "checkout-flow", "main")
			putHook(t, f, "tracked", "pre-ready", "01-suite", passHook)
			runIn(t, f.Dir(), "change", "ready", "--trust-once").mustSucceed(t, "change", "ready")
			free(t, runIn(t, f.Dir(), "review", "submit", "--approve"), "review submit")
		})
	})

	// The gate is one function shared by `check` and `change integrate`, and the two run different points, so
	// the caller names them. These four cases are the reason it is a parameter and not a constant.
	t.Run("the gate asks about the points its caller runs", func(t *testing.T) {
		// An approved changeset carrying one unrecorded tracked hook at `point`.
		approvedWith := func(t *testing.T, point string) *gittest.Fixture {
			t.Helper()
			f, _ := newChangeset(t, "checkout-flow", "main")
			putHook(t, f, "tracked", point, "01-suite", passHook)
			// The hand-off asks only if the hook is at its own point; anywhere else it walks free.
			runIn(t, f.Dir(), "change", "ready", "--trust-once").mustSucceed(t, "change", "ready")
			runIn(t, f.Dir(), "review", "submit", "--approve", "--trust-once").
				mustSucceed(t, "review", "submit", "--approve")
			return f
		}

		t.Run("check asks about pre-check", func(t *testing.T) {
			f := approvedWith(t, "pre-check")
			res := runIn(t, f.Dir(), "check")
			asked(t, res, "check")
		})
		t.Run("check says nothing about pre-ready", func(t *testing.T) {
			f := approvedWith(t, "pre-ready")
			res := runIn(t, f.Dir(), "check")
			free(t, res, "check")
			if res.code != 0 {
				t.Errorf("`git pair check` exited %d, want 0 — a verdict about a script no merge runs\n%s", res.code, res.stderr)
			}
		})
		t.Run("the declaration asks about pre-integrate", func(t *testing.T) {
			f := approvedWith(t, "pre-integrate")
			asked(t, runIn(t, f.Dir(), "change", "integrate"), "change integrate")
		})
		t.Run("the declaration says nothing about pre-check", func(t *testing.T) {
			f := approvedWith(t, "pre-check")
			runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")
		})
	})
}
