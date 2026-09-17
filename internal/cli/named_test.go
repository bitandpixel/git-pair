package cli_test

import (
	"strings"
	"testing"
)

// Reading is not writing. `status`, `review history` and `change feedback` answer
// "where does this changeset stand", and there is no reason the answer should require
// checking the branch out — which is the move an agent supervising several changesets
// has to make dozens of times a day, and the one git-pair itself will never make.
func TestReadCommandsCanNameAnotherChangeset(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "feedback")

	// Leave the changeset's branch behind: the named reads must not need it.
	f.CreateBranch("somewhere-else", "main")
	if f.CurrentBranch() != "somewhere-else" {
		t.Fatalf("fixture is on %s, want somewhere-else", f.CurrentBranch())
	}

	status := runIn(t, f.Dir(), "status", "--changeset", slug, "--json").mustSucceed(t, "status", "--changeset", slug)
	out := status.json(t)
	if out["state"] != "FEEDBACK" {
		t.Errorf("state = %v, want FEEDBACK read from the other branch", out["state"])
	}
	if out["branch"] != slug {
		t.Errorf("branch = %v, want the branch the answer came from", out["branch"])
	}
	if out["changeset"] != slug {
		t.Errorf("changeset = %v, want %s", out["changeset"], slug)
	}
	if reason, _ := out["reason"].(string); reason == "" {
		t.Error("reason is empty; the read must explain the state it derived")
	}
	if err := runIn(t, f.Dir(), "review", "history", "--changeset", slug, "--json"); err.code != 0 {
		t.Errorf("review history --changeset exited %d: %s", err.code, err.stderr)
	}
	if err := runIn(t, f.Dir(), "change", "feedback", "--changeset", slug); err.code != 0 {
		t.Errorf("change feedback --changeset exited %d: %s", err.code, err.stderr)
	}
}

// Two fields describe the checkout rather than the commit, so for a changeset read from
// elsewhere they report that they do not know. `false` for `uncommitted` would claim a
// clean tree that belongs to a different branch, and a span label would name a HEAD that
// is not this changeset's.
func TestStatusFromElsewhereReportsWhatItCannotKnow(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	f.CreateBranch("somewhere-else", "main")
	f.Write("dirty.go", "package main\n\nfunc D() {}\n")

	out := runIn(t, f.Dir(), "status", "--changeset", slug, "--json").mustSucceed(t, "status").json(t)
	if v, ok := out["uncommitted"]; !ok || v != nil {
		t.Errorf("uncommitted = %v (%t), want null: the dirty tree belongs to somewhere-else", v, v)
	}
	if span, _ := out["span"].(string); span != "" {
		t.Errorf("span = %q, want empty for a changeset that is not checked out", span)
	}
	if action, _ := out["next_action"].(string); !strings.Contains(action, "git switch "+slug) {
		t.Errorf("next_action = %q, want it to name the branch to stand on", action)
	}

	human := runIn(t, f.Dir(), "status", "--changeset", slug).mustSucceed(t, "status")
	if strings.Contains(human.stdout, "Uncommitted changes") {
		t.Errorf("status reported the working tree of another branch:\n%s", human.stdout)
	}
}

// The same status run with no flag still reports the tree, so the null above is a
// statement about the named changeset and not a change to the current-branch contract.
func TestStatusStillReportsTheCheckedOutTree(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	out := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)
	if out["uncommitted"] != false {
		t.Errorf("uncommitted = %v, want false for the checked-out changeset", out["uncommitted"])
	}

	f.Write("dirty.go", "package main\n\nfunc D() {}\n")
	dirty := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)
	if dirty["uncommitted"] != true {
		t.Errorf("uncommitted = %v, want true with uncommitted work", dirty["uncommitted"])
	}
}

// A slug that names nothing is a usage error, not a git failure: the caller typed
// something the repository does not have.
func TestStatusRejectsASlugNoBranchCarries(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	res := runIn(t, f.Dir(), "status", "--changeset", "not-a-changeset")
	if res.code != exitUsage {
		t.Errorf("exit = %d, want %d\nstderr: %s", res.code, exitUsage, res.stderr)
	}
	mustContain(t, res.stderr, "not-a-changeset", "the refusal must name the slug it could not resolve")
}

// The flag is deliberately absent on the commands that record something. A marker is a
// commit, and a commit lands where HEAD is: `change ready --changeset other` would write
// a marker onto the branch you are standing on and claim it described the other one.
func TestMarkerCommandsDoNotTakeAChangesetFlag(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	for _, args := range [][]string{
		{"change", "ready", "--changeset", slug},
		{"change", "unready", "--changeset", slug},
		{"change", "complete", "--changeset", slug},
		{"review", "submit", "--approve", "--changeset", slug},
	} {
		res := runIn(t, f.Dir(), args...)
		if res.code != exitUsage {
			t.Errorf("`git pair %v` exited %d, want %d: writes stay on the checked-out branch\nstderr: %s",
				args, res.code, exitUsage, res.stderr)
		}
		mustContain(t, res.stderr, "unknown flag: --changeset",
			"the flag must not exist on a command that records a marker")
	}
}
