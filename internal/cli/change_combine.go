package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/lifecycle"
	"gitpair/internal/marker"

	"github.com/spf13/cobra"
)

// change combine is the second exit the branch-shape refusal offers (PRD 4), for the answer that is not a stack: two
// directories that are one piece of work. One changeset survives and the other is archived inside it, whole, so
// nothing is deleted - the disappearing directory keeps every file it had, at
// changesets/<into>/.combined/<gone>/.
//
// The name is `combine` and not `merge`, because landing already uses merge language for bringing commits into the
// destination and the two acts must not read as one.

const combinedDir = ".combined"

type combineOptions struct {
	into    string
	threads bool
	json    bool
}

func newChangeCombineCommand(a *app) *cobra.Command {
	opts := &combineOptions{}
	cmd := &cobra.Command{
		Use:   "combine --into <id> [--threads]",
		Short: "Fold one changeset into another when the two are one piece of work",
		Long: `Archive one changeset's files inside another, when the two directories describe one change.

The branch-shape rule says a branch carries one changeset, plus the ones it is stacked on. When the directory beside
yours is not below yours but is the same work - one changeset started before the other was named, or a split nobody
meant - a stack record would be a lie and the two belong together. This command folds them.

Nothing is deleted. The disappearing changeset moves whole into ` + "`changesets/<into>/.combined/<id>/`" + `, and an
archive there is inert: it answers no id, it is neither landed nor active, and it is not offered as a thread. The
survivor keeps its own ` + "`base:`" + ` and ` + "`base-changeset:`" + ` - folding a changeset in is not a licence to move
the comparison underneath a review - and the command prints which links were kept and which were dropped. The
survivor's ABOUT.md gains one line pointing at the archive rather than a copy of anything.

With ` + "`--threads`" + ` the disappeared changeset's review threads are copied, not moved, each prefixed with its id so
two threads called scope.md do not collide. A thread file is not an implementation change, so the copy cannot move
what a reviewer is comparing.

Refuses while either changeset is offered or under review, because a reviewer's diff must not change underneath them.`,
		Example: `  git pair change combine --into booking
  git pair change combine --into booking --threads`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChangeCombine(cmd.Context(), a, opts)
		},
	}
	cmd.Flags().StringVar(&opts.into, "into", "", "the changeset that survives (required)")
	cmd.Flags().BoolVar(&opts.threads, "threads", false, "copy the disappeared changeset's threads into the survivor")
	cmd.Flags().BoolVar(&opts.json, "json", false, "emit JSON instead of prose")
	_ = cmd.MarkFlagRequired("into")
	return cmd
}

