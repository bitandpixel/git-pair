package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/cli"
	"gitpair/internal/gittest"
)

// `--default-branch` names the destination, and a CI job that runs git-pair from several commands has to
// pass it several times. `GIT_PAIR_DEFAULT_BRANCH` is the same value carried once in the process
// environment, which is what the shipped merge job already does for its own commands
// (`scripts/ci/git-pair-integrate.sh`). What it moves is the destination rather than the amount of work,
// so the tests below hold the two properties that make an inherited destination safe: the command line
// outranks the environment, and the environment never supplies it quietly.

// releaseBranch adds a branch off trunk that carries no changeset, so naming it as the destination
// changes what the run compares against without changing what the run finds. The changeset's own branch is
// left checked out, because the tests below read the changeset's status.
func releaseBranch(t *testing.T, f *gittest.Fixture) {
	t.Helper()
	f.CreateBranch("release", "main")
	f.SwitchTo("booking")
}

// bareRemote publishes the fixture's branches to a fresh bare repository and returns its path, which is
// what a test needs to build the one-branch checkout a CI job starts from.
func bareRemote(t *testing.T, f *gittest.Fixture) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	f.MustGit("init", "--bare", "-b", "main", remote)
	f.MustGit("push", "--quiet", remote, "--all")
	return remote
}

// TestDefaultBranchEnvNamesTheIntegrationBranch: the variable alone answers a question the checkout
// cannot, and says so in both of the places a reader looks — stderr, and the provenance field
// `status --json` reports.
func TestDefaultBranchEnvNamesTheIntegrationBranch(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	releaseBranch(t, f)

	t.Setenv(cli.DefaultBranchEnv, "refs/heads/release")
	res := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status")
	out := res.json(t)

	if out["default_branch"] != "release" {
		t.Errorf("default_branch = %v, want release: the environment is the only place this value could come from", out["default_branch"])
	}
	if out["default_branch_source"] != "env" {
		t.Errorf("default_branch_source = %v, want env: a destination inherited from the shell must not report as one typed",
			out["default_branch_source"])
	}
	mustContain(t, res.stderr, cli.DefaultBranchEnv, "the run says which variable chose the destination")
	mustContain(t, res.stderr, "release", "and what it chose")
	// Resolution is not one call per command, so without the latch a reader gets the same sentence once
	// per resolution rather than once per run.
	if n := strings.Count(res.stderr, cli.DefaultBranchEnv); n != 1 {
		t.Errorf("the note names $%s %d times, want once per run\n%s", cli.DefaultBranchEnv, n, res.stderr)
	}
}

// TestDefaultBranchFlagBeatsTheEnvironment: the flag is the statement about this invocation, so a value
// inherited from the shell cannot contradict it — and a value the run did not use is not announced.
func TestDefaultBranchFlagBeatsTheEnvironment(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	releaseBranch(t, f)

	t.Setenv(cli.DefaultBranchEnv, "refs/heads/release")
	res := runIn(t, f.Dir(), "status", "--json", "--default-branch", "refs/heads/main").mustSucceed(t, "status")
	out := res.json(t)

	if out["default_branch"] != "main" {
		t.Errorf("default_branch = %v, want main: the flag names this run's destination and the variable does not", out["default_branch"])
	}
	if out["default_branch_source"] != "flag" {
		t.Errorf("default_branch_source = %v, want flag", out["default_branch_source"])
	}
	mustNotContain(t, res.stderr, cli.DefaultBranchEnv,
		"no note about a value the run overrode — the sentence is for destinations the reader did not type")
}

// TestDefaultBranchEnvEmptyIsNotSet: `GIT_PAIR_DEFAULT_BRANCH=` is the shape a shell leaves behind when a
// job meant to leave it unset. Resolving an empty ref would be a refusal nobody asked for; git's own
// answer is the answer.
func TestDefaultBranchEnvEmptyIsNotSet(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")

	t.Setenv(cli.DefaultBranchEnv, "")
	res := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status")
	out := res.json(t)

	if out["default_branch"] != "main" || out["default_branch_source"] != "sole-candidate" {
		t.Errorf("with the variable set to empty: %v from %v, want main from sole-candidate",
			out["default_branch"], out["default_branch_source"])
	}
	mustNotContain(t, res.stderr, cli.DefaultBranchEnv, "an empty variable is not a value, so there is nothing to report")
}

