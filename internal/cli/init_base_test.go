package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// `base:` is written once, at `init`, and read by everything after it: the diff, the landed test, the
// `Base:` line of `status`, and whoever reads the file on another machine. Recording it as the ref this clone
// happens to reach trunk through made all four of those answer differently depending on what had been
// fetched — and the ordinary clone triggers it, because `git clone` records the remote's default branch in
// `refs/remotes/origin/HEAD` and `DefaultBranch` prefers that answer over a local trunk sitting right there.
// The file then said `base: refs/remotes/origin/main`, and `status` reported the base as a fetched remote ref
// for a change that had never been pushed anywhere.

func TestInitRecordsTheBranchNameInAnOrdinaryClone(t *testing.T) {
	f := newRepo(t)
	// The shape `git clone` leaves: the remote's default recorded, and a local trunk as well.
	f.MustGit("update-ref", "refs/remotes/origin/main", f.RevParse("main"))
	f.MustGit("symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	f.CreateBranch("bookings")

	runIn(t, f.Dir(), "init").mustSucceed(t, "init")
	body := readChangesetYAML(t, f, "bookings")
	if !strings.Contains(body, "base: main\n") {
		t.Errorf("CHANGESET.yaml records:\n%s\nwant `base: main`: the branch, not the root this clone happens to reach it through", body)
	}
	if strings.Contains(body, "refs/remotes") {
		t.Errorf("CHANGESET.yaml carries a fetch ref:\n%s\nwhich reads as a different destination on the machine that has never fetched", body)
	}
	// The assertion the first version of this test lacked: the recorded base has to resolve, because a base
	// that does not is every command failing with `cannot resolve changeset base`.
	res := runIn(t, f.Dir(), "status")
	res.mustSucceed(t, "status")
	mustNotContain(t, res.stderr, "not the same commit",
		"a base that agrees with its remote copy is not a divergence")
}

// Where the clone holds the integration branch only under the fetch root, the qualified ref is what gets
// recorded: `main` resolves to nothing there, and a changeset whose base cannot be resolved is a changeset
// no command can read. The name is the preference, not the rule.
func TestInitRecordsTheFetchRefWhenTheNameResolvesToNothing(t *testing.T) {
	f := newRepo(t)
	f.MustGit("update-ref", "refs/remotes/origin/main", f.RevParse("main"))
	f.CreateBranch("bookings")
	f.MustGit("branch", "-D", "main")

	runIn(t, f.Dir(), "init").mustSucceed(t, "init")
	body := readChangesetYAML(t, f, "bookings")
	if !strings.Contains(body, "base: refs/remotes/origin/main\n") {
		t.Errorf("CHANGESET.yaml records:\n%s\nwant the qualified ref: this clone has no local trunk, so a bare name resolves to nothing", body)
	}
	runIn(t, f.Dir(), "status").mustSucceed(t, "status")
}

// The other half of the same confusion: the trunk in this clone has commits the remote's does not, so the
// diff measured from here is not the diff the forge will show, and `status` reports a base resolved through
// `refs/remotes/`. It is a note, not a refusal — a base may legitimately be ahead locally, and pushing it is
// not git-pair's to do (§26).
func TestInitNotesABaseThatDiffersFromItsRemoteCopy(t *testing.T) {
	f := newRepo(t)
	f.Commit("trunk work that was never pushed", gittest.WithFile("b.go", "package main\n"))
	f.MustGit("update-ref", "refs/remotes/origin/main", f.RevParse("main~1"))
	f.CreateBranch("bookings")

	res := runIn(t, f.Dir(), "init")
	res.mustSucceed(t, "init")
	mustContain(t, res.stderr, "base: main", "the name is still what gets recorded")
	mustContain(t, res.stderr, "not the same commit here and on origin",
		"and the difference between the two copies is said out loud, where the base is chosen")
	mustContain(t, res.stderr, "1 here that origin does not have",
		"with the counts, because ahead-and-behind is the thing the author is about to read in status")
	mustContain(t, res.stderr, "pushing main is yours", "and the remedy is named without being performed")
}

// `--base` is the caller naming a ref, and it is recorded as it was typed: this note is about what git-pair
// inferred, not about overriding what the author said.
func TestInitStillRecordsABaseThatWasNamed(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("bookings")
	runIn(t, f.Dir(), "init", "--base", "main").mustSucceed(t, "init")
	if body := readChangesetYAML(t, f, "bookings"); !strings.Contains(body, "base: main\n") {
		t.Errorf("CHANGESET.yaml records:\n%s\nwant the base that was named", body)
	}
}

func readChangesetYAML(t *testing.T, f *gittest.Fixture, id string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(f.Dir(), "changesets", id, "CHANGESET.yaml"))
	if err != nil {
		t.Fatalf("read %s's CHANGESET.yaml: %v", id, err)
	}
	return string(body)
}
