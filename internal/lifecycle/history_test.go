package lifecycle_test

import (
	"context"
	"strings"
	"testing"

	"gitpair/internal/git"
	"gitpair/internal/gittest"
	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
)

func repo(f *gittest.Fixture) *git.Repo { return &git.Repo{Dir: f.Dir()} }

func summarize(t *testing.T, f *gittest.Fixture, slug, base, headRef string) lifecycle.Summary {
	t.Helper()
	summary, err := lifecycle.Summarize(context.Background(), repo(f), slug, base, headRef)
	if err != nil {
		t.Fatalf("Summarize(%s, base %s, head %s): %v", slug, base, headRef, err)
	}
	return summary
}

// summarizeAgainstTree is the derivation `git pair check` asks its verdict of: the marker
// verdict, then the question of whether the content it spoke about is still at headRef.
func summarizeAgainstTree(t *testing.T, f *gittest.Fixture, slug, base, headRef string) lifecycle.Summary {
	t.Helper()
	return summarizeAgainstTreeCrediting(t, f, slug, base, headRef, "")
}

// summarizeAgainstTreeCrediting is the same reading with the destination named, which is what lets the
// drift rule credit content the branch took in rather than content it added (§10.4). An empty destination
// is the reading for a clone that cannot name one.
func summarizeAgainstTreeCrediting(t *testing.T, f *gittest.Fixture, slug, base, headRef,
	destination string) lifecycle.Summary {
	t.Helper()
	summary, err := lifecycle.SummarizeAgainstTree(context.Background(), repo(f), slug, base, headRef, destination)
	if err != nil {
		t.Fatalf("SummarizeAgainstTree(%s, base %s, head %s): %v", slug, base, headRef, err)
	}
	return summary
}

// TestSummarizePRD12GoldenHistory replays the PRD §12 lifecycle against real
// commits and checks the derived state after every step, which is the golden
// history the plan's M2 verification asks for.
func TestSummarizePRD12GoldenHistory(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking-transaction")
	slug, base := "booking-transaction", "main"

	steps := []struct {
		name      string
		act       func()
		want      model.State
		wantRev   int
		wantStale bool
	}{
		{
			name: "implementation only",
			act: func() {
				f.CommitChangeset(slug, base)
				f.Commit("implement locking", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))
			},
			want: model.StateWorking, wantRev: 0,
		},
		{
			name: "ready",
			act:  func() { f.CommitReadyMarker(slug) },
			want: model.StateReady, wantRev: 0,
		},
		{
			name: "review block",
			act: func() {
				f.CommitReviewMarker(slug, "block",
					gittest.WithFile("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n"))
			},
			want: model.StateBlocked, wantRev: 1,
		},
		{
			// Committing a fix is not a marker, so the block is still the newest one and
			// the changeset stays BLOCKED until the author runs `change ready`.
			name:      "author response leaves the block standing",
			act:       func() { f.Commit("address review", gittest.WithFile("other.go", "package main\n\nvar x = 1\n")) },
			want:      model.StateBlocked,
			wantRev:   1,
			wantStale: true,
		},
		{
			name: "ready again",
			act:  func() { f.CommitReadyMarker(slug) },
			want: model.StateReady, wantRev: 1,
		},
		{
			name:      "review feedback",
			act:       func() { f.CommitReviewMarker(slug, "feedback", gittest.WithFile("notes.md", "note\n")) },
			want:      model.StateFeedback,
			wantRev:   2,
			wantStale: false,
		},
		{
			name:      "review approve",
			act:       func() { f.CommitReviewMarker(slug, "approve") },
			want:      model.StateApproved,
			wantRev:   3,
			wantStale: false,
		},
	}

	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			step.act()
			got := summarize(t, f, slug, base, "HEAD")
			if got.State != step.want {
				t.Errorf("state = %s, want %s (reason: %s)", got.State, step.want, got.Reason)
			}
			if len(got.Reviews) != step.wantRev {
				t.Errorf("reviews = %d, want %d", len(got.Reviews), step.wantRev)
			}
			if got.Stale != step.wantStale {
				t.Errorf("stale = %v, want %v (reason: %s)", got.Stale, step.wantStale, got.Reason)
			}
		})
	}
}

