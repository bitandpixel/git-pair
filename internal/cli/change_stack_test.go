package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The exits the branch-shape refusal offers (PRD 4). `change stack` records the link that makes a second directory
// into an ancestor rather than an unexplained arrival; `change combine` is the other answer and lives in
// change_combine_test.go. Both are asserted on their refusal text as well as their writes, because the refusal is
// where the author learns which exit fits.

// stackedBranch is the two-level case with the record missing: this branch carries the parent's directory, the child
// has a changeset of its own, and nothing ties the two - which is the shape `change ready` refuses and this repairs.
func stackedBranch(t *testing.T) (*gittest.Fixture, string, string) {
	t.Helper()
	f := newRepo(t)
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("booking work", gittest.WithFile("service.go", "package main\n"))
	f.CreateBranch("booking-tests", "booking")
	f.CommitChangeset("booking-tests", "main")
	f.Commit("tests work", gittest.WithFile("tests.go", "package main\n"))
	return f, "booking", "booking-tests"
}

func TestChangeStackRecordsTheLinkTheBranchWasMissing(t *testing.T) {
	f, parent, child := stackedBranch(t)

	r := runIn(t, f.Dir(), "change", "stack", "--base", parent).mustSucceed(t, "change stack")
	mustContain(t, r.stdout, "base: main -> "+parent, "the command prints the old base and the new one")
	mustContain(t, r.stdout, "base-changeset: (none) -> "+parent, "and both halves of the pair it wrote")
	mustContain(t, r.stdout, "change ready", "a base that moves means the branch has to be offered again")

	md := f.Read("changesets/" + child + "/CHANGESET.yaml")
	for _, want := range []string{"base: " + parent + "\n", "base-changeset: " + parent + "\n"} {
		if !strings.Contains(md, want) {
			t.Errorf("the child's file does not record %q:\n%s", want, md)
		}
	}

	// One path, and inside changesets/. The parent's record belongs to its own branch, and this branch holding a
	// copy of the parent's directory is what every stacked branch looks like - rewriting it here would move a
	// comparison somebody else's review is resting on.
	paths := strings.Fields(f.MustGit("diff", "--name-only", "HEAD~1", "HEAD"))
	if len(paths) != 1 || paths[0] != "changesets/"+child+"/CHANGESET.yaml" {
		t.Errorf("the commit touched %v, want only the child's CHANGESET.yaml", paths)
	}

	// The repair is the point: the branch can be offered once the record exists.
	ready(t, f).mustSucceed(t, "change ready")
}

// A third level. This branch carries the grandparent as well, so the parent candidates are the shared set minus the
// ancestors the base branch records - and the exclusion has to be read from there, because telling this branch to
// exclude its own ancestors would ask it to exclude the fact this command is about to write.
func TestChangeStackWritesTheDeepestLinkOnAThirdLevel(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("feature/auth")
	f.CommitChangeset("feature-auth", "main")
	f.Commit("auth work", gittest.WithFile("auth.go", "package main\n"))

	f.CreateBranch("feature/auth-tests", "feature/auth")
	f.Commit("auth-tests changeset", gittest.WithFiles(map[string]string{
		"changesets/feature-auth-tests/CHANGESET.yaml": "id: feature-auth-tests\nbase: feature/auth\nbase-changeset: feature-auth\n",
		"changesets/feature-auth-tests/ABOUT.md":       "# feature-auth-tests\n",
	}), gittest.WithFile("auth_test.go", "package main\n"))

	f.CreateBranch("feature/auth-cases", "feature/auth-tests")
	f.CommitChangeset("feature-auth-cases", "main")

	runIn(t, f.Dir(), "change", "stack", "--base", "feature/auth-tests").mustSucceed(t, "change stack")
	md := f.Read("changesets/feature-auth-cases/CHANGESET.yaml")
	if !strings.Contains(md, "base: feature/auth-tests\n") || !strings.Contains(md, "base-changeset: feature-auth-tests\n") {
		t.Errorf("the command wrote the wrong level, so it picked the grandparent it should have excluded:\n%s", md)
	}
}

