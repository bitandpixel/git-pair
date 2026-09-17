package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// PRD §9.1: `git pair change init` creates the deterministic changeset directory, its
// metadata and ABOUT.md, accepts --base, is idempotent, and must not destroy existing
// changeset data.
func TestChangeInitCreatesScaffoldingFromBranchName(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("feature/booking-transaction")
	head := f.Head()

	res := runIn(t, f.Dir(), "change", "init", "--base", "main").mustSucceed(t, "change", "init")

	dir := filepath.Join("changesets", "feature-booking-transaction")
	if !f.HasWorktreeFile(filepath.Join(dir, "CHANGESET.yaml")) {
		t.Fatalf("CHANGESET.yaml was not created; output:\n%s", res.stdout)
	}
	if !f.HasWorktreeFile(filepath.Join(dir, "ABOUT.md")) {
		t.Error("ABOUT.md was not created")
	}
	if got, want := strings.TrimSpace(f.Read(filepath.Join(dir, "CHANGESET.yaml"))),
		"id: feature-booking-transaction\nbase: main"; got != want {
		t.Errorf("CHANGESET.yaml = %q, want %q (PRD §5)", got, want)
	}
	about := f.Read(filepath.Join(dir, "ABOUT.md"))
	for _, heading := range []string{"Summary", "What changed", "Design decisions", "Validation"} {
		mustContain(t, about, heading, "ABOUT.md scaffold")
	}
	// `change init` commits the scaffold, so a following `change ready` is not
	// blocked by a dirty working tree.
	if f.Head() == head {
		t.Error("change init did not commit the scaffolding")
	}
	if got := f.Subject("HEAD"); got != "git-pair: initialize changeset feature-booking-transaction" {
		t.Errorf("HEAD subject = %q, want the initialize marker", got)
	}
	if !f.Clean() {
		t.Errorf("the scaffolding commit left the working tree dirty:\n%s", f.MustGit("status", "--porcelain"))
	}
}

// PRD §9.1 and §21: a stacked branch names its sibling changeset as its base.
func TestChangeInitRecordsRequestedBase(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking-transaction")
	f.CommitChangeset("booking-transaction", "main")
	f.Commit("impl", gittest.WithFile("service.go", "package main\n"))

	f.CreateBranch("booking-transaction-tests", "booking-transaction")
	runIn(t, f.Dir(), "change", "init", "--base", "booking-transaction").mustSucceed(t, "change", "init")

	if got := f.MetadataBase("booking-transaction-tests"); got != "booking-transaction" {
		t.Errorf("base = %q, want booking-transaction", got)
	}
	// The lower changeset's data is untouched.
	if got := f.MetadataBase("booking-transaction"); got != "main" {
		t.Errorf("the lower changeset's base changed to %q", got)
	}
}

func TestChangeInitIsIdempotent(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")

	runIn(t, f.Dir(), "change", "init", "--base", "main").mustSucceed(t, "change", "init")
	dir := filepath.Join("changesets", "booking")
	about := f.Read(filepath.Join(dir, "ABOUT.md"))
	metadata := f.Read(filepath.Join(dir, "CHANGESET.yaml"))

	second := runIn(t, f.Dir(), "change", "init", "--base", "main").mustSucceed(t, "change", "init")
	if got := f.Read(filepath.Join(dir, "ABOUT.md")); got != about {
		t.Errorf("second init rewrote ABOUT.md:\n%s", got)
	}
	if got := f.Read(filepath.Join(dir, "CHANGESET.yaml")); got != metadata {
		t.Errorf("second init rewrote CHANGESET.yaml:\n%s", got)
	}
	mustContain(t, second.stdout+second.stderr, "unchanged", "second init output")

	// Author content must survive a re-run just as the scaffold does (PRD §9.1).
	authorText := "# booking\n\n## Summary\n\nThe real description.\n"
	f.Write(filepath.Join(dir, "ABOUT.md"), authorText)
	runIn(t, f.Dir(), "change", "init", "--base", "main").mustSucceed(t, "change", "init")
	if got := f.Read(filepath.Join(dir, "ABOUT.md")); got != authorText {
		t.Errorf("init overwrote the author's ABOUT.md:\n%s", got)
	}
}