// The archive guard, measured against real commits: an implementation commit after an
// approval leaves the approval as the state the queue and status report, but the head no
// longer carries the content that was approved, so completion refuses it (PRD §9.5, §12).
func TestSummarizeImplementationAfterApproveBlocksCompletionButNotState(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	slug, base := "booking", "main"

	f.CommitChangeset(slug, base)
	f.CommitReadyMarker(slug)
	approve := f.CommitReviewMarker(slug, "approve")

	if got := summarize(t, f, slug, base, "HEAD"); got.State != model.StateApproved {
		t.Fatalf("state before the agent commit = %s, want APPROVED", got.State)
	}

	after := f.Commit("agent: address feedback", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))

	got := summarize(t, f, slug, base, "HEAD")
	if got.State != model.StateApproved {
		t.Errorf("state = %s, want APPROVED: a commit is not a marker", got.State)
	}
	if !got.Stale {
		t.Error("stale = false, want true")
	}
	if got.Marker == nil || got.Marker.SHA != approve {
		t.Errorf("Marker = %+v, want the approve %s still named as the newest marker", got.Marker, approve)
	}

	// What the drift does decide: the head is no longer the reviewed content.
	if tree := summarizeAgainstTree(t, f, slug, base, "HEAD"); tree.State != model.StateWorking {
		t.Errorf("SummarizeAgainstTree state = %s, want WORKING (reason: %s)", tree.State, tree.Reason)
	}

	// The branch head is the implementation commit, not the approval.
	if f.Head() != after {
		t.Fatalf("HEAD = %s, want %s", f.Head(), after)
	}
}

// PRD §10.5: "Only commits explicitly identified as review marker commits
// count as reviews. Ordinary Git commits do not."
func TestSummarizeIgnoresOrdinaryCommitsAsReviews(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	slug, base := "booking", "main"

	f.CommitChangeset(slug, base)
	f.Commit("review: looks fine to me", gittest.WithFile("a.txt", "a\n"))
	f.Commit("review: block booking", gittest.WithFile("b.txt", "b\n"))
	f.Commit("Review-Outcome: block", gittest.WithFile("c.txt", "c\n"))

	got := summarize(t, f, slug, base, "HEAD")
	if len(got.Reviews) != 0 {
		t.Errorf("reviews = %v, want none: a commit subject or message body is not a review submission", got.Reviews)
	}
	if got.State != model.StateWorking {
		t.Errorf("state = %s, want WORKING", got.State)
	}
}

// The plan's risk table: "Accept a marker only with both expected trailers
// (Review-Changeset matching); malformed ⇒ warning line, not a state change."
// Each of these commits carries Review-* trailers that must not be honoured.
func TestSummarizeMalformedAndMismatchedTrailersAreImplementation(t *testing.T) {
	tests := []struct {
		name    string
		message string
		// wantWarn is whether git-pair can see the malformed trailer at all. When a
		// hand-written trailer block is not a valid trailer block, git's own
		// `%(trailers)` expansion reports nothing, so git-pair cannot warn about it
		// either; what matters is that no state is established.
		wantWarn bool
	}{
		{
			name:     "ready marker for a different changeset",
			message:  "git-pair: ready other-changeset\n\nReview-State: ready\nReview-Changeset: other-changeset\n",
			wantWarn: true,
		},
		{
			name:     "ready marker with no changeset trailer",
			message:  "git-pair: ready booking\n\nReview-State: ready\n",
			wantWarn: true,
		},
		{
			name:     "unknown state value",
			message:  "git-pair: ready booking\n\nReview-State: READY\nReview-Changeset: booking\n",
			wantWarn: true,
		},
		{
			name:     "unknown outcome value",
			message:  "review: approve booking\n\nReview-Outcome: approved\nReview-Changeset: booking\n",
			wantWarn: true,
		},
		{
			name:     "review marker with no changeset trailer",
			message:  "review: block booking\n\nReview-Outcome: block\n",
			wantWarn: true,
		},
		{
			name:     "changeset trailer line without a colon",
			message:  "review: block booking\n\nReview-Outcome: block\nReview-Changeset booking\n",
			wantWarn: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := gittest.New(t)
			f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
			f.CreateBranch("booking")
			slug, base := "booking", "main"
			f.CommitChangeset(slug, base)

			f.CommitMessage(tc.message, gittest.WithEmpty())

			got := summarize(t, f, slug, base, "HEAD")
			if got.State != model.StateWorking {
				t.Errorf("state = %s, want WORKING: %s must not establish a lifecycle state", got.State, tc.name)
			}
			if len(got.Reviews) != 0 {
				t.Errorf("reviews = %v, want none", got.Reviews)
			}
			if got.Marker != nil {
				t.Errorf("Marker = %+v, want nil", got.Marker)
			}
			if len(got.Unrecognised) == 0 && tc.wantWarn {
				t.Error("Unrecognised is empty: the plan requires a warning line for an unreadable marker")
			}
			if !tc.wantWarn && len(got.Unrecognised) != 0 {
				t.Errorf("Unrecognised = %v, want empty: git reports no trailer block, so there is nothing to warn about", got.Unrecognised)
			}
		})
	}
}

