package reviewref_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gitpair/internal/git"
	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
)

func repo(f *gittest.Fixture) *git.Repo { return &git.Repo{Dir: f.Dir()} }

// The two ref spellings are the whole durable surface of git-pair, and other tooling (notifications,
// agents, the README contract, CI fetch lines) builds on the exact strings. The id names the refs,
// never the branch, which is what keeps the work findable after the branch is gone.
//
// Both are flat leaves under their own family directory. Nothing is nested under a per-changeset
// namespace any more, which is what lets a changeset id be a single path component: the old layout
// needed `<id>/<child>` because a ref cannot be both a leaf and a namespace, and that constraint was
// the only reason for the nesting.
func TestRefNaming(t *testing.T) {
	if got, want := reviewref.NamespaceRoot, "refs/git-pair"; got != want {
		t.Errorf("NamespaceRoot = %q, want %q", got, want)
	}
	if got, want := reviewref.Archive("booking-transaction"), "refs/git-pair/archive/booking-transaction"; got != want {
		t.Errorf("Archive = %q, want %q", got, want)
	}
	if got, want := reviewref.Integration("booking-transaction"), "refs/git-pair/integrations/booking-transaction"; got != want {
		t.Errorf("Integration = %q, want %q", got, want)
	}
	// Flat, both of them: the id is the whole last component of the path.
	for _, ref := range []string{reviewref.Archive("booking"), reviewref.Integration("booking")} {
		if strings.Count(strings.TrimPrefix(ref, reviewref.NamespaceRoot+"/"), "/") != 1 {
			t.Errorf("%s is not a flat <family>/<id> path", ref)
		}
	}
}

// List reads every durable family in one pass, and tells them apart, because a caller asking "which of
// these landed?" needs the integration refs and a caller asking "where is the chain?" needs the archive
// refs, and neither should pay for a second `for-each-ref` to find out.
//
// The retired layout is reported too, under kinds of its own. `Taken` refuses to let it reserve a name,
// but a command asking whether a landing was written down has to answer about history this code did not
// write, and a legacy record is a record.
func TestListReportsBothFamiliesAndTheRetiredOnes(t *testing.T) {
	f := gittest.New(t)
	head := f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	ctx := context.Background()

	if _, err := reviewref.CreatePair(ctx, repo(f), reviewref.Pair{ID: "booking", Archive: head, Integration: head}); err != nil {
		t.Fatalf("CreatePair: %v", err)
	}
	if _, err := reviewref.CreateOnly(ctx, repo(f), reviewref.Integration("other"), head); err != nil {
		t.Fatalf("CreateOnly: %v", err)
	}
	// The retired layout, spelled as the pre-two-ref code wrote it.
	f.MustGit("update-ref", "refs/git-pair/changesets/legacy/archive", head)
	f.MustGit("update-ref", "refs/git-pair/changesets/legacy/integration", head)
	// And refs under the namespace that belong to no family at all — a stray someone left, a nested
	// path in a family that has no nesting. Reporting one would attribute a changeset that does not
	// exist to someone's work.
	f.MustGit("update-ref", "refs/git-pair/stray", head)
	f.MustGit("update-ref", "refs/git-pair/archive/nested/notes", head)
	f.MustGit("update-ref", "refs/git-pair/changesets/legacy/other", head)
	f.MustGit("update-ref", "refs/git-pair/changesets/legacy/nested/deep", head)

	entries, err := reviewref.List(ctx, repo(f))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]reviewref.Kind{}
	for _, e := range entries {
		got[e.Ref] = e.Kind
	}
	for ref, want := range map[string]reviewref.Kind{
		"refs/git-pair/archive/booking":               reviewref.KindArchive,
		"refs/git-pair/integrations/booking":          reviewref.KindIntegration,
		"refs/git-pair/integrations/other":            reviewref.KindIntegration,
		"refs/git-pair/changesets/legacy/archive":     reviewref.KindLegacyArchive,
		"refs/git-pair/changesets/legacy/integration": reviewref.KindLegacyIntegration,
	} {
		if got[ref] != want {
			t.Errorf("List reported %s as %q, want %q (entries: %+v)", ref, got[ref], want, entries)
		}
	}
	for _, ref := range []string{
		"refs/git-pair/changesets/legacy/other",
		"refs/git-pair/changesets/legacy/nested/deep",
		"refs/git-pair/stray",
		"refs/git-pair/archive/nested/notes",
	} {
		if _, ok := got[ref]; ok {
			t.Errorf("List reported %s, which is neither family", ref)
		}
	}
	for _, e := range entries {
		if e.SHA != head {
			t.Errorf("%s = %+v, want it to point at %s", e.Ref, e, head)
		}
	}
}