func TestChangeInitBaseConflict(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	runIn(t, f.Dir(), "change", "init", "--base", "main").mustSucceed(t, "change", "init")

	res := runIn(t, f.Dir(), "change", "init", "--base", "trunk")
	if res.code != exitUsage {
		t.Errorf("changing the base exited %d, want %d (usage error)\nstderr: %s", res.code, exitUsage, res.stderr)
	}
	if got := f.MetadataBase("booking"); got != "main" {
		t.Errorf("refused init changed base to %q, want main", got)
	}
	mustContain(t, res.stderr+res.stdout, "--set-base", "conflict message")

	runIn(t, f.Dir(), "change", "init", "--base", "trunk", "--set-base").mustSucceed(t, "change", "init")
	if got := f.MetadataBase("booking"); got != "trunk" {
		t.Errorf("base = %q after --set-base, want trunk", got)
	}
}

// PRD §9.1 shows `--base main`; the default must be the repository's trunk and never
// a guess at something else.
func TestChangeInitDefaultsToTrunk(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")

	runIn(t, f.Dir(), "change", "init").mustSucceed(t, "change", "init")

	if got := f.MetadataBase("booking"); got != "main" {
		t.Errorf("base = %q, want main", got)
	}
}

func TestChangeInitRefusesToGuessWithoutTrunk(t *testing.T) {
	f := gittest.New(t)
	f.CreateBranch("trunk")
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch("booking")

	res := runIn(t, f.Dir(), "change", "init")
	if res.code != exitUsage {
		t.Errorf("init with no main/master branch exited %d, want %d\nstderr: %s", res.code, exitUsage, res.stderr)
	}
	mustContain(t, res.stderr, "--base", "the refusal must tell the caller how to be explicit")
	if f.HasWorktreeFile(filepath.Join("changesets", "booking", "CHANGESET.yaml")) {
		t.Error("init wrote a base it could not determine")
	}
}

// `change init` needs a branch to name the changeset after (plan M1).
func TestChangeInitRefusesDetachedHead(t *testing.T) {
	f := newRepo(t)
	f.Detach()

	res := runIn(t, f.Dir(), "change", "init", "--base", "main")
	if res.code != exitUsage {
		t.Errorf("detached HEAD exited %d, want %d\nstderr: %s", res.code, exitUsage, res.stderr)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.Dir(), "changesets")); len(entries) != 0 {
		t.Errorf("init wrote a changeset on a detached HEAD: %v", entries)
	}
}

// PRD §9.2: `change ready` creates a lifecycle marker commit with machine-readable
// trailers and makes the changeset discoverable by `git pair review queue`.
func TestChangeReadyCreatesMarkerAndEnqueuesChangeset(t *testing.T) {
	f, slug := newChangeset(t, "feature/booking-transaction", "main")
	before := f.Head()

	ready(t, f)

	head := f.Head()
	if head == before {
		t.Fatal("change ready created no commit")
	}
	if got := f.Subject(head); got != "git-pair: ready "+slug {
		t.Errorf("subject = %q, want %q (PRD §9.2)", got, "git-pair: ready "+slug)
	}
	trailers := f.Trailers(head)
	if trailers["Review-State"] != "ready" {
		t.Errorf("Review-State = %q, want ready", trailers["Review-State"])
	}
	if trailers["Review-Changeset"] != slug {
		t.Errorf("Review-Changeset = %q, want %q", trailers["Review-Changeset"], slug)
	}
	if got := f.ParentCount(head); got != 1 {
		t.Errorf("ready marker has %d parents, want 1", got)
	}
	if !f.Clean() {
		t.Error("change ready left the working tree dirty")
	}

	queue := runIn(t, f.Dir(), "review", "queue", "--json").mustSucceed(t, "review", "queue", "--json")
	if !queueListsChangeset(t, queue, slug) {
		t.Errorf("review queue does not list the changeset that was just marked ready:\n%s", queue.stdout)
	}

	status := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json").json(t)
	if status["state"] != "READY" {
		t.Errorf("status state = %v, want READY", status["state"])
	}
}

