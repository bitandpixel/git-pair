package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitpair/internal/changeset"
	"gitpair/internal/gittest"
)

// --- review submit (PRD §10.4) ----------------------------------------------

func TestReviewSubmitWritesReviewCommit(t *testing.T) {
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
	if trailers["Review-Outcome"] != "block" {
		t.Errorf("Review-Outcome = %q, want block", trailers["Review-Outcome"])
	}
	if trailers["Review-Changeset"] != slug {
		t.Errorf("Review-Changeset = %q, want %q", trailers["Review-Changeset"], slug)
	}
	// The submission writes a commit and nothing else. PRD §10.4 used to add "immediately after a
	// successful submission, update the changeset's review archive ref to the resulting exact HEAD";
	// the ref was a pointer that had to be maintained by four commands to stay where HEAD already was,
	// and the durable refs are now written once, at landing.
	if refs := durableRefs(t, f); len(refs) != 0 {
		t.Errorf("review submit wrote %v for %s; the submission is the commit", refs, slug)
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
	if got := f.Trailers(head)["Review-Outcome"]; got != "approve" {
		t.Errorf("Review-Outcome = %q, want approve", got)
	}
	// An empty commit is still only a commit: no ref accompanies it.
	if refs := f.RefNames("refs/git-pair"); len(refs) != 0 {
		t.Errorf("an empty approval wrote %v", refs)
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
	if f.RefNames("refs/git-pair") != nil {
		t.Error("a refused submission wrote a durable ref")
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
	if refs := durableRefs(t, f); len(refs) != 0 {
		t.Errorf("--no-stage submission wrote %v for %s", refs, slug)
	}
}

// --- review history (PRD §10.5) ---------------------------------------------

// "Only commits explicitly identified as review marker commits count as reviews.
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
	mustContain(t, span.stderr, "last review..current",
		"the label must name the span in the spelling that was chosen: -1 is the newest review")
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

// A changeset can be read by more than one person, and the only durable record of who
// submitted which verdict is the author of the review commit. `review history` prints it as
// REVIEWER and reports it in `--json` as `reviewer`: on a submission the commit author is the
// reviewer, while "author" means the person who wrote the change everywhere else in git-pair.
func TestReviewHistoryNamesTheReviewer(t *testing.T) {
	f, _ := newChangeset(t, "booking-transaction", "main")
	ready(t, f)

	submitAs(t, f, "Rae", "block")
	submitAs(t, f, "Nils", "approve")

	res := runIn(t, f.Dir(), "review", "history", "--json").mustSucceed(t, "review", "history", "--json")
	rows := res.jsonList(t, "reviews")
	if len(rows) != 2 {
		t.Fatalf("reviews = %v, want the two submissions", rows)
	}
	want := []string{"Rae", "Nils"}
	for i, entry := range rows {
		row, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("review %d = %T, want an object", i, entry)
		}
		if row["reviewer"] != want[i] {
			t.Errorf("reviews[%d].reviewer = %v, want %s", i, row["reviewer"], want[i])
		}
		if _, ok := row["author"]; ok {
			t.Errorf("reviews[%d] still carries the retired `author` key: %v", i, row)
		}
	}

	text := runIn(t, f.Dir(), "review", "history").mustSucceed(t, "review", "history").stdout
	mustContain(t, text, "REVIEWER", "the column header")
	for _, name := range want {
		mustContain(t, text, name, "the reviewer who made that submission")
	}
	// The fixture's own identity is the changeset author's, and none of their commits is a
	// review: the column that appears here is the reviewer's, not a fallback to the branch.
	mustNotContain(t, text, gittest.AuthorName, "the history table")
}

// --- queue (PRD §10.6) ------------------------------------------------

// A ready marker is what puts a changeset in the queue, and the entry must carry the
// fields PRD §10.6 shows: base, ready age, head.
func TestReviewQueueReportsReadyChangesetFields(t *testing.T) {
	f, slug := newChangeset(t, "feature/booking-transaction", "main")
	ready(t, f)
	head := f.Head()

	res := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	rows := res.jsonList(t, "ready_for_review")
	if len(rows) != 1 {
		t.Fatalf("ready_for_review = %v, want exactly one entry", rows)
	}
	entry, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("queue entry = %T, want an object", rows[0])
	}
	for _, key := range []string{"changeset", "branch", "base", "state", "head", "ready_commit", "ready_age"} {
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
	// No archive_ref: the queue is the list of work awaiting review, and the refs are written at
	// landing — a changeset with a record is not in the queue at all, it is named in the skip note.
	if age, ok := entry["ready_age"].(string); !ok || age == "" {
		t.Errorf("ready_age = %v, want a compact duration (PRD §10.6 shows \"18m\")", entry["ready_age"])
	}

	human := runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	mustContain(t, human.stdout, "READY FOR REVIEW", "human output")
	mustContain(t, human.stdout, slug, "human output must name the changeset")
	mustContain(t, human.stdout, "base: main", "human output must show the base (PRD §10.6)")

	// A changeset a branch owns is not an orphan, and the directory it left in the
	// tree must not be classified a second time as one.
	if skipped := res.jsonList(t, "skipped"); len(skipped) != 0 {
		t.Errorf("skipped = %v, want the empty array: nothing to say about a changeset the queue just listed", skipped)
	}
}

// Queue membership follows the markers, not the working branch: `change ready` puts a
// changeset in, an implementation commit leaves it there, and `change unready` takes it
// out (PRD §12). The reason counts what arrived since the offer, so a reviewer can see the
// branch has moved without the state pretending otherwise.
func TestReviewQueueGainsAndLosesChangesetAcrossReadyAndUnready(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")

	if queueListsChangeset(t, runIn(t, f.Dir(), "queue", "--json"), slug) {
		t.Fatal("an unready changeset is already in the queue")
	}

	ready(t, f)
	if !queueListsChangeset(t, runIn(t, f.Dir(), "queue", "--json"), slug) {
		t.Fatal("the changeset is missing from the queue after `change ready`")
	}

	f.Commit("agent: one more change", gittest.WithFile("service.go", "package main\n\nfunc Lock() { retry() }\n"))

	res := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	if !queueListsChangeset(t, res, slug) {
		t.Errorf("an implementation commit took the changeset out of the queue; only a command moves state:\n%s", res.stdout)
	}
	status := runIn(t, f.Dir(), "status", "--json").json(t)
	if status["state"] != "READY" {
		t.Errorf("state = %v, want READY", status["state"])
	}
	// The queue does not hide the drift; it just refuses to infer a state from it.
	mustContain(t, status["reason"].(string), "1 commit since", "status must show the branch has moved since the offer")

	runIn(t, f.Dir(), "change", "unready").mustSucceed(t, "change", "unready")
	if queueListsChangeset(t, runIn(t, f.Dir(), "queue", "--json"), slug) {
		t.Error("the changeset is still queued after `change unready`")
	}

	// Marking ready again re-queues it.
	ready(t, f)
	if !queueListsChangeset(t, runIn(t, f.Dir(), "queue", "--json"), slug) {
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
		{"blocked, then an author response", func() {
			// The fix is a commit, not a marker, so the block is still the newest marker.
			f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { transaction() }\n"))
		}, "BLOCKED"},
		{"feedback", func() { ready(t, f); submit(t, f, "feedback") }, "FEEDBACK"},
		{"approved", func() { submit(t, f, "approve") }, "APPROVED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.act()
			if got := runIn(t, f.Dir(), "status", "--json").json(t)["state"]; got != tc.state {
				t.Fatalf("state = %v, want %s", got, tc.state)
			}
			if queueListsChangeset(t, runIn(t, f.Dir(), "queue", "--json"), slug) {
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
	// Branched from aaa-older so the two changesets are stacked, which is the
	// ordinary way to end up with two of them at once.
	f.CreateBranch("zzz-newer", "aaa-older")
	f.CommitChangeset("zzz-newer", "aaa-older")
	f.Commit("implement zzz", gittest.WithFile("zzz.go", "package main\n\nfunc Z() {}\n"))

	f.SwitchTo("aaa-older")
	f.CommitReadyMarker("aaa-older", gittest.WithDate(time.Now().Add(-3*time.Hour)))
	f.SwitchTo("zzz-newer")
	f.CommitReadyMarker("zzz-newer", gittest.WithDate(time.Now().Add(-time.Minute)))
	if !f.Clean() {
		t.Fatal("fixture left changes")
	}

	res := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
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

// A branch carrying two changesets is decided by the branch's own history: the changeset whose
// directory this branch touched last is the work in hand. The ordering used to come from the archive
// refs, which on a fresh clone — or on any branch that had never been archived — said nothing about
// either, so a branch that had visibly been working on one of the two was skipped as unresolvable.
func TestReviewQueueOrdersABranchCarryingTwoChangesetsByItsOwnHistory(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("aaa-one")
	f.CommitChangeset("aaa-one", "main")
	f.Commit("implement aaa", gittest.WithFile("aaa.go", "package main\n"))
	f.CreateBranch("bbb-two", "aaa-one")
	f.CommitChangeset("bbb-two", "main")
	f.Commit("implement bbb", gittest.WithFile("bbb.go", "package main\n"))
	// Both offered, so the queue has a reason to list each of them.
	f.SwitchTo("aaa-one")
	f.CommitReadyMarker("aaa-one")
	f.SwitchTo("bbb-two")
	f.CommitReadyMarker("bbb-two")

	f.SwitchTo("main")
	res := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	if skipped := res.jsonList(t, "skipped"); len(skipped) != 0 {
		t.Errorf("skipped = %v, want the empty array: both branches are listed, bbb-two last on its own directory\n%s",
			skipped, res.stdout)
	}
	for _, want := range []string{"aaa-one", "bbb-two"} {
		if !queueListsChangeset(t, res, want) {
			t.Errorf("%s is missing from the queue:\n%s", want, res.stdout)
		}
	}
}

// When the branch's own history cannot tell the two apart — both directories arriving in one commit —
// nothing orders them, and the queue names the branch with the reason rather than guessing at the diff
// base. The queue is where an author looks to find out why a branch will not show up.
func TestReviewQueueNamesABranchItCannotResolve(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("ccc-pair")
	// One commit, two changeset directories: the tie the ordering cannot break. Both are offered, so
	// the branch is one the queue has to explain rather than one it has no reason to mention.
	f.Commit("claim two changesets at once", gittest.WithFiles(map[string]string{
		"changesets/ccc-one/CHANGESET.yaml": "id: ccc-one\nbase: main\n",
		"changesets/ccc-two/CHANGESET.yaml": "id: ccc-two\nbase: main\n",
	}))
	f.CommitReadyMarker("ccc-one")
	f.CommitReadyMarker("ccc-two")

	f.SwitchTo("main")
	res := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	skipped, _ := res.json(t)["skipped"].([]any)
	var found string
	for _, entry := range skipped {
		line, _ := entry.(string)
		if strings.HasPrefix(line, "ccc-pair ") {
			found = line
		}
	}
	if found == "" {
		t.Fatalf("skipped = %v, want ccc-pair named with the reason", skipped)
	}
	if !strings.Contains(found, "more than one changeset") {
		t.Errorf("skipped line = %q, want it to say the branch holds more than one changeset", found)
	}
	for _, want := range []string{"ccc-one", "ccc-two", "--changeset"} {
		if !strings.Contains(found, want) {
			t.Errorf("skipped line %q does not mention %q: the reader has to see both candidates and the way to choose", found, want)
		}
	}
}

// The queue is a property of the repository, not of the checkout. It used to enumerate
// the changesets/ directory on disk, which made it useless exactly when an agent most
// wants it: run from main, it either said nothing or complained about directories whose
// branches had been deleted.
func TestReviewQueueReadsBranchesNotTheWorkingTree(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("aaa-one")
	f.CommitChangeset("aaa-one", "main")
	f.Commit("implement aaa", gittest.WithFile("aaa.go", "package main\n\nfunc A() {}\n"))
	f.CreateBranch("zzz-two", "aaa-one")
	f.CommitChangeset("zzz-two", "aaa-one")
	f.Commit("implement zzz", gittest.WithFile("zzz.go", "package main\n\nfunc Z() {}\n"))

	f.SwitchTo("aaa-one")
	ready(t, f)
	f.SwitchTo("zzz-two")
	ready(t, f)

	// Neither changeset directory exists here.
	f.SwitchTo("main")
	if f.HasWorktreeFile(filepath.Join("changesets", "aaa-one", "CHANGESET.yaml")) {
		t.Fatal("the fixture left a changeset directory on main")
	}

	res := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	rows := res.jsonList(t, "ready_for_review")
	if len(rows) != 2 {
		t.Fatalf("ready_for_review = %v, want both changesets listed from main:\n%s", rows, res.stdout)
	}
	for _, want := range []string{"aaa-one", "zzz-two"} {
		if !queueListsChangeset(t, res, want) {
			t.Errorf("%s missing from the queue run on main", want)
		}
	}
}

// One branch, one row. A changeset can sit on two branches at once — a copy made to try a different
// approach, a parent and the child branched off it — and the two have different heads, different marker
// commits, and often different states. The queue used to collapse them into a single row and print
// whichever branch carried the newest ready marker, which made work on one branch invisible on the
// other. The reviewer's question is per branch, because a review commit is appended to a branch
// (requirements, invariant 5), so the row is too.
func TestReviewQueueKeepsOneRowPerBranchForOneChangeset(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	f.CreateBranch("booking-copy", "booking")
	f.Commit("copy: a different approach", gittest.WithFile("alt.go", "package main\n\nfunc Alt() {}\n"))
	ready(t, f)

	res := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	rows := res.jsonList(t, "ready_for_review")
	if len(rows) != 2 {
		t.Fatalf("ready_for_review = %d rows, want one per branch:\n%s", len(rows), res.stdout)
	}
	byBranch := map[string]map[string]any{}
	for _, r := range rows {
		row, ok := r.(map[string]any)
		if !ok {
			t.Fatalf("queue row = %T, want an object", r)
		}
		if row["changeset"] != slug {
			t.Errorf("row %v names changeset %v, want %q", row, row["changeset"], slug)
		}
		byBranch[row["branch"].(string)] = row
	}
	original, at := byBranch["booking"], byBranch["booking-copy"]
	if original == nil || at == nil {
		t.Fatalf("the queue queued %v, want rows for booking and booking-copy", byBranch)
	}
	// Two rows that pointed at the same head would be a bug wearing invariant 5's clothes. The point of
	// the row is the branch, and these branches disagree about what is ready.
	if original["head"] == at["head"] {
		t.Errorf("both rows name head %v; the copy's implementation commit should have moved it", original["head"])
	}
	if original["ready_commit"] == at["ready_commit"] {
		t.Errorf("both rows name ready_commit %v, want each branch's own marker", original["ready_commit"])
	}

	human := runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	mustContain(t, human.stdout, "branch: booking\n", "the human form says which branch a row is")
	mustContain(t, human.stdout, "branch: booking-copy\n", "and prints both rows")

	// Each branch's own state decides its own row. Blocking the copy does not borrow the original's
	// readiness, and the original does not inherit the block.
	submit(t, f, "block")
	after := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	rows = after.jsonList(t, "ready_for_review")
	if len(rows) != 1 {
		t.Fatalf("after blocking the copy the queue holds %d rows, want the original alone:\n%s", len(rows), after.stdout)
	}
	if got := rows[0].(map[string]any)["branch"]; got != "booking" {
		t.Errorf("the surviving row is branch %v, want booking", got)
	}
}

// A squash-merged changeset whose branch has been deleted is the single most common
// thing the old queue got wrong: it either stayed listed as READY forever or became a
// permanent `note: skipped ... (no branch matches this changeset directory)`. Both are
// wrong, because there is nothing left for a reviewer to do. The anchor outlives the
// branch, so the comparison that proves the work landed can still be made.
func TestReviewQueueIsSilentAboutChangesetsThatLanded(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)

	// Land it the way a squash merge would: the changeset's content, verbatim, on main.
	f.SwitchTo("main")
	f.MustGit("checkout", "booking", "--", filepath.Join("changesets", slug))
	f.Commit("land the booking change")
	f.ForceDeleteBranch("booking")

	res := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	if queueListsChangeset(t, res, slug) {
		t.Errorf("a landed changeset is still in the queue:\n%s", res.stdout)
	}
	if skipped := res.jsonList(t, "skipped"); len(skipped) != 0 {
		t.Errorf("skipped = %v, want the empty array: nothing to say about a changeset that has landed", skipped)
	}
	human := runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	if strings.Contains(human.stdout+human.stderr, "skipped") {
		t.Errorf("the queue complained about a landed changeset:\n%s\n%s", human.stdout, human.stderr)
	}
}

// A directory under changesets/ that was never anchored was never offered for review,
// so the queue has nothing to say about it. This is the case the old code reported as a
// skip note for every stray directory, and it is the reason the primitive that lists
// directories does not have to know what a changeset is.
func TestReviewQueueIgnoresDirectoriesThatWereNeverOffered(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")

	// The directory reaches main without any marker or anchor behind it: a notes
	// directory, or scaffolding committed by hand. The branch goes too, which is what
	// leaves the directory for the queue to explain.
	f.SwitchTo("main")
	f.ForceDeleteBranch("booking")
	f.Commit("note the plan", gittest.WithFiles(map[string]string{
		"changesets/" + slug + "/CHANGESET.yaml": "base: main\n",
		"changesets/" + slug + "/ABOUT.md":       "# booking\n\nWritten down, never offered.\n",
	}))

	res := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	if queueListsChangeset(t, res, slug) {
		t.Errorf("a changeset with no marker is not ready:\n%s", res.stdout)
	}
	if skipped := res.jsonList(t, "skipped"); len(skipped) != 0 {
		t.Errorf("skipped = %v, want the empty array: no word about a directory that was never offered", skipped)
	}
}

// The other side of the same directory: content that is *not* in its base, left in a branch that accounts
// for a different changeset, with no branch of the changeset's own name behind it. Silence here would hide
// the only surviving copy of the work, so it gets one line naming what it is missing from.
//
// Two directories are needed on purpose: a branch carrying one changeset directory treats it as the work it
// is doing, and only a *second* directory on that branch is the leftover this note is about. The base has to
// be a branch that really lacks the work too, because the anchor is no longer a ref somebody wrote — it is
// the directory's own history on the branch that carries it. The destination holding the directory is the
// other answer (a landing), and it is reported under its own heading instead.
func TestReviewQueueNamesWorkWhoseBranchWentMissing(t *testing.T) {
	const stray = "zook"
	f := newRepo(t)
	f.CreateBranch(stray)
	f.CommitChangeset(stray, "main")
	f.Commit(stray+": work", gittest.WithFile("z.go", "package main\n"))
	copied := f.Head()
	// The branch carries the copy before it has its own changeset, so the directory it works on is the one it
	// accounts for and the copy is the leftover.
	f.SwitchTo("main")
	f.CreateBranch("other")
	f.MustGit("checkout", copied, "--", changeset.Root+"/"+stray)
	f.Commit("other: carry a copy of the zook directory", gittest.WithFile("carry.md", "carry\n"))
	f.CommitChangeset("other", "main")
	f.ForceDeleteBranch(stray)

	res := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	if queueListsChangeset(t, res, stray) {
		t.Errorf("work with no branch behind it is not reviewable:\n%s", res.stdout)
	}
	skipped, ok := res.json(t)["skipped"].([]any)
	if !ok || len(skipped) != 1 {
		t.Fatalf("skipped = %v, want one note naming the work nobody carries", res.json(t)["skipped"])
	}
	note := skipped[0].(string)
	for _, want := range []string{stray, "is not in main", "no branch carries it"} {
		if !strings.Contains(note, want) {
			t.Errorf("skipped note %q does not mention %q", note, want)
		}
	}
}

// And the case with no record at all, which the queue must not guess about: a directory on trunk with
// no branch and nothing in the namespace is either a leftover or work whose branch went before anyone
// recorded it, and git-pair cannot tell which. It says nothing.
func TestReviewQueueIsSilentAboutAnUnrecordedOrphan(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)

	f.SwitchTo("main")
	f.Write(filepath.Join("changesets", slug, "CHANGESET.yaml"), "base: main\n")
	f.Write(filepath.Join("changesets", slug, "ABOUT.md"), "# booking\n\nA different description than the recorded one.\n")
	f.Commit("note the booking change")
	f.ForceDeleteBranch("booking")

	res := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
	if queueListsChangeset(t, res, slug) {
		t.Errorf("an orphan with no record is not reviewable:\n%s", res.stdout)
	}
	if skipped := res.jsonList(t, "skipped"); len(skipped) != 0 {
		t.Errorf("skipped = %v, want the empty array: no word about a directory nothing records", skipped)
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
			t.Errorf("git-pair %v exited %d, want %d\nstderr: %s", args, res.code, exitUsage, res.stderr)
		}
		mustContain(t, res.stderr, "git pair diff", "the refusal must name the non-interactive equivalents")
	}
}

// `git pair review` with no subcommand is `git pair review open`: the same screen, the same flags, and
// the same refusal spelled the way the reviewer typed it. `open` keeps existing because a reader of
// `git pair review --help` should find the verb they would have guessed.
func TestBareReviewIsReviewOpen(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")

	bare := runIn(t, f.Dir(), "review")
	if bare.code != exitUsage {
		t.Errorf("`git-pair review` exited %d, want %d\nstderr: %s", bare.code, exitUsage, bare.stderr)
	}
	mustContain(t, bare.stderr, "`git pair review` needs a terminal",
		"the refusal quotes the wrong command")
	mustContain(t, bare.stderr, "git pair diff", "the refusal must name the non-interactive equivalents")

	// The span flags belong to the group as much as to `open`, so the shorthand can still say which span.
	span := runIn(t, f.Dir(), "review", "--since-review=xyz")
	if span.code != exitUsage {
		t.Errorf("`git-pair review --since-review=xyz` exited %d, want %d\nstderr: %s",
			span.code, exitUsage, span.stderr)
	}
	mustContain(t, span.stderr, "--since-review expects an integer index",
		"the group does not carry the span flags `open` has")

	help := runIn(t, f.Dir(), "review", "--help")
	if help.code != exitOK {
		t.Errorf("`git-pair review --help` exited %d, want %d\nstderr: %s", help.code, exitOK, help.stderr)
	}
	mustContain(t, help.stdout, "git pair review open", "the group's help must name what it abbreviates")
	mustContain(t, help.stdout, "--unreviewed", "the group's help must show the flags it accepts")

	// A word that is not one of the group's commands is still a mistake, not an argument to the screen.
	bogus := runIn(t, f.Dir(), "review", "bogus")
	if bogus.code != exitUsage {
		t.Errorf("`git-pair review bogus` exited %d, want %d\nstderr: %s", bogus.code, exitUsage, bogus.stderr)
	}
	mustContain(t, bogus.stderr, `unknown review command "bogus"`, "the group stopped naming its mistakes")
}

// --- helpers ----------------------------------------------------------------

// submitJSON submits a review with --json so the commit SHA can be asserted.
func submitJSON(t *testing.T, f *gittest.Fixture, outcome string) map[string]any {
	t.Helper()
	args := []string{"review", "submit", "--" + outcome, "--json"}
	return runIn(t, f.Dir(), args...).mustSucceed(t, args...).json(t)
}

// submitAs records a review submission as a named reviewer, which is how a test tells two
// reviewers apart. `review submit` commits through ordinary git with the process environment,
// so GIT_AUTHOR_* is the identity the commit gets. The author is read back from the commit
// because a test of a printed name is worth nothing if the fixture never changed it.
func submitAs(t *testing.T, f *gittest.Fixture, name, outcome string) string {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", name)
	t.Setenv("GIT_AUTHOR_EMAIL", strings.ToLower(name)+"@example.invalid")
	args := []string{"review", "submit", "--" + outcome, "--json"}
	out := runIn(t, f.Dir(), args...).mustSucceed(t, args...).json(t)
	sha, _ := out["commit"].(string)
	if sha == "" {
		t.Fatalf("submit --json returned no commit: %v", out)
	}
	if got := strings.TrimSpace(f.MustGit("show", "-s", "--format=%an", sha)); got != name {
		t.Fatalf("%s is authored by %q, want %q", sha, got, name)
	}
	return sha
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
	mustContain(t, got.stderr, "git pair review open", "should name the command that works")
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
// state is derived from commit trailers and git-pair does not rewrite history. The
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
