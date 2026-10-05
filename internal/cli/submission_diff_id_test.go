package cli_test

import (
	"context"
	"testing"

	"gitpair/internal/git"
	"gitpair/internal/model"
)

// `review submit` records the identity of the diff it reviewed beside the head and the base it already
// recorded, so that the gate's content question — is this the diff that was approved? — is one comparison
// rather than an inference from how far the parent and the destination have moved since (PRD §21).
//
// What is asserted here is reproducibility, because a recorded value is only worth its trailer: recomputing
// it from the base the same marker names, with git, must give the digest the marker claims. A value that
// cannot be reproduced from its own marker reads as a comparison that was made when none was.

func TestSubmissionRecordsTheIdentityOfTheDiffItReviewed(t *testing.T) {
	f, slug, reviewed, marker := approvedChangeset(t)

	trailers := f.Trailers(marker)
	version, id, ok := model.ParseDiffID(trailers["Review-Diff-Id"])
	if !ok || version != model.DiffIDVersion {
		t.Fatalf("Review-Diff-Id = %q, want %q:<hex>[+<hex>]", trailers["Review-Diff-Id"], model.DiffIDVersion)
	}
	base := trailers["Review-Base-Head"]
	if base == "" {
		t.Fatal("the submission recorded no base, so no reader could reproduce the digest it recorded")
	}

	repo := &git.Repo{Dir: f.Dir()}
	want, err := repo.DiffIdentity(context.Background(), f.RevParse(base), f.RevParse(reviewed),
		"changesets/"+slug, "changesets/.landed/"+slug)
	if err != nil {
		t.Fatalf("DiffIdentity: %v", err)
	}
	if id.Raw != want.Raw {
		t.Errorf("the marker names digest %s, want %s: the recorded value must follow from the base the marker names",
			id.Raw, want.Raw)
	}
	// The second half is what the gate relaxes with, so a submission that recorded only one would look
	// healthy and quietly keep refusing every merge into a shared file.
	if id.Patch == "" || want.Patch == "" {
		t.Errorf("the rendered half is absent (marker %q, recomputed %q): a submission records both",
			id.Patch, want.Patch)
	}
	if id.Patch != want.Patch {
		t.Errorf("the marker names patch %s, want %s", id.Patch, want.Patch)
	}
}

// The changeset's own directory is outside the recorded identity, because that is where ABOUT.md keeps the
// review threads. Without the exclusion, replying to a comment would change the identity of the approval
// the reply is written into — the reader's own words turning against the review they were part of.
func TestAReplyToTheReviewThreadKeepsTheRecordedIdentity(t *testing.T) {
	f, slug, _, firstMarker := approvedChangeset(t)
	before := f.Trailers(firstMarker)["Review-Diff-Id"]
	if before == "" {
		t.Fatal("the submission recorded no diff identity")
	}

	f.Append("changesets/"+slug+"/ABOUT.md", "\n### comment (author)\n\nfixed\n")
	f.Commit("reply to the review thread")
	ready(t, f)
	submit(t, f, "approve")

	if after := f.Trailers(f.Head())["Review-Diff-Id"]; after != before {
		t.Errorf("the recorded identity moved from %s to %s across a reply to the review thread", before, after)
	}
}

// The identity is of the head the submission spoke about, so two submissions over different work record
// two identities, and the older marker keeps the one it made. A reader asking what an older review looked
// at asks that marker, and it still says.
func TestSubmissionsOverDifferentWorkRecordDifferentIdentities(t *testing.T) {
	f, _, _, firstMarker := approvedChangeset(t)
	before := f.Trailers(firstMarker)["Review-Diff-Id"]

	f.Write("service.go", "package main\n\nfunc Lock() { transaction() }\n")
	f.Commit("author response")
	ready(t, f)
	submit(t, f, "approve")

	if after := f.Trailers(f.Head())["Review-Diff-Id"]; after == before {
		t.Error("two submissions over different content recorded the same identity")
	}
	if still := f.Trailers(firstMarker)["Review-Diff-Id"]; still != before {
		t.Errorf("the first marker's identity changed from %s to %s; a record describes the moment it was written",
			before, still)
	}
}
