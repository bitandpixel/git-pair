package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// `git pair check` is the assertion half of the CLI: a merge gate reads its exit code, and the
// words exist for a human who is watching the same gate. These tests pin the surface — exit code,
// the shape of the two outputs, the policy switch — while check_internal_test.go pins the
// conditions themselves.

func TestCheckPassesOnAnArchivedApprovedHead(t *testing.T) {
	f, slug, _, approved := approvedChangeset(t)

	res := runIn(t, f.Dir(), "check").mustSucceed(t, "check")
	mustContain(t, res.stdout, "OK: "+slug+" is integration-ready",
		"the success line must name the changeset the gate just cleared")
	mustContain(t, res.stdout, "archive: "+f.Short(approved),
		"the success line must name the archived commit, so the log says what was gated")
	if res.stderr != "" {
		t.Errorf("a passing check wrote to stderr: %q", res.stderr)
	}
}

// The failure output is a list, not the first refusal: one run has to name every problem, or the
// fix costs a round trip per problem.
func TestCheckListsEveryFailure(t *testing.T) {
	f, _, _, _ := approvedChangeset(t)
	f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { transaction() }\n"))

	res := runIn(t, f.Dir(), "check")
	if res.code != exitRefusal {
		t.Fatalf("check exited %d, want %d\nstdout: %s", res.code, exitRefusal, res.stdout)
	}
	if !strings.HasPrefix(res.stdout, "NOT READY:\n") {
		t.Fatalf("stdout = %q, want it to open with the verdict", res.stdout)
	}
	mustContain(t, res.stdout, "- content outside changesets/booking-transaction/ changed since",
		"the drift condition must name the paths that moved")
	mustContain(t, res.stdout, "service.go", "the drift bullet must name the file")
	mustContain(t, res.stdout, "- archive does not point to the current source commit",
		"the archive condition must be reported alongside the drift, not instead of it")

	lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
	if got := len(lines) - 1; got != 2 {
		t.Errorf("got %d bullets, want both conditions reported at once:\n%s", got, res.stdout)
	}
	// The verdict is the command's answer, not an error: a failing check must not also
	// print a `git-pair:` diagnostic over its own output.
	mustNotContain(t, res.stdout+res.stderr, "git-pair:",
		"a failing check reports through its exit code, not as a tool error")
}

// Requirements §29: the feedback rule is the one policy switch, and a verdict recorded without
// its policy is a number nobody can re-derive.
func TestCheckFeedbackPolicy(t *testing.T) {
	feedbackChangeset := func(t *testing.T) *gittest.Fixture {
		t.Helper()
		f, _ := newChangeset(t, "booking-transaction", "main")
		ready(t, f)
		submit(t, f, "feedback")
		return f
	}

	t.Run("default policy requires an approval", func(t *testing.T) {
		f := feedbackChangeset(t)
		res := runIn(t, f.Dir(), "check")
		if res.code != exitRefusal {
			t.Fatalf("check exited %d, want %d\nstdout: %s", res.code, exitRefusal, res.stdout)
		}
		mustContain(t, res.stdout, "feedback", "the bullet must name the outcome it is refusing")
		mustContain(t, res.stdout, "--allow-feedback", "the bullet must name the switch that would accept it")

		out := runIn(t, f.Dir(), "check", "--json").json(t)
		if out["policy"] != "approve-only" {
			t.Errorf("policy = %v, want approve-only", out["policy"])
		}
	})

	t.Run("--allow-feedback accepts it", func(t *testing.T) {
		f := feedbackChangeset(t)
		args := []string{"check", "--allow-feedback"}
		res := runIn(t, f.Dir(), args...).mustSucceed(t, args...)
		mustContain(t, res.stdout, "OK: booking-transaction is integration-ready",
			"the permissive policy must clear a non-blocking review")

		out := runIn(t, f.Dir(), "check", "--allow-feedback", "--json").json(t)
		if out["policy"] != "approve-or-feedback" {
			t.Errorf("policy = %v, want approve-or-feedback", out["policy"])
		}
	})
}

