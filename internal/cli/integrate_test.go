package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
)

// `git pair change integrate` is the author's half of an automatic merge: one empty commit that says the
// work is approved and should be landed, and nothing else. It writes no ref, pushes nothing, and merges
// nothing (PRD §26) — it moves the waiting from the author to whoever owns the destination branch.
//
// What these tests pin is the surface: the marker is the only thing written, the gate that clears it is
// `git pair check`'s gate rather than an imitation of it, the refusal for each condition is exit 1 with
// that condition named, and re-running at the head already declared is a success that recorded nothing.
// The gate's conditions themselves belong to the check tests; what belongs here is that a declaration
// cannot be made for work the gate would refuse.

// The declaration is a commit and the commit is the whole record of it: no ref, no config, no network. The
// assertion that nothing durable was written is the one that matters most, because a command named
// "integrate" that wrote an integration ref would end the rule this repository is built on.
func TestChangeIntegrateDeclaresAnApprovedChangeset(t *testing.T) {
	f, slug, _, _ := approvedChangeset(t)
	head := f.Head()

	if got := durableRefs(t, f); len(got) != 0 {
		t.Fatalf("durable refs for in-flight work: %v", got)
	}
	res := runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")
	mustContain(t, res.stdout, "Integrating: "+slug, "the answer names the changeset it declared")
	mustContain(t, res.stdout, "declared by: "+f.Short(f.Head()), "and the commit that says so")
	mustContain(t, res.stdout, "merge into:  main",
		"and where the work is asking to go, because a declaration a reader cannot aim is a wish")
	mustContain(t, res.stdout, "git push origin booking-transaction",
		"the next step is the author's own push, which git-pair never runs for them")

	declared := f.Head()
	if declared == head {
		t.Fatal("no commit was written")
	}
	if n := f.ParentCount(declared); n != 1 {
		t.Errorf("the declaration has %d parents, want 1: it is a commit on top of the work, not a merge", n)
	}
	if got := f.ChangedFiles(head, declared); len(got) != 0 {
		t.Errorf("the declaration changed %v: it declares the head it stands on and touches nothing", got)
	}
	if got := f.Subject(declared); got != "git-pair: integrate "+slug {
		t.Errorf("subject = %q", got)
	}
	trailers := f.Trailers(declared)
	if trailers["Review-State"] != "integrating" {
		t.Errorf("Review-State = %q, want integrating", trailers["Review-State"])
	}
	if trailers["Review-Changeset"] != slug {
		t.Errorf("Review-Changeset = %q, want %s", trailers["Review-Changeset"], slug)
	}
	if trailers["Review-Head"] != head {
		t.Errorf("Review-Head = %q, want %s: the declaration is about a commit, and the stale name is what "+
			"makes the gate refuse a rewrite of it", trailers["Review-Head"], head)
	}
	if got := durableRefs(t, f); len(got) != 0 {
		t.Errorf("a declaration wrote durable refs: %v", got)
	}
}

