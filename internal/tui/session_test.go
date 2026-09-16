package tui_test

import (
	"context"
	"strings"
	"testing"

	"gitpr/internal/changeset"
	"gitpr/internal/git"
	"gitpr/internal/gittest"
	"gitpr/internal/lifecycle"
	"gitpr/internal/span"
	"gitpr/internal/tui"
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
	cs, err := changeset.ForBranch(e.repo, slug)
	if err != nil {
		t.Fatalf("ForBranch: %v", err)
	}
	e.cs = cs
	return e
}

func (e *env) session(t *testing.T, opts span.Options) *tui.Session {
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

	sess := e.session(t, span.Options{})

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
	if sess.ReviewRef() != "refs/reviews/"+slug {
		t.Errorf("ReviewRef() = %q, want refs/reviews/%s", sess.ReviewRef(), slug)
	}
	if sess.AboutPath() != "changesets/"+slug+"/ABOUT.md" {
		t.Errorf("AboutPath() = %q", sess.AboutPath())
	}
}

func TestSessionToggleMarksFilesReviewed(t *testing.T) {
	e := newEnv(t)
	sess := e.session(t, span.Options{})

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
// during the current session, gitpr should ideally reset it to unreviewed."
func TestSessionResetsReviewedMarkWhenFileDiffChanges(t *testing.T) {
	e := newEnv(t)
	sess := e.session(t, span.Options{})

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
	sess := e.session(t, span.Options{})
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

// PRD §14's `v` binding toggles between the full changeset and the unreviewed span,
// and PRD §17.2 makes the unreviewed span the work done since the latest review.
func TestSessionToggleSpanSwitchesBetweenFullAndUnreviewed(t *testing.T) {
	e := newEnv(t)
	sess := e.session(t, span.Options{})

	if sess.CanToggleSpan() {
		t.Error("CanToggleSpan() = true before any review submission")
	}
	if err := sess.ToggleSpan(context.Background()); err == nil {
		t.Error("ToggleSpan succeeded with no reviews to compare against")
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

	if err := sess.ToggleSpan(context.Background()); err != nil {
		t.Fatalf("ToggleSpan: %v", err)
	}
	if !sess.Unreviewed() {
		t.Error("Unreviewed() = false after toggling to the unreviewed span")
	}
	files := filePaths(sess.Files())
	if len(files) != 1 || files[0] != "service.go" {
		t.Errorf("unreviewed span Files() = %v, want only the file the review touched", files)
	}
	if got := sess.Span().Kind; got != span.SinceReview {
		t.Errorf("Span().Kind = %s, want since-review", got)
	}

	// Marks are per-span content: switching spans must not silently keep marks for
	// a file whose diff in the new span was never looked at.
	sess.Toggle(indexOfFile(sess.Files(), "service.go"))
	if err := sess.ToggleSpan(context.Background()); err != nil {
		t.Fatalf("ToggleSpan back: %v", err)
	}
	if sess.Unreviewed() {
		t.Error("Unreviewed() = true after toggling back to the full span")
	}
	if got := sess.Span().Kind; got != span.Full {
		t.Errorf("Span().Kind = %s, want full", got)
	}
	if len(sess.Files()) < 3 {
		t.Errorf("full span Files() = %v, want the whole changeset again", filePaths(sess.Files()))
	}
}

// A thread created during a review appears in the thread list (PRD §14's `T`).
func TestSessionThreadsAndChangesetAccessors(t *testing.T) {
	e := newEnv(t)
	sess := e.session(t, span.Options{})

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
		Repo: e.repo, Changeset: e.cs, Summary: summary, Span: span.Options{Unreviewed: true},
	}); err == nil {
		t.Error("NewSession succeeded with --unreviewed and no reviews")
	}
}

// `review reopen` can open a session on the span a submission covered, which only
// happens when nothing landed after it. `v` must then have somewhere useful to go.
func TestSessionToggleSpanFromCoveredGoesToTheFullChangeset(t *testing.T) {
	e := newEnv(t)
	e.f.Write("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n")
	e.f.CommitReviewMarker(slug, "feedback")

	sess := e.session(t, span.Options{Covered: true})
	if sess.Unreviewed() {
		t.Error("Unreviewed() = true on a span that ends at the submission")
	}
	covered := filePaths(sess.Files())
	if len(covered) == 0 {
		t.Fatal("the covered span listed no files")
	}

	if err := sess.ToggleSpan(context.Background()); err != nil {
		t.Fatalf("ToggleSpan: %v", err)
	}
	if sess.Unreviewed() {
		t.Error("toggling landed on the since-review span, which is empty by construction here")
	}
	if got := len(filePaths(sess.Files())); got < len(covered) {
		t.Errorf("full span listed %d file(s), want at least the covered span's %d", got, len(covered))
	}
}