// PRD §21: stacked branches each have their own review history. A sibling
// changeset is the base, and only the branch's own commits are walked.
func TestSummarizeStackedChangesetsHaveIndependentHistories(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))

	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("booking impl", gittest.WithFile("service.go", "package main\n"))
	f.CommitReadyMarker("booking")
	f.CommitReviewMarker("booking", "block", gittest.WithFile("service.go", "package main\n\n// comment\n"))

	f.CreateBranch("booking-tests", "booking")
	f.CommitChangeset("booking-tests", "booking")
	f.Commit("tests impl", gittest.WithFile("service_test.go", "package main\n\nfunc TestLock() {}\n"))
	f.CommitReadyMarker("booking-tests")

	below := summarize(t, f, "booking", "main", "booking")
	if len(below.Reviews) != 1 || below.Reviews[0].Outcome != model.OutcomeBlock {
		t.Errorf("lower changeset reviews = %v, want its own single block review", below.Reviews)
	}
	if below.State != model.StateBlocked {
		t.Errorf("lower changeset state = %s, want BLOCKED", below.State)
	}

	top := summarize(t, f, "booking-tests", "booking", "HEAD")
	if len(top.Reviews) != 0 {
		t.Errorf("upper changeset reviews = %v, want none: it has never been reviewed", top.Reviews)
	}
	if top.State != model.StateReady {
		t.Errorf("upper changeset state = %s, want READY", top.State)
	}
	// Its history is only its own commits, measured against the sibling branch.
	if len(top.Events) != 3 {
		t.Errorf("upper changeset events = %d (%v), want 3: changeset dir, implementation, ready marker",
			len(top.Events), subjects(top.Events))
	}
}

// A changeset whose base cannot be resolved must fail loudly rather than guess a
// trunk (plan risk table: "never guess; test covers missing base").
func TestSummarizeUnresolvableBaseFails(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "does-not-exist")

	_, err := lifecycle.Summarize(context.Background(), repo(f), "booking", "does-not-exist", "HEAD")
	if err == nil {
		t.Fatal("Summarize succeeded with an unresolvable base")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error = %v, want it to name the unresolvable base", err)
	}
	if _, err := lifecycle.Summarize(context.Background(), repo(f), "booking", "", "HEAD"); err == nil {
		t.Error("Summarize succeeded with an empty base")
	}
}

func TestSummarizeOtherHeadRef(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.CommitReadyMarker("booking")
	ready := f.Head()

	// Inspecting another branch must not depend on it being checked out.
	f.CreateBranch("other", "main")
	got := summarize(t, f, "booking", "main", "booking")
	if got.State != model.StateReady {
		t.Errorf("state of the non-checked-out branch = %s, want READY", got.State)
	}
	if got.Marker == nil || got.Marker.SHA != ready {
		t.Errorf("Marker = %+v, want the ready marker %s", got.Marker, ready)
	}
}

func subjects(events []lifecycle.Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Subject)
	}
	return out
}

