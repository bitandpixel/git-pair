package cli_test

import (
	"testing"

	"gitpair/internal/gittest"
)

// These ask the gate the question the credit rule exists to answer, end to end: a child takes its parent
// in the way it takes trunk in, and the approval has to survive it. The pieces are tested apart elsewhere —
// `TestCreditRefs` for who may be cited, the lifecycle's drift tests for what a citation excuses — and what
// they cannot show is that the gate actually threads one into the other, which is where this change lives.

// approvedChildOnAnUnlandedParent builds `alpha` with its own approved changeset and `beta` stacked on it,
// approved and checked out, with nothing landed.
func approvedChildOnAnUnlandedParent(t *testing.T) *gittest.Fixture {
	t.Helper()
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha first", gittest.WithFile("a1.go", "package main\n\nfunc A1() {}\n"))
	ready(t, f)
	submit(t, f, "approve")
	stackedByBase(t, f, "beta", "alpha", "alpha", "b.go")
	ready(t, f)
	submit(t, f, "approve")
	return f
}

// The reading an author gets today for merging an approved-but-unlanded parent into their own branch is a
// re-review, which is the same cost as rewriting the branch and none of the benefit. The parent's content is
// not the child's: it was reviewed on the parent, it has its own changeset and its own gate, and the child
// cannot be declared until it has landed.
func TestCheckKeepsTheApprovalWhenAChildTakesItsParentIn(t *testing.T) {
	f := approvedChildOnAnUnlandedParent(t)

	f.SwitchTo("alpha")
	f.Commit("alpha round two", gittest.WithFile("roundtwo.go", "package main\n\nfunc RoundTwo() string { return \"parent's\" }\n"))
	// The parent's round is reviewed where it belongs, which is the only way its content becomes citable:
	// the child's reviewer never saw these files, and the parent's reviewer is the one who did.
	ready(t, f)
	submit(t, f, "approve")
	f.SwitchTo("beta")
	if _, err := f.Git("merge", "--no-edit", "alpha"); err != nil {
		t.Fatalf("the merge should be clean: %v", err)
	}

	verdict := runIn(t, f.Dir(), "check", "--json").json(t)
	if ready, _ := verdict["ready"].(bool); !ready {
		t.Fatalf("gate refused a parent merge: reasons = %v", verdict["reasons"])
	}
	if state, _ := verdict["state"].(string); state != "APPROVED" {
		t.Errorf("state = %v, want APPROVED", verdict["state"])
	}
	by := creditedBy(t, verdict)
	if by["roundtwo.go"] != "alpha" {
		t.Errorf("roundtwo.go credited to %q, want alpha: the reader has to see which branch vouched (%v)",
			by["roundtwo.go"], by)
	}
}

// The citation is bounded by what a reviewer read, not by what a branch holds. Work the parent took on after
// its approval covers nothing, and the child that merges it in is carrying content no review spoke about —
// the reading that has to cost a re-offer rather than pass silently. It is also the shape an author can
// build on purpose, so the two ways of producing it are pinned together: writing the file on the parent, and
// copying the child's own post-approval commit onto the parent and merging it back.
func TestCheckRefusesWhatTheParentGainedAfterItsApproval(t *testing.T) {
	t.Run("work written on the parent", func(t *testing.T) {
		f := approvedChildOnAnUnlandedParent(t)
		f.SwitchTo("alpha")
		f.Commit("alpha round two, never reviewed", gittest.WithFile("roundtwo.go", "package main\n\nfunc RoundTwo() string { return \"unreviewed\" }\n"))
		f.SwitchTo("beta")
		if _, err := f.Git("merge", "--no-edit", "alpha"); err != nil {
			t.Fatalf("the merge should be clean: %v", err)
		}
		refuseUnreviewedParentContent(t, f, "roundtwo.go")
	})

	t.Run("the child's own commit copied onto the parent", func(t *testing.T) {
		f := approvedChildOnAnUnlandedParent(t)
		// The child writes a file after its approval, which is drift it cannot hide — and then moves that
		// commit onto the parent and merges the parent back, so the file arrives from a cited branch. Copying
		// it does not make a reviewer of it: the parent's approval still speaks about the older commit.
		f.SwitchTo("beta")
		f.Commit("the child adds its own file after the approval",
			gittest.WithFile("copied.go", "package main\n\nfunc Copied() string { return \"laundered\" }\n"))
		copied := f.Head()
		f.SwitchTo("alpha")
		f.MustGit("cherry-pick", copied)
		f.SwitchTo("beta")
		if _, err := f.Git("merge", "--no-edit", "alpha"); err != nil {
			t.Fatalf("the merge should be clean: %v", err)
		}
		refuseUnreviewedParentContent(t, f, "copied.go")
	})
}

