package cli_test

import (
	"testing"

	"gitpair/internal/gittest"
)

// Content leaving the tree is as much a change to what the reviewer read as content arriving, and it is the
// change a comparison of names can lose twice over. `git diff` finds renames by default, so a deletion in the
// same commit as a similar new file is reported as one path moving and the deleted path is in neither list;
// and a comparison that asks only "do these two sides differ at this path" calls two paths that both hold
// nothing identical, so a file nobody carries is read as content a cited branch vouched for. Both readings
// were measured on `main` before this change: an approved changeset that deleted a reviewed file stayed
// `ready: true`, with `drift_credited_by` naming a branch that holds nothing at that path.

// The author's own file, reviewed, then deleted after the approval. No ground — neither trunk nor a parent —
// ever carried it, so nothing can say the deletion was somebody else's work, and the pair of commits the
// author wrote (delete one file, add a similar one) is exactly the shape that hid it.
func TestCheckRefusesAContentDeletionNobodyGroundHolds(t *testing.T) {
	f, _ := newChangeset(t, "feature", "main")
	ready(t, f)
	submit(t, f, "approve")

	reviewed := f.Read("service.go")
	f.Remove("service.go")
	f.Commit("drop a reviewed file and write a similar one", gittest.WithFile("service_copy.go", reviewed))

	refuseDeletion(t, f, "service.go")
}

// The same deletion of content the destination does carry. The destination's copy is not an excuse for the
// deletion — the reviewer read the file present — and before `--no-renames` the pairing with a similar new
// file kept the path out of the comparison entirely, so this refusal is the one the flag alone buys.
func TestCheckRefusesADeletionOfContentTheDestinationHolds(t *testing.T) {
	f, _ := newChangeset(t, "feature", "main")
	ready(t, f)
	submit(t, f, "approve")

	tracked := f.Read("main.go")
	f.Remove("main.go")
	f.Commit("drop a file trunk carries and write a similar one", gittest.WithFile("main_copy.go", tracked))

	refuseDeletion(t, f, "main.go")
}

// A deletion the ground made stays credited. Trunk dropped the file, the branch merged trunk in, and the
// content the branch now lacks is content trunk chose to remove — the reading the credit rule exists for, and
// the one a plain "the cited ref must hold the path" rule would wrongly refuse.
func TestCheckCreditsADeletionTheGroundMade(t *testing.T) {
	f := deletionTheGroundMade(t)

	verdict := runIn(t, f.Dir(), "check", "--json").json(t)
	if ready, _ := verdict["ready"].(bool); !ready {
		t.Fatalf("gate refused a deletion the destination made: reasons = %v", verdict["reasons"])
	}
	if by := creditedBy(t, verdict); by["main.go"] != "main" {
		t.Errorf("main.go credited to %q, want main: the reader has to see which branch removed it (%v)",
			by["main.go"], by)
	}
}

// And the ground's deletion stays credited when the branch goes on working after the merge. That second
// commit is what separates the two readings: head is no longer what `git merge-tree` would make of the marker
// and trunk, so the whole-branch reading cannot settle it and the file-by-file reading has to know that trunk
// held the path where the branch was cut. The branch's own new file is drift and is refused — that part is
// ordinary — and what is pinned here is that the merged-in deletion is not refused alongside it.
func TestCheckCreditsADeletionTheGroundMadeWhenTheBranchWentOnWorking(t *testing.T) {
	f := deletionTheGroundMade(t)
	f.Commit("ordinary work after the merge", gittest.WithFile("extra.go", "package main\n\nfunc Extra() {}\n"))

	verdict := runIn(t, f.Dir(), "check", "--json").json(t)
	if ready, _ := verdict["ready"].(bool); ready {
		t.Fatalf("gate passed the branch's own unreviewed file: %v", verdict)
	}
	reasons := reasonText(verdict)
	mustContain(t, reasons, "extra.go", "the refusal has to name the file the branch added itself")
	mustNotContain(t, reasons, "main.go", "the merged-in deletion is trunk's work, not drift")
	if by := creditedBy(t, verdict); by["main.go"] != "main" {
		t.Errorf("main.go credited to %q, want main: the file-by-file reading has to remember that trunk held it"+
			" where the branch was cut (%v)", by["main.go"], by)
	}
}

// deletionTheGroundMade builds an approved changeset on `main`, deletes one of trunk's files on trunk, and
// merges trunk into the branch.
func deletionTheGroundMade(t *testing.T) *gittest.Fixture {
	t.Helper()
	f, _ := newChangeset(t, "feature", "main")
	ready(t, f)
	submit(t, f, "approve")

	f.SwitchTo("main")
	f.Remove("main.go")
	f.Commit("trunk drops a file", gittest.WithFile("trunk_note.go", "package main\n\nfunc Note() {}\n"))
	f.SwitchTo("feature")
	if _, err := f.Git("merge", "--no-edit", "main"); err != nil {
		t.Fatalf("the merge should be clean: %v", err)
	}
	return f
}

// refuseDeletion asks the gate about a path the branch deleted after its approval and requires the refusal to
// name it and credit it to nobody: a deletion that comes back credited is the bug, whatever the verdict says.
func refuseDeletion(t *testing.T, f *gittest.Fixture, path string) {
	t.Helper()
	verdict := runIn(t, f.Dir(), "check", "--json").json(t)
	if ready, _ := verdict["ready"].(bool); ready {
		t.Fatalf("gate passed a post-approval deletion of %s: %v", path, verdict)
	}
	mustContain(t, reasonText(verdict), path, "the refusal has to name the file that left the tree")
	if by := creditedBy(t, verdict); by[path] != "" {
		t.Errorf("%s was credited to %q, want it refused: no ref holds content that is gone from both sides",
			path, by[path])
	}
}
