package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
)

// The four verifications M3 puts in front of the write, in the order they fire. Each is a refusal
// rather than a warning (plan P1), because the pair of refs is the durable answer to "where did this
// reviewed work go" and a record written from a wrong claim is not a record anyone can trust afterwards.
//
// They are grouped in one table rather than four tests because the order is part of the contract: the
// shortest true refusal is the useful one, and a caller who got two things wrong should hear about the
// first in that order.

func TestIntegrationRecordRefusesWhatWasNeverReviewed(t *testing.T) {
	tests := []struct {
		name string
		// setup leaves the changeset branch at the head that will be recorded.
		setup func(t *testing.T, f *gittest.Fixture, slug string)
		code  int
		want  []string
	}{
		{
			// The directory alone is not a claim that anyone looked. Recording this head would put a
			// reviewed-then-landed shape on work that has never been offered.
			name:  "no markers at all",
			setup: func(t *testing.T, f *gittest.Fixture, slug string) {},
			code:  1,
			want:  []string{"never been offered for review", "git pair review submit --approve"},
		},
		{
			// `check` refuses this head for the same reason (§11.3): an offer is not a verdict. The two
			// commands a person runs one after the other must not disagree about it.
			name:  "offered, never reviewed",
			setup: func(t *testing.T, f *gittest.Fixture, slug string) { ready(t, f) },
			code:  1,
			want:  []string{"`Review-State: ready`", "an offer rather than a verdict"},
		},
		{
			// The reviewer corrected themselves by submitting again (§10.6), and the newest submission is
			// what the state is. An approval two submissions back does not license the landing.
			name: "approved, then blocked",
			setup: func(t *testing.T, f *gittest.Fixture, slug string) {
				ready(t, f)
				submit(t, f, "approve")
				submit(t, f, "block")
			},
			code: 1,
			want: []string{"blocks it", "Address the review"},
		},
		{
			// Feedback permits integration under `check --allow-feedback` and is not an approval without
			// it. The recorder carries the same switch, so a pair that lands on feedback records on
			// feedback — and says so only when it was told that is how this pair works.
			name: "approved, then feedback",
			setup: func(t *testing.T, f *gittest.Fixture, slug string) {
				ready(t, f)
				submit(t, f, "approve")
				submit(t, f, "feedback")
			},
			code: 1,
			want: []string{"is feedback", "not an approval", "--allow-feedback"},
		},
		{
			// §9.7: an ending is not a landing. The wording is the one every command uses for an ended
			// changeset, so a reader who has seen one of them has seen this one.
			name: "abandoned",
			setup: func(t *testing.T, f *gittest.Fixture, slug string) {
				ready(t, f)
				submit(t, f, "approve")
				runIn(t, f.Dir(), "change", "abandon").mustSucceed(t, "change", "abandon")
			},
			code: 1,
			want: []string{"was abandoned by", "nothing to integrate"},
		},
		{
			// The mistake this check exists for: the directory says one changeset and the review history
			// says another — a directory copied out of a stacked parent, or an id renamed after the review.
			// Naming the disagreement is the whole refusal. "never been offered for review" would send the
			// reader to review work that has been reviewed, under a name that is not its own.
			name: "the markers name a different changeset",
			setup: func(t *testing.T, f *gittest.Fixture, slug string) {
				t.Helper()
				f.SwitchTo("booking")
				f.CommitReviewMarker("other", "approve")
			},
			code: 1,
			want: []string{"no marker in", "names changeset booking", "the markers there name other"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// A fixture with no review at all: each case adds the verdicts it wants, so "no markers"
			// needs no deletion to be true.
			f, slug := newChangeset(t, "booking", "main")
			f.CreateBranch("release/2.x", "main")
			f.MustGit("checkout", "booking", "--", changeset.Root+"/"+slug)
			f.SwitchTo("release/2.x")
			landing := f.Commit("booking: land the reviewed work", gittest.WithFile("landed.md", "landed\n"))
			f.SwitchTo("booking")
			tc.setup(t, f, slug)
			source := f.Head()

			res := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing,
				"--target", "release/2.x")
			if res.code != tc.code {
				t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", res.code, tc.code, res.stdout, res.stderr)
			}
			for _, want := range tc.want {
				mustContain(t, res.stderr, want, "the refusal must say what to do about it")
			}
			if got := durableRefs(t, f); len(got) != 0 {
				t.Errorf("a refused record wrote %v", got)
			}
		})
	}
}

