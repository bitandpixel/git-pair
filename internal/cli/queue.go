package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
)

// --- queue ------------------------------------------------------------------

// `queue` is a top-level command rather than `review queue`: the queue is a view over the repository's
// branches, and a reviewer's "what is waiting for me?" is not only a review-side question — an author
// watches it too, and CI reads its JSON. The name says what it is, and one spelling is the rule (plan P3).

type queueEntry struct {
	Changeset   string `json:"changeset"`
	Branch      string `json:"branch"`
	Base        string `json:"base"`
	State       string `json:"state"`
	Head        string `json:"head"`
	ReadyCommit string `json:"ready_commit"`
	ReadyAge    string `json:"ready_age"`
}

// integrationEntry is a changeset whose author has asked for the merge and whose landing nobody has
// recorded. It is a second list rather than a second state inside `ready_for_review` because the two rows
// answer two different people: the review queue is "what is waiting for a reviewer", and this is "what a
// reviewer has already approved and the author has handed over". A dashboard that reads only the first
// would keep showing approved work as if it still needed somebody to look at it.
type integrationEntry struct {
	Changeset string `json:"changeset"`
	Branch    string `json:"branch"`
	Base      string `json:"base"`
	State     string `json:"state"`
	Head      string `json:"head"`
	// IntegrateCommit is the declaration — the commit that says the author is done with this head.
	IntegrateCommit string `json:"integrate_commit"`
	DeclaredAge     string `json:"declared_age"`
	// Destination is the branch the work is asking to land on, which for the child of a landed parent is
	// not the branch its own `base:` names. It is reported because the row exists to be acted on, and an
	// actor who merges into the wrong branch finds out from the record refusing, not from here.
	Destination string `json:"destination"`
}

func newQueueCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "List changesets awaiting review, and those handed over for merging",
		Long: `List every changeset in this repository whose branch is READY, and every
changeset whose author has asked for the merge with ` + "`git pair change integrate`" + `.

Readiness comes from commit history, not a queue file: a changeset is listed
while its branch carries a ready marker that no review submission has answered.
Entries are ordered longest-waiting first.

The second list is the author's half of an automatic merge: approved work with a declaration on its tip,
waiting for whoever owns the destination branch to perform it. git-pair performs nothing itself and writes
nothing durable while work is in flight (PRD §26).

Branches are read from the repository, not from the checked-out directory, so the
queue says the same thing on main as it does on the changeset's own branch. A
changeset whose content has landed in its destination is not listed, and says nothing.

--json is the stable contract for notifications, dashboards, and agent
supervisors.`,
		Example: `  git pair queue
  git pair queue --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReviewQueue(cmd.Context(), a)
		},
	}
	return cmd
}

func runReviewQueue(ctx context.Context, a *app) error {
	repo, err := a.loadRepo(ctx)
	if err != nil {
		return err
	}
	// Branches, not directories. Only a branch can be reviewed, so only a branch
	// can be queued; a changeset directory whose branch is gone is a record rather
	// than work, and the record gets one honest line instead of a warning per slug.
	//
	// One trunk listing serves the whole queue; per branch it costs a tree listing and one batch
	// read. That is what makes "resolve every branch" affordable — the formulation this replaced
	// asked a question per ref for each branch.
	db, err := a.resolveDefaultBranch(ctx, repo)
	if err != nil {
		return err
	}
	scan, err := changeset.ScanBranches(ctx, repo, db)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	noted := map[string]bool{}
	var entries []queueEntry
	// Declarations the author has made and nobody has landed yet. A second list beside the review queue,
	// because the two answer different questions — see integrationEntry.
	var integrations []integrationEntry
	var skipped []string
	// Stacks whose parent has moved on. A note, not a row: see behindParent.
	var stale []string
	unreviewed := a.unreviewedLandings(ctx, repo, db, scan.TrunkIDs)
	for _, br := range scan.Branches {
		if br.Err != nil {
			skipped = append(skipped, br.Branch+" (unreadable changeset metadata: "+br.Err.Error()+")")
			continue
		}
		if br.Resolution.Ambiguous {
			skipped = append(skipped, br.Branch+" ("+changeset.AmbiguityError(br.Resolution).Error()+")")
			continue
		}
		if br.Resolution.Selected == nil {
			continue
		}
		cs := br.Resolution.Selected.Changeset
		cs.Branch = br.Branch
		seen[cs.Slug] = true
		// A changeset the destination already carries has landed, and a review queue has nothing to ask of
		// it. The trunk case is covered by the scan itself; this read catches the landing that went to a
		// branch that is not the default one, where the directory is still absent from trunk and the branch
		// is still here. It is named in the skip note rather than dropped silently, because a changeset that
		// disappears from the queue without a word is a mystery.
		//
		// The note is per changeset and printed once, even though the rows below are per branch: two
		// branches can carry one landed changeset, and "it landed at 4f2b8c1" is one fact, repeated
		// twice for the same reason.
		if dest, derr := changeset.DestinationFor(ctx, repo, cs, db); derr == nil {
			if chain, cerr := changeset.LandedChain(ctx, repo, dest.Ref, cs.Slug); cerr == nil {
				if !noted[cs.Slug] {
					noted[cs.Slug] = true
					skipped = append(skipped, fmt.Sprintf("%s (landed on %s at %s)", cs.Slug, displayRef(dest.Ref), short(chain.Landing)))
				}
				continue
			}
		}
		// One row per branch, because one review lives on one branch. Two branches can carry the same
		// changeset — a copy made to try a different approach, a parent and the child branched off it —
		// and they have different heads, different markers, and often different states. Collapsing them
		// to one row and picking the branch with the newest ready marker made a changeset under review on
		// one branch invisible on the other, which is the reviewer's question the queue exists to answer
		// (requirements, invariant 5).
		// One read of the branch's history answers both queue questions: is this offered for review, and
		// has the author asked for the merge. They are mutually exclusive by construction — a declaration
		// is a marker, so a branch carrying one is not in the state the review row asks for.
		entry, declared, err := branchQueueEntries(ctx, repo, cs, br.Branch, db)
		if err != nil {
			skipped = append(skipped, br.Branch+" ("+err.Error()+")")
			continue
		}
		if declared != nil {
			integrations = append(integrations, *declared)
		}
		if entry != nil {
			// A stacked child whose parent has moved is a review someone is about to read against a
			// base that is no longer current. It is a note and not a row, and not a refusal: a READY
			// changeset has no approval for the parent to invalidate (a review submission would have
			// made the newest marker that review, and the row would be gone), so all the queue can
			// honestly say is that the parent is ahead.
			if behind, err := a.behindParent(ctx, repo, cs, db, entry.Head); err == nil && behind > 0 {
				stale = append(stale, fmt.Sprintf("%s on %s is %s behind parent %s",
					cs.Slug, br.Branch, plural(behind, "commit", "commits"), entry.Base))
			}
			// A landed parent is the case `behindParent` cannot see: it counts parent commits the branch
			// does not have, and a merge into the destination leaves the parent's branch with none.
			// One read of the destination's history says whether the base is finished.
			if note, err := a.landedParentNote(ctx, repo, cs, db, entry.Head); err == nil && note != "" {
				stale = append(stale, note)
			}
			entries = append(entries, *entry)
		}
	}

	// Directories no branch accounts for. Read from HEAD's tree, not the working
	// tree, so the answer does not depend on what happens to be checked out.
	head, err := repo.Head(ctx)
	if err != nil && !git.IsUnknownRevision(err) {
		return err
	}
	dirs, err := changeset.ActiveIDs(ctx, repo, head)
	if err != nil {
		return err
	}
	for _, slug := range dirs {
		if seen[slug] {
			continue
		}
		note, err := classifyOrphan(ctx, repo, db, head, slug)
		if err != nil {
			skipped = append(skipped, slug+" ("+err.Error()+")")
			continue
		}
		if note != "" {
			skipped = append(skipped, note)
		}
	}

	// Longest waiting first. Ties keep the branch order git reports (`for-each-ref` sorts by refname),
	// so a repository with two ready branches and one changeset prints them the same way every run.
	sort.SliceStable(entries, func(i, j int) bool {
		return ageLess(entries[i].ReadyAge, entries[j].ReadyAge)
	})
	// Same rule as the review rows: the request that has been waiting longest is asked about first.
	sort.SliceStable(integrations, func(i, j int) bool {
		return ageLess(integrations[i].DeclaredAge, integrations[j].DeclaredAge)
	})

	if a.json {
		out := map[string]any{
			// Every array here is `[]` rather than null, including `landed_unreviewed`.
			// An empty list is the answer "asked, and none", and a missing key is "this build did not look".
			"ready_for_review":     orEmpty(entries),
			"awaiting_integration": orEmpty(integrations),
			"skipped":              orEmpty(skipped),
			"landed_unreviewed":    unreviewed,
			// The notes the text surface prints to stderr: a row whose parent has landed, or moved.
			// They were prose-only, which left a machine reading the queue with no way to learn that the
			// base a row is being reviewed against has already been integrated.
			"parent_notes": orEmpty(stale),
		}
		return a.emitJSON(out)
	}
	if len(entries) == 0 {
		a.printf("READY FOR REVIEW\n\n  nothing is ready\n")
		a.printUnreviewed(unreviewed, displayRef(db.Ref), true)
		printSkipped(a, skipped)
		printBehindParent(a, stale)
		printAwaitingIntegration(a, integrations)
		return nil
	}
	a.printf("READY FOR REVIEW\n\n")
	for _, e := range entries {
		a.printf("%s\n", e.Changeset)
		// The branch is printed because the row is per branch: the same changeset can be queued twice
		// with different heads, and a reader comparing two rows has to be able to tell them apart.
		a.printf("  branch: %s\n", e.Branch)
		a.printf("  base: %s\n", e.Base)
		a.printf("  ready: %s ago\n", e.ReadyAge)
		a.printf("  head: %s\n", short(e.Head))
		a.printf("\n")
	}
	a.printUnreviewed(unreviewed, displayRef(db.Ref), false)
	printSkipped(a, skipped)
	printBehindParent(a, stale)
	printAwaitingIntegration(a, integrations)
	return nil
}

// printAwaitingIntegration lists the work its author has handed over for the merge. It is a section and not
// a note under the review rows because it is not a qualification of a review row: nothing here is waiting
// for a reviewer, and a list of approved work printed under "READY FOR REVIEW" would tell a reader to look
// at something that has already been looked at.
func printAwaitingIntegration(a *app, entries []integrationEntry) {
	if len(entries) == 0 {
		return
	}
	a.printf("AWAITING INTEGRATION\n\n")
	for _, e := range entries {
		a.printf("%s\n", e.Changeset)
		a.printf("  branch: %s\n", e.Branch)
		// The destination rather than the base, because this is the list somebody acts on: `base` is where
		// the diff starts, and for a child of a landed parent the two are different answers.
		a.printf("  merge into: %s\n", e.Destination)
		a.printf("  declared: %s ago by %s\n", e.DeclaredAge, short(e.IntegrateCommit))
		a.printf("  head: %s\n", short(e.Head))
		a.printf("\n")
	}
}

// printBehindParent says which ready rows sit on a parent that has moved. It goes to the notes stream
// with the other qualifications, because the row itself is a true answer to the queue's question — the
// branch *is* ready — and what the note adds is what the reviewer is about to read: a diff measured
// against a parent that is no longer there.
func printBehindParent(a *app, stale []string) {
	for _, s := range stale {
		a.warn("note: %s\n", s)
	}
}

// classifyOrphan decides what to say about a changeset directory with no branch
// behind it, and returns "" when the honest answer is nothing. Every one of them
// used to print `note: skipped <slug> (no branch matches this changeset directory)`,
// which gave the same warning to two opposite situations: work that was reviewed,
// merged, and had its branch deleted — nothing left for a reviewer to do — and work
// whose branch really did go missing.
//
// The two are told apart without any durable ref. The destination carrying the directory is the first
// answer (a landing, not a disappearance, and the landing report names it under its own heading); the
// directory's own history on this branch is the second (a marker that ended the changeset on purpose is a
// complete answer, and the diff would only repeat it); what is left is content in this branch's directory
// that its base does not hold and no branch carries, which is the disappearance worth reporting.
func classifyOrphan(ctx context.Context, repo *git.Repo, db changeset.DefaultBranchRef,
	head, slug string) (string, error) {
	if db.Ref != "" {
		if present, _ := changeset.CarriesDir(ctx, repo, db.Ref, slug); present {
			return "", nil
		}
	}
	base, err := changeset.BaseAt(ctx, repo, head, slug)
	if err != nil {
		if errors.Is(err, git.ErrUnknownPath) {
			return "", nil
		}
		return "", err
	}
	if base == "" {
		return "", nil
	}
	// An abandoned changeset ended on purpose, and the marker that says so is on this branch. That is the
	// whole answer: nothing is pending (PRD §9.7).
	if summary, err := lifecycle.Summarize(ctx, repo, slug, base, head); err == nil && summary.Abandoned != nil {
		return "", nil
	}
	// Identical changeset content on both sides is what a merge leaves behind, so the comparison decides
	// whether anything is actually missing.
	dir := filepath.Join(changeset.Root, slug)
	changed, err := repo.PathsChanged(ctx, base, head, dir)
	if err != nil {
		return "", err
	}
	if len(changed) == 0 {
		return "", nil
	}
	return fmt.Sprintf("%s (%s is not in %s and no branch carries it)", slug, dir, base), nil
}

func printSkipped(a *app, skipped []string) {
	for _, s := range skipped {
		a.warn("note: skipped %s\n", s)
	}
}

// behindParent counts the parent commits this changeset's branch does not have, or 0 when it is not
// stacked, has no parent branch to compare against, or is already current.
func (a *app) behindParent(ctx context.Context, repo *git.Repo, cs changeset.Changeset,
	db changeset.DefaultBranchRef, head string) (int, error) {
	parent, err := changeset.ParentOf(ctx, repo, cs, db)
	if err != nil || parent.Tip == "" {
		return 0, err
	}
	base, err := repo.MergeBase(ctx, head, parent.Tip)
	if err != nil {
		return 0, err
	}
	if base == parent.Tip {
		return 0, nil
	}
	records, err := repo.LogFields(ctx, head+".."+parent.Tip, "%h")
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

// landedParentNote is the queue's reading of the same fact.
//
// It does not go through parentSinceApproval because that path costs a marker walk of the parent branch,
// which the queue cannot pay once per READY row: a queue is a list of branches, and the parent's history is
// somebody else's cost. What it pays instead is one chain derivation of the parent, and only for a row that
// is stacked on a changeset at all — the destination's first-parent line, bounded by the times the parent's
// directory changed, not by the length of the branch. One containment question then decides which step to
// name.
func (a *app) landedParentNote(ctx context.Context, repo *git.Repo, cs changeset.Changeset,
	db changeset.DefaultBranchRef, head string) (string, error) {
	if cs.BaseChangeset == "" || db.Ref == "" {
		return "", nil
	}
	chain, err := changeset.LandedChain(ctx, repo, db.Ref, cs.BaseChangeset)
	if err != nil {
		// No chain means the destination does not carry the parent, which is the same answer as the old
		// "no record" — and one read of the destination's history is what replaces the index.
		return "", nil
	}
	sha := chain.Landing
	on, err := repo.IsAncestor(ctx, sha, head)
	if err != nil {
		return "", err
	}
	branch := cs.ParentBranch
	if branch == "" {
		branch = cs.BaseChangeset
	}
	// The step is spelled by the same helper `status` and `check` print, with one thing left out: the
	// worktree lookup. The queue is a reviewer's surface and its cost is per row, so it names the command
	// and leaves the blocker to the command the author runs before deleting anything.
	st := parentStatus{Branch: branch, Landed: short(sha), StaleBranch: on}
	return fmt.Sprintf("%s: parent %s landed as %s — %s",
		cs.Slug, cs.BaseChangeset, short(sha), landedParentStep(cs, st)), nil
}

// branchQueueEntries is what one branch contributes to the two queue lists, from one read of its history.
// Either half can be nil: a branch is offered for review, or handed over for the merge, or is neither.
//
// Both ask about one branch because that is the unit the queue reports: markers are appended to a branch,
// so the branch is what is ready or what is declared, and two branches carrying one changeset have two
// answers.
func branchQueueEntries(ctx context.Context, repo *git.Repo, cs changeset.Changeset, branch string,
	db changeset.DefaultBranchRef) (*queueEntry, *integrationEntry, error) {
	summary, err := lifecycle.Summarize(ctx, repo, cs.Slug, cs.Base, branch)
	if err != nil {
		return nil, nil, err
	}
	if summary.Marker == nil {
		return nil, nil, nil
	}
	head, err := repo.RevParse(ctx, branch)
	if err != nil {
		return nil, nil, err
	}
	if summary.State == model.StateReady {
		return &queueEntry{
			Changeset:   cs.Slug,
			Branch:      branch,
			Base:        cs.Base,
			State:       string(summary.State),
			Head:        head,
			ReadyCommit: summary.Marker.SHA,
			ReadyAge:    lifecycle.Age(summary.Marker.When, now()),
		}, nil, nil
	}
	if summary.State != model.StateIntegrating {
		return nil, nil, nil
	}
	// The destination is the one fact a row in this list cannot be acted on without, and it costs a walk
	// only for the branches that got here: an unstacked changeset answers from its own base.
	dest, err := changeset.DestinationFor(ctx, repo, cs, db)
	if err != nil {
		return nil, nil, err
	}
	return nil, &integrationEntry{
		Changeset:       cs.Slug,
		Branch:          branch,
		Base:            cs.Base,
		State:           string(summary.State),
		Head:            head,
		IntegrateCommit: summary.Marker.SHA,
		DeclaredAge:     lifecycle.Age(summary.Marker.When, now()),
		Destination:     displayRef(dest.Ref),
	}, nil
}

// ageLess orders "18m" before "1h" before "2d" so the longest wait comes first.
func ageLess(a, b string) bool {
	return ageSeconds(a) > ageSeconds(b)
}

func ageSeconds(age string) int64 {
	if age == "" {
		return 0
	}
	var n int64
	unit := age[len(age)-1]
	fmt.Sscanf(age[:len(age)-1], "%d", &n)
	switch unit {
	case 's':
		return n
	case 'm':
		return n * 60
	case 'h':
		return n * 3600
	case 'd':
		return n * 86400
	}
	return 0
}