// The base branch records no chain, so both of its directories are candidates for the level below and the command
// cannot choose. The author can: the message says which branch to settle first.
func TestChangeStackRefusesWhenTheBaseBranchRecordsNoChain(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("feature/auth")
	f.CommitChangeset("feature-auth", "main")
	f.CreateBranch("feature/auth-tests", "feature/auth")
	f.Commit("auth-tests changeset, no id recorded", gittest.WithFiles(map[string]string{
		"changesets/feature-auth-tests/CHANGESET.yaml": "id: feature-auth-tests\nbase: feature/auth\n",
		"changesets/feature-auth-tests/ABOUT.md":       "# feature-auth-tests\n",
	}))
	f.CreateBranch("feature/auth-cases", "feature/auth-tests")
	f.CommitChangeset("feature-auth-cases", "main")

	r := runIn(t, f.Dir(), "change", "stack", "--base", "feature/auth-tests")
	if r.code != exitUsage {
		t.Fatalf("a base branch with no recorded chain exited %d, want %d\nstderr: %s", r.code, exitUsage, r.stderr)
	}
	out := r.stdout + r.stderr
	for _, want := range []string{"feature-auth", "feature-auth-tests", "level below first"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, out)
		}
	}
}

// Nothing in common means the named branch is not this branch's base, and the answer is not a stack at all: it is a
// `base:` naming the integration branch. The message says that, and names both sets, because the author chose
// --base and has to be told what the comparison found.
func TestChangeStackRefusesABaseThatSharesNothing(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.CreateBranch("beta", "main")
	f.CommitChangeset("beta", "main")

	r := runIn(t, f.Dir(), "change", "stack", "--base", "alpha")
	if r.code != exitUsage {
		t.Fatalf("a base sharing nothing exited %d, want %d\nstderr: %s", r.code, exitUsage, r.stderr)
	}
	out := r.stdout + r.stderr
	for _, want := range []string{"alpha", "beta", "main"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, out)
		}
	}
}

// Two of this branch's own is the shape `change combine` answers, and the refusal names it: the author has to be
// sent to the other exit rather than told the command could not decide.
func TestChangeStackRefusesABranchCarryingTwoOfItsOwn(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("shared-base")
	f.CommitChangeset("shared", "main")
	f.CreateBranch("two-of-ours", "shared-base")
	f.CommitChangeset("work-one", "main")
	f.Commit("second changeset added later", gittest.WithFiles(map[string]string{
		"changesets/work-two/CHANGESET.yaml": "id: work-two\nbase: main\n",
		"changesets/work-two/ABOUT.md":       "# work-two\n",
		"more.go":                            "package main\n",
	}))

	r := runIn(t, f.Dir(), "change", "stack", "--base", "shared-base")
	if r.code != exitUsage {
		t.Fatalf("two changesets of its own exited %d, want %d\nstderr: %s", r.code, exitUsage, r.stderr)
	}
	out := r.stdout + r.stderr
	for _, want := range []string{"work-one", "work-two", "change combine"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, out)
		}
	}
}

// The base cannot be this branch, which is the same claim `init` makes about its own base: the stack would move with
// every commit, and the comparison becomes the branch against itself.
func TestChangeStackRefusesItsOwnBranch(t *testing.T) {
	f, _, child := stackedBranch(t)

	r := runIn(t, f.Dir(), "change", "stack", "--base", "booking-tests")
	if r.code != exitUsage {
		t.Fatalf("--base naming this branch exited %d, want %d\nstderr: %s", r.code, exitUsage, r.stderr)
	}
	mustContain(t, r.stdout+r.stderr, "git switch -c", "the refusal says to make a branch for the child")
	if strings.Contains(f.Read("changesets/"+child+"/CHANGESET.yaml"), "base-changeset") {
		t.Error("the refused command still wrote a record")
	}
}
