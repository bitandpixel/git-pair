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

// The author's four write points, and the two halves of each. A pre-hook stands in front of a transition and
// a post-hook stands after it, and the difference between them is not where they run but what they are
// allowed to do about the outcome: one refuses, and the other reports, because git-pair does not unwind a
// lifecycle marker once it is written. These cases ask the same four questions of each transition — does a
// refusal leave nothing behind, does a failure after the fact leave the marker alone, is a hook that commits
// noticed, and does every hook after a failure still run.
type authorPoint struct {
	// name is the transition, used for the subtest title and the point names.
	name string
	// args is the command that performs it.
	args []string
	// markerSubject is the subject prefix of the marker it writes, and is how these cases tell "the
	// transition happened" from "the command exited 0".
	markerSubject string
	// state is the word a post-hook failure is expected to use for what the changeset now is. A reader who
	// is told only "failed" will assume the marker was not written, so the message has to say which.
	state string
	// arrange brings the fixture to the state the command acts on.
	arrange func(t *testing.T, f *gittest.Fixture)
}

func authorPoints() []authorPoint {
	return []authorPoint{
		{
			name:          "ready",
			args:          []string{"change", "ready"},
			markerSubject: "git-pair: ready ",
			state:         "READY",
		},
		{
			name:          "unready",
			args:          []string{"change", "unready"},
			markerSubject: "git-pair: unready ",
			state:         "WORKING",
			// Withdrawing needs something to withdraw.
			arrange: func(t *testing.T, f *gittest.Fixture) { ready(t, f) },
		},
		{
			name:          "abandon",
			args:          []string{"change", "abandon"},
			markerSubject: "git-pair: abandon ",
			state:         "abandoned",
		},
		{
			name:          "integrate",
			args:          []string{"change", "integrate"},
			markerSubject: "git-pair: integrate ",
			state:         "INTEGRATING",
			arrange: func(t *testing.T, f *gittest.Fixture) {
				ready(t, f)
				submit(t, f, "approve")
			},
		},
	}
}

