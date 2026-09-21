package cli_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
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

// publishAndClone pushes every ref the fixture has — branches and the git-pair namespace — to a
// scratch bare remote, then clones it the way a CI job does.
//
// The remote having the refs is the point. A clone maps `refs/heads/*` into `refs/remotes/*` and
// nothing else, so the durable refs are absent from the clone even though they were published
// perfectly: this is the case where the fix is one line in the checkout, and the only way to prove
// the message sends the reader there is to build the checkout that needs it.
func publishAndClone(t *testing.T, f *gittest.Fixture) string {
	t.Helper()
	// The remote has to hold *something* in the namespace for the clone's silence to mean "the
	// refspec was missing" rather than "nobody ever wrote a ref". An integration record for another
	// changeset does that without touching the one under test.
	if len(f.RefNames(reviewref.NamespaceRoot)) == 0 {
		f.MustGit("update-ref", reviewref.Integration("already-landed"), f.RevParse("main"))
	}
	remote := filepath.Join(t.TempDir(), "remote.git")
	f.MustGit("init", "--bare", "-b", "main", remote)
	f.MustGit("push", "--quiet", remote, "--all")
	f.MustGit("push", "--quiet", remote, reviewref.FetchRefspec)
	clone := filepath.Join(t.TempDir(), "ci")
	f.MustGit("clone", "--quiet", remote, clone)
	if got := gitIn(t, clone, "for-each-ref", "--format=%(refname)", reviewref.NamespaceRoot); got != "" {
		t.Fatalf("the clone already holds the namespace, so it proves nothing: %s", got)
	}
	if got := gitIn(t, remote, "for-each-ref", "--format=%(refname)", reviewref.NamespaceRoot); got == "" {
		t.Fatal("the remote holds no git-pair refs, so the fetch cannot be the fix")
	}
	return clone
}

// §24 is the difference between two failures that look identical. A clone that was never given
// `refs/git-pair/changesets/*` has no archive for any changeset, and a verdict built from that is
// not a verdict about the changeset: `check` would report the review history as unanchored, and
// `integration record` would report a changeset nobody archived. Both are true of the clone and
// false of the work, and both send someone to `change ready` — a command on the author's branch —
// when the fix is a refspec.
//
// The assertion that carries the weight is the second half: after the fetch, the same commands give
// different answers. That is what shows the first answer was about the clone.
func TestCICloneWithoutTheGitPairRefsSaysSo(t *testing.T) {
	f, slug, source, landing := recordFixture(t)
	// Offered again, so `check` has one clear thing left to say once the refs arrive.
	f.SwitchTo("booking")
	runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")
	source = f.Head()
	clone := publishAndClone(t, f)
	gitIn(t, clone, "switch", "--quiet", "booking")

	// `check` reads the derivation and the trunk and no durable ref, so an empty namespace changes
	// nothing about its verdict — which is the point of moving the refs out of the gate. The command
	// that reads the namespace is the one that has to say the refs are missing.
	absent := runIn(t, clone, "check")
	if absent.code != 1 {
		t.Fatalf("check in a clone without the refs exited %d, want 1\n%s%s", absent.code, absent.stdout, absent.stderr)
	}
	mustContain(t, absent.stdout, "has not been reviewed since",
		"the verdict is the changeset's own, with or without the refs")
	mustNotContain(t, absent.stdout, "this clone has no", "and it does not blame the clone for something it never read")

	record := runIn(t, clone, "integration", "record", "--source", source, "--commit", landing,
		"--target", "origin/release/2.x")
	record.mustSucceed(t, "integration", "record")
	mustContain(t, record.stderr, "holds no refs/git-pair/* refs at all", "the recorder says so, because it reads the namespace")
	mustContain(t, record.stderr, "git fetch origin", "and prints the command")

	after := runIn(t, clone, "integration", "record", "--source", source, "--commit", landing,
		"--target", "origin/release/2.x")
	after.mustSucceed(t, "integration", "record")
	if got := gitIn(t, clone, "rev-parse", reviewref.Integration(slug)); len(got) != 40 {
		t.Errorf("the record written from the clone is %q, want a commit sha", got)
	}
}

// `status` and `review queue` read branches and commits, so the durable namespace is not load-bearing
// for them and a CI job that fetched nothing custom must not be told its repository is broken. What
// they do need is the default branch — the tree rule compares against it — and that refusal is
// #TestOneBranchCheckoutNamesTheFetchItIsMissing's business, not this one.
func TestStatusAndQueueWorkWithoutTheGitPairRefs(t *testing.T) {
	f, slug, _, _ := recordFixture(t)
	f.SwitchTo("booking")
	runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")
	clone := publishAndClone(t, f)
	gitIn(t, clone, "switch", "--quiet", "booking")

	status := runIn(t, clone, "status", "--json").mustSucceed(t, "status")
	got := status.json(t)
	if got["changeset"] != slug {
		t.Errorf("changeset = %v, want %s: the branch alone is enough to resolve it", got["changeset"], slug)
	}
	if got["state"] != "READY" {
		t.Errorf("state = %v, want READY: markers are commits, not custom refs", got["state"])
	}
	if got["integrated"] != false {
		t.Errorf("integrated = %v, want false: no record is present, and none is invented", got["integrated"])
	}
	// The trunk fields matter most in exactly this shape: a clone where "has this landed?" is a
	// comparison against a ref the job fetched, and nothing else in the output says which.
	if got["default_branch"] != "origin/main" || got["default_branch_source"] != "origin-head" {
		t.Errorf(`default_branch = %v from %v, want origin/main from origin-head`, got["default_branch"], got["default_branch_source"])
	}
	if got["default_branch_commit"] != shortOf(f.RevParse("main")) {
		t.Errorf("default_branch_commit = %v, want %s", got["default_branch_commit"], shortOf(f.RevParse("main")))
	}

	queue := runIn(t, clone, "review", "queue").mustSucceed(t, "review", "queue")
	mustContain(t, queue.stdout, slug, "the offered changeset is in the queue with no git-pair refs fetched")
}

// The other CI mistake, and the one that looks like a repository fault: a job that checked out one
// branch has no integration branch to compare against, so resolution refuses — correctly. The refusal
// has to name the fetch, because a message that only offers `--default-branch` leaves the reader to
// work out that their checkout is the thing at fault, and a message mentioning the changeset would
// send them to fix a changeset that is fine.
func TestOneBranchCheckoutNamesTheFetchItIsMissing(t *testing.T) {
	f, _, _, _ := recordFixture(t)
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
	f, _, _, _ := recordFixture(t)
	f.SwitchTo("booking")

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
