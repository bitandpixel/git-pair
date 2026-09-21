package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// TestPRDTwentyNineGoldenWorkflow replays PRD §29 end to end through the CLI only:
// init, implement, ready, queue, review block, response, the surviving-additions refusal, ready
// again, approve, the gate, the landing, and the record. Assertions are on git state (commits,
// trailers, refs, reachability) rather than on formatted output.
//
// The shape worth naming: through all of the review, nothing is written to refs/git-pair. The two
// durable refs appear together, at landing, and the archive half is what makes the unsquashed chain
// readable after the branch is deleted — which is the whole promise, moved from a ref maintained
// during the work to a record written once at the end of it.
func TestPRDTwentyNineGoldenWorkflow(t *testing.T) {
	const slug = "booking-transaction"
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n\nfunc main() {}\n"))
	mainBefore := f.RevParse("main")

	// --- author: git pair init --base main ---------------------------------
	f.CreateBranch(slug)
	runIn(t, f.Dir(), "init", "--base", "main").mustSucceed(t, "init")

	metadata := filepath.Join("changesets", slug, "CHANGESET.yaml")
	about := filepath.Join("changesets", slug, "ABOUT.md")
	wantMetadata := "id: " + slug + "\nbase: main"
	if got := strings.TrimSpace(f.Read(metadata)); got != wantMetadata {
		t.Fatalf("CHANGESET.yaml = %q, want %q", got, wantMetadata)
	}
	if !f.HasWorktreeFile(about) {
		t.Fatal("ABOUT.md was not scaffolded")
	}

	// --- author: implement, populate ABOUT.md, commit with ordinary git --------
	f.Write(about, "# Booking transaction locking\n\n"+
		"## Summary\n\nAdds transactional locking around offering creation.\n\n"+
		"## Validation\n\n- unit tests\n- concurrent final-seat test\n")
	f.Write("service.go", "package main\n\nfunc CreateOffering() {}\n\nfunc Enroll() {}\n")
	f.Write("service_test.go", "package main\n\nfunc TestFinalSeat() {}\n")
	f.Commit("implement booking transaction locking")

	// --- author: git pair change ready -------------------------------------------
	ready(t, f)

	queue := runIn(t, f.Dir(), "queue", "--json")
	if rows := queue.jsonList(t, "ready_for_review"); len(rows) != 1 ||
		rows[0].(map[string]any)["changeset"] != slug {
		t.Fatalf("queue = %v, want %s waiting for review", rows, slug)
	}

	// --- reviewer: inspect, edit code, edit ABOUT.md, open a thread, submit -----
	const blockingOne = "// Please use a transaction here"
	const blockingTwo = "// What happens if these execute concurrently?"
	f.Write("service.go", "package main\n\n"+blockingOne+"\nfunc CreateOffering() {}\n\n"+
		blockingTwo+"\nfunc Enroll() {}\n")
	f.Write(about, f.Read(about)+"\n## Review note\n\nThe concurrency story needs to be explicit.\n")
	f.WriteChangesetFile(slug, "concurrency-tests.md",
		"# Concurrency tests\n\nI'm not convinced the test makes the two transactions overlap.\n")
	submit(t, f, "block")

	status := runIn(t, f.Dir(), "status", "--json").json(t)
	if status["state"] != "BLOCKED" {
		t.Fatalf("state = %v, want BLOCKED after a blocking review", status["state"])
	}
	latest, ok := status["latest_review"].(map[string]any)
	if !ok || latest["outcome"] != "block" {
		t.Fatalf("latest_review = %v, want the blocking review", status["latest_review"])
	}
	if rows := runIn(t, f.Dir(), "queue", "--json").jsonList(t, "ready_for_review"); len(rows) != 0 {
		t.Errorf("a blocked changeset is still queued: %v", rows)
	}

	// The response-oriented span is empty while HEAD is the review commit itself.
	firstReviewSpan := runIn(t, f.Dir(), "diff", "--unreviewed").mustSucceed(t, "diff", "--unreviewed")
	if strings.TrimSpace(firstReviewSpan.stdout) != "" {
		t.Errorf("the span after the review should be empty before any response:\n%s", firstReviewSpan.stdout)
	}

	// --- author: address ONE comment, leave the other, commit ------------------
	f.Commit("author: wrap offering creation in a transaction", gittest.WithFile("service.go",
		"package main\n\nfunc CreateOffering() { transaction() }\n\n"+blockingTwo+"\nfunc Enroll() {}\n"))

	// --- author: git pair change ready must refuse and name the survivor ----------
	res := runIn(t, f.Dir(), "change", "ready")
	if res.code != exitRefusal {
		t.Fatalf("change ready exited %d, want %d\nstdout: %s\nstderr: %s",
			res.code, exitRefusal, res.stdout, res.stderr)
	}
	mustContain(t, res.stderr, "service.go", "the refusal must name the file")
	mustContain(t, res.stderr, blockingTwo, "the refusal must print the surviving addition")
	mustContain(t, res.stderr, "1 addition from review", "the refusal must count the survivors")
	mustContain(t, res.stderr, "git pair change ready --allow-surviving-review-additions",
		"the refusal must print the override")
	if got := f.Subject(f.Head()); strings.HasPrefix(got, "git-pair: ready") {
		t.Error("a ready marker was created despite the surviving addition")
	}
	if rows := runIn(t, f.Dir(), "queue", "--json").jsonList(t, "ready_for_review"); len(rows) != 0 {
		t.Errorf("the refused changeset is queued: %v", rows)
	}

	// The removal of the *resolved* comment is visible in the response span, which is
	// what makes resolution explicit (PRD §17.2).
	span := runIn(t, f.Dir(), "diff", "--unreviewed").mustSucceed(t, "diff", "--unreviewed")
	if !strings.Contains(span.stdout, "-"+blockingOne) {
		t.Errorf("the response span must show the resolved comment as removed:\n%s", span.stdout)
	}

	// --- author: consciously resolve the survivor, then ready again ------------
	f.Commit("author: synchronise both transactions before the capacity read",
		gittest.WithFile("service.go", "package main\n\nfunc CreateOffering() { transaction() }\n\nfunc Enroll() { transaction() }\n"),
		gittest.WithFile(filepath.Join("changesets", slug, "concurrency-tests.md"),
			"# Concurrency tests\n\nI'm not convinced the test makes the two transactions overlap.\n\n"+
				"## Response\n\nBoth transactions now block immediately before the capacity read.\n"))
	ready(t, f)

	// The reviewer's thread text survives (answering a thread by appending keeps the
	// question), and that alone must not block readiness (plan D3).
	if got := f.Subject(f.Head()); got != "git-pair: ready "+slug {
		t.Fatalf("HEAD = %q, want the ready marker", got)
	}

	// --- reviewer: see the response, approve -----------------------------------
	secondSpan := runIn(t, f.Dir(), "diff", "--unreviewed").mustSucceed(t, "diff", "--unreviewed")
	if !strings.Contains(secondSpan.stdout, "## Response") {
		t.Errorf("the reviewer's unreviewed span must contain the author's thread response:\n%s", secondSpan.stdout)
	}
	// The TUI needs a terminal; the CLI equivalent must be available (PRD §15).
	if refused := runIn(t, f.Dir(), "review", "open", "--unreviewed"); refused.code != exitUsage {
		t.Errorf("review open without a terminal exited %d, want %d", refused.code, exitUsage)
	}
	submit(t, f, "approve")
	if got := runIn(t, f.Dir(), "status", "--json").json(t)["state"]; got != "APPROVED" {
		t.Fatalf("state = %v, want APPROVED", got)
	}

	// --- the gate, before anything is written -------------------------------------
	// `check` is what a merge runs, and it needs no durable ref to answer: the approval is a commit,
	// and the content it spoke about is the content HEAD carries.
	check := runIn(t, f.Dir(), "check").mustSucceed(t, "check")
	mustContain(t, check.stdout, "OK: "+slug+" is integration-ready", "the gate clears the work")

	// Nothing has been written to `refs/git-pair` through the whole lifecycle so far: init, the
	// implementation, two ready markers, a blocking review, a response, an approval. The refs are what
	// landing records, which is the property this workflow exists to pin.
	if got := durableRefs(t, f); len(got) != 0 {
		t.Fatalf("durable refs before any landing: %v", got)
	}

	// --- author: land it, then record it ------------------------------------------
	// The chain the record must preserve: everything committed up to the approval, including the
	// blocking review and the response to it — the history a squash would destroy.
	chain := f.RevList("HEAD")
	reviewed := f.Head()

	// The history is the noisy-but-honest lifecycle PRD §2.3 describes, in order. Read before the
	// landing, because the landing is a commit on the integration branch rather than on this one.
	want := []string{
		"git-pair: initialize changeset " + slug,
		"implement booking transaction locking",
		"git-pair: ready " + slug,
		"review: block " + slug,
		"author: wrap offering creation in a transaction",
		"author: synchronise both transactions before the capacity read",
		"git-pair: ready " + slug,
		"review: approve " + slug,
	}
	if got := f.SubjectsAbove("main", "HEAD"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("commit sequence =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	f.SwitchTo("main")
	// Squash-merged: the reviewed content arrives on main as one commit, with none of the ancestry
	// that made it. The changeset directory is committed content, so it arrives with the work.
	f.MustGit("checkout", reviewed, "--", filepath.Join("changesets", slug))
	landing := f.Commit(slug+": land the reviewed work", gittest.WithFile("landed.md", "landed\n"))

	recorded := runIn(t, f.Dir(), "integration", "record", "--source", reviewed, "--commit", landing,
		"--target", "main").mustSucceed(t, "integration", "record")
	mustContain(t, recorded.stdout, archiveRef(slug), "the record must print the archive ref it wrote")
	mustContain(t, recorded.stdout, integrationRef(slug), "and the integration ref")
	mustContain(t, recorded.stdout, "reachable from main", "and the reachability it verified")

	if got := runIn(t, f.Dir(), "status", "--changeset", slug, "--json").json(t)["integrated"]; got != true {
		t.Errorf("integrated = %v, want true: the record is how a landing is known", got)
	}
	if got := f.RefSHA(archiveRef(slug)); got != reviewed {
		t.Errorf("archive = %s, want the reviewed head %s", got, reviewed)
	}
	if got := f.RefSHA(integrationRef(slug)); got != landing {
		t.Errorf("integration record = %s, want the landing %s", got, landing)
	}
	// Recording changed no lifecycle state: landing is not a marker, and a state value for it would put
	// a derived fact inside the machine that markers move.
	if got := runIn(t, f.Dir(), "status", "--changeset", slug, "--json").json(t)["state"]; got != "APPROVED" {
		t.Errorf("state = %v, want APPROVED", got)
	}

	// The archival promise: after the branch is gone, the complete unsquashed chain is still
	// reachable — through the record, which is the only thing that was ever written to hold it. And
	// the landing is the only thing main gained.
	f.ForceDeleteBranch(slug)
	if got := f.RevParse("main"); got != landing {
		t.Errorf("main = %s, want the landing commit %s", got, landing)
	}
	if got := f.RevListCount(mainBefore + "..main"); got != 1 {
		t.Errorf("main gained %d commits from this changeset, want the one landing commit", got)
	}
	reachable := map[string]bool{}
	for _, sha := range f.RevList(archiveRef(slug)) {
		reachable[sha] = true
	}
	for _, sha := range chain {
		if !reachable[sha] {
			t.Errorf("%s is not reachable from %s after `git branch -D`", sha, archiveRef(slug))
		}
	}
	// The reviewed head itself — the commit the approval names — is reachable, which is what a later
	// reader follows to see what was reviewed and what the author answered.
	if !f.ReachableFrom(reviewed, archiveRef(slug)) {
		t.Errorf("the reviewed head %s is not reachable from %s", reviewed, archiveRef(slug))
	}

	// And the record is idempotent, because the pipeline that wrote it may run again.
	again := runIn(t, f.Dir(), "integration", "record", "--source", reviewed, "--commit", landing,
		"--target", "main").mustSucceed(t, "integration", "record")
	mustContain(t, again.stdout, "already recorded", "a retry says so rather than failing or pretending")
	if got := durableRefs(t, f); len(got) != 2 {
		t.Errorf("durable refs at the end = %v, want exactly the pair", got)
	}
}
