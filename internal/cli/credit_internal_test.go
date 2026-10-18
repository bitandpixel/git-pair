package cli

import (
	"context"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/gittest"
)

// The list `creditRefs` assembles is the whole policy of the content condition: which branches may be
// cited when a changeset's head carries content its own review never read, and at which commit each of
// them is cited. It is tested on its own because the two refusals it exists to make are invisible in the
// verdict that gets them — a wrong list reads as a correct refusal, and a too-wide list reads as a correct
// pass.
func TestCreditRefs(t *testing.T) {
	for _, tc := range []struct {
		name string
		// build returns the repository and the changeset to ask about, beside the commit the parent entry
		// is expected to name — "" when no parent entry is expected at all. The commit is what the case
		// cares about, because a parent cited at its branch tip would credit whatever anybody pushes
		// there after the review that covers it.
		build func(t *testing.T) (*gittest.Fixture, changeset.Changeset, string)
		db    changeset.DefaultBranchRef
		want  []string
	}{
		{
			name: "a changeset measured against trunk is credited for trunk alone",
			build: func(t *testing.T) (*gittest.Fixture, changeset.Changeset, string) {
				return creditStack(t, creditShape{})
			},
			db:   changeset.DefaultBranchRef{Ref: "refs/heads/main"},
			want: []string{"main"},
		},
		{
			name: "a child takes its approved unlanded parent beside trunk",
			build: func(t *testing.T) (*gittest.Fixture, changeset.Changeset, string) {
				return creditStack(t, creditShape{stacked: true, approveParent: true})
			},
			db:   changeset.DefaultBranchRef{Ref: "refs/heads/main"},
			want: []string{"main", "alpha"},
		},
		{
			name: "a parent's work added after its own approval is not what the parent is cited at",
			build: func(t *testing.T) (*gittest.Fixture, changeset.Changeset, string) {
				return creditStack(t, creditShape{stacked: true, approveParent: true, parentWorkAfterApproval: true})
			},
			db:   changeset.DefaultBranchRef{Ref: "refs/heads/main"},
			want: []string{"main", "alpha"},
		},
		{
			name: "a parent with no approval of its own is not cited",
			build: func(t *testing.T) (*gittest.Fixture, changeset.Changeset, string) {
				return creditStack(t, creditShape{stacked: true})
			},
			db:   changeset.DefaultBranchRef{Ref: "refs/heads/main"},
			want: []string{"main"},
		},
		{
			name: "a parent the record names only as a branch is not cited",
			build: func(t *testing.T) (*gittest.Fixture, changeset.Changeset, string) {
				return creditStack(t, creditShape{stacked: true, approveParent: true, omitBaseChangeset: true})
			},
			db:   changeset.DefaultBranchRef{Ref: "refs/heads/main"},
			want: []string{"main"},
		},
		{
			name: "a landed parent is not cited by name",
			build: func(t *testing.T) (*gittest.Fixture, changeset.Changeset, string) {
				return creditStack(t, creditShape{stacked: true, approveParent: true, landParent: true})
			},
			db:   changeset.DefaultBranchRef{Ref: "refs/heads/main"},
			want: []string{"main"},
		},
		{
			name: "a clone that cannot name trunk credits nothing, because no landing can be proved",
			build: func(t *testing.T) (*gittest.Fixture, changeset.Changeset, string) {
				f, cs, _ := creditStack(t, creditShape{stacked: true, approveParent: true})
				return f, cs, ""
			},
			db:   changeset.DefaultBranchRef{},
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, cs, wantParent := tc.build(t)
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
			if wantParent == "" {
				return
			}
			// The parent is named by the branch and asked as a commit, so the decision is the one this run
			// made rather than a ref somebody moves before the read.
			if len(got) == 0 {
				t.Fatalf("no credit ref at all, want the parent cited at %s", wantParent)
			}
			last := got[len(got)-1]
			if last.Name != "alpha" {
				t.Fatalf("last credit = %+v, want the parent branch", last)
			}
			if last.Ref != wantParent {
				tip, err := repo.RevParse(context.Background(), "alpha")
				if err != nil {
					t.Fatalf("rev-parse alpha: %v", err)
				}
				t.Errorf("parent credit = %s at %s, want it at %s (the branch tip today is %s)",
					last.Name, last.Ref, wantParent, tip)
			}
		})
	}
}

// creditShape is the shape of the stack the credit policy is asked about: whether `beta` is a child at
// all, whether the parent was ever approved, whether the parent took on work after that approval, whether
// the child's record names the parent's changeset, and whether the parent has landed.
type creditShape struct {
	stacked                 bool
	approveParent           bool
	parentWorkAfterApproval bool
	// omitBaseChangeset builds the record a `parent:`-only changeset left behind: the branch is named
	// and no changeset is, so there is nothing to look a review up by.
	omitBaseChangeset bool
	landParent        bool
}

// creditStack builds `alpha` with its own changeset and `beta` on top of it, leaving beta checked out, and
// returns the commit alpha's approval spoke about ("" where the parent was never approved, or no parent is
// in play). The approval is recorded by the fixture rather than by `review submit` because this is the
// internal package and the command is tested end to end elsewhere.
func creditStack(t *testing.T, shape creditShape) (*gittest.Fixture, changeset.Changeset, string) {
	t.Helper()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	approved := ""
	if shape.approveParent {
		f.CommitReadyMarker("alpha")
		// The commit a review submission speaks about is the head when it is written, and that is the
		// commit the credit cites: the approval marker itself is bookkeeping, and the branch tip is
		// whatever anybody pushes afterwards.
		approved = f.Head()
		f.CommitReviewMarker("alpha", "approve")
	}
	if shape.parentWorkAfterApproval {
		f.Commit("alpha work nobody reviewed", gittest.WithFile("late.go", "package main\n"))
	}
	if shape.landParent {
		f.SwitchTo("main")
		f.MustGit("merge", "--no-ff", "--no-edit", "-m", "land alpha", "alpha")
		f.SwitchTo("alpha")
	}
	cs := changeset.Changeset{Slug: "beta", Branch: "beta", Base: "main"}
	if !shape.stacked {
		f.CreateBranch("beta")
		f.CommitChangeset("beta", "main")
		f.Commit("beta work", gittest.WithFile("b.go", "package main\n"))
		return f, cs, ""
	}
	record := "id: beta\nbase: alpha\n"
	parent := changeset.Changeset{Slug: "beta", Branch: "beta", Base: "alpha", ParentBranch: "alpha"}
	if shape.omitBaseChangeset {
		// The shape a `parent:`-only record leaves behind: a branch is named, and no changeset is. The
		// approval still happened, so the case asks whether a name alone can buy a citation.
		approved = ""
	} else {
		record += "base-changeset: alpha\n"
		parent.BaseChangeset = "alpha"
	}
	f.CreateBranch("beta")
	f.Commit("changeset beta", gittest.WithFiles(map[string]string{
		"changesets/beta/CHANGESET.yaml": record,
		"changesets/beta/ABOUT.md":       "# beta\n",
	}))
	f.Commit("beta work", gittest.WithFile("b.go", "package main\n"))
	if shape.landParent {
		// The parent is landed, so no ref is expected for it: the assertion about the parent's commit
		// belongs to the cases that cite the branch.
		return f, parent, ""
	}
	return f, parent, approved
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