// What the credit cites is the content a reviewer of the parent approved, and it stays citable when the
// parent goes on to take that content back. A parent that deletes the file after approving it is work in
// progress on its own branch — unreviewed, unlanded, and refused by its own gate — so it is not the state
// that decides whether the child's copy was ever read. This is the reading the commit-not-a-branch choice
// lands on, recorded here rather than left to fall out of the code: the alternative, asking the parent's
// tip as well, would make a parent's later housekeeping silently revoke a credit its reviewer had already
// given.
func TestCheckStillCreditsContentTheParentHasSinceTakenBack(t *testing.T) {
	f := approvedChildOnAnUnlandedParent(t)

	f.SwitchTo("alpha")
	f.Commit("alpha round two", gittest.WithFile("roundtwo.go", "package main\n\nfunc RoundTwo() string { return \"parent's\" }\n"))
	ready(t, f)
	submit(t, f, "approve")
	f.SwitchTo("beta")
	if _, err := f.Git("merge", "--no-edit", "alpha"); err != nil {
		t.Fatalf("the merge should be clean: %v", err)
	}
	f.SwitchTo("alpha")
	f.MustGit("rm", "roundtwo.go")
	f.Commit("alpha takes the file back after approving it", gittest.WithNoStage())

	f.SwitchTo("beta")
	verdict := runIn(t, f.Dir(), "check", "--json").json(t)
	if ready, _ := verdict["ready"].(bool); !ready {
		t.Fatalf("gate refused content its own reviewer had covered: reasons = %v", verdict["reasons"])
	}
	if by := creditedBy(t, verdict); by["roundtwo.go"] != "alpha" {
		t.Errorf("roundtwo.go credited to %q, want alpha (%v)", by["roundtwo.go"], by)
	}
}

// refuseUnreviewedParentContent is the refusal every parent citation has to earn: the file is named as
// content the reviewer never read, and no branch is recorded as having vouched for it.
func refuseUnreviewedParentContent(t *testing.T, f *gittest.Fixture, path string) {
	t.Helper()
	verdict := runIn(t, f.Dir(), "check", "--json").json(t)
	if ready, _ := verdict["ready"].(bool); ready {
		t.Fatalf("gate passed content no review covered: %v", verdict)
	}
	mustContain(t, reasonText(verdict), path, "the refusal has to name the file the reviewer never read")
	if by := creditedBy(t, verdict); by[path] != "" {
		t.Errorf("%s was credited to %q, want it refused outright", path, by[path])
	}
}

// A parent nobody ever reviewed is not ground either. The credit cites a parent at its approval because the
// approval is the only evidence that anybody read the content; with none, the child is measured against
// unreviewed work and the gate says so.
func TestCheckRefusesAParentThatHasNeverBeenApproved(t *testing.T) {
	f := newRepo(t)
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a1.go", "package main\n\nfunc A1() {}\n"))
	stackedByBase(t, f, "beta", "alpha", "alpha", "b.go")
	ready(t, f)
	submit(t, f, "approve")

	f.SwitchTo("alpha")
	f.Commit("alpha adds a file", gittest.WithFile("unreviewed.go", "package main\n\nfunc Un() string { return \"nobody read this\" }\n"))
	f.SwitchTo("beta")
	if _, err := f.Git("merge", "--no-edit", "alpha"); err != nil {
		t.Fatalf("the merge should be clean: %v", err)
	}
	refuseUnreviewedParentContent(t, f, "unreviewed.go")
}

