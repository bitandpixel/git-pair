package lifecycle

import (
	"testing"

	"gitpair/internal/model"
)

// `change integrate` writes a marker that establishes a state without being a verdict, which is a
// shape this package had no other example of: the declaration says the author handed the work over,
// and the review underneath it stays the thing that permits a merge. These tests pin the derivation
// half of that split. The gate half — `check` reading through the declaration to the verdict under it
// — is `internal/cli/check_test.go`, and the command's own refusals are
// `internal/cli/integrate_test.go`.

func integrate(sha string) Event {
	return Event{
		SHA: sha, Short: short(sha), Kind: KindIntegrate,
		Subject: "git-pair: integrate booking",
	}
}

func integrateHead(sha, head string) Event {
	e := integrate(sha)
	e.ReviewedHead = head
	return e
}

func unready(sha string) Event {
	return Event{SHA: sha, Short: short(sha), Kind: KindUnready, Subject: "git-pair: unready booking"}
}

func abandoned(sha string) Event {
	return Event{SHA: sha, Short: short(sha), Kind: KindAbandoned, Subject: "git-pair: abandon booking"}
}

// TestDeriveIntegrating walks the states reachable from a declaration. The point of the table is the
// second column: a declaration is the newest marker, so it wins the newest-marker rule and moves the
// state, and yet `Reviews`/`LatestReview` keep naming the approval underneath it — which is what lets
// one derivation answer both "has this been handed over" and "what licenses the merge".
func TestDeriveIntegrating(t *testing.T) {
	tests := []struct {
		name        string
		events      []Event
		want        model.State
		stale       bool
		trailing    int
		reviews     int
		integrating string
		abandoned   string
	}{
		{
			name:        "approve then declare",
			events:      []Event{impl("c1"), ready("c2"), review("c3", model.OutcomeApprove), integrate("c4")},
			want:        model.StateIntegrating,
			reviews:     1,
			integrating: "c4",
		},
		{
			// The derivation reads the marker; the *gate* is what refuses a declaration with no
			// verdict under it (`change integrate` runs it, `check` runs it again before the merge).
			// Deriving WORKING here would hide a marker the author wrote.
			name:        "a declaration with no review under it still reads",
			events:      []Event{impl("c1"), ready("c2"), integrate("c3")},
			want:        model.StateIntegrating,
			reviews:     0,
			integrating: "c3",
		},
		{
			// A commit after the declaration does not take the changeset back out of the state —
			// PRD §12's rule, which the declaration inherits rather than redefines. Whether the
			// commit invalidates the *approval* is the tree question `check` asks.
			name:        "implementation after the declaration",
			events:      []Event{review("c1", model.OutcomeApprove), integrate("c2"), impl("c3")},
			want:        model.StateIntegrating,
			stale:       true,
			trailing:    1,
			reviews:     1,
			integrating: "c2",
		},
		{
			// `change unready` is how a declaration is withdrawn. The state goes back to what the
			// markers under the retraction derive, and the superseded declaration stays visible.
			name:        "a retraction supersedes the declaration",
			events:      []Event{review("c1", model.OutcomeApprove), integrate("c2"), unready("c3")},
			want:        model.StateWorking,
			reviews:     1,
			integrating: "c2",
		},
		{
			// A reviewer speaking after the declaration is the newest marker again, so the
			// declaration stops being the answer and the verdict is. `check` follows the same rule,
			// which is why CI re-runs it rather than trusting the marker it was handed.
			name:        "a later review supersedes the declaration",
			events:      []Event{integrate("c1"), review("c2", model.OutcomeBlock)},
			want:        model.StateBlocked,
			reviews:     1,
			integrating: "c1",
		},
		{
			// Re-declaring at a new head is the idempotent retry, and the commit to name is the
			// newest declaration rather than the first one.
			name:        "the newest declaration wins",
			events:      []Event{review("c1", model.OutcomeApprove), integrate("c2"), impl("c3"), integrate("c4")},
			want:        model.StateIntegrating,
			reviews:     1,
			integrating: "c4",
		},
		{
			name:        "abandoning ends it",
			events:      []Event{review("c1", model.OutcomeApprove), integrate("c2"), abandoned("c3")},
			want:        model.StateWorking,
			reviews:     1,
			integrating: "c2",
			abandoned:   "c3",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := derive(tc.events)
			if got.State != tc.want {
				t.Errorf("state = %s, want %s (reason: %s)", got.State, tc.want, got.Reason)
			}
			if got.Stale != tc.stale {
				t.Errorf("stale = %v, want %v", got.Stale, tc.stale)
			}
			if got.Trailing != tc.trailing {
				t.Errorf("trailing = %d, want %d", got.Trailing, tc.trailing)
			}
			if len(got.Reviews) != tc.reviews {
				t.Errorf("reviews = %d, want %d", len(got.Reviews), tc.reviews)
			}
			if sha := eventSHA(got.Integrating); sha != tc.integrating {
				t.Errorf("Integrating = %q, want %q", sha, tc.integrating)
			}
			if sha := eventSHA(got.Abandoned); sha != tc.abandoned {
				t.Errorf("Abandoned = %q, want %q", sha, tc.abandoned)
			}
		})
	}
}

