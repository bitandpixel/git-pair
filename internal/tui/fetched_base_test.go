package tui_test

import (
	"context"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/gittest"
	"gitpair/internal/lifecycle"
	"gitpair/internal/span"
	"gitpair/internal/tui"
)

// The changeset box prints a base and a span beside it. Both have to name the same thing, and for a
// changeset measured against the integration branch in a clone that has fetched it, the thing they name is
// not the ref `base:` recorded: the local branch is where trunk stood the day the branch was cut, and the
// fetched copy is what the diff is measured from (`changeset.BaseFor`).

// fetchedTrunk moves the remote's trunk past this clone's, rebases the changeset onto it, and hands back
// the ref the session should be told about.
func fetchedTrunk(t *testing.T, e *env) changeset.DefaultBranchRef {
	t.Helper()
	e.f.SwitchTo("main")
	e.f.CreateBranch("fetched")
	there := e.f.Commit("main: work the changeset does not own", gittest.WithFile("other.go", "package main\n"))
	e.f.MustGit("update-ref", "refs/remotes/origin/main", there)
	e.f.SwitchTo("main")
	e.f.ForceDeleteBranch("fetched")
	e.f.SwitchTo(slug)
	e.f.MustGit("rebase", "refs/remotes/origin/main")
	return changeset.DefaultBranchRef{Ref: "refs/remotes/origin/main", Source: changeset.DefaultBranchRemoteHead}
}

func sessionWith(t *testing.T, e *env, trunk changeset.DefaultBranchRef) *tui.Session {
	t.Helper()
	summary, err := lifecycle.SummarizeHEAD(context.Background(), e.repo, e.cs.Slug, e.cs.Base)
	if err != nil {
		t.Fatalf("SummarizeHEAD: %v", err)
	}
	sess, err := tui.NewSession(context.Background(), tui.Options{
		Repo: e.repo, Changeset: e.cs, Summary: summary, Span: span.Full(), Trunk: trunk,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return sess
}

func TestSessionMeasuresAgainstTheFetchedTrunk(t *testing.T) {
	e := newEnv(t)
	trunk := fetchedTrunk(t, e)

	sess := sessionWith(t, e, trunk)
	if got := sess.Header().Base; got != "refs/remotes/origin/main" {
		t.Errorf("header base = %q, want the fetched copy of the integration branch", got)
	}
	if got := sess.Span().Label; got != "origin/main...current" {
		t.Errorf("span label = %q, want it named by the base it measured against", got)
	}
	for _, path := range filePaths(sess.Files()) {
		if path == "other.go" {
			t.Error("the file list carries work the destination already has")
		}
	}
	if !containsPath(filePaths(sess.Files()), "service.go") {
		t.Error("the file list lost the changeset's own work")
	}
}

// The same repository with nobody telling the session what the integration branch is. This is the shape the
// screen had before: a base that resolves to the stale local branch, and a file list widened by whatever the
// destination gained in between.
func TestSessionWithoutATrunkKeepsTheRecordedBase(t *testing.T) {
	e := newEnv(t)
	fetchedTrunk(t, e)

	sess := sessionWith(t, e, changeset.DefaultBranchRef{})
	if got := sess.Header().Base; got != "main" {
		t.Errorf("header base = %q, want the recorded base", got)
	}
	if got := sess.Span().Label; got != "main...current" {
		t.Errorf("span label = %q, want the recorded base's own span", got)
	}
	if !containsPath(filePaths(sess.Files()), "other.go") {
		t.Error("want the unmeasured span to still carry the destination's file: this is the shape the fix answers")
	}
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}
