package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitpr/internal/gittest"
)

// --- review submit (PRD §10.4) ----------------------------------------------

func TestReviewSubmitWritesReviewCommitAndMovesRef(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	before := f.Head()

	const comment = "// Please use a transaction here"
	f.Write("service.go", "package main\n\n"+comment+"\nfunc Lock() {}\n")

	submit(t, f, "block")

	head := f.Head()
	if head == before {
		t.Fatal("review submit created no commit")
	}
	if got := f.Subject(head); got != "review: block "+slug {
		t.Errorf("subject = %q, want %q (PRD §10.4)", got, "review: block "+slug)
	}
	trailers := f.Trailers(head)
	if trailers["GitPR-Outcome"] != "block" {
		t.Errorf("GitPR-Outcome = %q, want block", trailers["GitPR-Outcome"])
	}
	if trailers["GitPR-Changeset"] != slug {
		t.Errorf("GitPR-Changeset = %q, want %q", trailers["GitPR-Changeset"], slug)
	}
	// PRD §10.4: "Immediately after successful review submission, update the
	// changeset's review archive ref to the resulting exact HEAD."
	if got := f.RefSHA(reviewRef(slug)); got != head {
		t.Errorf("%s = %s, want the review commit %s", reviewRef(slug), got, head)
	}
	// The reviewer's direct edit is part of the review (PRD §20).
	if changed := f.ChangedFiles(before, head); len(changed) != 1 || changed[0] != "service.go" {
		t.Errorf("the review changed %v, want [service.go]", changed)
	}
	if !f.Clean() {
		t.Error("the review submission left the working tree dirty")
	}

	status := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json").json(t)
	if status["state"] != "BLOCKED" {
		t.Errorf("state = %v, want BLOCKED", status["state"])
	}
	latest, ok := status["latest_review"].(map[string]any)
	if !ok {
		t.Fatalf("latest_review = %v, want an object", status["latest_review"])
	}
	if latest["outcome"] != "block" || latest["index"] != float64(0) {
		t.Errorf("latest_review = %v, want index 0 and outcome block", latest)
	}
}

// PRD §10.4: "Approval must support a clean working tree and therefore may create an
// empty Git commit." The ref must still move to that exact HEAD.
func TestReviewSubmitApproveOnCleanTreeCreatesEmptyReviewCommit(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)
	before := f.Head()

	res := submit(t, f, "approve")

	head := f.Head()
	if head == before {
		t.Fatal("approval created no commit on a clean tree")
	}
	if changed := f.ChangedFiles(before, head); len(changed) != 0 {
		t.Errorf("the approval changed %v, want an empty commit", changed)
	}
	if got := f.Subject(head); got != "review: approve booking" {
		t.Errorf("subject = %q, want %q", got, "review: approve booking")
	}
	if got := f.Trailers(head)["GitPR-Outcome"]; got != "approve" {
		t.Errorf("GitPR-Outcome = %q, want approve", got)
	}
	if got := f.RefSHA(reviewRef("booking")); got != head {
		t.Errorf("%s = %s, want the empty approval %s", reviewRef("booking"), got, head)
	}
	if !strings.Contains(res.stdout, "none") && !strings.Contains(res.stdout, "0") {
		t.Logf("note: human output for an empty review: %q", res.stdout)
	}
	status := runIn(t, f.Dir(), "status", "--json").json(t)
	if status["state"] != "APPROVED" {
		t.Errorf("state = %v, want APPROVED", status["state"])
	}
}

// Exactly one outcome is a valid submission.
func TestReviewSubmitOutcomeArguments(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")

	res := runIn(t, f.Dir(), "review", "submit")
	if res.code != exitUsage {
		t.Errorf("submit with no outcome exited %d, want %d\nstderr: %s", res.code, exitUsage, res.stderr)
	}

	res = runIn(t, f.Dir(), "review", "submit", "--block", "--approve")
	if res.code != exitUsage {
		t.Errorf("submit with two outcomes exited %d, want %d\nstderr: %s", res.code, exitUsage, res.stderr)
	}
	if f.RefNames("refs/reviews") != nil {
		t.Error("a refused submission moved a review ref")
	}
}

