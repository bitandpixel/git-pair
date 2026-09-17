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
