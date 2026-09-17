package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/gittest"
	"gitpair/internal/lifecycle"
	"gitpair/internal/span"
)

// Reviewed marks are the one piece of review state that lives outside the repository, and the
// span ring made them fragile: `v` rebuilds the file list for the span being entered, whose
// diff keys belong to that span, so the marks made in the span being left existed nowhere but
// the store — which the next save then replaced. These are the tests for the round trip.

// marksEnv is one repository with a review in it and work after the review, so the full
// changeset and the unreviewed span both exist and both end at HEAD. Unlike the other
// fixtures it hands back a factory: two sessions over the same commit is the case that
// matters here, and each new fixture would bring new commit ids with it.
func marksEnv(t *testing.T) (func(span.Selector) *Session, *gittest.Fixture) {
	t.Helper()
	ctx := context.Background()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n\nfunc main() {}\n"))
	f.CreateBranch(readonlySlug)
	f.CommitChangeset(readonlySlug, "main")
	f.Commit("implement", gittest.WithFiles(map[string]string{
		"service.go": "package main\n\nfunc Lock() {}\n",
		"handler.go": "package main\n\nfunc Serve() {}\n",
	}))
	f.CommitReviewMarker(readonlySlug, "feedback")
	f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { tx() }\n"))

	repo := &git.Repo{Dir: f.Dir()}
	cs, err := changeset.Current(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	return func(sel span.Selector) *Session {
		summary, err := lifecycle.SummarizeHEAD(ctx, repo, cs.Slug, cs.Base)
		if err != nil {
			t.Fatalf("SummarizeHEAD: %v", err)
		}
		sess, err := NewSession(ctx, Options{Repo: repo, Changeset: cs, Summary: summary, Span: sel})
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		return sess
	}, f
}

func markedFiles(sess *Session) []string {
	var out []string
	for _, file := range sess.Files() {
		if file.Reviewed {
			out = append(out, file.Path)
		}
	}
	return out
}

// cursorOn puts the cursor on a file row by path, so the test presses Space on the file it
// means rather than on an offset that moves when the fixture changes.
func cursorOn(t *testing.T, m reviewModel, path string) reviewModel {
	t.Helper()
	for i, r := range m.rows {
		if r.kind == rowFile && r.path == path {
			m.cursor = i
			return m
		}
	}
	t.Fatalf("no file row for %s among %d rows", path, len(m.rows))
	return m
}

// The defect, at the level a reviewer experiences it: mark two files, press `v` twice, and the
// marks are back. One of the two files is untouched by the review and so not even in the span
// `v` steps to; the other is in it, with a different diff, and must not be marked there.
func TestMarksComeBackWhenTheSpanComesBack(t *testing.T) {
	newSession, _ := marksEnv(t)
	sess := newSession(span.Full())
	opening := sess.Span().Label
	m := reviewModel{ctx: context.Background(), sess: sess, width: 100, height: 24}
	m.refresh()

	m = press(cursorOn(t, m, "service.go"), tea.KeySpace)
	m = press(cursorOn(t, m, "handler.go"), tea.KeySpace)
	if got := markedFiles(m.sess); len(got) != 2 {
		t.Fatalf("marked %v before stepping anywhere, want both files", got)
	}

	m = pressRune(m, 'v')
	if m.sess.Span().Label == opening {
		t.Fatalf("v did not change the span (%s), so the round trip proves nothing", m.sess.Span().Label)
	}
	if got := markedFiles(m.sess); len(got) != 0 {
		t.Errorf("marks from the full changeset appeared in another span: %v", got)
	}

	m = pressRune(m, 'v')
	if m.sess.Span().Label != opening {
		t.Fatalf("the second v landed on %s, want the span it started on", m.sess.Span().Label)
	}
	if !m.sess.Span().Live() {
		t.Fatal("the round trip ended on a read-only span")
	}
	want := map[string]bool{"service.go": true, "handler.go": true}
	got := markedFiles(m.sess)
	if len(got) != len(want) {
		t.Fatalf("after a span round trip %d files are marked (%v), want %v", len(got), got, keysOf(want))
	}
	for _, path := range got {
		if !want[path] {
			t.Errorf("%s came back marked, which no one marked in this span", path)
		}
		delete(want, path)
	}
	for path := range want {
		t.Errorf("%s lost its reviewed mark across a span round trip (marked: %v)", path, got)
	}
}

func keysOf(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// Marks are keyed on the commit under review, and the two spans a reviewer toggles between
// most often — full changeset and unreviewed — end at the same commit. Saving what one span
// shows used to replace what the other had recorded, so the marks of a session that is still
// open could be erased from disk by the same session.
func TestSavingOneSpanLeavesTheOtherSpansMarks(t *testing.T) {
	newSession, f := marksEnv(t)
	ctx := context.Background()

	full := newSession(span.Full())
	i := fileIndex(full.Files(), "service.go")
	full.Toggle(i)
	if err := full.SaveMarks(ctx); err != nil {
		t.Fatalf("SaveMarks: %v", err)
	}

	unreviewed := newSession(span.SinceReview(-1))
	if unreviewed.Span().To != full.Span().To {
		t.Fatalf("the two spans end at different commits, so this test needs a different fixture")
	}
	if err := unreviewed.SaveMarks(ctx); err != nil {
		t.Fatalf("SaveMarks: %v", err)
	}

	again := newSession(span.Full())
	if !again.Files()[fileIndex(again.Files(), "service.go")].Reviewed {
		t.Errorf("the unreviewed span's save erased the full span's marks: %v marked, want service.go",
			markedFiles(again))
	}
	if !f.Clean() {
		t.Error("marks reached the working tree")
	}
}

func fileIndex(files []File, path string) int {
	for i, file := range files {
		if file.Path == path {
			return i
		}
	}
	return -1
}