// `--no-stage` is for reviewers who stage deliberately: their unstaged work must stay
// out of the review commit.
func TestReviewSubmitNoStageLeavesEditsOut(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	f.Write("service.go", "package main\n\n// not staged\nfunc Lock() {}\n")
	before := f.Head()

	runIn(t, f.Dir(), "review", "submit", "--feedback", "--no-stage").
		mustSucceed(t, "review", "submit", "--feedback", "--no-stage")

	if changed := f.ChangedFiles(before, f.Head()); len(changed) != 0 {
		t.Errorf("--no-stage committed %v, want an empty review", changed)
	}
	if f.Clean() {
		t.Error("--no-stage consumed the reviewer's unstaged edit")
	}
	if got := f.RefSHA(reviewRef(slug)); got != f.Head() {
		t.Errorf("review ref = %s, want the new HEAD %s", got, f.Head())
	}
}

// --- review history (PRD §10.5) ---------------------------------------------

// "Only commits explicitly identified as GitPR review submissions count as reviews.
// Ordinary Git commits do not." The index is chronological from 0.
func TestReviewHistoryListsOnlyReviewSubmissionsChronologically(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)

	f.Write("service.go", "package main\n\n// first\nfunc Lock() {}\n")
	first := submitJSON(t, f, "block")
	f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))
	f.Commit("review: block booking", gittest.WithFile("notes.md", "an ordinary commit with a review-looking subject\n"))
	second := submitJSON(t, f, "feedback")
	f.Commit("author response 2", gittest.WithFile("handler.go", "package main\n\nfunc Serve() { ctx() }\n"))
	third := submitJSON(t, f, "approve")

	res := runIn(t, f.Dir(), "review", "history", "--json").mustSucceed(t, "review", "history", "--json")
	reviews := res.jsonList(t, "reviews")
	if len(reviews) != 3 {
		t.Fatalf("reviews = %d (%v), want exactly the three review submissions", len(reviews), reviews)
	}
	wantOutcomes := []string{"block", "feedback", "approve"}
	wantSHAs := []string{first["commit"].(string), second["commit"].(string), third["commit"].(string)}
	for i, entry := range reviews {
		row, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("review %d = %T, want an object", i, entry)
		}
		if row["index"] != float64(i) {
			t.Errorf("reviews[%d].index = %v, want %d (PRD §10.5: chronological from 0)", i, row["index"], i)
		}
		if row["outcome"] != wantOutcomes[i] {
			t.Errorf("reviews[%d].outcome = %v, want %s", i, row["outcome"], wantOutcomes[i])
		}
		if row["sha"] != wantSHAs[i] && row["short"] != shortOf(wantSHAs[i]) {
			t.Errorf("reviews[%d] = %v, want the review commit %s", i, row, wantSHAs[i])
		}
		if _, ok := row["age"]; !ok {
			t.Errorf("reviews[%d] has no age key: %v", i, row)
		}
	}
	if res.json(t)["changeset"] != slug {
		t.Errorf("changeset = %v, want %q", res.json(t)["changeset"], slug)
	}

	// -1 addresses the most recent review, which `--since-review` uses.
	span := runIn(t, f.Dir(), "diff", "--since-review=-1", "--stat").mustSucceed(t, "diff", "--since-review=-1", "--stat")
	mustContain(t, span.stderr, "after review 2", "the span label must name the resolved review index")
}

func TestReviewHistoryWithNoReviews(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")

	res := runIn(t, f.Dir(), "review", "history", "--json").mustSucceed(t, "review", "history", "--json")
	if reviews := res.jsonList(t, "reviews"); len(reviews) != 0 {
		t.Errorf("reviews = %v, want empty", reviews)
	}
	if res.json(t)["changeset"] != slug {
		t.Errorf("changeset = %v, want %q", res.json(t)["changeset"], slug)
	}
}

// --- review queue (PRD §10.6) ------------------------------------------------

