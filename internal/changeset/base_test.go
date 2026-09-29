package changeset_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/gittest"
)

// The base is the one number every surface of a stack depends on, and it has three honest answers. These
// fixtures are one answer each, plus the two landing shapes and the case a durable ref got wrong: a stack
// whose parent landed and whose child was then rebased.

func db() changeset.DefaultBranchRef {
	return changeset.DefaultBranchRef{Ref: "refs/heads/main", Source: changeset.DefaultBranchSoleCandidate}
}

// changedBetween names the files a span touches, which is how these tests check what a base *means* rather
// than only what string it is.
func changedBetween(t *testing.T, f *gittest.Fixture, base, head string) string {
	t.Helper()
	out, err := f.Git("diff", "--name-only", base, head)
	if err != nil {
		t.Fatalf("diff %s %s: %v", short(base), short(head), err)
	}
	return strings.TrimSpace(out)
}

func baseOf(t *testing.T, f *gittest.Fixture, cs changeset.Changeset, head string) changeset.Base {
	t.Helper()
	b, err := changeset.BaseFor(context.Background(), repo(f), cs, head, db())
	if err != nil {
		t.Fatalf("BaseFor(%s): %v", cs.Slug, err)
	}
	return b
}

// stacked is the shape every one of these fixtures starts from: a parent changeset on its own branch, and a
// child built on top of it.
func stacked(t *testing.T, f *gittest.Fixture) (changeset.Changeset, changeset.Changeset, string) {
	t.Helper()
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha: the work", gittest.WithFile("alpha.txt", "1\n"))
	f.CreateBranch("beta")
	f.CommitChangeset("beta", "alpha")
	f.Commit("beta: the work", gittest.WithFile("beta.txt", "1\n"))
	parent := changeset.Changeset{Slug: "alpha", Branch: "alpha", Base: "main"}
	child := changeset.Changeset{Slug: "beta", Branch: "beta", Base: "alpha",
		ParentBranch: "alpha", ParentChangeset: "alpha"}
	return parent, child, f.RevParse("beta")
}

// Rule 1. The parent branch is where the work below the child is still being written, so it is the base
// while it stands, and no derivation runs.
func TestBaseOfAStackOnALiveParentBranch(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	_, child, head := stacked(t, f)

	b := baseOf(t, f, child, head)
	if b.Ref != "alpha" || b.Derived {
		t.Errorf("base = %q (derived %v), want the parent branch itself", b.Ref, b.Derived)
	}
	if !strings.Contains(b.Why, "parent branch") {
		t.Errorf("why = %q, want the rule that named the branch", b.Why)
	}
	if b.ParentBranch != "alpha" {
		t.Errorf("parent branch = %q; the stack relationship is reported beside the base, not replaced by it", b.ParentBranch)
	}
}

// Rule 2, the merge landing. The parent's branch is still there and its work is in the destination, so the
// branch is no longer where the child is measured: the merge base is.
func TestBaseOfAStackWhoseParentLanded(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	_, child, head := stacked(t, f)
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "-m", "alpha: merge the branch", "alpha")
	later := f.Commit("main: unrelated work after the landing", gittest.WithFile("other.txt", "1\n"))

	b := baseOf(t, f, child, head)
	if want := f.RevParse("alpha"); b.Ref != want {
		t.Errorf("base = %s, want the parent's tip %s (where the child's own work starts)", short(b.Ref), short(want))
	}
	if !b.Derived {
		t.Error("derived = false for a base derived from the destination")
	}
	if !strings.Contains(b.Why, "landed") {
		t.Errorf("why = %q, want the rule that says the parent landed", b.Why)
	}
	// The point of the rule: trunk's later work is not the child's diff.
	changed := changedBetween(t, f, b.Ref, head)
	if strings.Contains(changed, "other.txt") {
		t.Errorf("the child's diff against its base includes the destination's later work: %s", changed)
	}
	if !strings.Contains(changed, "beta.txt") {
		t.Errorf("the child's diff does not include its own work: %s", changed)
	}
	_ = later
}