func TestAuthorLifecyclePoints(t *testing.T) {
	for _, tc := range authorPoints() {
		t.Run(tc.name, func(t *testing.T) {
			// The per-clone root, not the tracked one. A tracked hook is a commit, and installing one
			// after the approval moves the head `change integrate`'s gate compares — the gate would then
			// refuse for a reason that has nothing to do with the point under test. The tracked root and
			// its consent step are the subject of the pre-ready cases, which is where the difference
			// between the two roots lives.
			setup := func(t *testing.T) *gittest.Fixture {
				f, _ := newChangeset(t, "checkout-flow", "main")
				if tc.arrange != nil {
					tc.arrange(t, f)
				}
				return f
			}

			t.Run("a pre-hook refusal leaves no marker", func(t *testing.T) {
				f := setup(t)
				putHook(t, f, "clone", "pre-"+tc.name, "01-gate", failHook("no"))
				head := f.Head()

				res := runIn(t, f.Dir(), tc.args...)
				if res.code != exitRefusal {
					t.Fatalf("exit = %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
				}
				if f.Head() != head {
					t.Error("a refused transition wrote a commit")
				}
				mustContain(t, res.stderr, "01-gate", "the refusal must name the hook")
				mustContain(t, res.stderr, "pre-"+tc.name, "and the point that fired")
			})

			t.Run("a post-hook failure reports without reversing", func(t *testing.T) {
				f := setup(t)
				putHook(t, f, "clone", "post-"+tc.name, "01-notify", failHook("webhook refused"))

				res := runIn(t, f.Dir(), tc.args...)
				// Nonzero, because automation has to see it; and the marker is still there, because
				// git-pair does not unwind a transition. Both halves have to be true at once, and the
				// wording is what stops a reader concluding the wrong one from either half alone.
				if res.code != exitRefusal {
					t.Fatalf("exit = %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
				}
				if subject := f.Subject(f.Head()); !strings.HasPrefix(subject, tc.markerSubject) {
					t.Errorf("head is %q, want the %s marker to stand", subject, tc.name)
				}
				mustContain(t, res.stderr, tc.state, "the message must say the transition happened")
				mustContain(t, res.stderr, "nothing was undone", "and that nothing was reversed")
				mustContain(t, res.stderr, "01-notify", "and name the hook that failed")
				mustContain(t, res.stderr, "webhook refused", "with its own output")
			})

			t.Run("a post-hook that commits is noticed by name", func(t *testing.T) {
				f := setup(t)
				putHook(t, f, "clone", "post-"+tc.name, "01-version",
					"#!/bin/sh\ngit commit --allow-empty -q -m 'written by the hook'\nexit 0\n")

				res := runIn(t, f.Dir(), tc.args...).mustSucceed(t, tc.args...)

				// The marker is the head a review is anchored to, so a commit after it means the
				// changeset carries work the marker never described. A silent HEAD move is the
				// version of that a reader cannot see at all.
				mustContain(t, res.stderr, "moved HEAD", "a post-hook that committed has to be noticed")
				mustContain(t, res.stderr, "01-version", "named by hook")
				if subject := f.Subject(f.Head()); strings.HasPrefix(subject, tc.markerSubject) {
					t.Error("the hook did not commit, so this case proved nothing")
				}
			})

			t.Run("a post-hook failure does not silence the hooks after it", func(t *testing.T) {
				f := setup(t)
				dir := scratch(t)
				order := filepath.Join(dir, "ORDER")
				record := func(word string) string {
					return fmt.Sprintf("#!/bin/sh\necho %s >> %s\nexit 0\n", word, strconv.Quote(order))
				}
				putHook(t, f, "clone", "post-"+tc.name, "01-first", failHook("first failed"))
				putHook(t, f, "clone", "post-"+tc.name, "02-second", record("second"))
				putHook(t, f, "clone", "post-"+tc.name, "03-third", record("third"))

				runIn(t, f.Dir(), tc.args...)

				// On the way in, the first failure stops the rest — that is what a gate is for. On the
				// way out there is nothing to stop, and a notification hook should not be silenced
				// because the coverage hook before it failed.
				got := strings.Fields(strings.TrimSpace(readFile(t, order)))
				if len(got) != 2 || got[0] != "second" || got[1] != "third" {
					t.Errorf("ran = %v, want [second third]: every hook after a failure still runs", got)
				}
			})
		})
	}
}

// TestReadyJSONCarriesBothSides covers the machine-readable half of a transition: a pipeline reading the
// answer has to tell which of its own scripts produced what, on both sides of the marker.
func TestReadyJSONCarriesBothSides(t *testing.T) {
	f, _ := newChangeset(t, "checkout-flow", "main")
	putHook(t, f, "clone", "pre-ready", "01-suite", passHook)
	putHook(t, f, "clone", "post-ready", "01-notify", passHook)

	res := runIn(t, f.Dir(), "change", "ready", "--json").mustSucceed(t, "change", "ready")

	var points []string
	for _, e := range res.jsonList(t, "hooks") {
		entry := e.(map[string]any)
		points = append(points, fmt.Sprintf("%s/%s", entry["point"], entry["name"]))
	}
	want := "pre-ready/01-suite,post-ready/01-notify"
	if strings.Join(points, ",") != want {
		t.Errorf("hooks = %v, want [%s]: the array carries the transition in order, both sides", points, want)
	}
}

// TestPostHooksSeeTheMarkerAsHead is why the post-hook context is rendered again rather than reused. The head
// a post-hook is told about has to be the commit that now exists: a notification quoting the commit from
// before the marker describes a state the branch no longer has.
func TestPostHooksSeeTheMarkerAsHead(t *testing.T) {
	f, _ := newChangeset(t, "checkout-flow", "main")
	dir := scratch(t)
	envFile := strconv.Quote(filepath.Join(dir, "ENV"))
	putHook(t, f, "clone", "post-ready", "01-env", strings.Join([]string{
		"#!/bin/sh",
		"{",
		`printf 'point=%s\n' "$GIT_PAIR_HOOK_POINT"`,
		`printf 'head=%s\n' "$GIT_PAIR_HEAD"`,
		`} > ` + envFile,
		`exit 0`,
	}, "\n")+"\n")

	runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")

	env := readFile(t, filepath.Join(dir, "ENV"))
	if !strings.Contains(env, "point=post-ready") {
		t.Errorf("post-hook environment = %q, want the point it ran under", env)
	}
	if !strings.Contains(env, "head="+f.Head()) {
		t.Errorf("post-hook environment = %q, want head=%s: the marker this run wrote", env, f.Head())
	}
}

// TestNoopTransitionsRunNoHooks is the rule the points are named for: a hook fires when a lifecycle event
// happens, and a command that writes nothing is not an event. Each case is the second run of a command whose
// wanted state already holds.
//
// The hooks here go in the per-clone root rather than the tracked one. A tracked hook is committed, and the
// commit would move HEAD — which for `change integrate` is precisely what turns an idempotent re-run into a
// new declaration, and would make this test measure the wrong thing.
// TestTrustedHooksApplyToEveryPoint is the one place the tracked root is used outside `pre-ready`, and it is
// here because trust is a property of the root rather than of the point: a repository that committed a
// notify-on-withdrawal script gets the same consent rule for it as for its test suite, and the same silence
// until somebody reads it.
func TestTrustedHooksApplyToEveryPoint(t *testing.T) {
	f, _ := newChangeset(t, "checkout-flow", "main")
	dir := scratch(t)
	ready(t, f)
	putHook(t, f, "tracked", "post-unready", "01-notify", touch(dir, "RAN"))

	res := runIn(t, f.Dir(), "change", "unready").mustSucceed(t, "change", "unready")
	if _, err := os.Stat(filepath.Join(dir, "RAN")); err == nil {
		t.Error("a tracked hook at a point other than pre-ready ran without acceptance")
	}
	mustContain(t, res.stderr, "git pair hooks --trust", "and the run says how to accept it at that point too")

	trustHooks(t, f)
	runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")
	runIn(t, f.Dir(), "change", "unready").mustSucceed(t, "change", "unready")
	if _, err := os.Stat(filepath.Join(dir, "RAN")); err != nil {
		t.Error("the accepted tracked hook did not run at post-unready")
	}
}

func TestNoopTransitionsRunNoHooks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, f *gittest.Fixture)
		args  []string
		point string
	}{
		{
			name:  "unready on a changeset that was never offered",
			setup: func(t *testing.T, f *gittest.Fixture) {},
			args:  []string{"change", "unready"},
			point: "unready",
		},
		{
			name: "abandon on a changeset already abandoned",
			setup: func(t *testing.T, f *gittest.Fixture) {
				runIn(t, f.Dir(), "change", "abandon").mustSucceed(t, "change", "abandon")
			},
			args:  []string{"change", "abandon"},
			point: "abandon",
		},
		{
			name: "integrate on a head already declared",
			setup: func(t *testing.T, f *gittest.Fixture) {
				ready(t, f)
				submit(t, f, "approve")
				runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")
			},
			args:  []string{"change", "integrate"},
			point: "integrate",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := newChangeset(t, "checkout-flow", "main")
			tc.setup(t, f)
			dir := scratch(t)
			putHook(t, f, "clone", "pre-"+tc.point, "01-gate", touch(dir, "RAN_PRE"))
			putHook(t, f, "clone", "post-"+tc.point, "01-notify", touch(dir, "RAN_POST"))

			runIn(t, f.Dir(), tc.args...).mustSucceed(t, tc.args...)

			for _, name := range []string{"RAN_PRE", "RAN_POST"} {
				if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
					t.Errorf("%s ran on a transition that wrote nothing", name)
				}
			}
		})
	}
}