func eventSHA(e *Event) string {
	if e == nil {
		return ""
	}
	return e.SHA
}

// TestParseEventIntegrateMarker pins the trailer reading. `Review-Head` on a declaration is read for
// the same reason it is read on a review: the marker survives a rebase and the commit it names does
// not, so the stale name is the evidence that the declaration speaks about history this branch no
// longer carries.
func TestParseEventIntegrateMarker(t *testing.T) {
	head := "1234567890abcdef1234567890abcdef12345678"
	rec := []string{"c1", "c1", "0", "author", "git-pair: integrate booking",
		"Review-State: integrating\nReview-Changeset: booking\nReview-Head: " + head + "\n"}
	got := parseEvent("booking", rec)
	if got.Kind != KindIntegrate {
		t.Errorf("kind = %s, want integrate", got.Kind)
	}
	if got.UnrecognisedMarker {
		t.Error("a well-formed declaration was read as an unrecognised marker")
	}
	if got.ReviewedHead != head {
		t.Errorf("ReviewedHead = %q, want %q", got.ReviewedHead, head)
	}
	if got.State() != model.StateIntegrating {
		t.Errorf("state = %s, want INTEGRATING", got.State())
	}
	if !got.Marker() {
		t.Error("a declaration is not a marker: it would then not hold the newest-marker rule")
	}
	if got.Kind.String() != "integrate" {
		t.Errorf("Kind.String() = %q", got.Kind.String())
	}

	// A declaration for another changeset is not this changeset's declaration, and reading it as an
	// implementation commit is the conservative answer: it invalidates the marker above it rather than
	// honouring a trailer block that may mean anything.
	other := append([]string{}, rec...)
	other[5] = "Review-State: integrating\nReview-Changeset: alpha\nReview-Head: " + head + "\n"
	if got := parseEvent("booking", other); !got.UnrecognisedMarker || got.Kind != KindImplementation {
		t.Errorf("a declaration naming another changeset read as kind %s, unread=%v", got.Kind, got.UnrecognisedMarker)
	}

	// A head that is not an object id is read as no head named, and the gate reports that rather than
	// resolving a branch name and calling it the reviewer's commitment.
	bad := append([]string{}, rec...)
	bad[5] = "Review-State: integrating\nReview-Changeset: booking\nReview-Head: main\n"
	if got := parseEvent("booking", bad); got.ReviewedHead != "" {
		t.Errorf("ReviewedHead = %q for a head that is not an object id, want it unread", got.ReviewedHead)
	}

	// The value git-pair has never written. Reading it as INTEGRATING would hand the merge gate to a
	// trailer this build does not understand.
	unknown := append([]string{}, rec...)
	unknown[5] = "Review-State: shipping\nReview-Changeset: booking\n"
	if got := parseEvent("booking", unknown); !got.UnrecognisedMarker {
		t.Errorf("`Review-State: shipping` was not read as unrecognised (kind %s)", got.Kind)
	}
}
