package tui

import (
	"context"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gitpr/internal/changeset"
	"gitpr/internal/git"
	"gitpr/internal/gittest"
	"gitpr/internal/lifecycle"
	"gitpr/internal/span"
)

// newFileListModel builds a session over a changeset with three files, so cursor
// behaviour can be tested against the model directly.
func newFileListModel(t *testing.T) reviewModel {
	t.Helper()
	ctx := context.Background()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n\nfunc main() {}\n"))
	const slug = "booking"
	f.CreateBranch(slug)
	f.CommitChangeset(slug, "main")
	f.Commit("implement", gittest.WithFiles(map[string]string{
		"service.go": "package main\n\nfunc Lock() {}\n",
		"handler.go": "package main\n\nfunc Serve() {}\n",
	}))
	// Two threads exist as working files, which is how a reviewer leaves them between
	// sessions: untracked until the review is submitted, listed either way.
	f.Write(filepath.Join("changesets", slug, "locking.md"), "# Thread: locking\n\nWhy the mutex?\n")
	f.Write(filepath.Join("changesets", slug, "naming.md"), "# Thread: naming\n\nServe or Handle?\n")

	repo := &git.Repo{Dir: f.Dir()}
	cs, err := changeset.ForBranch(repo, slug)
	if err != nil {
		t.Fatalf("ForBranch: %v", err)
	}
	summary, err := lifecycle.SummarizeHEAD(ctx, repo, cs.Slug, cs.Base)
	if err != nil {
		t.Fatalf("SummarizeHEAD: %v", err)
	}
	sess, err := NewSession(ctx, Options{Repo: repo, Changeset: cs, Summary: summary, Span: span.Options{}})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if len(sess.Files()) < 3 {
		t.Fatalf("fixture has %d files, want at least 3", len(sess.Files()))
	}
	// The navigable list is built from the session, so a model that tests are handed must
	// have it built too, exactly as Run does.
	m := reviewModel{ctx: ctx, sess: sess, width: 80, height: 24, threadsOpen: true}
	m.refresh()
	return m
}

// Marking a file is not a navigation command. Advancing the cursor after every mark
// means the file you just marked cannot be looked at again without pressing k, and
// the cursor drifts away from wherever you were reading.
func TestSpaceMarksTheFileAndLeavesTheCursorOnIt(t *testing.T) {
	m := newFileListModel(t)
	at := m.cursor

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(reviewModel)

	if m.cursor != at {
		t.Errorf("cursor moved from %d to %d after marking a file reviewed", at, m.cursor)
	}
	files := m.sess.Files()
	if !files[at].Reviewed {
		t.Error("the file under the cursor was not marked reviewed")
	}
	if files[at+1].Reviewed {
		t.Error("marking one file marked the next one too")
	}

	// Pressing space again on the same row clears it, still without moving.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(reviewModel)
	if m.cursor != at {
		t.Errorf("cursor moved from %d to %d when unmarking", at, m.cursor)
	}
	if m.sess.Files()[at].Reviewed {
		t.Error("the second space did not clear the mark")
	}
}

// k at the top of the list used to drive the cursor negative, and View indexes rows by
// cursor, so the program died with an index-out-of-range panic. The list now runs past the
// files into the changeset section, so "both ends" means the ends of that whole list.
func TestNavigationStopsAtBothEndsOfTheList(t *testing.T) {
	m := newFileListModel(t)
	total := len(m.rows)

	up, down := tea.KeyMsg{Type: tea.KeyUp}, tea.KeyMsg{Type: tea.KeyDown}
	for i := 0; i < total+3; i++ {
		updated, _ := m.Update(up)
		m = updated.(reviewModel)
		if m.cursor != 0 {
			t.Fatalf("cursor = %d after %d presses of up, want 0", m.cursor, i+1)
		}
		if m.scroll < 0 {
			t.Fatalf("scroll = %d after %d presses of up, want >= 0", m.scroll, i+1)
		}
		_ = m.View()
	}
	for i := 0; i < total+3; i++ {
		updated, _ := m.Update(down)
		m = updated.(reviewModel)
		if m.cursor > total-1 {
			t.Fatalf("cursor = %d after %d presses of down, want at most %d", m.cursor, i+1, total-1)
		}
		if m.scroll < 0 {
			t.Fatalf("scroll = %d after %d presses of down, want >= 0", m.scroll, i+1)
		}
		_ = m.View()
	}
	if m.cursor != total-1 {
		t.Errorf("cursor = %d after pressing down past the bottom, want %d", m.cursor, total-1)
	}
}

// A span with no changed files still has the changeset section to navigate, so the cursor
// stays inside that. The genuinely empty list is only reachable from a unit test, and clamp
// has to leave the cursor at zero rather than index anything.
func TestNavigationOnAnEmptyListDoesNotPanic(t *testing.T) {
	m := newFileListModel(t)
	m.sess.files = nil
	m.refresh()
	if len(m.rows) == 0 {
		t.Fatal("a span with no files still has the changeset section to navigate")
	}
	for _, k := range []tea.KeyMsg{{Type: tea.KeyUp}, {Type: tea.KeyDown}, {Type: tea.KeySpace}} {
		updated, _ := m.Update(k)
		m = updated.(reviewModel)
		if m.cursor < 0 || m.cursor >= len(m.rows) {
			t.Errorf("cursor=%d outside the %d rows left after %v", m.cursor, len(m.rows), k)
		}
		if m.scroll < 0 {
			t.Errorf("scroll=%d after %v", m.scroll, k)
		}
		_ = m.View()
	}

	m.rows, m.cursor, m.scroll = nil, 4, 4
	m.clamp()
	if m.cursor != 0 || m.scroll != 0 {
		t.Errorf("cursor=%d scroll=%d with no rows at all, want 0/0", m.cursor, m.scroll)
	}
}