// PRD §9.2 lists the preconditions: changeset exists, clean tree, ABOUT.md exists.
func TestChangeReadyRefusesWithoutChangeset(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("lonely")
	f.Commit("impl", gittest.WithFile("a.go", "package main\n"))
	before := f.Head()

	res := runIn(t, f.Dir(), "change", "ready")
	if res.code != exitUsage {
		t.Errorf("exited %d, want %d (no changeset to mark)\nstderr: %s", res.code, exitUsage, res.stderr)
	}
	mustContain(t, res.stderr, "change init", "the refusal must point at the command that fixes it")
	if f.Head() != before {
		t.Error("a ready marker was created for a branch with no changeset")
	}
}

// The plan's exit-code table lists a dirty tree as a business-rule refusal (1), not a
// usage error (2): the command was used correctly and the repository refused it.
func TestChangeReadyRefusesDirtyWorkingTree(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	before := f.Head()
	f.Write("service.go", "package main\n\n// uncommitted reviewer note\nfunc Lock() {}\n")

	res := runIn(t, f.Dir(), "change", "ready")
	if res.code != exitRefusal {
		t.Errorf("dirty tree exited %d, want %d (plan: 1 is a business-rule refusal; 2 is a usage error)\nstderr: %s",
			res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "clean", "the refusal must say the tree must be clean")
	if f.Head() != before {
		t.Error("change ready created a marker from a dirty working tree")
	}
}

func TestChangeReadyRefusesWithoutAbout(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	f.Remove(filepath.Join("changesets", slug, "ABOUT.md"))
	f.Commit("drop the description")
	before := f.Head()

	res := runIn(t, f.Dir(), "change", "ready")
	if res.code == exitOK {
		t.Error("change ready succeeded without ABOUT.md (PRD §9.2)")
	}
	mustContain(t, res.stderr, "ABOUT.md", "the refusal must name the missing file")
	if f.Head() != before {
		t.Error("change ready created a marker without ABOUT.md")
	}
}

// PRD §19.2: a reviewer-added line the author left untouched must make `change ready`
// print it, exit non-zero, and not create the marker. Resolving it unblocks.
func TestChangeReadyBlockedBySurvivingReviewAdditions(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "block")

	const untouched = "// What happens if these execute concurrently?"
	const resolved = "// Please use a transaction here"
	f.Write("service.go", "package main\n\n"+resolved+"\nfunc Lock() {}\n")
	f.Write("handler.go", "package main\n\n"+untouched+"\nfunc Serve() {}\n")
	submit(t, f, "block")

	// The author rewrites one comment and leaves the other alone.
	f.Commit("address one comment", gittest.WithFile("service.go", "package main\n\nfunc Lock() { transaction() }\n"))
	before := f.Head()

	res := runIn(t, f.Dir(), "change", "ready")
	if res.code != exitRefusal {
		t.Fatalf("surviving review addition exited %d, want %d\nstdout: %s\nstderr: %s",
			res.code, exitRefusal, res.stdout, res.stderr)
	}
	report := res.stderr
	mustContain(t, report, "handler.go", "the report must name the file")
	mustContain(t, report, untouched, "the report must print the surviving addition")
	mustContain(t, report, "1 addition from review", "the report must count the surviving additions")
	mustContain(t, report, "Cannot mark changeset "+slug+" ready", "the report must say what was refused")
	mustContain(t, report, "git pair change ready --allow-surviving-review-additions",
		"PRD §19.2 prints the override an author can use")
	if f.Head() != before {
		t.Errorf("a ready marker was created despite the surviving addition (HEAD %s -> %s)", before, f.Head())
	}
	if queueListsChangeset(t, runIn(t, f.Dir(), "review", "queue", "--json"), slug) {
		t.Error("the refused changeset appeared in the review queue")
	}

	// Resolving the surviving line unblocks the command.
	f.Commit("resolve the rest", gittest.WithFile("handler.go", "package main\n\nfunc Serve() { ctx() }\n"))
	ready(t, f)

	if got := f.Subject(f.Head()); got != "git-pair: ready "+slug {
		t.Errorf("HEAD = %q, want the ready marker", got)
	}
}

