package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// The ID is what git-pair calls the work from here on, so an explicit one has to survive
// the whole path: the directory, `change ready`, `status` and the queue. A name that worked
// only at init would be a second identity, not a replacement for the branch name.
func TestChangeInitHonoursAnExplicitID(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("feature/booking-transaction")

	res := runIn(t, f.Dir(), "change", "init", "--id", "booking-transaction-v2", "--base", "main").
		mustSucceed(t, "change", "init")
	mustContain(t, res.stdout, filepath.Join("changesets", "booking-transaction-v2"), "init reports the directory it created")

	if got := f.MetadataID("booking-transaction-v2"); got != "booking-transaction-v2" {
		t.Errorf("id = %q, want the id that was typed", got)
	}
	if got := f.MetadataBase("booking-transaction-v2"); got != "main" {
		t.Errorf("base = %q, want main", got)
	}
	if f.HasWorktreeFile(filepath.Join("changesets", "feature-booking-transaction", "CHANGESET.yaml")) {
		t.Error("the branch-derived directory was created as well: --id replaces the default, it does not add to it")
	}

	// Everything downstream resolves the changeset from the directory it carries, so the
	// branch's name is never consulted again.
	runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")
	status := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)
	if status["changeset"] != "booking-transaction-v2" {
		t.Errorf("status changeset = %v, want booking-transaction-v2", status["changeset"])
	}
	queue := runIn(t, f.Dir(), "review", "queue", "--json").mustSucceed(t, "review", "queue")
	if !queueListsChangeset(t, queue, "booking-transaction-v2") {
		t.Errorf("the queue does not carry the id:\n%s", queue.stdout)
	}
	if queueListsChangeset(t, queue, "feature-booking-transaction") {
		t.Errorf("the queue lists the branch name as a changeset:\n%s", queue.stdout)
	}
}

// Two branches whose names normalise to the same id are a collision once the directory
// from the first is in the second's tree, which is what a derived branch and a landed
// changeset both look like. Sibling branches that never share a tree are not: nothing is
// in use yet, and refusing them would invent a conflict git has not been asked about.
// A branch created off a sibling inherits that sibling's changeset directory. When the
// inherited directory's name is also this branch's default id, the branch is that changeset:
// the directory is the identity, so `change init` says it is already initialised rather than
// refusing a collision between a changeset and itself. Asking for a separate identity is what
// --id is for, and nothing is ever suffixed to dodge a collision.
func TestChangeInitAdoptsTheDirectoryItInherits(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("feature/booking")
	runIn(t, f.Dir(), "change", "init", "--base", "main").mustSucceed(t, "change", "init")

	f.CreateBranch("feature-booking")

	r := runIn(t, f.Dir(), "change", "init", "--base", "main").mustSucceed(t, "change", "init")
	mustContain(t, r.stdout, "already initialised", "the inherited directory must be named as the answer")

	// A genuinely second changeset still gets its own identity when it asks for one.
	runIn(t, f.Dir(), "change", "init", "--id", "feature-booking-2", "--base", "main").
		mustSucceed(t, "change", "init")
	if !f.HasWorktreeFile(filepath.Join("changesets", "feature-booking-2", "CHANGESET.yaml")) {
		t.Error("an explicit id was not usable")
	}
}

// A ref outlives its branch, so a name whose refs exist is spoken for even with no
// directory anywhere. Matching must be exact: `booking-transaction` may not be blocked by
// `booking-transaction-v2`, which shares its prefix.
func TestChangeInitRefusesAnIDItsRefsAlreadyUse(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	f.MustGit("update-ref", "refs/git-pair/changesets/booking-transaction-v2/archive", f.Head())

	runIn(t, f.Dir(), "change", "init", "--id", "booking-transaction", "--base", "main").
		mustSucceed(t, "change", "init")

	f.CreateBranch("second")
	r := runIn(t, f.Dir(), "change", "init", "--id", "booking-transaction-v2", "--base", "main")
	if r.code != exitUsage {
		t.Fatalf("reusing a name with refs exited %d, want %d\nstderr: %s", r.code, exitUsage, r.stderr)
	}
	mustContain(t, r.stderr, "refs already exist", "the refusal must say what holds the name")
}

