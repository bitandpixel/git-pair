package git_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"gitpair/internal/git"
	"gitpair/internal/gittest"
)

// blobOIDs matches the object names `--raw` prints, so a test can hide them and ask what is left of the
// entry: modes, status and path, which is the part of the line that is about the shape of the change.
var blobOIDs = regexp.MustCompile(`[0-9a-f]{7,64}`)

// DiffRawDigest is the identity a recorded approval compares against, so its two properties have to be
// provable rather than assumed: configuration cannot move the answer, and content cannot hide from it.
// A digest of patch text has neither — `git patch-id --stable` on one commit hashed three different ways
// under the default, `-U10` and `-U1` — which is why this reads `--raw` instead.

// changedFixture builds a repository with a base commit and one commit of `changes`, and returns the
// repository with both ends.
func changedFixture(t *testing.T, changes map[string]string) (*gittest.Fixture, *git.Repo, string, string) {
	t.Helper()
	f := gittest.New(t)
	f.Commit("base", gittest.WithFile("a.txt", "one\n"), gittest.WithFile("b.txt", "two\n"))
	base := headOf(t, f)
	f.Commit("change", gittest.WithFiles(changes))
	head := headOf(t, f)
	repo, err := git.Open(f.Dir())
	if err != nil {
		t.Fatalf("git.Open(%s): %v", f.Dir(), err)
	}
	repo.Env = f.Env()
	return f, repo, base, head
}

// headOf is the tip of the fixture branch, without the newline git prints.
func headOf(t *testing.T, f *gittest.Fixture) string {
	t.Helper()
	return strings.TrimSpace(f.MustGit("rev-parse", "HEAD"))
}

func digestOf(t *testing.T, repo *git.Repo, from, to string, exclude ...string) string {
	t.Helper()
	d, err := repo.DiffRawDigest(context.Background(), from, to, exclude...)
	if err != nil {
		t.Fatalf("DiffRawDigest(%s, %s): %v", from, to, err)
	}
	return d
}

// What a reader's git configuration prefers about diffs must not reach a value written into an approval:
// `diff.context`, `diff.algorithm` and `diff.renames` are each a way for the same commit to record a
// different identity, and the reviewer's `~/.gitconfig` is not part of what was reviewed.
func TestDiffRawDigestIsNotMovedByDiffConfiguration(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"context width", "diff.context", "40"},
		{"diff algorithm", "diff.algorithm", "patience"},
		{"rename detection", "diff.renames", "true"},
		{"prefix style", "diff.noprefix", "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, repo, base, head := changedFixture(t, map[string]string{"a.txt": "one\nchanged\n"})
			want := digestOf(t, repo, base, head)

			f.Config(tc.key, tc.value)
			repo.ResetMemo()
			if got := digestOf(t, repo, base, head); got != want {
				t.Errorf("%s: digest with %s=%s is %s, want %s — a recorded value cannot depend on whose git computed it",
					tc.name, tc.key, tc.value, got, want)
			}
		})
	}
}

