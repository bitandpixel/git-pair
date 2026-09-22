package tui

import (
	"context"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/gittest"
	"gitpair/internal/lifecycle"
	"gitpair/internal/span"
)

// newFileListModel builds a session over a changeset with three files, so cursor
// behaviour can be tested against the model directly.
func newFileListModel(t *testing.T) reviewModel {
	t.Helper()
	return newFileListModelWith(t, map[string]string{
		"service.go": "package main\n\nfunc Lock() {}\n",
		"handler.go": "package main\n\nfunc Serve() {}\n",
	})
}

// newFileListModelWith is that fixture with the span's files chosen: a test that needs a tree of forty
// rows says so, and gets a real repository for them. Rows are rebuilt from the session on every key,
// so rows added to the model by hand are gone before the key arrives.
func newFileListModelWith(t *testing.T, files map[string]string) reviewModel {
	t.Helper()
	ctx := context.Background()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n\nfunc main() {}\n"))
	const slug = "booking"
	f.CreateBranch(slug)
	f.CommitChangeset(slug, "main")
	f.Commit("implement", gittest.WithFiles(files))
	// Two threads exist as working files, which is how a reviewer leaves them between
	// sessions: untracked until the review is submitted, listed either way.
	f.Write(filepath.Join("changesets", slug, "locking.md"), "# Thread: locking\n\nWhy the mutex?\n")
	f.Write(filepath.Join("changesets", slug, "naming.md"), "# Thread: naming\n\nServe or Handle?\n")

	repo := &git.Repo{Dir: f.Dir()}
	cs, err := changeset.Current(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	summary, err := lifecycle.SummarizeHEAD(ctx, repo, cs.Slug, cs.Base)
	if err != nil {
		t.Fatalf("SummarizeHEAD: %v", err)
	}
	sess, err := NewSession(ctx, Options{Repo: repo, Changeset: cs, Summary: summary, Span: span.Full()})
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
	// A file row on purpose: the top of the list is a directory now, and Space on one of those
	// marks everything under it, which is a different test.
	m = cursorOn(t, m, "service.go")
	at := m.cursor

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(reviewModel)

	if m.cursor != at {
		t.Errorf("cursor moved from %d to %d after marking a file reviewed", at, m.cursor)
	}
	if got := markedFiles(m.sess); len(got) != 1 || got[0] != "service.go" {
		t.Errorf("marking one file left %v marked, want service.go alone", got)
	}

	// Pressing space again on the same row clears it, still without moving.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(reviewModel)
	if m.cursor != at {
		t.Errorf("cursor moved from %d to %d when unmarking", at, m.cursor)
	}
	if got := markedFiles(m.sess); len(got) != 0 {
		t.Errorf("the second space left %v marked, want nothing", got)
	}
}

// k at the top of the column used to drive the cursor negative, and View indexes rows by cursor, so the
// program died with an index-out-of-range panic. The column is two windows over one list, so the guard is
// the same arithmetic twice: the box's rows have a floor and a ceiling of their own, the tree's have
// theirs, and the edge the two share is the only place a step crosses from one into the other.
func TestNavigationStopsAtBothEndsOfTheColumn(t *testing.T) {
	m := newFileListModel(t)
	total, box := len(m.fileRows()), len(m.metaRows())

	up, down := tea.KeyMsg{Type: tea.KeyUp}, tea.KeyMsg{Type: tea.KeyDown}
	// Up past the tree's top crosses into the box and then walks it: the tree's cursor stops at its first
	// row, the box's stops at its own, and no index ever leaves the rows it belongs to.
	for i := 0; i < total+box+3; i++ {
		updated, _ := m.Update(up)
		m = updated.(reviewModel)
		if m.cursor != 0 {
			t.Fatalf("cursor = %d after %d presses of up, want 0", m.cursor, i+1)
		}
		if m.focus == focusMeta && m.metaCursor < m.metaStart {
			t.Fatalf("box cursor = %d after %d presses of up, want at least the box's first row %d",
				m.metaCursor, i+1, m.metaStart)
		}
		if m.scroll < 0 {
			t.Fatalf("scroll = %d after %d presses of up, want >= 0", m.scroll, i+1)
		}
		_ = m.View()
	}
	if !m.metaHasFocus() || m.metaCursor != m.metaStart {
		t.Errorf("up past the top of the column left %v on %d, want the box on its first row %d",
			m.focus, m.metaCursor, m.metaStart)
	}
	// Down the whole thing again: the box's rows, the crossing, then the tree's, stopping on the tree's
	// last row rather than past it.
	for i := 0; i < total+box+3; i++ {
		updated, _ := m.Update(down)
		m = updated.(reviewModel)
		if m.focus == focusFiles && m.cursor > total-1 {
			t.Fatalf("cursor = %d after %d presses of down, want at most %d", m.cursor, i+1, total-1)
		}
		if m.focus == focusMeta && m.metaCursor < m.metaStart {
			t.Fatalf("box cursor = %d after %d presses of down, want a box row", m.metaCursor, i+1)
		}
		if m.scroll < 0 {
			t.Fatalf("scroll = %d after %v, want >= 0", m.scroll, down)
		}
		_ = m.View()
	}
	if m.focus != focusFiles || m.cursor != total-1 {
		t.Errorf("down past the bottom of the column left %v on %d, want the tree on %d",
			m.focus, m.cursor, total-1)
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