// The machine answer, with the two fields a pipeline cannot re-derive cheaply: the commit that carries the
// declaration, and the destination it is asking for. `reasons` is an array in the success too, so a caller
// that reads a refusal and a caller that reads a success handle one shape.
func TestChangeIntegrateReportsTheDeclarationAsJSON(t *testing.T) {
	f, slug, _, _ := approvedChangeset(t)
	head := f.Head()

	res := runIn(t, f.Dir(), "change", "integrate", "--json").mustSucceed(t, "change", "integrate")
	j := res.json(t)
	if j["changeset"] != slug || j["branch"] != slug {
		t.Errorf("changeset/branch = %v/%v, want %s", j["changeset"], j["branch"], slug)
	}
	if j["base"] != "main" {
		t.Errorf("base = %v, want main", j["base"])
	}
	if j["was"] != "APPROVED" {
		t.Errorf("was = %v, want APPROVED: what the branch said before this run", j["was"])
	}
	if j["state"] != "INTEGRATING" {
		t.Errorf("state = %v, want INTEGRATING", j["state"])
	}
	if j["head"] != head {
		t.Errorf("head = %v, want %s", j["head"], head)
	}
	if j["recorded"] != true {
		t.Errorf("recorded = %v, want true: this run wrote the declaration", j["recorded"])
	}
	if j["integrate_commit"] != f.Head() {
		t.Errorf("integrate_commit = %v, want the commit it wrote (%s)", j["integrate_commit"], f.Head())
	}
	if j["destination"] != "main" {
		t.Errorf("destination = %v, want main", j["destination"])
	}
	if j["destination_source"] != "base" {
		t.Errorf("destination_source = %v, want base", j["destination_source"])
	}
	if got := res.jsonList(t, "reasons"); len(got) != 0 {
		t.Errorf("reasons = %v on a declaration that succeeded", got)
	}
	next, _ := j["next_action"].(string)
	if !strings.Contains(next, "git push origin "+slug) {
		t.Errorf("next_action = %q, want the push that makes the declaration visible to whoever merges", next)
	}
}

// Work that was never approved cannot be declared, and the refusal is the gate's answer rather than a new
// one: the command's whole promise is that a declaration means `check` passed.
func TestChangeIntegrateRefusesWorkThatWasNeverApproved(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	before := f.Head()

	res := runIn(t, f.Dir(), "change", "integrate")
	if res.code != exitRefusal {
		t.Fatalf("integrate exited %d, want %d\nstdout: %s", res.code, exitRefusal, res.stdout)
	}
	mustContain(t, res.stdout, "Cannot declare "+slug,
		"a refusal opens with the verdict, the way check's does")
	mustContain(t, res.stdout, "- ", "and names the condition as a bullet")
	mustNotContain(t, res.stdout+res.stderr, "git-pair:",
		"the verdict is the answer, not a tool error laid over it")
	if f.Head() != before {
		t.Error("a refused declaration still wrote a commit")
	}

	j := runIn(t, f.Dir(), "change", "integrate", "--json").json(t)
	if j["recorded"] != false {
		t.Errorf("recorded = %v on a refusal", j["recorded"])
	}
	if got := len(j["reasons"].([]any)); got == 0 {
		t.Error("a refusal with no reasons in --json is a refusal nobody can act on")
	}
}

// The gate is the same code, so the drift condition refuses a declaration exactly as it refuses a merge.
// The declaration must not become a way to wave content past an approval that never saw it.
func TestChangeIntegrateRefusesContentThatChangedSinceTheApproval(t *testing.T) {
	f, slug, _, _ := approvedChangeset(t)
	f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { transaction() }\n"))
	before := f.Head()

	res := runIn(t, f.Dir(), "change", "integrate")
	if res.code != exitRefusal {
		t.Fatalf("integrate exited %d, want %d\nstdout: %s", res.code, exitRefusal, res.stdout)
	}
	mustContain(t, res.stdout, "content outside changesets/"+slug+"/ changed since",
		"the drift reason arrives in the command's own words")
	mustContain(t, res.stdout, "service.go", "and names the file that moved")
	if f.Head() != before {
		t.Error("a refused declaration still wrote a commit")
	}
}

// A rebase rewrites every SHA and no file, which is the case the lineage rule exists for — and the case
// where a declaration left behind by the pre-rebase branch would otherwise bless history no reviewer read.
// The marker message survives a rewrite; `Review-Head` is what notices.
func TestChangeIntegrateRefusesHistoryTheApprovalDidNotReview(t *testing.T) {
	f, _, _, _ := approvedChangeset(t)
	f.CreateBranch("trunk-advanced", "main")
	f.Commit("nothing to see", gittest.WithEmpty())
	f.SwitchTo("booking-transaction")
	f.MustGit("rebase", "trunk-advanced")
	before := f.Head()

	res := runIn(t, f.Dir(), "change", "integrate")
	if res.code != exitRefusal {
		t.Fatalf("integrate exited %d, want %d\nstdout: %s", res.code, exitRefusal, res.stdout)
	}
	mustContain(t, res.stdout, "no longer in this history",
		"the rewritten approval has to be the reason, with no file changed to blame")
	if f.Head() != before {
		t.Error("a refused declaration still wrote a commit")
	}
}

