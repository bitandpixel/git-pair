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
		model.FormatDiffID(someDigest)))

	if event.ReviewedDiffID != model.DiffIDVersion+":"+someDigest {
		t.Errorf("ReviewedDiffID = %q, want the value as written", event.ReviewedDiffID)
	}
	version, digest, ok := event.DiffID()
	if !ok {
		t.Fatalf("DiffID() reported unreadable for a value this build writes: %q", event.ReviewedDiffID)
	}
	if version != model.DiffIDVersion || digest != someDigest {
		t.Errorf("DiffID() = (%q, %q), want (%q, %q)", version, digest, model.DiffIDVersion, someDigest)
	}
}

// A marker written under a future definition is not a content mismatch: the rule behind its value is one
// this build cannot reproduce, so the caller falls back to the comparison it can still make. The raw value
// stays on the event, because "this approval recorded `2:`" is a fact worth printing and "this approval
// recorded nothing" is not.
func TestAFutureVersionReadsAsNoDigest(t *testing.T) {
	event := markerFrom(t, gittest.ReviewMessageRecorded(diffSlug, "approve", "abc123", "def456", "789abc",
		"2:"+someDigest))

	if event.ReviewedDiffID != "2:"+someDigest {
		t.Errorf("ReviewedDiffID = %q, want the value kept verbatim", event.ReviewedDiffID)
	}
	if _, _, ok := event.DiffID(); ok {
		t.Error("DiffID() reported comparable for a version this build does not compute")
	}
}

// A value that is not a diff identity at all — a hand-edited marker, a truncated trailer, an empty one —
// reads the same way, and is never read as a digest that failed to match.
func TestAMalformedDiffIdentityReadsAsNoDigest(t *testing.T) {
	for _, value := range []string{"", "nonsense", ":" + someDigest, "1:", "1:" + someDigest + "g", "1:ABC"} {
		event := markerFrom(t, gittest.ReviewMessageRecorded(diffSlug, "approve", "abc123", "def456", "789abc", value))
		if _, _, ok := event.DiffID(); ok {
			t.Errorf("DiffID() reported comparable for %q", value)
		}
	}
}
