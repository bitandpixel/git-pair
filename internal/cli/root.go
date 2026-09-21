// Package cli implements the git-pair command surface.
//
// Exit codes are part of the agent-facing contract:
//
//	0 success
//	1 a git-pair business rule refused the operation (surviving additions, dirty
//	  tree, outcome does not permit integration)
//	2 usage error (bad flag, missing argument, no changeset)
//	3 git itself failed
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/lifecycle"
	"gitpair/internal/reviewref"
)

// Version is stamped by the build.
const Version = "0.1.0"

// Exit codes.
const (
	exitOK       = 0
	exitRefusal  = 1
	exitUsage    = 2
	exitGitError = 3
)

// app is the state shared by every command.
type app struct {
	// Repo is the resolved repository; nil until loadRepo runs.
	repo   *git.Repo
	stdout io.Writer
	stderr io.Writer
	json   bool
	// defaultBranch is the `--default-branch` override. Every command that asks "has this
	// landed?" needs the integration branch to answer, and CI passes this because a checkout
	// built with `init` and one `fetch` has no recorded remote default to read.
	defaultBranch string
}

// Execute builds the command tree and runs it, returning the process exit code.
func Execute(args []string) int {
	a := &app{stdout: os.Stdout, stderr: os.Stderr}
	root := newRootCommand(a)
	root.SetArgs(args)
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)

	err := root.Execute()
	if err == nil {
		return exitOK
	}
	if errors.Is(err, context.Canceled) {
		return exitRefusal
	}
	code := exitRefusal
	var ge *git.Error
	var ue *usageError
	switch {
	case errors.Is(err, git.ErrNotRepository), errors.As(err, &ge):
		code = exitGitError
	case errors.Is(err, changeset.ErrNoDefaultBranch):
		// Nothing about the repository was refused: the question "has this landed?" cannot be asked
		// without the integration branch, and the caller supplies it — with `--default-branch` or
		// with a fetch that brought it. That is a usage error in the sense the table means: a
		// different invocation answers it. An unresolvable `base:` in CHANGESET.yaml stays exit 1,
		// because there the repository really is what changed.
		code = exitUsage
	case errors.As(err, &ue), isUsageError(err):
		code = exitUsage
	}
	if !errors.Is(err, errSilent) {
		fmt.Fprintf(a.stderr, "git-pair: %s\n", messageOf(err))
	}
	return code
}

func isUsageError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unknown flag") ||
		strings.Contains(msg, "invalid argument") ||
		strings.Contains(msg, "accepts ") ||
		strings.Contains(msg, "requires ")
}

// errSilent marks an error whose message has already been printed.
var errSilent = errors.New("git-pair: output already written")

func messageOf(err error) string {
	msg := err.Error()
	return strings.TrimPrefix(msg, "Error: ")
}

func newRootCommand(a *app) *cobra.Command {
	root := &cobra.Command{
		Use:           "git-pair",
		Short:         "Local-first peer review for human and coding-agent pairs",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `git-pair adds a thin review protocol on top of ordinary git commits, files and
refs. It does not replace git, your editor, your difftool, or your forge.

Review state lives in the repository: a changeset directory holds ABOUT.md and
review threads, lifecycle markers are commits carrying Review-* trailers, and
and refs/git-pair/* holds the two durable refs written when a changeset lands.

Author commands:   git pair change init | use | ready | unready | abandon
Reviewer commands: git pair review open | about | thread | submit | history | queue
Inspection:        git pair status | diff
Gates:             git pair check`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if jsonFlag, err := cmd.Flags().GetBool("json"); err == nil {
				a.json = jsonFlag
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return &usageError{fmt.Errorf("unknown command %q; run `git-pair --help` for the command list", args[0])}
			}
			return cmd.Help()
		},
	}
	root.PersistentFlags().Bool("json", false, "machine-readable output where supported")
	root.PersistentFlags().StringVar(&a.defaultBranch, "default-branch", "",
		"ref of the integration branch; otherwise git-pair reads git's own answer (origin/HEAD, then a sole main/master)")
	root.AddCommand(
		newChangeCommand(a),
		newReviewCommand(a),
		newStatusCommand(a),
		newDiffCommand(a),
		newCheckCommand(a),
		newIntegrationCommand(a),
	)
	return root
}

// groupUsage makes a parent command report an unknown subcommand instead of
// silently printing help and exiting 0.
func groupUsage(name string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return &usageError{fmt.Errorf("unknown %s command %q; run `git-pair %s --help`", name, args[0], name)}
		}
		return cmd.Help()
	}
}

