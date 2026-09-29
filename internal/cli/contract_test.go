package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The exit codes are the agent-facing half of the CLI contract (plan: "Exit codes:
// 0 success, 1 business-rule refusal, 2 usage/argument error, 3 repository/git error.
// Stable for agents."). These tests pin each class through cli.Execute.

func TestExitCodeSuccess(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")

	for _, args := range [][]string{
		{"status"},
		{"status", "--json"},
		{"queue"},
		{"review", "history"},
		{"diff"},
		{"--version"},
		{"--help"},
	} {
		if res := runIn(t, f.Dir(), args...); res.code != exitOK {
			t.Errorf("git-pair %v exited %d, want %d\nstderr: %s", args, res.code, exitOK, res.stderr)
		}
	}
}

// Exit 1 is a correctly used command that the repository state refused.
func TestExitCodeBusinessRuleRefusal(t *testing.T) {
	t.Run("surviving review additions", func(t *testing.T) {
		f, _ := newChangeset(t, "booking", "main")
		ready(t, f)
		f.Write("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n")
		submit(t, f, "block")
		f.Commit("author response", gittest.WithFile("handler.go", "package main\n\nfunc Serve() { ctx() }\n"))

		res := runIn(t, f.Dir(), "change", "ready")
		if res.code != exitRefusal {
			t.Errorf("exited %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
		}
	})

	t.Run("outcome does not permit integration", func(t *testing.T) {
		f, _ := newChangeset(t, "booking", "main")
		ready(t, f)
		submit(t, f, "block")

		res := runIn(t, f.Dir(), "check")
		if res.code != exitRefusal {
			t.Errorf("exited %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
		}
	})

	t.Run("content moved after the approval", func(t *testing.T) {
		f, _ := newChangeset(t, "booking", "main")
		ready(t, f)
		submit(t, f, "approve")
		f.Commit("author response", gittest.WithFile("service.go", "package main\n\nfunc Lock() { named() }\n"))

		// The gate is the one a merge runs: the approval no longer describes the content, so the
		// assertion fails and says so in the exit code.
		res := runIn(t, f.Dir(), "check")
		if res.code != exitRefusal {
			t.Errorf("exited %d, want %d\nstdout: %s\nstderr: %s", res.code, exitRefusal, res.stdout, res.stderr)
		}
	})

	// An unresolvable revision is not a git failure. `rev-parse --verify --quiet` reports
	// it by exiting 1 with no stderr, so a shallow reading would turn "the base branch is
	// gone" into exit 3, which tells an agent to retry instead of to fix the changeset.
	t.Run("unresolvable revision", func(t *testing.T) {
		f, slug := newChangeset(t, "booking", "main")
		f.Write(f.ChangesetPath(slug, "CHANGESET.yaml"), "base: deleted-base\n")
		f.Commit("record a base branch that no longer exists")

		for _, args := range [][]string{{"status"}, {"diff"}} {
			res := runIn(t, f.Dir(), args...)
			if res.code == exitGit {
				t.Errorf("git-pair %v exited %d (git error), want %d: an absent revision is a repository fact\nstderr: %s",
					args, res.code, exitRefusal, res.stderr)
				continue
			}
			if res.code != exitRefusal {
				t.Errorf("git-pair %v exited %d, want %d\nstderr: %s", args, res.code, exitRefusal, res.stderr)
			}
			mustContain(t, res.stderr, "deleted-base", "the refusal must name the revision it could not resolve")
		}
	})
}

// Exit 2 is a usage or argument problem, including a repository that is not ready to
// be used with the command at all.
// The approved state is where git-pair hands the work on, and both machine surfaces are pinned here
// because both are read by something that acts on it: `status --json` by an observer deciding what to
// do next, `check --json` by the gate the author and CI run. They must say the same thing, and the thing
// they say has to include the step git-pair does not perform — the merge — and the one it performs but
// cannot decide — the record. An agent that finishes an approval and reads only "APPROVED" has no way to
// know the contract continues, and an agent that parses prose out of a text field will parse it wrongly
// eventually.
func TestApprovedStateNamesTheLandingAndTheRecord(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "approve")
	head := f.Head()

	st := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json").json(t)
	assertKeys(t, st, "state", "head", "head_full", "next_action", "landed", "landed_commit",
		"landed_branch", "chain_base", "chain_head", "reviewed")
	if st["state"] != "APPROVED" {
		t.Errorf("status state = %v, want APPROVED", st["state"])
	}
	if st["head_full"] != head {
		t.Errorf("status head_full = %v, want the approved head %s", st["head_full"], head)
	}
	// The landing contract (PRD §29) in one string, asserted as a whole because the *order* is the
	// contract: record, then publish, and only then may the branch go. Spelled once in
	// `landingNextAction`, and every command that offers it has to offer the same sentence.
	want := "`git pair check`, then merge into main with ordinary git, then " +
		"`git pair integration record`, then `git pair integration publish`"
	if st["next_action"] != want {
		t.Errorf("status next_action = %v, want %q", st["next_action"], want)
	}
	if i, j := strings.Index(want, "integration record"), strings.Index(want, "integration publish"); i < 0 || j < 0 || i > j {
		t.Errorf("the landing contract must read record before publish: %q", want)
	}
	// The next step is landing, so nothing may report it already done — and the two halves of the
	// record are reported differently on purpose, which is worth pinning rather than rediscovering:
	// `archive_ref`/`archive_commit` are present and empty so a consumer sees one shape either way,
	// while `integration_ref`/`integrated_commit` appear only with the record itself (README, PRD §11.1).
	if st["landed"] != false || st["reviewed"] != false {
		t.Errorf("status landed/reviewed = %v/%v on a changeset the destination does not carry", st["landed"], st["reviewed"])
	}
	if st["landed_commit"] != "" || st["chain_head"] != "" {
		t.Errorf("status reports a landing before one exists: %v / %v", st["landed_commit"], st["chain_head"])
	}
	if _, present := st["integration_ref"]; present {
		t.Errorf("status carries integration_ref = %v with no record to name", st["integration_ref"])
	}

	ch := runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check", "--json").json(t)
	assertKeys(t, ch, "changeset", "state", "ready", "head", "policy", "reasons", "next_action")
	if ch["ready"] != true {
		t.Fatalf("check ready = %v with reasons %v, want the gate to pass", ch["ready"], ch["reasons"])
	}
	if ch["head"] != head {
		t.Errorf("check head = %v, want the commit the gate cleared (%s) — a log that says ready without saying what it looked at cannot be re-read", ch["head"], head)
	}
	if ch["next_action"] != st["next_action"] {
		t.Errorf("check next_action = %v, want the same step status names: %v", ch["next_action"], st["next_action"])
	}
	if reasons, isList := ch["reasons"].([]any); !isList || len(reasons) != 0 {
		t.Errorf("check reasons = %#v on a passing gate, want an empty array", ch["reasons"])
	}

	// The human forms say it too, in the same words. They are what a person reads, and the two
	// commands disagreeing about the next step would be the contract drifting in real time.
	human := runIn(t, f.Dir(), "check").mustSucceed(t, "check")
	mustContain(t, human.stdout, "next:  "+want, "check's answer names the merge and the record")
	shown := runIn(t, f.Dir(), "status").mustSucceed(t, "status")
	mustContain(t, shown.stdout, "Next: "+want, "and so does status's")

	// A refusal says nothing about the next step: `reasons` is the next step when the gate fails, and
	// two fields would be two answers.
	f.Commit("implementation after the approval", gittest.WithFile("extra.go", "package main\n"))
	refused := runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check", "--json").json(t)
	if refused["ready"] != false {
		t.Fatalf("check ready = true after an unreviewed implementation commit")
	}
	if _, present := refused["next_action"]; present {
		t.Errorf("check next_action = %v on a failing gate, want the key absent: reasons is the next step, "+
			"and two fields would be two answers", refused["next_action"])
	}
	if len(refused["reasons"].([]any)) == 0 {
		t.Errorf("check reasons = %v on a failing gate, want at least one reason", refused["reasons"])
	}
}

func TestExitCodeUsageError(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"status", "--nope"}},
		{"unknown command", []string{"bogus"}},
		{"unknown subcommand", []string{"review", "bogus"}},
		{"unknown subcommand of a group", []string{"change", "bogus"}},
		{"missing outcome", []string{"review", "submit"}},
		{"two outcomes", []string{"review", "submit", "--block", "--feedback"}},
		{"non-integer span index", []string{"diff", "--since-review=xyz"}},
		{"out-of-range span index", []string{"diff", "--since-review=42"}},
		{"contradictory spans", []string{"diff", "--unreviewed", "--since-review=-1"}},
		{"non-integer base index", []string{"diff", "--base-review=xyz"}},
		{"contradictory bases", []string{"diff", "--base-commit=abc", "--base-ref=main"}},
		{"base named twice across the families", []string{"diff", "--since-review=-1", "--base-review=-1"}},
		{"extra argument", []string{"queue", "extra"}},
		{"thread without a title", []string{"review", "thread"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := runIn(t, f.Dir(), tc.args...)
			if res.code != exitUsage {
				t.Errorf("git-pair %v exited %d, want %d\nstderr: %s", tc.args, res.code, exitUsage, res.stderr)
			}
		})
	}

	// Commands that need a changeset say so as a usage error, with the fix.
	lonely := newRepo(t)
	lonely.CreateBranch("lonely")
	lonely.Commit("impl", gittest.WithFile("a.go", "package main\n"))
	for _, args := range [][]string{{"status"}, {"change", "ready"}, {"review", "submit", "--block"}} {
		res := runIn(t, lonely.Dir(), args...)
		if res.code != exitUsage {
			t.Errorf("git-pair %v without a changeset exited %d, want %d\nstderr: %s",
				args, res.code, exitUsage, res.stderr)
		}
	}

	// A detached HEAD has no changeset to act on.
	detached := newRepo(t)
	detached.Detach()
	if res := runIn(t, detached.Dir(), "init", "--base", "main"); res.code != exitUsage {
		t.Errorf("`init` on a detached HEAD exited %d, want %d\nstderr: %s",
			res.code, exitUsage, res.stderr)
	}
}

