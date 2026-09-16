package cli_test

import (
	"strings"
	"testing"

	"gitpr/internal/gittest"
)

// --- status (PRD §11.1) ------------------------------------------------------

// PRD §11.1 lists the JSON keys agents may rely on. Extra keys are allowed; these
// must always be present and correctly shaped.
func TestStatusJSONEmitsPRDKeySet(t *testing.T) {
	f, slug := newChangeset(t, "feature/booking-transaction", "main")
	ready(t, f)
	f.Write("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n")
	submit := submitJSON(t, f, "block")
	head := f.Head()

	out := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json").json(t)

	for _, key := range []string{"changeset", "branch", "base", "state", "head", "latest_review", "review_ref"} {
		if _, ok := out[key]; !ok {
			t.Errorf("status --json is missing %q: %v", key, out)
		}
	}
	if out["changeset"] != slug {
		t.Errorf("changeset = %v, want %q", out["changeset"], slug)
	}
	if out["branch"] != "feature/booking-transaction" {
		t.Errorf("branch = %v, want the branch name with its slash", out["branch"])
	}
	if out["base"] != "main" {
		t.Errorf("base = %v, want main", out["base"])
	}
	if out["state"] != "BLOCKED" {
		t.Errorf("state = %v, want BLOCKED", out["state"])
	}
	if out["head"] != head && out["head"] != shortOf(head) {
		t.Errorf("head = %v, want %s", out["head"], shortOf(head))
	}
	if out["review_ref"] != reviewRef(slug) {
		t.Errorf("review_ref = %v, want %s", out["review_ref"], reviewRef(slug))
	}
	latest, ok := out["latest_review"].(map[string]any)
	if !ok {
		t.Fatalf("latest_review = %v, want an object", out["latest_review"])
	}
	for _, key := range []string{"index", "outcome", "commit"} {
		if _, ok := latest[key]; !ok {
			t.Errorf("latest_review is missing %q: %v", key, latest)
		}
	}
	if latest["outcome"] != "block" {
		t.Errorf("latest_review.outcome = %v, want block", latest["outcome"])
	}
	if latest["index"] != float64(0) {
		t.Errorf("latest_review.index = %v, want 0 (PRD §10.5 indexes from 0)", latest["index"])
	}
	commit, _ := latest["commit"].(string)
	if commit != submit["commit"] && commit != submit["short"] {
		t.Errorf("latest_review.commit = %v, want the review commit %v", latest["commit"], submit["commit"])
	}
	if out["uncommitted"] != false {
		t.Errorf("uncommitted = %v, want false on a clean tree", out["uncommitted"])
	}

	human := runIn(t, f.Dir(), "status").mustSucceed(t, "status")
	for _, want := range []string{"Changeset: " + slug, "Branch: feature/booking-transaction", "Base: main", "State: BLOCKED"} {
		mustContain(t, human.stdout, want, "human status output")
	}
}

func TestStatusJSONWithNoReviews(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")

	out := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json").json(t)

	if out["state"] != "WORKING" {
		t.Errorf("state = %v, want WORKING", out["state"])
	}
	value, ok := out["latest_review"]
	if !ok {
		t.Fatal("latest_review key is missing; agents must be able to read it as absent")
	}
	if value != nil {
		t.Errorf("latest_review = %v, want null with no reviews", value)
	}
	human := runIn(t, f.Dir(), "status").mustSucceed(t, "status")
	mustContain(t, human.stdout, "none", "the human output must say there is no review yet")
}

// PRD §11.1 shows "Uncommitted changes: no".
func TestStatusReportsUncommittedChanges(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")

	f.Write("untracked.go", "package main\n")
	out := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json").json(t)
	if out["uncommitted"] != true {
		t.Errorf("uncommitted = %v, want true with an untracked file", out["uncommitted"])
	}
	f.Remove("untracked.go")
	out = runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json").json(t)
	if out["uncommitted"] != false {
		t.Errorf("uncommitted = %v, want false after removing the file", out["uncommitted"])
	}
}