// TestDefaultBranchEnvRefusalNamesItsOwnValue: a destination that does not resolve is refused the same way
// however it arrived, and a value nobody typed has to be traceable to where it came from or the reader is
// sent to fix a command line that was never wrong.
func TestDefaultBranchEnvRefusalNamesItsOwnValue(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")

	t.Setenv(cli.DefaultBranchEnv, "refs/heads/nowhere")
	res := runIn(t, f.Dir(), "queue")
	// Exit 1, not 2: a ref that resolves to nothing is the repository saying no, which is what the same
	// value spelled `--default-branch refs/heads/nowhere` already answers with. The two forms have to
	// disagree about nothing except where the value came from.
	if res.code != exitRefusal {
		t.Fatalf("queue exited %d with an unresolvable destination in the environment, want %d\n%s%s",
			res.code, exitRefusal, res.stdout, res.stderr)
	}
	mustContain(t, res.stderr, "refs/heads/nowhere", "the refusal names the ref it could not resolve")
	mustContain(t, res.stderr, cli.DefaultBranchEnv, "and where the run got it")
}

// TestDefaultBranchEnvReplacesTheFlagInACICheckout is the case the variable exists for: a job that
// fetched the branch under review and nothing else has no recorded remote default, so it has to name the
// destination. The flag is the per-command way; the variable is the way that survives being invoked from
// several steps.
func TestDefaultBranchEnvReplacesTheFlagInACICheckout(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	remote := bareRemote(t, f)

	ci := t.TempDir()
	gitIn(t, ci, "init", "--quiet", ".")
	gitIn(t, ci, "remote", "add", "origin", remote)
	gitIn(t, ci, "fetch", "--quiet", "origin", "booking")
	gitIn(t, ci, "checkout", "--quiet", "-b", "booking", "FETCH_HEAD")

	// Without it, the refusal has to offer both escapes, since both work.
	res := runIn(t, ci, "queue")
	if res.code != exitUsage {
		t.Fatalf("queue exited %d with no integration branch to compare against, want %d\n%s%s",
			res.code, exitUsage, res.stdout, res.stderr)
	}
	mustContain(t, res.stderr, "cannot tell which branch is the integration branch", "it refuses, as it must")
	mustContain(t, res.stderr, "--default-branch", "naming the flag")
	mustContain(t, res.stderr, cli.DefaultBranchEnv, "and the variable that spares a job the flag on every call")

	// With it, the same command answers. The fetch of trunk is the part the job skipped, so the value it
	// names has to be the remote-tracking ref rather than a branch this checkout does not have.
	gitIn(t, ci, "fetch", "--quiet", "origin", "main")
	t.Setenv(cli.DefaultBranchEnv, "refs/remotes/origin/main")
	runIn(t, ci, "queue").mustSucceed(t, "queue")
}

// TestDefaultBranchEnvIsTheNameTheDocsTeach: the variable is an interface, and README and the shipped
// skill are where a reader learns it. A rename that reaches one of the three and not the others leaves a
// document teaching a variable nothing reads — the exact drift this changeset started as.
func TestDefaultBranchEnvIsTheNameTheDocsTeach(t *testing.T) {
	if cli.DefaultBranchEnv != "GIT_PAIR_DEFAULT_BRANCH" {
		t.Fatalf("cli.DefaultBranchEnv = %q, want %q: the name in the code is the name every other surface teaches",
			cli.DefaultBranchEnv, "GIT_PAIR_DEFAULT_BRANCH")
	}
	for _, file := range []string{"../../README.md", "../../skills/git-pair/references/cli.md"} {
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if !strings.Contains(string(text), cli.DefaultBranchEnv) {
			t.Errorf("%s never names $%s, which is now an interface of the command", file, cli.DefaultBranchEnv)
		}
	}
}
