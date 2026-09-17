package cli_test

import (
	"strings"
	"testing"
)

// Offering a changeset is the moment its commits stop being disposable. Before this
// milestone only a review submission moved the ref, so an offered-but-never-reviewed
// changeset had nothing holding its ready marker alive: a branch that existed only in the
// reflog, or one deleted after a squash merge, could take the marker with it, and the
// archive an agent is told to trust would be describing history that no longer exists.
func TestChangeReadyAnchorsTheHistoryAtTheFirstHandoff(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	ref := archiveRef(slug)

	if f.HasRef(ref) {
		t.Fatalf("%s exists before any handoff; nothing has anchored this changeset yet", ref)
	}

	ready(t, f)
	marker := f.Head()
	if got := f.RefSHA(ref); got != marker {
		t.Errorf("%s = %s, want the ready marker %s", ref, got, marker)
	}

	// The point of the anchor: the branch can go and the offered history stays
	// reachable, so a later `change archive` still has a chain to advance.
	f.SwitchTo("main")
	f.ForceDeleteBranch("booking-transaction")
	if _, err := f.Git("cat-file", "-e", marker+"^{commit}"); err != nil {
		t.Errorf("the ready marker became unreachable when the branch went: %v", err)
	}
}

// `status` used to print a slug-derived ref name whether or not the ref existed, so the
// ref key could never answer the question it appeared to answer. It reports the ref and the
// commit it names only once there is one, and says nothing otherwise: the absence is the
// answer to "has this ever been handed to a reviewer?".
//
// The heading says "archive" for a changeset that has never been reviewed, and that is now
// the honest word: the ref has held the chain since the first handoff, which is what an
// archive is for.
func TestStatusReportsTheArchiveRefOnlyOnceItExists(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	ref := archiveRef(slug)

	before := runIn(t, f.Dir(), "status", "--json").json(t)
	if before["archive_ref"] != "" {
		t.Errorf("archive_ref = %v, want empty for a changeset nothing has handed off", before["archive_ref"])
	}
	if before["archive_commit"] != "" {
		t.Errorf("archive_commit = %v, want empty", before["archive_commit"])
	}
	human := runIn(t, f.Dir(), "status")
	human.mustSucceed(t, "status")
	if strings.Contains(human.stdout, "Review archive") {
		t.Errorf("status listed an archive for a changeset that has no ref:\n%s", human.stdout)
	}

	ready(t, f)
	after := runIn(t, f.Dir(), "status", "--json").json(t)
	if after["archive_ref"] != ref {
		t.Errorf("archive_ref = %v, want %s", after["archive_ref"], ref)
	}
	if after["archive_commit"] != f.Short(f.Head()) {
		t.Errorf("archive_commit = %v, want the anchored marker %s", after["archive_commit"], f.Short(f.Head()))
	}
	anchored := runIn(t, f.Dir(), "status")
	anchored.mustSucceed(t, "status")
	mustContain(t, anchored.stdout, ref,
		"the archive ref is listed once it exists")
	mustContain(t, anchored.stdout, "points at: "+f.Short(f.Head()),
		"the commit it names is listed with it")
}
