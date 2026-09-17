package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
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

// An implementation commit after an approve leaves the approval standing: state moves on
// commands. What the drift does decide is whether the head may be archived, and there it
// refuses (PRD §9.5, §12).
func TestStatusImplementationCommitAfterApproveKeepsTheApproval(t *testing.T) {
	f, slug, _, approve := approvedChangeset(t)

	if got := runIn(t, f.Dir(), "status", "--json").json(t)["state"]; got != "APPROVED" {
		t.Fatalf("state = %v, want APPROVED before the agent commits", got)
	}

	f.Commit("agent: address feedback", gittest.WithFile("service.go", "package main\n\nfunc Lock() { retry() }\n"))

	out := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json").json(t)
	if out["state"] != "APPROVED" {
		t.Errorf("state = %v, want APPROVED: a commit is not a marker", out["state"])
	}
	// The approval is still the latest review, and the reason says the branch moved.
	latest, ok := out["latest_review"].(map[string]any)
	if !ok || latest["outcome"] != "approve" {
		t.Errorf("latest_review = %v, want the approve to remain the latest review", out["latest_review"])
	}
	mustContain(t, out["reason"].(string), "1 commit since", "status must show the branch has moved since the review")

	res := runIn(t, f.Dir(), "change", "complete")
	if res.code != exitRefusal {
		t.Errorf("completing drifted work exited %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "code changed since review", "the refusal must say the reviewed content is gone")
	if refs := f.RefNames(archivePattern(slug)); len(refs) != 0 {
		t.Errorf("drifted work was archived anyway: %v", refs)
	}
	if got := f.RefSHA(reviewRef(slug)); got != approve {
		t.Errorf("%s moved to %s, want the approval %s", reviewRef(slug), got, approve)
	}
}

// A hand-written Review-* trailer must not become a lifecycle event (plan risk table). It
// establishes nothing and clears nothing — the real ready marker stands — but it is
// reported, and it is what makes completion refuse to archive over it.
func TestStatusReportsUnrecognisedMarkers(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)
	// A ready marker for a different changeset: it must not make this one ready, and it
	// must not be honoured as the marker of record.
	f.CommitMessage("git-pair: ready other-changeset\n\nReview-State: ready\nReview-Changeset: other-changeset\n",
		gittest.WithEmpty())

	out := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json").json(t)
	if out["state"] != "READY" {
		t.Errorf("state = %v, want READY: a marker for another changeset establishes nothing and clears nothing", out["state"])
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

// git-pair renders no diff of its own (PRD §2.1, §26): its output must be exactly git's
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
		t.Errorf("`git pair diff` output differs from `git diff %s HEAD`", from)
	}
	mustContain(t, got.stderr, "main...current", "the resolved span must be named, so the range is never implicit")

	stat := runIn(t, f.Dir(), "diff", "--stat").mustSucceed(t, "diff", "--stat")
	wantStat, err := f.Git("diff", "--stat", from, "HEAD")
	if err != nil {
		t.Fatalf("git diff --stat: %v", err)
	}
	if stat.stdout != wantStat {
		t.Error("`git pair diff --stat` output differs from git's")
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
		t.Errorf("`git pair diff --unreviewed` output differs from `git diff %s HEAD`", review)
	}
	if !strings.Contains(got.stdout, "-"+comment) {
		t.Errorf("the unreviewed span must show the deleted review comment as a removal:\n%s", got.stdout)
	}
	// The label names the review by the alias that addresses it rather than by sha: the sha is what
	// `review history` and `status --json` are for, and here it would be hex where a reader wants
	// "which review am I looking at".
	mustContain(t, got.stderr, "last review", "the span label must name the review it measured from")

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
			t.Errorf("`git pair diff %s` differs from `git diff %s HEAD`", tc.flag, tc.from)
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

// The head is an end of the span too. Naming one turns `diff` into a look at a
// historical range; a ref keeps its name next to the commit it was pinned to.
func TestDiffHeadCheckpoints(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)

	f.Write("service.go", "package main\n\n// first review\nfunc Lock() {}\n")
	submit(t, f, "block")
	firstReview := f.Head()
	f.Commit("response 1", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))
	f.Write("handler.go", "package main\n\n// second review\nfunc Serve() {}\n")
	submit(t, f, "feedback")
	secondReview := f.Head()

	want, err := f.Git("diff", firstReview, secondReview)
	if err != nil {
		t.Fatalf("git diff: %v", err)
	}

	t.Run("review head", func(t *testing.T) {
		got := runIn(t, f.Dir(), "diff", "--since-review=0", "--head-review=-1").
			mustSucceed(t, "diff --since-review=0 --head-review=-1")
		if got.stdout != want {
			t.Error("the span between two reviews differs from `git diff <first> <second>`")
		}
	})

	t.Run("commit head", func(t *testing.T) {
		got := runIn(t, f.Dir(), "diff", "--since-review=0", "--head-commit="+secondReview).
			mustSucceed(t, "diff --head-commit")
		if got.stdout != want {
			t.Error("a commit head differs from the same span named by review")
		}
	})

	t.Run("ref head keeps its name", func(t *testing.T) {
		f.MustGit("branch", "probe", secondReview)
		got := runIn(t, f.Dir(), "diff", "--since-review=0", "--head-ref=probe").
			mustSucceed(t, "diff --head-ref=probe")
		if got.stdout != want {
			t.Error("a ref head differs from the same span named by commit")
		}
		// §16: the name is the identity; the pin is secondary.
		mustContain(t, got.stderr, "probe@", "the span must name the ref, not just its commit")
	})

	t.Run("one end, one name", func(t *testing.T) {
		res := runIn(t, f.Dir(), "diff", "--head-review=-1", "--head-commit="+secondReview)
		if res.code != exitUsage {
			t.Errorf("naming the head twice exited %d, want %d\nstderr: %s", res.code, exitUsage, res.stderr)
		}
	})

	t.Run("a head that is not there", func(t *testing.T) {
		res := runIn(t, f.Dir(), "diff", "--head-commit=no-such-thing")
		if res.code == 0 {
			t.Fatalf("a commit that does not exist resolved a span\nstdout: %s", res.stdout)
		}
		mustContain(t, res.stderr, "no-such-thing", "the error must name what failed to resolve")
	})
}

