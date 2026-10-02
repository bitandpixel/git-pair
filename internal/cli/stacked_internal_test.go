package cli

import (
	"strings"
	"testing"

	"gitpair/internal/changeset"
)

// The two readings a landed parent leaves behind, and the one sentence each produces. They are here rather
// than only in the end-to-end scenarios because both decide a *step*, and a step spelled wrong in a helper
// four commands share is a wrong command in four commands.

func TestApprovalBasePrefersWhatTheSubmissionMeasured(t *testing.T) {
	tests := []struct {
		name       string
		st         parentStatus
		wantCommit string
	}{
		{
			name:       "the base the submission measured from",
			st:         parentStatus{Measured: "aaaaaaa", Recorded: "bbbbbbb"},
			wantCommit: "aaaaaaa",
		},
		{
			name:       "an approval written before the base was recorded",
			st:         parentStatus{Recorded: "bbbbbbb"},
			wantCommit: "bbbbbbb",
		},
		{
			name:       "a parent whose branch was already gone at submission",
			st:         parentStatus{Measured: "aaaaaaa"},
			wantCommit: "aaaaaaa",
		},
		{
			name:       "nothing recorded at all",
			st:         parentStatus{Branch: "alpha", Tip: "ccccccc"},
			wantCommit: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.st.approvalBase(); got != tt.wantCommit {
				t.Errorf("approvalBase() = %q, want %q", got, tt.wantCommit)
			}
		})
	}
}

func TestOwesLandingStep(t *testing.T) {
	tests := []struct {
		name string
		st   parentStatus
		want bool
	}{
		{
			name: "no landing, so nothing landed to step toward",
			st:   parentStatus{Branch: "alpha", Tip: "ccccccc"},
			want: false,
		},
		{
			name: "the branch is here and the head is below the landing",
			st:   parentStatus{Branch: "alpha", Tip: "ccccccc", Landed: "ddddddd"},
			want: true,
		},
		{
			name: "the branch is here and the head is on the landing: the step is the delete",
			st:   parentStatus{Branch: "alpha", Tip: "ccccccc", Landed: "ddddddd", StaleBranch: true, LandingUnderHead: true},
			want: true,
		},
		{
			name: "the branch is gone and the head is on the landing: nothing is owed",
			st:   parentStatus{Branch: "alpha", Landed: "ddddddd", LandingUnderHead: true},
			want: false,
		},
		{
			name: "the branch is gone and the head has not caught up",
			st:   parentStatus{Branch: "alpha", Landed: "ddddddd"},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.st.owesLandingStep(); got != tt.want {
				t.Errorf("owesLandingStep() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The rebase `landedParentStep` prints names the parent's branch. On the branch-is-gone path — the only
// caller that leaves `Tip` empty — that branch does not exist, and a head already carrying the landing has
// no rebase to run in any case. This is the case where the helper has to say so rather than print the
// command that will fail.
func TestLandedParentStepSaysNothingIsOwedWhenTheBranchIsGoneAndTheHeadIsOnTheLanding(t *testing.T) {
	step := landedParentStep(changeset.Changeset{Branch: "beta"},
		parentStatus{Branch: "alpha", Landed: "abcdef1", LandingUnderHead: true})
	if strings.Contains(step, "git rebase --onto") {
		t.Errorf("step = %q, which names the deleted parent branch in a rebase", step)
	}
	if strings.Contains(step, "git branch -D") {
		t.Errorf("step = %q, which advises deleting a branch that is already deleted", step)
	}
	if step == "" {
		t.Error("step is empty: the sentence is printed by four commands and must still say something")
	}
}

func TestLandedParentStepStillNamesTheDeleteAndTheRebase(t *testing.T) {
	deletion := landedParentStep(changeset.Changeset{Branch: "beta"},
		parentStatus{Branch: "alpha", Tip: "1234567", Landed: "abcdef1", StaleBranch: true, LandingUnderHead: true})
	if !strings.Contains(deletion, "git branch -D alpha") {
		t.Errorf("step = %q, want the delete the stale branch leaves", deletion)
	}

	rebase := landedParentStep(changeset.Changeset{Branch: "beta"},
		parentStatus{Branch: "alpha", Tip: "1234567", Landed: "abcdef1"})
	if !strings.Contains(rebase, "git rebase --onto abcdef1 alpha beta") {
		t.Errorf("step = %q, want the rebase onto the landing spelled out", rebase)
	}
}