// A ready marker is what puts a changeset in the queue, and the entry must carry the
// fields PRD §10.6 shows: base, ready age, head.
func TestReviewQueueReportsReadyChangesetFields(t *testing.T) {
	f, slug := newChangeset(t, "feature/booking-transaction", "main")
	ready(t, f)
	head := f.Head()

	res := runIn(t, f.Dir(), "review", "queue", "--json").mustSucceed(t, "review", "queue", "--json")
	rows := res.jsonList(t, "ready_for_review")
	if len(rows) != 1 {
		t.Fatalf("ready_for_review = %v, want exactly one entry", rows)
	}
	entry, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("queue entry = %T, want an object", rows[0])
	}
	for _, key := range []string{"changeset", "branch", "base", "state", "head", "ready_commit", "ready_age", "review_ref"} {
		if _, ok := entry[key]; !ok {
			t.Errorf("queue entry is missing %q: %v", key, entry)
		}
	}
	if entry["changeset"] != slug {
		t.Errorf("changeset = %v, want %q", entry["changeset"], slug)
	}
	if entry["branch"] != "feature/booking-transaction" {
		t.Errorf("branch = %v, want feature/booking-transaction", entry["branch"])
	}
	if entry["base"] != "main" {
		t.Errorf("base = %v, want main", entry["base"])
	}
	if entry["state"] != "READY" {
		t.Errorf("state = %v, want READY", entry["state"])
	}
	if entry["head"] != head {
		t.Errorf("head = %v, want the ready marker %s", entry["head"], head)
	}
	if entry["review_ref"] != reviewRef(slug) {
		t.Errorf("review_ref = %v, want %s", entry["review_ref"], reviewRef(slug))
	}
	if age, ok := entry["ready_age"].(string); !ok || age == "" {
		t.Errorf("ready_age = %v, want a compact duration (PRD §10.6 shows \"18m\")", entry["ready_age"])
	}

	human := runIn(t, f.Dir(), "review", "queue").mustSucceed(t, "review", "queue")
	mustContain(t, human.stdout, "READY FOR REVIEW", "human output")
	mustContain(t, human.stdout, slug, "human output must name the changeset")
	mustContain(t, human.stdout, "base: main", "human output must show the base (PRD §10.6)")
}

// PRD §12's safety property, seen through the queue: a ready marker puts a changeset
// in the queue and a later implementation commit takes it back out.
func TestReviewQueueGainsAndLosesChangesetAcrossReadyAndImplementation(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")

	if queueListsChangeset(t, runIn(t, f.Dir(), "review", "queue", "--json"), slug) {
		t.Fatal("an unready changeset is already in the queue")
	}

	ready(t, f)
	if !queueListsChangeset(t, runIn(t, f.Dir(), "review", "queue", "--json"), slug) {
		t.Fatal("the changeset is missing from the queue after `change ready`")
	}

	// "Any later implementation commit makes the previous ready marker stale and
	// returns the effective state to working" (PRD §9.2).
	f.Commit("agent: one more change", gittest.WithFile("service.go", "package main\n\nfunc Lock() { retry() }\n"))

	res := runIn(t, f.Dir(), "review", "queue", "--json").mustSucceed(t, "review", "queue", "--json")
	if queueListsChangeset(t, res, slug) {
		t.Errorf("the changeset is still queued after an implementation commit:\n%s", res.stdout)
	}
	status := runIn(t, f.Dir(), "status", "--json").json(t)
	if status["state"] != "WORKING" {
		t.Errorf("state = %v, want WORKING", status["state"])
	}
	// The stale marker is still history, not a lie about the present.
	mustContain(t, status["reason"].(string), "code changed since ready", "status must explain why the marker is stale")

	// Marking ready again re-queues it.
	ready(t, f)
	if !queueListsChangeset(t, runIn(t, f.Dir(), "review", "queue", "--json"), slug) {
		t.Error("the changeset did not return to the queue after a new ready marker")
	}
}

// Only the READY state belongs in the queue (PRD §10.6 "changesets currently ready for
// human review").
func TestReviewQueueExcludesOtherStates(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)

	cases := []struct {
		name  string
		act   func()
		state string
	}{
		{"blocked", func() { submit(t, f, "block") }, "BLOCKED"},
		{"working after a response", func() {
			f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { transaction() }\n"))
		}, "WORKING"},
		{"feedback", func() { ready(t, f); submit(t, f, "feedback") }, "FEEDBACK"},
		{"approved", func() { submit(t, f, "approve") }, "APPROVED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.act()
			if got := runIn(t, f.Dir(), "status", "--json").json(t)["state"]; got != tc.state {
				t.Fatalf("state = %v, want %s", got, tc.state)
			}
			if queueListsChangeset(t, runIn(t, f.Dir(), "review", "queue", "--json"), slug) {
				t.Errorf("a %s changeset is in the queue", tc.state)
			}
		})
	}
}