// PRD §9.2 and §19.2: the override exists for review material that is deliberately
// retained, and the command must stay fully non-interactive.
func TestChangeReadyOverrideAcknowledgesSurvivingAdditions(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "block")

	const kept = "// Please keep this guard"
	f.Write("service.go", "package main\n\n"+kept+"\nfunc Lock() {}\n")
	submit(t, f, "block")
	f.Commit("author response", gittest.WithFile("handler.go", "package main\n\nfunc Serve() {} // touched\n"))
	before := f.Head()

	// stdin is /dev/null, so a command that tried to prompt would fail rather than
	// hang; success here is the non-interactive guarantee.
	res := runIn(t, f.Dir(), "change", "ready", "--allow-surviving-review-additions").
		mustSucceed(t, "change", "ready", "--allow-surviving-review-additions")

	if f.Head() == before {
		t.Fatal("the override created no ready marker")
	}
	if got := f.Subject(f.Head()); got != "git-pair: ready "+slug {
		t.Errorf("subject = %q, want the ready marker", got)
	}
	mustContain(t, res.stdout+res.stderr, "surviving",
		"the override must report what it acknowledged (PRD §9.2)")
}

// PRD §19.1 and §9.2: the check covers only the most recent review submission.
func TestChangeReadyChecksOnlyTheMostRecentReview(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)

	// First review: one comment, later resolved.
	f.Write("service.go", "package main\n\n// first review comment\nfunc Lock() {}\n")
	submit(t, f, "block")
	f.Commit("resolve first review", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))

	// Second review: one comment, left untouched, but the author *modified* it, so it
	// no longer survives unchanged.
	const second = "// second review comment"
	f.Write("handler.go", "package main\n\n"+second+"\nfunc Serve() {}\n")
	submit(t, f, "block")
	f.Commit("modify the second comment", gittest.WithFile("handler.go",
		"package main\n\n// second review comment -- addressed below\nfunc Serve() {}\n"))

	ready(t, f)

	if got := f.Subject(f.Head()); got != "git-pair: ready booking" {
		t.Errorf("HEAD = %q, want the ready marker", got)
	}
}

// A review whose only additions are inside the changeset directory must not block
// readiness (plan decision D3): answering a thread by appending keeps every line the
// reviewer wrote.
func TestChangeReadyNotBlockedByChangesetArtifactSurvivals(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)

	thread := filepath.Join("changesets", slug, "concurrency-tests.md")
	f.Write(thread, "# Concurrency tests\n\nI'm not convinced the test makes these overlap.\n")
	submit(t, f, "block")

	// The author answers by appending, so every reviewer line still survives.
	f.Commit("answer the thread", gittest.WithFile(thread,
		"# Concurrency tests\n\nI'm not convinced the test makes these overlap.\n\n"+
			"## Response\n\nBoth transactions now block before the capacity read.\n"))

	ready(t, f)

	if got := f.Subject(f.Head()); got != "git-pair: ready booking" {
		t.Errorf("HEAD = %q, want the ready marker", got)
	}
}

func queueListsChangeset(t *testing.T, res result, slug string) bool {
	t.Helper()
	for _, entry := range res.jsonList(t, "ready_for_review") {
		row, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if row["changeset"] == slug {
			return true
		}
	}
	return false
}

