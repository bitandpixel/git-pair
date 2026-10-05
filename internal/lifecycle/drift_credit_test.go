package lifecycle_test

import (
	"strings"
	"testing"

	"gitpair/internal/gittest"
	"gitpair/internal/model"
)

// The rule these tests ask is the one PRD §10.4 states: content the review never saw is drift, and the
// destination is credited for the content it carries. Taking the integration branch in moves files under
// an approved branch and is not the author's work; a file that arrived from an ancestor that has not
// landed is. Both readings are asked of the same two commits, so the difference between them has to be
// the content, not who wrote it.

// sharedBody is long enough that two edits at opposite ends are two hunks: a merge that git can settle by
// itself, which is the shape the credit is for. A conflict is its own test below.
func sharedBody() string {
	out := "header\n"
	for i := 1; i <= 12; i++ {
		out += "line " + string(rune('a'+i-1)) + "\n"
	}
	return out + "the end\n"
}

func replaceLine(s, old, replacement string) string {
	return strings.Replace(s, old, replacement, 1)
}

// A branch that merges the integration branch in has changed files since its marker, and every one of those
// changes is the destination's own content. Refusing it asked a reviewer to read trunk; the credit reads the
// same commits and says what they are.
func TestMergingTheDestinationInIsNotContentTheReviewNeverSaw(t *testing.T) {
	shared := sharedBody()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"), gittest.WithFile("shared.txt", shared))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.CommitReadyMarker("booking")

	f.SwitchTo("main")
	f.Commit("trunk: a note at the bottom", gittest.WithFile("shared.txt", shared+"trunk note\n"))

	f.SwitchTo("booking")
	if _, err := f.Git("merge", "--no-edit", "main"); err != nil {
		t.Fatalf("the fixture's merge should be clean: %v", err)
	}

	got := summarizeAgainstTreeCrediting(t, f, "booking", "main", "HEAD", "main")
	if got.State != model.StateReady {
		t.Fatalf("state = %s (reason %q), want READY: the only content above the marker is main's own",
			got.State, got.Reason)
	}
	if len(got.Drifted) > 0 {
		t.Errorf("drifted = %v, want none", got.Drifted)
	}
	if strings.Join(got.DriftCredited, ",") != "shared.txt" {
		t.Errorf("credited = %v, want shared.txt: the fact has to be reported, not just acted on", got.DriftCredited)
	}
	if !strings.Contains(got.Reason, "content the destination carries") {
		t.Errorf("reason = %q, want it to say what the commits after the marker were made of", got.Reason)
	}

	// Without a destination the credit is not available, and the reading is the one every approval written
	// before it was measured under: refused, because the gate cannot tell trunk's content from the author's.
	bare := summarizeAgainstTreeCrediting(t, f, "booking", "main", "HEAD", "")
	if bare.State != model.StateWorking || len(bare.Drifted) == 0 {
		t.Errorf("state = %s with drift %v, want WORKING on the destination's files where no destination is named",
			bare.State, bare.Drifted)
	}
}

// The credit is for content the destination carries, so content it does not carry is still drift. This is
// the case that keeps the rule honest: the same merge, one extra commit of the author's own.
func TestWhatTheAuthorAddsAfterTheReviewIsStillDrift(t *testing.T) {
	shared := sharedBody()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"), gittest.WithFile("shared.txt", shared))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.CommitReadyMarker("booking")

	f.SwitchTo("main")
	f.Commit("trunk: a note at the bottom", gittest.WithFile("shared.txt", shared+"trunk note\n"))

	f.SwitchTo("booking")
	if _, err := f.Git("merge", "--no-edit", "main"); err != nil {
		t.Fatalf("the fixture's merge should be clean: %v", err)
	}
	f.Commit("the author's own work after the review", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))

	got := summarizeAgainstTreeCrediting(t, f, "booking", "main", "HEAD", "main")
	if got.State != model.StateWorking {
		t.Fatalf("state = %s (reason %q), want WORKING", got.State, got.Reason)
	}
	if strings.Join(got.Drifted, ",") != "service.go" {
		t.Errorf("drifted = %v, want exactly service.go: the destination's file is credited, the author's is not",
			got.Drifted)
	}
	if strings.Join(got.DriftCredited, ",") != "shared.txt" {
		t.Errorf("credited = %v, want shared.txt", got.DriftCredited)
	}
}

// A conflict is the boundary of the whole-branch credit. `merge-tree` produces a tree only for a merge it can
// settle, so head can equal "the reviewed commit plus the destination" only when git did the merging. What a
// person writes to settle a conflict is content no reviewer read, and it arrives by the same route as trunk's
// own content — which is why the credit stops at the merge git would have made.
func TestAHandResolvedConflictIsContentTheReviewNeverSaw(t *testing.T) {
	shared := sharedBody()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"), gittest.WithFile("shared.txt", shared))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.CommitReadyMarker("booking")

	f.SwitchTo("main")
	f.Commit("trunk rewrites a line", gittest.WithFile("shared.txt", replaceLine(shared, "line f\n", "line f, from trunk\n")))

	f.SwitchTo("booking")
	f.Write("shared.txt", replaceLine(shared, "line f\n", "line f, from the branch\n"))
	f.Commit("the branch rewrites the same line")
	if _, err := f.Git("merge", "--no-edit", "main"); err == nil {
		t.Fatal("the fixture was meant to conflict")
	}
	f.Write("shared.txt", replaceLine(shared, "line f\n", "line f, from the branch, and trunk's sentence too\n"))
	f.MustGit("add", "-A")
	f.MustGit("commit", "--no-edit")

	got := summarizeAgainstTreeCrediting(t, f, "booking", "main", "HEAD", "main")
	if got.State != model.StateWorking {
		t.Fatalf("state = %s (reason %q), want WORKING: the resolution is content neither the marker nor main carries",
			got.State, got.Reason)
	}
	if strings.Join(got.Drifted, ",") != "shared.txt" {
		t.Errorf("drifted = %v, want shared.txt", got.Drifted)
	}
	if len(got.DriftCredited) > 0 {
		t.Errorf("credited = %v, want nothing credited", got.DriftCredited)
	}
}