// The policy switch is one flag with the same meaning it has on `check` and on `integration record`, and it
// is not a per-command invention: a declaration made under `--allow-feedback` is the same permission that
// flag already grants at the gate, and nothing more.
func TestChangeIntegrateFollowsTheFeedbackPolicy(t *testing.T) {
	f, _ := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	submit(t, f, "feedback")

	res := runIn(t, f.Dir(), "change", "integrate")
	if res.code != exitRefusal {
		t.Fatalf("integrate on feedback exited %d, want %d\nstdout: %s", res.code, exitRefusal, res.stdout)
	}
	mustContain(t, res.stdout, "the newest review is feedback", "the policy reason, in the gate's words")

	runIn(t, f.Dir(), "change", "integrate", "--allow-feedback").mustSucceed(t, "change", "integrate")
	if got := f.Trailers(f.Head())["Review-State"]; got != "integrating" {
		t.Errorf("the newest commit's Review-State = %q, want integrating", got)
	}
}

// The declaration is a commit, so a dirty tree is the same blocker `change ready` treats it as: repository
// state, not bad arguments — exit 1, and nothing written.
func TestChangeIntegrateRefusesADirtyTree(t *testing.T) {
	f, _, _, _ := approvedChangeset(t)
	f.Write("service.go", "package main\n\nfunc Lock() { unlocked() }\n")
	before := f.Head()

	res := runIn(t, f.Dir(), "change", "integrate")
	if res.code != exitRefusal {
		t.Fatalf("integrate on a dirty tree exited %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stdout+res.stderr, "clean", "the answer says what to do about it")
	if f.Head() != before {
		t.Error("a refused declaration committed the uncommitted work")
	}
}

// A changeset with an integration record has landed, and the branch that carried it holds no work in
// progress: the session load says so before any command-specific gate runs, in the words `status` uses on
// the same branch. A declaration on top of a record would ask for a second merge of work that is already in
// the destination, so this is the refusal the command never has to reach.
func TestChangeIntegrateRefusesAChangesetThatHasLanded(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	landAndRecord(t, f, "booking-transaction", "main")
	// The recorder leaves the reader on the destination branch, and the declaration is asked of the
	// changeset's own branch — the one that still carries the approval the landing did not need.
	f.SwitchTo(slug)
	before := f.Head()

	res := runIn(t, f.Dir(), "change", "integrate")
	if res.code != exitUsage {
		t.Fatalf("integrate on a landed changeset exited %d, want %d\nstdout: %s\nstderr: %s",
			res.code, exitUsage, res.stdout, res.stderr)
	}
	mustContain(t, res.stdout+res.stderr, "that changeset landed", "the answer says the work is already in")
	if f.Head() != before {
		t.Error("a refused declaration still wrote a commit")
	}
}

// Re-running at the head already declared records nothing and succeeds, which is what lets a script or a
// job re-run the declaration unconditionally. It is not a silent no-op: the answer says it found one, and
// `recorded` is false so a log can tell the two successes apart.
func TestChangeIntegrateIsIdempotentAtTheHeadItDeclared(t *testing.T) {
	f, slug, _, _ := approvedChangeset(t)
	runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")
	declared := f.Head()

	res := runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")
	mustContain(t, res.stdout, slug+" is already declared ready to integrate at "+f.Short(declared),
		"the answer says it found a declaration rather than made one")
	mustNotContain(t, res.stdout, "Integrating: "+slug,
		"and does not read as a second declaration")
	if f.Head() != declared {
		t.Errorf("a second run wrote a commit: %s, want %s", f.Head(), declared)
	}

	again := runIn(t, f.Dir(), "change", "integrate", "--json").json(t)
	if again["recorded"] != false {
		t.Errorf("recorded = %v on the second run, want false", again["recorded"])
	}
	if again["integrate_commit"] != declared {
		t.Errorf("integrate_commit = %v, want the declaration that was already there (%s)",
			again["integrate_commit"], declared)
	}
}

// A declaration is about a commit, so a commit after it is a head that has not been offered for the merge —
// even one inside `changesets/<id>/`, which no gate counts as drift. The author declares the head they are
// actually handing over, and the chain keeps each head that was offered.
func TestChangeIntegrateDeclaresTheNewHeadAfterACommit(t *testing.T) {
	f, slug, _, _ := approvedChangeset(t)
	runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")
	first := f.Head()

	f.Commit("note the answer", gittest.WithFile("changesets/"+slug+"/ABOUT.md",
		"# "+slug+"\n\nConcurrency answered.\n"))
	after := f.Head()

	res := runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")
	mustContain(t, res.stdout, "Integrating: "+slug, "the new head is declared")
	mustContain(t, res.stdout, "head:        "+f.Short(after), "and the answer names the head it declared")
	if f.Head() == first {
		t.Fatal("the second declaration did not land on the new head")
	}
	if got := f.Trailers(f.Head())["Review-Head"]; got != after {
		t.Errorf("Review-Head = %q, want %s", got, after)
	}
	// Both declarations are in the history: the branch shows every head that was offered for the merge.
	if !f.ReachableFrom(first, f.Head()) {
		t.Errorf("the first declaration %s is gone from the branch", first)
	}
}

// The stacked rule, which belongs to the request and not to the gate: a child is not declared until its
// parent has landed, because the automatic merge would land the child on a branch review can still
// rewrite — and a durable record naming a commit on such a branch can end up naming history that stopped
// existing. Merging a child onto its parent by hand stays open; git-pair declines to queue it, not to
// record it.
func TestChangeIntegrateRefusesAChildWhoseParentHasNotLanded(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	// The child is cut from the parent before either of them has been offered: what matters here is that
	// the parent has no record at the moment the child asks.
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	ready(t, f)
	submit(t, f, "approve")
	before := f.Head()

	res := runIn(t, f.Dir(), "change", "integrate")
	if res.code != exitRefusal {
		t.Fatalf("integrate on an unlanded parent exited %d, want %d\nstdout: %s", res.code, exitRefusal, res.stdout)
	}
	mustContain(t, res.stdout, "the parent alpha has no integration record",
		"the refusal names the parent and the missing fact about it")
	mustContain(t, res.stdout, "git pair integration record",
		"and the step that would have made the parent land")
	if f.Head() != before {
		t.Error("a refused declaration still wrote a commit")
	}

	// The parent lands and is recorded, and the child comes back onto the landing — the step `status`
	// names for a stale parent — and is reviewed there. Now the request is one an unattended merge can
	// act on: the child sits on recorded work, and the branch it asks to land on is written down.
	f.SwitchTo("alpha")
	landing := landAndRecord(t, f, "alpha", "main")
	f.SwitchTo("beta")
	f.MustGit("rebase", "--onto", landing, "alpha", "beta")
	ready(t, f)
	submit(t, f, "approve")

	res = runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")
	mustContain(t, res.stdout, "merge into:  main (where alpha landed)",
		"the destination is the branch the parent landed on, which the child's own base no longer names")

	if got := durableRefs(t, f); len(got) != 2 {
		t.Errorf("durable refs = %v, want only the parent's record: a declaration writes none", got)
	}
}

// A child of a landed parent asks for a branch its own yaml no longer names: the base moved to the parent's
// integration ref when the parent landed, and the destination has to be read around it. The command says
// which rule produced the answer, because a reader auditing a merge cannot see the walk.
func TestChangeIntegrateReportsADestinationInheritedFromALandedParent(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))
	landAndRecord(t, f, "alpha", "main")
	f.SwitchTo("alpha")
	stackedOff(t, f, "beta", "alpha", "alpha", "b.go")
	ready(t, f)
	submit(t, f, "approve")

	j := runIn(t, f.Dir(), "change", "integrate", "--json").mustSucceed(t, "change", "integrate").json(t)
	if j["destination"] != "main" {
		t.Errorf("destination = %v, want main", j["destination"])
	}
	if j["destination_source"] != "parent" {
		t.Errorf("destination_source = %v, want parent", j["destination_source"])
	}
	via, _ := j["destination_via"].([]any)
	if len(via) != 1 || via[0] != "alpha" {
		t.Errorf("destination_via = %v, want [alpha]", j["destination_via"])
	}
	if base, _ := j["base"].(string); base != reviewref.Integration("alpha") {
		t.Errorf("base = %q, want the parent's integration ref: the measurement stays where the landing "+
			"moved it, and only the destination reads around it", base)
	}
}