// Widening the credit to the parent is not widening it to any branch the author cares to merge. A file
// nobody's review covered and no cited branch carries is still content the reviewer never saw, and this is
// the refusal the change is only safe because of.
func TestCheckStillRefusesContentNoCitedBranchCarries(t *testing.T) {
	f := approvedChildOnAnUnlandedParent(t)

	f.SwitchTo("main")
	f.CreateBranch("scratch")
	f.Commit("someone else's experiment", gittest.WithFile("experiment.go", "package main\n\nfunc Secret() string { return \"unreviewed\" }\n"))
	f.SwitchTo("beta")
	if _, err := f.Git("merge", "--no-edit", "scratch"); err != nil {
		t.Fatalf("the merge should be clean: %v", err)
	}

	verdict := runIn(t, f.Dir(), "check", "--json").json(t)
	if ready, _ := verdict["ready"].(bool); ready {
		t.Fatalf("gate passed a branch carrying content no cited ref holds: %v", verdict)
	}
	mustContain(t, reasonText(verdict), "experiment.go",
		"the refusal has to name the file the reviewer never read")
	if by := creditedBy(t, verdict); by["experiment.go"] != "" {
		t.Errorf("experiment.go was credited to %q, want it refused outright", by["experiment.go"])
	}
}

// Once the parent has landed, its branch stops being ground: the name outlived the content, so citing it
// would credit whatever anybody pushes to a finished branch. Anything the parent gained after its landing is
// the child's problem again, and the destination is the only ref left to cite.
func TestCheckDoesNotCreditALandedParentsLaterWork(t *testing.T) {
	f := approvedChildOnAnUnlandedParent(t)

	landApproved(t, f, "alpha", "main")

	f.SwitchTo("alpha")
	f.Commit("alpha round two after landing", gittest.WithFile("roundtwo.go", "package main\n\nfunc RoundTwo() string { return \"only here\" }\n"))
	f.SwitchTo("beta")
	if _, err := f.Git("merge", "--no-edit", "alpha"); err != nil {
		t.Fatalf("the merge should be clean: %v", err)
	}

	verdict := runIn(t, f.Dir(), "check", "--json").json(t)
	if ready, _ := verdict["ready"].(bool); ready {
		t.Fatalf("gate passed content that is on no branch the destination vouches for: %v", verdict)
	}
	mustContain(t, reasonText(verdict), "roundtwo.go", "the refusal has to name the file")
	// The landed parent's refusal comes from the stack condition as well as from the credit, so the reason
	// alone cannot tell the two apart: only the absence of an attribution can.
	if by := creditedBy(t, verdict); by["roundtwo.go"] != "" {
		t.Errorf("roundtwo.go was credited to %q, want a finished branch to vouch for nothing",
			by["roundtwo.go"])
	}
}

// creditedBy reads `drift_credited_by`: the branch whose content accounted for each excused path.
func creditedBy(t *testing.T, verdict map[string]any) map[string]string {
	t.Helper()
	raw, ok := verdict["drift_credited_by"].(map[string]any)
	if !ok {
		return map[string]string{}
	}
	out := make(map[string]string, len(raw))
	for path, name := range raw {
		out[path], _ = name.(string)
	}
	return out
}

// reasonText joins `reasons` so a test can ask one question of the whole verdict instead of indexing a
// sentence that is allowed to be rewritten.
func reasonText(verdict map[string]any) string {
	out := ""
	if reasons, ok := verdict["reasons"].([]any); ok {
		for _, r := range reasons {
			if s, ok := r.(string); ok {
				out += s + "\n"
			}
		}
	}
	return out
}
