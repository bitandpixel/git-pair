package lifecycle_test

import (
	"testing"

	"gitpair/internal/gittest"
	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
)

// The recorded diff identity is read the way every other recorded value in a marker is: an absence means
// "this was not said", never "the content moved" (PRD §21). A value carrying a version this build does not
// compute is the same absence, kept visible rather than dropped, because a reader who can see `2:` can ask
// why — and the version check lives on the event rather than at each call site, because every reader has
// to ask it before comparing.

const diffSlug = "booking-transaction"

// markerFrom commits `message` as the newest commit and returns the event the derivation reads from it.
func markerFrom(t *testing.T, message string) lifecycle.Event {
	t.Helper()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.CreateBranch(diffSlug)
	f.CommitChangeset(diffSlug, "main")
	f.CommitMessage(message, gittest.WithEmpty())

	summary := summarize(t, f, diffSlug, "main", "HEAD")
	if summary.Marker == nil {
		t.Fatalf("no marker derived from this history:\n%s", message)
	}
	return *summary.Marker
}

const someDigest = "8f14e45fceea167a5a36dedd4bea2543a2c8dcb0f6f4e0f1cb8e3f2a9d5c1b47"

func TestMarkerKeepsTheRecordedDiffIdentity(t *testing.T) {
	event := markerFrom(t, gittest.ReviewMessageRecorded(diffSlug, "approve", "abc123", "def456", "789abc",
		model.FormatDiffID(model.DiffID{Raw: someDigest})))

	if event.ReviewedDiffID != model.DiffIDVersion+":"+someDigest {
		t.Errorf("ReviewedDiffID = %q, want the value as written", event.ReviewedDiffID)
	}
	version, id, ok := event.DiffID()
	if !ok {
		t.Fatalf("DiffID() reported unreadable for a value this build writes: %q", event.ReviewedDiffID)
	}
	if version != model.DiffIDVersion || id.Raw != someDigest {
		t.Errorf("DiffID() = (%q, %q), want (%q, %q)", version, id.Raw, model.DiffIDVersion, someDigest)
	}
	if id.Patch != "" {
		t.Errorf("patch half = %q for a value that carried none", id.Patch)
	}
}

// A marker written under a future definition is not a content mismatch: the rule behind its value is one
// this build cannot reproduce, so the caller falls back to the comparison it can still make. The raw value
// stays on the event, because "this approval recorded `3:`" is a fact worth printing and "this approval
// recorded nothing" is not.
func TestAFutureVersionReadsAsNoDigest(t *testing.T) {
	event := markerFrom(t, gittest.ReviewMessageRecorded(diffSlug, "approve", "abc123", "def456", "789abc",
		"3:"+someDigest))

	if event.ReviewedDiffID != "3:"+someDigest {
		t.Errorf("ReviewedDiffID = %q, want the value kept verbatim", event.ReviewedDiffID)
	}
	if _, _, ok := event.DiffID(); ok {
		t.Error("DiffID() reported comparable for a version this build does not compute")
	}
}

// The two halves travel in one value, separated by `+`, and are read back as the pair they were written as.
func TestMarkerKeepsBothHalvesOfTheRecordedIdentity(t *testing.T) {
	const somePatch = "1b8852ce3d3becc9f4b1a9d2a3f8a5c2b0a7d4f9e6c1358a2b4d7e0f6a9c2b5d"
	event := markerFrom(t, gittest.ReviewMessageRecorded(diffSlug, "approve", "abc123", "def456", "789abc",
		model.FormatDiffID(model.DiffID{Raw: someDigest, Patch: somePatch})))

	if want := model.DiffIDVersion + ":" + someDigest + "+" + somePatch; event.ReviewedDiffID != want {
		t.Errorf("ReviewedDiffID = %q, want %q", event.ReviewedDiffID, want)
	}
	if _, id, ok := event.DiffID(); !ok || id.Raw != someDigest || id.Patch != somePatch {
		t.Errorf("DiffID() = %#v, ok=%v, want both halves", id, ok)
	}
}

// An approval written when the trailer carried the content identity alone is still an approval: its version
// is known, its half is comparable, and the absence of the other half costs the relaxation rather than the
// approval. Reading `1:` as unreadable would put every approval written before the second half back for
// review by a change nobody made to the work.
func TestAnIdentityFromTheFirstVersionComparesOnItsHalf(t *testing.T) {
	event := markerFrom(t, gittest.ReviewMessageRecorded(diffSlug, "approve", "abc123", "def456", "789abc",
		"1:"+someDigest))

	version, id, ok := event.DiffID()
	if !ok || version != "1" || id.Raw != someDigest || id.Patch != "" {
		t.Errorf("DiffID() = (%q, %#v, %v), want a comparable raw-only identity", version, id, ok)
	}
}

// A value that is not a diff identity at all — a hand-edited marker, a truncated trailer, an empty one —
// reads the same way, and is never read as a digest that failed to match.
func TestAMalformedDiffIdentityReadsAsNoDigest(t *testing.T) {
	for _, value := range []string{
		"", "nonsense", ":" + someDigest, "1:", "1:" + someDigest + "g", "1:ABC",
		someDigest, "+" + someDigest, "2:" + someDigest + "+", "2:" + someDigest + "+xy", "2:+" + someDigest,
	} {
		event := markerFrom(t, gittest.ReviewMessageRecorded(diffSlug, "approve", "abc123", "def456", "789abc", value))
		if _, _, ok := event.DiffID(); ok {
			t.Errorf("DiffID() reported comparable for %q", value)
		}
	}
}
