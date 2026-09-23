package cli_test

import (
	"testing"

	"gitpair/internal/gittest"
	"gitpair/internal/reviewref"
)

// The derivation names the branch still carrying the changeset directory, and a clone can hold that branch
// under two roots. `git clone` puts every branch at `refs/remotes/origin/<branch>` and creates a local one
// only for the branch it checks out, so a CI runner handed the feature branch by git — which is how a
// runner gets a branch at all — holds the reviewed head nowhere except under `refs/remotes/`. Asking only
// `refs/heads/` told that runner the branch it was standing on did not exist, and asked it for a `--source`
// it had no way to know.
//
// The shape that makes this worth the care: one branch, two spellings. Candidates are keyed by the branch
// a ref names, so the two copies are one candidate and the local one is what gets recorded — a derivation
// that counted `origin/feat/ux` beside `feat/ux` would refuse every pushed branch as ambiguous, for a
// reason nobody reading the refusal would believe.

// landedReview lands the reviewed work on main and returns the two tips the record will name.
func landedReview(t *testing.T, branch string) (*gittest.Fixture, string, string, string) {
	t.Helper()
	f, slug := newChangeset(t, branch, "main")
	ready(t, f)
	submit(t, f, "approve")
	source := f.Head()

	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "Merge "+branch+": the reviewed work", branch)
	landing := f.Head()
	return f, slug, source, landing
}

func TestIntegrationRecordDerivesFromARemoteTrackingBranch(t *testing.T) {
	f, slug, source, landing := landedReview(t, "feat/booked")

	// The CI clone: the branch is only what the fetch brought.
	f.MustGit("update-ref", "refs/remotes/origin/feat/booked", source)
	f.ForceDeleteBranch("feat/booked")

	res := runIn(t, f.Dir(), "integration", "record").mustSucceed(t, "integration", "record")
	mustContain(t, res.stdout, "derived", "the pair still came from the graph, with nothing named for it")
	if got := f.RefSHA(reviewref.Archive(slug)); got != source {
		t.Errorf("archive = %s, want the reviewed head %s, which this clone holds only as a remote-tracking ref", got, source)
	}
	if got := f.RefSHA(reviewref.Integration(slug)); got != landing {
		t.Errorf("integration = %s, want the merge %s", got, landing)
	}
}

func TestIntegrationRecordCountsOneBranchUnderBothSpellings(t *testing.T) {
	f, slug, source, landing := landedReview(t, "feat/booked")

	// The remote-tracking copy is *behind* the local branch on purpose. Keyed on the commit, these are two
	// carriers and the command refuses; keyed on the branch, they are one, and the branch in the working
	// tree is the one whose tip the approval spoke about.
	f.MustGit("update-ref", "refs/remotes/origin/feat/booked", f.RevParse("feat/booked~1"))

	res := runIn(t, f.Dir(), "integration", "record").mustSucceed(t, "integration", "record")
	mustContain(t, res.stdout, "recorded "+shortOf(landing), "one candidate, recorded")
	if got := f.RefSHA(reviewref.Archive(slug)); got != source {
		t.Errorf("archive = %s, want the local tip %s, not the fetched copy behind it", got, source)
	}
}

func TestIntegrationRecordExcludesTheDestinationsRemoteSpelling(t *testing.T) {
	f, slug, source, landing := landedReview(t, "feat/booked")

	// A destination fetched into the clone is still the destination. `main` has moved on since the merge,
	// so the stale `refs/remotes/origin/main` points at a commit the destination exclusion does not catch
	// by SHA — and it carries the changeset directory, because the merge put it there. Matching branches
	// rather than spellings is what keeps the landing out of the candidate list.
	f.Commit("a later commit on trunk", gittest.WithFile("later.md", "later\n"))
	f.MustGit("update-ref", "refs/remotes/origin/main", landing)

	res := runIn(t, f.Dir(), "integration", "record").mustSucceed(t, "integration", "record")
	mustContain(t, res.stdout, "recorded "+shortOf(landing), "the merge is still the landing, once its other spelling is excluded")
	if got := f.RefSHA(reviewref.Archive(slug)); got != source {
		t.Errorf("archive = %s, want the reviewed head %s", got, source)
	}
}
