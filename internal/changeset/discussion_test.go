package changeset_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/gittest"
)

// The discussion file is one document per changeset, written by both sides, with no
// schema: nothing here parses its headings, and nothing may start to. The rules under
// test are where its first text comes from — the repository's
// `.git-pair/discussion-template.md`, then the built-in title — and the guarantee that
// keeps it a place to write in rather than a thing the tool overwrites.

func TestDiscussionPathIsInsideTheChangesetDir(t *testing.T) {
	cs := changeset.Changeset{Slug: "booking-transaction", Dir: filepath.Join("changesets", "booking-transaction")}
	want := filepath.Join("changesets", "booking-transaction", changeset.DiscussionFile)
	if got := cs.DiscussionPath(); got != want {
		t.Errorf("DiscussionPath() = %q, want %q", got, want)
	}
}

// A team that wants prompts in the file says so in the repository rather than in a
// release of git-pair, the same way ABOUT.md's scaffold is overridden.
func TestResolveDiscussionTemplateUsesTheRepositoryFile(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.Write(changeset.DiscussionTemplateFile, "# Discussion on {{slug}}\n\n## open question\n\n## response\n")

	got, err := changeset.ResolveDiscussionTemplate(&git.Repo{Dir: f.Dir()}, "booking")
	if err != nil {
		t.Fatalf("ResolveDiscussionTemplate: %v", err)
	}
	want := "# Discussion on booking\n\n## open question\n\n## response\n"
	if got != want {
		t.Errorf("resolved scaffold = %q, want %q", got, want)
	}
}

// With no repository template the file opens with a title and nothing else. The shape
// of a conversation belongs to the two people having it, so the built-in scaffold
// makes no offer about headings — and it must keep the token so a repository scaffold
// built on it still round-trips.
func TestDiscussionTemplateIsATitle(t *testing.T) {
	got := changeset.DiscussionTemplate("booking")
	if got != "# Discussion\n" {
		t.Errorf("DiscussionTemplate = %q, want the built-in title alone", got)
	}
	if strings.Contains(got, changeset.AboutSlugToken) {
		t.Errorf("built-in scaffold still holds the token:\n%s", got)
	}
}

// A template file with nothing in it is the same decision as not having one. The
// alternative — an empty DISCUSSION.md — hands the reader a blank document with no
// clue that anyone's setting failed.
func TestResolveDiscussionTemplateTreatsBlankFileAsAbsent(t *testing.T) {
	for _, blank := range []string{"", "\n", "   \n\t\n"} {
		f := gittest.New(t)
		f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
		f.Write(changeset.DiscussionTemplateFile, blank)

		got, err := changeset.ResolveDiscussionTemplate(&git.Repo{Dir: f.Dir()}, "booking")
		if err != nil {
			t.Fatalf("ResolveDiscussionTemplate(%q): %v", blank, err)
		}
		if want := changeset.DiscussionTemplate("booking"); got != want {
			t.Errorf("blank template %q produced %q, want the built-in scaffold %q", blank, got, want)
		}
	}
}

// A file that is there and cannot be read is an error rather than a quiet return to the
// built-in: the team that wrote a template expects their template, and writing a
// different document than the repository names is the worse outcome.
func TestResolveDiscussionTemplateReportsAnUnreadableFile(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	// A directory where the file belongs: ReadFile answers EISDIR on every platform,
	// where a chmod 000 file would read fine for a test running as root.
	if err := os.MkdirAll(filepath.Join(f.Dir(), changeset.DiscussionTemplateFile), 0o755); err != nil {
		t.Fatalf("mkdir template path: %v", err)
	}

	_, err := changeset.ResolveDiscussionTemplate(&git.Repo{Dir: f.Dir()}, "booking")
	if err == nil {
		t.Fatal("ResolveDiscussionTemplate accepted a template it could not read")
	}
	if !strings.Contains(err.Error(), changeset.DiscussionTemplateFile) {
		t.Errorf("error %q does not name the file at fault", err)
	}
}