// --- change init: committing -------------------------------------------------

// PRD §9.1 + §28 (agent contract): init must leave the repository in a state
// where the next command works, which means the scaffold cannot sit uncommitted
// in the working tree and block `change ready`.
func TestChangeInitCommitsTheScaffold(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	before := f.Head()

	runIn(t, f.Dir(), "change", "init", "--base", "main").mustSucceed(t, "change", "init")

	if f.RevListCount("main..HEAD") != 1 {
		t.Errorf("init made %d commits above main, want exactly 1", f.RevListCount("main..HEAD"))
	}
	got := f.ChangedFiles(before, f.Head())
	want := []string{"changesets/booking/ABOUT.md", "changesets/booking/CHANGESET.yaml"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("commit touched %v, want %v", got, want)
	}
	if !f.Clean() {
		t.Errorf("working tree is dirty after init:\n%s", f.MustGit("status", "--porcelain"))
	}
}

// The commit is scoped with `git commit --only`. Without that scope, init would
// silently consume whatever the author had already staged for a different
// commit, which is the one way this command could destroy work.
func TestChangeInitCommitLeavesUnstagedAndStagedWorkAlone(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	f.Write("staged.go", "package main\n")
	f.MustGit("add", "staged.go")
	f.Write("unstaged.go", "package main\n")

	runIn(t, f.Dir(), "change", "init", "--base", "main").mustSucceed(t, "change", "init")

	if got := f.ChangedFiles("HEAD~1", "HEAD"); len(got) != 2 {
		t.Errorf("commit touched %v, want only the two scaffolding files", got)
	}
	status := f.MustGit("status", "--porcelain")
	if !strings.Contains(status, "A  staged.go") {
		t.Errorf("the author's staged file was swept out of the index:\n%s", status)
	}
	if !strings.Contains(status, "?? unstaged.go") {
		t.Errorf("the author's unstaged file was touched:\n%s", status)
	}
	if f.HasFile("HEAD", "staged.go") {
		t.Error("staged.go was committed by init")
	}
}

func TestChangeInitNoCommitLeavesTheScaffoldUncommitted(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	before := f.Head()

	res := runIn(t, f.Dir(), "change", "init", "--base", "main", "--no-commit").mustSucceed(t, "change", "init")

	if f.Head() != before {
		t.Error("--no-commit still created a commit")
	}
	mustContain(t, res.stdout, "--no-commit", "output should say why nothing was committed")
	if !f.HasWorktreeFile(filepath.Join("changesets", "booking", "ABOUT.md")) {
		t.Error("--no-commit did not write the scaffolding")
	}
}

// Re-running init must not fail and must not add an empty commit.
func TestChangeInitAfterCommittingIsIdempotent(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	runIn(t, f.Dir(), "change", "init", "--base", "main").mustSucceed(t, "change", "init")
	first := f.Head()

	second := runIn(t, f.Dir(), "change", "init", "--base", "main").mustSucceed(t, "change", "init")

	if f.Head() != first {
		t.Errorf("re-init created a commit: HEAD moved from %s to %s", first, f.Head())
	}
	mustContain(t, second.stdout, "already tracked", "re-init should explain that there is nothing to commit")
}

// --- change init: ABOUT.md content ------------------------------------------

// The point of --about: initialise and describe in one non-interactive call, so
// an agent does not have to sequence a write, an add, and a commit.
func TestChangeInitAboutFlagWritesAndCommitsContent(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")

	body := "# booking\n\n## Summary\n\nAdds row-level locking.\n"
	runIn(t, f.Dir(), "change", "init", "--base", "main", "--about", body).mustSucceed(t, "change", "init")

	if got := f.Read(filepath.Join("changesets", "booking", "ABOUT.md")); got != body {
		t.Errorf("ABOUT.md = %q, want %q", got, body)
	}
	if got := f.FileAt("HEAD", filepath.Join("changesets", "booking", "ABOUT.md")); got != body {
		t.Errorf("ABOUT.md at HEAD = %q, want it committed in the same call", got)
	}
}

