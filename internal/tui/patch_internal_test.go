package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/gittest"
	"gitpair/internal/lifecycle"
	"gitpair/internal/span"
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

// The author's change and the reviewer's typing are two different diffs, and the pane shows both at
// once, so which revisions each one spans is not a detail: measured from the span's start, the
// reviewer's edits would carry the author's work and be indistinguishable from it.
func TestWorkingPatchIsTheReviewersOwnEdits(t *testing.T) {
	ctx := context.Background()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFiles(map[string]string{
		"service.go": "package main\n\nfunc Lock() {}\n",
		"main.go":    "package main\n\nfunc main() {}\n",
	}))
	const slug = "booking"
	f.CreateBranch(slug)
	f.CommitChangeset(slug, "main")
	f.Commit("author reworks the lock", gittest.WithFiles(map[string]string{
		"service.go": "package main\n\nfunc lock() { panic(\"no\") }\n",
	}))

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

	// The reviewer disagrees with the panic and edits the file, without committing.
	path := filepath.Join(f.Dir(), "service.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc lock() { return }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	spanPatch := ansi.Strip(strings.Join(sess.Patch(ctx, "service.go").Lines, "\n"))
	workPatch := ansi.Strip(strings.Join(sess.WorkingPatch(ctx, "service.go").Lines, "\n"))

	if !strings.Contains(spanPatch, "+func lock() { panic(\"no\") }") {
		t.Errorf("the span no longer carries the author's change:\n%s", spanPatch)
	}
	if strings.Contains(spanPatch, "return") {
		t.Errorf("the span picked up the reviewer's uncommitted edit:\n%s", spanPatch)
	}
	if !strings.Contains(workPatch, "+func lock() { return }") {
		t.Errorf("WorkingPatch is missing the reviewer's edit:\n%s", workPatch)
	}
	if strings.Contains(workPatch, "+func lock() { panic") {
		t.Errorf("WorkingPatch repeats the author's change, so the two sections cannot be told apart:\n%s", workPatch)
	}

	// Its counts are the reviewer's too -- the pane prints them beside the caption for that reason.
	work := sess.WorkingPatch(ctx, "service.go")
	if work.Added != 1 || work.Deleted != 1 {
		t.Errorf("WorkingPatch reported +%d \u2212%d, want +1 \u22121", work.Added, work.Deleted)
	}

	// A file nobody has touched since it was committed has no reviewer section at all.
	if empty := sess.WorkingPatch(ctx, "main.go"); len(empty.Lines) != 0 || empty.Err != "" {
		t.Errorf("an untouched file produced %d lines and err %q", len(empty.Lines), empty.Err)
	}
}
