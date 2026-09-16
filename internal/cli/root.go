// Package cli implements the gitpr command surface.
//
// Exit codes are part of the agent-facing contract:
//
//	0 success
//	1 a gitpr business rule refused the operation (surviving additions, dirty
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
	"strings"

	"github.com/spf13/cobra"

	"gitpr/internal/changeset"
	"gitpr/internal/git"
	"gitpr/internal/lifecycle"
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
	case errors.As(err, &ue), isUsageError(err):
		code = exitUsage
	}
	if !errors.Is(err, errSilent) {
		fmt.Fprintf(a.stderr, "gitpr: %s\n", messageOf(err))
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
var errSilent = errors.New("gitpr: output already written")

func messageOf(err error) string {
	msg := err.Error()
	return strings.TrimPrefix(msg, "Error: ")
}

func newRootCommand(a *app) *cobra.Command {
	root := &cobra.Command{
		Use:           "gitpr",
		Short:         "Local-first peer review for human and coding-agent pairs",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `gitpr adds a thin review protocol on top of ordinary git commits, files and
refs. It does not replace git, your editor, your difftool, or your forge.

Review state lives in the repository: a changeset directory holds ABOUT.md and
review threads, lifecycle markers are commits carrying GitPR-* trailers, and
refs/reviews/* keeps the complete unsquashed history reachable.

Author commands:   gitpr change init | ready
Reviewer commands: gitpr review open | about | thread | submit | history | queue | close
Inspection:        gitpr status | diff`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if jsonFlag, err := cmd.Flags().GetBool("json"); err == nil {
				a.json = jsonFlag
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return &usageError{fmt.Errorf("unknown command %q; run `gitpr --help` for the command list", args[0])}
			}
			return cmd.Help()
		},
	}
	root.PersistentFlags().Bool("json", false, "machine-readable output where supported")
	root.AddCommand(
		newChangeCommand(a),
		newReviewCommand(a),
		newStatusCommand(a),
		newDiffCommand(a),
	)
	return root
}

// groupUsage makes a parent command report an unknown subcommand instead of
// silently printing help and exiting 0.
func groupUsage(name string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return &usageError{fmt.Errorf("unknown %s command %q; run `gitpr %s --help`", name, args[0], name)}
		}
		return cmd.Help()
	}
}

// --- shared loading ---------------------------------------------------------

// session is everything a command needs about the current changeset.
type session struct {
	repo    *git.Repo
	cs      changeset.Changeset
	summary lifecycle.Summary
	clean   bool
	head    string
	// baseIsOwnBranch records the unrecoverable base configuration, so the
	// surfaces that can explain it (status, change ready) do not each redo the
	// lookup and so they agree on the wording.
	baseIsOwnBranch bool
}

// load resolves the repository and the current branch's changeset, requiring
// the changeset directory to exist.
func (a *app) load(ctx context.Context) (*session, error) {
	repo, err := a.loadRepo(ctx)
	if err != nil {
		return nil, err
	}
	cs, err := changeset.RequireCurrent(ctx, repo)
	if err != nil {
		return nil, usageWrap(err)
	}
	return a.sessionFor(ctx, repo, cs)
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
func (a *app) sessionFor(ctx context.Context, repo *git.Repo, cs changeset.Changeset) (*session, error) {
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
	return &session{repo: repo, cs: cs, summary: summary, clean: clean, head: head, baseIsOwnBranch: own}, nil
}

// usageWrap marks an error as a usage problem rather than a git failure.
func usageWrap(err error) error {
	if errors.Is(err, changeset.ErrDetachedHead) || errors.Is(err, changeset.ErrNoChangeset) {
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
