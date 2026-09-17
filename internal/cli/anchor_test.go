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
	ref := reviewRef(slug)

	if f.HasRef(ref) {
		t.Fatalf("%s exists before any handoff; nothing has anchored this changeset yet", ref)
	}

	ready(t, f)
	marker := f.Head()
	if got := f.RefSHA(ref); got != marker {
		t.Errorf("%s = %s, want the ready marker %s", ref, got, marker)
	}

	// The point of the anchor: the branch can go and the offered history stays
	// reachable, so a later `change complete` still has a chain to archive.
	f.SwitchTo("main")
	f.ForceDeleteBranch("booking-transaction")
	if _, err := f.Git("cat-file", "-e", marker+"^{commit}"); err != nil {
		t.Errorf("the ready marker became unreachable when the branch went: %v", err)
	}
}

// `status` used to print a slug-derived ref name whether or not the ref existed, so
// `review_ref` could never answer the question it appeared to answer, and the heading
// called every changeset archived. Both are reported as what they are now.
func TestStatusReportsAnchorsOnlyOnceTheyExist(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	ref := reviewRef(slug)

	before := runIn(t, f.Dir(), "status", "--json").json(t)
	if before["review_ref"] != "" {
		t.Errorf("review_ref = %v, want empty for a changeset nothing has anchored", before["review_ref"])
	}
	if before["review_commit"] != "" {
		t.Errorf("review_commit = %v, want empty", before["review_commit"])
	}
	human := runIn(t, f.Dir(), "status")
	human.mustSucceed(t, "status")
	if strings.Contains(human.stdout, "Review anchors") {
		t.Errorf("status listed anchors for a changeset that has none:\n%s", human.stdout)
	}

	ready(t, f)
	after := runIn(t, f.Dir(), "status", "--json").json(t)
	if after["review_ref"] != ref {
		t.Errorf("review_ref = %v, want %s", after["review_ref"], ref)
	}
	if after["review_commit"] != f.Short(f.Head()) {
		t.Errorf("review_commit = %v, want the anchored marker %s", after["review_commit"], f.Short(f.Head()))
	}
	anchored := runIn(t, f.Dir(), "status")
	anchored.mustSucceed(t, "status")
	mustContain(t, anchored.stdout, "movable: "+ref,
		"the movable anchor is listed once it exists")
	if strings.Contains(anchored.stdout, "archive:") {
		t.Errorf("status claimed an archive for a changeset that was never completed:\n%s", anchored.stdout)
	}
}
