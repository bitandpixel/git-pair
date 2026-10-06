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

// The scaffold is the first thing a reviewer reads and the only thing an agent is
// guaranteed to be handed, so a repository that wants a different shape has to be
// able to say so in a file rather than in a release of git-pair. The rule under
// test is the resolution order: the repository's `.git-pair/about-template.md`,
// then the built-in headings of PRD §6.

func TestResolveAboutTemplateUsesTheRepositoryFile(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.Write(changeset.AboutTemplateFile, "# {{slug}}\n\n## Why\n\n## Verification\n")

	got, err := changeset.ResolveAboutTemplate(&git.Repo{Dir: f.Dir()}, "booking")
	if err != nil {
		t.Fatalf("ResolveAboutTemplate: %v", err)
	}
	want := "# booking\n\n## Why\n\n## Verification\n"
	if got != want {
		t.Errorf("resolved scaffold = %q, want %q", got, want)
	}
}

// The token is replaced everywhere, not only in the title: a team that names the
// changeset inside a checklist line means that line.
func TestResolveAboutTemplateReplacesEveryToken(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.Write(changeset.AboutTemplateFile, "# {{slug}}\n\n- [ ] run {{slug}} end to end\n\nSee {{slug}}.\n")

	got, err := changeset.ResolveAboutTemplate(&git.Repo{Dir: f.Dir()}, "booking")
	if err != nil {
		t.Fatalf("ResolveAboutTemplate: %v", err)
	}
	if strings.Contains(got, changeset.AboutSlugToken) {
		t.Errorf("scaffold still holds the token:\n%s", got)
	}
	for _, want := range []string{"# booking", "run booking end to end", "See booking."} {
		if !strings.Contains(got, want) {
			t.Errorf("scaffold is missing %q, got:\n%s", want, got)
		}
	}
}

// A template file with nothing in it is the same decision as not having one. The
// alternative — an empty ABOUT.md — hands the reviewer a blank document with no
// clue that anyone's setting failed.
func TestResolveAboutTemplateTreatsBlankFileAsAbsent(t *testing.T) {
	for _, blank := range []string{"", "\n", "   \n\t\n"} {
		f := gittest.New(t)
		f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
		f.Write(changeset.AboutTemplateFile, blank)

		got, err := changeset.ResolveAboutTemplate(&git.Repo{Dir: f.Dir()}, "booking")
		if err != nil {
			t.Fatalf("ResolveAboutTemplate(%q): %v", blank, err)
		}
		if want := changeset.AboutTemplate("booking"); got != want {
			t.Errorf("blank template %q produced %q, want the built-in scaffold %q", blank, got, want)
		}
	}
}

// A file that is there and cannot be read is an error rather than a quiet return
// to the built-in: the author who wrote a template expects their template, and
// writing a different document than the repository names is the worse outcome.
func TestResolveAboutTemplateReportsAnUnreadableFile(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	// A directory where the file belongs: ReadFile answers EISDIR on every platform,
	// where a chmod 000 file would read fine for a test running as root.
	if err := os.MkdirAll(filepath.Join(f.Dir(), changeset.AboutTemplateFile), 0o755); err != nil {
		t.Fatalf("mkdir template path: %v", err)
	}

	_, err := changeset.ResolveAboutTemplate(&git.Repo{Dir: f.Dir()}, "booking")
	if err == nil {
		t.Fatal("ResolveAboutTemplate accepted a template it could not read")
	}
	if !strings.Contains(err.Error(), changeset.AboutTemplateFile) {
		t.Errorf("error %q does not name the file at fault", err)
	}
}

