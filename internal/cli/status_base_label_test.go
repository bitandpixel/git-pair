package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// `short` is a sha-width helper, and the `Base:` line ran every value through it - including the branch names.
// A parent branch called `feature/auth` was printed as `feature`, which is no name anywhere, on the one line
// whose job is to say where the work is measured from. The commit a landed parent leaves behind is still
// abbreviated, because that is what a commit looks like on this line; a name somebody wrote is not.
func TestStatusNamesAParentBranchAtFullLength(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("feature/auth")
	f.CommitChangeset("feature-auth", "main")
	f.Commit("auth work", gittest.WithFile("auth.go", "package main\n"))
	f.CreateBranch("feature/auth-tests", "feature/auth")
	f.Commit("auth-tests changeset", gittest.WithFiles(map[string]string{
		"changesets/feature-auth-tests/CHANGESET.yaml": "id: feature-auth-tests\nparent: feature/auth\nparent-changeset: feature-auth\n",
		"changesets/feature-auth-tests/ABOUT.md":       "# feature-auth-tests\n",
	}), gittest.WithFile("auth_test.go", "package main\n"))

	res := runIn(t, f.Dir(), "status", "--changeset", "feature-auth-tests").mustSucceed(t, "status")
	mustContain(t, res.stdout, "Base: feature/auth — the parent branch", "the parent branch, named in full")
	if strings.Contains(res.stdout, "Base: feature —") {
		t.Errorf("the branch name was abbreviated to sha width:\n%s", res.stdout)
	}
}
