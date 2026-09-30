package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/marker"
)

// tidyItem is one directory `change tidy` was asked about, and what it did with it.
type tidyItem struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	// Action is "moved" for a directory this run renamed, "noop" for one already out of the way, and
	// "would-move" under --dry-run. A refusal is not an action: it is an error the command exits 1 on,
	// with the reason on stderr, because a refusal means the caller's list is not the list to move.
	Action string `json:"action"`
}

func newChangeTidyCommand(a *app) *cobra.Command {
	var (
		allLanded bool
		dryRun    bool
		asJSON    bool
	)
	cmd := &cobra.Command{
		Use:   "tidy [<changeset-id>...]",
		Short: "Move landed changeset directories out of the way",
		Long: `Move the directory of a changeset that has landed from ` + "`changesets/<id>/`" + ` to
` + "`changesets/.landed/<id>/`" + `, in one commit on this branch.

Landing does not delete anything, and it should not: the directory is the record of what was reviewed.
It stays where it is until it is in the integration branch, where it becomes scenery. ` + "`change tidy`" + `
moves it aside so a reader looking at ` + "`changesets/`" + ` sees the work still open.

The move is an ordinary commit of renames, written on the branch you are on, so it rides a changeset and a
review like any other change. ` + "`git pair status`" + `, ` + "`queue`" + `, ` + "`check`" + ` and the
destination walk read both spellings, so nothing about the changeset changes except where its directory
sits. ` + "`changesets/.landed/`" + ` is git-pair's own name and no changeset may use it.

Name the ids to move, or ask for every landed one with ` + "`--all-landed`" + `. A run that names an id
which is not landed, or which a changeset still in flight is stacked on, refuses rather than moving part
of the list: the reason is a fact about the repository, not a warning to read past.`,
		Example: `  git pair change tidy booking-transaction
  git pair change tidy --all-landed --dry-run
  git pair change tidy --all-landed --json`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChangeTidy(cmd.Context(), a, args, allLanded, dryRun, asJSON)
		},
	}
	cmd.Flags().BoolVar(&allLanded, "all-landed", false, "tidy every landed changeset this branch carries")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would move and commit nothing")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}

func runChangeTidy(ctx context.Context, a *app, args []string, allLanded, dryRun, asJSON bool) error {
	repo, err := a.loadRepo(ctx)
	if err != nil {
		return err
	}
	if len(args) == 0 && !allLanded {
		return &usageError{fmt.Errorf("name the changesets to tidy, or pass --all-landed")}
	}
	if len(args) > 0 && allLanded {
		return &usageError{fmt.Errorf("--all-landed names the whole set, so it takes no changeset ids")}
	}
	db, err := changeset.DefaultBranch(ctx, repo, a.defaultBranch)
	if err != nil && !errors.Is(err, changeset.ErrNoDefaultBranch) {
		return err
	}
	scan, err := changeset.ScanBranches(ctx, repo, db)
	if err != nil {
		return err
	}
	head, err := repo.Head(ctx)
	if err != nil {
		return err
	}
	active, err := changeset.ActiveIDs(ctx, repo, "HEAD")
	if err != nil {
		return err
	}
	// Everything the commit here carries under `changesets/`, in either spelling, minus the active
	// spelling: that remainder is what has already been moved aside, which is the second run's answer.
	carried, err := changeset.LandedIDs(ctx, repo, "HEAD")
	if err != nil {
		return err
	}
	tidied := map[string]bool{}
	for _, id := range carried {
		if !slices.Contains(active, id) {
			tidied[id] = true
		}
	}

	ids := args
	if allLanded {
		ids = nil
		for _, id := range active {
			if !landedOn(ctx, repo, db, id) {
				continue
			}
			ids = append(ids, id)
		}
		if len(ids) == 0 {
			return reportTidy(a, asJSON, dryRun, nil, "", db)
		}
	}
	items := make([]tidyItem, 0, len(ids))
	for _, id := range ids {
		if err := changeset.ValidateID(id); err != nil {
			return &usageError{err}
		}
		item, err := classifyTidy(ctx, repo, db, id, active, tidied, scan)
		if err != nil {
			return err
		}
		items = append(items, item)
	}
	if dryRun {
		return reportTidy(a, asJSON, dryRun, items, "", db)
	}
	moving := make([]tidyItem, 0, len(items))
	for _, it := range items {
		if it.Action == "move" {
			moving = append(moving, it)
		}
	}
	if len(moving) == 0 {
		return reportTidy(a, asJSON, dryRun, items, "", db)
	}
	// The move happens in the working tree, so nothing else may be in it. `check` and `change integrate`
	// ask the same question for the same reason: a commit that sweeps a half-written file off the author's
	// index is not a tidy.
	clean, err := repo.IsClean(ctx)
	if err != nil {
		return err
	}
	if !clean {
		status, _ := repo.StatusPorcelain(ctx)
		return fmt.Errorf("your working tree has changes outside this command; commit or stash them before tidying%s", untrackedNote(status))
	}
	if err := os.MkdirAll(filepath.Join(repo.Dir, changeset.Root, changeset.LandedDir), 0o755); err != nil {
		return err
	}
	paths := make([]string, 0, 2*len(moving))
	for _, it := range moving {
		if _, err := repo.Git(ctx, "mv", it.From, it.To); err != nil {
			return err
		}
		paths = append(paths, it.From, it.To)
	}
	if head == "" {
		return fmt.Errorf("this branch has no commit to tidy on")
	}
	msg := marker.Message{Subject: tidySubject(moving)}
	for _, it := range moving {
		msg.Body += it.ID + ": " + it.From + " -> " + it.To + "\n"
	}
	sha, err := marker.CommitPaths(ctx, repo, msg, paths, db)
	if err != nil {
		return err
	}
	for i, it := range items {
		if it.Action == "move" {
			items[i].Action = "moved"
		}
	}
	return reportTidy(a, asJSON, dryRun, items, sha, db)
}

