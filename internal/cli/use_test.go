package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// A sibling merge puts another changeset's directory in this branch's tree, and nothing in the
// durable data orders the two, so every command refuses. `change use` is the answer: one record
// in the chosen changeset's file, which is what the rule then reads.
func TestChangeUseRecordsTheChoiceAndResolvesTheBranch(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("theirs")
	f.CommitChangeset("theirs", "main")
	f.Commit("their work", gittest.WithFile("their.go", "package main\n"))

	f.SwitchTo("main")
	f.CreateBranch("mine")
	f.CommitChangeset("mine", "main")
	f.Commit("my work", gittest.WithFile("mine.go", "package main\n"))
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "take their branch too", "theirs")

	if strings.Contains(f.ChangesetFile("mine", "CHANGESET.yaml"), "ignores") {
		t.Fatal("fixture starts with a record")
	}

	res := runIn(t, f.Dir(), "change", "use", "mine").mustSucceed(t, "change", "use")
	mustContain(t, res.stdout, "recorded mine as this branch's changeset", "the command must say what it decided")

	if got := f.ChangesetFile("mine", "CHANGESET.yaml"); !strings.Contains(got, "ignores: theirs") {
		t.Errorf("mine's CHANGESET.yaml = %q, want it to ignore theirs", got)
	}
	if got := f.ChangesetFile("theirs", "CHANGESET.yaml"); strings.Contains(got, "ignores") {
		t.Errorf("theirs' CHANGESET.yaml = %q, want it untouched: the record belongs to the changeset that was chosen", got)
	}

	if got := f.Subject("HEAD"); got != "git-pair: work on changeset mine" {
		t.Errorf("HEAD subject = %q, want the record commit", got)
	}
	if files := f.ChangedFiles("HEAD~1", "HEAD"); len(files) != 1 || files[0] != "changesets/mine/CHANGESET.yaml" {
		t.Errorf("the record commit touched %v, want only this changeset's metadata", files)
	}

	status := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)
	if status["changeset"] != "mine" {
		t.Errorf("status changeset = %v, want mine", status["changeset"])
	}
}

