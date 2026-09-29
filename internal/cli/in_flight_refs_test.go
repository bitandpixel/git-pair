package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
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
	if out["landed_commit"] != "" || out["chain_head"] != "" {
		t.Errorf("landed_commit = %v, chain = %v; abandoning a changeset does not put it in the destination",
			out["landed_commit"], out["chain_head"])
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
	queue := runIn(t, f.Dir(), "queue").mustSucceed(t, "queue")
	mustContain(t, queue.stdout, slug, "the offered changeset is in the queue with nothing recorded")
}

// The landed fields say nothing until the destination carries the directory. Before that the honest answer
// to "has this landed?" is the empty string, and a value the command could derive from the slug would
// answer a question the key was never asked: chain_base is only meaningful as an address into the
// destination's history, and there is no such address while the work is in flight.
func TestStatusReportsTheLandingOnlyOnceTheDestinationCarriesIt(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")

	before := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)
	if before["landed"] != false || before["landed_commit"] != "" || before["chain_head"] != "" {
		t.Errorf("landed = %v/%v/%v; a changeset nobody has landed has landed nowhere",
			before["landed"], before["landed_commit"], before["chain_head"])
	}
	human := runIn(t, f.Dir(), "status").mustSucceed(t, "status").stdout
	mustNotContain(t, human, "Landed:", "the text surface says nothing about a landing that has not happened")

	// The transitions people used to expect a ref for.
	ready(t, f)
	submit(t, f, "approve")
	still := runIn(t, f.Dir(), "status").mustSucceed(t, "status").stdout
	mustNotContain(t, still, "Landed:", "and an approved, offered changeset has landed nowhere either")

	// The landing is a merge, so the chain behind the directory is the branch that was reviewed, and the
	// verdict the reviewer left is readable from the destination alone -- the branch is deleted below.
	f.SwitchTo("main")
	f.Commit("main: prepare the destination", gittest.WithFile("notes.md", "notes\n"))
	f.MustGit("merge", "--no-ff", "-m", "booking-transaction: merge the reviewed branch", "booking-transaction")
	landing := f.Head()
	f.ForceDeleteBranch("booking-transaction")

	after := runIn(t, f.Dir(), "status", "--changeset", slug, "--json").mustSucceed(t, "status").json(t)
	if after["landed"] != true {
		t.Fatalf("landed = %v; the destination carries the directory", after["landed"])
	}
	if after["landed_commit"] != f.Short(landing) {
		t.Errorf("landed_commit = %v, want the merge %s", after["landed_commit"], f.Short(landing))
	}
	if after["chain_base"] == "" || after["chain_head"] == "" {
		t.Errorf("chain = %v..%v, want the run the merge carried", after["chain_base"], after["chain_head"])
	}
	if after["reviewed"] != true {
		t.Errorf("reviewed = %v, want true: the approval the branch left is in the chain", after["reviewed"])
	}
	text := runIn(t, f.Dir(), "status", "--changeset", slug).mustSucceed(t, "status").stdout
	mustContain(t, text, "Landed:", "the text surface reports the landing")
	mustContain(t, text, f.Short(landing), "naming the commit the directory arrived in")
	mustContain(t, text, "chain:  "+after["chain_base"].(string), "and the range it read")
	mustContain(t, text, "the chain carries an approval", "and what the chain said")
}
