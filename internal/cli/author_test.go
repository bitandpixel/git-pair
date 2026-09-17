package cli_test

// The author's side of a review: `change feedback` reads what the reviewer submitted,
// and `change wait` blocks until there is something to read. Together they replace the
// old advice to read a review with the reviewer's own command.

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChangeFeedbackShowsTheReviewItself(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)

	// One submission, one commit: a thread plus the edit the reviewer made while
	// reading, which is what an author has to consume.
	f.Write(filepath.Join("changesets", slug, "concurrency.md"),
		"# Thread: does the lock cover the map?\n\nNeeds a test.\n")
	f.Write("service.go", "package main\n\n// reviewer: cover the lock\n\nfunc Lock() {}\n")
	submit(t, f, "feedback")

	t.Run("the diff is the submission, not the changeset", func(t *testing.T) {
		plain := runIn(t, f.Dir(), "change", "feedback").mustSucceed(t, "change", "feedback")
		if !strings.Contains(plain.stdout, "cover the lock") {
			t.Errorf("raw diff omits the reviewer's edit:\n%s", plain.stdout)
		}
		if !strings.Contains(plain.stdout, "++") {
			t.Errorf("raw diff is not a git diff:\n%s", plain.stdout)
		}
	})

	t.Run("file selection lists threads and code alike", func(t *testing.T) {
		got := runIn(t, f.Dir(), "change", "feedback", "--name-only").mustSucceed(t, "change", "feedback", "--name-only")
		for _, want := range []string{"service.go", "changesets/booking/concurrency.md"} {
			if !strings.Contains(got.stdout, want) {
				t.Errorf("--name-only omits %q:\n%s", want, got.stdout)
			}
		}
		// The human header is a note, not part of the machine-readable list.
		if strings.Contains(got.stdout, "git pair change feedback:") {
			t.Errorf("the header belongs on stderr, not in --name-only output:\n%s", got.stdout)
		}
		if !strings.Contains(got.stderr, "(feedback)") {
			t.Errorf("stderr should name what is being shown:\n%s", got.stderr)
		}
	})

	t.Run("--stat summarises without printing hunks", func(t *testing.T) {
		got := runIn(t, f.Dir(), "change", "feedback", "--stat").mustSucceed(t, "change", "feedback", "--stat")
		if !strings.Contains(got.stdout, "|") || strings.Contains(got.stdout, "+package main") {
			t.Errorf("--stat should show the summary only:\n%s", got.stdout)
		}
	})

	t.Run("the reviewer's command answers a different question", func(t *testing.T) {
		// HEAD *is* the review, so there is nothing after it to review: `--unreviewed`
		// is empty exactly when `change feedback` has the most to say.
		empty := runIn(t, f.Dir(), "diff", "--unreviewed", "--stat").mustSucceed(t, "diff", "--unreviewed", "--stat")
		if strings.TrimSpace(empty.stdout) != "" {
			t.Errorf("`diff --unreviewed` should be empty after a submission:\n%s", empty.stdout)
		}
		full := runIn(t, f.Dir(), "change", "feedback", "--stat").mustSucceed(t, "change", "feedback", "--stat")
		if strings.TrimSpace(full.stdout) == "" {
			t.Error("`change feedback` must not be empty where `diff --unreviewed` is")
		}
	})
}

func TestChangeFeedbackWithoutAReview(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)

	got := runIn(t, f.Dir(), "change", "feedback")
	if got.code != exitUsage {
		t.Fatalf("exit code = %d, want %d\n%s", got.code, exitUsage, got.stderr)
	}
	for _, want := range []string{"no review submission", "change wait"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, got.stderr)
		}
	}
}

func TestChangeFeedbackRejectsTwoSelections(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "block")

	got := runIn(t, f.Dir(), "change", "feedback", "--stat", "--name-only")
	if got.code != exitUsage {
		t.Fatalf("exit code = %d, want %d\n%s", got.code, exitUsage, got.stderr)
	}
}

