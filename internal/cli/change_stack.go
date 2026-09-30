package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/marker"

	"github.com/spf13/cobra"
)

// change stack is one of the two exits the branch-shape refusal offers (PRD 4). The other is `change combine`, in
// change_combine.go. Both exist because the refusal cannot tell which answer fits: a second changeset directory on a
// branch is either this branch's own work sitting on top of another changeset, or separate work that arrived from
// elsewhere. Provenance is not readable from history, so the refusal states both and these two commands are what the
// author can run.
//
// This one writes what `init --base <branch>` writes when it infers a stack, for the case where the changesets
// already exist. It records the pair and nothing else.

type stackOptions struct {
	base string
	json bool
}

func newChangeStackCommand(a *app) *cobra.Command {
	opts := &stackOptions{}
	cmd := &cobra.Command{
		Use:   "stack --base <branch>",
		Short: "Record that this branch's changeset is stacked on the one another branch carries",
		Long: `Record, in this branch's own CHANGESET.yaml, which changeset the base branch carries.

The branch-shape rule says a branch carries one changeset, plus the changesets it is stacked on. When the record is
missing, the branch looks like it carries two unrelated changesets and ` + "`change ready`" + ` refuses it. This command
writes the missing record: ` + "`base:`" + ` naming the branch, and ` + "`base-changeset:`" + ` naming the changeset that
lives on it.

It compares two sets, each read as the changeset directories a branch carries minus the ones the integration branch
already carries. The one in both lists is the parent, the one only here is the child, and the base branch's own
ancestors are excluded - on a third level of a stack this branch carries the grandparent too, and telling it to
exclude its own ancestors would ask it to name the fact this command is about to write.

Only the child's file changes. The parent's record belongs to its own branch, and a stacked branch holding a copy of
the parent's directory is what every stacked branch looks like.

A base that changes is the comparison changing, so a reviewer's diff changes with it. This command does not refuse
because a ready marker is present - ` + "`check`" + ` already refuses a merge over a commit that followed the marker - and
it prints the old value and the new one, then says to offer the branch again.`,
		Example: `  git pair change stack --base booking
  git pair change stack --base feature/auth --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChangeStack(cmd.Context(), a, opts)
		},
	}
	cmd.Flags().StringVar(&opts.base, "base", "", "the branch this branch's changeset is stacked on (required)")
	cmd.Flags().BoolVar(&opts.json, "json", false, "emit JSON instead of prose")
	_ = cmd.MarkFlagRequired("base")
	return cmd
}

func runChangeStack(ctx context.Context, a *app, opts *stackOptions) error {
	repo, err := a.loadRepo(ctx)
	if err != nil {
		return err
	}
	db := a.destination(ctx, repo)
	if db.Ref == "" {
		return &usageError{fmt.Errorf("this clone cannot name the integration branch, so it cannot tell a landed changeset from an unlanded one; pass --default-branch or set the repository's integration branch")}
	}
	branch, err := repo.CurrentBranch(ctx)
	if err != nil {
		branch = ""
	}
	if branch == "" {
		return &usageError{fmt.Errorf("`change stack` records a stack for the checked-out branch, and this working tree is not on a branch")}
	}
	base := strings.TrimPrefix(opts.base, "refs/heads/")
	if base == "" {
		return &usageError{fmt.Errorf("--base names the branch this work is stacked on, and it was empty")}
	}
	if base == branch {
		// The same claim `init` makes about its own base: a stack whose parent is this branch moves with
		// every commit, and the comparison becomes the branch against itself.
		return &usageError{fmt.Errorf("--base %s is the branch this changeset lives on, so the stack would move with every commit. Create a branch for the change (`git switch -c <name>`) and run `git pair change stack --base %s` there", base, base)}
	}
	if db.IsBranch(base) {
		return &usageError{fmt.Errorf("--base %s is the integration branch, which is not a stack: a changeset measured against it is not stacked. Name the branch carrying the work this one sits on top of", base)}
	}
	baseRev, err := repo.RevParse(ctx, "refs/heads/"+base)
	if err != nil {
		return &usageError{fmt.Errorf("--base %s does not name a branch here: %v", base, err)}
	}

	landedList, err := changeset.LandedIDs(ctx, repo, db.Ref)
	if err != nil {
		return err
	}
	landed := inSet(landedList)
	mine, err := unlandedIDs(ctx, repo, "HEAD", landed)
	if err != nil {
		return err
	}
	theirs, err := unlandedIDs(ctx, repo, baseRev, landed)
	if err != nil {
		return err
	}
	if len(theirs) == 0 {
		return &usageError{fmt.Errorf("%s carries no unlanded changeset, so nothing on this branch is stacked on it. A changeset measured against the integration branch records `base: %s` and no stack; `git pair init --base %s` writes that", base, db.LocalName(), base)}
	}

	common := intersect(mine, theirs)
	if len(common) == 0 {
		return &usageError{fmt.Errorf("%s is not this branch's base: the two branches share no unlanded changeset, so nothing here is stacked on anything there. %s carries %s; this branch (%s) carries %s. If %s is where this work belongs, record `base: %s` in the child's CHANGESET.yaml instead of a stack",
			base, base, joinIDs(theirs), branch, joinIDs(mine), base, db.LocalName())}
	}

	// The base branch's ancestors, excluded from the parent candidates. Read from where the chain is already
	// recorded - that branch's files - because this branch cannot be asked to exclude what this command is
	// about to write.
	anc, err := recordedAncestors(ctx, repo, baseRev, theirs)
	if err != nil {
		return err
	}
	parents := subtract(common, anc)
	child := subtract(mine, inSet(theirs))

	if len(parents) != 1 {
		return &usageError{fmt.Errorf("which changeset on %s is the one below this branch cannot be told: %s are in both branches and %s records no chain below them. Settle the level below first - `git pair change stack --base <its base>` on %s - then run this again",
			base, joinIDs(common), base, base)}
	}
	if len(child) != 1 {
		if len(child) == 0 {
			return &usageError{fmt.Errorf("this branch carries no changeset of its own: everything it carries (%s) is on %s too, so it is a copy of that branch and not a child of it", joinIDs(mine), base)}
		}
		return &usageError{fmt.Errorf("this branch carries %d changesets of its own (%s), and a stack records one child. Two directories that belong to one piece of work are what `git pair change combine --into <survivor>` answers; two that do not belong on this branch are taken out with `git restore --source=<ref> -- changesets/<id>`",
			len(child), joinIDs(child))}
	}

	parentID := parents[0]
	childID := child[0]
	before, err := changeset.StackAt(ctx, repo, "HEAD", childID)
	if err != nil {
		return err
	}
	cs := changeset.Changeset{Slug: childID, Dir: changeset.Root + "/" + childID, Branch: branch}
	written, err := changeset.Write(repo, cs, changeset.WriteOptions{
		Parent:        base,
		BaseChangeset: parentID,
		SetParent:     true,
	})
	if err != nil {
		switch {
		case errors.Is(err, changeset.ErrParentWithBase), errors.Is(err, changeset.ErrBaseConflict), errors.Is(err, changeset.ErrIDMismatch):
			return &usageError{err}
		}
		return err
	}
	if len(written) == 0 {
		a.printf("%s already records base: %s and base-changeset: %s\n", cs.MetadataPath(), base, parentID)
		return nil
	}

	if err := repo.StagePaths(ctx, cs.Dir); err != nil {
		return err
	}
	sha, err := marker.CommitPaths(ctx, repo, marker.Message{
		Subject:  fmt.Sprintf("git-pair: stack %s on %s", childID, parentID),
		Trailers: []string{"Review-Changeset=" + childID},
	}, []string{cs.Dir}, db)
	if err != nil {
		if isNothingToCommit(err) {
			a.printf("nothing to commit (%s already records the stack)\n", cs.MetadataPath())
			return nil
		}
		return err
	}
	// Both keys, old and new: the base is the comparison a reviewer's diff is measured against, so the
	// author is being told what moved under any review that already exists, not merely what was written.
	a.printf("committed %s: %s base: %s -> %s, base-changeset: %s -> %s\n",
		short(sha), cs.MetadataPath(), displayRef(before.Base), base, orNone(before.BaseChangeset), parentID)
	a.printf("the comparison changed, so offer the branch again with `git pair change ready`\n")
	if opts.json {
		return a.emitJSON(map[string]any{
			"changeset": childID, "base": base, "baseChangeset": parentID,
			"previousBase": before.Base, "previousBaseChangeset": before.BaseChangeset, "commit": sha,
		})
	}
	return nil
}

// unlandedIDs is the set both exits compare: the changeset directories a revision carries that the integration
// branch does not, which is what makes a landed parent stop counting against a branch.
func unlandedIDs(ctx context.Context, repo *git.Repo, rev string, landed map[string]bool) ([]string, error) {
	active, err := changeset.ActiveIDs(ctx, repo, rev)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, id := range active {
		if !landed[id] {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

// recordedAncestors is the part of a branch's unlanded set that some other member of that set names as its own
// parent. It is read from that branch's files, because on a third level of a stack the branch being told to stack
// carries the grandparent as well and cannot exclude it - naming the ancestors is the fact the caller is about to
// record.
func recordedAncestors(ctx context.Context, repo *git.Repo, rev string, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	inList := map[string]bool{}
	for _, id := range ids {
		inList[id] = true
	}
	for _, id := range ids {
		st, err := changeset.StackAt(ctx, repo, rev, id)
		if err != nil {
			return nil, err
		}
		if st.BaseChangeset != "" && st.BaseChangeset != id && inList[st.BaseChangeset] {
			out[st.BaseChangeset] = true
		}
	}
	return out, nil
}

func inSet(ids []string) map[string]bool {
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

func intersect(a, b []string) []string {
	in := map[string]bool{}
	for _, s := range b {
		in[s] = true
	}
	var out []string
	for _, s := range a {
		if in[s] {
			out = append(out, s)
		}
	}
	return out
}

func subtract(a []string, b map[string]bool) []string {
	var out []string
	for _, s := range a {
		if !b[s] {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func joinIDs(ids []string) string {
	if len(ids) == 0 {
		return "(none)"
	}
	return strings.Join(ids, ", ")
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