// Exit 3 is git itself failing. A failing hook is the reproducible way to make git
// refuse a command git-pair has already accepted.
func TestExitCodeGitFailure(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	hook := f.InstallHook("pre-commit", "#!/bin/sh\necho \"hook refuses\" >&2\nexit 9\n")
	defer f.RemoveHook(hook)

	head := f.Head()
	res := runIn(t, f.Dir(), "change", "ready")
	if res.code != exitGit {
		t.Errorf("change ready with a failing git exited %d, want %d\nstdout: %s\nstderr: %s",
			res.code, exitGit, res.stdout, res.stderr)
	}
	mustContain(t, res.stderr, "hook refuses", "the git error must be passed through so an agent can act on it")
	if f.Head() != head {
		t.Errorf("HEAD moved to %s despite git failing", f.Head())
	}
	if refs := f.RefNames("refs/git-pair/changesets"); len(refs) != 0 {
		t.Errorf("a failed submission moved review refs: %v", refs)
	}

	res = runIn(t, f.Dir(), "review", "submit", "--approve")
	if res.code != exitGit {
		t.Errorf("review submit with a failing git exited %d, want %d\nstderr: %s", res.code, exitGit, res.stderr)
	}
	if got := f.Trailers(f.Head())["Review-Outcome"]; got != "" {
		t.Errorf("a failed submission wrote review trailers: %v", got)
	}
	if f.HasRef(archiveRef(slug)) {
		t.Error("a failed submission created a review ref")
	}
}