// The tree verdict looks past a commit that touches nothing but changesets/<slug>/: that is
// not an implementation change, and the reviewer reads ABOUT.md and threads from HEAD anyway.
func TestSummarizeAgainstTreeChangesetOnlyCommitKeepsTheMarker(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	slug, base := "booking", "main"
	f.CommitChangeset(slug, base)
	f.CommitReadyMarker(slug)

	f.WriteChangesetFile(slug, "ABOUT.md", "# booking\n\n## Summary\n\nExpanded for the reviewer.\n")
	f.Commit("describe booking")

	got := summarizeAgainstTree(t, f, slug, base, "HEAD")
	if got.State != model.StateReady {
		t.Errorf("state = %s, want READY: no implementation code changed (reason: %s)", got.State, got.Reason)
	}
	if got.Stale {
		t.Error("Stale = true, want false")
	}
	if !strings.Contains(got.Reason, "changeset-only") {
		t.Errorf("reason = %q, want it to name the commits it looked past", got.Reason)
	}
}

// The boundary the tree comparison has to get right: one commit touching both the
// changeset directory and code is an implementation commit, because the code moved.
func TestSummarizeAgainstTreeMixedCommitDropsTheMarker(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	slug, base := "booking", "main"
	f.CommitChangeset(slug, base)
	f.CommitReadyMarker(slug)

	f.WriteChangesetFile(slug, "ABOUT.md", "# booking\n\n## Summary\n\nAlso touched the code.\n")
	f.Commit("address feedback", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))

	got := summarizeAgainstTree(t, f, slug, base, "HEAD")
	if got.State != model.StateWorking {
		t.Errorf("state = %s, want WORKING: code changed above the marker", got.State)
	}
	if !got.Stale {
		t.Error("Stale = false, want true")
	}
	if !strings.Contains(got.Reason, "code changed since ready") {
		t.Errorf("reason = %q, want it to say the code changed since the marker", got.Reason)
	}
	// Without the tree question the same history still reads as READY: state moves on
	// markers, and this is the difference between the two derivations.
	if plain := summarize(t, f, slug, base, "HEAD"); plain.State != model.StateReady {
		t.Errorf("Summarize state = %s, want READY (reason: %s)", plain.State, plain.Reason)
	}
}

// Counting commits gets merges and rebases wrong; comparing trees does not. Here
// the changeset is unchanged and merged into an advanced trunk, so the reviewed
// code is still exactly what was approved.
func TestSummarizeBaseMovingUnderAReadyChangesetKeepsReady(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	slug, base := "booking", "main"
	f.CommitChangeset(slug, base)
	f.CommitReadyMarker(slug)

	f.SwitchTo("main")
	f.Commit("trunk work", gittest.WithFile("other.go", "package main\n"))
	f.SwitchTo("booking")

	if got := summarize(t, f, slug, base, "HEAD"); got.State != model.StateReady {
		t.Errorf("state = %s, want READY: the reviewed code is untouched (reason: %s)", got.State, got.Reason)
	}
}

// TestScanLineageKeepsEveryChangesetsMarkers pins the two properties `integration record` depends on:
// newest-first order, and no filtering by changeset id.
//
// Summarize filters by id because it answers one changeset's state. The scan answers a different
// question — "which changesets do the markers in this history name" — and the disagreement between that
// answer and the directory in the tree is a refusal the recorder reports (PRD §11.4). It is invisible
// once the scan has dropped the ids nobody asked about, which is why the scan does not take a slug.
func TestScanLineageKeepsEveryChangesetsMarkers(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	ready := f.CommitReadyMarker("booking")
	approval := f.CommitReviewMarker("booking", "approve")
	feedback := f.CommitReviewMarker("other", "feedback")
	// An ordinary commit: no trailers, so it is not a marker and does not belong in the result.
	f.Commit("implement locking", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))

	scans, err := lifecycle.ScanLineage(context.Background(), repo(f), "HEAD")
	if err != nil {
		t.Fatalf("ScanLineage: %v", err)
	}
	got := make([]string, 0, len(scans))
	for _, s := range scans {
		got = append(got, s.Short)
	}
	want := []string{f.Short(feedback), f.Short(approval), f.Short(ready)}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("scan = %v, want the three markers newest-first %v", got, want)
	}
	// The trailers come back parsed, because the caller's question is about their values: which
	// changeset, and what verdict.
	newest := scans[0]
	if newest.Trailers[model.TrailerChangeset] != "other" || newest.Trailers[model.TrailerOutcome] != string(model.OutcomeFeedback) {
		t.Errorf("newest marker = %v, want an approval of `other` read out of the trailers", newest.Trailers)
	}
	if newest.SHA != feedback {
		t.Errorf("SHA = %s, want %s", newest.SHA, feedback)
	}
}

