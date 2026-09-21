package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
)

// Every lifecycle transition is a commit on the changeset's branch, and nothing else. This is the
// property the moving archive ref used to make untestable: `change ready` used to advance a ref, so
// an offered changeset had a durable name before anyone had landed anything, and the ref had to be
// chased by four different commands to stay pointed at HEAD.
//
// One test per command, rather than one test that loops over the four, because a command added later
// has to be added here deliberately. `durableRefs` asks for the whole `refs/git-pair` namespace, so it
// catches a ref written under a name the test did not think to check.
//
// The other half — that landing writes both refs and completes the pair if it was interrupted — is
// integration_test.go's.

// TestChangeReadyWritesNoRefs is the transition that most looked like it needed a ref: offering a
// changeset is the moment its commits stop being disposable, and a branch deleted before anyone
// reviewed would once have taken the ready marker with it. The answer is the record written at
// landing from the still-checked-out branch, not a ref maintained while the work is in flight.
func TestChangeReadyWritesNoRefs(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	if got := durableRefs(t, f); len(got) != 0 {
		t.Fatalf("durable refs before anything was offered: %v", got)
	}

	ready(t, f)

	if got := durableRefs(t, f); len(got) != 0 {
		t.Errorf("`change ready` wrote %v for %s; the transition is the marker commit, not a ref", got, slug)
	}
	if f.Head() == "" {
		t.Fatal("fixture lost its head")
	}
	if !strings.Contains(f.Subject(f.Head()), "ready") {
		t.Errorf("HEAD is %q; the ready marker is what the transition writes", f.Subject(f.Head()))
	}
}

// TestChangeUnreadyWritesNoRefs: withdrawing is a marker commit like any other. The old model had the
// unready marker move the archive too, so that the withdrawal stayed reachable — which is a property
// of the branch while it exists, and of the record once it does not.
func TestChangeUnreadyWritesNoRefs(t *testing.T) {
	f, slug, _, _ := approvedChangeset(t)
	readyBefore := f.Head()
	runIn(t, f.Dir(), "change", "unready").mustSucceed(t, "change", "unready")
	if got := durableRefs(t, f); len(got) != 0 {
		t.Errorf("`change unready` wrote %v for %s (HEAD moved %s -> %s)", got, slug, shortOf(readyBefore), shortOf(f.Head()))
	}
}

// TestChangeAbandonWritesNoRefs: abandoning ends the changeset, and the ending is a marker commit on
// the branch. It used to also anchor the archive ref on that marker so the ending outlived the branch
// — a job the record does better, because the record is only written for work that was actually
// finished.
func TestChangeAbandonWritesNoRefs(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	runIn(t, f.Dir(), "change", "abandon").mustSucceed(t, "change", "abandon")

	if got := durableRefs(t, f); len(got) != 0 {
		t.Errorf("`change abandon` wrote %v for %s; an abandoned changeset has nothing to record", got, slug)
	}
	out := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)
	// The ending is a field beside `state`, not a state value: `state` keeps its five values and the
	// marker commit stays the whole record of the ending.
	if out["abandoned"] != true {
		t.Errorf("abandoned = %v, want true: the marker commit is the ending", out["abandoned"])
	}
	if out["abandoned_commit"] == "" {
		t.Error("abandoned_commit is empty; the marker that ended the changeset is nameable")
	}
	if out["archive_ref"] != "" || out["archive_commit"] != "" {
		t.Errorf("archive_ref = %v, archive_commit = %v; nothing durable is written for an abandoned changeset",
			out["archive_ref"], out["archive_commit"])
	}
}

// TestReviewSubmitWritesNoRefs: the review submission is a commit carrying trailers, and the previous
// model's "update the archive ref to the resulting exact HEAD immediately after a successful
// submission" is gone. A submitted review whose branch is deleted before landing is a real loss, and
// the design accepts it rather than maintaining a ref to prevent it.
func TestReviewSubmitWritesNoRefs(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	submit(t, f, "approve")

	if got := durableRefs(t, f); len(got) != 0 {
		t.Errorf("`review submit` wrote %v for %s; the submission is the commit with the trailers", got, slug)
	}
	out := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)
	if out["state"] != "APPROVED" {
		t.Errorf("state = %v, want APPROVED: the derivation reads the marker the submission wrote", out["state"])
	}
}

// The queue is the read side of the same rule: an offered changeset with no record is an ordinary
// in-flight changeset, and nothing durable has to exist for it to be listed.
func TestQueueListsInFlightChangesetsWithNoRefs(t *testing.T) {
	// Offered, and nothing since: the queue's subject is a changeset waiting for a reviewer, which
	// is also the moment a durable ref would have been most tempting to write.
	f, slug := newChangeset(t, "booking-transaction", "main")
	ready(t, f)

	if got := durableRefs(t, f); len(got) != 0 {
		t.Fatalf("durable refs for in-flight work: %v", got)
	}
	queue := runIn(t, f.Dir(), "review", "queue").mustSucceed(t, "review", "queue")
	mustContain(t, queue.stdout, slug, "the offered changeset is in the queue with nothing recorded")
}

// status names the recorded refs only once landing has written them. Before that the honest answer to
// "where is the durable record?" is nothing at all, and a slug-derived ref name in the output could
// never answer the question its key appeared to ask.
func TestStatusReportsTheRecordedRefsOnlyOnceLandingHappens(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	ref := archiveRef(slug)

	before := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)
	if before["archive_ref"] != "" || before["archive_commit"] != "" {
		t.Errorf("archive_ref = %v, archive_commit = %v; a changeset nobody has landed has no record",
			before["archive_ref"], before["archive_commit"])
	}
	human := runIn(t, f.Dir(), "status").mustSucceed(t, "status").stdout
	mustNotContain(t, human, ref, "the text surface says nothing about a ref that does not exist")

	// The transitions people used to expect a ref for.
	ready(t, f)
	submit(t, f, "approve")
	still := runIn(t, f.Dir(), "status").mustSucceed(t, "status").stdout
	mustNotContain(t, still, ref, "and an approved, offered changeset still has no record")

	// Landing writes it, and the record is what the branch no longer has to be. The landing carries
	// the changeset directory, as a merge, squash or cherry-pick would: `changesets/` is committed
	// content, and it is what makes a landed changeset nameable without its branch.
	f.SwitchTo("main")
	source := f.RevParse("booking-transaction")
	f.MustGit("checkout", source, "--", changeset.Root+"/"+slug)
	landed := f.Commit("booking-transaction: land the reviewed work", gittest.WithFile("landed.md", "landed\n"))
	runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landed).mustSucceed(t, "integration", "record")
	f.ForceDeleteBranch("booking-transaction")

	after := runIn(t, f.Dir(), "status", "--changeset", slug, "--json").mustSucceed(t, "status").json(t)
	if after["archive_ref"] != ref {
		t.Errorf("archive_ref = %v, want %s", after["archive_ref"], ref)
	}
	if after["archive_commit"] != f.Short(source) {
		t.Errorf("archive_commit = %v, want the recorded head %s", after["archive_commit"], f.Short(source))
	}
	if after["integrated_commit"] != f.Short(landed) {
		t.Errorf("integrated_commit = %v, want %s", after["integrated_commit"], f.Short(landed))
	}
	text := runIn(t, f.Dir(), "status", "--changeset", slug).mustSucceed(t, "status").stdout
	mustContain(t, text, ref, "the text surface names the archive ref once it exists")
	mustContain(t, text, "points at: "+f.Short(source), "and the commit it names")
	mustContain(t, text, reviewref.Integration(slug), "and the integration ref beside it")
}
