package git_test

import (
	"context"
	"strings"
	"testing"

	"gitpair/internal/git"
	"gitpair/internal/gittest"
)

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
