package cli_test

import (
	"testing"

	"gitpair/internal/reviewref"
)

// The derivation looks for the branch still carrying the changeset directory, and it asks git for the
// local branches with a pattern. `git for-each-ref` matches that pattern path-name aware, so `*` stops at
// a `/`: `refs/heads/*` answers the branches whose name is one component long. Every branch this project
// actually creates is named `feat/…`, `fix/…` or `docs/…`, so on a real repository the derivation was
// handed one branch — the integration branch itself, which it then correctly excluded — and refused to say
// which head had been reviewed about a branch it was standing next to.
//
// The flat-named fixtures could not see this: `booking` matches `refs/heads/*`. The reviewed branch here is
// named the way `git pair init` names it, which is the only shape that exercises the ask.
func TestIntegrationRecordDerivesFromABranchNamedWithASlash(t *testing.T) {
	f, slug := newChangeset(t, "feat/booked", "main")
	if slug != "feat-booked" {
		t.Fatalf("slug = %q, want feat-booked: this test is about the nested branch name", slug)
	}
	ready(t, f)
	submit(t, f, "approve")
	source := f.Head()

	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "Merge feat/booked: the reviewed work", "feat/booked")
	landing := f.Head()

	res := runIn(t, f.Dir(), "integration", "record").mustSucceed(t, "integration", "record")
	mustContain(t, res.stdout, "derived", "the answer says the pair came from the repository, not from a flag")
	if got := f.RefSHA(reviewref.Archive(slug)); got != source {
		t.Errorf("archive = %s, want the reviewed head %s on the nested branch", got, source)
	}
	if got := f.RefSHA(reviewref.Integration(slug)); got != landing {
		t.Errorf("integration = %s, want the merge %s", got, landing)
	}
}
