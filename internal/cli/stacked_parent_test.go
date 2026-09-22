package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The stacked rule, from PRD §21 and the requirements' "Initial conservative stacked-approval rule":
// any change to the parent branch after a child is approved invalidates the child's approval. The
// child cannot see any of it in its own history — its commits are untouched, so the drift test passes
// and the rebase test passes — which is why the approval records the parent's tip beside its own head
// and why `check` compares the two.
//
// The reason names the *kind* of movement on purpose (plan risk R3). A rule that reports "the parent
// moved" for every shape of parent activity reads as arbitrary, and an author who thinks the gate is
// being pedantic starts passing --allow-feedback or ignoring the gate.

// stackedPair builds a parent changeset `booking` on main and a child `booking-tests` stacked on it,
// both marked ready and approved. It returns the fixture with the child checked out.
func stackedPair(t *testing.T) (*gittest.Fixture, string, string) {
	t.Helper()
	f, parent := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "approve")

	f.CreateBranch("booking-tests")
	// The child carries the parent's directory — it was branched off it — and its own, and names
	// the parent in both of its halves: the branch to measure against, and the changeset that
	// survives the branch.
	f.Commit("stack on booking", gittest.WithFiles(map[string]string{
		"changesets/booking-tests/CHANGESET.yaml": "id: booking-tests\nparent: booking\nparent-changeset: booking\n",
		"changesets/booking-tests/ABOUT.md":       "# booking-tests\n\nTests for the booking transaction.\n",
		"booking_test.go":                         "package main\n\nfunc TestBooking() {}\n",
	}))
	ready(t, f)
	submit(t, f, "approve")
	return f, parent, "booking-tests"
}

// parentMoves is the set of things that can happen to a parent branch between the child's approval
// and the gate. Each one ends the child's approval, and each one has to be named differently.
func TestAnyParentMovementEndsTheChildsApproval(t *testing.T) {
	tests := []struct {
		name string
		on   func(t *testing.T, f *gittest.Fixture)
		want string
	}{
		{
			name: "an implementation commit",
			on: func(t *testing.T, f *gittest.Fixture) {
				f.Commit("book it properly", gittest.WithFile("service.go",
					"package main\n\nfunc Lock() { mu.Lock() }\n"))
			},
			want: "an implementation commit",
		},
		{
			name: "a review submission that is not an approval",
			on: func(t *testing.T, f *gittest.Fixture) {
				f.CommitReviewMarker("booking", "feedback")
			},
			want: "a review commit",
		},
		{
			name: "an approval",
			on: func(t *testing.T, f *gittest.Fixture) {
				f.CommitReviewMarker("booking", "approve")
			},
			want: "an approval",
		},
		{
			// A merge adds commits and rewrites none, which is what separates it from the rebase
			// below; the newest commit is the merge itself, and that is the fact worth naming.
			name: "a merge into the parent",
			on: func(t *testing.T, f *gittest.Fixture) {
				f.CreateBranch("side-work")
				f.Commit("someone else's work", gittest.WithFile("other.go", "package main\n"))
				f.SwitchTo("booking")
				f.MustGit("merge", "--no-ff", "--no-edit", "side-work")
			},
			want: "a merge",
		},
		{
			// The parent's history no longer contains the tip the approval recorded. Note that the
			// child's own rebase rule would say nothing here — it only looks at the child's branch.
			name: "a rebase or force-push",
			on: func(t *testing.T, f *gittest.Fixture) {
				f.MustGit("reset", "--hard", "main")
				f.Commit("redone on a new base", gittest.WithFile("booking.go", "package main\n"))
			},
			want: "it was rewritten",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, _, _ := stackedPair(t)
			if res := runIn(t, f.Dir(), "check"); res.code != 0 {
				t.Fatalf("the child should pass before the parent moves\n%s%s", res.stdout, res.stderr)
			}
			f.SwitchTo("booking")
			tc.on(t, f)
			f.SwitchTo("booking-tests")

			res := runIn(t, f.Dir(), "check")
			if res.code != 1 {
				t.Fatalf("check exited %d, want 1\n%s%s", res.code, res.stdout, res.stderr)
			}
			mustContain(t, res.stdout, "the parent branch booking moved", "the stack reason")
			mustContain(t, res.stdout, tc.want, "the kind of movement")
			mustContain(t, res.stdout, "rebase onto booking and have the result reviewed again", "the way forward")
		})
	}
}

// An unchanged parent must not cost the child anything. Without this the table above proves nothing:
// a rule that always fires passes every row of it.
func TestUnchangedParentLeavesTheChildApproved(t *testing.T) {
	f, _, child := stackedPair(t)
	res := runIn(t, f.Dir(), "check").mustSucceed(t)
	mustContain(t, res.stdout, "OK: "+child+" is integration-ready", "the verdict")
	mustNotContain(t, res.stdout, "the parent branch", "no stack complaint")

	out := runIn(t, f.Dir(), "status", "--json").mustSucceed(t).json(t)
	if p, ok := out["parent"].(map[string]any); !ok || p["branch"] != "booking" {
		t.Fatalf("status --json parent = %v, want the stack to be reported", out["parent"])
	}
}

