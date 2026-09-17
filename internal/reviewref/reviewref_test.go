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
// contract) builds on the exact string.
func TestRefNaming(t *testing.T) {
	if got, want := reviewref.Archive("booking-transaction"), "refs/reviews/booking-transaction"; got != want {
		t.Errorf("Archive = %q, want %q", got, want)
	}
	if got, want := reviewref.Namespace("booking-transaction"), "refs/reviews/booking-transaction"; got != want {
		t.Errorf("Namespace = %q, want %q", got, want)
	}
}

func TestSlug(t *testing.T) {
	tests := []struct {
		ref        string
		wantSlug   string
		wantSlugOK bool
	}{
		{"refs/reviews/booking-transaction", "booking-transaction", true},
		// A leftover from the retired layout: three components, so it is not a changeset
		// ref. Nothing writes this shape any more, and reading one as a changeset named
		// `archive` would invent a changeset from an old run.
		{"refs/reviews/archive/booking-transaction/91bf204", "", false},
		{"refs/reviews/archive", "archive", true},
		{"refs/reviews/booking/nested", "", false},
		{"refs/heads/booking-transaction", "", false},
		{"refs/reviews/", "", false},
		{"refs/reviews", "", false},
	}
	for _, tc := range tests {
		slug, ok := reviewref.Slug(tc.ref)
		if slug != tc.wantSlug || ok != tc.wantSlugOK {
			t.Errorf("Slug(%q) = (%q, %v), want (%q, %v)", tc.ref, slug, ok, tc.wantSlug, tc.wantSlugOK)
		}
	}
}

// The resolution rule reads a stacked `base:` that names the parent's durable ref, so the
// parse has to be exact in both directions: a ref in the shape must yield the id, and a name
// that merely ends in `/archive` must not be mistaken for one.
func TestArchivedChangesetID(t *testing.T) {
	tests := []struct {
		ref  string
		want string
		ok   bool
	}{
		{"refs/git-pair/changesets/booking/archive", "booking", true},
		{"refs/git-pair/changesets/feat-JIRA-123_Foo/archive", "feat-JIRA-123_Foo", true},
		{"refs/git-pair/changesets/booking", "", false},
		{"refs/git-pair/changesets/booking/integration", "", false},
		{"refs/git-pair/changesets//archive", "", false},
		{"refs/git-pair/changesets/a/b/archive", "", false},
		{"refs/git-pair/changeset/booking/archive", "", false},
		{"refs/heads/feature/archive", "", false},
		{"refs/reviews/archive/booking/91bf204", "", false},
		{"booking", "", false},
	}
	for _, tc := range tests {
		got, ok := reviewref.ArchivedChangesetID(tc.ref)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ArchivedChangesetID(%q) = (%q, %v), want (%q, %v)", tc.ref, got, ok, tc.want, tc.ok)
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
	if ref != "refs/reviews/booking" {
		t.Errorf("ref = %q, want refs/reviews/booking", ref)
	}
	if got := f.RefSHA("refs/reviews/booking"); got != first {
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
	if err != nil && !strings.Contains(err.Error(), "refs/reviews/never-reviewed") {
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
	// A leftover ref from the retired layout, which git-pair no longer writes, must
	// not be read as a changeset.
	f.MustGit("update-ref", "refs/reviews/archive/stray/abc123", head)

	entries, err := reviewref.List(ctx, repo(f))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List = %+v, want the two changeset refs", entries)
	}
	// Sorted by ref name, which is the order `queue` prints in.
	byRef := map[string]reviewref.Entry{}
	for _, e := range entries {
		byRef[e.Ref] = e
	}
	for _, name := range []string{"refs/reviews/another", "refs/reviews/booking"} {
		got, ok := byRef[name]
		if !ok {
			t.Errorf("List is missing %s: %+v", name, entries)
			continue
		}
		if got.SHA != head {
			t.Errorf("%s = %+v, want it to point at %s", name, got, head)
		}
	}
	if got := byRef["refs/reviews/booking"]; got.Slug != "booking" {
		t.Errorf("booking entry = %+v, want Slug booking", got)
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
