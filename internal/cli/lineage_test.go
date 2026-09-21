package cli_test

import (
	"testing"

	"gitpair/internal/gittest"
)

// The rebase rule, from PRD §12 and the requirements' "Rebasing" section: an approval is about a
// commit, and it licenses integration only while that commit is still in this line of history.
// `Review-Head` is what makes the question askable — a rebase rewrites the review commit and
// preserves its message, so the rewritten marker still names the head that is gone from the branch.
//
// The table is the set of shapes history can take after an approval, chosen because they split on
// one property: what the tree comparison can see. A rebase that changes no file passes drift
// cleanly, and that is the case this rule exists for — the headline of the milestone.
func TestCheckRefusesHistoryTheApprovalDidNotReview(t *testing.T) {
	tests := []struct {
		name string
		// after is what happens to the branch between the approval and the gate.
		after func(t *testing.T, f *gittest.Fixture, reviewed, approval string)
		// want is a fragment of the reason the gate must give, "" when it is expected to pass.
		want string
		// notWant names the reason that must not appear. Where the gate refuses for some other
		// reason, this is the assertion that the lineage rule stayed out of it.
		notWant string
	}{
		{
			// The headline. Every SHA on the branch changes; not one file does. The tree
			// comparison has nothing to say, and only the ancestry test refuses.
			name: "a rebase onto a base that changed no file",
			after: func(t *testing.T, f *gittest.Fixture, reviewed, approval string) {
				f.CreateBranch("trunk-advanced", "main")
				f.Commit("nothing to see", gittest.WithEmpty())
				f.SwitchTo("booking-transaction")
				f.MustGit("rebase", "trunk-advanced")
			},
			want: "no longer in this history",
		},
		{
			name: "a rebase onto a base that brought code in",
			after: func(t *testing.T, f *gittest.Fixture, reviewed, approval string) {
				f.SwitchTo("main")
				f.Commit("trunk moves", gittest.WithFile("helper.go", "package main\n"))
				f.SwitchTo("booking-transaction")
				f.MustGit("rebase", "main")
			},
			// Both problems are real and both are named: the history moved, and so did the
			// content the approval spoke about.
			want: "no longer in this history",
		},
		{
			// A merge adds a commit and rewrites none, which is what separates it from the
			// rebase above. The approval still names an ancestor of HEAD, so the gate passes.
			name: "a merge of the base, which rewrites nothing",
			after: func(t *testing.T, f *gittest.Fixture, reviewed, approval string) {
				f.CreateBranch("trunk-advanced", "main")
				f.Commit("nothing to see", gittest.WithEmpty())
				f.SwitchTo("booking-transaction")
				f.MustGit("merge", "--no-edit", "trunk-advanced")
			},
			want: "",
		},
		{
			// The fast-forward case in miniature: history grows above the approval, the
			// approved commit is untouched, and only changeset files moved.
			name: "a changeset-only commit after the approval",
			after: func(t *testing.T, f *gittest.Fixture, reviewed, approval string) {
				f.Commit("note the answer", gittest.WithFile("changesets/booking-transaction/ABOUT.md",
					"# booking-transaction\n\nConcurrency answered.\n"))
			},
			want: "",
		},
		{
			// The negative control. Content moved and history did not, so the gate refuses for
			// the tree and says nothing about lineage.
			name: "an implementation commit after the approval",
			after: func(t *testing.T, f *gittest.Fixture, reviewed, approval string) {
				f.Commit("lock it properly", gittest.WithFile("service.go",
					"package main\n\nfunc Lock() { mu.Lock() }\n"))
			},
			want:    "content outside",
			notWant: "no longer in this history",
		},
		{
			// The branch is moved back under the approval, so the approval is not on the branch
			// at all. That is a changeset marked ready, which is what the reason says — not a
			// rewritten review.
			name: "a branch reset back to the reviewed commit",
			after: func(t *testing.T, f *gittest.Fixture, reviewed, approval string) {
				f.MustGit("reset", "--hard", reviewed)
			},
			want:    "has not been reviewed",
			notWant: "no longer in this history",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, slug, reviewed, approval := approvedChangeset(t)
			tc.after(t, f, reviewed, approval)

			res := runIn(t, f.Dir(), "check")
			if tc.want == "" {
				if res.code != 0 {
					t.Fatalf("check exited %d, want the gate to pass\n%s%s", res.code, res.stdout, res.stderr)
				}
				mustContain(t, res.stdout, "OK: "+slug+" is integration-ready", "the passing verdict")
				return
			}
			if res.code != 1 {
				t.Fatalf("check exited %d, want 1\n%s%s", res.code, res.stdout, res.stderr)
			}
			mustContain(t, res.stdout, tc.want, "the reason the gate gives")
			if tc.notWant != "" {
				mustNotContain(t, res.stdout, tc.notWant, "the reason the gate must not give")
			}
		})
	}
}

