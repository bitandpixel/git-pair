package changeset_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/gittest"
)

// The chain is what a landed changeset's review record becomes: the run of commits on the destination's
// first-parent line that carries its directory. Each of these fixtures is one landing shape, because the
// shapes differ in how much of the record survives and in how exactly its endpoints can be named. The
// merge case is exact; the linear cases leave no landing commit, and the assertions below say which is
// which rather than pretending they are the same measurement.

func chainFor(t *testing.T, f *gittest.Fixture, trunk, id string) changeset.Chain {
	t.Helper()
	ch, err := changeset.LandedChain(context.Background(), repo(f), trunk, id)
	if err != nil {
		t.Fatalf("LandedChain(%s, %s): %v", trunk, id, err)
	}
	return ch
}

// A merge landing keeps the chain under its second parent. The range cannot widen: the branch's own
// commits are what sits there, and trunk's later work is on the first parent.
func TestLandedChainOfAMergeLanding(t *testing.T) {
	f := gittest.New(t)
	seed := f.Commit("seed", gittest.WithFile("a.txt", "a\n"))

	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	work := f.Commit("booking work", gittest.WithFile("booking.txt", "1\n"))
	review := f.Commit("review: approve booking", gittest.WithFile("booking.txt", "2\n"))

	f.SwitchTo("main")
	merge := f.MustGit("merge", "--quiet", "--no-ff", "-m", "land booking", "booking")
	_ = merge
	// Trunk moves on after the landing. A chain derivation that could not tell the second parent from
	// the first would extend into this commit.
	f.Commit("later trunk work", gittest.WithFile("later.txt", "1\n"))

	ch := chainFor(t, f, "main", "booking")
	if !ch.Merged || ch.Linear {
		t.Errorf("chain = %+v, want the merge shape", ch)
	}
	if ch.Base != seed {
		t.Errorf("Base = %s, want %s: the branch started at the seed", short(ch.Base), short(seed))
	}
	if ch.Head != review {
		t.Errorf("Head = %s, want %s: the branch tip, not the merge commit", short(ch.Head), short(review))
	}
	if ch.Landing == ch.Head || ch.Landing == "" {
		t.Errorf("Landing = %s, want the landing commit itself", short(ch.Landing))
	}
	// The arrival is a merge commit, so the record did not arrive in one commit of the destination's own
	// line — it arrived with a second parent holding the run.
	if ch.ArrivedInOneCommit || ch.Moved {
		t.Errorf("chain = %+v, want neither the one-commit arrival nor Moved", ch)
	}
	// The chain range holds the work and the review, and nothing of trunk's later commit.
	if !inRange(t, f, ch.Base, ch.Head, work) || !inRange(t, f, ch.Base, ch.Head, review) {
		t.Error("chain range is missing the implementation or the review commit")
	}
}