func TestChangeWaitRefusesAnUnqueuedChange(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")

	// WORKING is the author's own state: waiting on it would wait on nobody.
	got := runIn(t, f.Dir(), "change", "wait", "--timeout", "1s")
	if got.code != exitRefusal {
		t.Fatalf("exit code = %d, want %d\n%s", got.code, exitRefusal, got.stderr)
	}
	for _, want := range []string{"WORKING", "change ready"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, got.stderr)
		}
	}
}

func TestChangeWaitReturnsAtOnceWhenAReviewIsAlreadyIn(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "block")

	// Re-running must not block for an interval just to say the same thing twice.
	got := runIn(t, f.Dir(), "change", "wait", "--interval", "1h", "--json").mustSucceed(t, "change", "wait", "--interval", "1h", "--json")
	out := got.json(t)
	// Nothing changed while it waited, so both sides of the comparison say the same
	// thing; an agent that re-runs the command gets the truth, not a fake transition.
	// The spelling matches `git pair status --json`, so one comparison works across commands.
	if out["previous_state"] != "BLOCKED" {
		t.Errorf("previous_state = %v, want %q", out["previous_state"], "BLOCKED")
	}
	if out["state"] != "BLOCKED" {
		t.Errorf("state = %v, want %q", out["state"], "BLOCKED")
	}
	if out["ref"] != "HEAD" {
		t.Errorf("ref = %v, want HEAD", out["ref"])
	}
	if out["timed_out"] != false {
		t.Errorf("timed_out = %v, want false", out["timed_out"])
	}
	if out["review_commit"] == "" || out["review_commit"] == nil {
		t.Errorf("review_commit = %v, want the review's short id", out["review_commit"])
	}
	if sha, ok := out["review_commit_full"].(string); !ok || len(sha) != 40 {
		t.Errorf("review_commit_full = %v, want a full commit id", out["review_commit_full"])
	}
	if !strings.Contains(out["next_action"].(string), "change feedback") {
		t.Errorf("next_action should point at the author's reading command: %v", out["next_action"])
	}
}

func TestChangeWaitTimeoutReportsWhereThingsStand(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)

	// Human mode says what it is doing and where it ended up.
	human := runIn(t, f.Dir(), "change", "wait", "--interval", "10ms", "--timeout", "50ms")
	if human.code != exitRefusal {
		t.Fatalf("exit code = %d, want %d\n%s", human.code, exitRefusal, human.stderr)
	}
	// The progress note is a note (stderr); the summary and its hint are output (stdout).
	if !strings.Contains(human.stderr, "waiting for review activity on booking") {
		t.Errorf("stderr missing the progress note:\n%s", human.stderr)
	}
	for _, want := range []string{"Timed out waiting for review activity on booking", "still waiting"} {
		if !strings.Contains(human.stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, human.stdout)
		}
	}

	got := runIn(t, f.Dir(), "change", "wait", "--interval", "10ms", "--timeout", "50ms", "--json")
	if got.code != exitRefusal {
		t.Fatalf("exit code = %d, want %d\n%s", got.code, exitRefusal, got.stderr)
	}
	// --json says everything on stdout: a timeout is data (`timed_out`), not an error
	// message, so stderr stays quiet and only the exit code reports it.
	if strings.Contains(got.stderr, "git-pair:") {
		t.Errorf("a JSON timeout should keep stderr clean:\n%s", got.stderr)
	}
	out := got.json(t)
	if out["timed_out"] != true {
		t.Errorf("timed_out = %v, want true", out["timed_out"])
	}
	if out["state"] != "READY" || out["previous_state"] != "READY" {
		t.Errorf("states = %v/%v, want the state actually observed (ready)",
			out["previous_state"], out["state"])
	}
	if !strings.Contains(out["next_action"].(string), "still waiting") {
		t.Errorf("next_action = %v, want a keep-waiting hint", out["next_action"])
	}
}