// TestTrackedRootAtTheIntegratePoint is the answer to "couldn't the hooks just be installed before
// `change integrate`?". They can, and the ordering is the whole of the question. A tracked hook is a commit,
// and the integrate gate compares the head the approval recorded with `HEAD`: installed after the approval the
// install is itself the reason the gate refuses, which says nothing about the point under test. Installed
// before the approval the hook is ordinary reviewed content, consent is recorded against the tree, and the
// point fires from the tracked root like any other. The table above uses the per-clone root for uniformity
// across the four points; this is the case that keeps the tracked root honest at the point the table cannot
// install it at.
func TestTrackedRootAtTheIntegratePoint(t *testing.T) {
	t.Run("installed before the approval, consent still decides whether it runs", func(t *testing.T) {
		dir := scratch(t)
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-integrate", "01-suite", touch(dir, "RAN"))
		ready(t, f)
		submit(t, f, "approve")

		res := runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")

		if _, err := os.Stat(filepath.Join(dir, "RAN")); err == nil {
			t.Error("an unaccepted tracked hook ran at integrate")
		}
		mustContain(t, res.stderr, "did not run", "the run must say the hook was skipped")
		mustContain(t, res.stderr, "git pair hooks --trust", "and name the way to accept it")
	})

	t.Run("accepted, it blocks the declaration", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-integrate", "01-suite", failHook("suite red"))
		ready(t, f)
		submit(t, f, "approve")
		trustHooks(t, f)
		head := f.Head()

		res := runIn(t, f.Dir(), "change", "integrate")

		if res.code != exitRefusal {
			t.Fatalf("exit = %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
		}
		if f.Head() != head {
			t.Error("a refused declaration wrote a marker")
		}
		mustContain(t, res.stderr, "01-suite", "the refusal must name the hook")
		mustContain(t, res.stderr, "suite red", "with its own output")
	})

	t.Run("installed after the approval, the install is what the gate refuses", func(t *testing.T) {
		dir := scratch(t)
		f, _ := newChangeset(t, "checkout-flow", "main")
		ready(t, f)
		submit(t, f, "approve")
		putHook(t, f, "tracked", "pre-integrate", "01-suite", touch(dir, "RAN"))
		trustHooks(t, f)

		res := runIn(t, f.Dir(), "change", "integrate")

		// The refusal is the ordering's cost, and it is not about the head moving: the hook file is content
		// the approval never covered, and the declaration gate says so by name. This is the message a test of
		// the point would have to explain away, which is why the table installs at the per-clone root.
		if res.code != exitRefusal {
			t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", res.code, exitRefusal, res.stdout, res.stderr)
		}
		mustContain(t, res.stdout+res.stderr, "content outside", "the refusal must be about the install, not the hook")
		mustContain(t, res.stdout+res.stderr, ".git-pair/hooks/pre-integrate/01-suite",
			"and name the file the reviewer never saw")
		if _, err := os.Stat(filepath.Join(dir, "RAN")); err == nil {
			t.Error("the hook ran although the command never reached the point")
		}
	})
}
