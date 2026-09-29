package cli_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// gitIn runs git in one directory. The CI tests need a second and third repository — a scratch
// remote and the clones a pipeline makes from it — and gittest's helpers belong to a single fixture.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// ciClone pushes the fixture's branches to a scratch bare remote and clones it the way a CI job does.
//
// The clone is the whole point: a `git clone` maps `refs/heads/*` into `refs/remotes/*` and nothing
// else, so it holds no `refs/git-pair/` ref of any kind. Nothing in git-pair writes one any more, so
// that absence is the ordinary shape of a pipeline checkout and no command may read it as a problem.
func ciClone(t *testing.T, f *gittest.Fixture) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	f.MustGit("init", "--bare", "-b", "main", remote)
	f.MustGit("push", "--quiet", remote, "--all")
	clone := filepath.Join(t.TempDir(), "ci")
	f.MustGit("clone", "--quiet", remote, clone)
	if got := gitIn(t, clone, "for-each-ref", "--format=%(refname)", "refs/git-pair"); got != "" {
		t.Fatalf("the clone holds git-pair refs, which nothing should have written: %s", got)
	}
	return clone
}

// The other CI mistake, and the one that looks like a repository fault: a job that checked out one
// branch has no integration branch to compare against, so resolution refuses — correctly. The refusal
// has to name the fetch, because a message that only offers `--default-branch` leaves the reader to
// work out that their checkout is the thing at fault, and a message mentioning the changeset would
// send them to fix a changeset that is fine.
func TestOneBranchCheckoutNamesTheFetchItIsMissing(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	remote := filepath.Join(t.TempDir(), "remote.git")
	f.MustGit("init", "--bare", "-b", "main", remote)
	f.MustGit("push", "--quiet", remote, "--all")

	ci := t.TempDir()
	gitIn(t, ci, "init", "--quiet", ".")
	gitIn(t, ci, "remote", "add", "origin", remote)
	gitIn(t, ci, "fetch", "--quiet", "origin", "booking")
	gitIn(t, ci, "checkout", "--quiet", "-b", "booking", "FETCH_HEAD")

	res := runIn(t, ci, "status")
	if res.code != 2 {
		t.Fatalf("status in a one-branch checkout exited %d, want 2\n%s%s", res.code, res.stdout, res.stderr)
	}
	mustContain(t, res.stderr, "cannot tell which branch is the integration branch", "it refuses, as it must")
	mustContain(t, res.stderr, "fetch the default branch", "and names the fetch that fixes it")
	mustContain(t, res.stderr, "--default-branch", "alongside the flag that overrides it")
	mustNotContain(t, res.stderr, "changeset", "without blaming the changeset for a checkout problem")
}

// The three trunk fields exist because a run reporting "nothing has landed" is either working from a
// stale fetch or from the wrong trunk, and neither is visible in a sentence about the changeset. So
// the output names the branch, the commit it pointed at, and how the run learned it — reported the
// same way whether the branch was named by flag or found in the checkout, since the two halves of
// the comparison both have to be in the log for the log to explain itself.
func TestStatusExplainsWhatLandedWasMeasuredAgainst(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")

	out := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)
	if out["default_branch"] != "main" {
		t.Errorf("default_branch = %v, want main: the fixture has no remote, so a local main is the only candidate",
			out["default_branch"])
	}
	if out["default_branch_source"] != "sole-candidate" {
		t.Errorf("default_branch_source = %v, want sole-candidate", out["default_branch_source"])
	}
	if want := shortOf(f.RevParse("main")); out["default_branch_commit"] != want {
		t.Errorf("default_branch_commit = %v, want %s", out["default_branch_commit"], want)
	}

	// The same branch named by hand is still the same branch, with a different provenance: the field
	// answers how the run knew, not merely what it settled on.
	out = runIn(t, f.Dir(), "status", "--json", "--default-branch", "main").mustSucceed(t, "status").json(t)
	if out["default_branch"] != "main" || out["default_branch_source"] != "flag" {
		t.Errorf("with the flag: %v from %v, want main from flag", out["default_branch"], out["default_branch_source"])
	}
	if want := shortOf(f.RevParse("main")); out["default_branch_commit"] != want {
		t.Errorf("with the flag: default_branch_commit = %v, want %s", out["default_branch_commit"], want)
	}
}