// `check` answers both halves of CI's question in one run, from one read of the repository. `ready` is
// "may this merge"; `integrating` is "did the author ask", and the conjunction is the gate. Each half has
// to be able to move on its own — that is the whole reason there are two of them.
func TestCheckReportsTheDeclarationBesideItsVerdict(t *testing.T) {
	f, _, _, _ := approvedChangeset(t)

	before := runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check").json(t)
	if before["integrating"] != false {
		t.Errorf("integrating = %v before any declaration", before["integrating"])
	}
	if _, ok := before["integrate_commit"]; ok {
		t.Errorf("integrate_commit = %v before any declaration, want it absent", before["integrate_commit"])
	}

	runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")
	declared := f.Head()
	after := runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check").json(t)
	if after["ready"] != true || after["integrating"] != true {
		t.Errorf("ready/integrating = %v/%v, want both true: this is the pair CI gates on",
			after["ready"], after["integrating"])
	}
	if after["integrate_commit"] != declared {
		t.Errorf("integrate_commit = %v, want the declaration %s", after["integrate_commit"], declared)
	}
	mustContain(t, runIn(t, f.Dir(), "check").mustSucceed(t, "check").stdout,
		"declared: "+f.Short(declared), "the human answer shows the declaration too")

	// The declaration is not a verdict: content that moved since the approval takes `ready` away and
	// leaves the request standing, so the pair reads as "asked, and not yet permitted".
	f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { transaction() }\n"))
	drifted := runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check").json(t)
	if drifted["ready"] != false || drifted["integrating"] != true {
		t.Errorf("ready/integrating = %v/%v after the approved content moved, want false/true",
			drifted["ready"], drifted["integrating"])
	}
}

