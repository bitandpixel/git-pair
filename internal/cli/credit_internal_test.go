package cli

import (
	"context"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/gittest"
)

// The list `creditRefs` assembles is the whole policy of the content condition: which branches may be
// cited when a changeset's head carries content its own review never read. It is tested on its own
// because the two refusals it exists to make are invisible in the verdict that gets them — a wrong list
// reads as a correct refusal, and a too-wide list reads as a correct pass.
func TestCreditRefs(t *testing.T) {
	for _, tc := range []struct {
		name string
		// build returns the repository and the changeset to ask about, and the parent branch whose tip
		// the parent ref is expected to name ("" when no parent is expected in the list).
		build func(t *testing.T) (*gittest.Fixture, changeset.Changeset, string)
		db    changeset.DefaultBranchRef
		want  []string
	}{
		{
			name:  "a changeset measured against trunk is credited for trunk alone",
			build: func(t *testing.T) (*gittest.Fixture, changeset.Changeset, string) { return stack(t, false, false) },
			db:    changeset.DefaultBranchRef{Ref: "refs/heads/main"},
			want:  []string{"main"},
		},
		{
			name:  "a child takes its unlanded parent beside trunk",
			build: func(t *testing.T) (*gittest.Fixture, changeset.Changeset, string) { return stack(t, false, true) },
			db:    changeset.DefaultBranchRef{Ref: "refs/heads/main"},
			want:  []string{"main", "alpha"},
		},
		{
			name:  "a landed parent is not cited by name",
			build: func(t *testing.T) (*gittest.Fixture, changeset.Changeset, string) { return stack(t, true, true) },
			db:    changeset.DefaultBranchRef{Ref: "refs/heads/main"},
			want:  []string{"main"},
		},
		{
			name:  "a clone that cannot name trunk still credits the parent it is measured on",
			build: func(t *testing.T) (*gittest.Fixture, changeset.Changeset, string) { return stack(t, false, true) },
			db:    changeset.DefaultBranchRef{},
			want:  []string{"alpha"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, cs, parentBranch := tc.build(t)
			repo, err := git.Open(f.Dir())
			if err != nil {
				t.Fatalf("git.Open: %v", err)
			}
			got, err := creditRefs(context.Background(), repo, cs, tc.db)
			if err != nil {
				t.Fatalf("creditRefs: %v", err)
			}
			var names []string
			for _, ref := range got {
				names = append(names, ref.Name)
			}
			if join(names) != join(tc.want) {
				t.Fatalf("credit refs = %v, want %v", names, tc.want)
			}
			if parentBranch == "" {
				return
			}
			// The parent is named by the branch and asked as a commit, so the decision is the one this
			// run made rather than a ref somebody moves before the read.
			want, err := repo.RevParse(context.Background(), parentBranch)
			if err != nil {
				t.Fatalf("rev-parse %s: %v", parentBranch, err)
			}
			last := got[len(got)-1]
			if last.Name != parentBranch || last.Ref != want {
				t.Errorf("parent credit = %+v, want %s at %s", last, parentBranch, want)
			}
		})
	}
}

// stack builds `alpha` with its own changeset and `beta` stacked on it, leaving beta checked out. landParent
// merges alpha into the integration branch first, and stacked makes beta a real child rather than a changeset
// measured against trunk.
func stack(t *testing.T, landParent, stacked bool) (*gittest.Fixture, changeset.Changeset, string) {
	t.Helper()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	if landParent {
		f.SwitchTo("main")
		f.MustGit("merge", "--no-ff", "--no-edit", "-m", "land alpha", "alpha")
		f.SwitchTo("alpha")
	}
	cs := changeset.Changeset{Slug: "beta", Branch: "beta", Base: "main"}
	if !stacked {
		f.CreateBranch("beta")
		f.CommitChangeset("beta", "main")
		f.Commit("beta work", gittest.WithFile("b.go", "package main\n"))
		return f, cs, ""
	}
	f.CreateBranch("beta")
	f.CommitChangeset("beta", "alpha")
	f.Write(f.ChangesetPath("beta", "CHANGESET.yaml"), "id: beta\nbase: alpha\nbase-changeset: alpha\n")
	f.MustGit("add", "-A")
	f.MustGit("commit", "-m", "record beta's parent changeset")
	f.Commit("beta work", gittest.WithFile("b.go", "package main\n"))
	cs = changeset.Changeset{Slug: "beta", Branch: "beta", Base: "alpha",
		ParentBranch: "alpha", BaseChangeset: "alpha"}
	if landParent {
		// The parent is landed, so no ref is expected for it: the assertion about the parent's commit
		// belongs to the cases that cite the branch.
		return f, cs, ""
	}
	return f, cs, "alpha"
}

func join(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ","
		}
		out += n
	}
	return out
}