// A fast-forward landing leaves no landing commit. The run is on trunk's own line, so it starts at the
// commit that added the directory and runs to the newest commit that still carries it.
func TestLandedChainOfAFastForwardLanding(t *testing.T) {
	f := gittest.New(t)
	seed := f.Commit("seed", gittest.WithFile("a.txt", "a\n"))

	f.CreateBranch("ui")
	added := f.CommitChangeset("ui", "main")
	tip := f.Commit("ui work", gittest.WithFile("ui.txt", "1\n"))

	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--ff-only", "ui")

	ch := chainFor(t, f, "main", "ui")
	if ch.Merged || !ch.Linear {
		t.Errorf("chain = %+v, want the linear shape", ch)
	}
	if ch.Base != seed {
		t.Errorf("Base = %s, want %s", short(ch.Base), short(seed))
	}
	if ch.Head != tip {
		t.Errorf("Head = %s, want %s: the chain tip is trunk's tip here", short(ch.Head), short(tip))
	}
	if ch.Landing != added {
		t.Errorf("Landing = %s, want %s: the commit that added the directory", short(ch.Landing), short(added))
	}
	// The arrival shape and the run are different questions. This record arrived in one commit and never
	// moved, so `ArrivedInOneCommit` is true here exactly as it is for a squash — the difference between the
	// two is that this run continued past the landing and may have carried its markers, which is what the
	// marker walk reports and not the chain. A shape fact must not change when the destination moves, so
	// this assertion is the fix rather than an exception to it.
	if !ch.ArrivedInOneCommit {
		t.Errorf("chain = %+v, want the one-commit arrival", ch)
	}
	// And `Head` stays at the destination's tip, where the branch's own commits are. Pulling it back to
	// `Landing` is the tempting fix for the squash wording and it is wrong: the ready marker and the review
	// submissions come after the commit that added the directory, and most of them change no file at all, so
	// a range ending at the landing would report a reviewed fast-forward landing as a changeset nobody read.
	if ch.Head == ch.Landing {
		t.Error("Head = Landing: the run stops before the branch's own commits")
	}
	if !inRange(t, f, ch.Base, ch.Head, tip) {
		t.Error("chain range is missing the work commit")
	}
}

// A rebase merge is linear too, and the chain is the replayed commits — which is why the record survives
// a rebase merge intact: the commits are new, their messages and trailers are not.
func TestLandedChainOfARebaseLanding(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))

	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("booking work", gittest.WithFile("booking.txt", "1\n"))

	// Trunk moves, the branch rebases onto it, and the rebased branch then lands by fast-forward — the
	// shape a "rebase and merge" button produces.
	f.SwitchTo("main")
	trunkMove := f.Commit("trunk moves", gittest.WithFile("trunk.txt", "1\n"))
	f.SwitchTo("booking")
	f.MustGit("rebase", "--quiet", "main")
	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--ff-only", "booking")

	ch := chainFor(t, f, "main", "booking")
	if !ch.Linear || ch.Merged {
		t.Errorf("chain = %+v, want the linear shape", ch)
	}
	if ch.Base != trunkMove {
		t.Errorf("Base = %s, want %s: the rebased chain sits on trunk's later commit", short(ch.Base), short(trunkMove))
	}
	if inRange(t, f, ch.Base, ch.Head, trunkMove) {
		t.Error("chain range includes the commit it was rebased onto")
	}
}

// A squash collapses the implementation into one commit, so the run is one commit and it carries no
// markers. This is the limitation PRD §13 has to state: past reflog expiry the conversation is gone, and
// the chain read says so instead of inventing a range.
func TestLandedChainOfASquashLanding(t *testing.T) {
	f := gittest.New(t)
	seed := f.Commit("seed", gittest.WithFile("a.txt", "a\n"))

	f.CreateBranch("squashed")
	f.CommitChangeset("squashed", "main")
	f.Commit("squashed work one", gittest.WithFile("s.txt", "1\n"))
	f.Commit("squashed work two", gittest.WithFile("s.txt", "2\n"))

	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--squash", "squashed")
	squash := f.Commit("Merge squashed (squashed)", gittest.WithNoStage())

	ch := chainFor(t, f, "main", "squashed")
	if !ch.ArrivedInOneCommit {
		t.Errorf("chain = %+v, want the one-commit arrival: the directory came in the landing and nothing "+
			"else came with it", ch)
	}
	if ch.Merged || !ch.Linear {
		t.Errorf("chain = %+v, want the linear shape", ch)
	}
	if ch.Base != seed {
		t.Errorf("Base = %s, want %s", short(ch.Base), short(seed))
	}
	if ch.Head != squash {
		t.Errorf("Head = %s, want %s", short(ch.Head), short(squash))
	}
	// The branch's own commits are unreachable from trunk, which is the finding, not a detail: a squashed
	// landing leaves the chain with nothing in it but the landing.
	if inRange(t, f, ch.Base, ch.Head, f.MustGit("rev-parse", "squashed")) {
		t.Error("the squashed branch tip should not be reachable from the chain range")
	}
}

