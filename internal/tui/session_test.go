package tui_test

import (
	"context"
	"strings"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/gittest"
	"gitpair/internal/lifecycle"
	"gitpair/internal/span"
	"gitpair/internal/tui"
)

const slug = "booking-transaction"

// The TUI's render half needs a terminal, so the PRD §14/§16 behaviours are tested
// through the headless Session model: the file list for the resolved span, the
// per-file reviewed marks, the span toggle, and the reset of a mark when a file's
// diff changes mid-session.

type env struct {
	f    *gittest.Fixture
	repo *git.Repo
	cs   changeset.Changeset
}

func newEnv(t *testing.T) *env {
	t.Helper()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n\nfunc main() {}\n"))
	f.CreateBranch(slug)
	f.CommitChangeset(slug, "main")
	f.Commit("implement", gittest.WithFiles(map[string]string{
		"service.go": "package main\n\nfunc Lock() {}\n",
		"handler.go": "package main\n\nfunc Serve() {}\n",
		"store.go":   "package main\n\nfunc Save() {}\n",
	}))

	e := &env{f: f, repo: &git.Repo{Dir: f.Dir()}}
	cs, err := changeset.Current(context.Background(), e.repo, "")
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	e.cs = cs
	return e
}

func (e *env) session(t *testing.T, opts span.Selector) *tui.Session {
	t.Helper()
	summary, err := lifecycle.SummarizeHEAD(context.Background(), e.repo, e.cs.Slug, e.cs.Base)
	if err != nil {
		t.Fatalf("SummarizeHEAD: %v", err)
	}
	sess, err := tui.NewSession(context.Background(), tui.Options{
		Repo: e.repo, Changeset: e.cs, Summary: summary, Span: opts,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return sess
}

func filePaths(files []tui.File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

func indexOfFile(files []tui.File, path string) int {
	for i, f := range files {
		if f.Path == path {
			return i
		}
	}
	return -1
}

// PRD §14: the screen lists the changed files of the chosen span with a header
// naming the changeset, its base and the span.
func TestSessionListsChangedFilesForFullChangeset(t *testing.T) {
	e := newEnv(t)

	sess := e.session(t, span.Full())

	files := sess.Files()
	// The three implementation files plus the two changeset artifacts.
	for _, want := range []string{"service.go", "handler.go", "store.go",
		"changesets/" + slug + "/ABOUT.md", "changesets/" + slug + "/CHANGESET.yaml"} {
		if indexOfFile(files, want) < 0 {
			t.Errorf("Files() = %v, want it to include %s", filePaths(files), want)
		}
	}
	for _, f := range files {
		if f.Reviewed {
			t.Errorf("%s starts reviewed; a fresh session has reviewed nothing", f.Path)
		}
	}
	if reviewed, total := sess.Count(); reviewed != 0 || total != len(files) {
		t.Errorf("Count() = (%d, %d), want (0, %d)", reviewed, total, len(files))
	}
	if sess.UnreviewedCount() != len(files) {
		t.Errorf("UnreviewedCount() = %d, want %d", sess.UnreviewedCount(), len(files))
	}

	header := sess.Header()
	if header.Title != slug {
		t.Errorf("Header.Title = %q, want %q", header.Title, slug)
	}
	if header.Base != "main" {
		t.Errorf("Header.Base = %q, want main", header.Base)
	}
	if !strings.Contains(header.SpanLabel, "main") {
		t.Errorf("Header.SpanLabel = %q, want it to name the resolved span", header.SpanLabel)
	}
	if sess.ArchiveRef() != "refs/git-pair/changesets/"+slug+"/archive" {
		t.Errorf("ArchiveRef() = %q, want refs/git-pair/changesets/%s/archive", sess.ArchiveRef(), slug)
	}
	if sess.AboutPath() != "changesets/"+slug+"/ABOUT.md" {
		t.Errorf("AboutPath() = %q", sess.AboutPath())
	}
}

// The full changeset is the default span, but it is a default a caller applies — `spanOptions`
// with no flags resolves to it, and the picker seeds the same pair. A selector that names no head
// is a caller that forgot to choose, and reading that as "the whole changeset" would open a screen
// reviewing something nobody asked for, which is worse than an error.
func TestSessionRefusesASelectorThatNamesNoHead(t *testing.T) {
	e := newEnv(t)
	summary, err := lifecycle.SummarizeHEAD(context.Background(), e.repo, e.cs.Slug, e.cs.Base)
	if err != nil {
		t.Fatalf("SummarizeHEAD: %v", err)
	}

	_, err = tui.NewSession(context.Background(), tui.Options{
		Repo: e.repo, Changeset: e.cs, Summary: summary, Span: span.Selector{},
	})
	if err == nil {
		t.Fatal("an empty selector opened a session; the full changeset must be chosen, not defaulted into by a zero value")
	}
	if !strings.Contains(err.Error(), "head") {
		t.Errorf("error = %q, want it to name the end that is missing", err)
	}
}

func TestSessionToggleMarksFilesReviewed(t *testing.T) {
	e := newEnv(t)
	sess := e.session(t, span.Full())

	i := indexOfFile(sess.Files(), "service.go")
	sess.Toggle(i)
	if reviewed, _ := sess.Count(); reviewed != 1 {
		t.Errorf("Count() reviewed = %d, want 1", reviewed)
	}
	if !sess.Files()[i].Reviewed {
		t.Error("toggle did not mark the file reviewed")
	}
	sess.Toggle(i)
	if reviewed, _ := sess.Count(); reviewed != 0 {
		t.Errorf("Count() reviewed = %d after a second toggle, want 0", reviewed)
	}

	// Out-of-range indexes are ignored rather than panicking: the TUI drives these
	// from arrow keys.
	sess.Toggle(-1)
	sess.Toggle(len(sess.Files()) + 5)
	if reviewed, _ := sess.Count(); reviewed != 0 {
		t.Errorf("Count() reviewed = %d after out-of-range toggles, want 0", reviewed)
	}
}

// PRD §16: "If the underlying diff for a file changes after it was marked reviewed
// during the current session, git-pair should ideally reset it to unreviewed."
func TestSessionResetsReviewedMarkWhenFileDiffChanges(t *testing.T) {
	e := newEnv(t)
	sess := e.session(t, span.Full())

	sess.Toggle(indexOfFile(sess.Files(), "service.go"))
	sess.Toggle(indexOfFile(sess.Files(), "handler.go"))
	if reviewed, _ := sess.Count(); reviewed != 2 {
		t.Fatalf("Count() reviewed = %d, want 2", reviewed)
	}

	// The author changes one of the two reviewed files.
	e.f.Commit("author response", gittest.WithFile("service.go",
		"package main\n\nfunc Lock() { transaction() }\n"))
	if err := sess.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	// Two distinct failures live here, and conflating them made an intermittent
	// report undiagnosable: the file can be absent from the rescan, or present
	// and still marked. Say which.
	changed := indexOfFile(sess.Files(), "service.go")
	if changed < 0 {
		t.Errorf("service.go left the span after the author's commit; it is not a diff change but a rescan problem: %+v", sess.Files())
	} else if sess.Files()[changed].Reviewed {
		t.Errorf("service.go is still marked reviewed after its diff changed: %+v", sess.Files())
	}
	untouched := indexOfFile(sess.Files(), "handler.go")
	if untouched < 0 {
		t.Errorf("handler.go left the span even though nothing touched it: %+v", sess.Files())
	} else if !sess.Files()[untouched].Reviewed {
		t.Errorf("handler.go lost its reviewed mark even though its diff did not change: %+v", sess.Files())
	}
}

// A new file appearing in the span shows up unreviewed.
func TestSessionPicksUpNewlyChangedFiles(t *testing.T) {
	e := newEnv(t)
	sess := e.session(t, span.Full())
	before := len(sess.Files())

	e.f.Commit("add a file", gittest.WithFile("cache.go", "package main\n\nfunc Cache() {}\n"))
	if err := sess.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	i := indexOfFile(sess.Files(), "cache.go")
	if i < 0 {
		t.Fatalf("Files() = %v, want cache.go after it was added", filePaths(sess.Files()))
	}
	if len(sess.Files()) != before+1 {
		t.Errorf("Files() grew from %d to %d, want one more", before, len(sess.Files()))
	}
	if sess.Files()[i].Reviewed {
		t.Error("a newly changed file must start unreviewed")
	}
}

// PRD §14's `v` binding steps through the spans this session has been in, and PRD §17.2
// makes the unreviewed span one of them. With nothing custom chosen yet the ring is the full
// changeset and the unreviewed span, so `v` is still the toggle the manual describes.
func TestSessionStepSpanWalksFullChangesetAndUnreviewed(t *testing.T) {
	e := newEnv(t)
	sess := e.session(t, span.Full())

	if sess.CanToggleSpan() {
		t.Error("CanToggleSpan() = true before any review submission")
	}
	if res, err := sess.StepSpan(context.Background()); err == nil {
		t.Error("StepSpan succeeded with no review submissions and nowhere to step")
	} else if res.Total != 1 {
		t.Errorf("the ring holds %d stops before any review, want only the span it opened on", res.Total)
	}

	// A review that touches one file, then the author's response to it.
	e.f.Write("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n")
	e.f.CommitReviewMarker(slug, "block")
	e.f.Commit("author response", gittest.WithFile("service.go",
		"package main\n\nfunc Lock() { transaction() }\n"))
	if err := sess.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !sess.CanToggleSpan() {
		t.Fatal("CanToggleSpan() = false after a review submission")
	}

	res, err := sess.StepSpan(context.Background())
	if err != nil {
		t.Fatalf("StepSpan: %v", err)
	}
	if res.Pos != 2 || res.Total != 2 {
		t.Errorf("StepSpan landed on stop %d of %d, want 2 of 2", res.Pos, res.Total)
	}
	if !sess.Unreviewed() {
		t.Error("Unreviewed() = false after stepping to the unreviewed span")
	}
	files := filePaths(sess.Files())
	if len(files) != 1 || files[0] != "service.go" {
		t.Errorf("unreviewed span Files() = %v, want only the file the review touched", files)
	}
	if got := sess.Span(); got.Base.Kind != span.KindReview || !got.Live() {
		t.Errorf("Span() = %s → %s, want a live span starting at a review", got.Base.Kind, got.Head.Kind)
	}

	// Marks are keyed on a file's diff within a span: stepping to a span where that diff
	// is not the whole story must not carry the mark over quietly.
	sess.Toggle(indexOfFile(sess.Files(), "service.go"))
	res, err = sess.StepSpan(context.Background())
	if err != nil {
		t.Fatalf("StepSpan back: %v", err)
	}
	if res.Pos != 1 || res.Total != 2 {
		t.Errorf("StepSpan wrapped to %d of %d, want 1 of 2", res.Pos, res.Total)
	}
	if sess.Unreviewed() {
		t.Error("Unreviewed() = true after stepping back to the full span")
	}
	if got := sess.Span(); got.Base.Kind != span.KindChangesetBase || !got.Live() {
		t.Errorf("Span() = %s → %s, want the full changeset", got.Base.Kind, got.Head.Kind)
	}
	if len(sess.Files()) < 3 {
		t.Errorf("full span Files() = %v, want the whole changeset again", filePaths(sess.Files()))
	}
	if f := sess.Files()[indexOfFile(sess.Files(), "service.go")]; f.Reviewed {
		t.Error("a mark made in the unreviewed span carried into the full changeset, where the " +
			"file's diff is a different piece of work")
	}
}

// A span chosen with `V` is a stop on the ring, which is the point of the ring: a custom
// span should still be one keystroke away after you have stepped off it, rather than
// something you have to pick out of the list again.
func TestSessionStepSpanWalksSpansChosenWithThePicker(t *testing.T) {
	e := newEnv(t)
	e.f.CommitReviewMarker(slug, "feedback")
	sess := e.session(t, span.Full())
	earlier := e.f.Parent(e.f.Head())
	ctx := context.Background()

	custom := span.Selector{Base: span.Commit(earlier), Head: span.WorkingTree()}
	if err := sess.SetSpan(ctx, custom); err != nil {
		t.Fatalf("SetSpan: %v", err)
	}
	pos, total := sess.SpanPosition()
	if want := len(sess.SpanRing()); pos != want || total != want {
		t.Errorf("the span just chosen sits at %d of %d, want it at the end of the ring", pos, total)
	}
	if total != 3 {
		t.Errorf("the ring holds %d stops (full, unreviewed, custom), want 3", total)
	}

	// A whole turn of the ring arrives back at the stop the reviewer chose. That is the
	// point of the ring: a custom span does not vanish the moment you step off it.
	for i := 0; i < total; i++ {
		if _, err := sess.StepSpan(ctx); err != nil {
			t.Fatalf("StepSpan %d: %v", i+1, err)
		}
	}
	got := sess.Selector()
	if got.Base.Kind != span.KindCommit || got.Base.Name != earlier || got.Head.Kind != span.KindWorkingTree {
		t.Errorf("a turn of the ring ended at %s %q \u2192 %s, want the span based on %s that was chosen",
			got.Base.Kind, got.Base.Name, got.Head.Kind, earlier[:7])
	}
}

// A rescan after an editor or difftool closes is not a visit: without this, the ring would
// fill with copies of the span the session never left, and `v` would become a way to stand
// still.
func TestSessionRescanAddsNoStopsToTheRing(t *testing.T) {
	e := newEnv(t)
	sess := e.session(t, span.Full())
	before := len(sess.SpanRing())
	for i := 0; i < 3; i++ {
		if err := sess.Reload(context.Background()); err != nil {
			t.Fatalf("Reload: %v", err)
		}
	}
	if after := len(sess.SpanRing()); after != before {
		t.Errorf("three rescans left %d stops on the ring, want %d", after, before)
	}
}

// A stop whose endpoint no longer resolves -- the tag deleted, the branch force-pushed away -- is
// passed over rather than being a wall, and it is named. Blocking there made one dead stop stop the
// walk: every press failed the same way, and `V` was the only way past. A walk that got shorter in
// silence would be worse in a different way, so the report says which spans it stepped over and why.
func TestStepSpanSkipsAStopThatNoLongerResolves(t *testing.T) {
	e := newEnv(t)
	e.f.CommitReviewMarker(slug, "feedback")
	tagged := e.f.Head()
	e.f.MustGit("tag", "probe-tag", tagged)
	ctx := context.Background()

	sess := e.session(t, span.Full())
	live := span.Selector{Base: span.Commit(e.f.Parent(e.f.Head())), Head: span.WorkingTree()}
	typed := span.Selector{Base: span.ChangesetBase(), Head: span.Commit("probe-tag")}
	for _, sel := range []span.Selector{live, typed, live} {
		if err := sess.SetSpan(ctx, sel); err != nil {
			t.Fatalf("SetSpan(%s): %v", sel.Display(), err)
		}
	}
	if stops := len(sess.SpanRing()); stops != 4 {
		t.Errorf("the ring holds %d stops, want 4: the ring is a set of spans, not a log of "+
			"keystrokes", stops)
	}
	if pos, _ := sess.SpanPosition(); pos != 3 {
		t.Fatalf("standing on stop %d, want the live span at 3", pos)
	}

	held := sess.Span().Label
	e.f.MustGit("update-ref", "-d", "refs/tags/probe-tag")
	res, err := sess.StepSpan(ctx)
	if err != nil {
		t.Fatalf("a dead stop stopped the walk: %v", err)
	}
	if res.Pos != 1 {
		t.Errorf("v went to stop %d, want it past the dead stop and around to stop 1", res.Pos)
	}
	if held == sess.Span().Label {
		t.Errorf("the session did not move off %s", held)
	}
	if len(res.Skipped) != 1 {
		t.Fatalf("the step reported %d skipped stops, want 1: %+v", len(res.Skipped), res.Skipped)
	}
	if got := res.Skipped[0].Selector.Key(); got != typed.Key() {
		t.Errorf("the step skipped %q, want the span chosen by the typed tag", got)
	}
	if res.Skipped[0].Reason == "" {
		t.Error("a skipped stop with no reason reads as a span that vanished, not one git cannot reach")
	}

	// The ring is intact: with the tag back, the stop is where it always was, and the walk stops
	// reporting it.
	e.f.MustGit("update-ref", "refs/tags/probe-tag", tagged)
	for i := 0; i < 3; i++ {
		res, err := sess.StepSpan(ctx)
		if err != nil {
			t.Fatalf("StepSpan %d: %v", i+1, err)
		}
		if len(res.Skipped) != 0 {
			t.Fatalf("step %d skipped %d stops with the tag back: %+v", i+1, len(res.Skipped), res.Skipped)
		}
		if got := sess.Selector().Head; got.Kind == span.KindCommit && got.Name == "probe-tag" {
			return // reached it, without skipping anything on the way
		}
	}
	t.Error("walking the ring after the tag came back never arrived at the typed span")
}

// Two dead stops in a row are two names in the report. One count would leave the reviewer wondering
// which of the spans they chose is gone.
func TestStepSpanNamesEveryStopItSkipped(t *testing.T) {
	e := newEnv(t)
	e.f.CommitReviewMarker(slug, "feedback")
	first := e.f.Head()
	e.f.MustGit("tag", "tag-a", first)
	e.f.Commit("more work", gittest.WithFile("service.go", "package main\n\nfunc Lock() { tx(); more() }\n"))
	e.f.MustGit("tag", "tag-b", e.f.Head())
	ctx := context.Background()

	sess := e.session(t, span.Full())
	spanA := span.Selector{Base: span.ChangesetBase(), Head: span.Commit("tag-a")}
	spanB := span.Selector{Base: span.ChangesetBase(), Head: span.Commit("tag-b")}
	for _, sel := range []span.Selector{spanA, spanB, span.SinceReview(-1)} {
		if err := sess.SetSpan(ctx, sel); err != nil {
			t.Fatalf("SetSpan(%s): %v", sel.Display(), err)
		}
	}
	e.f.MustGit("update-ref", "-d", "refs/tags/tag-a")
	e.f.MustGit("update-ref", "-d", "refs/tags/tag-b")

	res, err := sess.StepSpan(ctx)
	if err != nil {
		t.Fatalf("StepSpan: %v", err)
	}
	if len(res.Skipped) != 2 {
		t.Fatalf("the step reported %d skipped stops, want both dead ones: %+v", len(res.Skipped), res.Skipped)
	}
	want := map[string]bool{spanA.Key(): true, spanB.Key(): true}
	for _, got := range res.Skipped {
		if !want[got.Selector.Key()] {
			t.Errorf("skipped %q, want one of the two tagged spans", got.Selector.Key())
		}
		if got.Reason == "" {
			t.Errorf("skipped %s with no reason", got.Selector.Display())
		}
	}
}

// When every other stop is dead too there is nowhere to go, and that is what gets said -- with the
// stops named, so the reviewer knows which spans to go and resurrect.
func TestStepSpanSaysSoWhenEveryOtherStopIsDead(t *testing.T) {
	e := newEnv(t)
	tagged := e.f.Commit("more work", gittest.WithFile("service.go",
		"package main\n\nfunc Lock() { tx() }\n"))
	e.f.MustGit("tag", "tag-a", tagged)
	tagged2 := e.f.Commit("even more", gittest.WithFile("service.go",
		"package main\n\nfunc Lock() { tx(); more() }\n"))
	e.f.MustGit("tag", "tag-b", tagged2)
	ctx := context.Background()

	// No review submissions, so the presets are not on the ring: three stops, two of them tags.
	sess := e.session(t, span.Selector{Base: span.Commit(e.f.Parent(e.f.Head())), Head: span.WorkingTree()})
	for _, sel := range []span.Selector{
		{Base: span.ChangesetBase(), Head: span.Commit("tag-a")},
		{Base: span.ChangesetBase(), Head: span.Commit("tag-b")},
		{Base: span.Commit(e.f.Parent(e.f.Head())), Head: span.WorkingTree()},
	} {
		if err := sess.SetSpan(ctx, sel); err != nil {
			t.Fatalf("SetSpan(%s): %v", sel.Display(), err)
		}
	}
	if _, total := sess.SpanPosition(); total != 3 {
		t.Fatalf("the ring holds %d stops, want the span it opened on and the two tagged ones", total)
	}
	held := sess.Span().Label
	e.f.MustGit("update-ref", "-d", "refs/tags/tag-a")
	e.f.MustGit("update-ref", "-d", "refs/tags/tag-b")

	res, err := sess.StepSpan(ctx)
	if err == nil {
		t.Fatalf("the step moved to %s with nowhere left to go", sess.Span().Label)
	}
	if sess.Span().Label != held {
		t.Errorf("a step with nowhere to go moved the session from %s to %s", held, sess.Span().Label)
	}
	if len(res.Skipped) != 2 {
		t.Errorf("the failure named %d skipped stops, want both: %+v", len(res.Skipped), res.Skipped)
	}
}

// `r` is the reviewer asking for the new pin. The span moves, the report says which ref moved
// where, and it says what that cost in reviewed marks (requirements §14). The stop on the span
// ring is rewritten rather than added: the reviewer chose `probe`, not `probe at abc123`.
func TestRefreshDriftRepinsAndSaysWhatItCost(t *testing.T) {
	e := newEnv(t)
	fork := e.f.RevParse("main")
	// One more commit on the branch, so service.go has two changes in the changeset and a
	// span that starts before them describes the file with a different diff.
	e.f.Commit("response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { tx() }\n"))
	probeStart := e.f.Parent(e.f.Head())
	e.f.MustGit("branch", "probe", probeStart)
	ctx := context.Background()

	sess := e.session(t, span.Selector{Base: span.Ref("refs/heads/probe"), Head: span.WorkingTree()})
	if !sess.Span().Live() {
		t.Fatal("a span ending at the working tree must be one you can review into")
	}
	i := indexOfFile(sess.Files(), "service.go")
	if i < 0 {
		t.Fatalf("no service.go in %v", filePaths(sess.Files()))
	}
	sess.Toggle(i)
	if reviewed, _ := sess.Count(); reviewed != 1 {
		t.Fatalf("reviewed = %d, want 1", reviewed)
	}

	if _, reset, err := sess.RefreshDrift(ctx); err == nil {
		t.Errorf("RefreshDrift with nothing drifted reset %d marks and no error, want a refusal", reset)
	}

	e.f.MustGit("update-ref", "refs/heads/probe", fork)
	if err := sess.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if sess.Span().From != probeStart {
		t.Fatalf("the span followed the ref to %s, want it pinned at %s", sess.Span().From, probeStart[:7])
	}

	moved, reset, err := sess.RefreshDrift(ctx)
	if err != nil {
		t.Fatalf("RefreshDrift: %v", err)
	}
	if len(moved) != 1 || moved[0].Name != "refs/heads/probe" {
		t.Fatalf("refreshed %v, want refs/heads/probe", moved)
	}
	if sess.Span().From != fork {
		t.Errorf("after refresh the base is %s, want %s", sess.Span().From, fork[:7])
	}
	if got := sess.Selector().Base; got.Kind != span.KindRef || got.OID != fork {
		t.Errorf("the selector carries %s @ %q, want the ref pinned at %s: a rescan must not "+
			"move the span again on its own", got.Kind, got.OID, fork[:7])
	}
	if reset != 1 {
		t.Errorf("reset = %d, want 1: service.go's diff within the span is a different piece of work", reset)
	}
	if reviewed, _ := sess.Count(); reviewed != 0 {
		t.Errorf("reviewed = %d after the refresh, want the mark to have stopped applying", reviewed)
	}
	if left := sess.Drifted(); len(left) != 0 {
		t.Errorf("Drifted() = %v after the refresh, want nothing", left)
	}
	pos, _ := sess.SpanPosition()
	if ring := sess.SpanRing()[pos-1]; ring.Base.Kind != span.KindRef || ring.Base.OID != fork {
		t.Errorf("the ring's stop still points at %q, want the refreshed pin", ring.Base.OID)
	}
}

// A ref endpoint is pinned when the session chooses it, and the pin is what the session keeps
// using: through a rescan, through the ref moving, and through the ref going away entirely.
// This is requirements §13 — the ground under a review in progress does not move — and it is
// what makes drift something the reviewer can act on instead of a thing that already happened.
func TestSessionStaysOnTheRefItPinned(t *testing.T) {
	e := newEnv(t)
	e.f.CommitReviewMarker(slug, "feedback")
	e.f.MustGit("branch", "probe", "HEAD")
	ctx := context.Background()

	sess := e.session(t, span.Selector{Base: span.ChangesetBase(), Head: span.Ref("refs/heads/probe")})
	pinned := sess.Span().To
	if got := sess.Selector().Head; got.Kind != span.KindRef || got.OID != pinned {
		t.Fatalf("the session's head is %s %q @ %q, want the ref pinned at %s",
			got.Kind, got.Name, got.OID, pinned[:7])
	}

	if moved := sess.CheckDrift(ctx); len(moved) != 0 {
		t.Fatalf("CheckDrift = %v on a span that has not had a chance to drift", moved)
	}

	moved := e.f.Commit("response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { tx() }\n"))
	e.f.MustGit("update-ref", "refs/heads/probe", moved)
	if err := sess.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if sess.Span().To != pinned {
		t.Errorf("the span followed the branch to %s, want it still pinned at %s", sess.Span().To, pinned[:7])
	}
	drift := sess.Drifted()
	if len(drift) != 1 {
		t.Fatalf("Drifted() = %v, want probe reported as moved", drift)
	}
	if drift[0].Name != "refs/heads/probe" || drift[0].Pinned != e.f.Short(pinned) || drift[0].Current != e.f.Short(moved) {
		t.Errorf("drift = %q, want refs/heads/probe from %s to %s", drift[0], e.f.Short(pinned), e.f.Short(moved))
	}

	// A ref that has gone is not drift: there is nothing to refresh to, and the commit the
	// reviewer chose is still the one on screen.
	e.f.ForceDeleteBranch("probe")
	if err := sess.Reload(ctx); err != nil {
		t.Fatalf("Reload after the delete: %v", err)
	}
	if sess.Span().To != pinned {
		t.Errorf("the span moved to %s when the ref was deleted", sess.Span().To)
	}
	if moved := sess.Drifted(); len(moved) != 0 {
		t.Errorf("Drifted() = %v after the ref was deleted, want nothing: the pin still resolves", moved)
	}
}

// A thread created during a review appears in the thread list (PRD §14's `T`).
func TestSessionThreadsAndChangesetAccessors(t *testing.T) {
	e := newEnv(t)
	sess := e.session(t, span.Full())

	threads, err := sess.Threads()
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(threads) != 0 {
		t.Errorf("Threads = %v, want none before a thread exists", threads)
	}

	e.f.WriteChangesetFile(slug, "concurrency-tests.md", "# Concurrency tests\n\nQuestion?\n")
	threads, err = sess.Threads()
	if err != nil {
		t.Fatalf("Threads after creating one: %v", err)
	}
	if len(threads) != 1 || !strings.HasSuffix(threads[0], "concurrency-tests.md") {
		t.Errorf("Threads = %v, want the new thread", threads)
	}
	if sess.Changeset().Slug != slug {
		t.Errorf("Changeset().Slug = %q, want %q", sess.Changeset().Slug, slug)
	}
	if sess.Repo() != e.repo {
		t.Error("Repo() does not return the repository the session was built with")
	}
	if sess.Summary().State == "" {
		t.Error("Summary() is empty; the header needs the derived state")
	}
}

// PRD §17: a review-relative span cannot exist before the first review, and the TUI
// must surface that rather than open an empty screen.
func TestSessionSpanErrorSurfaces(t *testing.T) {
	e := newEnv(t)

	summary, err := lifecycle.SummarizeHEAD(context.Background(), e.repo, e.cs.Slug, e.cs.Base)
	if err != nil {
		t.Fatalf("SummarizeHEAD: %v", err)
	}
	if _, err := tui.NewSession(context.Background(), tui.Options{
		Repo: e.repo, Changeset: e.cs, Summary: summary, Span: span.SinceReview(-1),
	}); err == nil {
		t.Error("NewSession succeeded with --unreviewed and no reviews")
	}
}

// A historical span is not a dead end. `v` walks out of it the way it walks out of anywhere else,
// and because the walk reaches every stop, a span you can review into is a bounded number of presses
// away. What it deliberately does not do is jump there in one press: that was the rule that made the
// second historical span on the ring unreachable. Getting to a reviewable span in one keystroke is
// `V`, whose head column always offers `Current`.
func TestSessionStepFromHistoricalWalksOutToAReviewableSpan(t *testing.T) {
	e := newEnv(t)
	e.f.CommitReviewMarker(slug, "feedback")
	ctx := context.Background()

	sess := e.session(t, span.Selector{Base: span.ChangesetBase(), Head: span.Review(-1)})
	if !sess.Span().Historical() {
		t.Fatalf("the session opened live, want a historical span")
	}
	custom := span.Selector{Base: span.Commit(e.f.Parent(e.f.Head())), Head: span.WorkingTree()}
	if err := sess.SetSpan(ctx, custom); err != nil {
		t.Fatalf("SetSpan(custom): %v", err)
	}
	if err := sess.SetSpan(ctx, span.Selector{Base: span.ChangesetBase(), Head: span.Review(-1)}); err != nil {
		t.Fatalf("SetSpan(history): %v", err)
	}
	ring := sess.SpanRing()
	pos, total := sess.SpanPosition()
	want := ring[pos%total].Display()

	res, err := sess.StepSpan(ctx)
	if err != nil {
		t.Fatalf("StepSpan from history: %v", err)
	}
	if res.Pos != pos%total+1 {
		t.Errorf("v from history landed on stop %d, want the next stop %d", res.Pos, pos%total+1)
	}
	if got := sess.Selector().Display(); got != want {
		t.Errorf("v from history went to %s, want the ring's next stop %s \u2014 a walk, not a jump", got, want)
	}

	for presses := 1; !sess.Span().Live(); presses++ {
		if presses > total {
			t.Fatalf("v walked %d stops from history and never reached a span it could review", presses)
		}
		if _, err := sess.StepSpan(ctx); err != nil {
			t.Fatalf("StepSpan: %v", err)
		}
	}
}

// "unreviewed" is the one word the screen substitutes for a span label, so it has to be
// earned: it means everything after the newest submission. A span based on an older review
// starts inside work that has already been reviewed, and calling that unreviewed is a claim
// the reviewer cannot check from the screen they are reading.
func TestUnreviewedMeansTheNewestReview(t *testing.T) {
	e := newEnv(t)
	e.f.CommitReviewMarker(slug, "feedback", gittest.WithFile("notes-0.md", "note\n"))
	e.f.Commit("response 0", gittest.WithFile("service.go", "package main\n\nfunc Lock() { tx0() }\n"))
	e.f.CommitReviewMarker(slug, "feedback", gittest.WithFile("notes-1.md", "note\n"))
	e.f.Commit("response 1", gittest.WithFile("service.go", "package main\n\nfunc Lock() { tx1() }\n"))

	newest := e.session(t, span.SinceReview(-1))
	if !newest.Unreviewed() {
		t.Errorf("Unreviewed() = false for the span after the newest review (%s)", newest.Span().Label)
	}

	older := e.session(t, span.SinceReview(-2))
	if older.Unreviewed() {
		t.Errorf("Unreviewed() = true for a span based on an older review (%s)", older.Span().Label)
	}
	if older.Span().Label == newest.Span().Label {
		t.Errorf("both spans are labelled %q, so the header could not tell them apart even in principle",
			older.Span().Label)
	}

	// The same holds for a review named by index, which is how the picker names anything
	// beyond the most recent three.
	byIndex := e.session(t, span.Selector{Base: span.Review(0), Head: span.WorkingTree()})
	if byIndex.Unreviewed() {
		t.Errorf("Unreviewed() = true for a span based on review 0 of 2 (%s)", byIndex.Span().Label)
	}
	if e.session(t, span.Full()).Unreviewed() {
		t.Error("Unreviewed() = true for the full changeset")
	}
}

// Marks are kept outside the working tree so a review can be resumed. PRD §16 keeps them
// out of the review artifact, which they still are: nothing here is committed, shared, or
// visible to `git status`.
func TestReviewedMarksResumeInALaterSession(t *testing.T) {
	e := newEnv(t)
	first := e.session(t, span.Full())
	first.Toggle(0)
	first.Toggle(2)
	if err := first.SaveMarks(context.Background()); err != nil {
		t.Fatalf("SaveMarks: %v", err)
	}
	want := filePaths(first.Files())

	second := e.session(t, span.Full())
	if got := second.Resumed(); got != 2 {
		t.Errorf("Resumed() = %d, want 2", got)
	}
	marked := 0
	for _, f := range second.Files() {
		if f.Reviewed {
			marked++
		}
	}
	if marked != 2 {
		t.Errorf("%d files came back reviewed, want 2 (files: %v)", marked, filePaths(second.Files()))
	}
	if got := filePaths(second.Files()); len(got) != len(want) {
		t.Fatalf("file list changed between sessions: %v vs %v", got, want)
	}

	// The marks must not be written into the working tree.
	if !e.f.Clean() {
		t.Error("saving marks dirtied the working tree; marks live under the git directory")
	}
}

// Marks belong to the diff they were made against. Once the author commits, the files the
// reviewer read are not the files that exist, so nothing may come back marked.
func TestMarksDoNotResumeOnceTheCodeHasChanged(t *testing.T) {
	e := newEnv(t)
	first := e.session(t, span.Full())
	for i := range first.Files() {
		first.Toggle(i)
	}
	if err := first.SaveMarks(context.Background()); err != nil {
		t.Fatalf("SaveMarks: %v", err)
	}

	e.f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { tx() }\n"))

	second := e.session(t, span.Full())
	for _, f := range second.Files() {
		if f.Reviewed {
			t.Errorf("%q came back reviewed after the code changed", f.Path)
		}
	}
}

// Clearing every mark is a state worth remembering; otherwise the old set would reappear.
func TestClearingEveryMarkIsRemembered(t *testing.T) {
	e := newEnv(t)
	first := e.session(t, span.Full())
	first.Toggle(0)
	if err := first.SaveMarks(context.Background()); err != nil {
		t.Fatalf("SaveMarks: %v", err)
	}
	first.Toggle(0)
	if err := first.SaveMarks(context.Background()); err != nil {
		t.Fatalf("SaveMarks after clearing: %v", err)
	}

	second := e.session(t, span.Full())
	for _, f := range second.Files() {
		if f.Reviewed {
			t.Errorf("%q is marked in a new session after every mark was cleared", f.Path)
		}
	}
}

// The walk is a walk. This is the bug the redirect caused: `v` jumped out of a read-only span on
// every step, so a ring holding two historical spans closed a loop between the last reviewable stop
// and the first historical one -- the second was unreachable by any number of presses, which is what
// it looked like from the keyboard.
func TestSessionStepSpanReachesEveryStop(t *testing.T) {
	e := newEnv(t)
	e.f.CommitReviewMarker(slug, "feedback")
	ctx := context.Background()
	sess := e.session(t, span.Full())
	first := e.f.Commit("author response 1", gittest.WithFile("service.go",
		"package main\n\nfunc Lock() { tx() }\n"))
	second := e.f.Commit("author response 2", gittest.WithFile("service.go",
		"package main\n\nfunc Lock() { tx(); more() }\n"))

	for _, commit := range []string{first, second} {
		if err := sess.SetSpan(ctx, span.Selector{Base: span.ChangesetBase(), Head: span.Commit(commit)}); err != nil {
			t.Fatalf("SetSpan(%s): %v", commit[:7], err)
		}
	}
	_, total := sess.SpanPosition()
	if total != 4 {
		t.Fatalf("the ring holds %d stops, want full, unreviewed and the two historical spans", total)
	}

	visited := map[int]bool{}
	for i := 0; i < total; i++ {
		res, err := sess.StepSpan(ctx)
		if err != nil {
			t.Fatalf("StepSpan %d: %v", i+1, err)
		}
		visited[res.Pos] = true
		if len(res.Skipped) != 0 {
			t.Fatalf("step %d skipped %d stops; every one of these resolves", i+1, len(res.Skipped))
		}
	}
	if len(visited) != total {
		t.Errorf("a turn of the ring visited %d of %d stops (%v); a step that always escapes leaves stops unreachable",
			len(visited), total, visited)
	}
}
