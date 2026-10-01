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

// changedFromSpan names the files the way every caller measures them. No surface hands `Base.Ref` to
// `git diff` on its own: `span.Resolve` takes the merge base of the base and the head first
// (`internal/span/span.go`), which is what lets rule 2 name the destination and still measure only the
// child's own work. The tests follow the callers rather than the shorter path, so a base the child sits
// *behind* cannot pass by being diffed against the head as though it were in front of it.
func changedFromSpan(t *testing.T, f *gittest.Fixture, base, head string) string {
	t.Helper()
	mb := strings.TrimSpace(f.MustGit("merge-base", base, head))
	if mb == "" {
		t.Fatalf("no merge base between %s and %s", short(base), short(head))
	}
	return changedBetween(t, f, mb, head)
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
		ParentBranch: "alpha", BaseChangeset: "alpha"}
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
// branch is no longer where the child is measured. The destination is named, because that is the answer a
// reader can use, and the measurement still starts at the merge base — which is what the diff below proves
// rather than what the ref asserts.
func TestBaseOfAStackWhoseParentLanded(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	_, child, head := stacked(t, f)
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "-m", "alpha: merge the branch", "alpha")
	f.Commit("main: unrelated work after the landing", gittest.WithFile("other.txt", "1\n"))

	b := baseOf(t, f, child, head)
	if want := db().Ref; b.Ref != want {
		t.Errorf("base = %s, want the destination %s: a commit the reader cannot name is not an explanation", short(b.Ref), want)
	}
	if !b.Derived {
		t.Error("derived = false for a base derived from the destination")
	}
	if !b.ParentLanded {
		t.Error("parent landed = false, and the destination carries changeset alpha's directory: the base's name no longer says it, so the fact has to travel")
	}
	if !strings.Contains(b.Why, "landed") {
		t.Errorf("why = %q, want the rule that says the parent landed", b.Why)
	}
	// The point of the rule: trunk's later work is not the child's diff.
	changed := changedFromSpan(t, f, b.Ref, head)
	if strings.Contains(changed, "other.txt") {
		t.Errorf("the child's diff against its base includes the destination's later work: %s", changed)
	}
	if !strings.Contains(changed, "beta.txt") {
		t.Errorf("the child's diff does not include its own work: %s", changed)
	}
}

// Rule 2, the gone branch: the same rule answers, and the sentence says which half is missing, because "the
// parent is gone" and "the parent landed" are different things for a reader to act on. The base is the same
// name in both cases, so `Why` and `ParentLanded` are what tell them apart.
func TestBaseOfAStackWhoseParentBranchIsGone(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	_, child, head := stacked(t, f)
	f.SwitchTo("main")
	f.ForceDeleteBranch("alpha")

	b := baseOf(t, f, child, head)
	if !b.Derived {
		t.Fatalf("base = %q derived=%v, want a base derived from the destination once the branch is gone", b.Ref, b.Derived)
	}
	if b.Ref != db().Ref {
		t.Errorf("base = %s, want the destination (%s)", short(b.Ref), db().Ref)
	}
	if !strings.Contains(b.Why, "gone") {
		t.Errorf("why = %q, want the sentence that names the missing branch", b.Why)
	}
	if b.ParentLanded {
		t.Error("parent landed = true, and this parent never reached the destination: the branch being gone is not the work having landed")
	}
	// The measurement starts at the fork, which is where the child's own work — and the parent's, which
	// never landed — begins. Naming the destination does not move it.
	if changed := changedFromSpan(t, f, b.Ref, head); !strings.Contains(changed, "alpha.txt") {
		t.Errorf("the child's diff lost the unlanded parent's work, which is still the child's to review: %s", changed)
	}
}

