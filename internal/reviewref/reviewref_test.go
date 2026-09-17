package reviewref_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gitpair/internal/git"
	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
)

func repo(f *gittest.Fixture) *git.Repo { return &git.Repo{Dir: f.Dir()} }

// PRD §13 names the ref spelling, and other tooling (notifications, agents, the README
// contract) builds on the exact string. The id names the refs, never the branch, which is what
// keeps the work findable after the branch is gone.
func TestRefNaming(t *testing.T) {
	if got, want := reviewref.NamespaceRoot(), "refs/git-pair/changesets"; got != want {
		t.Errorf("NamespaceRoot = %q, want %q", got, want)
	}
	if got, want := reviewref.Namespace("booking-transaction"), "refs/git-pair/changesets/booking-transaction"; got != want {
		t.Errorf("Namespace = %q, want %q", got, want)
	}
	if got, want := reviewref.Archive("booking-transaction"), "refs/git-pair/changesets/booking-transaction/archive"; got != want {
		t.Errorf("Archive = %q, want %q", got, want)
	}
	// The archive is a child of the namespace rather than the namespace itself, and that is
	// not a stylistic choice: a ref cannot be a leaf and a namespace at once, so the moment a
	// second durable ref exists the leaf cannot be created at all.
	if !strings.HasPrefix(reviewref.Archive("booking-transaction"), reviewref.Namespace("booking-transaction")+"/") {
		t.Error("the archive ref must live under the changeset's namespace")
	}
}

// Reading the id out of a durable ref has to be exact in both directions: a ref in the shape
// yields the id, and a name that merely looks like one — a branch called `feature/archive`, a
// ref from the retired `refs/reviews` layout, the namespace itself — yields nothing. Mistaking
// one for a durable ref would attribute someone's work to a changeset they never named.
func TestChangesetID(t *testing.T) {
	tests := []struct {
		ref  string
		want string
		ok   bool
	}{
		{"refs/git-pair/changesets/booking/archive", "booking", true},
		{"refs/git-pair/changesets/feat-JIRA-123_Foo/archive", "feat-JIRA-123_Foo", true},
		// Any child names the changeset: an integration ref is a record about the same work.
		{"refs/git-pair/changesets/booking/integration", "booking", true},
		// The namespace itself is never a ref, so it names nothing.
		{"refs/git-pair/changesets/booking", "", false},
		{"refs/git-pair/changesets/booking/notes/deep", "", false},
		{"refs/git-pair/changesets//archive", "", false},
		{"refs/git-pair/changeset/booking/archive", "", false},
		{"refs/heads/feature/archive", "", false},
		// The retired layout: reading these would resurrect changesets that no longer exist.
		{"refs/reviews/booking", "", false},
		{"refs/reviews/archive/booking/91bf204", "", false},
		{"refs/git-pair/changesets", "", false},
		{"refs/git-pair/changesets/", "", false},
		{"booking", "", false},
	}
	for _, tc := range tests {
		got, ok := reviewref.ChangesetID(tc.ref)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ChangesetID(%q) = (%q, %v), want (%q, %v)", tc.ref, got, ok, tc.want, tc.ok)
		}
	}
}

// PRD §10.4: "Immediately after successful review submission, update the
// changeset's review archive ref to the resulting exact HEAD."
func TestUpdateAndResolve(t *testing.T) {
	f := gittest.New(t)
	first := f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	review := f.CommitReviewMarker("booking", "block")
	ctx := context.Background()

	ref, err := reviewref.Update(ctx, repo(f), "booking", first)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if ref != "refs/git-pair/changesets/booking/archive" {
		t.Errorf("ref = %q, want refs/git-pair/changesets/booking/archive", ref)
	}
	if got := f.RefSHA("refs/git-pair/changesets/booking/archive"); got != first {
		t.Errorf("ref points at %s, want %s", got, first)
	}
	if sha, err := reviewref.Resolve(ctx, repo(f), "booking"); err != nil || sha != first {
		t.Fatalf("Resolve = (%s, %v), want (%s, nil)", sha, err, first)
	}

	// The archive follows the newest review.
	if _, err := reviewref.Update(ctx, repo(f), "booking", review); err != nil {
		t.Fatalf("second Update: %v", err)
	}
	if got, err := reviewref.Resolve(ctx, repo(f), "booking"); err != nil || got != review {
		t.Errorf("Resolve = (%s, %v), want the newest review %s", got, err, review)
	}

	_, err = reviewref.Resolve(ctx, repo(f), "never-reviewed")
	if !errors.Is(err, reviewref.ErrNoArchiveRef) {
		t.Errorf("Resolve for an unarchived changeset = %v, want ErrNoArchiveRef", err)
	}
	if err != nil && !strings.Contains(err.Error(), "refs/git-pair/changesets/never-reviewed/archive") {
		t.Errorf("error = %v, want it to name the missing ref", err)
	}
}