// The base is an end of the span like any other, and naming it is how a script says "diff
// from here" without knowing the merge base. It gets the same three spellings as the head:
// review index, commit, ref — and the same rule that a ref keeps its name next to its pin.
func TestDiffBaseCheckpoints(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)

	f.Write("service.go", "package main\n\n// first review\nfunc Lock() {}\n")
	submit(t, f, "block")
	firstReview := f.Head()
	f.Commit("response 1", gittest.WithFile("service.go", "package main\n\nfunc Lock() { tx() }\n"))
	f.Write("handler.go", "package main\n\n// second review\nfunc Serve() {}\n")
	submit(t, f, "feedback")
	secondReview := f.Head()

	want, err := f.Git("diff", firstReview, secondReview)
	if err != nil {
		t.Fatalf("git diff: %v", err)
	}

	t.Run("review base", func(t *testing.T) {
		got := runIn(t, f.Dir(), "diff", "--base-review=0", "--head-review=1").
			mustSucceed(t, "diff --base-review=0 --head-review=1")
		if got.stdout != want {
			t.Error("a span named by base and head review differs from `git diff <first> <second>`")
		}
		// One span, one resolver (requirements §21): naming the base and saying "since" it
		// must arrive at the same commits.
		same := runIn(t, f.Dir(), "diff", "--since-review=0", "--head-review=1").
			mustSucceed(t, "diff --since-review=0 --head-review=1")
		if got.stdout != same.stdout {
			t.Error("--base-review=0 and --since-review=0 resolved different spans")
		}
	})

	t.Run("commit base", func(t *testing.T) {
		got := runIn(t, f.Dir(), "diff", "--base-commit="+firstReview, "--head-commit="+secondReview).
			mustSucceed(t, "diff --base-commit")
		if got.stdout != want {
			t.Error("a commit base differs from the same span named by review")
		}
	})

	t.Run("ref base keeps its name", func(t *testing.T) {
		f.MustGit("branch", "probe", firstReview)
		got := runIn(t, f.Dir(), "diff", "--base-ref=probe", "--head-commit="+secondReview).
			mustSucceed(t, "diff --base-ref=probe")
		if got.stdout != want {
			t.Error("a ref base differs from the same span named by commit")
		}
		mustContain(t, got.stderr, "probe@", "the span must name the ref, not just its commit")
	})

	t.Run("a bare --base-review is the latest review", func(t *testing.T) {
		wantLatest, err := f.Git("diff", secondReview, "HEAD")
		if err != nil {
			t.Fatalf("git diff: %v", err)
		}
		got := runIn(t, f.Dir(), "diff", "--base-review").mustSucceed(t, "diff --base-review")
		if got.stdout != wantLatest {
			t.Error("a bare --base-review did not resolve to the most recent review")
		}
	})

	t.Run("the base is named once", func(t *testing.T) {
		for _, args := range [][]string{
			{"--base-commit=" + firstReview, "--base-ref=probe"},
			{"--base-review=0", "--base-commit=" + firstReview},
			{"--unreviewed", "--base-commit=" + firstReview},
			{"--since-review=0", "--base-review=0"},
		} {
			res := runIn(t, f.Dir(), append([]string{"diff"}, args...)...)
			if res.code != exitUsage {
				t.Errorf("naming the base twice (%s) exited %d, want %d\nstderr: %s",
					strings.Join(args, " "), res.code, exitUsage, res.stderr)
			}
		}
	})

	t.Run("a base that is not there", func(t *testing.T) {
		res := runIn(t, f.Dir(), "diff", "--base-commit=no-such-thing")
		if res.code == 0 {
			t.Fatalf("a commit that does not exist resolved a span\nstdout: %s", res.stdout)
		}
		mustContain(t, res.stderr, "no-such-thing", "the error must name what failed to resolve")
	})

	t.Run("a base index that is not a number", func(t *testing.T) {
		res := runIn(t, f.Dir(), "diff", "--base-review=xyz")
		if res.code != exitUsage {
			t.Errorf("a non-integer --base-review exited %d, want %d\nstderr: %s", res.code, exitUsage, res.stderr)
		}
		mustContain(t, res.stderr, "--base-review", "the error must name the flag it rejected")
	})
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
		t.Errorf("`git pair diff service.go` differs from `git diff %s HEAD -- service.go`", from)
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
				t.Errorf("git-pair %v exited %d, want %d\nstderr: %s", tc.args, res.code, exitUsage, res.stderr)
			}
		})
	}

	// A review-relative span becomes available after the first review.
	ready(t, f)
	submit(t, f, "block")
	runIn(t, f.Dir(), "diff", "--unreviewed").mustSucceed(t, "diff", "--unreviewed")
}

// "no commits above the base yet" is true for a self-based changeset and useless:
// there never will be any. status has to name the configuration instead.
func TestStatusExplainsASelfBasedChangeset(t *testing.T) {
	f := newRepo(t)
	f.StageChangeset("main", "main")
	f.Commit("work", gittest.WithFile("service.go", "package main\n"))

	res := runIn(t, f.Dir(), "status").mustSucceed(t, "status")
	mustContain(t, res.stdout, "is this branch itself", "reason should name the problem")
	mustContain(t, res.stdout, "switch -c", "status should say what to do instead")

	out := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)
	if reason, _ := out["reason"].(string); !strings.Contains(reason, "this branch itself") {
		t.Errorf("reason = %q, want it to name the self-referential base", reason)
	}
	if next, _ := out["next_action"].(string); !strings.Contains(next, "CHANGESET.yaml") {
		t.Errorf("next_action = %q, want it to point at CHANGESET.yaml", next)
	}
}
