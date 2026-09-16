package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpr/internal/gittest"
)

// PRD §9.1: `gitpr change init` creates the deterministic changeset directory, its
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
	if got := f.Read(filepath.Join(dir, "CHANGESET.yaml")); strings.TrimSpace(got) != "base: main" {
		t.Errorf("CHANGESET.yaml = %q, want %q (PRD §5)", got, "base: main")
	}
	about := f.Read(filepath.Join(dir, "ABOUT.md"))
	for _, heading := range []string{"Summary", "What changed", "Design decisions", "Validation"} {
		mustContain(t, about, heading, "ABOUT.md scaffold")
	}
	// The author commits scaffolding with their implementation (PRD §22 step 4), so
	// init itself must not create a commit.
	if f.Head() != head {
		t.Errorf("change init created a commit: HEAD moved from %s to %s", head, f.Head())
	}
	if f.Clean() {
		t.Error("the new scaffolding is not visible as a working-tree change")
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
// trailers and makes the changeset discoverable by `gitpr review queue`.
func TestChangeReadyCreatesMarkerAndEnqueuesChangeset(t *testing.T) {
	f, slug := newChangeset(t, "feature/booking-transaction", "main")
	before := f.Head()

	ready(t, f)

	head := f.Head()
	if head == before {
		t.Fatal("change ready created no commit")
	}
	if got := f.Subject(head); got != "gitpr: ready "+slug {
		t.Errorf("subject = %q, want %q (PRD §9.2)", got, "gitpr: ready "+slug)
	}
	trailers := f.Trailers(head)
	if trailers["GitPR-State"] != "ready" {
		t.Errorf("GitPR-State = %q, want ready", trailers["GitPR-State"])
	}
	if trailers["GitPR-Changeset"] != slug {
		t.Errorf("GitPR-Changeset = %q, want %q", trailers["GitPR-Changeset"], slug)
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
	mustContain(t, report, "gitpr change ready --allow-surviving-review-additions",
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

	if got := f.Subject(f.Head()); got != "gitpr: ready "+slug {
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
	if got := f.Subject(f.Head()); got != "gitpr: ready "+slug {
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

	if got := f.Subject(f.Head()); got != "gitpr: ready booking" {
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

	if got := f.Subject(f.Head()); got != "gitpr: ready booking" {
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