// A branch may hold more than one changeset — that is what a stacked branch that starts its
// own work looks like — so a second id is not refused. It is warned about, because the tool
// now has to order two directories and the ordering is rarely what the author meant.
func TestChangeInitWarnsWhenABranchTakesASecondChangeset(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	runIn(t, f.Dir(), "change", "init", "--id", "booking-work", "--base", "main").mustSucceed(t, "change", "init")

	r := runIn(t, f.Dir(), "change", "init", "--id", "booking-work-v2", "--base", "main").
		mustSucceed(t, "change", "init")
	mustContain(t, r.stderr, `already carries changeset "booking-work"`, "the warning must name what is already here")
	mustContain(t, r.stderr, "--changeset", "the warning must say how to disambiguate")
	for _, id := range []string{"booking-work", "booking-work-v2"} {
		if !f.HasWorktreeFile(filepath.Join("changesets", id, "CHANGESET.yaml")) {
			t.Errorf("changesets/%s/ is missing", id)
		}
	}
}

// An id is chosen, not derived, so it is never rewritten to fit. Silently turning
// `booking v2` into `booking-v2` would name refs after a string nobody typed.
func TestChangeInitRejectsAnUnusableID(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")

	for _, id := range []string{"booking v2", "feature/booking", "..", "-booking", "booking--v2"} {
		r := runIn(t, f.Dir(), "change", "init", "--id", id, "--base", "main")
		if r.code != exitUsage {
			t.Errorf("`--id %q` exited %d, want %d\nstderr: %s", id, r.code, exitUsage, r.stderr)
			continue
		}
		if !strings.Contains(r.stderr, id) {
			t.Errorf("`--id %q` refusal does not name the value:\n%s", id, r.stderr)
		}
		// Pasting a branch name as an id is the likely mistake, and the generic
		// character complaint would not say what to fix.
		if strings.Contains(id, "/") && !strings.Contains(r.stderr, "path separator") {
			t.Errorf("`--id %q` refusal should name the path separator:\n%s", id, r.stderr)
		}
	}
	if f.HasWorktreeFile(filepath.Join("changesets", "booking-v2", "CHANGESET.yaml")) {
		t.Error("an unusable id was normalised and created anyway")
	}
}

// Renaming a branch used to leave its `branch:` claim behind, after which every command
// reported "no changeset for this branch" until someone edited CHANGESET.yaml. The directory
// is the identity, so a rename changes nothing: the changeset still resolves, and the branch
// it is reported on is the one that is checked out.
func TestRenamingABranchDoesNotStrandItsChangeset(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	runIn(t, f.Dir(), "change", "init", "--base", "main").mustSucceed(t, "change", "init")

	f.MustGit("branch", "-m", "booking", "booking-renamed")

	status := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)
	if status["changeset"] != "booking" {
		t.Errorf("changeset = %v, want booking after the branch was renamed", status["changeset"])
	}
	if status["branch"] != "booking-renamed" {
		t.Errorf("branch = %v, want the branch that is checked out", status["branch"])
	}
}

// Retiring a changeset directory is a commit, so the name is free again once that commit
// exists — and still taken while the deletion is only a local edit. This is why the check
// looks at HEAD and not just at the working tree.
func TestChangeInitReleasesAnIDOnlyWhenTheDeletionIsCommitted(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	runIn(t, f.Dir(), "change", "init", "--base", "main").mustSucceed(t, "change", "init")

	f.MustGit("rm", "-r", filepath.Join("changesets", "booking"))
	r := runIn(t, f.Dir(), "change", "init", "--base", "main")
	if r.code != exitUsage {
		t.Fatalf("init after an uncommitted deletion exited %d, want %d\nstdout: %s", r.code, exitUsage, r.stdout)
	}
	mustContain(t, r.stderr, "deletion is not committed", "a deletion that is not committed has not released the name")

	f.Commit("retire the booking changeset notes")
	runIn(t, f.Dir(), "change", "init", "--base", "main").mustSucceed(t, "change", "init")
}