// A declaration is superseded, not erased. Once the author puts the work back in review — or a reviewer
// answers it — the newest statement is no longer "merge this", and a pipeline that merged on the older one
// would perform the merge the author has just taken back.
func TestCheckStopsReportingADeclarationThatWasSuperseded(t *testing.T) {
	f, slug, _, _ := approvedChangeset(t)
	runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")
	f.Commit("note the answer", gittest.WithFile("changesets/"+slug+"/ABOUT.md",
		"# "+slug+"\n\nConcurrency answered.\n"))
	ready(t, f)

	j := runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check").json(t)
	if j["integrating"] != false {
		t.Errorf("integrating = %v after the work was put back in review, want false", j["integrating"])
	}
	if _, ok := j["integrate_commit"]; ok {
		t.Errorf("integrate_commit = %v after a re-offer, want it absent", j["integrate_commit"])
	}
	if j["state"] != "READY" {
		t.Errorf("state = %v, want READY", j["state"])
	}
}

// The usage contract: arguments and unknown flags are exit 2, so a script with a typo fails as a mistake
// rather than as a refusal somebody has to read.
func TestChangeIntegrateUsageErrors(t *testing.T) {
	f, _, _, _ := approvedChangeset(t)
	if got := runIn(t, f.Dir(), "change", "integrate", "now"); got.code != exitUsage {
		t.Errorf("an extra argument exited %d, want %d\n%s", got.code, exitUsage, got.stderr)
	}
	if got := runIn(t, f.Dir(), "change", "integrate", "--approve"); got.code != exitUsage {
		t.Errorf("an unknown flag exited %d, want %d\n%s", got.code, exitUsage, got.stderr)
	}
	// The command is under `change`, and the group help has to show it: a subcommand nobody can find in
	// `--help` is a subcommand nobody uses.
	help := runIn(t, f.Dir(), "change", "--help").mustSucceed(t, "change", "--help").stdout
	mustContain(t, help, "integrate", "`git pair change --help` lists the declaration")
}