// --- shared loading ---------------------------------------------------------

// session is everything a command needs about the current changeset.
type session struct {
	repo            *git.Repo
	cs              changeset.Changeset
	summary         lifecycle.Summary
	clean           bool
	onCurrentBranch bool
	head            string
	// baseIsOwnBranch records the unrecoverable base configuration, so the
	// surfaces that can explain it (status, change ready) do not each redo the
	// lookup and so they agree on the wording.
	baseIsOwnBranch bool
	// trunk is the integration branch the resolution compared against. It travels with the
	// session because "which changeset is this?" is a comparison against trunk, and a surface
	// that reports the answer should be able to report the other side of it.
	trunk changeset.DefaultBranchRef
}

// loadFor resolves the session a changeset-scoped read should work from: the
// checked-out changeset, or the one named by `--changeset`.
func (a *app) loadFor(ctx context.Context, slug string) (*session, error) {
	if slug == "" {
		return a.load(ctx)
	}
	return a.loadNamed(ctx, slug)
}

// loadNamed resolves a changeset by slug, from whichever branch carries it.
//
// Reads may look anywhere in the repository, because reading a commit damages
// nothing. Every command that writes stays on the checked-out branch, because a
// marker is a commit and a commit lands wherever HEAD is — which is why `change
// ready` and `change unready` take no such flag.
func (a *app) loadNamed(ctx context.Context, slug string) (*session, error) {
	repo, err := a.loadRepo(ctx)
	if err != nil {
		return nil, err
	}
	db, err := changeset.DefaultBranch(ctx, repo, a.defaultBranch)
	if err != nil {
		return nil, usageWrap(err)
	}
	cs, summary, branch, err := a.resolveNamed(ctx, repo, slug, db)
	if err != nil {
		return nil, usageWrap(err)
	}
	head, err := repo.RevParse(ctx, branch)
	if err != nil {
		return nil, err
	}
	own, err := changeset.BaseIsOwnBranch(ctx, repo, cs.Base, branch)
	if err != nil {
		return nil, err
	}
	// The working tree belongs to whichever branch is checked out, so a changeset
	// read from elsewhere has no opinion about it.
	current, err := repo.CurrentBranch(ctx)
	if err != nil {
		current = ""
	}
	onBranch := current == branch
	return &session{repo: repo, cs: cs, summary: summary, clean: onBranch,
		onCurrentBranch: onBranch, head: head, baseIsOwnBranch: own, trunk: db}, nil
}

// resolveNamed finds the changeset a slug names. Two branches can carry the same directory
// (`feature/x` branched before the changeset existed, or a stack), so "the" changeset is the
// one whose history is furthest along, and the branch that answer came from is returned so
// callers can print it.
func (a *app) resolveNamed(ctx context.Context, repo *git.Repo, slug string, db changeset.DefaultBranchRef) (changeset.Changeset, lifecycle.Summary, string, error) {
	resolutions, err := changeset.BranchResolutions(ctx, repo, db)
	if err != nil {
		return changeset.Changeset{}, lifecycle.Summary{}, "", err
	}
	var branches []string
	selected := map[string]changeset.Candidate{}
	for _, br := range resolutions {
		if br.Err != nil || br.Resolution.Selected == nil {
			continue
		}
		if br.Resolution.Selected.Changeset.Slug == slug {
			branches = append(branches, br.Branch)
			selected[br.Branch] = *br.Resolution.Selected
		}
	}
	if len(branches) == 0 {
		// No branch carries the slug, so the durable pair is the only place its history can be
		// read — which is exactly what a reader asking about a landed changeset wants. For a
		// changeset that never landed there is nothing to read: the branch was the record, and it
		// is gone. Deriving from the archive can only report what the branch claimed before it
		// disappeared, which is why it is a fallback and not a second source of state (PRD §12).
		anchor, err := reviewref.ResolveArchive(ctx, repo, slug)
		if errors.Is(err, reviewref.ErrNoArchiveRef) {
			return changeset.Changeset{}, lifecycle.Summary{}, "", &usageError{
				fmt.Errorf("no branch carries changeset %q; `git pair review queue` lists what this repository has", slug)}
		}
		if err != nil {
			return changeset.Changeset{}, lifecycle.Summary{}, "", err
		}
		base, err := changeset.BaseAt(ctx, repo, anchor, slug)
		if err != nil {
			if errors.Is(err, git.ErrUnknownPath) {
				return changeset.Changeset{}, lifecycle.Summary{}, "", &usageError{
					fmt.Errorf("changeset %q is recorded at %s but carries no %s", slug, short(anchor), changeset.MetadataFile)}
			}
			return changeset.Changeset{}, lifecycle.Summary{}, "", err
		}
		cs := changeset.Changeset{Slug: slug, Dir: filepath.Join(changeset.Root, slug), Base: base, Exists: true}
		summary, err := lifecycle.Summarize(ctx, repo, slug, base, anchor)
		if err != nil {
			return changeset.Changeset{}, lifecycle.Summary{}, "", err
		}
		// cs.Branch stays empty: there is no branch, and a reader must be able to tell.
		return cs, summary, anchor, nil
	}
	var (
		best    changeset.Changeset
		bestSum lifecycle.Summary
		name    string
		bestAt  time.Time
		first   error
	)
	for _, branch := range branches {
		cs := selected[branch].Changeset
		cs.Branch = branch
		summary, err := lifecycle.Summarize(ctx, repo, cs.Slug, cs.Base, branch)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		at := time.Time{}
		if summary.Marker != nil {
			at = summary.Marker.When
		}
		if name == "" || at.After(bestAt) {
			best, bestSum, name, bestAt = cs, summary, branch, at
		}
	}
	if name == "" {
		if first == nil {
			first = fmt.Errorf("no branch carries changeset %q", slug)
		}
		return changeset.Changeset{}, lifecycle.Summary{}, "", first
	}
	return best, bestSum, name, nil
}