// Feedback is accepted when the caller says that is how this pair works — the recorder's copy of
// `git pair check --allow-feedback`. It is a flag rather than a default for the same reason check has it:
// a landing that records a feedback-only changeset is a decision someone made, and it should appear in
// the command line that made it.
func TestIntegrationRecordAcceptsFeedbackOnlyWhenToldTo(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "feedback")
	source := f.Head()
	f.CreateBranch("release/2.x", "main")
	f.MustGit("checkout", source, "--", changeset.Root+"/"+slug)
	f.SwitchTo("release/2.x")
	landing := f.Commit("booking: land it", gittest.WithFile("landed.md", "landed\n"))

	res := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing,
		"--target", "release/2.x", "--allow-feedback", "--json")
	res.mustSucceed(t, "integration", "record")
	if got := f.RefSHA(integrationRef(slug)); got != landing {
		t.Errorf("the record is at %s, want %s", got, landing)
	}
	if out := res.json(t); out["recorded"] != true {
		t.Errorf("recorded = %v, want true", out["recorded"])
	}
}

// The fourth check is a transition rather than a presence: the landing commit has to be the commit that
// brought `changesets/<id>/` into the destination, not a later commit that merely carries it. A follow-up
// on the destination branch satisfies "does the tree contain the directory" and fails this, which is the
// difference between a record and a pointer at whoever last touched the branch.
func TestIntegrationRecordRefusesACommitThatDidNotAddTheChangeset(t *testing.T) {
	f, slug, source, landing := recordFixture(t)
	// A follow-up on the destination branch. It carries the directory — the landing put it there — so
	// only the first-parent comparison tells the two commits apart.
	f.SwitchTo("release/2.x")
	f.Commit("follow-up on the destination branch", gittest.WithFile("notes.md", "after the fact\n"))
	followUp := f.Head()

	res := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", followUp,
		"--target", "release/2.x")
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1\n%s%s", res.code, res.stdout, res.stderr)
	}
	mustContain(t, res.stderr, "does not add changesets/"+slug+"/ over its first parent", "the refusal names the transition it wanted")
	mustContain(t, res.stderr, shortOf(landing), "and the commit that made it, which it can see")
	mustContain(t, res.stderr, "git log --first-parent", "and the command that lists the candidates")
	if f.HasRef(integrationRef(slug)) {
		t.Error("a record was written for a commit that did not bring the changeset in")
	}
}

// The destination is derived when the caller does not name one, and which branch that was is reported
// rather than left for the reader to reconstruct. Two shapes are worth pinning: a landing on trunk (the
// changeset's `base:` is trunk, so one candidate suffices) and a landing on another branch entirely,
// where the derivation is a refusal and the flag is the way out.
func TestIntegrationRecordDerivesTheDestination(t *testing.T) {
	f, slug, source, _ := recordFixture(t)
	f.SwitchTo("main")
	f.MustGit("checkout", source, "--", changeset.Root+"/"+slug)
	f.Commit("booking: squash-merge the reviewed work")
	landing := f.Head()

	res := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing, "--json")
	res.mustSucceed(t, "integration", "record")
	out := res.json(t)
	if !strings.HasSuffix(str(t, out, "target"), "main") {
		t.Errorf("target = %v, want the branch the landing is on", out["target"])
	}
	if out["target_derived"] != true {
		t.Errorf("target_derived = %v, want true: git-pair chose that branch, the caller did not name it", out["target_derived"])
	}
	if got := f.RefSHA(integrationRef(slug)); got != landing {
		t.Errorf("the record is at %s, want %s", got, landing)
	}
	// A second pair of the same shape, recorded through the text surface: the line that names the branch
	// has to say git-pair chose it, because a log line reading "verified reachable from main" is a claim
	// about a check someone did not ask for. The retry above prints no such line, and prints none on
	// purpose — the no-op path makes no checks, so it can claim none.
	f3, _, source3, _ := recordFixture(t)
	f3.SwitchTo("main")
	f3.MustGit("checkout", source3, "--", changeset.Root+"/booking")
	f3.Commit("booking: squash-merge the reviewed work")
	human := runIn(t, f3.Dir(), "integration", "record", "--source", source3, "--commit", f3.Head())
	human.mustSucceed(t, "integration", "record")
	mustContain(t, human.stdout, "verified reachable from main (the changeset's base branch)",
		"the text surface says which branch, and that it was derived")

	// The release-branch landing, with no --target: nothing git-pair can name contains the commit, so
	// this is the refusal — and the refusal is where the flag is explained.
	f2, _, source2, landing2 := recordFixture(t)
	refused := runIn(t, f2.Dir(), "integration", "record", "--source", source2, "--commit", landing2)
	if refused.code != 1 {
		t.Fatalf("a landing in no derivable destination exited %d, want 1\n%s%s", refused.code, refused.stdout, refused.stderr)
	}
	mustContain(t, refused.stderr, "is not reachable from main", "it names what it tried")
	mustContain(t, refused.stderr, "the changeset's own `base:` (main)", "and where the guess came from")
	mustContain(t, refused.stderr, "--target <ref>", "and the flag that settles it")
	mustContain(t, refused.stderr, "release branch is a destination too",
		"and says the deviation is allowed, only not silent")
	if got := durableRefs(t, f2); len(got) != 0 {
		t.Errorf("the refused record wrote %v", got)
	}
}

