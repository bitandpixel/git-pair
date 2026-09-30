package cli_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// `change tidy` is a rename of a directory the destination already carries. It is the smallest possible
// change — one commit, renames only, nothing else in the diff — and everything the tool says about a
// landed changeset has to hold on both sides of it, because the move is meant to be scenery.

// landedOnMain puts `changesets/<id>/` in main by a merge, and leaves the branch that carried it where it
// is, which is the state tidy exists for: the work is in the destination and the directory is still in
// everybody's way.
func landedOnMain(t *testing.T, f *gittest.Fixture, id string) string {
	t.Helper()
	f.CreateBranch(id)
	f.CommitChangeset(id, "main")
	f.Commit(id+" work", gittest.WithFile(id+".go", "package main\n"))
	landing := landAndRecord(t, f, id, "main")
	f.SwitchTo(id)
	return landing
}

func TestChangeTidyMovesALandedDirectoryAside(t *testing.T) {
	f := newRepo(t)
	landedOnMain(t, f, "alpha")

	res := runIn(t, f.Dir(), "change", "tidy", "alpha")
	res.mustSucceed(t, "change tidy")
	mustContain(t, res.stdout, "changesets/alpha -> changesets/.landed/alpha", "the report names both paths")

	if _, err := f.Git("rev-parse", "HEAD:changesets/alpha/CHANGESET.yaml"); err == nil {
		t.Error("HEAD still carries changesets/alpha/CHANGESET.yaml, so nothing moved")
	}
	if _, err := f.Git("rev-parse", "HEAD:changesets/.landed/alpha/CHANGESET.yaml"); err != nil {
		t.Errorf("HEAD does not carry the landed spelling: %v", err)
	}
	status, err := f.Git("status", "--porcelain")
	if err != nil || strings.TrimSpace(status) != "" {
		t.Errorf("the tidy left the working tree dirty: %q", status)
	}
	names, err := f.Git("show", "--pretty=", "--name-status", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(names), "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "R") {
			t.Errorf("the tidy commit carries %q: the move is a rename, so a reader can follow it", line)
		}
	}
}

// A second run has nothing to do and says so, because "I moved nothing" and "there was nothing to move" are
// different answers and only the first is a failure.
func TestChangeTidySecondRunIsANoOp(t *testing.T) {
	f := newRepo(t)
	landedOnMain(t, f, "alpha")
	runIn(t, f.Dir(), "change", "tidy", "alpha").mustSucceed(t, "change tidy")
	head := f.Head()

	res := runIn(t, f.Dir(), "change", "tidy", "alpha")
	res.mustSucceed(t, "change tidy")
	mustContain(t, res.stdout, "already tidied: changesets/.landed/alpha", "the no-op names the directory it found")
	if f.Head() != head {
		t.Error("the second run committed something")
	}
}

// Not landed is not a no-op. The directory is the record of work that is still open, and moving it would
// hide a changeset from every list that reads `changesets/`.
func TestChangeTidyRefusesAChangesetThatHasNotLanded(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))

	res := runIn(t, f.Dir(), "change", "tidy", "alpha")
	if res.code != exitRefusal {
		t.Fatalf("tidying open work exited %d, want %d\n%s", res.code, exitRefusal, res.stderr)
	}
	mustContain(t, res.stderr, "changeset alpha is not landed on main", "the refusal says what is not true")
	mustContain(t, res.stderr, "status --changeset alpha", "and points at the read that says where it stands")
	if _, err := f.Git("rev-parse", "HEAD:changesets/alpha/CHANGESET.yaml"); err != nil {
		t.Error("the refusal moved the directory anyway")
	}
}