func TestList(t *testing.T) {
	f := gittest.New(t)
	head := f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	ctx := context.Background()

	if _, err := reviewref.Update(ctx, repo(f), "booking", head); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := reviewref.Update(ctx, repo(f), "another", head); err != nil {
		t.Fatalf("Update: %v", err)
	}
	// Two shapes must not be read as archives: a ref from the retired `refs/reviews` layout,
	// which git-pair no longer writes, and a changeset whose only durable ref is an integration
	// record — a changeset with an integration record and no archive has no archived history,
	// and reporting it as archived would be wrong.
	f.MustGit("update-ref", "refs/reviews/stray", head)
	f.MustGit("update-ref", "refs/git-pair/changesets/recorded/integration", head)

	entries, err := reviewref.List(ctx, repo(f))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List = %+v, want the two archive refs", entries)
	}
	// Sorted by ref name, which is the order `queue` prints in.
	byRef := map[string]reviewref.Entry{}
	for _, e := range entries {
		byRef[e.Ref] = e
	}
	for _, name := range []string{"refs/git-pair/changesets/another/archive", "refs/git-pair/changesets/booking/archive"} {
		got, ok := byRef[name]
		if !ok {
			t.Errorf("List is missing %s: %+v", name, entries)
			continue
		}
		if got.SHA != head {
			t.Errorf("%s = %+v, want it to point at %s", name, got, head)
		}
	}
	if got := byRef["refs/git-pair/changesets/booking/archive"]; got.ID != "booking" {
		t.Errorf("booking entry = %+v, want ID booking", got)
	}
}

// `change init` refuses an id whose namespace already holds a ref, and it must refuse on the
// strength of any child: a changeset that has an integration record and no archive still has a
// history, and giving its name to a new changeset would attach that history to a stranger.
func TestTakenSeesAnyChildOfTheNamespace(t *testing.T) {
	f := gittest.New(t)
	head := f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	ctx := context.Background()

	f.MustGit("update-ref", "refs/git-pair/changesets/booking/integration", head)
	for _, tc := range []struct {
		id   string
		want bool
	}{
		{"booking", true},
		// Matching is by path component, so a neighbour is not a collision.
		{"booking-v2", false},
		{"book", false},
	} {
		got, err := reviewref.Taken(ctx, repo(f), tc.id)
		if err != nil {
			t.Fatalf("Taken(%s): %v", tc.id, err)
		}
		if got != tc.want {
			t.Errorf("Taken(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

// The archive ref must keep the whole chain reachable on its own, which is the property
// `git pair change archive` relies on. The CLI test replays this through the product; this
// pins the ref primitive.
func TestArchiveRefKeepsChainReachableWithoutABranch(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	first := f.Head()
	f.CommitChangeset("booking", "main")
	ready := f.CommitReadyMarker("booking")
	review := f.CommitReviewMarker("booking", "approve", gittest.WithFile("notes.md", "ok\n"))
	ctx := context.Background()

	ref, err := reviewref.Update(ctx, repo(f), "booking", review)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	f.SwitchTo("main")
	f.ForceDeleteBranch("booking")

	for _, want := range []string{first, ready, review} {
		if !f.ReachableFrom(want, ref) {
			t.Errorf("%s is not reachable from %s after the branch was deleted", want, ref)
		}
	}
	if got := f.RevListCount(ref); got != 4 {
		t.Errorf("archive ref reaches %d commits, want the full 4-commit chain", got)
	}
}
