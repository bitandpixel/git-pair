package changeset_test

import (
	"context"
	"strings"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/gittest"
)

// The contribution is the value a stacked child's approval is compared with, so its two properties have to
// be provable rather than assumed: taking the destination in moves the ground and leaves the content alone,
// and the review record is not part of the content at all.

// longFile is long enough that two branches editing opposite ends of it merge cleanly. Two edits three lines
// apart are one hunk to git, and one hunk is a conflict rather than the clean merge this needs.
func longFile(body string) string {
	out := body
	for i := 1; i <= 12; i++ {
		out += "line " + string(rune('a'+i-1)) + "\n"
	}
	return out + "the end\n"
}

func measure(t *testing.T, f *gittest.Fixture, child changeset.Changeset, head, landing, recorded string) changeset.Contribution {
	t.Helper()
	return changeset.MeasureContribution(context.Background(), repo(f), child, db(), head, landing, recorded)
}

// Taking the destination in is the case the old rule got backwards: it cost an approval, and it is the thing
// a reviewer most wants before a merge. Where trunk's work is in files this branch does not touch, the
// ground moves — the newest candidate is the trunk commit that just came in — and the contribution does not,
// because that commit already carries what trunk changed. The two assertions are the pair: a value that
// stayed equal *and* a base that moved, so an implementation that measured the same commit twice could not
// pass for one that followed the ground.
func TestContributionIsUnmovedByTakingTheDestinationIn(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	_, child, _ := stacked(t, f)

	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "-m", "alpha: merge the branch", "alpha")
	f.Commit("main: somebody else's work, in a file this branch never touches",
		gittest.WithFile("other.txt", "1\n"))
	landing := f.RevParse("main")
	recorded := f.RevParse("alpha")
	before := f.RevParse("beta")

	first := measure(t, f, child, before, landing, recorded)
	if !first.Measured {
		t.Fatal("the contribution before the merge was not measured")
	}

	f.SwitchTo("beta")
	f.MustGit("merge", "--no-edit", "main")
	after := measure(t, f, child, f.RevParse("beta"), landing, recorded)
	if !after.Measured {
		t.Fatal("the contribution after taking the destination in was not measured")
	}
	if after.Digest != first.Digest {
		t.Errorf("merging the destination in changed the contribution: %s then %s — the trunk's own work is being counted as the child's",
			first.Digest, after.Digest)
	}
	if after.Base == first.Base {
		t.Errorf("the ground did not move (%s both times): equal digests here would be an artifact of measuring the same pair twice",
			short(first.Base))
	}
}

// The limit of that, stated rather than left to be discovered: the identity is of *file content*, not of
// authorship of lines. `--raw` hashes pre- and post-image blob OIDs, and a blob is a whole file — so when
// trunk edits a file this branch also edits, on lines nowhere near the child's, the child's post-image blob
// now contains trunk's work too and the identity moves. A far away trunk line inside a shared file therefore
// does cost a re-review of that file.
//
// This is not a cost the gate imposes today that it did not already: the rule that counts content the review
// never saw refuses on any content arriving from another branch, near or far (`check`'s
// "changed since" reason), and `TestMergingTheDestinationInIsStillUnreviewedWork` pins that. The reason the
// limit is written here is that the follow-up PRD §27 names — teaching that rule about the destination —
// cannot be done with this identity alone, because it cannot tell whose line is whose.
func TestContributionCountsATrunkEditToAFileTheChildAlsoChanges(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("shared.txt", longFile("header\n")))
	_, child, _ := stacked(t, f)

	f.SwitchTo("beta")
	f.Commit("beta: a note at the top", gittest.WithFile("shared.txt", "beta note\nheader\n"+longFile("")))
	before := f.RevParse("beta")
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "-m", "alpha: merge the branch", "alpha")
	f.Commit("main: somebody else's note at the bottom", gittest.WithFile("shared.txt",
		longFile("header\n")+"trunk note\n"))
	landing := f.RevParse("main")
	recorded := f.RevParse("alpha")

	first := measure(t, f, child, before, landing, recorded)
	if !first.Measured {
		t.Fatal("the contribution before the merge was not measured")
	}
	f.SwitchTo("beta")
	f.MustGit("merge", "--no-edit", "main")
	after := measure(t, f, child, f.RevParse("beta"), landing, recorded)
	if after.Digest == first.Digest {
		t.Errorf("the shared file kept its identity across a trunk edit to it (%s): a blob OID is a whole-file "+
			"identity, so this equality would mean the child's post-image had not changed — and it had", first.Digest)
	}
}

// The review record is not the thing reviewed. A reply to a review thread, a landing that moves the parent's
// directory into `.landed/`, and the parent's own record are all changes to files under `changesets/`, and
// none of them may change the identity of the work an approval stands on.
func TestContributionLeavesOutTheReviewRecord(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	_, child, head := stacked(t, f)
	recorded := f.RevParse("alpha")

	want := measure(t, f, child, head, "", recorded)
	if !want.Measured {
		t.Fatal("the contribution was not measured")
	}
	if strings.Join(changeset.DigestExclusions(context.Background(), repo(f), child, db(), head), " ") !=
		"changesets/beta changesets/.landed/beta changesets/alpha changesets/.landed/alpha" {
		t.Errorf("the exclusion is not this changeset's and its ancestor's, in both homes: %v",
			changeset.DigestExclusions(context.Background(), repo(f), child, db(), head))
	}

	f.SwitchTo("beta")
	f.Commit("beta: a reply to the review thread",
		gittest.WithFiles(map[string]string{
			"changesets/beta/ABOUT.md":  "# beta\n\n## Addressed feedback\n\nanswered\n",
			"changesets/alpha/ABOUT.md": "# alpha\n\nsomebody edited the parent's record too\n",
		}))
	got := measure(t, f, child, f.RevParse("beta"), "", recorded)
	if !got.Measured {
		t.Fatal("the contribution after the record changed was not measured")
	}
	if got.Digest != want.Digest {
		t.Errorf("the review record moved the identity of the work: %s then %s", want.Digest, got.Digest)
	}
}
