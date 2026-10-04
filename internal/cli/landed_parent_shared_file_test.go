package cli_test

import (
	"encoding/json"
	"testing"

	"gitpair/internal/gittest"
)

// The shape the question was asked about: the parent edits the same file the child edits, and then lands. All
// three landing shapes are asked in one table, because they are three ways for the parent's work to reach the
// destination and only two of them leave the parent's commits in its history.
//
// What has to stay true in every one is that the child's own edit of that shared file is still the only
// content above the ground. The parent — including the approval the parent got after its child was reviewed,
// which is a commit on the parent's branch — sits at or below the commit the approval recorded measuring
// from, whatever shape the landing took. So the content question answers "still this work" and the verdict is
// ready, and it is answered by the comparison of contributions rather than by the older one of two base trees.
func TestLandedParentThatEditedTheSameFileKeepsTheChildsApproval(t *testing.T) {
	// The file is long enough that two edits at opposite ends are two hunks. Two edits three lines apart are
	// one hunk to git, and one hunk is a conflict rather than the clean merge this fixture needs.
	shared := "header\n"
	for i := 1; i <= 12; i++ {
		shared += "line " + string(rune('a'+i-1)) + "\n"
	}
	shared += "the end\n"
	parentWork := shared + "parent note\n"         // the parent writes at the bottom
	childWork := "the child's line\n" + parentWork // the child writes at the top, on top of the parent

	for _, tc := range []struct {
		name string
		land func(t *testing.T, f *gittest.Fixture)
	}{
		{"fast-forward", func(t *testing.T, f *gittest.Fixture) {
			f.MustGit("merge", "--ff-only", "--no-edit", "booking")
		}},
		{"merge", func(t *testing.T, f *gittest.Fixture) {
			f.MustGit("merge", "--no-ff", "--no-edit", "-m", "booking: merge the branch", "booking")
		}},
		{"squash", func(t *testing.T, f *gittest.Fixture) {
			// What `change integrate` leaves behind: the parent's record in the landed home rather than the
			// live one, and the squash carrying the parent's content.
			f.Commit("booking: squash the branch", gittest.WithFiles(map[string]string{
				"changesets/.landed/booking/ABOUT.md":       "# booking\n",
				"changesets/.landed/booking/CHANGESET.yaml": "id: booking\nbranch: booking\nbase: main\n",
				"shared.txt": parentWork,
			}))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRepo(t)
			f.Commit("seed the shared file", gittest.WithFile("shared.txt", shared))
			f.CreateBranch("booking")
			f.CommitChangeset("booking", "main")
			f.Commit("booking: edits the bottom of the shared file", gittest.WithFile("shared.txt", parentWork))

			f.CreateBranch("booking-tests")
			f.Commit("changeset booking-tests", gittest.WithFiles(map[string]string{
				"changesets/booking-tests/CHANGESET.yaml": "id: booking-tests\nparent: booking\nparent-changeset: booking\n",
				"changesets/booking-tests/ABOUT.md":       "# booking-tests\n",
			}))
			f.Commit("booking-tests: edits the top of the same file", gittest.WithFile("shared.txt", childWork))
			ready(t, f)
			submit(t, f, "approve")

			// The parent is reviewed after its child, so the base the child's approval recorded is older than
			// the landing. That ordering is the common one, and it is the case the older comparison used to
			// answer instead of the content question.
			f.SwitchTo("booking")
			ready(t, f)
			submit(t, f, "approve")

			f.SwitchTo("main")
			tc.land(t, f)
			f.SwitchTo("booking-tests")

			var verdict struct {
				Ready            bool     `json:"ready"`
				State            string   `json:"state"`
				Reasons          []string `json:"reasons"`
				ParentLanded     bool     `json:"parent_landed"`
				ParentLandedComm string   `json:"parent_landed_commit"`
				ParentComparison string   `json:"parent_comparison"`
			}
			res := runIn(t, f.Dir(), "check", "--json")
			if err := json.Unmarshal([]byte(res.stdout), &verdict); err != nil {
				t.Fatalf("stdout: %s\nstderr: %s\nerr: %v", res.stdout, res.stderr, err)
			}
			if !verdict.Ready {
				t.Fatalf("ready = false (state %s), reasons %v: the parent's edit of the shared file sits at or "+
					"below the commit the approval recorded measuring from, so this branch still contributes only "+
					"what was read", verdict.State, verdict.Reasons)
			}
			if !verdict.ParentLanded || verdict.ParentLandedComm == "" {
				t.Errorf("parent_landed = %v with landing %q", verdict.ParentLanded, verdict.ParentLandedComm)
			}
			if verdict.ParentComparison != "contribution" {
				t.Errorf("parent_comparison = %q, want the content question answered as itself rather than by the "+
					"older comparison of two base trees", verdict.ParentComparison)
			}
			note, _ := parentJSONOf(t, f, "booking-tests")["note"].(string)
			mustContain(t, note, "measured above the landing", "the note naming the ground that was measured")
			mustContain(t, note, "still contributes", "the note saying whose content it measured")
		})
	}
}
