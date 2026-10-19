package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// git pairs a file that leaves with a file that arrives when they read alike, and reports the pair as one
// renamed path. The path that went away is then in no list `git diff --name-only` builds — not the list a
// review report prints, not the changed-path list a refused `git pair diff` prints, not the list the
// parent-movement note compares against the files this branch changes. Every one of those lists is asked "at
// which paths does the content or its presence differ", so every one of them names both halves. These tests
// are the four surfaces a reader reads, one each.
//
// sameBytes is the text that makes git fold: forty lines, identical on both sides of the pair.

func sameBytes(line string) string { return strings.Repeat(line+"\n", 40) }

func listsPath(list []any, want string) bool {
	for _, v := range list {
		if s, ok := v.(string); ok && s == want {
			return true
		}
	}
	return false
}

// The review submission is a commit, and the reviewer's own edits are part of it: the note they rewrote under
// a new name is a deletion and an addition, which git reports as one move. The submission report and the
// author's `change feedback` both read that diff, and a file the reviewer deleted is part of what the review
// did — the author reads about it or wonders where the note went.
func TestReviewSubmissionNamesBothHalvesOfAFileGitFolds(t *testing.T) {
	f, _ := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	note := sameBytes("- read the locking section again")
	f.Commit("reviewer's first note", gittest.WithFile("review-notes/round-1.md", note))

	f.Remove("review-notes/round-1.md")
	f.Write("review-notes/round-2.md", note)

	out := runIn(t, f.Dir(), "review", "submit", "--approve", "--json").
		mustSucceed(t, "review", "submit", "--approve", "--json")
	files := out.jsonList(t, "files")
	for _, want := range []string{"review-notes/round-1.md", "review-notes/round-2.md"} {
		if !listsPath(files, want) {
			t.Errorf("review submit files = %v, want %s named: the review deleted that path", files, want)
		}
	}

	feedback := runIn(t, f.Dir(), "change", "feedback", "--name-only").
		mustSucceed(t, "change", "feedback", "--name-only")
	for _, want := range []string{"review-notes/round-1.md", "review-notes/round-2.md"} {
		if !strings.Contains(feedback.stdout, want) {
			t.Errorf("change feedback --name-only omitted %s:\n%s", want, feedback.stdout)
		}
	}
}

// `git pair diff <path>` answers "that path does not appear in this span" by refusing, and it answers it from
// the changed-path list. A deletion git folded away therefore cost the reviewer the one patch they came for:
// the content leaving the tree. The path is in the span — its presence changed — so it is in the list.
func TestGitPairDiffTakesADeletedPathGitFoldedIntoAnother(t *testing.T) {
	f, _ := newChangeset(t, "booking-transaction", "main")
	body := sameBytes("func Account() string { return \"reviewed\" }")
	f.SwitchTo("main")
	f.Commit("the file the branch will drop", gittest.WithFile("account.go", body))
	f.SwitchTo("booking-transaction")
	f.MustGit("merge", "--no-ff", "--no-edit", "main")
	f.Remove("account.go")
	f.Commit("drop it and write one that reads alike", gittest.WithFile("ledger.go", body))
	ready(t, f)

	res := runIn(t, f.Dir(), "diff", "account.go").mustSucceed(t, "diff", "account.go")
	mustContain(t, res.stdout, "--- a/account.go", "the deletion's patch is what the reader came for")
	mustContain(t, res.stdout, "func Account()", "and it names the content that left")

	// The refusal keeps its shape: a path the span really does not mention is still refused, and the list it
	// prints now names the deleted path beside the one that arrived.
	bad := runIn(t, f.Dir(), "diff", "nope.go")
	if bad.code == 0 {
		t.Errorf("diff of an untouched path succeeded:\n%s", bad.stdout)
	}
	for _, want := range []string{"account.go", "ledger.go"} {
		if !strings.Contains(bad.stdout+bad.stderr, want) {
			t.Errorf("the changed-path list of the refusal omits %s:\n%s%s", want, bad.stdout, bad.stderr)
		}
	}
}

// The parent-movement note is the one sentence folding could have made wrong rather than merely short: a
// parent that deletes a file this child changes, and writes a lookalike in its place, used to be reported as
// a parent that touched nothing this branch touches — a false all-clear about the two branches meeting.
//
// The shared file has to exist before the child branches: an overlap is two branches working in one path, and
// a file only the child ever had is nothing for the parent to delete.
func TestParentMovementNamesTheFileTheParentDeleted(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	f.Commit("the parent's test", gittest.WithFile("booking_test.go", sameBytes("func TestBooking() {}")))
	ready(t, f)
	submit(t, f, "approve")

	f.CreateBranch("booking-tests")
	f.Commit("stack on booking", gittest.WithFiles(map[string]string{
		"changesets/booking-tests/CHANGESET.yaml": "id: booking-tests\nparent: booking\nparent-changeset: booking\n",
		"changesets/booking-tests/ABOUT.md":       "# booking-tests\n\nTests for the booking transaction.\n",
		"booking_test.go":                         sameBytes("func TestBooking() { t := 1 }"),
	}))
	ready(t, f)
	submit(t, f, "approve")

	f.SwitchTo("booking")
	// A move of the parent's own file: the same bytes under a new name, which is the pair git is surest
	// about and the one that used to leave the note with nothing to name.
	f.Rename("booking_test.go", "booking_test_v2.go")
	f.Commit("move the test out of the way")
	f.SwitchTo("booking-tests")

	res := runIn(t, f.Dir(), "check").mustSucceed(t, "check")
	mustContain(t, res.stdout, "the parent branch booking moved", "the note still says the parent moved")
	mustContain(t, res.stdout, "booking_test.go",
		"and names the file the parent deleted, which is one this branch changes")
	mustNotContain(t, res.stdout, "none of them touch files this branch changes",
		"the all-clear it used to give, about a parent that removed this branch's file")
}