// Write is the `init` path, and the file it writes is what the agent fills in.
func TestWriteUsesTheRepositoryTemplate(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.Write(changeset.AboutTemplateFile, "# {{slug}}\n\n## Risk\n")

	cs := changeset.Changeset{Slug: "booking", Branch: "booking", Dir: filepath.Join("changesets", "booking")}
	if _, err := changeset.Write(&git.Repo{Dir: f.Dir()}, cs, changeset.WriteOptions{Base: "main"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, want := f.Read(cs.AboutPath()), "# booking\n\n## Risk\n"; got != want {
		t.Errorf("ABOUT.md = %q, want the repository scaffold %q", got, want)
	}
}

// A template the repository cannot read stops `init` before it starts, rather than
// half-creating a changeset: a directory with a CHANGESET.yaml and no ABOUT.md is
// state every later command then has an opinion about.
func TestWriteAnUnreadableTemplateCreatesNothing(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	if err := os.MkdirAll(filepath.Join(f.Dir(), changeset.AboutTemplateFile), 0o755); err != nil {
		t.Fatalf("mkdir template path: %v", err)
	}

	cs := changeset.Changeset{Slug: "booking", Branch: "booking", Dir: filepath.Join("changesets", "booking")}
	if _, err := changeset.Write(&git.Repo{Dir: f.Dir()}, cs, changeset.WriteOptions{Base: "main"}); err == nil {
		t.Fatal("Write accepted a template it could not read")
	}
	if _, err := os.Stat(filepath.Join(f.Dir(), cs.Dir)); !os.IsNotExist(err) {
		t.Errorf("Write created %s despite the unreadable template (stat err = %v)", cs.Dir, err)
	}
}

func TestEnsureAboutUsesTheRepositoryTemplate(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.Write(changeset.AboutTemplateFile, "# {{slug}}\n\n## Risk\n")
	repo := &git.Repo{Dir: f.Dir()}

	cs := changeset.Changeset{Slug: "booking", Dir: filepath.Join("changesets", "booking")}
	created, err := cs.EnsureAbout(repo)
	if err != nil {
		t.Fatalf("EnsureAbout: %v", err)
	}
	if !created {
		t.Fatal("EnsureAbout reported nothing created for a missing ABOUT.md")
	}
	if got, want := f.Read(cs.AboutPath()), "# booking\n\n## Risk\n"; got != want {
		t.Errorf("ABOUT.md = %q, want the repository scaffold %q", got, want)
	}
	// Idempotent, because `review about` runs it on a file the reviewer may already
	// have filled in.
	created, err = cs.EnsureAbout(repo)
	if err != nil {
		t.Fatalf("second EnsureAbout: %v", err)
	}
	if created {
		t.Error("second EnsureAbout reported it created a file that was already there")
	}
	if f.Read(cs.AboutPath()) != "# booking\n\n## Risk\n" {
		t.Error("second EnsureAbout rewrote ABOUT.md")
	}
}

func TestAboutIsUntouched(t *testing.T) {
	scaffold := changeset.AboutTemplate("booking")
	tests := []struct {
		name     string
		about    string
		template string
		want     bool
	}{
		{"as written", scaffold, scaffold, true},
		{"title rewritten, sections empty", "# Booking transaction locking\n" + strings.TrimPrefix(scaffold, "# booking\n"), scaffold, true},
		{"one section filled in", strings.Replace(scaffold, "## Validation\n", "## Validation\n\n`go test ./...`\n", 1), scaffold, false},
		{"trailing whitespace drift", strings.Replace(scaffold, "## Summary\n", "## Summary   \n", 1), scaffold, true},
		{"title only", "# booking\n", scaffold, false},
		{"empty", "", scaffold, false},
		{"unrelated document", "# booking\n\n## Summary\n\nIt locks.\n", scaffold, false},
		// A repository with its own template is measured against that template, and
		// the built-in scaffold is a different document.
		{"custom scaffold against custom template", "# booking\n\n## Risk\n", "# {{slug}}\n\n## Risk\n", true},
		{"custom scaffold against built-in template", "# booking\n\n## Risk\n", scaffold, false},
		// A template with nothing below the title describes the document by itself, so
		// there is no untouched body to recognise.
		{"title-only template", "# booking\n\nwhatever\n", "# {{slug}}\n", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := changeset.AboutIsUntouched(tc.about, tc.template); got != tc.want {
				t.Errorf("AboutIsUntouched(%q, %q) = %v, want %v", tc.about, tc.template, got, tc.want)
			}
		})
	}
}

// The scaffold is what every later check recognises, so the built-in text has to
// keep the token rather than lose it: a scaffold that no longer round-trips is a
// scaffold nobody can detect.
func TestAboutTemplateRendersTheBuiltInScaffold(t *testing.T) {
	got := changeset.AboutTemplate("booking")
	if strings.Contains(got, changeset.AboutSlugToken) {
		t.Errorf("built-in scaffold still holds the token:\n%s", got)
	}
	if !strings.HasPrefix(got, "# booking\n") {
		t.Errorf("built-in scaffold does not open with the changeset id:\n%s", got)
	}
	for _, heading := range []string{"## Summary", "## What changed", "## Design decisions", "## Validation", "## Known limitations", "## Open questions"} {
		if !strings.Contains(got, heading) {
			t.Errorf("built-in scaffold is missing %q (PRD §6):\n%s", heading, got)
		}
	}
	if !changeset.AboutIsUntouched(got, got) {
		t.Error("the built-in scaffold is not recognised as untouched, so the ready warning can never fire")
	}
}