// Rule 2, the linear landing and the gone branch: the same rule answers, and the sentence says which half
// is missing, because "the parent is gone" and "the parent landed" are different things for a reader to act
// on.
func TestBaseOfAStackWhoseParentBranchIsGone(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	_, child, head := stacked(t, f)
	f.SwitchTo("main")
	f.ForceDeleteBranch("alpha")

	b := baseOf(t, f, child, head)
	if !b.Derived {
		t.Fatalf("base = %q derived=%v, want a derived commit once the branch is gone", b.Ref, b.Derived)
	}
	if !strings.Contains(b.Why, "gone") {
		t.Errorf("why = %q, want the sentence that names the missing branch", b.Why)
	}
	if b.Ref != f.RevParse("main") {
		t.Errorf("base = %s, want the shared run with the destination (%s)", short(b.Ref), short(f.RevParse("main")))
	}
}

// The case a durable ref got wrong. The parent landed, the child was rebased onto the destination, and the
// destination moved again afterwards. Naming the parent's landing commit as the base would widen the child's
// diff to everything trunk gained since; the merge base is where the child's own work starts.
func TestBaseOfARebasedStackIsItsForkFromTheDestination(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	_, child, _ := stacked(t, f)
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "-m", "alpha: merge the branch", "alpha")

	f.SwitchTo("beta")
	f.MustGit("rebase", "main")
	head := f.Head()
	f.SwitchTo("main")
	f.Commit("main: work the child must not own", gittest.WithFile("trunk-only.txt", "1\n"))

	b := baseOf(t, f, child, head)
	changed := changedBetween(t, f, b.Ref, head)
	if strings.Contains(changed, "trunk-only.txt") {
		t.Errorf("a rebased child's diff carries trunk's later work (base %s): %s", short(b.Ref), changed)
	}
	if !strings.Contains(changed, "beta.txt") {
		t.Errorf("a rebased child's diff lost its own work: %s", changed)
	}
	// The parent's file is not in the span either, and that is the rule working rather than losing the
	// child's history: the fork point is past the landing, so the parent's content is in the destination
	// below the child rather than in the child's diff.
	if strings.Contains(changed, "alpha.txt") {
		t.Errorf("a rebased child's diff carries the parent's landed work: %s", changed)
	}
}

// Rule 3, and the answer when there is no destination to name. A base of "" is not usable by any caller, so
// the fallback is a real ref with a sentence attached, and the only way out is the error that says the
// question cannot be asked.
func TestBaseFallsBackToTheIntegrationBranch(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	_, child, _ := stacked(t, f)
	f.SwitchTo("main")
	f.ForceDeleteBranch("alpha")

	b := baseOf(t, f, child, "")
	if b.Ref != "refs/heads/main" || !b.Derived {
		t.Errorf("base = %q derived=%v, want the integration branch as the fallback", b.Ref, b.Derived)
	}
	if !strings.Contains(b.Why, "integration branch") {
		t.Errorf("why = %q, want the fallback named rather than the value printed bare", b.Why)
	}

	noDB := changeset.DefaultBranchRef{}
	if _, err := changeset.BaseFor(context.Background(), repo(f), child, "", noDB); !errors.Is(err, changeset.ErrNoDefaultBranch) {
		t.Errorf("err = %v, want ErrNoDefaultBranch: no destination means the question cannot be asked", err)
	}
}

// An unstacked changeset's recorded base is the answer. Deriving one from the destination would second-guess
// a `base:` the author wrote on purpose — including a deliberate `base: release/2.x`.
func TestBaseOfAnUnstackedChangesetIsWhatItRecorded(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("release/2.x")
	f.SwitchTo("main")
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "release/2.x")
	cs := changeset.Changeset{Slug: "booking", Branch: "booking", Base: "release/2.x"}

	b := baseOf(t, f, cs, f.RevParse("booking"))
	if b.Ref != "release/2.x" || b.Derived || !strings.Contains(b.Why, "recorded") {
		t.Errorf("base = %+v, want the recorded base and no derivation", b)
	}
}
