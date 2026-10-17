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
// not the child's: it has its own changeset, its own approval and its own gate, and the child cannot be
// declared until it has landed.
func TestCheckKeepsTheApprovalWhenAChildTakesItsParentIn(t *testing.T) {
	f := approvedChildOnAnUnlandedParent(t)

	f.SwitchTo("alpha")
	f.Commit("alpha round two", gittest.WithFile("roundtwo.go", "package main\n\nfunc RoundTwo() string { return \"parent's\" }\n"))
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
