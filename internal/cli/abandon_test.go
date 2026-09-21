package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// Abandoning is the one ending git-pair can record for itself. Completion means "landed", which only
// the person doing the landing knows; abandoning means "not coming back", which the author knows the
// moment they decide it.
//
// The ending is a marker commit and nothing more. It used to also move the archive ref onto itself so
// the ending would outlive the branch — which meant an abandoned changeset had durable refs, while a
// completed one got its refs from the landing that actually proved something. Now a record exists only
// for work that landed, and the abandoned changeset's history is its branch.
func TestChangeAbandonRecordsATerminalMarker(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)

	res := runIn(t, f.Dir(), "change", "abandon", "--json").mustSucceed(t, "change", "abandon")
	out := res.json(t)
	if out["recorded"] != true {
		t.Errorf("recorded = %v, want true", out["recorded"])
	}
	if out["was"] != "READY" {
		t.Errorf("was = %v, want READY", out["was"])
	}
	if out["state"] != "WORKING" {
		t.Errorf("state = %v, want WORKING: the ending is reported beside the state, not as one", out["state"])
	}
	sha, _ := out["abandoned_commit"].(string)
	if sha == "" {
		t.Fatal("abandoned_commit is empty")
	}
	if f.Head() != sha {
		t.Errorf("abandoned_commit = %s, want the new head %s", sha, f.Head())
	}
	if got := f.Trailers(sha)["Review-State"]; got != "abandoned" {
		t.Errorf("Review-State = %q, want abandoned", got)
	}
	// Nothing durable is written. `slug` is in scope precisely so this fails if a future change
	// decides an ending deserves a ref of its own.
	if got := durableRefs(t, f); len(got) != 0 {
		t.Errorf("`change abandon` wrote %v for %s; an abandoned changeset has nothing to record", got, slug)
	}

	status := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status").json(t)
	if status["abandoned"] != true {
		t.Errorf("abandoned = %v, want true", status["abandoned"])
	}
	if status["abandoned_commit"] != sha {
		t.Errorf("abandoned_commit = %v, want %s", status["abandoned_commit"], sha)
	}
	if action, _ := status["next_action"].(string); action == "" || action[:9] != "abandoned" {
		t.Errorf("next_action = %q, want it to say nothing further is owed", action)
	}
	human := runIn(t, f.Dir(), "status").mustSucceed(t, "status")
	mustContain(t, human.stdout, "abandoned by "+f.Short(sha), "the human output must name the ending")
}

// The consequence of writing no ref, stated as a test: when the branch goes, the abandoned
// changeset's history goes with it. The queue is silent (there is no branch, and the directory is not
// on trunk), and `status` has nothing left to read — no anchor to fall back on, because the only
// durable refs git-pair writes are the ones landing records for work that arrived somewhere.
//
// This is a real loss, and it is the one the design accepts rather than prevent: an abandoned
// changeset is work nobody is going to land, and the alternative was a ref maintained by four
// commands to hold the history of changesets that were never finished.
func TestChangeAbandonHistoryGoesWithTheBranch(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	runIn(t, f.Dir(), "change", "abandon").mustSucceed(t, "change", "abandon")

	f.SwitchTo("main")
	f.ForceDeleteBranch("booking")

	queue := runIn(t, f.Dir(), "review", "queue", "--json").mustSucceed(t, "review", "queue")
	if queueListsChangeset(t, queue, slug) {
		t.Errorf("an abandoned changeset is in the queue:\n%s", queue.stdout)
	}

	res := runIn(t, f.Dir(), "status", "--changeset", slug)
	if res.code == 0 {
		t.Fatalf("status read a changeset whose branch and record are both gone:\n%s", res.stdout)
	}
	mustContain(t, res.stderr, slug, "the refusal names the changeset it could not resolve")
	mustNotContain(t, res.stderr, "abandoned by",
		"and it does not claim an ending it can no longer see")
}