// Content cannot hide from it, which is the other half: a digest that normalises what a reviewer read
// would pass a diff nobody looked at. Trailing whitespace and a file mode are the two cases a patch
// digest is known to wave through.
func TestDiffRawDigestSeesEveryChangeAReviewerRead(t *testing.T) {
	t.Run("trailing whitespace is a change", func(t *testing.T) {
		f, repo, base, plain := changedFixture(t, map[string]string{"a.txt": "one\nchanged\n"})
		f.Write("a.txt", "one\nchanged   \n")
		f.Commit("whitespace only")
		spaced := headOf(t, f)

		if digestOf(t, repo, base, plain) == digestOf(t, repo, base, spaced) {
			t.Error("a whitespace-only change hashed the same as the commit before it")
		}
	})

	t.Run("a mode change is a change", func(t *testing.T) {
		f, repo, base, before := changedFixture(t, map[string]string{"a.txt": "one\nchanged\n"})
		f.Chmod("a.txt", 0o755)
		f.Commit("make it executable")
		after := headOf(t, f)

		if digestOf(t, repo, base, before) == digestOf(t, repo, base, after) {
			t.Error("a mode-only change hashed the same as the commit before it")
		}
	})

	t.Run("different content is a different identity", func(t *testing.T) {
		_, repo1, base1, head1 := changedFixture(t, map[string]string{"a.txt": "one\nchanged\n"})
		_, repo2, base2, head2 := changedFixture(t, map[string]string{"a.txt": "one\nother\n"})
		if digestOf(t, repo1, base1, head1) == digestOf(t, repo2, base2, head2) {
			t.Error("two different contents recorded the same identity")
		}
	})

	// The case a `--raw` digest is suspected of missing, because `--raw` prints no hunk text: the file was
	// already in the diff, the reviewer had looked at it, and now one line inside it changes. The entry's
	// shape — its modes, its status, its path — really is identical between the two; the post-image blob OID
	// is not, and it is one of the hashed fields. So a change to content is never a non-change, and the
	// insensitivity this digest buys is confined to how git renders a diff.
	t.Run("a line changed in a file already in the diff is a change", func(t *testing.T) {
		f, repo, base, first := changedFixture(t, map[string]string{"a.txt": "one\nchanged\n"})
		f.Write("a.txt", "one\nchanged\nand one more line\n")
		f.Commit("one line more in the same file")
		second := headOf(t, f)

		if digestOf(t, repo, base, first) == digestOf(t, repo, base, second) {
			t.Error("a line changed inside a file already in the diff hashed the same as before it")
		}
		rawOf := func(to string) string {
			out, err := repo.Git(context.Background(), "diff", "--raw", "-z", "--no-renames", base, to, "--", ".")
			if err != nil {
				t.Fatalf("git diff --raw %s %s: %v", base, to, err)
			}
			return blobOIDs.ReplaceAllString(out, "<oid>")
		}
		if a, b := rawOf(first), rawOf(second); a != b {
			t.Errorf("the raw entries differ once their blob OIDs are hidden:\n%s\n%s\n— the digest moved for a shape difference, not a content one",
				a, b)
		}
	})
}

// The other direction of the same property, and the one a reviewer has to be able to rely on: a recorded
// value is a fact about two named commits, so work that lands somewhere else cannot move it — not a commit
// to another file, and not a commit to the same file on a line far from the one this branch changed. That
// is the difference between a digest of a diff and a reading of "how far has the world moved since": the
// second expires on somebody else's merge in an unrelated file, and the first does not. What *can* move a
// comparison is naming a different pair of commits, which is the second assertion below: the value is not
// inert, it is pinned to the two it was taken between.
func TestDiffRawDigestIsNotMovedByWorkOnAnotherBranch(t *testing.T) {
	f, repo, base, head := changedFixture(t, map[string]string{"a.txt": "one\nchanged\n"})
	want := digestOf(t, repo, base, head)

	f.CreateBranch("destination", base)
	f.SwitchTo("destination")
	f.Commit("somebody else edits the same file, far away",
		gittest.WithFile("a.txt", "one\nchanged\n\n\n// added well below the reviewed line\n"))
	onDestination := headOf(t, f)
	f.SwitchTo("main")

	if got := digestOf(t, repo, base, head); got != want {
		t.Errorf("a commit on another branch moved the digest of the same two commits: %s, want %s", got, want)
	}
	if digestOf(t, repo, base, onDestination) == want {
		t.Error("the digest between the base and the branch that gained content matched the one before it: " +
			"the value is not pinned to the commits it names, it simply never changes")
	}
}

// The changeset's own directory is excluded by the caller because the review record lives there: a reply
// to a review thread must not change the identity of the approval it is written into.
func TestDiffRawDigestExcludesWhatTheCallerNames(t *testing.T) {
	f, repo, base, before := changedFixture(t, map[string]string{"a.txt": "one\nchanged\n"})
	f.Write("changesets/booking-transaction/ABOUT.md", "### comment\n\nfixed\n")
	f.Commit("reply to the review thread")
	after := headOf(t, f)

	beforeDigest := digestOf(t, repo, base, before, "changesets/booking-transaction")
	afterDigest := digestOf(t, repo, base, after, "changesets/booking-transaction")
	if beforeDigest != afterDigest {
		t.Errorf("excluding the directory still let it move the digest: %s then %s", beforeDigest, afterDigest)
	}
	if digestOf(t, repo, base, before) == digestOf(t, repo, base, after) {
		t.Error("with nothing excluded the reply to the thread was invisible: the exclusion is doing real work")
	}
}