// The durable half of the stack: once the parent has landed and its branch is gone, the integration
// ref is what the relationship means. The child stays measurable — its diff is against the commit the
// parent's work became — and `check` says where the parent went rather than failing to resolve a
// branch that no longer exists.
func TestChildOfALandedParentIsToldWhereTheWorkWent(t *testing.T) {
	f, _, _ := stackedPair(t)

	f.SwitchTo("main")
	parentSHA := f.RefSHA("booking")
	f.MustGit("merge", "--squash", "booking")
	f.MustGit("commit", "-m", "landed booking")
	landing := f.RevParse("HEAD")
	runIn(t, f.Dir(), "integration", "record", "--commit", "HEAD", "--source", parentSHA).mustSucceed(t)
	f.ForceDeleteBranch("booking")

	f.SwitchTo("booking-tests")
	res := runIn(t, f.Dir(), "check")
	if res.code != 1 {
		t.Fatalf("check exited %d, want 1\n%s%s", res.code, res.stdout, res.stderr)
	}
	mustContain(t, res.stdout, "the parent booking landed as "+shortOf(landing),
		"the landing named")
	mustContain(t, res.stdout, "rebase onto main", "the destination named")

	// The stack is reported even though the branch is gone: the child has to be told the parent
	// left, not left to wonder why nothing answers.
	st := runIn(t, f.Dir(), "status").mustSucceed(t)
	mustContain(t, st.stdout, "Stack:", "the stack section")
	mustContain(t, st.stdout, "the branch is gone", "the parent's absence")
}

// An abandoned parent is not a moving target — it will never carry the work the child was stacked on —
// so the child needs a new base rather than a rebase, and the gate refuses rather than advising a
// rebase onto a dead branch.
func TestChildOfAnAbandonedParentIsUnreconciled(t *testing.T) {
	f, _, _ := stackedPair(t)
	f.SwitchTo("booking")
	runIn(t, f.Dir(), "change", "abandon").mustSucceed(t, "change", "abandon")
	f.SwitchTo("booking-tests")

	res := runIn(t, f.Dir(), "check")
	if res.code != 1 {
		t.Fatalf("check exited %d, want 1\n%s%s", res.code, res.stdout, res.stderr)
	}
	mustContain(t, res.stdout, "abandoned", "the parent's state")
	mustContain(t, res.stdout, "unreconciled", "what the stack needs")
	mustContain(t, res.stdout, "--set-parent", "the command that decides")
}

// A stack is the author's decision, so changing one takes an explicit act. The abandoned parent above
// leaves the child pointing at a branch with no future; `init --parent` restacks it, and refuses to
// do so while the old value differs and no override flag was given.
func TestRestackingTakesAnExplicitFlag(t *testing.T) {
	f, _, _ := stackedPair(t)
	f.SwitchTo("main")
	f.CreateBranch("rebased-onto")
	f.Commit("a fresh base", gittest.WithFile("base.go", "package main\n"))
	runIn(t, f.Dir(), "init", "--base", "main", "--about", "the new base").mustSucceed(t)
	f.SwitchTo("booking-tests")

	res := runIn(t, f.Dir(), "init", "--parent", "rebased-onto")
	if res.code != exitUsage {
		t.Fatalf("init --parent exited %d, want %d\n%s%s", res.code, exitUsage, res.stdout, res.stderr)
	}
	mustContain(t, res.stdout+res.stderr, "measured against", "the conflict it reports")

	runIn(t, f.Dir(), "init", "--parent", "rebased-onto", "--set-parent").mustSucceed(t)
	md := f.Read("changesets/booking-tests/CHANGESET.yaml")
	if !strings.Contains(md, "parent: rebased-onto") || strings.Contains(md, "parent: booking\n") {
		t.Fatalf("CHANGESET.yaml after restacking:\n%s", md)
	}
}

// The metadata rules: `parent:` is the base, both keys at once is a file that means two things, and
// the parent's own changeset is discovered rather than guessed.
func TestInitParentWritesTheStackOnce(t *testing.T) {
	f, parent := newChangeset(t, "booking", "main")
	f.CreateBranch("booking-tests")

	res := runIn(t, f.Dir(), "init", "--parent", "booking")
	if res.code != 0 {
		t.Fatalf("init --parent exited %d\n%s%s", res.code, res.stdout, res.stderr)
	}
	md := f.Read("changesets/booking-tests/CHANGESET.yaml")
	for _, want := range []string{"id: booking-tests", "parent: booking", "parent-changeset: " + parent} {
		if !strings.Contains(md, want) {
			t.Fatalf("CHANGESET.yaml is missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "base:") {
		t.Fatalf("`parent:` is the base, so no `base:` belongs in:\n%s", md)
	}
}

func TestInitParentRefusesTheShapesThatCannotMeanAnything(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	f.CreateBranch("booking-tests")
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{"with --base", []string{"init", "--parent", "booking", "--base", "main"}, "same thing twice"},
		{"the trunk", []string{"init", "--parent", "main"}, "not a stack"},
		{"its own branch", []string{"init", "--parent", "booking-tests"}, "the branch this changeset lives on"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := runIn(t, f.Dir(), tc.argv...)
			if res.code != exitUsage {
				t.Fatalf("init %v exited %d, want %d\n%s%s", tc.argv, res.code, exitUsage, res.stdout, res.stderr)
			}
			mustContain(t, res.stdout+res.stderr, tc.want, "the refusal")
		})
	}
}

// A file naming both keys answers "what does this diff against?" twice, and the two answers will not
// stay in agreement. It is refused the way an inconsistent `id:` is, rather than one key winning.
func TestNamingBothParentAndBaseIsRefused(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	f.Commit("a stack with two bases", gittest.WithFile("changesets/booking/CHANGESET.yaml",
		"id: booking\nbase: main\nparent: main\n"))
	res := runIn(t, f.Dir(), "status")
	if res.code == 0 {
		t.Fatalf("status accepted a changeset naming both keys\n%s", res.stdout)
	}
	mustContain(t, res.stdout+res.stderr, "both base and parent", "the refusal")
}