// The case a durable ref got wrong. The parent landed, the child was rebased onto the destination, and the
// destination moved again afterwards. Naming a fixed commit as the base would widen the child's diff to
// everything trunk gained since; the measurement starts at the fork, wherever the base's name points.
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
	changed := changedFromSpan(t, f, b.Ref, head)
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
	if b.ParentLanded {
		t.Error("parent landed = true for a stack this clone could not resolve at all")
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

// fetched is the integration branch as `git clone` records it and `DefaultBranch` prefers it: the same
// branch, reached through the fetch root instead of through a branch of this clone.
func fetched() changeset.DefaultBranchRef {
	return changeset.DefaultBranchRef{Ref: "refs/remotes/origin/main", Source: changeset.DefaultBranchRemoteHead}
}

func baseWith(t *testing.T, f *gittest.Fixture, db changeset.DefaultBranchRef, cs changeset.Changeset,
	head string) changeset.Base {
	t.Helper()
	b, err := changeset.BaseFor(context.Background(), repo(f), cs, head, db)
	if err != nil {
		t.Fatalf("BaseFor(%s): %v", cs.Slug, err)
	}
	return b
}

// trunkBased is an unstacked changeset measured against the integration branch — the shape `init` records
// when nothing was named, and the shape every first changeset of a stack has.
func trunkBased(t *testing.T, f *gittest.Fixture) (changeset.Changeset, string) {
	t.Helper()
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("booking: the work", gittest.WithFile("booking.txt", "1\n"))
	return changeset.Changeset{Slug: "booking", Branch: "booking", Base: "main"}, f.RevParse("booking")
}

// trunkMovesOnRemote leaves this clone holding a trunk behind its fetched copy, which is the ordinary state
// of a person who fetches and never updates their local trunk: `git fetch` moves the fetch ref and does not
// touch `refs/heads/main`.
func trunkMovesOnRemote(t *testing.T, f *gittest.Fixture, subject, file string) string {
	t.Helper()
	f.SwitchTo("main")
	f.CreateBranch("fetched")
	there := f.Commit(subject, gittest.WithFile(file, "1\n"))
	f.MustGit("update-ref", "refs/remotes/origin/main", there)
	f.SwitchTo("main")
	f.ForceDeleteBranch("fetched")
	return there
}

// The base names the integration branch, and the name resolves under `refs/heads/` first. That branch is
// where trunk stood the day this one was cut; the fetched copy is where trunk is. Measuring against the
// local copy after a rebase onto the fetched trunk puts the destination's merged work in this changeset's
// diff, which is the report this rule answers: `git pair review` showing a diff built on a trunk everyone
// else has moved past.
func TestBaseOfATrunkBasedChangesetIsTheFetchedCopy(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	cs, _ := trunkBased(t, f)
	trunkMovesOnRemote(t, f, "main: someone else's merged work", "theirs.txt")

	f.SwitchTo("booking")
	f.MustGit("rebase", "refs/remotes/origin/main")
	head := f.RevParse("booking")

	b := baseWith(t, f, fetched(), cs, head)
	if b.Ref != "refs/remotes/origin/main" {
		t.Errorf("base = %q, want the fetched copy of the integration branch", b.Ref)
	}
	if b.Derived {
		t.Error("derived = true for a base that is a branch this clone has")
	}
	if !strings.Contains(b.Why, "fetched") {
		t.Errorf("why = %q, want the sentence that names the copy it chose", b.Why)
	}
	changed := changedBetween(t, f, b.Ref, head)
	if strings.Contains(changed, "theirs.txt") {
		t.Errorf("the diff measured from %s carries work the destination already has: %s", short(b.Ref), changed)
	}
	if !strings.Contains(changed, "booking.txt") {
		t.Errorf("the diff lost the changeset's own work: %s", changed)
	}
}

// The same repository read the other way, to keep the rule honest about what it changes: with a local trunk
// and no fetch ref, the recorded name is the whole answer, and it is the answer that widens the diff.
func TestBaseOfATrunkBasedChangesetIsTheRecordedNameWithoutAFetchRef(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	cs, head := trunkBased(t, f)

	b := baseWith(t, f, db(), cs, head)
	if b.Ref != "main" || b.Derived || !strings.Contains(b.Why, "recorded") {
		t.Errorf("base = %+v, want the recorded name and no derivation where there is no fetched copy", b)
	}
}

// A stack's parent is not the integration branch, and the fetched trunk does not replace it. The parent's
// work is still being written on a branch of this clone, where it is ahead of anything pushed.
func TestBaseOfAStackKeepsItsParentBranchWhenTrunkIsFetched(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	_, child, head := stacked(t, f)
	trunkMovesOnRemote(t, f, "main: work below the parent", "theirs.txt")

	b := baseWith(t, f, fetched(), child, head)
	if b.Ref != "alpha" {
		t.Errorf("base = %q, want the parent branch: a fetched trunk says nothing about where the parent's work is", b.Ref)
	}
	if !strings.Contains(b.Why, "parent branch") {
		t.Errorf("why = %q, want the rule that named the branch", b.Why)
	}
}

// Measure is the answer for a surface that prints the rule beside the base and has no error path to put a
// failure in: the review screen's box. It fills the head it is not given, which rule 2 needs, and it keeps
// the fact the base's name no longer carries once the parent has landed.
func TestMeasureCarriesTheRuleAndCannotFail(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	_, child, head := stacked(t, f)
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "-m", "alpha: merge the branch", "alpha")
	f.SwitchTo("beta")

	// No head passed: `Measure` reads the checkout's, which is the child's, and answers what BaseFor answers
	// for it.
	if got, want := changeset.Measure(context.Background(), repo(f), child, db(), ""), baseOf(t, f, child, head); got.Ref != want.Ref ||
		!got.ParentLanded || !got.Derived {
		t.Errorf("Measure = %+v, want BaseFor's answer %+v with the head read for the caller", got, want)
	}
	// No destination to derive from: the recorded base is what this clone can answer with, and the screen
	// shows that rather than refusing to draw.
	if got := changeset.Measure(context.Background(), repo(f), child, changeset.DefaultBranchRef{}, head); got.Ref != "alpha" {
		t.Errorf("Measure without a destination = %q, want the recorded base", got.Ref)
	}
}

// MeasureBase is the answer for a caller that pins a commit and has nowhere to put the rule behind it. It
// agrees with BaseFor where BaseFor answers, and falls back to the base as recorded where it cannot.
func TestMeasureBaseAgreesWithBaseForAndFallsBack(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	cs, _ := trunkBased(t, f)
	trunkMovesOnRemote(t, f, "main: someone else's merged work", "theirs.txt")

	f.SwitchTo("booking")
	f.MustGit("rebase", "refs/remotes/origin/main")
	head := f.RevParse("booking")

	if got, want := changeset.MeasureBase(context.Background(), repo(f), cs, fetched(), head), baseWith(t, f, fetched(), cs, head).Ref; got != want {
		t.Errorf("MeasureBase = %q, want BaseFor's ref %q", got, want)
	}
	// No integration branch to name: the recorded base is what this clone can answer with, and it is what
	// every one of these surfaces measured against before the base was a rule rather than a string.
	if got := changeset.MeasureBase(context.Background(), repo(f), cs, changeset.DefaultBranchRef{}, head); got != "main" {
		t.Errorf("MeasureBase without a destination = %q, want the recorded base", got)
	}
	// A caller that does not hold a head gets one read for it, and the same answer.
	if got := changeset.MeasureBase(context.Background(), repo(f), cs, fetched(), ""); got != "refs/remotes/origin/main" {
		t.Errorf("MeasureBase with no head = %q, want the fetched copy", got)
	}
}