// A tidied changeset is landed work whose directory moved. The chain is the same run; `Moved` says which
// spelling the destination holds now, which is what `status` prints in the path.
func TestLandedChainOfATidiedChangeset(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("done")
	f.CommitChangeset("done", "main")
	review := f.Commit("review: approve done", gittest.WithFile("done.txt", "1\n"))

	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "land done", "done")
	f.Write(filepath.Join("changesets", ".landed", ".keep"), "the namespace, tracked\n")
	f.MustGit("mv", "changesets/done", filepath.Join("changesets", ".landed", "done"))
	f.Commit("tidy done")

	ch := chainFor(t, f, "main", "done")
	if !ch.Moved {
		t.Errorf("chain = %+v, want Moved: the destination holds changesets/.landed/done/", ch)
	}
	if !ch.Merged {
		t.Errorf("chain = %+v, want the merge shape to survive the move", ch)
	}
	if ch.Head != review {
		t.Errorf("Head = %s, want %s: tidying does not change where the chain ended", short(ch.Head), short(review))
	}
}

// The arrival's shape must not depend on where the destination's tip has since moved to. This is the defect:
// the old test was `Head == Landing`, which was true only while the landing was trunk's newest commit, so a
// squash landing's reading — and the sentence naming the shape — expired the first time an unrelated commit
// landed. The two assertions below are the same changeset read before and after trunk moves on.
func TestAOneCommitArrivalSurvivesTheDestinationMovingOn(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("squashed")
	f.CommitChangeset("squashed", "main")
	f.Commit("squashed work", gittest.WithFile("s.txt", "1\n"))
	f.SwitchTo("main")
	f.MustGit("merge", "--quiet", "--squash", "squashed")
	f.Commit("Merge squashed (squashed)", gittest.WithNoStage())

	before := chainFor(t, f, "main", "squashed")
	if !before.ArrivedInOneCommit {
		t.Fatalf("chain = %+v, want the one-commit arrival before trunk moves", before)
	}
	landing := before.Landing

	f.Commit("unrelated trunk work", gittest.WithFile("other.txt", "1\n"))
	f.Commit("and more of it", gittest.WithFile("third.txt", "1\n"))

	after := chainFor(t, f, "main", "squashed")
	if !after.ArrivedInOneCommit {
		t.Errorf("chain = %+v, want the arrival still reported after two trunk commits", after)
	}
	// What does move is `Head`, and it moves honestly: the endpoint of a linear run is the destination's
	// tip, which is why the surfaces decide the reading from the arrival plus the markers rather than from
	// the range.
	if after.Head == before.Head {
		t.Error("Head did not follow the destination, so the cache and the walk are not keyed on one commit")
	}
	if after.Landing != landing {
		t.Errorf("Landing = %s, want %s: the arrival is the same commit", short(after.Landing), short(landing))
	}
}

// Nothing landed, nothing to read. The distinction matters to the message `status --changeset` prints: a
// changeset whose branch is gone and whose destination never carried the directory had only its branch as
// a record, and the honest answer names that instead of printing an empty chain.
func TestLandedChainOfChangesetsThatAreNotOnTheDestination(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("work")
	f.CommitChangeset("work", "main")
	f.Commit("work", gittest.WithFile("work.txt", "1\n"))

	for _, id := range []string{"work", "never-existed"} {
		_, err := changeset.LandedChain(context.Background(), repo(f), "main", id)
		if !errors.Is(err, changeset.ErrNoChain) {
			t.Errorf("LandedChain(main, %s) = %v, want ErrNoChain", id, err)
		}
	}
	// A destination this clone cannot read is reported the same way, not as a git failure: the question is
	// answerable ("nothing landed here that I can see") for a branch that has not been fetched.
	if _, err := changeset.LandedChain(context.Background(), repo(f), "refs/heads/not-here", "work"); err == nil {
		t.Error("LandedChain on an unreadable destination succeeded")
	}
}

