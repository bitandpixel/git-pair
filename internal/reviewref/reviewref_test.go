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

// PRD §13 names the two ref layouts verbatim, and other tooling (notifications,
// agents, the README contract) builds on the exact spelling.
func TestRefNaming(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{reviewref.Head("booking-transaction"), "refs/reviews/booking-transaction"},
		{reviewref.Archive("booking-transaction", "91bf204"), "refs/reviews/archive/booking-transaction/91bf204"},
		{reviewref.ArchivePattern("booking-transaction"), "refs/reviews/archive/booking-transaction/*"},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}

func TestIsArchiveAndSlug(t *testing.T) {
	tests := []struct {
		ref        string
		wantArch   bool
		wantSlug   string
		wantSlugOK bool
	}{
		{"refs/reviews/booking-transaction", false, "booking-transaction", true},
		{"refs/reviews/archive/booking-transaction/91bf204", true, "", false},
		{"refs/reviews/booking/nested", false, "", false},
		{"refs/heads/booking-transaction", false, "", false},
		{"refs/reviews/", false, "", false},
		{"refs/reviews", false, "", false},
	}
	for _, tc := range tests {
		if got := reviewref.IsArchive(tc.ref); got != tc.wantArch {
			t.Errorf("IsArchive(%q) = %v, want %v", tc.ref, got, tc.wantArch)
		}
		slug, ok := reviewref.Slug(tc.ref)
		if slug != tc.wantSlug || ok != tc.wantSlugOK {
			t.Errorf("Slug(%q) = (%q, %v), want (%q, %v)", tc.ref, slug, ok, tc.wantSlug, tc.wantSlugOK)
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

	// The movable ref follows the newest review.
	if _, err := reviewref.Update(ctx, repo(f), "booking", review); err != nil {
		t.Fatalf("second Update: %v", err)
	}
	if got, err := reviewref.Resolve(ctx, repo(f), "booking"); err != nil || got != review {
		t.Errorf("Resolve = (%s, %v), want the newest review %s", got, err, review)
	}

	_, err = reviewref.Resolve(ctx, repo(f), "never-reviewed")
	if !errors.Is(err, reviewref.ErrNoReviewRef) {
		t.Errorf("Resolve for an unreviewed changeset = %v, want ErrNoReviewRef", err)
	}
	if err != nil && !strings.Contains(err.Error(), "refs/reviews/never-reviewed") {
		t.Errorf("error = %v, want it to name the missing ref", err)
	}
}

// The archive ref is what makes a squash safe, so writing it twice must never move
// it (PRD §13: "the complete final unsquashed stack must remain reachable").
func TestArchiveCommitIsImmutable(t *testing.T) {
	f := gittest.New(t)
	first := f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	second := f.Commit("later", gittest.WithFile("a.txt", "b\n"))
	ctx := context.Background()

	ref, created, err := reviewref.ArchiveCommit(ctx, repo(f), "booking", first)
	if err != nil {
		t.Fatalf("ArchiveCommit: %v", err)
	}
	if !created {
		t.Error("created = false, want true for a new archive ref")
	}
	if ref != "refs/reviews/archive/booking/"+f.Short(first) {
		t.Errorf("ref = %q, want refs/reviews/archive/booking/%s", ref, f.Short(first))
	}
	if got := f.RefSHA(ref); got != first {
		t.Errorf("archive ref points at %s, want %s", got, first)
	}

	// Writing the same archive ref again is a no-op: the ref is written once and
	// never moved.
	ref2, created, err := reviewref.ArchiveCommit(ctx, repo(f), "booking", first)
	if err != nil {
		t.Fatalf("second ArchiveCommit: %v", err)
	}
	if created {
		t.Error("created = true, want false: an archive ref is written once")
	}
	if ref2 != ref {
		t.Errorf("second ArchiveCommit returned %q, want the original %q", ref2, ref)
	}
	if got := f.RefSHA(ref); got != first {
		t.Errorf("archive ref was moved to %s; it must stay at %s", got, first)
	}
	// A different commit is a different archival point, and it must not disturb the
	// first one.
	other, created, err := reviewref.ArchiveCommit(ctx, repo(f), "booking", second)
	if err != nil || !created {
		t.Fatalf("ArchiveCommit for a second commit = (%v, %v)", created, err)
	}
	if other == ref {
		t.Errorf("second archival reused the first ref %q", other)
	}
	if f.RefSHA(other) != second {
		t.Errorf("second archive ref points at %s, want %s", f.RefSHA(other), second)
	}
	if f.RefSHA(ref) != first {
		t.Errorf("the first archive ref moved to %s, want %s", f.RefSHA(ref), first)
	}
}

func TestListExcludesArchives(t *testing.T) {
	f := gittest.New(t)
	head := f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	ctx := context.Background()

	if _, err := reviewref.Update(ctx, repo(f), "booking", head); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, _, err := reviewref.ArchiveCommit(ctx, repo(f), "booking", head); err != nil {
		t.Fatalf("ArchiveCommit: %v", err)
	}
	// A ref git-pair would never create must not be mistaken for a changeset ref.
	f.MustGit("update-ref", "refs/reviews/archive/stray/abc123", head)

	entries, err := reviewref.List(ctx, repo(f))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List = %+v, want only the movable refs/reviews/booking entry", entries)
	}
	got := entries[0]
	if got.Ref != "refs/reviews/booking" || got.Slug != "booking" || got.SHA != head {
		t.Errorf("entry = %+v, want {refs/reviews/booking %s booking}", got, head)
	}
}

// The archival ref must keep the whole chain reachable on its own, which is the
// property `git pair change complete` relies on. The CLI test replays this through the
// product; this pins the ref primitive.
func TestArchiveRefKeepsChainReachableWithoutABranch(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")
	first := f.Head()
	f.CommitChangeset("booking", "main")
	ready := f.CommitReadyMarker("booking")
	review := f.CommitReviewMarker("booking", "approve", gittest.WithFile("notes.md", "ok\n"))
	ctx := context.Background()

	ref, _, err := reviewref.ArchiveCommit(ctx, repo(f), "booking", review)
	if err != nil {
		t.Fatalf("ArchiveCommit: %v", err)
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
