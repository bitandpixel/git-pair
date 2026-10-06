package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/gittest"
)

// The nudge that an author never described the change only works if the scaffold is
// recognised. It is compared line by line against the template, and the template ends
// with a newline while the comparison trims the file: getting that wrong makes the
// warning unreachable, and an assertion-free warning is invisible to the suite until
// someone actually reads the output.
func TestChangeReadyWarnsWhenAboutIsStillTheScaffold(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	f.StageChangeset("booking", "main")
	f.WriteChangesetFile("booking", "ABOUT.md", changeset.AboutTemplate("booking"))
	f.MustGit("add", "changesets/booking")
	f.MustGit("commit", "-m", "add changeset scaffold")
	f.Commit("implement", gittest.WithFile("service.go", "package main\n"))

	res := runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")
	mustContain(t, res.stderr, "still has the empty", "ready should warn about an undescribed changeset")

	// Describing it removes the warning.
	f.WriteChangesetFile("booking", "ABOUT.md", "# booking\n\n## Summary\n\nNow described.\n")
	f.Commit("describe booking")
	res = runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")
	if strings.Contains(res.stderr, "still has the empty") {
		t.Errorf("ready warned about the scaffold after ABOUT.md was filled in:\n%s", res.stderr)
	}
}

// The scaffold is a repository's document, not the tool's: a team whose reviewers
// read something else writes `.git-pair/about-template.md` once and every later
// changeset starts from it. `{{slug}}` is where the changeset id goes.
func TestInitWritesTheRepositoryAboutTemplate(t *testing.T) {
	f := newRepo(t)
	f.Write(".git-pair/about-template.md", "# {{slug}}\n\n## Why\n\n## Risk\n\n- run {{slug}} end to end\n")
	f.CreateBranch("bookings")

	runIn(t, f.Dir(), "init").mustSucceed(t, "init")

	about := f.ChangesetFile("bookings", "ABOUT.md")
	want := "# bookings\n\n## Why\n\n## Risk\n\n- run bookings end to end\n"
	if about != want {
		t.Errorf("ABOUT.md =\n%s\nwant the repository template rendered for the id:\n%s", about, want)
	}

	// The template is the repository's input, so `init` leaves it out of the commit it
	// makes — which only ever carries the changeset directory.
	for _, path := range f.ChangedFiles("HEAD~1", "HEAD") {
		if strings.HasPrefix(path, ".git-pair/") {
			t.Errorf("init committed %s; the template is an input, not part of the changeset", path)
		}
	}
}

// The warning that an author never described the change has to follow the
// template, or a repository with its own scaffold loses the one nudge that an
// agent left ABOUT.md empty.
func TestChangeReadyWarnsAgainstTheRepositoryScaffold(t *testing.T) {
	f := newRepo(t)
	f.Write(".git-pair/about-template.md", "# {{slug}}\n\n## Why\n\n## Risk\n")
	f.Commit("adopt an about template")
	f.CreateBranch("booking")
	f.StageChangeset("booking", "main")
	f.WriteChangesetFile("booking", "ABOUT.md", changeset.AboutTemplate("booking"))
	f.Commit("add changeset scaffold")
	f.Commit("implement", gittest.WithFile("service.go", "package main\n"))

	res := runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")
	mustContain(t, res.stderr, "still has the empty",
		"ready should warn about a built-in scaffold even after the repository adopted its own template")

	// Filling in the repository's own sections is what clears it.
	f.WriteChangesetFile("booking", "ABOUT.md", "# booking\n\n## Why\n\nBecause it double-books.\n\n## Risk\n\nNone.\n")
	f.Commit("describe booking")
	res = runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")
	if strings.Contains(res.stderr, "still has the empty") {
		t.Errorf("ready warned after ABOUT.md was filled in:\n%s", res.stderr)
	}
}

// The other half: the file that matches the repository's own scaffold is the file
// nobody described, and the built-in headings are not the only shape that can be
// left empty.
func TestChangeReadyWarnsAgainstTheCustomScaffold(t *testing.T) {
	f := newRepo(t)
	f.Write(".git-pair/about-template.md", "# {{slug}}\n\n## Why\n\n## Risk\n")
	f.Commit("adopt an about template")
	f.CreateBranch("booking")
	f.StageChangeset("booking", "main")
	f.WriteChangesetFile("booking", "ABOUT.md", "# booking\n\n## Why\n\n## Risk\n")
	f.Commit("add changeset scaffold")
	f.Commit("implement", gittest.WithFile("service.go", "package main\n"))

	res := runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")
	mustContain(t, res.stderr, "still has the empty",
		"ready should recognise the repository's own scaffold")
}