// classifyTidy decides what one id needs. It returns a refusal (a plain error, exit 1) rather than an
// action when the repository says the id must not move, and an item with Action "noop" when it is already
// out of the way — which is the second run, and a success.
func classifyTidy(ctx context.Context, repo *git.Repo, db changeset.DefaultBranchRef, id string,
	active []string, tidied map[string]bool, scan changeset.Scan) (tidyItem, error) {
	item := tidyItem{ID: id, From: changeset.ActiveDirPath(id), To: changeset.LandedDirPath(id)}
	if tidied[id] {
		item.Action = "noop"
		return item, nil
	}
	if !slices.Contains(active, id) {
		// The id is not a directory on this branch. It may be landed work on trunk the author named by
		// habit; saying which of the two it is costs nothing and settles the next question.
		if db.Ref != "" && landedOn(ctx, repo, db, id) {
			return item, fmt.Errorf("changeset %s is landed and this branch does not carry changesets/%s: nothing here to move — run `git pair change tidy` on the branch that still has it", id, id)
		}
		return item, fmt.Errorf("changeset %s has no changesets/%s on this branch, so there is nothing to move", id, id)
	}
	if db.Ref == "" {
		return item, fmt.Errorf("changeset %s cannot be tidied: this repository has no integration branch to say whether it landed", id)
	}
	if !landedOn(ctx, repo, db, id) {
		return item, fmt.Errorf("changeset %s is not landed on %s, so there is nothing to move: `git pair status --changeset %s` says where it stands",
			id, displayRef(db.LocalName()), id)
	}
	if child := stackedOn(scan, id); child != "" {
		return item, fmt.Errorf("changeset %s is still in flight on %s, which is stacked on it: tidy it after that changeset lands", child, id)
	}
	item.Action = "move"
	return item, nil
}

// landedOn is the tree rule: the destination carries the directory, in either spelling, or the changeset
// has not landed.
func landedOn(ctx context.Context, repo *git.Repo, db changeset.DefaultBranchRef, id string) bool {
	if db.Ref == "" {
		return false
	}
	present, _ := changeset.CarriesDir(ctx, repo, db.Ref, id)
	return present
}

// stackedOn names one branch whose unlanded changeset is stacked on id, or "" when nothing is. It is the
// guard that keeps a tidy from moving the directory a child still measures itself against: the child would
// keep working and its base would come from a path nobody can see.
func stackedOn(scan changeset.Scan, id string) string {
	for _, br := range scan.Branches {
		for _, c := range br.Resolution.Candidates {
			if c.Changeset.BaseChangeset == id {
				return br.Branch
			}
		}
	}
	return ""
}

func tidySubject(moving []tidyItem) string {
	ids := make([]string, 0, len(moving))
	for _, it := range moving {
		ids = append(ids, it.ID)
	}
	sort.Strings(ids)
	if len(ids) == 1 {
		return "git-pair: tidy the landed changeset " + ids[0]
	}
	return fmt.Sprintf("git-pair: tidy %d landed changesets", len(ids))
}

// untrackedNote names the paths a tidy would have swept, because "commit or stash them" is only useful
// once the author sees which files are meant.
func untrackedNote(status string) string {
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(status), "\n") {
		if line == "" {
			continue
		}
		paths = append(paths, strings.TrimSpace(line[2:]))
	}
	if len(paths) == 0 {
		return ""
	}
	if len(paths) > 6 {
		paths = append(paths[:6], "...")
	}
	return ": " + strings.Join(paths, ", ")
}

// reportTidy prints the run. The JSON document is the same shape for every outcome — `changesets` is
// always an array, `commit` always a string — because a caller that has to type-check for absence learns
// nothing.
func reportTidy(a *app, asJSON, dryRun bool, items []tidyItem, sha string, db changeset.DefaultBranchRef) error {
	if items == nil {
		items = []tidyItem{}
	}
	dest := ""
	if db.Ref != "" {
		dest = displayRef(db.LocalName())
	}
	if asJSON {
		return a.emitJSON(struct {
			Changesets  []tidyItem `json:"changesets"`
			Commit      string     `json:"commit"`
			DryRun      bool       `json:"dry_run"`
			Destination string     `json:"destination"`
		}{Changesets: items, Commit: sha, DryRun: dryRun, Destination: dest})
	}
	if len(items) == 0 {
		a.printf("nothing to tidy: this branch carries no landed changeset directory\n")
		return nil
	}
	for _, it := range items {
		switch it.Action {
		case "noop":
			a.printf("already tidied: %s\n", it.To)
		default:
			a.printf("%s: %s -> %s\n", it.ID, it.From, it.To)
		}
	}
	if dryRun {
		a.printf("dry run: nothing committed\n")
		return nil
	}
	if sha != "" {
		a.printf("committed %s\n", short(sha))
	}
	return nil
}