// The whole-branch reading earns its keep where the file-by-file one cannot: a file both sides changed, in
// different places, so that what head carries there is neither the reviewed file nor the destination's file.
// `merge-tree` produces exactly that combination, so head equal to it says the only thing that happened is
// the merge — which is what the reviewer would have said reading the same two commits.
func TestAMergeThatCombinesBothSidesOfAFileIsCreditedAsTheMergeItIs(t *testing.T) {
	shared := sharedBody()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"), gittest.WithFile("shared.txt", shared))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Commit("the branch writes the top of the shared file",
		gittest.WithFile("shared.txt", "the branch's line\n"+shared))
	f.CommitReadyMarker("booking")

	f.SwitchTo("main")
	f.Commit("trunk writes the bottom", gittest.WithFile("shared.txt", shared+"trunk note\n"))

	f.SwitchTo("booking")
	if _, err := f.Git("merge", "--no-edit", "main"); err != nil {
		t.Fatalf("the fixture's merge should be clean: %v", err)
	}

	got := summarizeAgainstTreeCrediting(t, f, "booking", "main", "HEAD", "main")
	if got.State != model.StateReady {
		t.Fatalf("state = %s (reason %q), want READY", got.State, got.Reason)
	}
	if strings.Join(got.DriftCredited, ",") != "shared.txt" {
		t.Errorf("credited = %v, drifted = %v, want shared.txt credited as git's own merge of both sides",
			got.DriftCredited, got.Drifted)
	}
}

// And the boundary of that reading: the same shape, with the branch's edit *after* the marker instead of
// before it. The merge tree git would produce from the reviewed commit and the destination does not contain
// an edit made after the review, so head is not that merge, and the file-by-file comparison — where the file
// matches neither side — is what the gate sees.
func TestAnEditAfterTheMarkerToAFileTheDestinationAlsoChangedIsStillDrift(t *testing.T) {
	shared := sharedBody()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"), gittest.WithFile("shared.txt", shared))
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.CommitReadyMarker("booking")

	f.SwitchTo("main")
	f.Commit("trunk writes the bottom", gittest.WithFile("shared.txt", shared+"trunk note\n"))

	f.SwitchTo("booking")
	if _, err := f.Git("merge", "--no-edit", "main"); err != nil {
		t.Fatalf("the fixture's merge should be clean: %v", err)
	}
	f.Commit("the branch writes the top after the review", gittest.WithFile("shared.txt",
		"the branch's line\n"+shared+"trunk note\n"))

	got := summarizeAgainstTreeCrediting(t, f, "booking", "main", "HEAD", "main")
	if got.State != model.StateWorking {
		t.Fatalf("state = %s (reason %q), want WORKING: content added after the review is drift whatever file it is in",
			got.State, got.Reason)
	}
	if strings.Join(got.Drifted, ",") != "shared.txt" {
		t.Errorf("drifted = %v, want shared.txt", got.Drifted)
	}
}

// Where an ancestor's work came from decides whether a file the branch did not write is drift. A parent that
// has landed put its content in the destination, so the destination vouches for it and the child is credited
// for taking it in. A parent still sitting on its own branch has asked nobody to read that file, and the
// destination cannot be cited for it.
func TestAnAncestorsFileIsCreditedOnlyWhenTheDestinationCarriesIt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		landed bool
		state  model.State
		drift  string
	}{
		{"the parent has landed", true, model.StateReady, ""},
		{"the parent has not landed", false, model.StateWorking, "authhelper.go"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := gittest.New(t)
			f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
			f.CreateBranch("booking")
			f.CommitChangeset("booking", "main")
			f.CreateBranch("booking-tests")
			f.CommitChangeset("booking-tests", "booking")
			f.CommitReadyMarker("booking-tests")

			// The parent's work arrives after the child's marker, which is the ordering that puts a file the
			// review never saw in front of the gate.
			f.SwitchTo("booking")
			f.Commit("booking adds a helper", gittest.WithFile("authhelper.go", "package main\n\nfunc Auth() bool { return true }\n"))
			if tc.landed {
				f.SwitchTo("main")
				f.Commit("main carries booking's helper",
					gittest.WithFile("authhelper.go", "package main\n\nfunc Auth() bool { return true }\n"))
				f.SwitchTo("booking")
			}

			f.SwitchTo("booking-tests")
			if _, err := f.Git("merge", "--no-edit", "booking"); err != nil {
				t.Fatalf("the fixture's merge should be clean: %v", err)
			}

			got := summarizeAgainstTreeCrediting(t, f, "booking-tests", "booking", "HEAD", "main")
			if got.State != tc.state {
				t.Fatalf("state = %s (reason %q), want %s", got.State, got.Reason, tc.state)
			}
			if strings.Join(got.Drifted, ",") != tc.drift {
				t.Errorf("drifted = %v, want %q", got.Drifted, tc.drift)
			}
			if !tc.landed {
				return
			}
			if strings.Join(got.DriftCredited, ",") != "authhelper.go" {
				t.Errorf("credited = %v, want authhelper.go", got.DriftCredited)
			}
		})
	}
}