// An unreadable revision is an error rather than the digest of an empty diff: a caller that recorded a
// value for a diff it could not read would be claiming a comparison it never made (PRD §21).
func TestDiffRawDigestRefusesToInventAnIdentity(t *testing.T) {
	_, repo, base, _ := changedFixture(t, map[string]string{"a.txt": "one\nchanged\n"})

	if _, err := repo.DiffRawDigest(context.Background(), base, "0000000000000000000000000000000000000000"); err == nil {
		t.Error("DiffRawDigest succeeded for a revision this clone cannot read")
	}
}

// Two commits that change nothing between them have an identity — the one empty diff — and it is stable,
// so a documentation-only changeset is correctly identical to another one and to itself.
func TestDiffRawDigestOfAnEmptyDiffIsStable(t *testing.T) {
	_, repo, base, _ := changedFixture(t, map[string]string{"a.txt": "one\nchanged\n"})

	if first, second := digestOf(t, repo, base, base), digestOf(t, repo, base, base); first != second || first == "" {
		t.Errorf("empty diff digests are %q and %q, want the same non-empty value", first, second)
	}
}

// identityOf measures both halves of the pair a review records.
func identityOf(t *testing.T, repo *git.Repo, from, to string, exclude ...string) git.DiffIdentity {
	t.Helper()
	id, err := repo.DiffIdentity(context.Background(), from, to, exclude...)
	if err != nil {
		t.Fatalf("DiffIdentity(%s, %s): %v", from, to, err)
	}
	return id
}

// The two halves must come out of one invocation without the raw half drifting away from the value the
// previous version recorded: approvals written before this change carry that value alone, and a half that
// stopped matching would read every one of them as a content difference.
func TestDiffIdentityRawHalfIsTheShippedDigest(t *testing.T) {
	_, repo, base, head := changedFixture(t, map[string]string{"a.txt": "one\nchanged\n", "new.txt": "fresh\n"})
	id := identityOf(t, repo, base, head)
	if want := digestOf(t, repo, base, head); id.Raw != want {
		t.Errorf("raw half = %s, want the digest the same span gives without -U3 (%s)", id.Raw, want)
	}
	if id.Patch == "" {
		t.Error("patch half is absent for a diff with content in it")
	}
}

// groundFixture builds the shape the patch half exists for. The parent and the child edit one file at
// opposite ends; the parent lands, trunk gains one more line, and the child merges that in. Measuring the
// child's contribution from the new ground changes the raw half — a blob OID is the identity of the whole
// file, and trunk's line is now inside it — and must not change the patch half, because the child's hunks
// read exactly as they did when the approval was written.
func groundFixture(t *testing.T, trunkEdit func(lines []string) []string) (f *gittest.Fixture, repo *git.Repo, parentTip, childTip, trunkTip, mergedTip string) {
	t.Helper()
	body := []string{"header"}
	for i := 1; i <= 12; i++ {
		body = append(body, "line "+string(rune('a'+i-1)))
	}
	body = append(body, "the end")
	write := func(lines []string) string { return strings.Join(lines, "\n") + "\n" }

	f = gittest.New(t)
	f.Commit("base", gittest.WithFile("shared.txt", write(body)))
	f.CreateBranch("parent")
	parent := append(append([]string{}, body...), "parent note")
	f.Commit("parent writes the bottom", gittest.WithFile("shared.txt", write(parent)))
	parentTip = f.RevParse("parent")

	f.CreateBranch("trunk")
	f.Commit("trunk edits", gittest.WithFile("shared.txt", write(trunkEdit(parent))))
	trunkTip = f.RevParse("trunk")

	f.SwitchTo("parent")
	f.CreateBranch("child")
	child := append([]string{"the child's line"}, parent...)
	f.Commit("child writes the top", gittest.WithFile("shared.txt", write(child)))
	childTip = f.RevParse("child")

	f.SwitchTo("child")
	f.MustGit("merge", "--no-edit", "trunk")
	mergedTip = f.RevParse("child")

	repo, err := git.Open(f.Dir())
	if err != nil {
		t.Fatalf("git.Open: %v", err)
	}
	repo.Env = f.Env()
	return
}