// The plan requires a deterministic queue order: longest waiting first. Two
// changesets on their own branches, with back-dated ready markers, make the
// ordering observable without depending on wall-clock timing.
func TestReviewQueueOrdersLongestWaitingFirst(t *testing.T) {
	f := newRepo(t)

	f.CreateBranch("aaa-older")
	f.CommitChangeset("aaa-older", "main")
	f.Commit("implement aaa", gittest.WithFile("aaa.go", "package main\n\nfunc A() {}\n"))
	// Branched from aaa-older so both changeset directories exist in one working
	// tree, which is what `review queue` enumerates.
	f.CreateBranch("zzz-newer", "aaa-older")
	f.CommitChangeset("zzz-newer", "main")
	f.Commit("implement zzz", gittest.WithFile("zzz.go", "package main\n\nfunc Z() {}\n"))

	f.SwitchTo("aaa-older")
	f.CommitReadyMarker("aaa-older", gittest.WithDate(time.Now().Add(-3*time.Hour)))
	f.SwitchTo("zzz-newer")
	f.CommitReadyMarker("zzz-newer", gittest.WithDate(time.Now().Add(-time.Minute)))
	if !f.Clean() {
		t.Fatal("fixture left changes")
	}

	res := runIn(t, f.Dir(), "review", "queue", "--json").mustSucceed(t, "review", "queue", "--json")
	rows := res.jsonList(t, "ready_for_review")
	if len(rows) != 2 {
		t.Fatalf("ready_for_review = %v, want both ready changesets:\n%s", rows, res.stdout)
	}
	first := rows[0].(map[string]any)["changeset"]
	second := rows[1].(map[string]any)["changeset"]
	if first != "aaa-older" || second != "zzz-newer" {
		t.Errorf("queue order = [%v %v], want the 3h-old changeset before the 1m-old one", first, second)
	}
}

// --- editor-backed commands (PRD §10.2, §10.3, §15) -------------------------

// With no terminal available, opening an editor must refuse rather than hang or
// silently do nothing — and it must not damage the document it was going to open.
func TestReviewAboutRefusesWithoutTerminalAndKeepsAbout(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	aboutPath := filepath.Join("changesets", slug, "ABOUT.md")
	authorText := "# booking\n\n## Summary\n\nThe author's description.\n"
	f.Write(aboutPath, authorText)
	f.Commit("describe the change")

	res := runIn(t, f.Dir(), "review", "about")
	if res.code == exitOK {
		t.Error("`review about` reported success without opening an editor")
	}
	if got := f.Read(aboutPath); got != authorText {
		t.Errorf("ABOUT.md was modified:\n%s", got)
	}
}

// PRD §10.3: the title is slugified into a thread file, and an existing thread for the
// same title is reopened instead of duplicated.
func TestReviewThreadSlugifiesAndDeduplicates(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")

	res := runIn(t, f.Dir(), "review", "thread", "Concurrency Tests")
	if res.code == exitOK {
		t.Error("`review thread` reported success without opening an editor")
	}
	path := filepath.Join("changesets", slug, "concurrency-tests.md")
	if !f.HasWorktreeFile(path) {
		t.Fatalf("thread file %s was not created; stderr: %s", path, res.stderr)
	}
	if !strings.HasPrefix(f.Read(path), "# Concurrency Tests") {
		t.Errorf("thread content = %q, want it to start with the title", f.Read(path))
	}

	// The reviewer writes in the thread, then asks for the same title again.
	reviewerText := "# Concurrency Tests\n\nI'm not convinced these overlap.\n"
	f.Write(path, reviewerText)
	runIn(t, f.Dir(), "review", "thread", "concurrency tests")

	if got := f.Read(path); got != reviewerText {
		t.Errorf("reopening a thread overwrote it:\n%s", got)
	}
	var duplicates []string
	for _, name := range f.Threads(slug) {
		if strings.HasPrefix(name, "concurrency") {
			duplicates = append(duplicates, name)
		}
	}
	if len(duplicates) != 1 {
		t.Errorf("thread files = %v, want a single concurrency-tests.md (PRD §10.3)", duplicates)
	}
}

func TestReviewThreadWithoutTitleIsUsageError(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")

	res := runIn(t, f.Dir(), "review", "thread")
	if res.code != exitUsage {
		t.Errorf("thread with no title and no terminal exited %d, want %d\nstderr: %s",
			res.code, exitUsage, res.stderr)
	}
}

// PRD §14's TUI needs a terminal; the CLI must say so and point at the
// non-interactive equivalents instead of hanging an agent.
func TestReviewOpenRefusesWithoutTerminal(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")

	for _, args := range [][]string{{"review", "open"}, {"review", "open", "--unreviewed"}} {
		res := runIn(t, f.Dir(), args...)
		if res.code != exitUsage {
			t.Errorf("gitpr %v exited %d, want %d\nstderr: %s", args, res.code, exitUsage, res.stderr)
		}
		mustContain(t, res.stderr, "gitpr diff", "the refusal must name the non-interactive equivalents")
	}
}