// The named safety property of PRD §12, read through `status`: an implementation
// commit after an approve marker yields WORKING, not APPROVED.
func TestStatusImplementationCommitAfterApproveIsWorking(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)

	if got := runIn(t, f.Dir(), "status", "--json").json(t)["state"]; got != "APPROVED" {
		t.Fatalf("state = %v, want APPROVED before the agent commits", got)
	}

	f.Commit("agent: address feedback", gittest.WithFile("service.go", "package main\n\nfunc Lock() { retry() }\n"))

	out := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json").json(t)
	if out["state"] != "WORKING" {
		t.Errorf("state = %v, want WORKING: approval must not survive an implementation commit", out["state"])
	}
	// The approval is still the latest review; only the effective state changed.
	latest, ok := out["latest_review"].(map[string]any)
	if !ok || latest["outcome"] != "approve" {
		t.Errorf("latest_review = %v, want the approve to remain the latest review", out["latest_review"])
	}
	mustContain(t, out["reason"].(string), "implementation commit", "status must explain the invalidation")
	if got := f.RefSHA(reviewRef(slug)); got != approve {
		t.Errorf("%s moved to %s, want the approval %s", reviewRef(slug), got, approve)
	}
}

// A hand-written GitPR-* trailer must not become a lifecycle event (plan risk table).
func TestStatusReportsUnrecognisedMarkers(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)
	// A ready marker for a different changeset: it must not make this one ready, and
	// it must invalidate the real ready marker by being a newer commit.
	f.CommitMessage("gitpr: ready other-changeset\n\nGitPR-State: ready\nGitPR-Changeset: other-changeset\n",
		gittest.WithEmpty())

	out := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json").json(t)
	if out["state"] != "WORKING" {
		t.Errorf("state = %v, want WORKING: a marker for another changeset establishes nothing", out["state"])
	}
	list, ok := out["unrecognised_markers"].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("unrecognised_markers = %v, want the unreadable marker reported", out["unrecognised_markers"])
	}
	human := runIn(t, f.Dir(), "status").mustSucceed(t, "status")
	mustContain(t, human.stdout, "Unrecognised", "the human output must warn about the unreadable marker")
}

func TestStatusWithoutChangesetIsUsageError(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("lonely")
	f.Commit("impl", gittest.WithFile("a.go", "package main\n"))

	res := runIn(t, f.Dir(), "status")
	if res.code != exitUsage {
		t.Errorf("status on a branch with no changeset exited %d, want %d\nstderr: %s",
			res.code, exitUsage, res.stderr)
	}
	mustContain(t, res.stderr, "change init", "the refusal must name the command that fixes it")
}

// --- diff (PRD §17) ---------------------------------------------------------

// gitpr renders no diff of its own (PRD §2.1, §26): its output must be exactly git's
// for the resolved range.
func TestDiffFullChangesetIsMergeBaseRange(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	// Advance main after the fork so a three-dot-vs-two-dot mistake would be visible.
	f.SwitchTo("main")
	f.Commit("unrelated work on main", gittest.WithFile("unrelated.go", "package main\n"))
	f.SwitchTo("booking")

	from := f.MergeBase("main", "HEAD")
	want, err := f.Git("diff", from, "HEAD")
	if err != nil {
		t.Fatalf("git diff: %v", err)
	}

	got := runIn(t, f.Dir(), "diff").mustSucceed(t, "diff")
	if got.stdout != want {
		t.Errorf("`gitpr diff` output differs from `git diff %s HEAD`", from)
	}
	mustContain(t, got.stderr, "main...HEAD", "the resolved span must be named, so the range is never implicit")

	stat := runIn(t, f.Dir(), "diff", "--stat").mustSucceed(t, "diff", "--stat")
	wantStat, err := f.Git("diff", "--stat", from, "HEAD")
	if err != nil {
		t.Fatalf("git diff --stat: %v", err)
	}
	if stat.stdout != wantStat {
		t.Error("`gitpr diff --stat` output differs from git's")
	}
	mustNotContain(t, got.stdout, "unrelated.go", "the full span must not include work committed to the base later")
}