// The stacked child's destination is its parent's branch by `base:`, and its landing is trunk. The check
// that refuses a release-branch record for not naming itself must not refuse this one, because a stack
// merged into trunk in one go is the ordinary way a child lands. So the derived check tries the base
// first and the default branch after, and takes the one that holds the commit.
func TestIntegrationRecordAcceptsAChildLandedOnTrunk(t *testing.T) {
	f, parent := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	submit(t, f, "approve")

	child := "booking-transaction-tests"
	f.CreateBranch(child, "booking-transaction")
	runIn(t, f.Dir(), "change", "init", "--base", "booking-transaction").mustSucceed(t, "change", "init")
	f.Commit("add the concurrent final-seat test", gittest.WithFile("service_test.go",
		"package main\n\nfunc TestFinalSeat() {}\n"))
	ready(t, f)
	submit(t, f, "approve")
	source := f.Head()

	// The stack lands on trunk in one go, which is where the child's directory ends up. The child
	// carries its parent's directory too, so the caller names which one this record is for.
	f.SwitchTo("main")
	f.MustGit("checkout", source, "--", changeset.Root+"/"+parent, changeset.Root+"/"+child)
	landing := f.Commit("land the stack", gittest.WithFile("landed.md", "landed\n"))

	res := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing,
		"--changeset", child)
	res.mustSucceed(t, "integration", "record")
	mustContain(t, res.stdout, "verified reachable from main (the default branch)",
		"the base branch did not hold it, and the check said which one did")
	if sha := f.RefSHA(integrationRef(child)); sha != landing {
		t.Errorf("the child's record is at %s, want %s", sha, landing)
	}
	for _, ref := range []string{archiveRef(parent), integrationRef(parent)} {
		if f.HasRef(ref) {
			t.Errorf("%s exists: recording the child does not record the parent", ref)
		}
	}
}

// The record that already exists is the answer to a retry, and it is answered before the four checks: a
// CI re-run arrives with whatever flags the second job happened to have, and re-verifying a fact already
// on the record against different assumptions can only refuse about the assumptions. A *different* pair
// is still refused, and refused with what is already recorded rather than with an incidental complaint
// from the landing checks — which a second landing into a branch that already carries the directory would
// also fail.
func TestIntegrationRecordAnswersFromTheRecordBeforeTheChecks(t *testing.T) {
	f, slug, source, landing := recordFixture(t)
	runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing,
		"--target", "release/2.x").mustSucceed(t, "integration", "record")

	// The same pair, no --target this time: the derivation could not find the landing, and it does not
	// get the chance to try.
	again := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing)
	again.mustSucceed(t, "integration", "record")
	mustContain(t, again.stdout, "already recorded", "the retry is a success that changed nothing")
	if got := f.RefSHA(reviewref.Archive(slug)); got != source {
		t.Errorf("the retry moved the archive to %s", got)
	}

	// A second landing of the same changeset into the same branch: it would fail the transition check
	// too, and the reader deserves the refusal about the record.
	f.SwitchTo("release/2.x")
	f.Commit("backport the same work", gittest.WithFile("backport.md", "again\n"))
	second := runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", f.Head(),
		"--target", "release/2.x")
	if second.code != 1 {
		t.Fatalf("a second landing exited %d, want 1\n%s%s", second.code, second.stdout, second.stderr)
	}
	mustContain(t, second.stderr, integrationRef(slug)+" records", "the refusal is about the record that exists")
	mustContain(t, second.stderr, shortOf(landing), "naming what is already on it")
	mustNotContain(t, second.stderr, "does not add changesets/", "and not about the tree it never got to")
}