// The record is a decision, so an id the branch does not carry is refused rather than recorded —
// a typo would otherwise silence every command on the branch by making it look decided.
func TestChangeUseRefusesANameTheBranchDoesNotCarry(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("theirs")
	f.CommitChangeset("theirs", "main")
	f.SwitchTo("main")
	f.CreateBranch("mine")
	f.CommitChangeset("mine", "main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "merge their branch", "theirs")

	res := runIn(t, f.Dir(), "change", "use", "no-such-changeset")
	if res.code != exitUsage {
		t.Fatalf("`change use` with an id this branch does not carry exited %d, want %d\nstderr: %s", res.code, exitUsage, res.stderr)
	}
	mustContain(t, res.stderr, "no-such-changeset", "the refusal must name what was asked for")
	mustContain(t, res.stderr, "mine", "the refusal must list what the branch actually carries")
	mustContain(t, res.stderr, "theirs", "the refusal must list both candidates")
	if strings.Contains(f.ChangesetFile("mine", "CHANGESET.yaml"), "ignores") {
		t.Error("a refused change use still wrote a record")
	}
}

// Running it twice records one decision, not two commits.
func TestChangeUseIsIdempotent(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("theirs")
	f.CommitChangeset("theirs", "main")
	f.SwitchTo("main")
	f.CreateBranch("mine")
	f.CommitChangeset("mine", "main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "merge their branch", "theirs")

	runIn(t, f.Dir(), "change", "use", "mine").mustSucceed(t, "change", "use")
	head := f.Head()

	second := runIn(t, f.Dir(), "change", "use", "mine").mustSucceed(t, "change", "use")
	mustContain(t, second.stdout, "already", "the second run must say it changed nothing")
	if f.Head() != head {
		t.Errorf("the second `change use` committed again: %s -> %s", head[:7], f.Head()[:7])
	}
}

// A branch with one changeset is not a choice to record; saying so is more useful than writing a
// record that changes nothing.
func TestChangeUseOnAnUnambiguousBranchSaysSo(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	runIn(t, f.Dir(), "init", "--base", "main").mustSucceed(t, "init")
	head := f.Head()

	res := runIn(t, f.Dir(), "change", "use", "booking").mustSucceed(t, "change", "use")
	mustContain(t, res.stdout, "already works on booking", "the command must report the decision it did not need to make")
	if f.Head() != head {
		t.Error("an unchanged decision was committed")
	}
	if strings.Contains(f.ChangesetFile("booking", "CHANGESET.yaml"), "ignores") {
		t.Error("an unambiguous branch was given a record it does not need")
	}
}

// Two records that contradict each other cannot both be honoured, and the rule refuses instead of
// picking the newer one — so `change use` must refuse too, rather than commit a third file that
// leaves the branch as undecided as it was.
func TestChangeUseRefusesAContradictoryRecord(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("theirs")
	f.CommitChangeset("theirs", "main")
	f.SwitchTo("main")
	f.CreateBranch("mine")
	f.CommitChangeset("mine", "main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "merge their branch", "theirs")

	// Someone already chose theirs, by hand or by an earlier `change use`.
	f.WriteChangesetFile("theirs", "CHANGESET.yaml", "id: theirs\nbase: main\nignores: mine\n")
	f.Commit("choose theirs")

	res := runIn(t, f.Dir(), "change", "use", "mine")
	if res.code != exitUsage {
		t.Fatalf("`change use` over a contradicting record exited %d, want %d\nstderr: %s", res.code, exitUsage, res.stderr)
	}
	mustContain(t, res.stderr, "already claims it", "the refusal must say the other record has the branch")
	mustContain(t, res.stderr, "changesets/theirs/CHANGESET.yaml", "the refusal must name the file holding the record")
	mustContain(t, res.stderr, "Remove that `ignores:` line", "the refusal must name the edit that would settle it")
	if strings.Contains(f.ChangesetFile("mine", "CHANGESET.yaml"), "ignores") {
		t.Error("a refused change use still wrote a record")
	}
}

// Two records that name each other cancel out: the rule keeps both candidates rather than
// emptying the set, so the branch is still ambiguous and each id is still offered. Writing a
// third record over the top changes nothing, so the command asks first and refuses.
func TestChangeUseRefusesWhenTheRecordWouldNotSettleIt(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("theirs")
	f.CommitChangeset("theirs", "main")
	f.SwitchTo("main")
	f.CreateBranch("mine")
	f.CommitChangeset("mine", "main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "merge their branch", "theirs")

	f.WriteChangesetFile("mine", "CHANGESET.yaml", "id: mine\nbase: main\nignores: theirs\n")
	f.WriteChangesetFile("theirs", "CHANGESET.yaml", "id: theirs\nbase: main\nignores: mine\n")
	f.Commit("both records name the other")
	head := f.Head()

	res := runIn(t, f.Dir(), "change", "use", "mine")
	if res.code != exitUsage {
		t.Fatalf("`change use` over two records that cancel out exited %d, want %d\nstderr: %s",
			res.code, exitUsage, res.stderr)
	}
	mustContain(t, res.stderr, "would still leave it undecided", "the refusal must say the record settles nothing")
	mustContain(t, res.stderr, "Remove its `ignores:` line", "the refusal must name the edit that would settle it")
	if f.Head() != head {
		t.Errorf("a record that decides nothing was committed: %s -> %s", head[:7], f.Head()[:7])
	}
}

// The refusal that sends an author here must name the way out, not just the dead end.
func TestAmbiguityPointsAtChangeUse(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("theirs")
	f.CommitChangeset("theirs", "main")
	f.SwitchTo("main")
	f.CreateBranch("mine")
	f.CommitChangeset("mine", "main")
	f.MustGit("merge", "--quiet", "--no-ff", "-m", "merge their branch", "theirs")

	res := runIn(t, f.Dir(), "status")
	if res.code != exitUsage {
		t.Fatalf("status on an ambiguous branch exited %d, want %d\nstderr: %s", res.code, exitUsage, res.stderr)
	}
	mustContain(t, res.stderr, "mine and theirs", "the refusal must name both candidates")
	mustContain(t, res.stderr, "--changeset", "the refusal must name the one-command escape")
	mustContain(t, res.stderr, "git pair change use", "the refusal must name the way to settle it for the branch")
}