// The patch half is the position-sensitive half: it must stay equal when the trunk's edit is nowhere near
// what was reviewed, and move when the trunk's edit lands inside the lines the reviewer read as context.
// The raw half moves in both, which is why the pair is recorded rather than one of them.
func TestDiffIdentityKeepsTheReviewedHunksApartFromTheRestOfTheFile(t *testing.T) {
	t.Run("a far away trunk line leaves the reviewed hunks alone", func(t *testing.T) {
		_, repo, parentTip, childTip, trunkTip, merged := groundFixture(t, func(lines []string) []string {
			return append(append([]string{}, lines...), "a trunk note far below the child's line")
		})
		rec, now := identityOf(t, repo, parentTip, childTip), identityOf(t, repo, trunkTip, merged)
		if rec.Patch == "" {
			t.Fatal("no patch half recorded")
		}
		if rec.Raw == now.Raw {
			t.Error("the raw half did not move: the fixture measured the same pair twice")
		}
		if rec.Patch != now.Patch {
			t.Errorf("a trunk edit 14 lines away moved the identity of the reviewed hunks: %s then %s", rec.Patch, now.Patch)
		}
		if identityOf(t, repo, parentTip, merged).Patch == rec.Patch {
			t.Error("measuring the merged head from the old base kept the value: the patch half is inert, not position-sensitive")
		}
	})

	t.Run("a trunk line inside the reviewed context moves it", func(t *testing.T) {
		_, repo, parentTip, childTip, trunkTip, merged := groundFixture(t, func(lines []string) []string {
			out := append([]string{}, lines...)
			out[2] = "line a — rewritten by trunk"
			return out
		})
		rec, now := identityOf(t, repo, parentTip, childTip), identityOf(t, repo, trunkTip, merged)
		if rec.Raw == now.Raw {
			t.Error("the raw half did not move: the fixture measured the same pair twice")
		}
		if rec.Patch == now.Patch {
			t.Errorf("a trunk edit inside the three lines of context the reviewer read kept the identity (%s): "+
				"the patch half cannot tell a reviewed hunk from its surroundings", rec.Patch)
		}
	})
}

// `--verbatim` rather than `--stable`, measured: `make` parses a recipe line and a top-level statement
// differently, and a value that scores them the same is not describing what a reviewer would have read.
func TestDiffIdentitySeesAWhitespaceSignificantLine(t *testing.T) {
	f, repo, base, tabbed := changedFixture(t, map[string]string{"Makefile": "build:\n\techo one\n\techo two\n"})
	f.Write("Makefile", "build:\n\techo one\n    echo two\n")
	f.Commit("the same words, indented with spaces")
	spaced := headOf(t, f)

	a, b := identityOf(t, repo, base, tabbed), identityOf(t, repo, base, spaced)
	if a.Raw == b.Raw {
		t.Fatal("the two files hashed the same raw: the fixture built one commit, not two")
	}
	if a.Patch == b.Patch {
		t.Errorf("a tab-indented recipe line and a space-indented statement scored the same patch (%s): "+
			"the patch half is not recording whitespace", a.Patch)
	}
}

// The same argument that keeps the raw half free of the reader's configuration applies to the patch half
// with more force: its bytes *are* a rendering, so every option that reaches it must be pinned by us.
func TestDiffIdentityIsNotMovedByDiffConfiguration(t *testing.T) {
	cases := []struct{ key, value string }{
		{"diff.context", "40"},
		{"diff.algorithm", "patience"},
		{"diff.noprefix", "true"},
		{"diff.ignoreAllSpace", "true"},
		{"core.whitespace", "cr-at-eol"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			f, repo, base, head := changedFixture(t, map[string]string{"a.txt": "one\nchanged\n"})
			want := identityOf(t, repo, base, head)
			f.Config(tc.key, tc.value)
			repo.ResetMemo()
			got := identityOf(t, repo, base, head)
			if got != want {
				t.Errorf("%s=%s moved the pair: raw %s→%s, patch %s→%s", tc.key, tc.value,
					want.Raw, got.Raw, want.Patch, got.Patch)
			}
		})
	}
}