// The --json contracts. These are what agents and notifications consume, so each
// command's documented keys must be present and correctly typed. Extra keys are
// allowed; missing or renamed ones are not.
func TestJSONKeySets(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		f, _ := newChangeset(t, "booking", "main")
		ready(t, f)
		out := runIn(t, f.Dir(), "status", "--json").json(t)
		assertKeys(t, out, "changeset", "branch", "base", "state", "head", "latest_review", "landed",
			"landed_commit", "landed_branch", "chain_base", "chain_head", "reviewed")
		if out["state"] != "READY" {
			t.Errorf("state = %v, want READY", out["state"])
		}
		// The keys are always present and empty while work is in flight: the record is what landing
		// writes, and a consumer should not have to handle two shapes for "there is no record yet".
		if out["landed_commit"] != "" || out["chain_head"] != "" {
			t.Errorf("landed_commit = %v, chain = %v; nothing has landed", out["landed_commit"], out["chain_head"])
		}
	})

	t.Run("change ready", func(t *testing.T) {
		f, slug := newChangeset(t, "booking", "main")
		args := []string{"change", "ready", "--json"}
		out := runIn(t, f.Dir(), args...).mustSucceed(t, args...).json(t)
		assertKeys(t, out, "changeset", "branch", "base", "state", "head", "ready_commit")
		if out["state"] != "READY" {
			t.Errorf("state = %v, want READY", out["state"])
		}
		if out["changeset"] != slug {
			t.Errorf("changeset = %v, want %q", out["changeset"], slug)
		}
		if out["ready_commit"] != f.Head() {
			t.Errorf("ready_commit = %v, want the marker %s", out["ready_commit"], f.Head())
		}
	})

	t.Run("review submit", func(t *testing.T) {
		f, _ := newChangeset(t, "booking", "main")
		ready(t, f)
		f.Write("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n")
		args := []string{"review", "submit", "--block", "--json"}
		out := runIn(t, f.Dir(), args...).mustSucceed(t, args...).json(t)
		assertKeys(t, out, "changeset", "outcome", "commit", "files", "empty")
		if out["outcome"] != "block" {
			t.Errorf("outcome = %v, want block", out["outcome"])
		}
		if out["commit"] != f.Head() {
			t.Errorf("commit = %v, want %s", out["commit"], f.Head())
		}
		if out["empty"] != false {
			t.Errorf("empty = %v, want false for a review that changed a file", out["empty"])
		}
		files, ok := out["files"].([]any)
		if !ok {
			t.Fatalf("files = %T, want an array", out["files"])
		}
		if len(files) != 1 || files[0] != "service.go" {
			t.Errorf("files = %v, want [service.go]", files)
		}

		// A clean-tree approval is the empty case; `empty` is what an agent checks.
		f.Commit("resolve the comment", gittest.WithFile("service.go", "package main\n\nfunc Lock() { transaction() }\n"))
		ready(t, f)
		emptyArgs := []string{"review", "submit", "--approve", "--json"}
		empty := runIn(t, f.Dir(), emptyArgs...).mustSucceed(t, emptyArgs...).json(t)
		if empty["empty"] != true {
			t.Errorf("empty = %v, want true for a clean-tree approval", empty["empty"])
		}
		// `files` is an empty array here, not null: the key answers a question, and `[]` is the answer
		// "the submission changed no files" (README's JSON contracts).
		if list, ok := empty["files"].([]any); !ok || len(list) != 0 {
			t.Errorf("files = %v, want [] for an empty review", empty["files"])
		}
	})

	t.Run("review history", func(t *testing.T) {
		f, slug := newChangeset(t, "booking", "main")
		ready(t, f)
		submit(t, f, "block")
		out := runIn(t, f.Dir(), "review", "history", "--json").json(t)
		assertKeys(t, out, "changeset", "reviews")
		if out["changeset"] != slug {
			t.Errorf("changeset = %v, want %q", out["changeset"], slug)
		}
		reviews := out["reviews"].([]any)
		if len(reviews) != 1 {
			t.Fatalf("reviews = %v, want one entry", reviews)
		}
		row := reviews[0].(map[string]any)
		assertKeys(t, row, "index", "sha", "short", "reviewed_head", "outcome", "subject", "reviewer", "age")
		if row["index"] != float64(0) || row["outcome"] != "block" {
			t.Errorf("review row = %v, want index 0 outcome block", row)
		}
	})

	t.Run("queue", func(t *testing.T) {
		f, slug := newChangeset(t, "booking", "main")
		ready(t, f)
		out := runIn(t, f.Dir(), "queue", "--json").json(t)
		assertKeys(t, out, "ready_for_review", "landed_unreviewed")
		if _, ok := out["landed_unreviewed"].([]any); !ok {
			t.Errorf("landed_unreviewed = %#v, want an array (never null: it answers a question)", out["landed_unreviewed"])
		}
		rows := out["ready_for_review"].([]any)
		if len(rows) != 1 {
			t.Fatalf("ready_for_review = %v, want one entry", rows)
		}
		entry := rows[0].(map[string]any)
		assertKeys(t, entry, "changeset", "branch", "base", "state", "head", "ready_commit", "ready_age")
		if entry["changeset"] != slug {
			t.Errorf("changeset = %v, want %q", entry["changeset"], slug)
		}
	})

	t.Run("integration record", func(t *testing.T) {
		f, slug, source, landing := recordFixture(t)
		args := []string{"integration", "record", "--source", source, "--commit", landing,
			"--target", "release/2.x", "--json"}
		out := runIn(t, f.Dir(), args...).mustSucceed(t, args...).json(t)
		assertKeys(t, out, "changeset", "source", "commit", "target", "archive_ref", "integration_ref",
			"recorded", "already_recorded")
		if out["changeset"] != slug {
			t.Errorf("changeset = %v, want %q", out["changeset"], slug)
		}
		// Full SHAs, because the consumer is a pipeline holding the SHA it built.
		if out["source"] != source || out["commit"] != landing {
			t.Errorf("source/commit = %v/%v, want %s/%s", out["source"], out["commit"], source, landing)
		}
		if out["archive_ref"] != archiveRef(slug) || out["integration_ref"] != integrationRef(slug) {
			t.Errorf("refs = %v/%v, want %s and %s", out["archive_ref"], out["integration_ref"],
				archiveRef(slug), integrationRef(slug))
		}
		if out["recorded"] != true || out["already_recorded"] != false {
			t.Errorf("recorded = %v, already_recorded = %v, want the pair written by this call",
				out["recorded"], out["already_recorded"])
		}
	})
}

func assertKeys(t *testing.T, object map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			t.Errorf("JSON object is missing %q: %v", key, object)
		}
	}
}