// The queue reads a directory left on the deployment branch, and a directory whose
// anchored history says the work ended is not pending work even when its content no
// longer matches what is in the base — here, a description that was updated on `main`
// after the code was abandoned.
func TestReviewQueueIsSilentAboutAnAbandonedChangesetDirectoryLeftBehind(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	runIn(t, f.Dir(), "change", "abandon").mustSucceed(t, "change", "abandon")

	f.SwitchTo("main")
	f.Write(filepath.Join("changesets", slug, "CHANGESET.yaml"), "base: main\n")
	f.Write(filepath.Join("changesets", slug, "ABOUT.md"), "# booking\n\nAbandoned; the description was updated here.\n")
	f.Commit("keep the notes from the booking attempt")
	f.ForceDeleteBranch("booking")

	queue := runIn(t, f.Dir(), "review", "queue", "--json").mustSucceed(t, "review", "queue")
	if queueListsChangeset(t, queue, slug) {
		t.Errorf("an abandoned changeset is in the queue:\n%s", queue.stdout)
	}
	if skipped := queue.json(t)["skipped"]; skipped != nil {
		t.Errorf("skipped = %v, want silence: the anchor says this changeset ended", skipped)
	}
}

// A terminal changeset cannot be restarted by the commands that move state. The refusal is a reading
// of the branch's own commits: the marker that ended the changeset is in the range the commands
// derive from, and there is no ref involved to be missing, unfetched, or stale.
func TestWriteCommandsRefuseAnAbandonedChangeset(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)
	res := runIn(t, f.Dir(), "change", "abandon", "--json").mustSucceed(t, "change", "abandon")
	at := f.Short(res.json(t)["abandoned_commit"].(string))

	for _, args := range [][]string{
		{"change", "ready"},
		{"change", "unready"},
		{"review", "submit", "--approve"},
	} {
		r := runIn(t, f.Dir(), args...)
		if r.code != exitRefusal {
			t.Errorf("`git pair %v` exited %d, want %d\nstderr: %s", args, r.code, exitRefusal, r.stderr)
			continue
		}
		mustContain(t, r.stderr, "was abandoned by "+at,
			"the refusal must name the marker that ended the changeset")
	}

	// Nothing was recorded by the refusals.
	if f.Head() != res.json(t)["abandoned_commit"] {
		t.Errorf("a refused write still committed: head is %s, want %v", f.Head(), res.json(t)["abandoned_commit"])
	}

	// The branch is the whole source of the ending. There is no anchor to lose, which is what makes
	// the refusal above a reading of the commits rather than a reading of a ref somebody might have
	// failed to fetch.
	if got := durableRefs(t, f); len(got) != 0 {
		t.Errorf("refused writes left durable refs behind: %v", got)
	}
}

// The other half of accepting the loss: with the branch gone there is nothing left to consult, so a
// branch that takes the slug again starts clean. `change init` still refuses to hand out the name
// while a record exists — records only exist for changesets that landed — and the marker-derived
// refusal above only lives as long as the branch that carries it.
//
// Asserted as a passing `change ready` rather than as an absence, so the day a record for abandoned
// work is added, this test is the one that has to change and say why.
func TestAbandonedSlugIsReusableOnceTheBranchIsGone(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	runIn(t, f.Dir(), "change", "abandon").mustSucceed(t, "change", "abandon")

	f.SwitchTo("main")
	f.ForceDeleteBranch("booking")
	f.CreateBranch("booking")
	f.CommitChangeset(slug, "main")
	f.Commit("implement the new attempt", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))

	r := runIn(t, f.Dir(), "change", "ready")
	r.mustSucceed(t, "change", "ready")
	if action := f.Subject(f.Head()); !strings.Contains(action, "ready") {
		t.Errorf("HEAD is %q, want the ready marker: the new attempt is a fresh changeset", action)
	}
}

// Re-running is not an error and must not stack a second marker, so a script can abandon
// unconditionally at the end of a branch.
func TestChangeAbandonIsIdempotent(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	first := runIn(t, f.Dir(), "change", "abandon", "--json").mustSucceed(t, "change", "abandon")
	at := first.json(t)["abandoned_commit"]

	second := runIn(t, f.Dir(), "change", "abandon", "--json").mustSucceed(t, "change", "abandon")
	if second.json(t)["recorded"] != false {
		t.Errorf("recorded = %v, want false: the ending is already recorded", second.json(t)["recorded"])
	}
	if second.json(t)["abandoned_commit"] != at {
		t.Errorf("abandoned_commit = %v, want the original marker %v", second.json(t)["abandoned_commit"], at)
	}
	if f.Head() != at {
		t.Errorf("head moved to %s, want the terminal marker %v", f.Head(), at)
	}
}