// PRD §17.2: `--unreviewed` is `latest-review.commit .. HEAD`, deliberately including
// the removal of review-added lines.
func TestDiffUnreviewedIsLatestReviewRange(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)

	const comment = "// Please use a transaction here"
	f.Write("service.go", "package main\n\n"+comment+"\nfunc Lock() {}\n")
	submit(t, f, "block")
	review := f.Head()
	// The author resolves it, which is exactly the deletion --unreviewed must show.
	f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { transaction() }\n"))

	want, err := f.Git("diff", review, "HEAD")
	if err != nil {
		t.Fatalf("git diff: %v", err)
	}
	got := runIn(t, f.Dir(), "diff", "--unreviewed").mustSucceed(t, "diff", "--unreviewed")
	if got.stdout != want {
		t.Errorf("`gitpr diff --unreviewed` output differs from `git diff %s HEAD`", review)
	}
	if !strings.Contains(got.stdout, "-"+comment) {
		t.Errorf("the unreviewed span must show the deleted review comment as a removal:\n%s", got.stdout)
	}
	mustContain(t, got.stderr, shortOf(review), "the span label must name the review it measured from")

	// --since-review=-1 is the same span (PRD §17.3).
	same := runIn(t, f.Dir(), "diff", "--since-review=-1").mustSucceed(t, "diff", "--since-review=-1")
	if same.stdout != got.stdout {
		t.Error("--since-review=-1 resolved to a different span than --unreviewed")
	}
}

// PRD §17.3 and §18: indexes are chronological, `0` is the first review, and the span
// starts at the review commit itself.
func TestDiffSinceReviewIndexes(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)

	f.Write("service.go", "package main\n\n// first review\nfunc Lock() {}\n")
	submit(t, f, "block")
	firstReview := f.Head()
	f.Commit("response 1", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))

	f.Write("handler.go", "package main\n\n// second review\nfunc Serve() {}\n")
	submit(t, f, "feedback")
	secondReview := f.Head()
	f.Commit("response 2", gittest.WithFile("handler.go", "package main\n\nfunc Serve() { ctx() }\n"))

	submit(t, f, "approve")
	thirdReview := f.Head()

	tests := []struct {
		flag string
		from string
	}{
		{"--since-review=0", firstReview},
		{"--since-review=1", secondReview},
		{"--since-review=-3", firstReview},
		{"--since-review=-2", secondReview},
		{"--since-review=-1", thirdReview},
	}
	for _, tc := range tests {
		want, err := f.Git("diff", tc.from, "HEAD")
		if err != nil {
			t.Fatalf("git diff %s HEAD: %v", tc.from, err)
		}
		got := runIn(t, f.Dir(), "diff", tc.flag).mustSucceed(t, "diff", tc.flag)
		if got.stdout != want {
			t.Errorf("`gitpr diff %s` differs from `git diff %s HEAD`", tc.flag, tc.from)
		}
	}

	// A bare --since-review means the latest review (PRD §17.3).
	bare := runIn(t, f.Dir(), "diff", "--since-review").mustSucceed(t, "diff", "--since-review")
	wantLatest, err := f.Git("diff", thirdReview, "HEAD")
	if err != nil {
		t.Fatalf("git diff: %v", err)
	}
	if bare.stdout != wantLatest {
		t.Error("a bare --since-review did not resolve to the most recent review")
	}
}

// PRD §11.2 / §17: a path narrows the same resolved span.
func TestDiffWithFilePath(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")

	from := f.MergeBase("main", "HEAD")
	want, err := f.Git("diff", from, "HEAD", "--", "service.go")
	if err != nil {
		t.Fatalf("git diff: %v", err)
	}
	got := runIn(t, f.Dir(), "diff", "service.go").mustSucceed(t, "diff", "service.go")
	if got.stdout != want {
		t.Errorf("`gitpr diff service.go` differs from `git diff %s HEAD -- service.go`", from)
	}
	mustNotContain(t, got.stdout, "handler.go", "the path filter must be honoured")

	res := runIn(t, f.Dir(), "diff", "does-not-exist.go")
	if res.code != exitUsage {
		t.Errorf("a path outside the span exited %d, want %d\nstderr: %s", res.code, exitUsage, res.stderr)
	}
}

func TestDiffSpanArgumentErrors(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no reviews yet", []string{"diff", "--unreviewed"}},
		{"index out of range", []string{"diff", "--since-review=9"}},
		{"negative index out of range", []string{"diff", "--since-review=-9"}},
		{"not an index", []string{"diff", "--since-review=abc"}},
		{"both span flags", []string{"diff", "--unreviewed", "--since-review=-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := runIn(t, f.Dir(), tc.args...)
			if res.code != exitUsage {
				t.Errorf("gitpr %v exited %d, want %d\nstderr: %s", tc.args, res.code, exitUsage, res.stderr)
			}
		})
	}

	// A review-relative span becomes available after the first review.
	ready(t, f)
	submit(t, f, "block")
	runIn(t, f.Dir(), "diff", "--unreviewed").mustSucceed(t, "diff", "--unreviewed")
}
