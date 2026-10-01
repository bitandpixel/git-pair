package tui_test

import (
	"context"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
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

// sessionFrom opens the screen with the integration branch named. `measure` is the base the lifecycle walk
// reads, which in production is the resolved one (`applyBases` rewrites `changeset.Changeset.Base` before any
// surface runs) rather than the value the file recorded.
func sessionFrom(t *testing.T, e *env, trunk changeset.DefaultBranchRef, measure string) *tui.Session {
	t.Helper()
	summary, err := lifecycle.SummarizeHEAD(context.Background(), e.repo, e.cs.Slug, measure)
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

func sessionWith(t *testing.T, e *env, trunk changeset.DefaultBranchRef) *tui.Session {
	t.Helper()
	return sessionFrom(t, e, trunk, e.cs.Base)
}

func TestSessionMeasuresAgainstTheFetchedTrunk(t *testing.T) {
	e := newEnv(t)
	trunk := fetchedTrunk(t, e)

	sess := sessionWith(t, e, trunk)
	if got := sess.Header().Base; got != "refs/remotes/origin/main" {
		t.Errorf("header base = %q, want the fetched copy of the integration branch", got)
	}
	if got := sess.Header().ParentLanded; got != "" {
		t.Errorf("header parent = %q, want nothing: this changeset never stacked on one, so the box has no parent to say anything about", got)
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

// stackedChild is a parent changeset on its own branch with a child built on top of it. The child's branch
// carries its parent's changeset directory as well as its own, which is why the child is named to the session
// rather than read off the checkout.
func stackedChild(t *testing.T) *env {
	t.Helper()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n\nfunc main() {}\n"))
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("alpha.go", "package main\n"))
	f.CreateBranch("beta")
	f.CommitChangeset("beta", "alpha")
	f.Commit("beta work", gittest.WithFile("beta.go", "package main\n"))
	f.SwitchTo("beta")

	e := &env{f: f, repo: &git.Repo{Dir: f.Dir()}}
	e.cs = changeset.Changeset{Slug: "beta", Branch: "beta", Dir: changeset.ActiveDirPath("beta"),
		Base: "alpha", ParentBranch: "alpha", BaseChangeset: "alpha", Exists: true}
	return e
}

// trunkMovesOn leaves this clone's trunk behind its own tip with work the child does not own, so a base that
// measured from the wrong place is visible in the file list rather than only in a string.
func trunkMovesOn(t *testing.T, e *env) {
	t.Helper()
	e.f.SwitchTo("main")
	e.f.Commit("main: work the child does not own", gittest.WithFile("other.go", "package main\n"))
	e.f.SwitchTo("beta")
}

// landedChild is the shape whose base row used to print forty characters nobody could read: the parent merged
// into trunk, the child left sitting where the landing left it, and trunk moved on afterwards.
func landedChild(t *testing.T) (*env, changeset.DefaultBranchRef) {
	t.Helper()
	e := stackedChild(t)
	e.f.SwitchTo("main")
	e.f.MustGit("merge", "--no-ff", "-m", "alpha: the landing", "alpha")
	e.f.SwitchTo("beta")
	trunkMovesOn(t, e)
	return e, changeset.DefaultBranchRef{Ref: "refs/heads/main", Source: changeset.DefaultBranchSoleCandidate}
}

// Rule 2 names the destination, because a commit id is not an explanation — and after the child rebases it
// is not even the parent's commit, it is trunk's newest one, which is usually somebody else's work. What the
// name stops carrying has to go somewhere else, so the session reports the parent's own id beside it.
func TestAChildWhoseParentLandedNamesTheDestinationAndTheParent(t *testing.T) {
	e, trunk := landedChild(t)

	sess := sessionWith(t, e, trunk)
	h := sess.Header()
	if h.Base != "refs/heads/main" {
		t.Errorf("header base = %q, want the destination: the box column cannot hold an object id, and the reader cannot read one", h.Base)
	}
	if h.SpanLabel != "main...current" {
		t.Errorf("span label = %q, want it drawn from the same name the base row prints", h.SpanLabel)
	}
	if h.ParentLanded != "alpha" {
		t.Errorf("header parent = %q, want the id of the changeset the destination now carries", h.ParentLanded)
	}
	// The label moved and the measurement did not: the child's run starts at the landing, so neither the
	// parent's landed work nor trunk's later work is the child's to review.
	paths := filePaths(sess.Files())
	for _, notWanted := range []string{"alpha.go", "other.go"} {
		if containsPath(paths, notWanted) {
			t.Errorf("the file list carries %q, which is not this child's work: %v", notWanted, paths)
		}
	}
	if !containsPath(paths, "beta.go") {
		t.Errorf("the file list lost the child's own work: %v", paths)
	}
}

// A parent's branch is not the landing. Deleting it — the step `status` prints once the work has landed —
// changes nothing about the answer, which is why the base row can name the destination and the line under it
// can still name the parent.
func TestAChildWhoseParentBranchWasDeletedStillNamesTheLanding(t *testing.T) {
	e, trunk := landedChild(t)
	e.f.SwitchTo("main")
	e.f.ForceDeleteBranch("alpha")
	e.f.SwitchTo("beta")

	sess := sessionFrom(t, e, trunk, trunk.Ref)
	if got := sess.Header().ParentLanded; got != "alpha" {
		t.Errorf("header parent = %q, want alpha: the branch is where the record used to live, and the landing is the fact", got)
	}
	if got := sess.Header().Base; got != "refs/heads/main" {
		t.Errorf("header base = %q, want the destination", got)
	}
}

// The other side of the same line: a parent still standing is a base that names it, and there is nothing
// landed to say.
func TestAChildOnAParentThatIsStillStandingSaysNothingAboutLanding(t *testing.T) {
	e := stackedChild(t)
	trunkMovesOn(t, e)
	trunk := changeset.DefaultBranchRef{Ref: "refs/heads/main", Source: changeset.DefaultBranchSoleCandidate}

	sess := sessionWith(t, e, trunk)
	if got := sess.Header().Base; got != "alpha" {
		t.Errorf("header base = %q, want the parent branch: its work has not reached the destination", got)
	}
	if got := sess.Header().ParentLanded; got != "" {
		t.Errorf("header parent = %q, want nothing to report", got)
	}
}