// A clone that has fetched and not branched off trunk knows the integration branch only as
// `refs/remotes/origin/main`, while its changeset file spells the same branch `main`. Comparing one
// spelling to the other says "stacked on a branch called main", and for this command that reading is not
// cosmetic: an unlanded parent is a refusal, so the command that asks for a merge refused to ask in the
// ordinary state of the clone that most needs it — the one a pipeline makes. The scripted CI replay in
// scripts/gates/ci-integrate.sh is what caught it, because a replay starts from a clone.
func TestChangeIntegrateAsksForTheMergeInACloneWhereTrunkIsOnlyAFetchRef(t *testing.T) {
	f, _, _, _ := approvedChangeset(t)
	remote := remoteWith(t, f)
	f.MustGit("push", "--quiet", "origin", "main", "booking-transaction")
	clone := filepath.Join(t.TempDir(), "clone")
	f.MustGit("clone", "--quiet", remote, clone)

	// The fixture has to be the case under test, and that is worth checking rather than assuming: a clone
	// that somehow held a local trunk would pass for the wrong reason.
	if got := gitIn(t, clone, "for-each-ref", "--format=%(refname)", "refs/remotes/origin/main"); got == "" {
		t.Fatal("the clone does not hold trunk as a fetch ref, so it proves nothing")
	}
	gitIn(t, clone, "checkout", "--quiet", "booking-transaction")
	// The clone is a fresh working copy with the fixture's isolated configuration, so it has no identity of
	// its own until one is given: the marker commit this command makes needs an author.
	gitIn(t, clone, "config", "user.email", "ci@example.com")
	gitIn(t, clone, "config", "user.name", "CI")
	// The guard that matters: this run has to be reading trunk through the fetch root. A clone whose
	// `main` was checked out locally would answer the question the wrong way round and pass for nothing.
	spelling := runIn(t, clone, "status", "--json").mustSucceed(t, "status", "--json").json(t)
	if spelling["default_branch"] != "origin/main" {
		t.Fatalf("default_branch = %v, want origin/main: the run must be reading trunk through refs/remotes", spelling["default_branch"])
	}

	res := runIn(t, clone, "change", "integrate", "--json").mustSucceed(t, "change", "integrate")
	j := res.json(t)
	if j["state"] != "INTEGRATING" {
		t.Errorf("state = %v, want INTEGRATING", j["state"])
	}
	if j["destination"] != "main" {
		t.Errorf("destination = %v, want main", j["destination"])
	}
	mustNotContain(t, res.stdout, "records no parent changeset", "trunk is not reported as a parent that has not landed")

	// The same reading in `status`: a changeset measured against the integration branch is not stacked on
	// it, and a Stack section naming trunk as a parent sends an author to `init --parent` for a stack that
	// does not exist.
	st := runIn(t, clone, "status").mustSucceed(t, "status")
	mustNotContain(t, st.stdout, "parent: main", "status does not call trunk a parent")

	// And the gate agrees with the command, in the same clone, on the same head.
	chk := runIn(t, clone, "check", "--json").mustSucceed(t, "check", "--json").json(t)
	if chk["ready"] != true || chk["integrating"] != true {
		t.Errorf("check said ready=%v integrating=%v for the head just declared, want both true", chk["ready"], chk["integrating"])
	}
}
