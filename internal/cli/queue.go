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

func newQueueCommand(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "queue",
		Short: "List changesets ready for human review",
		Long: `List every changeset in this repository whose branch is READY.

Readiness comes from commit history, not a queue file: a changeset is listed
while its branch carries a ready marker that no review submission has answered.
Entries are ordered longest-waiting first.

Branches are read from the repository, not from the checked-out directory, so the
queue says the same thing on main as it does on the changeset's own branch. A
changeset whose content has landed in its base is not listed, and says nothing.

--json is the stable contract for notifications, dashboards, and agent
supervisors.`,
		Example: `  git pair queue
  git pair queue --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReviewQueue(cmd.Context(), a)
		},
	}
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
	// asked a question per durable ref for each branch.
	db, err := changeset.DefaultBranch(ctx, repo, a.defaultBranch)
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
	var skipped []string
	// Stacks whose parent has moved on. A note, not a row: see behindParent.
	var stale []string
	// One read of the durable namespace for the whole command. Three things in here are questions about
	// those refs — has this changeset landed, does this orphan have a chain to read, which landings carry
	// no record — and each used to ask git separately, once per changeset. The queue's cost now follows
	// the branches, not the number of changesets the repository has ever had.
	durable, err := indexDurableRefs(ctx, repo)
	if err != nil {
		return err
	}
	unrecorded := durable.unrecordedLandings(scan.TrunkIDs)
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
		// A changeset with an integration ref has landed, and a review queue has nothing to ask of
		// it. This is the case the queue could not answer before the record existed: the landing
		// went to a branch that is not the default one, so the directory is still absent from trunk
		// and the tree rule still reads it as live work. It is named in the skip note rather than
		// dropped silently, because unlike a trunk landing this branch is still here and its
		// disappearance from the queue would otherwise be a mystery.
		//
		// The note is per changeset and printed once, even though the rows below are per branch: two
		// branches can carry one landed changeset, and "it landed at 4f2b8c1" is one fact, repeated
		// twice for the same reason.
		if sha, ok := durable.Integrated[cs.Slug]; ok {
			if !noted[cs.Slug] {
				noted[cs.Slug] = true
				skipped = append(skipped, fmt.Sprintf("%s (integrated at %s)", cs.Slug, short(sha)))
			}
			continue
		}
		// One row per branch, because one review lives on one branch. Two branches can carry the same
		// changeset — a copy made to try a different approach, a parent and the child branched off it —
		// and they have different heads, different markers, and often different states. Collapsing them
		// to one row and picking the branch with the newest ready marker made a changeset under review on
		// one branch invisible on the other, which is the reviewer's question the queue exists to answer
		// (requirements, invariant 5).
		entry, err := branchReadyEntry(ctx, repo, cs, br.Branch)
		if err != nil {
			skipped = append(skipped, br.Branch+" ("+err.Error()+")")
			continue
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
			entries = append(entries, *entry)
		}
	}

	// Directories no branch accounts for. Read from HEAD's tree, not the working
	// tree, so the answer does not depend on what happens to be checked out.
	head, err := repo.Head(ctx)
	if err != nil && !git.IsUnknownRevision(err) {
		return err
	}
	dirs, err := changeset.DirsAt(ctx, repo, head)
	if err != nil {
		return err
	}
	for _, slug := range dirs {
		if seen[slug] {
			continue
		}
		note, err := classifyOrphan(ctx, repo, head, slug, durable)
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

	if a.json {
		return a.emitJSON(map[string]any{
			"ready_for_review":  entries,
			"skipped":           skipped,
			"landed_unrecorded": unrecorded,
		})
	}
	if len(entries) == 0 {
		a.printf("READY FOR REVIEW\n\n  nothing is ready\n")
		a.printUnrecorded(unrecorded, durable.NamespaceEmpty, displayRef(db.Ref), true)
		printSkipped(a, skipped)
		printBehindParent(a, stale)
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
	a.printUnrecorded(unrecorded, durable.NamespaceEmpty, displayRef(db.Ref), false)
	printSkipped(a, skipped)
	printBehindParent(a, stale)
	return nil
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
func classifyOrphan(ctx context.Context, repo *git.Repo, head, slug string, durable refIndex) (string, error) {
	// The chain an orphan might have is read from the same index the rest of this command reads, so an
	// orphan with no refs costs nothing: no `rev-parse`, no per-slug question about the namespace.
	archive, ok := durable.Archive[slug]
	if !ok {
		// With no archive ref there is no record of this changeset, and a directory with no
		// branch behind it is either a leftover or work whose branch was deleted before anyone
		// recorded it — git-pair cannot tell which, so it says nothing rather than guessing about
		// deleted work. The one shape of it it *can* tell is a directory the destination carries:
		// that is a landing rather than a disappearance, and `landedIn` reports it under its own
		// heading instead (see `unrecordedLandings`).
		return "", nil
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
	// An integrated changeset landed, and the branch that carried it is gone. That is the case
	// the record was written for: nothing is pending, and the diff would only say the work is not
	// in its base — which the record already says better.
	if _, ok := durable.Integrated[slug]; ok {
		return "", nil
	}
	// An abandoned changeset ended on purpose, and the archive carries the ending. That is the
	// whole answer: nothing is pending, and the diff would only report that the work is not in its
	// base, which is what abandoning means (PRD §9.7).
	if summary, err := lifecycle.Summarize(ctx, repo, slug, base, archive); err == nil && summary.Abandoned != nil {
		return "", nil
	}
	// The archive ref names a commit that survives the branch, so it can be compared with the
	// base without touching the working tree: identical changeset content on both sides is what a
	// merge leaves behind.
	dir := filepath.Join(changeset.Root, slug)
	changed, err := repo.PathsChanged(ctx, base, archive, dir)
	if err != nil {
		return "", err
	}
	if len(changed) == 0 {
		return "", nil
	}
	return fmt.Sprintf("%s (archived at %s, whose %s is not in %s and no branch carries it)",
		slug, short(archive), dir, base), nil
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

// branchReadyEntry is the queue row for one branch, or nil when that branch is not READY.
//
// It asks about one branch because that is the unit the queue reports: review commits are appended to a
// branch, so the branch is what is ready, and two branches carrying one changeset have two answers.
func branchReadyEntry(ctx context.Context, repo *git.Repo, cs changeset.Changeset, branch string) (*queueEntry, error) {
	summary, err := lifecycle.Summarize(ctx, repo, cs.Slug, cs.Base, branch)
	if err != nil {
		return nil, err
	}
	if summary.State != model.StateReady || summary.Marker == nil {
		return nil, nil
	}
	head, err := repo.RevParse(ctx, branch)
	if err != nil {
		return nil, err
	}
	return &queueEntry{
		Changeset:   cs.Slug,
		Branch:      branch,
		Base:        cs.Base,
		State:       string(summary.State),
		Head:        head,
		ReadyCommit: summary.Marker.SHA,
		ReadyAge:    lifecycle.Age(summary.Marker.When, now()),
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