func runChangeCombine(ctx context.Context, a *app, opts *combineOptions) error {
	repo, err := a.loadRepo(ctx)
	if err != nil {
		return err
	}
	db := a.destination(ctx, repo)
	if db.Ref == "" {
		return &usageError{fmt.Errorf("this clone cannot name the integration branch, so it cannot tell a landed changeset from an unlanded one; pass --default-branch or set the repository's integration branch")}
	}
	if err := changeset.ValidateID(opts.into); err != nil {
		return &usageError{err}
	}
	branch, err := repo.CurrentBranch(ctx)
	if err != nil {
		branch = ""
	}

	landedList, err := changeset.LandedIDs(ctx, repo, db.Ref)
	if err != nil {
		return err
	}
	mine, err := unlandedIDs(ctx, repo, "HEAD", inSet(landedList))
	if err != nil {
		return err
	}
	into := opts.into
	gone := ""
	for _, id := range mine {
		if id != into {
			if gone != "" {
				return &usageError{fmt.Errorf("this branch carries more than two unlanded changesets (%s), and combine folds one pair at a time. Combine two of them first, then run this again", joinIDs(mine))}
			}
			gone = id
		}
	}
	if gone == "" {
		if len(mine) == 0 {
			return &usageError{fmt.Errorf("this branch carries no unlanded changeset to combine; %s is not one of them", into)}
		}
		return &usageError{fmt.Errorf("this branch carries one unlanded changeset (%s), so there is nothing to combine. If %s is on another branch, the answer is `git pair change stack --base <branch>` or taking the directory out of this branch with `git restore --source=<ref> -- changesets/<id>`",
			joinIDs(mine), into)}
	}

	survivor := changeset.Changeset{Slug: into, Dir: changeset.Root + "/" + into, Branch: branch}
	vanishing := changeset.Changeset{Slug: gone, Dir: changeset.Root + "/" + gone, Branch: branch}

	// Nothing that a review is resting on moves. The check is per changeset rather than per branch, because a
	// reviewer compared one changeset's diff and it is that diff which would change underneath them.
	for _, cs := range []changeset.Changeset{survivor, vanishing} {
		if err := refuseIfOfferedOrReviewed(ctx, repo, cs); err != nil {
			return err
		}
	}
	// The command commits two directories, so it refuses while either is dirty rather than folding uncommitted
	// work into a commit nobody asked for. Work elsewhere on the branch is not its business.
	dirty, err := repo.Git(ctx, "status", "--porcelain", "--", survivor.Dir, vanishing.Dir)
	if err != nil {
		return err
	}
	if strings.TrimSpace(dirty) != "" {
		return fmt.Errorf("%s and %s have uncommitted changes; commit or stash them before combining", survivor.Dir, vanishing.Dir)
	}

	archive := filepath.Join(survivor.Dir, combinedDir, gone)
	if _, err := os.Stat(filepath.Join(repo.Dir, archive)); err == nil {
		return &usageError{fmt.Errorf("%s already holds an archive of %s; the archive is inert, so leave it where it is", archive, gone)}
	}
	before, err := changeset.StackAt(ctx, repo, "HEAD", gone)
	if err != nil {
		return err
	}
	kept, err := changeset.StackAt(ctx, repo, "HEAD", into)
	if err != nil {
		return err
	}

	// A thread copy has to be read before the move, because after it the file is under the archive and the copy
	// would be naming a path the command just relocated.
	var copies []string
	if opts.threads {
		threads, err := vanishing.Threads(repo)
		if err != nil {
			return err
		}
		for _, th := range threads {
			if th == changeset.AboutFile {
				continue
			}
			copies = append(copies, th)
		}
	}

	if err := os.MkdirAll(filepath.Join(repo.Dir, survivor.Dir, combinedDir), 0o755); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(repo.Dir, vanishing.Dir), filepath.Join(repo.Dir, archive)); err != nil {
		return fmt.Errorf("cannot move %s to %s: %w", vanishing.Dir, archive, err)
	}
	// Threads names its results from the repository root, so the copy takes the base name: the archive is the
	// directory that moved, and re-joining a root-relative path under it would name nothing.
	for _, th := range copies {
		name := filepath.Base(th)
		src := filepath.Join(repo.Dir, archive, name)
		dst := filepath.Join(repo.Dir, survivor.Dir, gone+"-"+name)
		body, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, body, 0o644); err != nil {
			return err
		}
	}
	if err := appendArchiveLine(repo, survivor, gone); err != nil {
		return err
	}

	if _, err := repo.Git(ctx, "add", "-A", "--", survivor.Dir, vanishing.Dir); err != nil {
		return err
	}
	sha, err := marker.CommitPaths(ctx, repo, marker.Message{
		Subject:  fmt.Sprintf("git-pair: combine %s into %s", gone, into),
		Trailers: []string{"Review-Changeset=" + into},
	}, []string{survivor.Dir, vanishing.Dir}, db)
	if err != nil {
		if isNothingToCommit(err) {
			a.printf("nothing to commit (%s is already archived in %s)\n", gone, into)
			return nil
		}
		return err
	}

	a.printf("committed %s: %s archived at %s\n", short(sha), gone, archive)
	// The survivor's comparison is its own, and saying so is the difference between a fold and a rebase nobody
	// asked for: the dropped links are the disappeared changeset's, and keeping them would move the base.
	a.printf("%s keeps base: %s and base-changeset: %s; dropped %s's base: %s and base-changeset: %s\n",
		into, orNone(kept.Base), orNone(kept.BaseChangeset), gone, orNone(before.Base), orNone(before.BaseChangeset))
	if len(copies) > 0 {
		a.printf("copied %d thread(s) from %s into %s as %s-<name>.md; replies continue in those copies, and the "+
			"archived originals stay in %s\n", len(copies), gone, into, gone, archive)
	}
	a.printf("%s now carries one unlanded changeset, so offer it again with `git pair change ready`\n", branch)
	if opts.json {
		return a.emitJSON(map[string]any{
			"into": into, "combined": gone, "archive": archive, "commit": sha,
			"keptBase": kept.Base, "keptBaseChangeset": kept.BaseChangeset,
			"droppedBase": before.Base, "droppedBaseChangeset": before.BaseChangeset,
			"threads": copies,
		})
	}
	return nil
}

// refuseIfOfferedOrReviewed is the guard both exits share: a fold and a stack record both change what a branch's
// changeset means, and a reviewer's diff must not move underneath them. It is read from the same lifecycle summary
// `check` reads, so the two cannot disagree about whether a changeset is in review.
func refuseIfOfferedOrReviewed(ctx context.Context, repo *git.Repo, cs changeset.Changeset) error {
	st, err := changeset.StackAt(ctx, repo, "HEAD", cs.Slug)
	if err != nil {
		return err
	}
	base := st.Base
	if base == "" {
		base = "HEAD"
	}
	summary, err := lifecycle.SummarizeHEAD(ctx, repo, cs.Slug, base)
	if err != nil {
		return err
	}
	if summary.Marker != nil || summary.LatestReview != nil {
		return &usageError{fmt.Errorf("%s is offered or under review, and %s would change what that review is comparing; run `git pair change unready` first, then `git pair change combine --into %s` again",
			cs.Slug, "this command", cs.Slug)}
	}
	return nil
}

// appendArchiveLine adds the one line that tells a reader where the rest went. It is a pointer and not a copy: the
// archived ABOUT.md keeps its own prose, and two copies of one story disagree the first time somebody edits one.
func appendArchiveLine(repo *git.Repo, survivor changeset.Changeset, gone string) error {
	path := filepath.Join(repo.Dir, survivor.Dir, changeset.AboutFile)
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			body = []byte("# " + survivor.Slug + "\n\n")
		} else {
			return err
		}
	}
	line := fmt.Sprintf("\nThis changeset also covers %s, whose files are archived under %s/%s/%s/.\n",
		gone, survivor.Dir, combinedDir, gone)
	if strings.Contains(string(body), line) {
		return nil
	}
	out := string(body)
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return os.WriteFile(path, []byte(out+line), 0o644)
}