// load resolves the repository and the current branch's changeset, requiring
// the changeset directory to exist.
func (a *app) load(ctx context.Context) (*session, error) {
	repo, err := a.loadRepo(ctx)
	if err != nil {
		return nil, err
	}
	db, err := changeset.DefaultBranch(ctx, repo, a.defaultBranch)
	if err != nil {
		return nil, usageWrap(err)
	}
	cs, err := changeset.RequireCurrentOn(ctx, repo, db)
	if err != nil {
		return nil, usageWrap(err)
	}
	return a.sessionFor(ctx, repo, cs, db)
}

// loadRepo resolves the repository without requiring a changeset.
func (a *app) loadRepo(ctx context.Context) (*git.Repo, error) {
	if a.repo != nil {
		return a.repo, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	repo, err := git.Open(cwd)
	if errors.Is(err, git.ErrNotRepository) {
		return nil, fmt.Errorf("%w: no git repository contains %s", git.ErrNotRepository, cwd)
	}
	if err != nil {
		return nil, err
	}
	a.repo = repo
	return repo, nil
}

// sessionFor derives state for an already-resolved changeset.
func (a *app) sessionFor(ctx context.Context, repo *git.Repo, cs changeset.Changeset, db changeset.DefaultBranchRef) (*session, error) {
	summary, err := lifecycle.SummarizeHEAD(ctx, repo, cs.Slug, cs.Base)
	if err != nil {
		return nil, err
	}
	clean, err := repo.IsClean(ctx)
	if err != nil {
		return nil, err
	}
	head, err := repo.Head(ctx)
	if err != nil {
		// An empty repository has no HEAD; report it as the empty string so
		// `status` stays useful while a project is being created.
		if !git.IsUnknownRevision(err) {
			return nil, err
		}
	}
	own, err := changeset.BaseIsOwnBranch(ctx, repo, cs.Base, cs.Branch)
	if err != nil {
		return nil, err
	}
	return &session{repo: repo, cs: cs, summary: summary, clean: clean, onCurrentBranch: true,
		head: head, baseIsOwnBranch: own, trunk: db}, nil
}

// usageWrap marks an error as a usage problem rather than a git failure.
//
// A tie between two changesets belongs here: the command asked a question this branch cannot
// answer, and both ways out — `--changeset <id>` for one command, `change use <id>` for the
// branch — are things the user can type, which is what separates exit 2 from exit 1.
func usageWrap(err error) error {
	if errors.Is(err, changeset.ErrDetachedHead) || errors.Is(err, changeset.ErrNoChangeset) ||
		errors.Is(err, changeset.ErrAmbiguousChangeset) {
		return &usageError{err}
	}
	return err
}

type usageError struct{ error }

func (u *usageError) Unwrap() error { return u.error }

// --- output helpers ---------------------------------------------------------

func (a *app) printf(format string, args ...any) {
	fmt.Fprintf(a.stdout, format, args...)
}

func (a *app) warn(format string, args ...any) {
	fmt.Fprintf(a.stderr, format, args...)
}

func (a *app) emitJSON(v any) error {
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