// A piped body is normalised to exactly one trailing newline, so `--about
// "$(cat f)"` and `--about - < f` agree.
func TestChangeInitAboutContentIsNewlineNormalised(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")

	runIn(t, f.Dir(), "change", "init", "--base", "main", "--about", "# booking\n\n## Summary\n\nNo trailing newline.\n\n\n").
		mustSucceed(t, "change", "init")

	if got, want := f.Read(filepath.Join("changesets", "booking", "ABOUT.md")), "# booking\n\n## Summary\n\nNo trailing newline.\n"; got != want {
		t.Errorf("ABOUT.md = %q, want %q", got, want)
	}
}

func TestChangeInitReadsAboutFromStdin(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")

	body := "# booking\n\n## Summary\n\nDescribed through a pipe.\n"
	runStdinIn(t, f.Dir(), body, "change", "init", "--base", "main").mustSucceed(t, "change", "init")

	if got := f.FileAt("HEAD", filepath.Join("changesets", "booking", "ABOUT.md")); got != body {
		t.Errorf("ABOUT.md at HEAD = %q, want %q", got, body)
	}
}

// Piped-but-empty input is not "described": `cmd </dev/null` and an agent that
// closes stdin must get the scaffold rather than an empty ABOUT.md.
func TestChangeInitEmptyStdinFallsBackToTheScaffold(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")

	runStdinIn(t, f.Dir(), "", "change", "init", "--base", "main").mustSucceed(t, "change", "init")

	about := f.Read(filepath.Join("changesets", "booking", "ABOUT.md"))
	mustContain(t, about, "## Summary", "scaffold headings")
	mustContain(t, about, "## Known limitations", "scaffold headings")
}

func TestChangeInitAboutDashNeedsStdin(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")

	res := runStdinIn(t, f.Dir(), "", "change", "init", "--base", "main", "--about", "-")
	if res.code != exitUsage {
		t.Errorf("--about - with empty stdin exited %d, want %d\n%s", res.code, exitUsage, res.stderr)
	}
}

// PRD §9.1 "should not destroy existing changeset data": --about cannot replace
// a description that is already there unless --set-about says so.
func TestChangeInitAboutDoesNotClobberWithoutSetAbout(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	original := "# booking\n\n## Summary\n\nThe author's own description.\n"
	f.StageChangeset("booking", "main")
	f.WriteChangesetFile("booking", "ABOUT.md", original)
	f.MustGit("add", "changesets/booking/ABOUT.md")
	f.MustGit("commit", "-m", "describe booking")

	res := runIn(t, f.Dir(), "change", "init", "--base", "main", "--about", "# booking\n\noverwritten\n")
	if res.code != exitUsage {
		t.Errorf("--about over existing content exited %d, want %d\n%s", res.code, exitUsage, res.stderr)
	}
	mustContain(t, res.stderr, "--set-about", "refusal should name the flag that allows it")
	if got := f.Read(filepath.Join("changesets", "booking", "ABOUT.md")); got != original {
		t.Errorf("ABOUT.md was modified: %q", got)
	}

	runIn(t, f.Dir(), "change", "init", "--base", "main", "--about", "# booking\n\nreplaced\n", "--set-about").
		mustSucceed(t, "change", "init")
	if got := f.FileAt("HEAD", filepath.Join("changesets", "booking", "ABOUT.md")); got != "# booking\n\nreplaced\n" {
		t.Errorf("ABOUT.md at HEAD = %q, want the replacement committed", got)
	}
}

