package tui

import (
	"context"
	"strings"
	"testing"

	"gitpr/internal/changeset"
	"gitpr/internal/git"
	"gitpr/internal/gittest"
	"gitpr/internal/lifecycle"
	"gitpr/internal/span"
)

// The preview is only a preview because git does the diffing. This is the one test that runs
// the real thing, so the flags — colour on, external diff drivers off — are checked against
// git rather than against a fake.
func TestPatchAsksGitForTheSpan(t *testing.T) {
	ctx := context.Background()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFiles(map[string]string{
		"service.go": "package main\n\nfunc Lock() {}\nfunc Serve() {}\n",
		"README.md":  "# demo\n",
	}))
	const slug = "booking"
	f.CreateBranch(slug)
	f.CommitChangeset(slug, "main")
	f.Commit("rework the lock", gittest.WithFiles(map[string]string{
		"service.go": "package main\n\nfunc lock() {\n\tpanic(\"no\")\n}\nfunc Serve() {}\n",
		"README.md":  "# demo\n",
	}))

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

	patch := sess.Patch(ctx, "service.go")
	if patch.Err != "" {
		t.Fatalf("Patch reported %q", patch.Err)
	}
	// git counts the three new lines of the function body against the one line it replaced.
	if patch.Added != 3 || patch.Deleted != 1 {
		t.Errorf("git reported +%d −%d, want +3 −1", patch.Added, patch.Deleted)
	}
	joined := strings.Join(patch.Lines, "\n")
	if !strings.Contains(joined, "+++ b/service.go") {
		t.Errorf("the patch is not a git patch:\n%s", joined)
	}
	if !strings.Contains(joined, "\x1b[") {
		t.Error("the patch carries no colour, so git was asked without --color")
	}

	// A file the span did not touch is not an error; it is an empty preview.
	untouched := sess.Patch(ctx, "README.md")
	if untouched.Err != "" || len(untouched.Lines) != 0 {
		t.Errorf("an untouched file produced err=%q and %d lines, want neither", untouched.Err, len(untouched.Lines))
	}

	// A path outside the repository is reported in the patch, not as a panic or a silent blank.
	missing := sess.Patch(ctx, "does/not/exist.go")
	if missing.Err == "" && len(missing.Lines) != 0 {
		t.Error("a path that cannot be diffed was reported as an empty preview")
	}
}
