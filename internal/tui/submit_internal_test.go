package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/gittest"
	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
	"gitpair/internal/reviewops"
	"gitpair/internal/span"
)

// Submitting is the end of the session, not a status line inside it: the review
// is committed and the changeset's archive ref has moved, so the list on screen now
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
	cs, err := changeset.Current(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	summary, err := lifecycle.SummarizeHEAD(ctx, repo, cs.Slug, cs.Base)
	if err != nil {
		t.Fatalf("SummarizeHEAD: %v", err)
	}
	// The screen performs no submission of its own: it calls the path its caller supplied, and that is the
	// whole reason the path is injected. `review submit` and this screen are two surfaces for one action, and
	// a reviewer's `pre-submit` hooks fire on the path — so a second path would be a surface where they
	// silently do not. The counter here is the assertion that the screen went through it once, with the
	// outcome the reviewer pressed.
	var submitted []model.Outcome
	sess, err := NewSession(ctx, Options{Repo: repo, Changeset: cs, Summary: summary, Span: span.Full(),
		Submit: func(o model.Outcome) (reviewops.Result, error) {
			submitted = append(submitted, o)
			return reviewops.Submit(ctx, repo, cs, o, "", true,
				changeset.MeasureSubmission(ctx, repo, cs, changeset.DefaultBranchRef{}, ""),
				changeset.DefaultBranchRef{})
		}})
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
	if len(submitted) != 1 || submitted[0] != model.OutcomeBlock {
		t.Errorf("the caller's submission path was called with %v, want exactly one block", submitted)
	}
}

// Esc in submit mode is the other half: backing out must keep the session alive.
// submitModel is a session sitting on the submit prompt, which is a decision with nothing left to
// review: the fixture is a changeset with work in it and no submission yet.
func submitModel(t *testing.T) reviewModel {
	t.Helper()
	ctx := context.Background()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n\nfunc main() {}\n"))
	const slug = "booking"
	f.CreateBranch(slug)
	f.CommitChangeset(slug, "main")
	f.Commit("implement", gittest.WithFile("service.go", "package main\n"))

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
	return reviewModel{ctx: ctx, sess: sess, width: 80, height: 24, mode: modeSubmit}
}

func TestEscInSubmitModeKeepsTheSessionOpen(t *testing.T) {
	m := submitModel(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(reviewModel)
	if m.quitting {
		t.Error("Esc ended the session; it should cancel the submission")
	}
	if m.mode != modeFiles {
		t.Errorf("mode = %v, want the file list back", m.mode)
	}
}

// Ctrl-C is the other key a reviewer reaches for at a prompt, and it used to do nothing here at
// all. It leaves without submitting: the review is not committed, so the session has nothing to
// stay open for.
func TestCtrlCInSubmitModeLeavesWithoutSubmitting(t *testing.T) {
	m := pressKey(t, submitModel(t), tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.quitting {
		t.Error("ctrl-c at the submit prompt did not leave the session")
	}
	if m.submitted != "" {
		t.Errorf("ctrl-c submitted %q, want nothing submitted", m.submitted)
	}
}

// A refused submission is the other half of the injected path. The screen calls the caller's submit, the
// caller's submit runs the reviewer's `pre-submit` hooks, and a hook can say no. When it does the reviewer is
// mid-action: their comments, their ABOUT edit and their staged files all exist, and a submission that ended
// the session would throw them away. So a refusal is a message, not an exit — the screen stays where it was,
// and the only thing lost is the verdict that was refused.
func TestRefusedSubmissionStaysOpenWithTheReason(t *testing.T) {
	m := submitModel(t)
	const reason = "hook \"01-suite\" at pre-submit refused the operation with exit 1; " +
		"the review was not recorded, and your staged changes are still staged"
	m.sess.submit = func(model.Outcome) (reviewops.Result, error) {
		return reviewops.Result{}, errors.New(reason)
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m = updated.(reviewModel)

	if m.quitting || cmd != nil {
		t.Errorf("a refused submission ended the session (quitting = %v, cmd = %v)", m.quitting, cmd)
	}
	if m.mode != modeFiles {
		t.Errorf("mode = %v, want the file list back so the review is still editable", m.mode)
	}
	if m.submitted != "" {
		t.Errorf("submitted = %q, want nothing submitted", m.submitted)
	}
	// The reason has to survive the screen's own formatting: it is a long sentence, and a status area that
	// swallowed it would leave the reviewer with "something failed" and no way to act.
	view := m.View()
	for _, want := range []string{"01-suite", "pre-submit", "staged"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen does not render %q; the refusal must be readable, not just set", want)
		}
	}
	if !strings.Contains(view, "refused") {
		t.Error("the status area did not render the refusal sentence")
	}
}