// The lineage question is only asked where an approval is standing to be invalidated. A blocked
// changeset refuses for its outcome whatever the history looks like, and feedback refuses unless
// `--allow-feedback` says the review is sufficient — at which point its lineage matters too.
func TestCheckAsksTheRebaseRuleOnlyWhereIntegrationIsPermitted(t *testing.T) {
	rewrite := func(t *testing.T, f *gittest.Fixture) {
		f.CreateBranch("trunk-advanced", "main")
		f.Commit("nothing to see", gittest.WithEmpty())
		f.SwitchTo("booking-transaction")
		f.MustGit("rebase", "trunk-advanced")
	}

	t.Run("blocked", func(t *testing.T) {
		f, _ := newChangeset(t, "booking-transaction", "main")
		ready(t, f)
		submit(t, f, "block")
		rewrite(t, f)

		res := runIn(t, f.Dir(), "check")
		if res.code != 1 {
			t.Fatalf("check exited %d, want 1\n%s%s", res.code, res.stdout, res.stderr)
		}
		mustContain(t, res.stdout, "latest review outcome is blocking", "the reason the block gives")
		mustNotContain(t, res.stdout, "no longer in this history",
			"a refusal for a history nobody was asked to approve of")
	})

	t.Run("feedback", func(t *testing.T) {
		f, _ := newChangeset(t, "booking-transaction", "main")
		ready(t, f)
		submit(t, f, "feedback")
		rewrite(t, f)

		res := runIn(t, f.Dir(), "check")
		mustContain(t, res.stdout, "is feedback, which is non-blocking", "the default policy's reason")
		mustNotContain(t, res.stdout, "no longer in this history",
			"a feedback that does not permit integration has no lineage to lose")

		res = runIn(t, f.Dir(), "check", "--allow-feedback")
		mustContain(t, res.stdout, "no longer in this history",
			"once the feedback is what licenses the merge, the history it reviewed is what matters")
	})
}

// A review commit with no `Review-Head` — one written before the trailer existed, or by hand — names
// no commit, so there is nothing to test ancestry against. Refusing is the conservative reading and
// the only one available: the review commit's own first parent would equal the trailer before a
// rewrite and a rewritten parent after one, so deriving it would let the rule pass in exactly the
// case it exists for.
func TestCheckRefusesAnApprovalThatNamesNoReviewedCommit(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	f.CommitMessage(gittest.ReviewMessage(slug, "approve", ""), gittest.WithEmpty())

	res := runIn(t, f.Dir(), "check")
	if res.code != 1 {
		t.Fatalf("check exited %d, want 1\n%s%s", res.code, res.stdout, res.stderr)
	}
	mustContain(t, res.stdout, "names no reviewed commit", "the reason the unnamed head gives")

	json := runIn(t, f.Dir(), "check", "--json").json(t)
	if _, ok := json["reviewed_head"]; ok {
		t.Errorf("check --json reported reviewed_head for a review that named none: %v", json["reviewed_head"])
	}
}

// The other way the rule can be unanswerable: the commit the review named is not in this clone. In
// CI that is a fetch gap, and it must read as one rather than as a verdict about the work.
func TestCheckRefusesWhenTheReviewedCommitIsAbsent(t *testing.T) {
	f, slug := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	const absent = "1111111111111111111111111111111111111111"
	f.CommitMessage(gittest.ReviewMessage(slug, "approve", absent), gittest.WithEmpty())

	res := runIn(t, f.Dir(), "check")
	if res.code != 1 {
		t.Fatalf("check exited %d, want 1\n%s%s", res.code, res.stdout, res.stderr)
	}
	mustContain(t, res.stdout, absent[:7], "the reason names the commit it could not find")
	mustContain(t, res.stdout, "this repository does not have", "and says which side is missing")

	// The raw value is still what --json reports: a consumer that can fetch has more to work with
	// than a verdict that says it could not look.
	json := runIn(t, f.Dir(), "check", "--json").json(t)
	if got := json["reviewed_head"]; got != absent {
		t.Errorf("reviewed_head = %v, want the value the marker named (%s)", got, absent)
	}
}

// `review submit` records the head it spoke about, and the two readers that explain a verdict print
// it: `status` beside the review it summarises, and `review history` as a column.
func TestReviewSubmitRecordsTheReviewedHead(t *testing.T) {
	f, _ := newChangeset(t, "booking-transaction", "main")
	ready(t, f)
	reviewed := f.Head()
	submit(t, f, "approve")
	approval := f.Head()

	if got := f.Trailers(approval)["Review-Head"]; got != reviewed {
		t.Errorf("Review-Head = %q, want the commit the submission was made against (%s)", got, reviewed)
	}
	if parent := f.RevParse(approval + "^1"); parent != reviewed {
		t.Errorf("the reviewed head %s is not the approval's first parent %s", reviewed, parent)
	}

	status := runIn(t, f.Dir(), "status", "--json").json(t)
	latest, ok := status["latest_review"].(map[string]any)
	if !ok {
		t.Fatalf("status --json latest_review = %v, want an object", status["latest_review"])
	}
	if got := latest["reviewed_head"]; got != reviewed[:7] {
		t.Errorf("latest_review.reviewed_head = %v, want %s", got, reviewed[:7])
	}

	history := runIn(t, f.Dir(), "review", "history", "--json").json(t)
	rows, ok := history["reviews"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("review history --json reviews = %v, want one row", history["reviews"])
	}
	row := rows[0].(map[string]any)
	if got := row["reviewed_head"]; got != reviewed {
		t.Errorf("reviews[0].reviewed_head = %v, want the full SHA %s", got, reviewed)
	}

	// The human surface carries it too, because the gate's refusal names both ends of the
	// comparison and a reviewer reading history wants the same two numbers.
	text := runIn(t, f.Dir(), "review", "history").mustSucceed(t, "review", "history").stdout
	mustContain(t, text, "REVIEWED", "the column header")
	mustContain(t, text, reviewed[:7], "and the commit under it")

	st := runIn(t, f.Dir(), "status").mustSucceed(t, "status").stdout
	mustContain(t, st, "reviewed: "+reviewed[:7], "status names the commit the newest review spoke about")
}
