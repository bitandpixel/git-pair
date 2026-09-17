package cli_test

import (
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
		{"review", "queue"},
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

		res := runIn(t, f.Dir(), "change", "archive")
		if res.code != exitRefusal {
			t.Errorf("exited %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
		}
	})

	t.Run("surviving additions at completion time", func(t *testing.T) {
		f, _ := newChangeset(t, "booking", "main")
		ready(t, f)
		f.Write("service.go", "package main\n\n// Please name this variable\nfunc Lock() {}\n")
		submit(t, f, "approve")

		res := runIn(t, f.Dir(), "change", "archive")
		if res.code != exitRefusal {
			t.Errorf("exited %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
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
		{"extra argument", []string{"review", "queue", "extra"}},
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
	if res := runIn(t, detached.Dir(), "change", "init", "--base", "main"); res.code != exitUsage {
		t.Errorf("change init on a detached HEAD exited %d, want %d\nstderr: %s",
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
	if refs := f.RefNames("refs/reviews"); len(refs) != 0 {
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
		f, slug := newChangeset(t, "booking", "main")
		ready(t, f)
		out := runIn(t, f.Dir(), "status", "--json").json(t)
		assertKeys(t, out, "changeset", "branch", "base", "state", "head", "latest_review", "archive_ref", "archive_commit")
		if out["state"] != "READY" {
			t.Errorf("state = %v, want READY", out["state"])
		}
		if out["archive_ref"] != archiveRef(slug) {
			t.Errorf("archive_ref = %v, want %s", out["archive_ref"], archiveRef(slug))
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
		f, slug := newChangeset(t, "booking", "main")
		ready(t, f)
		f.Write("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n")
		args := []string{"review", "submit", "--block", "--json"}
		out := runIn(t, f.Dir(), args...).mustSucceed(t, args...).json(t)
		assertKeys(t, out, "changeset", "outcome", "commit", "archive_ref", "files", "empty")
		if out["outcome"] != "block" {
			t.Errorf("outcome = %v, want block", out["outcome"])
		}
		if out["commit"] != f.Head() {
			t.Errorf("commit = %v, want %s", out["commit"], f.Head())
		}
		if out["archive_ref"] != archiveRef(slug) {
			t.Errorf("archive_ref = %v, want %s", out["archive_ref"], archiveRef(slug))
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
		if value, ok := empty["files"]; ok && value != nil {
			if list, ok := value.([]any); !ok || len(list) != 0 {
				t.Errorf("files = %v, want an empty list for an empty review", value)
			}
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
		assertKeys(t, row, "index", "sha", "short", "outcome", "subject", "age")
		if row["index"] != float64(0) || row["outcome"] != "block" {
			t.Errorf("review row = %v, want index 0 outcome block", row)
		}
	})

	t.Run("review queue", func(t *testing.T) {
		f, slug := newChangeset(t, "booking", "main")
		ready(t, f)
		out := runIn(t, f.Dir(), "review", "queue", "--json").json(t)
		assertKeys(t, out, "ready_for_review")
		rows := out["ready_for_review"].([]any)
		if len(rows) != 1 {
			t.Fatalf("ready_for_review = %v, want one entry", rows)
		}
		entry := rows[0].(map[string]any)
		assertKeys(t, entry, "changeset", "branch", "base", "state", "head", "ready_commit", "ready_age", "archive_ref")
		if entry["changeset"] != slug {
			t.Errorf("changeset = %v, want %q", entry["changeset"], slug)
		}
	})

	t.Run("change archive", func(t *testing.T) {
		f, slug, _, approve := approvedChangeset(t)
		args := []string{"change", "archive", "--json"}
		out := runIn(t, f.Dir(), args...).mustSucceed(t, args...).json(t)
		assertKeys(t, out, "changeset", "state", "head", "base", "archive_ref", "archive_was",
			"archive_advanced", "squash_safe", "acknowledged_survivors")
		if out["state"] != "APPROVED" {
			t.Errorf("state = %v, want APPROVED: archiving moves a ref, it does not move the state", out["state"])
		}
		if out["head"] != approve {
			t.Errorf("head = %v, want the archived commit %s", out["head"], approve)
		}
		if out["archive_ref"] != archiveRef(slug) {
			t.Errorf("archive_ref = %v, want %s", out["archive_ref"], archiveRef(slug))
		}
		if out["squash_safe"] != true {
			t.Errorf("squash_safe = %v, want true", out["squash_safe"])
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
