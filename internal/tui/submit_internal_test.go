package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gitpr/internal/changeset"
	"gitpr/internal/git"
	"gitpr/internal/gittest"
	"gitpr/internal/lifecycle"
	"gitpr/internal/model"
	"gitpr/internal/span"
)

// Submitting is the end of the session, not a status line inside it: the review
// is committed and refs/reviews/<cs> has moved, so the list on screen now
// describes a span that no longer means what it did, and a second `s` would
// submit the same state twice.
func TestSubmitKeyEndsTheSession(t *testing.T) {
	ctx := context.Background()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n\nfunc main() {}\n"))
	const slug = "booking"
	f.CreateBranch(slug)
	f.CommitChangeset(slug, "main")
	f.Commit("implement", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))

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

	m := reviewModel{ctx: ctx, sess: sess, width: 80, height: 24}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = updated.(reviewModel)
	if m.mode != modeSubmit {
		t.Fatalf("s did not enter submit mode (mode = %v)", m.mode)
	}
	if m.quitting {
		t.Fatal("s alone ended the session; only an outcome key may")
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m = updated.(reviewModel)
	if !m.quitting {
		t.Error("submitting a block review did not end the session")
	}
	if cmd == nil {
		t.Error("submitting returned no command, want tea.Quit")
	}
	if !strings.Contains(m.submitted, "block") {
		t.Errorf("submitted = %q, want a summary naming the outcome", m.submitted)
	}

	// The submission is real, not just a state change in the model.
	got, err := lifecycle.SummarizeHEAD(ctx, repo, cs.Slug, cs.Base)
	if err != nil {
		t.Fatalf("SummarizeHEAD after submit: %v", err)
	}
	if got.State != model.StateBlocked {
		t.Errorf("state after submit = %s, want BLOCKED", got.State)
	}
}

// Esc in submit mode is the other half: backing out must keep the session alive.
func TestEscInSubmitModeKeepsTheSessionOpen(t *testing.T) {
	ctx := context.Background()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n\nfunc main() {}\n"))
	const slug = "booking"
	f.CreateBranch(slug)
	f.CommitChangeset(slug, "main")
	f.Commit("implement", gittest.WithFile("service.go", "package main\n"))

	repo := &git.Repo{Dir: f.Dir()}
	cs, err := changeset.ForBranch(repo, slug)
	if err != nil {
		t.Fatalf("ForBranch: %v", err)
	}
	summary, err := lifecycle.SummarizeHEAD(ctx, repo, cs.Slug, cs.Base)
	if err != nil {
		t.Fatalf("SummarizeHEAD: %v", err)
	}
	sess, err := NewSession(ctx, Options{Repo: repo, Changeset: cs, Summary: summary})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	m := reviewModel{ctx: ctx, sess: sess, width: 80, height: 24, mode: modeSubmit}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(reviewModel)
	if m.quitting {
		t.Error("Esc ended the session; it should cancel the submission")
	}
	if m.mode != modeFiles {
		t.Errorf("mode = %v, want the file list back", m.mode)
	}
}