func TestCheckRefusesWhatTheGateMustRefuse(t *testing.T) {
	t.Run("a blocking review", func(t *testing.T) {
		f, _ := newChangeset(t, "booking-transaction", "main")
		ready(t, f)
		submit(t, f, "block")

		res := runIn(t, f.Dir(), "check")
		if res.code != exitRefusal {
			t.Fatalf("check exited %d, want %d\nstdout: %s", res.code, exitRefusal, res.stdout)
		}
		mustContain(t, res.stdout, "latest review outcome is blocking",
			"requirements §28's wording for the blocking case")
	})

	t.Run("a changeset offered but never reviewed", func(t *testing.T) {
		f, _ := newChangeset(t, "booking-transaction", "main")
		ready(t, f)

		res := runIn(t, f.Dir(), "check")
		if res.code != exitRefusal {
			t.Fatalf("check exited %d, want %d\nstdout: %s", res.code, exitRefusal, res.stdout)
		}
		mustContain(t, res.stdout, "marked ready and has not been reviewed since",
			"being offered is not being reviewed")
	})

	t.Run("a withdrawn changeset, even after an approval", func(t *testing.T) {
		f, _, _, _ := approvedChangeset(t)
		runIn(t, f.Dir(), "change", "unready").mustSucceed(t, "change", "unready")

		res := runIn(t, f.Dir(), "check", "--allow-feedback")
		if res.code != exitRefusal {
			t.Fatalf("check exited %d, want %d\nstdout: %s", res.code, exitRefusal, res.stdout)
		}
		mustContain(t, res.stdout, "took the changeset out of review",
			"a withdrawal is the author saying this is not ready, and the newest marker says so")
	})

	t.Run("an abandoned changeset reports only the ending", func(t *testing.T) {
		f, slug := newChangeset(t, "booking-transaction", "main")
		ready(t, f)
		res := runIn(t, f.Dir(), "change", "abandon", "--json").mustSucceed(t, "change", "abandon")
		at := f.Short(res.json(t)["abandoned_commit"].(string))

		out := runIn(t, f.Dir(), "check")
		if out.code != exitRefusal {
			t.Fatalf("check exited %d, want %d\nstdout: %s", out.code, exitRefusal, out.stdout)
		}
		mustContain(t, out.stdout, "abandoned by "+at, "the bullet must name the ending")
		if lines := strings.Split(strings.TrimSpace(out.stdout), "\n"); len(lines) != 2 {
			// The archive is current here — abandoning moved the ref onto the terminal
			// marker — so the only thing worth saying is that the changeset has ended.
			t.Errorf("want the verdict and one bullet, got:\n%s", out.stdout)
		}
		mustNotContain(t, out.stdout, archiveRef(slug),
			"nothing below the ending is actionable, so listing it sends someone to fix the archive")
	})

	t.Run("a marker this build cannot read", func(t *testing.T) {
		f, _, _, _ := approvedChangeset(t)
		f.CommitMessage("git-pair: close booking-transaction\n\nReview-State: closed\nReview-Changeset: booking-transaction\n",
			gittest.WithEmpty())

		res := runIn(t, f.Dir(), "check")
		if res.code != exitRefusal {
			t.Fatalf("check exited %d, want %d\nstdout: %s", res.code, exitRefusal, res.stdout)
		}
		mustContain(t, res.stdout, "cannot read",
			"a trailer git-pair cannot interpret cannot permit integration, and the bullet must say so")
	})
}

// The archive condition is the one `change archive` exists to clear: a changeset-only commit is
// not drift, so the review still stands and the only thing missing is moving the archive.
func TestCheckWaitsForTheArchiveToFollowTheBranch(t *testing.T) {
	f, slug, _, _ := approvedChangeset(t)
	f.Write(f.ChangesetPath(slug, "ABOUT.md"), "Decided to keep the retry budget at three.\n")
	f.Commit("record the decision in the changeset")

	res := runIn(t, f.Dir(), "check")
	if res.code != exitRefusal {
		t.Fatalf("check exited %d, want %d\nstdout: %s", res.code, exitRefusal, res.stdout)
	}
	if lines := strings.Split(strings.TrimSpace(res.stdout), "\n"); len(lines) != 2 {
		t.Fatalf("want exactly the archive bullet, got:\n%s", res.stdout)
	}
	mustContain(t, res.stdout, "archive does not point to the current source commit",
		"the archive naming an ancestor is the one thing left to do here")

	runIn(t, f.Dir(), "change", "archive").mustSucceed(t, "change", "archive")
	runIn(t, f.Dir(), "check").mustSucceed(t, "check")
}

// The assertion is about the commit under review. Uncommitted edits are not in HEAD, so they
// cannot invalidate a review of it — and in CI, where this command runs, there is no working
// tree to speak of. `status` reports the dirt, because status is observing.
func TestCheckAssertsTheCommitNotTheCheckout(t *testing.T) {
	f, _, _, _ := approvedChangeset(t)
	f.Write("service.go", "package main\n\nfunc Lock() { transaction() }\n")

	if out := runIn(t, f.Dir(), "status", "--json").json(t); out["uncommitted"] != true {
		t.Fatalf("status should see the dirty tree, got %v", out["uncommitted"])
	}
	runIn(t, f.Dir(), "check").mustSucceed(t, "check")
}