func TestChangeWaitIsNotHeldByItsInterval(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)

	// The default interval is 10s; a short timeout has to be honoured anyway.
	started := time.Now()
	got := runIn(t, f.Dir(), "change", "wait", "--interval", "1h", "--timeout", "200ms", "--json")
	if got.code != exitRefusal {
		t.Fatalf("exit code = %d, want %d\n%s", got.code, exitRefusal, got.stderr)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("waited %s for a 200ms timeout with a 1h interval", elapsed)
	}
	if out := got.json(t); out["timed_out"] != true {
		t.Errorf("timed_out = %v, want true", out["timed_out"])
	}
}

func TestChangeWaitValidatesItsFlags(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)

	for _, args := range [][]string{
		{"change", "wait", "--interval", "banana"},
		{"change", "wait", "--interval", "0s"},
		{"change", "wait", "--timeout", "-5s"},
		{"change", "wait", "--timeout", "soon"},
		{"change", "wait", "--fetch"}, // no remote configured
	} {
		got := runIn(t, f.Dir(), args...)
		if got.code != exitUsage {
			t.Errorf("%v exited %d, want %d\n%s", args, got.code, exitUsage, got.stderr)
		}
	}
	if !strings.Contains(runIn(t, f.Dir(), "change", "wait", "--interval", "banana").stderr, "30s") {
		t.Error("the interval message should show the accepted format")
	}
	if !strings.Contains(runIn(t, f.Dir(), "change", "wait", "--fetch").stderr, "remote") {
		t.Error("the no-remote message should say how to fix it")
	}
}

// TestChangeWaitFetchesAndSeesAReviewFromAnotherClone is the forge-independent case: the
// reviewer works in a second clone, pushes, and the author's `wait --fetch` notices
// through ordinary Git refs — no forge, no network.
func TestChangeWaitFetchesAndSeesAReviewFromAnotherClone(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)

	remote := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, f.Env(), f.Dir(), "init", "--bare", remote)
	f.MustGit("remote", "add", "origin", remote)
	f.MustGit("push", "--quiet", "origin", "main", "booking")

	clone := filepath.Join(t.TempDir(), "reviewer")
	runGit(t, f.Env(), f.Dir(), "clone", "--quiet", "--branch", "booking", remote, clone)
	// A clone carries no identity of its own; the reviewer signs their submission.
	runGit(t, f.Env(), clone, "config", "user.name", "Reviewer")
	runGit(t, f.Env(), clone, "config", "user.email", "reviewer@example.com")
	runGit(t, f.Env(), clone, "config", "commit.gpgsign", "false")
	// The changeset records base "main"; in a clone that only checked out the
	// changeset branch, main exists solely as a remote-tracking ref.
	runGit(t, f.Env(), clone, "branch", "main", "origin/main")

	if got := runIn(t, clone, "review", "submit", "--feedback", "--message", "see the thread"); got.code != exitOK {
		t.Fatalf("reviewer's submit exited %d\n%s\n%s", got.code, got.stdout, got.stderr)
	}
	runGit(t, f.Env(), clone, "push", "--quiet", "origin", "booking")

	// The author's own HEAD has not moved: only the fetch can reveal the review.
	got := runIn(t, f.Dir(), "change", "wait", "--fetch", "--interval", "50ms", "--timeout", "30s", "--json").
		mustSucceed(t, "change", "wait", "--fetch", "--interval", "50ms", "--timeout", "30s", "--json")
	out := got.json(t)
	if out["state"] != "FEEDBACK" {
		t.Errorf("state = %v, want FEEDBACK from the remote branch", out["state"])
	}
	if out["ref"] != "refs/remotes/origin/booking" {
		t.Errorf("ref = %v, want the remote-tracking branch it came from", out["ref"])
	}
	if fetches, _ := out["fetches"].(float64); fetches < 1 {
		t.Errorf("fetches = %v, want at least one fetch", out["fetches"])
	}
	if out["review_commit"] == "" || out["review_commit"] == nil {
		t.Errorf("review_commit = %v, want the remote review's id", out["review_commit"])
	}
}

// runGit runs plain git in dir with the author fixture's sanitized environment, so the
// helper repos are as isolated as the fixture itself.
func runGit(t *testing.T, env []string, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(env, "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}