// `init` refuses an id either family already holds. Matching is on the two exact paths, so
// `booking-v2` is free while `booking` is taken — and a legacy nested ref no longer reserves the
// name, because its id is not readable as a component of either family.
func TestTakenSeesEitherFamily(t *testing.T) {
	f := gittest.New(t)
	head := f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	ctx := context.Background()

	f.MustGit("update-ref", reviewref.Integration("booking"), head)
	for _, tc := range []struct {
		id   string
		want bool
	}{
		{"booking", true},
		{"booking-v2", false},
		{"book", false},
	} {
		got, err := reviewref.Taken(ctx, repo(f), tc.id)
		if err != nil {
			t.Fatalf("Taken(%s): %v", tc.id, err)
		}
		if got != tc.want {
			t.Errorf("Taken(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}

	// The other family counts the same way: a changeset with an archive and no integration record
	// is a half-written record, and handing its name to a new changeset would strand the chain.
	f.MustGit("update-ref", reviewref.Archive("archive-only"), head)
	if taken, err := reviewref.Taken(ctx, repo(f), "archive-only"); err != nil || !taken {
		t.Errorf("Taken for an archive-only changeset = %v, %v; want taken", taken, err)
	}
	// A legacy ref from the retired layout reserves nothing.
	f.MustGit("update-ref", "refs/git-pair/changesets/legacy/archive", head)
	if taken, err := reviewref.Taken(ctx, repo(f), "legacy"); err != nil || taken {
		t.Errorf("Taken for a legacy-only name = %v, %v; want free", taken, err)
	}
}

// Create-only is not create-and-fail: re-asking for the commit a ref already names is a no-op, and
// asking for a different one is the refusal. The pair of properties is what makes a retry the way
// you finish a record rather than the way you discover you need a command git-pair does not have —
// and deleting a ref is not something this design does.
func TestCreateOnlyIsIdempotentAndRefusesConflict(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	first := f.Commit("first", gittest.WithFile("first.md", "first\n"))
	second := f.Commit("second", gittest.WithFile("second.md", "second\n"))
	ctx := context.Background()

	created, err := reviewref.CreateOnly(ctx, repo(f), reviewref.Archive("booking"), first)
	if err != nil || !created {
		t.Fatalf("CreateOnly = (%v, %v); want it to create the ref", created, err)
	}
	created, err = reviewref.CreateOnly(ctx, repo(f), reviewref.Archive("booking"), first)
	if err != nil || created {
		t.Errorf("re-creating the same ref = (%v, %v); want a no-op that succeeds", created, err)
	}
	if got := f.RefSHA(reviewref.Archive("booking")); got != first {
		t.Errorf("the ref moved to %s; it must stay at %s", got, first)
	}

	_, err = reviewref.CreateOnly(ctx, repo(f), reviewref.Archive("booking"), second)
	if !errors.Is(err, reviewref.ErrRefConflict) {
		t.Fatalf("CreateOnly for a different commit = %v, want ErrRefConflict", err)
	}
	// The refusal has to answer the question a re-run actually asks: what is recorded, and what did
	// I ask for. Both SHAs, and the ref between them.
	msg := err.Error()
	for _, want := range []string{reviewref.Archive("booking"), first[:7], second[:7]} {
		if !strings.Contains(msg, want) {
			t.Errorf("conflict error %q does not name %s", msg, want)
		}
	}
	if got := f.RefSHA(reviewref.Archive("booking")); got != first {
		t.Errorf("a refused write moved the ref to %s", got)
	}
}

// The pair is written archive first, and a pair half-written by a crash is completed rather than
// refused. The order matters: the integration ref is the one whose existence means "this changeset
// is finished", so the recoverable half is the one that reads as unfinished.
func TestCreatePairWritesArchiveFirstAndCompletesAHalfPair(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	source := f.Commit("the reviewed head", gittest.WithFile("service.go", "package service\n"))
	f.SwitchTo("main")
	landing := f.Commit("the landing", gittest.WithFile("landed.md", "landed\n"))
	ctx := context.Background()

	// Simulate the crash between the two writes.
	if _, err := reviewref.CreateOnly(ctx, repo(f), reviewref.Archive("booking"), source); err != nil {
		t.Fatalf("CreateOnly: %v", err)
	}
	res, err := reviewref.CreatePair(ctx, repo(f), reviewref.Pair{ID: "booking", Archive: source, Integration: landing})
	if err != nil {
		t.Fatalf("CreatePair completing a half pair: %v", err)
	}
	if res.ArchiveCreated {
		t.Error("the completion reported creating the archive ref, which already existed")
	}
	if !res.IntegrationCreated {
		t.Error("the completion did not create the integration ref")
	}
	if got := f.RefSHA(reviewref.Archive("booking")); got != source {
		t.Errorf("archive = %s, want the reviewed head %s", got, source)
	}
	if got := f.RefSHA(reviewref.Integration("booking")); got != landing {
		t.Errorf("integration = %s, want the landing %s", got, landing)
	}

	// The whole pair, from nothing.
	res, err = reviewref.CreatePair(ctx, repo(f), reviewref.Pair{ID: "other", Archive: source, Integration: landing})
	if err != nil {
		t.Fatalf("CreatePair: %v", err)
	}
	if !res.ArchiveCreated || !res.IntegrationCreated {
		t.Errorf("CreatePair = %+v, want both refs created", res)
	}

	// Re-running the same pair changes nothing and fails nothing.
	res, err = reviewref.CreatePair(ctx, repo(f), reviewref.Pair{ID: "other", Archive: source, Integration: landing})
	if err != nil {
		t.Fatalf("re-running the same pair: %v", err)
	}
	if res.ArchiveCreated || res.IntegrationCreated {
		t.Errorf("re-running the same pair = %+v, want no writes", res)
	}

	// And a pair whose other half disagrees is refused, naming the record that exists.
	if _, err := reviewref.CreatePair(ctx, repo(f), reviewref.Pair{ID: "other", Archive: landing, Integration: source}); !errors.Is(err, reviewref.ErrRefConflict) {
		t.Errorf("conflicting pair = %v, want ErrRefConflict", err)
	}
	if got := f.RefSHA(reviewref.Archive("other")); got != source {
		t.Errorf("a conflicting pair moved the archive to %s; it stays at %s", got, source)
	}
}

// The archive ref is what keeps the chain reachable after the branch is gone, which is the property
// the pair exists for. The CLI test replays this through `integration record`; this pins the
// primitive, including the post-approval tail a squash would otherwise destroy.
func TestArchiveRefKeepsChainReachableWithoutABranch(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	first := f.Head()
	f.CommitChangeset("booking", "main")
	ready := f.CommitReadyMarker("booking")
	approve := f.CommitReviewMarker("booking", "approve", gittest.WithFile("notes.md", "ok\n"))
	tail := f.Commit("a thread reply after the approval", gittest.WithFile("changesets/booking/reply.md", "done\n"))
	ctx := context.Background()

	f.SwitchTo("main")
	landing := f.Commit("the landing", gittest.WithFile("landed.md", "landed\n"))
	if _, err := reviewref.CreatePair(ctx, repo(f), reviewref.Pair{ID: "booking", Archive: approve, Integration: landing}); err != nil {
		t.Fatalf("CreatePair: %v", err)
	}
	f.ForceDeleteBranch("booking")

	for _, want := range []string{first, ready, approve} {
		if !f.ReachableFrom(want, reviewref.Archive("booking")) {
			t.Errorf("%s is not reachable from the archive ref after the branch was deleted", want)
		}
	}
	// The tail is not: the record named the approval, which is the head that was reviewed, and
	// this is the window the design accepts — the branch is what holds anything after it, until the
	// record is written from it.
	if f.ReachableFrom(tail, reviewref.Archive("booking")) {
		t.Error("the archive ref reaches a commit it was never pointed at")
	}
	if got := f.RevListCount(reviewref.Archive("booking")); got != 4 {
		t.Errorf("archive ref reaches %d commits, want the 4-commit chain through the approval", got)
	}
}

// The integration record's existence is the whole answer to "did this land", and the archive's to
// "is there a chain", so each has to say no on its own terms rather than guess from the other.
func TestResolveEachFamilyIndependently(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	head := f.Head()
	ctx := context.Background()

	if _, err := reviewref.CreateOnly(ctx, repo(f), reviewref.Archive("booking"), head); err != nil {
		t.Fatalf("CreateOnly: %v", err)
	}
	if _, err := reviewref.ResolveIntegration(ctx, repo(f), "booking"); !errors.Is(err, reviewref.ErrNotIntegrated) {
		t.Errorf("ResolveIntegration with an archive alone = %v, want ErrNotIntegrated", err)
	}
	if got, err := reviewref.ResolveArchive(ctx, repo(f), "booking"); err != nil || got != head {
		t.Errorf("ResolveArchive = (%q, %v), want (%s, nil)", got, err, head)
	}
	if _, err := reviewref.ResolveArchive(ctx, repo(f), "never-recorded"); !errors.Is(err, reviewref.ErrNoArchiveRef) {
		t.Errorf("ResolveArchive for an unrecorded changeset = %v, want ErrNoArchiveRef", err)
	}
	if _, err := reviewref.ResolveIntegration(ctx, repo(f), "never-recorded"); !errors.Is(err, reviewref.ErrNotIntegrated) {
		t.Errorf("ResolveIntegration for an unrecorded changeset = %v, want ErrNotIntegrated", err)
	}
}

// §24's failure is not the changeset's failure, and `Present` is what tells them apart: one is a
// checkout short of a refspec, the other a changeset nobody recorded. It answers about the namespace,
// so a clone holding either family counts as holding the namespace, which is the point: the fetch
// worked.
func TestPresentIsAboutTheNamespace(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	ctx := context.Background()

	if ok, err := reviewref.Present(ctx, repo(f)); err != nil || ok {
		t.Errorf("Present = %v, %v; want false while the namespace holds nothing", ok, err)
	}
	if _, err := reviewref.CreateOnly(ctx, repo(f), reviewref.Integration("booking"), f.Head()); err != nil {
		t.Fatalf("CreateOnly: %v", err)
	}
	if ok, err := reviewref.Present(ctx, repo(f)); err != nil || !ok {
		t.Errorf("Present = %v, %v; want true once any durable ref exists", ok, err)
	}
}

// The fetch guidance is one string in one place, because it appears in failures and in the README,
// and a message that contradicts the documentation is how people end up fetching the wrong thing.
// It maps the whole namespace — both families and anything the next milestone adds to it — and it
// carries no `+`: both families are append-only, so a fetch that would need to move one of them is
// the bug rather than the case to enable.
func TestFetchRefspecNamesTheNamespace(t *testing.T) {
	if !strings.Contains(reviewref.FetchRefspec, reviewref.NamespaceRoot+"/*:") {
		t.Errorf("FetchRefspec = %q, want it to map the namespace", reviewref.FetchRefspec)
	}
	if strings.HasPrefix(reviewref.FetchRefspec, "+") {
		t.Errorf("FetchRefspec = %q; a force refspec has no business existing while both families are create-only", reviewref.FetchRefspec)
	}
	if !strings.Contains(reviewref.FetchCommand, reviewref.FetchRefspec) {
		t.Errorf("FetchCommand = %q does not contain %q", reviewref.FetchCommand, reviewref.FetchRefspec)
	}
}

// The recorder reads the record before it verifies anything, and both halves of that read matter: a pair
// that already says exactly this is a no-op, and a pair that says something else is refused with what is
// on the record — which is the refusal the reader needs, not the incidental complaint a later check would
// make. Conflict returns the same error the write returns, so the two cannot disagree about wording.
func TestRecordedPairAndConflict(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	one, two := f.Head(), f.Commit("second", gittest.WithFile("b.go", "package main\n"))
	ctx := context.Background()
	pair := reviewref.Pair{ID: "booking", Archive: one, Integration: two}

	if got, err := reviewref.RecordedPair(ctx, repo(f), "booking"); err != nil || got.Archive != "" || got.Integration != "" {
		t.Fatalf("RecordedPair before the record = %+v, %v; want an empty pair and no error", got, err)
	}
	if err := reviewref.Conflict(ctx, repo(f), pair); err != nil {
		t.Fatalf("Conflict before the record = %v, want nil", err)
	}
	if _, err := reviewref.CreatePair(ctx, repo(f), pair); err != nil {
		t.Fatalf("CreatePair: %v", err)
	}
	got, err := reviewref.RecordedPair(ctx, repo(f), "booking")
	if err != nil || got.Archive != one || got.Integration != two {
		t.Fatalf("RecordedPair = %+v, %v; want the pair just written", got, err)
	}
	if err := reviewref.Conflict(ctx, repo(f), pair); err != nil {
		t.Errorf("Conflict for the pair on the record = %v, want nil: the retry completes, it does not argue", err)
	}

	// A different landing commit for a changeset that has one: the integration half is what disagrees,
	// and the refusal names the ref, what it records, and what was asked for.
	backport := f.Commit("backport", gittest.WithFile("c.go", "package main\n"))
	err = reviewref.Conflict(ctx, repo(f), reviewref.Pair{ID: "booking", Archive: one, Integration: backport})
	if !errors.Is(err, reviewref.ErrRefConflict) {
		t.Fatalf("Conflict for a second landing = %v, want ErrRefConflict", err)
	}
	for _, want := range []string{reviewref.Integration("booking"), f.Short(two), f.Short(backport), "never moves"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the conflict must name %q, got: %v", want, err)
		}
	}

	// A half pair is a real answer, and it is the answer that decides the retry completes rather than
	// refuses: the crash case leaves an archive with no integration, and the same pair written again has
	// to finish it.
	f2 := gittest.New(t)
	f2.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	half := reviewref.Pair{ID: "booking", Archive: f2.Head(), Integration: f2.Commit("second", gittest.WithFile("b.go", "package main\n"))}
	if _, err := reviewref.CreateOnly(ctx, repo(f2), reviewref.Archive("booking"), half.Archive); err != nil {
		t.Fatalf("seed the half pair: %v", err)
	}
	got, err = reviewref.RecordedPair(ctx, repo(f2), "booking")
	if err != nil || got.Archive != half.Archive || got.Integration != "" {
		t.Fatalf("RecordedPair on a half pair = %+v, %v; want the archive and an empty integration", got, err)
	}
	if err := reviewref.Conflict(ctx, repo(f2), half); err != nil {
		t.Errorf("Conflict on a half pair = %v, want nil: completing a half record is not a conflict", err)
	}
}