// The refusal this command used to raise here protected nothing, and the plan's M9-era probes are what said
// so: the child measures its diff against a commit, and every reader of `changesets/` takes either
// spelling, so a landed parent can move aside while its child is still open. These two fixtures are the
// probe written as a test - the child's own answers on both sides of the move, and the child's span when
// its work reached into the parent's record. The branch that was pointed at the parent is still reported,
// because it is worth knowing and costs one line.
func TestChangeTidyMovesAParentWhoseChildIsStillOpen(t *testing.T) {
	f := newRepo(t)
	landedOnMain(t, f, "alpha")
	f.SwitchTo("main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")

	// What the child is told before the move, so "nothing changed" is a comparison and not an opinion.
	f.SwitchTo("beta")
	before := runIn(t, f.Dir(), "status", "--json").json(t)
	beforeCheck := runIn(t, f.Dir(), "check", "--json").json(t)
	beforeDiff := runIn(t, f.Dir(), "diff", "--stat").stdout

	f.SwitchTo("main")
	res := runIn(t, f.Dir(), "change", "tidy", "alpha")
	res.mustSucceed(t, "change tidy on a parent with a live child")
	mustContain(t, res.stdout, "changesets/alpha -> changesets/.landed/alpha", "the parent moves")
	mustContain(t, res.stdout, "note: the changeset on beta still records it as its base",
		"and the run names the branch whose open work points at it - the branch, not a changeset id")

	f.SwitchTo("beta")
	after := runIn(t, f.Dir(), "status", "--json").json(t)
	for _, key := range []string{"changeset", "base", "base_ref"} {
		if before[key] != after[key] {
			t.Errorf("status.%s went from %v to %v when the parent's directory moved", key, before[key], after[key])
		}
	}
	afterCheck := runIn(t, f.Dir(), "check", "--json").json(t)
	if beforeCheck["state"] != afterCheck["state"] {
		t.Errorf("check.state went from %v to %v", beforeCheck["state"], afterCheck["state"])
	}
	if diffRes := runIn(t, f.Dir(), "diff", "--stat"); diffRes.stdout != beforeDiff {
		t.Errorf("the child's diff moved under it:\nbefore:\n%s\nafter:\n%s", beforeDiff, diffRes.stdout)
	}
}

// The case the old reason named: the child's own commit reaches into the parent's record, and the parent is
// tidied while that work is open. The span keeps the same content and reports it at the path the file now
// lives at, which is the only difference this command can make to a review.
func TestChangeTidyMovesAParentTheChildEditedWithoutChangingTheSpan(t *testing.T) {
	f := newRepo(t)
	landedOnMain(t, f, "alpha")
	f.SwitchTo("main")
	stackedChangeset(t, f, "beta", "alpha", "alpha", "b.go")
	f.SwitchTo("beta")
	f.Commit("a note the reviewer asked for, in the parent's record",
		gittest.WithFile("changesets/alpha/ABOUT.md", "# alpha\n\nNote added from the child review.\n"))

	before := runIn(t, f.Dir(), "diff", "--stat").stdout
	mustContain(t, before, "changesets/alpha/ABOUT.md", "the span holds the child's edit at the active spelling")

	f.SwitchTo("main")
	out := runIn(t, f.Dir(), "change", "tidy", "alpha", "--json").json(t)
	list, ok := out["changesets"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("changesets = %v, want one entry", out["changesets"])
	}
	item, _ := list[0].(map[string]any)
	note, _ := item["in_flight_on"].([]any)
	if len(note) != 1 || note[0] != "beta" {
		t.Errorf("in_flight_on = %v, want the one branch whose changeset records alpha as its base", item["in_flight_on"])
	}

	// The child catching up with trunk is what brings the move into its own history, which is the case the
	// dropped refusal claimed to prevent.
	f.SwitchTo("beta")
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "catch up with trunk", "main")
	after := runIn(t, f.Dir(), "diff", "--stat").stdout
	mustContain(t, after, "changesets/.landed/alpha/ABOUT.md", "the same edit, reported where the file now lives")
	// The counts column is the claim: the same edit, the same number of lines, reported at the path the
	// file now lives at. That is all the move does to a review, and it is the claim the dropped refusal
	// used to rest on.
	if beforeCounts, afterCounts := statCounts(before, "changesets/alpha/ABOUT.md"),
		statCounts(after, "changesets/.landed/alpha/ABOUT.md"); beforeCounts != afterCounts {
		t.Errorf("the child's edit changed across the move: %q then %q", beforeCounts, afterCounts)
	}
}