func inRange(t *testing.T, f *gittest.Fixture, base, head, sha string) bool {
	t.Helper()
	out, err := f.Git("rev-list", base+".."+head)
	if err != nil {
		t.Fatalf("rev-list %s..%s: %v", short(base), short(head), err)
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == sha {
			return true
		}
	}
	return false
}

// short keeps a failure message readable: the assertions compare full SHAs, and a diff of two 40-character
// strings tells a reader nothing.
func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// LandingCommit is the cheap form of one question `LandedChain` answers: which commit brought the
// directory here. The stack walk in `status` asks it per ancestor and nothing else, so it must cost two
// git calls rather than a chain derivation — and it must still agree with the derivation, which is what
// these three landing shapes pin. The tidied one is the case that makes the second call necessary: the
// newest change to the paths is the move, and naming it as the arrival would print a tidy as a landing.
func TestLandingCommitNamesTheArrivalInEveryShape(t *testing.T) {
	t.Run("a merge landing", func(t *testing.T) {
		f := gittest.New(t)
		f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
		f.CreateBranch("booking")
		f.CommitChangeset("booking", "main")
		f.Commit("booking work", gittest.WithFile("booking.txt", "1\n"))
		f.SwitchTo("main")
		f.MustGit("merge", "--quiet", "--no-ff", "-m", "land booking", "booking")
		f.Commit("later trunk work", gittest.WithFile("later.txt", "1\n"))

		want := chainFor(t, f, "main", "booking").Landing
		if got := changeset.LandingCommit(context.Background(), repo(f), "main", "booking"); got != want {
			t.Errorf("LandingCommit = %s, want the chain's landing %s", short(got), short(want))
		}
	})

	t.Run("a fast-forward landing", func(t *testing.T) {
		f := gittest.New(t)
		f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
		f.CreateBranch("booking")
		f.CommitChangeset("booking", "main")
		f.Commit("booking work", gittest.WithFile("booking.txt", "1\n"))
		f.SwitchTo("main")
		f.MustGit("merge", "--quiet", "--ff", "booking")

		want := chainFor(t, f, "main", "booking").Landing
		if got := changeset.LandingCommit(context.Background(), repo(f), "main", "booking"); got != want {
			t.Errorf("LandingCommit = %s, want the chain's landing %s", short(got), short(want))
		}
	})

	t.Run("a tidied changeset", func(t *testing.T) {
		f := gittest.New(t)
		f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
		f.CreateBranch("done")
		f.CommitChangeset("done", "main")
		f.Commit("done work", gittest.WithFile("done.txt", "1\n"))
		f.SwitchTo("main")
		f.MustGit("merge", "--quiet", "--no-ff", "-m", "land done", "done")
		f.Write(filepath.Join("changesets", ".landed", ".keep"), "the namespace, tracked\n")
		f.MustGit("mv", "changesets/done", filepath.Join("changesets", ".landed", "done"))
		f.Commit("tidy done")

		want := chainFor(t, f, "main", "done").Landing
		got := changeset.LandingCommit(context.Background(), repo(f), "main", "done")
		if got != want {
			t.Errorf("LandingCommit = %s, want the arrival %s, not the move", short(got), short(want))
		}
		if got == f.Head() {
			t.Error("LandingCommit named the tidying move as the landing")
		}
	})

	t.Run("a changeset the destination does not carry", func(t *testing.T) {
		f := gittest.New(t)
		f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
		f.CreateBranch("work")
		f.CommitChangeset("work", "main")
		f.Commit("work", gittest.WithFile("work.txt", "1\n"))

		for _, id := range []string{"work", "never-existed"} {
			if got := changeset.LandingCommit(context.Background(), repo(f), "main", id); got != "" {
				t.Errorf("LandingCommit(main, %s) = %s, want the empty answer", id, short(got))
			}
		}
	})
}