// TestSummarizeDeclarationOverRealCommits is the trailer round trip: a declaration written as an
// ordinary commit message has to survive `git log`'s trailer block and come back as INTEGRATING with
// both commits addressable — the one that made the declaration, and the approval underneath it that
// licenses the merge.
//
// The last two steps are the reason `check` asks the tree as well as the marker. A declaration is a
// claim about the commit it names, so an implementation commit placed after it must not read as a
// still-declared head, while a commit inside changesets/<slug>/ must not destroy the declaration the
// author just wrote.
func TestSummarizeDeclarationOverRealCommits(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("implement locking", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))
	f.CommitReadyMarker("booking")
	approval := f.CommitReviewMarker("booking", "approve")

	head := f.Head()
	declared := f.CommitIntegrateMarker("booking")

	got := summarize(t, f, "booking", "main", "HEAD")
	if got.State != model.StateIntegrating {
		t.Fatalf("state = %s, want INTEGRATING (%s)", got.State, got.Reason)
	}
	if got.Marker == nil || got.Marker.Kind != lifecycle.KindIntegrate || got.Marker.SHA != declared {
		t.Errorf("marker = %+v, want the declaration %s", got.Marker, f.Short(declared))
	}
	if got.Integrating == nil || got.Integrating.SHA != declared {
		t.Fatalf("Integrating = %+v, want %s", got.Integrating, f.Short(declared))
	}
	if got.Integrating.ReviewedHead != head {
		t.Errorf("declaration names %q, want the head it was written on %s", got.Integrating.ReviewedHead, f.Short(head))
	}
	if got.LatestReview == nil || got.LatestReview.SHA != approval {
		t.Errorf("latest review = %+v, want the approval %s to still be the verdict", got.LatestReview, f.Short(approval))
	}
	if got.Trailing != 0 || got.Stale {
		t.Errorf("trailing=%d stale=%v, want a clean head at the declaration", got.Trailing, got.Stale)
	}

	// A commit inside the changeset directory is not content the reviewer looked at, so the
	// declaration still describes HEAD and `check`'s verdict stays where the author left it.
	f.Commit("note the follow-up", gittest.WithFile(f.ChangesetPath("booking", "notes.md"), "follow-up\n"))
	got = summarizeAgainstTree(t, f, "booking", "main", "HEAD")
	if got.State != model.StateIntegrating || got.Stale || len(got.Drifted) != 0 {
		t.Errorf("after a changeset-only commit: state=%s stale=%v drifted=%v, want the declaration standing",
			got.State, got.Stale, got.Drifted)
	}

	// An implementation commit is. The declaration's own tree is what was declared, so the changed
	// file is drift against it and the state falls back to WORKING for the gate to refuse.
	f.Commit("one more change", gittest.WithFile("service.go", "package main\n\nfunc Lock() { ctx() }\n"))
	got = summarizeAgainstTree(t, f, "booking", "main", "HEAD")
	if got.State != model.StateWorking {
		t.Errorf("after an implementation commit: state = %s, want WORKING (%s)", got.State, got.Reason)
	}
	if len(got.Drifted) != 1 || got.Drifted[0] != "service.go" {
		t.Errorf("drifted = %v, want service.go", got.Drifted)
	}
	// The declaration is still the newest marker; the tree is what changed the answer.
	if got.Integrating == nil {
		t.Error("Integrating was dropped when the tree moved: the marker is still on the branch")
	}
}