// statCounts returns the counts column of the `diff --stat` line for one path, so two runs can be compared
// on what changed and not on how the paths are spelled or the columns padded.
func statCounts(stat, path string) string {
	for _, line := range strings.Split(stat, "\n") {
		if !strings.Contains(line, path) {
			continue
		}
		if i := strings.LastIndex(line, "|"); i >= 0 {
			return strings.TrimSpace(line[i+1:])
		}
	}
	return ""
}

// A dry run reports the plan and changes nothing, because the author's next question after `--dry-run` is
// whether the list is the one they meant.
func TestChangeTidyDryRunCommitsNothing(t *testing.T) {
	f := newRepo(t)
	landedOnMain(t, f, "alpha")
	head := f.Head()

	res := runIn(t, f.Dir(), "change", "tidy", "--all-landed", "--dry-run")
	res.mustSucceed(t, "change tidy --dry-run")
	mustContain(t, res.stdout, "changesets/alpha -> changesets/.landed/alpha", "the dry run names the move it would make")
	mustContain(t, res.stdout, "dry run: nothing committed", "and says it committed nothing")
	if f.Head() != head {
		t.Error("--dry-run committed")
	}
	if _, err := f.Git("rev-parse", "HEAD:changesets/alpha/CHANGESET.yaml"); err != nil {
		t.Error("--dry-run moved the directory")
	}
}

// `--all-landed` is the list, so it takes no ids: a caller that passes both has two different answers to
// give and should be told to pick one before anything is moved.
func TestChangeTidyNeedsAList(t *testing.T) {
	f := newRepo(t)
	landedOnMain(t, f, "alpha")

	res := runIn(t, f.Dir(), "change", "tidy")
	if res.code != exitUsage {
		t.Fatalf("tidy with no ids exited %d, want %d\n%s", res.code, exitUsage, res.stderr)
	}
	mustContain(t, res.stderr, "--all-landed", "the usage error names the flag that answers")

	res = runIn(t, f.Dir(), "change", "tidy", "alpha", "--all-landed")
	if res.code != exitUsage {
		t.Fatalf("ids with --all-landed exited %d, want %d\n%s", res.code, exitUsage, res.stderr)
	}
}

// The machine shape is the same on every outcome: an array that is empty rather than absent, and a commit
// that is empty rather than missing. `landed` is what the reviewer's tooling asks next, so the landed
// spelling has to answer it — the move changes where the directory sits and nothing else.
func TestChangeTidyJSONAndTheSurfacesAfter(t *testing.T) {
	f := newRepo(t)
	landedOnMain(t, f, "alpha")

	out := runIn(t, f.Dir(), "change", "tidy", "alpha", "--json").json(t)
	list, ok := out["changesets"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("changesets = %v, want one entry", out["changesets"])
	}
	item, _ := list[0].(map[string]any)
	if item["id"] != "alpha" || item["to"] != "changesets/.landed/alpha" || item["action"] != "moved" {
		t.Errorf("entry = %v, want the move named by id, destination and action", item)
	}
	if commit, _ := out["commit"].(string); len(commit) < 7 {
		t.Errorf("commit = %v, want the tidy commit", out["commit"])
	}

	res := runIn(t, f.Dir(), "status", "--changeset", "alpha")
	res.mustSucceed(t, "status --changeset alpha")
	mustContain(t, res.stdout, "landed", "the landed changeset is still landed after the move")

	q := runIn(t, f.Dir(), "queue", "--json")
	q.mustSucceed(t, "queue --json")
	if strings.Contains(q.stdout, `"slug": "alpha"`) {
		t.Error("queue lists a tidied landing as work in review")
	}
}

// The empty list is an empty array. A caller that has to handle absence learns nothing from a run that had
// nothing to do.
func TestChangeTidyJSONWithNothingToTidy(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))

	out := runIn(t, f.Dir(), "change", "tidy", "--all-landed", "--json").json(t)
	list, ok := out["changesets"].([]any)
	if !ok {
		t.Fatalf("changesets = %T, want an array even with nothing to do", out["changesets"])
	}
	if len(list) != 0 {
		t.Errorf("changesets = %v, want an empty array for a branch with no landed directory", list)
	}
}