// --- helpers ----------------------------------------------------------------

// submitJSON submits a review with --json so the commit SHA can be asserted.
func submitJSON(t *testing.T, f *gittest.Fixture, outcome string) map[string]any {
	t.Helper()
	args := []string{"review", "submit", "--" + outcome, "--json"}
	return runIn(t, f.Dir(), args...).mustSucceed(t, args...).json(t)
}

// readyAt marks the current changeset ready with a back-dated marker commit, so queue
// ordering can be asserted deterministically.
func readyAt(t *testing.T, f *gittest.Fixture, slug string, ago time.Duration) {
	t.Helper()
	f.CommitReadyMarker(slug, gittest.WithDate(time.Now().Add(-ago)))
}

func shortOf(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// --- review reopen ----------------------------------------------------------

// PRD §17.2 keeps `review open` on the full changeset; reopen is the named way to
// come back to what the author changed since a submission. Both refusals have to
// say which command does work.
func TestReviewReopenWithoutReviews(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)

	got := runIn(t, f.Dir(), "review", "reopen")
	if got.code != exitUsage {
		t.Errorf("reopen with no reviews exited %d, want %d\n%s", got.code, exitUsage, got.stderr)
	}
	mustContain(t, got.stderr, "no review submissions", "should say why there is nothing to reopen onto")
	mustContain(t, got.stderr, "gitpr review open", "should name the command that works")
}

// The author answering a review is the whole point of the command, so the span must
// be measured from the submission, not from the changeset base.
func TestReviewReopenAfterTheAuthorResponds(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "block")
	f.Commit("author: use a transaction", gittest.WithFile("service.go",
		"package main\n\nfunc Lock() { tx() }\n"))

	// Both guards passed, so this reaches the TUI, which the harness has no
	// terminal for. Anything else would mean the span looked empty to reopen.
	got := runIn(t, f.Dir(), "review", "reopen")
	if got.code != exitUsage || !strings.Contains(got.stderr, "needs a terminal") {
		t.Errorf("reopen exited %d (%s), want the terminal refusal", got.code, got.stderr)
	}
}

// The review being the newest commit is the state a reviewer actually types `reopen`
// in: they submitted, the session closed, and they want to get back to that review.
// Nothing has landed since the submission, so the span is empty — and it is still the
// session they came back to, because an empty span is the honest answer and their own
// uncommitted notes show up in the preview.
func TestReviewReopenReachesTheSessionWhenNothingHasLanded(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "feedback")

	// Past both guards and into the TUI, which the harness has no terminal for. A
	// refusal here would mean an empty span was treated as an error.
	got := runIn(t, f.Dir(), "review", "reopen")
	if got.code != exitUsage || !strings.Contains(got.stderr, "needs a terminal") {
		t.Errorf("reopen exited %d (%s), want the terminal refusal", got.code, got.stderr)
	}
	mustContain(t, got.stderr, "needs a terminal", "reopen should reach the session, not refuse")
}

// There is no `review undo`: a submission is corrected by submitting again, because
// state is derived from commit trailers and gitpr does not rewrite history. The
// supersession has to be visible at the moment it happens.
func TestSecondSubmissionSupersedesTheFirst(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)

	first := submitJSON(t, f, "feedback")
	if prev, ok := first["previous_review"].(string); !ok || prev != "" {
		t.Errorf("first submit reported previous_review = %#v, want the empty string", first["previous_review"])
	}

	second := runIn(t, f.Dir(), "review", "submit", "--approve", "--json").
		mustSucceed(t, "review", "submit", "--approve", "--json").json(t)
	prev, _ := second["previous_review"].(string)
	if prev == "" {
		t.Fatal("second submit reported no previous review")
	}
	if got := runIn(t, f.Dir(), "status").mustSucceed(t, "status").stdout; !strings.Contains(got, "State: APPROVED") {
		t.Errorf("state after the correcting submission is not APPROVED\n%s", got)
	}
	if got := runIn(t, f.Dir(), "review", "history").mustSucceed(t, "review", "history").stdout; strings.Count(got, "review:") != 2 {
		t.Errorf("history lost the superseded submission\n%s", got)
	}

	human := runIn(t, f.Dir(), "review", "submit", "--block", "-m", "one more").
		mustSucceed(t, "review", "submit", "--block").stdout
	prevShort, _ := second["short"].(string)
	mustContain(t, human, "supersedes: review "+prevShort+" (approve)",
		"the human summary should name the submission it supersedes")
}
