package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The ID is what git-pair calls the work from here on, so an explicit one has to survive
// the whole path: the directory, `change ready`, `status` and the queue. A name that worked
// only at init would be a second identity, not a replacement for the branch name.
func TestChangeInitHonoursAnExplicitID(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("feature/booking-transaction")

	res := runIn(t, f.Dir(), "init", "--id", "booking-transaction-v2", "--base", "main").
		mustSucceed(t, "init")
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
	queue := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue")
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
// the directory is the identity, so `init` says it is already initialised rather than
// refusing a collision between a changeset and itself. Asking for a separate identity is what
// --id is for, and nothing is ever suffixed to dodge a collision.
func TestChangeInitAdoptsTheDirectoryItInherits(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("feature/booking")
	runIn(t, f.Dir(), "init", "--base", "main").mustSucceed(t, "init")

	f.CreateBranch("feature-booking")

	r := runIn(t, f.Dir(), "init", "--base", "main").mustSucceed(t, "init")
	mustContain(t, r.stdout, "already initialised", "the inherited directory must be named as the answer")

	// A second changeset tied to nothing is refused on that branch, and `--id` does not buy the way past
	// it: the id says what the directory is called, not that two unrelated directories belong together.
	r = runIn(t, f.Dir(), "init", "--id", "feature-booking-2", "--base", "main")
	if r.code != exitUsage {
		t.Fatalf("a second unconnected changeset exited %d, want %d (you invoked init on the wrong branch)\nstderr: %s", r.code, exitUsage, r.stderr)
	}
	mustContain(t, r.stderr, "feature-booking-2", "the refusal names the changeset it would create")
	mustContain(t, r.stderr, "feature-booking", "the refusal names the changeset already on the branch")
	if f.HasWorktreeFile(filepath.Join("changesets", "feature-booking-2", "CHANGESET.yaml")) {
		t.Error("the refused changeset was written anyway")
	}

	// The same id is usable on a branch of its own, which is the way out the refusal gives.
	f.CreateBranch("feature-booking-2", "main")
	runIn(t, f.Dir(), "init", "--id", "feature-booking-2", "--base", "main").mustSucceed(t, "init")
	if !f.HasWorktreeFile(filepath.Join("changesets", "feature-booking-2", "CHANGESET.yaml")) {
		t.Error("an explicit id was not usable on a branch of its own")
	}
}

// An id belongs to the changeset that carries it: in the working tree, on the current branch, or landed in
// the destination. A landed changeset keeps its directory until it is tidied, and a branch that cannot see
// the landing must still refuse to take the name, because two changesets under one id is two sets of records
// with one address.
func TestChangeInitRefusesAnIDALandedChangesetAlreadyUses(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("booking work", gittest.WithFile("b.go", "package main\n"))
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "-m", "booking: merge the branch", "booking")
	f.CreateBranch("second")

	r := runIn(t, f.Dir(), "init", "--id", "booking", "--base", "main")
	if r.code != exitRefusal {
		t.Fatalf("reusing a landed id exited %d, want %d\nstderr: %s", r.code, exitRefusal, r.stderr)
	}
	mustContain(t, r.stderr, "changesets/booking", "the refusal names the directory that holds the id")
	mustContain(t, r.stderr, "is landed on main", "and says where the changeset landed")

	runIn(t, f.Dir(), "init", "--id", "booking-transaction", "--base", "main").mustSucceed(t, "init")
}

// A tidied landing leaves `changesets/.landed/<id>` behind on the integration branch. The id is still spoken
// for: the directory is there, and the work it describes is closed. A new changeset taking the name would put
// two histories under one address, so the refusal is the ordinary one and it names the path.
func TestChangeInitRefusesAnIDALandedDirectoryHolds(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("booking work", gittest.WithFile("b.go", "package main\n"))
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "-m", "booking: merge the branch", "booking")
	f.Commit("main: open the landed shelf", gittest.WithFile("changesets/.landed/README", "tidied landings\n"))
	f.MustGit("mv", "changesets/booking", "changesets/.landed/booking")
	f.Commit("main: tidy the landing", gittest.WithFile("notes.md", "notes\n"))
	f.CreateBranch("second")

	r := runIn(t, f.Dir(), "init", "--id", "booking", "--base", "main")
	if r.code != exitRefusal {
		t.Fatalf("reusing an id a landed directory holds exited %d, want %d\nstderr: %s", r.code, exitRefusal, r.stderr)
	}
	mustContain(t, r.stderr, "is landed on main at changesets/.landed/booking",
		"the refusal names the landed directory that holds the name, and says where it landed")
}

// The branch shape rule, at the door: a branch carries one changeset, plus the ones it is stacked on. Two
// directories that no record ties together arrive when work from another branch comes onto this one, and the CI job
// would merge the second with no approval of its own, so `init` refuses instead of warning (PRD 4).
func TestChangeInitRefusesASecondChangesetThatTiesToNothing(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")
	runIn(t, f.Dir(), "init", "--id", "booking-work", "--base", "main").mustSucceed(t, "init")

	r := runIn(t, f.Dir(), "init", "--id", "booking-work-v2", "--base", "main")
	if r.code != exitUsage {
		t.Fatalf("a second changeset on a carrying branch exited %d, want %d (you invoked init on the wrong branch)\nstderr: %s", r.code, exitUsage, r.stderr)
	}
	out := r.stdout + r.stderr
	for _, want := range []string{"booking-work", "booking-work-v2", "one changeset"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not name %q:\n%s", want, out)
		}
	}
	// Every command in the message has to exist in this build: the exits that are their own commands are the
	// changeset after this one, and a reader sent to a command that does not answer learns nothing.
	for _, want := range []string{"git restore --source=", "--set-base", "git fetch"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal gives no way out containing %q:\n%s", want, out)
		}
	}
	if f.HasWorktreeFile(filepath.Join("changesets", "booking-work-v2", "CHANGESET.yaml")) {
		t.Error("init wrote the changeset it refused")
	}
}

// An id is chosen, not derived, so it is never rewritten to fit. Silently turning
// `booking v2` into `booking-v2` would name refs after a string nobody typed.
func TestChangeInitRejectsAnUnusableID(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("booking")

	for _, id := range []string{"booking v2", "feature/booking", "..", "-booking", "booking--v2"} {
		r := runIn(t, f.Dir(), "init", "--id", id, "--base", "main")
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
	runIn(t, f.Dir(), "init", "--base", "main").mustSucceed(t, "init")

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
	runIn(t, f.Dir(), "init", "--base", "main").mustSucceed(t, "init")

	f.MustGit("rm", "-r", filepath.Join("changesets", "booking"))
	r := runIn(t, f.Dir(), "init", "--base", "main")
	if r.code != exitUsage {
		t.Fatalf("init after an uncommitted deletion exited %d, want %d\nstdout: %s", r.code, exitUsage, r.stdout)
	}
	mustContain(t, r.stderr, "deletion is not committed", "a deletion that is not committed has not released the name")

	f.Commit("retire the booking changeset notes")
	runIn(t, f.Dir(), "init", "--base", "main").mustSucceed(t, "init")
}