// The scaffold commit carries the changeset trailer but no lifecycle state, so
// it must not move the changeset out of WORKING or into the review queue.
func TestChangeInitCommitDoesNotChangeLifecycleState(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	runIn(t, f.Dir(), "change", "init", "--base", "main").mustSucceed(t, "change", "init")

	status := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status")
	if got := status.json(t)["state"]; got != "WORKING" {
		t.Errorf("state after init = %v, want WORKING", got)
	}
	if list := runIn(t, f.Dir(), "review", "queue", "--json").jsonList(t, "ready_for_review"); len(list) != 0 {
		t.Errorf("a freshly initialised changeset is in the review queue: %v", list)
	}
}

// --- change init / ready: a base must not be the branch itself ---------------

// Starting a changeset on the integration branch is refused. A changeset is measured against
// that branch, so one started on it can never contain anything, and every directory it carries
// is already landed. The refusal is a clarity guard rather than a correctness rule — the tree
// rule already makes such a changeset invisible — which is why it says what to do instead.
func TestChangeInitRefusesOnTheIntegrationBranch(t *testing.T) {
	f := newRepo(t)

	res := runIn(t, f.Dir(), "change", "init", "--base", "HEAD")
	if res.code != exitUsage {
		t.Fatalf("`change init` on the integration branch exited %d, want %d\n%s", res.code, exitUsage, res.stderr)
	}
	mustContain(t, res.stderr, "integration branch", "the refusal should name what is wrong")
	mustContain(t, res.stderr, "switch -c", "the refusal should say what to do instead")
	if f.HasWorktreeFile(filepath.Join("changesets", "main", "CHANGESET.yaml")) {
		t.Error("init wrote a changeset directory on the integration branch")
	}
}

// Everything derived from a changeset is measured as `base...HEAD`. With base ==
// branch that range is empty for all time, so `change ready` writes a marker that
// no command can observe and `status` answers WORKING forever. This is the
// configuration that has to be refused, and refused where it is created.
func TestChangeInitRefusesToBaseAChangesetOnItsOwnBranch(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")

	for _, args := range [][]string{
		{"change", "init", "--base", "booking"}, // asked for explicitly
		{"change", "init", "--base", "HEAD"},    // self-reference by another name
	} {
		res := runIn(t, f.Dir(), args...)
		if res.code != exitUsage {
			t.Errorf("%v exited %d, want %d\n%s", args, res.code, exitUsage, res.stderr)
			continue
		}
		mustContain(t, res.stderr, "branch it lives on", "refusal should name the problem")
		mustContain(t, res.stderr, "switch -c", "refusal should say what to do instead")
	}

	if f.HasWorktreeFile(filepath.Join("changesets", "main", "CHANGESET.yaml")) {
		t.Error("init wrote a changeset directory it had just refused to create")
	}
}

// The opposite guard: a branch created moments ago shares its tip with main, and
// init must still work there. Comparing commits instead of refs would break the
// normal first run of `change init`.
func TestChangeInitAllowsAFreshBranchThatSharesItsBasesTip(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	if f.MergeBase("main", "HEAD") != f.RevParse("HEAD") {
		t.Fatal("the fixture branch is not at its base's tip; this test no longer guards anything")
	}

	runIn(t, f.Dir(), "change", "init", "--base", "main").mustSucceed(t, "change", "init")

	if got := f.MetadataBase("booking"); got != "main" {
		t.Errorf("base = %q, want main", got)
	}
}

func TestChangeReadyRefusesASelfBasedChangeset(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	f.StageChangeset("booking", "booking")
	f.WriteChangesetFile("booking", "ABOUT.md", "# booking\n\n## Summary\n\nDescribed.\n")
	f.Commit("work", gittest.WithFile("service.go", "package main\n"))

	res := runIn(t, f.Dir(), "change", "ready")
	if res.code != exitRefusal {
		t.Errorf("change ready on a self-based changeset exited %d, want %d\n%s", res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "this branch itself", "refusal should name the problem")
	if strings.HasPrefix(f.Subject("HEAD"), "git-pair: ready") {
		t.Error("ready created a marker commit it should have refused to write")
	}
}
