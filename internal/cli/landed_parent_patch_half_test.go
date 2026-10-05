package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The second recorded half earns its keep on one shape: a file this branch edits, which the destination also
// edited, and the two edits nowhere near each other. Measured above the landing, the contribution's *content*
// identity moves — a blob OID names the whole file, and the file now carries trunk's lines too — while the
// hunks the reviewer read are byte for byte what they were. Comparing the content half alone put that case
// back for review; the rendered half answers it, and the merge probe below is what makes the answer safe.

func landedStackWithSharedFile(t *testing.T) (*gittest.Fixture, string) {
	t.Helper()
	shared := "header\n"
	for i := 1; i <= 12; i++ {
		shared += "line " + string(rune('a'+i-1)) + "\n"
	}
	shared += "the end\n"
	parentWork := shared + "parent note\n"
	childWork := "the child's line\n" + parentWork

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

	// The parent is reviewed after its child, which is the ordering that leaves the child's recorded value
	// older than the landing.
	f.SwitchTo("booking")
	ready(t, f)
	submit(t, f, "approve")

	f.SwitchTo("main")
	f.MustGit("merge", "--ff-only", "--no-edit", "booking")
	return f, parentWork
}

// The destination edits the same file the child edits, far below the child's own line; the child takes the
// destination in, which is what a branch does while it waits for its parent's review; and the gate asks
// whether the content under the approval is still there. It is, and `parent_comparison` says which reading
// answered, because a reader who does not believe the verdict needs to know what was compared.
func TestTrunkEditingTheSameFileKeepsTheApprovalOnTheRenderedHalf(t *testing.T) {
	f, parentWork := landedStackWithSharedFile(t)

	f.Commit("trunk adds a line at the bottom", gittest.WithFile("shared.txt", parentWork+"trunk note\n"))
	f.SwitchTo("booking-tests")
	f.MustGit("merge", "--no-edit", "main")

	var verdict struct {
		Ready            bool     `json:"ready"`
		Reasons          []string `json:"reasons"`
		ParentComparison string   `json:"parent_comparison"`
		DriftCredited    []string `json:"drift_credited"`
	}
	res := runIn(t, f.Dir(), "check", "--json")
	if err := json.Unmarshal([]byte(res.stdout), &verdict); err != nil {
		t.Fatalf("stdout: %s\nstderr: %s", res.stdout, res.stderr)
	}
	if !verdict.Ready {
		t.Fatalf("ready = false (reasons %v): the child's hunks are what the reviewer read, and the file's new "+
			"line is trunk's, 14 lines below them", verdict.Reasons)
	}
	if verdict.ParentComparison != "contribution-patch" {
		t.Errorf("parent_comparison = %q, want the rendered half to be the reading that answered", verdict.ParentComparison)
	}
	if strings.Join(verdict.DriftCredited, ",") != "shared.txt" {
		t.Errorf("drift_credited = %v, want shared.txt: the same merge is what moved the content half", verdict.DriftCredited)
	}
}

// The other side of the rendered half, and the reason it is not a blanket pass: the destination rewrote a
// line *inside* the three lines of context the reviewer read. The child's insertion is the same text in the
// same place, and that is exactly why the content comparison alone cannot see the difference — so the patch
// id moves, the relaxation is not available, and the approval goes back for review.
func TestTrunkEditingAReviewedContextLineRefusesTheChild(t *testing.T) {
	f, parentWork := landedStackWithSharedFile(t)

	rewritten := strings.Replace(parentWork, "line b\n", "line b, rewritten by trunk\n", 1)
	f.Commit("trunk rewrites a line the child's hunk shows as context", gittest.WithFile("shared.txt", rewritten))
	f.SwitchTo("booking-tests")
	f.MustGit("merge", "--no-edit", "main")

	res := runIn(t, f.Dir(), "check", "--json")
	out := res.json(t)
	if out["ready"] == true {
		t.Fatalf("check passed: the reviewer's context is no longer what head carries: %v", out)
	}
	reasons, _ := out["reasons"].([]any)
	joined := res.stdout + strings.Join(stringList(reasons), "\n")
	if !strings.Contains(joined, "the diff under it differs") {
		t.Errorf("reasons = %v, want the parent comparison to refuse rather than the unreviewed-content rule", reasons)
	}
}

// stringList is `[]any` of strings read as `[]string`, for a message that wants to print them.
func stringList(items []any) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// The relaxation is available to an approval that recorded the rendered half, and to no other. The control
// is the same fixture with the marker written the way the first version wrote it — `<version>:<raw>` alone:
// the content half still answers its own question, and where the content half moves there is nothing to fall
// back on. Reading `1:` as "no identity recorded" instead would put every approval written before the second
// half into the same refusal.
func TestAnApprovalFromTheFirstVersionDoesNotRelaxOnTheRenderedHalf(t *testing.T) {
	f, parentWork := landedStackWithSharedFile(t)
	f.SwitchTo("booking-tests")

	const trailer = "Review-Diff-Id: "
	message := f.Message(f.Head())
	half := firstHalfOf(message, trailer)
	if half == "" {
		t.Fatalf("the marker recorded no %s trailer with a rendered half:\n%s", trailer, message)
	}
	lines := strings.Split(message, "\n")
	for i, part := range lines {
		if strings.HasPrefix(part, trailer) {
			lines[i] = trailer + half
		}
	}
	f.MustGit("commit", "--amend", "--allow-empty", "-m", strings.Join(lines, "\n"))

	f.SwitchTo("main")
	f.Commit("trunk adds a line at the bottom", gittest.WithFile("shared.txt", parentWork+"trunk note\n"))
	f.SwitchTo("booking-tests")
	f.MustGit("merge", "--no-edit", "main")

	res := runIn(t, f.Dir(), "check", "--json")
	out := res.json(t)
	if out["ready"] == true {
		t.Fatalf("check passed on a recorded value that carries no rendered half: %v", out)
	}
	if comparison, _ := out["parent_comparison"].(string); comparison == "contribution-patch" {
		t.Errorf("parent_comparison = %q: a marker cannot relax on a half it never recorded", comparison)
	}
	reasons, _ := out["reasons"].([]any)
	if !strings.Contains(strings.Join(stringList(reasons), "\n"), "the diff under it differs") {
		t.Errorf("reasons = %v, want the content comparison to be the one that refused", reasons)
	}
}

// firstHalfOf is the recorded value with its rendered half removed, which is the shape `1:` wrote.
func firstHalfOf(message, trailer string) string {
	for _, part := range strings.Split(message, "\n") {
		if strings.HasPrefix(part, trailer) {
			value := strings.TrimPrefix(part, trailer)
			if i := strings.Index(value, "+"); i > 0 {
				return value[:i]
			}
		}
	}
	return ""
}
