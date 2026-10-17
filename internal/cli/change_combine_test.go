package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The second exit: two directories that are one piece of work. The disappearing changeset moves whole into the
// survivor's archive, so the fold is visible and reversible, and the survivor's comparison does not move.

// twoChangesetsOnOneWorkBranch is the shape `change combine` answers: two unlanded directories, neither recorded
// below the other, both base: main, so neither is a stack and neither is landed work.
func twoChangesetsOnOneWorkBranch(t *testing.T) *gittest.Fixture {
	t.Helper()
	f := newRepo(t)
	f.CreateBranch("one-work")
	f.CommitChangeset("parser", "main")
	f.Commit("parser work", gittest.WithFile("parser.go", "package main\n"))
	f.Commit("second changeset added by the same branch", gittest.WithFiles(map[string]string{
		"changesets/lexer/CHANGESET.yaml": "id: lexer\nbase: main\n",
		"changesets/lexer/ABOUT.md":       "# lexer\n",
		"lexer.go":                        "package main\n",
	}))
	return f
}

func TestChangeCombineArchivesTheDisappearedChangeset(t *testing.T) {
	f := twoChangesetsOnOneWorkBranch(t)

	r := runIn(t, f.Dir(), "change", "combine", "--into", "parser").mustSucceed(t, "change combine")
	out := r.stdout
	for _, want := range []string{"lexer archived at changesets/parser/.combined/lexer",
		"parser keeps base: main", "dropped lexer's base: main"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say %q:\n%s", want, out)
		}
	}

	if f.HasWorktreeFile("changesets/lexer/CHANGESET.yaml") {
		t.Error("the disappeared directory is still live at changesets/lexer/")
	}
	for _, archived := range []string{"changesets/parser/.combined/lexer/CHANGESET.yaml",
		"changesets/parser/.combined/lexer/ABOUT.md"} {
		if !f.HasWorktreeFile(archived) {
			t.Errorf("%s is missing: the fold moves the directory whole and deletes nothing", archived)
		}
	}
	if about := f.Read("changesets/parser/ABOUT.md"); !strings.Contains(about, ".combined/lexer/") {
		t.Errorf("the survivor's ABOUT.md does not point at the archive:\n%s", about)
	}
	// The survivor's own comparison is untouched: folding something in is not a licence to move the base.
	if md := f.Read("changesets/parser/CHANGESET.yaml"); !strings.Contains(md, "base: main\n") {
		t.Errorf("the survivor lost its own base:\n%s", md)
	}

	// The point of the exit: the branch is now offerable.
	ready(t, f).mustSucceed(t, "change ready")
}

// `--discussion` appends rather than overwrites: the survivor keeps its own conversation and
// gains the disappeared changeset's under one line naming the archive, while the archived
// original stays where the archive put it. Discussion text is not an implementation change, so
// the append cannot move what a reviewer is comparing.
func TestChangeCombineAppendsTheDisappearedDiscussion(t *testing.T) {
	f := twoChangesetsOnOneWorkBranch(t)
	f.Commit("discussion files", gittest.WithFiles(map[string]string{
		"changesets/lexer/DISCUSSION.md":  "# Discussion\n\n## scope\n\nwhat the lexer covers\n",
		"changesets/parser/DISCUSSION.md": "# Discussion\n\n## grammar\n\nwhat the parser covers\n",
	}))

	runIn(t, f.Dir(), "change", "combine", "--into", "parser", "--discussion").mustSucceed(t, "change combine")

	got := f.Read("changesets/parser/DISCUSSION.md")
	for _, want := range []string{"what the parser covers", "what the lexer covers", ".combined/lexer/DISCUSSION.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("the survivor's DISCUSSION.md omits %q:\n%s", want, got)
		}
	}
	if !f.HasWorktreeFile("changesets/parser/.combined/lexer/DISCUSSION.md") {
		t.Error("--discussion appends: the archived file has to stay where the archive holds it")
	}
	// Order is the point of appending rather than merging: the survivor's own entry came first.
	if strings.Index(got, "what the parser covers") > strings.Index(got, "what the lexer covers") {
		t.Errorf("the brought-across text was placed before the survivor's own:\n%s", got)
	}
}

// A changeset that never had the file brings nothing across, so the survivor's own discussion
// is left exactly as it was: the flag has to be able to say nothing.
func TestChangeCombineWithNothingToBringAcross(t *testing.T) {
	f := twoChangesetsOnOneWorkBranch(t)
	f.Commit("only the survivor has a discussion", gittest.WithFiles(map[string]string{
		"changesets/parser/DISCUSSION.md": "# Discussion\n\n## grammar\n\nwhat the parser covers\n",
	}))
	before := f.Read("changesets/parser/DISCUSSION.md")

	runIn(t, f.Dir(), "change", "combine", "--into", "parser", "--discussion").mustSucceed(t, "change combine")

	if got := f.Read("changesets/parser/DISCUSSION.md"); got != before {
		t.Errorf("the survivor's DISCUSSION.md changed with nothing to bring across:\n%s", got)
	}
}

// Neither exit moves a diff out from under a reviewer, so a ready marker on either changeset stops the command. The
// marker here is real rather than asserted: the branch was offered while it carried one changeset, and the second
// directory arrived afterwards, which is also how the shape is acquired in practice.
func TestChangeCombineRefusesWhileEitherChangesetIsOffered(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("one-work")
	f.CommitChangeset("parser", "main")
	f.Commit("parser work", gittest.WithFile("parser.go", "package main\n"))
	ready(t, f).mustSucceed(t, "change ready")

	f.Commit("a second changeset added after the offer", gittest.WithFiles(map[string]string{
		"changesets/lexer/CHANGESET.yaml": "id: lexer\nbase: main\n",
		"changesets/lexer/ABOUT.md":       "# lexer\n",
		"lexer.go":                        "package main\n",
	}))

	r := runIn(t, f.Dir(), "change", "combine", "--into", "parser")
	if r.code != exitUsage {
		t.Fatalf("combining an offered changeset exited %d, want %d\nstderr: %s", r.code, exitUsage, r.stderr)
	}
	out := r.stdout + r.stderr
	for _, want := range []string{"parser", "change unready"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, out)
		}
	}
	if !f.HasWorktreeFile("changesets/lexer/CHANGESET.yaml") {
		t.Error("the refused command moved the directory anyway")
	}
}
