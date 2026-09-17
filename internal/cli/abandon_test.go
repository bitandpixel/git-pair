package cli_test

import (
	"path/filepath"
	"testing"

	"gitpair/internal/gittest"
)

// Abandoning is the one ending git-pair can record for itself. Completion means "merged",
// which only the deployment branch knows; abandoning means "not coming back", which the
// author knows the moment they decide it. The record has to outlive the branch, because
// the branch is what gets deleted next.
func TestChangeAbandonRecordsATerminalMarkerAndAnchorsIt(t *testing.T) {
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
	if got := f.RefSHA(archiveRef(slug)); got != sha {
		t.Errorf("%s = %s, want the terminal marker %s", archiveRef(slug), got, sha)
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

// The anchor is the point. Once the branch is gone the queue has to stop talking about the
// changeset entirely, and `status` still has to be able to say how it ended.
func TestChangeAbandonOutlivesTheBranch(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	runIn(t, f.Dir(), "change", "abandon").mustSucceed(t, "change", "abandon")

	f.SwitchTo("main")
	f.ForceDeleteBranch("booking")

	queue := runIn(t, f.Dir(), "review", "queue", "--json").mustSucceed(t, "review", "queue")
	if queueListsChangeset(t, queue, slug) {
		t.Errorf("an abandoned changeset is in the queue:\n%s", queue.stdout)
	}
	if skipped := queue.json(t)["skipped"]; skipped != nil {
		t.Errorf("skipped = %v, want silence about a changeset that ended on purpose", skipped)
	}

	status := runIn(t, f.Dir(), "status", "--changeset", slug, "--json").mustSucceed(t, "status").json(t)
	if status["abandoned"] != true {
		t.Errorf("abandoned = %v, want true, read from the anchor: %v", status["abandoned"], status)
	}
	if status["branch"] != "" {
		t.Errorf("branch = %v, want empty: there is no branch to name", status["branch"])
	}
	if status["state"] != "WORKING" {
		t.Errorf("state = %v, want WORKING", status["state"])
	}
	human := runIn(t, f.Dir(), "status", "--changeset", slug).mustSucceed(t, "status")
	mustContain(t, human.stdout, "Branch: none (read from the review anchor)",
		"a branchless read must say so rather than print an empty branch")
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

// A terminal changeset cannot be restarted by the commands that move state. This is also
// the guard against a branch reusing the name of a changeset that ended: the fresh branch
// carries no markers, and only the anchor still knows how the last one finished.
func TestWriteCommandsRefuseAnAbandonedChangeset(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	res := runIn(t, f.Dir(), "change", "abandon", "--json").mustSucceed(t, "change", "abandon")
	at := f.Short(res.json(t)["abandoned_commit"].(string))

	for _, args := range [][]string{
		{"change", "ready"},
		{"change", "unready"},
		{"review", "submit", "--approve"},
		// Archiving reports squash-safety, so it must not report it for a changeset
		// that will never be taken forward — even though abandoning left the archive
		// sitting exactly on HEAD.
		{"change", "archive"},
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

	// The branch is the primary source and the anchor the fallback, not the reverse:
	// a review ref is an anchor, not a guarantee (PRD §13), so a repository that lost
	// it must still refuse to restart a changeset whose branch says it ended.
	f.MustGit("update-ref", "-d", archiveRef(slug))
	r := runIn(t, f.Dir(), "change", "ready")
	if r.code != exitRefusal {
		t.Errorf("`change ready` with the review ref gone exited %d, want %d\nstderr: %s",
			r.code, exitRefusal, r.stderr)
	}
	mustContain(t, r.stderr, "was abandoned by "+at,
		"the refusal must come from the branch when the anchor is gone")
}

// The anchor is consulted as well as the branch, which is what makes a recreated slug
// refuse instead of starting a clean-looking changeset over a name that already ended.
func TestAbandonedSlugCannotBeReopenedOnARecreatedBranch(t *testing.T) {
	f, slug := newChangeset(t, "booking", "main")
	ready(t, f)
	runIn(t, f.Dir(), "change", "abandon").mustSucceed(t, "change", "abandon")

	f.SwitchTo("main")
	f.ForceDeleteBranch("booking")
	f.CreateBranch("booking")
	f.CommitChangeset(slug, "main")
	f.Commit("implement the new attempt", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))

	r := runIn(t, f.Dir(), "change", "ready")
	if r.code != exitRefusal {
		t.Fatalf("`change ready` on a recreated slug exited %d, want %d\nstdout: %s", r.code, exitRefusal, r.stdout)
	}
	mustContain(t, r.stderr, "was abandoned by", "the refusal must come from the anchored record")
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