// `init` does not create the file — an empty scaffold in every changeset is bloat — so
// EnsureDiscussion is the only writer, and it has to be safe on a file somebody has
// already filled in.
func TestEnsureDiscussionWritesOnceAndNeverOverwrites(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	repo := &git.Repo{Dir: f.Dir()}
	cs := changeset.Changeset{Slug: "booking", Dir: filepath.Join("changesets", "booking")}

	created, err := cs.EnsureDiscussion(repo)
	if err != nil {
		t.Fatalf("EnsureDiscussion: %v", err)
	}
	if !created {
		t.Error("first EnsureDiscussion reported an existing file")
	}
	if got, want := f.Read(cs.DiscussionPath()), "# Discussion\n"; got != want {
		t.Errorf("DISCUSSION.md = %q, want the built-in scaffold %q", got, want)
	}

	reviewerText := "# Discussion\n\n## the overlap test\n\nI'm not convinced these overlap.\n"
	f.Write(cs.DiscussionPath(), reviewerText)

	created, err = cs.EnsureDiscussion(repo)
	if err != nil {
		t.Fatalf("second EnsureDiscussion: %v", err)
	}
	if created {
		t.Error("second EnsureDiscussion reported creating a file that was already there")
	}
	if got := f.Read(cs.DiscussionPath()); got != reviewerText {
		t.Errorf("second EnsureDiscussion overwrote the discussion:\n%s", got)
	}
}

// A repository scaffold reaches the file the reviewer is handed, and an unreadable one
// stops the write rather than leaving a half state.
func TestEnsureDiscussionUsesTheRepositoryTemplate(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.Write(changeset.DiscussionTemplateFile, "# Discussion on {{slug}}\n\n## open question\n")
	repo := &git.Repo{Dir: f.Dir()}

	cs := changeset.Changeset{Slug: "booking", Dir: filepath.Join("changesets", "booking")}
	if _, err := cs.EnsureDiscussion(repo); err != nil {
		t.Fatalf("EnsureDiscussion: %v", err)
	}
	if got, want := f.Read(cs.DiscussionPath()), "# Discussion on booking\n\n## open question\n"; got != want {
		t.Errorf("DISCUSSION.md = %q, want the repository scaffold %q", got, want)
	}
}

func TestEnsureDiscussionAnUnreadableTemplateCreatesNothing(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	if err := os.MkdirAll(filepath.Join(f.Dir(), changeset.DiscussionTemplateFile), 0o755); err != nil {
		t.Fatalf("mkdir template path: %v", err)
	}

	cs := changeset.Changeset{Slug: "booking", Dir: filepath.Join("changesets", "booking")}
	if _, err := cs.EnsureDiscussion(&git.Repo{Dir: f.Dir()}); err == nil {
		t.Fatal("EnsureDiscussion accepted a template it could not read")
	}
	if _, err := os.Stat(filepath.Join(f.Dir(), cs.DiscussionPath())); !os.IsNotExist(err) {
		t.Errorf("EnsureDiscussion created the file despite the unreadable template (stat err = %v)", err)
	}
}

// The file arrives on demand rather than at `init`, so a changeset written by
// `changeset.Write` has an ABOUT.md and no discussion file: the first person to want
// it creates it.
func TestWriteDoesNotCreateTheDiscussionFile(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))

	cs := changeset.Changeset{Slug: "booking", Branch: "booking", Dir: filepath.Join("changesets", "booking")}
	if _, err := changeset.Write(&git.Repo{Dir: f.Dir()}, cs, changeset.WriteOptions{Base: "main"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.Dir(), cs.DiscussionPath())); !os.IsNotExist(err) {
		t.Errorf("init created %s (stat err = %v); the file is written on first use", cs.DiscussionPath(), err)
	}
	if _, err := os.Stat(filepath.Join(f.Dir(), cs.AboutPath())); err != nil {
		t.Errorf("init did not create ABOUT.md: %v", err)
	}
}