func TestCheckUsageErrors(t *testing.T) {
	t.Run("no changeset on this branch", func(t *testing.T) {
		f := newRepo(t)
		res := runIn(t, f.Dir(), "check")
		if res.code != exitUsage {
			t.Errorf("check on a branch with no changeset exited %d, want %d\nstderr: %s",
				res.code, exitUsage, res.stderr)
		}
	})

	// `--changeset` is the read-side flag, and this is the gate a forge runs *on* a
	// revision: naming a second changeset would make the verdict ambiguous about what
	// was gated, so the command does not offer the flag at all.
	t.Run("naming another changeset", func(t *testing.T) {
		f, slug, _, _ := approvedChangeset(t)
		res := runIn(t, f.Dir(), "check", "--changeset", slug)
		if res.code != exitUsage {
			t.Errorf("`check --changeset` exited %d, want %d\nstderr: %s",
				res.code, exitUsage, res.stderr)
		}
		mustContain(t, res.stderr, "unknown flag", "the refusal is cobra's, and it is the right one")
	})

	t.Run("a stray argument", func(t *testing.T) {
		f, _, _, _ := approvedChangeset(t)
		res := runIn(t, f.Dir(), "check", "booking-transaction")
		if res.code != exitUsage {
			t.Errorf("`check <arg>` exited %d, want %d\nstderr: %s", res.code, exitUsage, res.stderr)
		}
	})

	// `--json` moves where the verdict lives, not the exit-code table: a call cobra refuses is
	// still a usage error, so a JSON job that ignores `$?` still learns the repository was
	// never readable rather than reading an absent object as "not ready".
	t.Run("asking for json does not soften a usage error", func(t *testing.T) {
		f, _, _, _ := approvedChangeset(t)
		res := runIn(t, f.Dir(), "check", "--json", "booking-transaction")
		if res.code != exitUsage {
			t.Errorf("`check --json <arg>` exited %d, want %d\nstderr: %s", res.code, exitUsage, res.stderr)
		}
	})
}

// The JSON is the contract an automation consumes: full SHAs, because a CI job compares them
// against the revision it built, and `reasons` always an array so a consumer never has to
// handle "empty means a different type".
func TestCheckJSONContract(t *testing.T) {
	f, slug, _, approved := approvedChangeset(t)
	args := []string{"check", "--json"}
	out := runIn(t, f.Dir(), args...).mustSucceed(t, args...).json(t)

	assertKeys(t, out, "changeset", "ready", "state", "head", "archive", "archive_current", "reasons", "policy")
	if out["changeset"] != slug {
		t.Errorf("changeset = %v, want %q", out["changeset"], slug)
	}
	if out["ready"] != true {
		t.Errorf("ready = %v, want true", out["ready"])
	}
	if out["state"] != "APPROVED" {
		t.Errorf("state = %v, want APPROVED", out["state"])
	}
	if out["archive"] != approved || out["head"] != approved {
		t.Errorf("head/archive = %v/%v, want both the approved commit %s", out["head"], out["archive"], approved)
	}
	if len(out["head"].(string)) != 40 {
		t.Errorf("head = %v, want the full SHA a CI job can compare against its build", out["head"])
	}
	if out["archive_current"] != true {
		t.Errorf("archive_current = %v, want true", out["archive_current"])
	}
	if reasons, ok := out["reasons"].([]any); !ok || len(reasons) != 0 {
		t.Errorf("reasons = %v, want an empty array", out["reasons"])
	}

	// The failure case reports the same keys, so a consumer branches on `ready` rather
	// than on which keys are present. It also exits 0: `--json` carries the verdict in
	// `ready`, and the exit code is the verdict in the human form alone. Pinned because
	// "the exit code is the verdict" is the sentence a reader brings to this output too.
	f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { x() }\n"))
	badRun := runIn(t, f.Dir(), "check", "--json")
	if badRun.code != exitOK {
		t.Errorf("not-ready `check --json` exited %d, want %d — the verdict travels in `ready`\nstdout: %s",
			badRun.code, exitOK, badRun.stdout)
	}
	bad := badRun.json(t)
	if bad["ready"] != false {
		t.Errorf("ready = %v, want false", bad["ready"])
	}
	reasons, ok := bad["reasons"].([]any)
	if !ok || len(reasons) == 0 {
		t.Fatalf("reasons = %v, want the reasons the human output lists", bad["reasons"])
	}
	if bad["archive_current"] != false {
		t.Errorf("archive_current = %v, want false", bad["archive_current"])
	}
	if bad["state"] != "WORKING" {
		t.Errorf("state = %v, want WORKING: the approval no longer describes HEAD", bad["state"])
	}
}
