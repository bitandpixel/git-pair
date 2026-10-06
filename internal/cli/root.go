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
)

// Version is what `git-pair --version` prints. A var, not a const: a release build overwrites it with
// the tag through `-ldflags -X gitpair/internal/cli.Version=…`, which `.goreleaser.yaml` sets. A const
// cannot be overwritten, so the stamped value would be refused at link time and every release would
// report the value below. That value is therefore the dev-build answer — what `mise run build` gives
// you — and so it names a state rather than a release: a build from a commit with no tag has no version
// to report, and the value has to be one nobody can mistake for a published number. `0.1.0` could be so
// mistaken, which is how a bug gets filed against a build that was never shipped. No release can ever
// be named this, because a release is named by its tag.
var Version = "0.0.0-untagged"

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
	// noCache turns off both local caches for one run. The answers are the same either way — a cached
	// fact is derived from the same commits the uncached derivation reads — so this is for measuring, for
	// debugging a suspected bad answer, and for a machine where writing to the git directory is unwanted.
	noCache bool
	// defaultBranch is the `--default-branch` override. Every command that asks "has this
	// landed?" needs the integration branch to answer, and CI passes this because a checkout
	// built with `init` and one `fetch` has no recorded remote default to read.
	defaultBranch string
	// defaultBranchEnvNoted records that this run has already said on stderr that the environment
	// named the integration branch. Resolving the destination is not one call — a single command
	// resolves it several times — and a note repeated for each would be noise rather than visibility.
	defaultBranchEnvNoted bool
}

// NoCacheEnv is the environment form of `--no-cache`.
//
// It exists for two callers the flag cannot reach. A CI job runs git-pair from many places, and a switch
// that has to be threaded through every invocation is a switch that gets missed. The test suite needs it
// more specifically: `gittest.SpawnShim` counts git subprocesses to bound how much work a formulation does,
// and a memo that answers a duplicated read would let that bound pass on the very duplicate it exists to
// catch. So a measurement sets this and counts the algorithm rather than the cache.
//
// Anything non-empty counts as set, including "0" and "false" — the rule that cannot be argued about, and
// the one that keeps `GIT_PAIR_NO_CACHE=false` from being a surprising way to leave caching on.
const NoCacheEnv = "GIT_PAIR_NO_CACHE"

// cachingOff reports whether this run must use neither cache.
//
// The flag and the environment can each turn caching off; neither can turn it back on. That asymmetry is the
// useful one — a job that set the variable globally still gets "off" from a command that also passes the
// flag, and there is no spelling which silently re-enables what a caller asked to disable.
func (a *app) cachingOff() bool {
	return a.noCache || os.Getenv(NoCacheEnv) != ""
}

// DefaultBranchEnv is the environment form of `--default-branch`.
//
// It exists for the same caller `GIT_PAIR_NO_CACHE` exists for: a CI job runs git-pair from many
// places, and a value that has to be threaded through every invocation is a value that gets missed
// in one of them. The shipped merge job sets it once from the repository's `GIT_PAIR_BASE`
// variable (`scripts/ci/git-pair-integrate.sh`) and a person whose trunk is not `main` can export
// it for a shell instead of typing the flag at every command.
//
// What it moves is the destination rather than the amount of work, which is the difference from
// `GIT_PAIR_NO_CACHE` and the reason using it is made visible: the run says on stderr that the
// environment named the branch, and `status --json` reports `default_branch_source: "env"` rather
// than the `flag` an identical command line would report. Nothing about the value is guessed at —
// it is a revision expression, and one that does not resolve is the same refusal as the flag's.
const DefaultBranchEnv = "GIT_PAIR_DEFAULT_BRANCH"

// defaultBranchOverride is the ref the caller supplied as the integration branch, and whether it
// came from the environment rather than from the command line.
//
// The flag wins outright. It is the statement about this invocation, and a value inherited from the
// shell must not be able to contradict it — the case that matters is the CI job that exports the
// variable for its merge script and then runs one `git pair` command against another destination.
// An empty value is not set, so `GIT_PAIR_DEFAULT_BRANCH=` falls back to git's own answer instead
// of resolving an empty ref.
func (a *app) defaultBranchOverride() (string, bool) {
	if a.defaultBranch != "" {
		return a.defaultBranch, false
	}
	if ref := os.Getenv(DefaultBranchEnv); ref != "" {
		return ref, true
	}
	return "", false
}

// resolveDefaultBranch answers the integration-branch question for this run: the flag, then the
// environment, then git's own answer. Every command that needs the destination reaches it through
// here, so the environment form cannot be honoured by some commands and missed by others, and the
// provenance of the answer travels with it.
func (a *app) resolveDefaultBranch(ctx context.Context, repo *git.Repo) (changeset.DefaultBranchRef, error) {
	override, fromEnv := a.defaultBranchOverride()
	db, err := changeset.DefaultBranch(ctx, repo, override)
	if err != nil {
		if fromEnv {
			// The refusal is about a value nobody typed, so it names where it came from before it is
			// returned; a reader sent to fix a command line that was never wrong learns nothing from it.
			a.noteDefaultBranchEnv(override)
		}
		return changeset.DefaultBranchRef{}, err
	}
	if fromEnv {
		// `DefaultBranch` labels any caller-supplied ref `flag`, which is true of the flag and misleading
		// about this one: the destination a run measured against came from the environment it inherited.
		db.Source = changeset.DefaultBranchEnvironment
		a.noteDefaultBranchEnv(db.Ref)
	}
	return db, nil
}

// noteDefaultBranchEnv says once per run that the environment named the destination, and what it
// named. A destination chosen by a variable nobody typed is the failure this run has to make visible
// in its own output rather than leave to whoever wonders why `queue` looked empty.
func (a *app) noteDefaultBranchEnv(ref string) {
	if a.defaultBranchEnvNoted {
		return
	}
	a.defaultBranchEnvNoted = true
	a.warn("git-pair: integration branch %s comes from $%s\n", displayRef(ref), DefaultBranchEnv)
}

// Execute builds the command tree and runs it, returning the process exit code.
func Execute(args []string) int {
	a := &app{stdout: os.Stdout, stderr: os.Stderr}
	root := newRootCommand(a)
	root.SetArgs(args)
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)

	cmd, err := root.ExecuteC()
	if err == nil {
		a.warnUnansweredJSON(cmd)
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

// noJSONCommands are the commands that take the global `--json` and have no machine-readable form to
// offer. Each is a viewer: the whole answer is a patch, a report, or a document a person reads. `--json`
// is global (PRD §8), so these commands accept the flag, and the defect was the silence after it — a
// machine that asks for JSON and reads an empty stdout learns "nothing", not "this command has no
// JSON".
var noJSONCommands = map[string]bool{
	"change feedback": true,
	"diff":            true,
	"skill show":      true,
	"skill agents-md": true,
}

// warnUnansweredJSON says out loud that `--json` changed nothing on a viewer. The note stays on stderr, so
// a caller that pipes the report somewhere is untouched.
func (a *app) warnUnansweredJSON(cmd *cobra.Command) {
	if !a.json || cmd == nil {
		return
	}
	path := strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")
	if !noJSONCommands[path] {
		return
	}
	a.warn("git-pair: `git pair %s` has no --json output; what it printed is the report itself\n", path)
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
review threads, lifecycle markers are commits carrying Review-* trailers, and a
landing is the changeset directory in the history of the branch it merged into.
git-pair writes no ref of its own.

Author commands:   git pair init, then git pair change ready | integrate | unready | abandon, with
                   change stack and change combine as the exits from a branch carrying two
Reviewer commands: git pair review open | about | thread | submit | history
Reading state:     git pair queue | status | diff
Gate:              git pair check, then merge into the destination with ordinary git`,
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
	root.PersistentFlags().BoolVar(&a.noCache, "no-cache", false,
		"derive everything from git again, ignoring the local caches under the git directory")
	root.PersistentFlags().StringVar(&a.defaultBranch, "default-branch", "",
		"ref of the integration branch; otherwise git-pair reads git's own answer (origin/HEAD, then a sole main/master). $"+DefaultBranchEnv+" is the same value for a whole process")
	root.AddCommand(
		newInitCommand(a),
		newChangeCommand(a),
		newReviewCommand(a),
		newQueueCommand(a),
		newStatusCommand(a),
		newDiffCommand(a),
		newCheckCommand(a),
		newSkillCommand(a),
	)
	return root
}

// groupUsage makes a parent command report an unknown subcommand instead of
// silently printing help and exiting 0.
func groupUsage(name string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return unknownGroupCommand(name, args)
		}
		return cmd.Help()
	}
}

// unknownGroupCommand is what a group says about a word that is not one of its commands. It is apart
// from groupUsage because `review` is a group with a command of its own, so its RunE answers the real
// invocation and needs the refusal on its own.
func unknownGroupCommand(name string, args []string) error {
	return &usageError{fmt.Errorf("unknown %s command %q; run `git-pair %s --help`", name, args[0], name)}
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

// destination is the integration branch this run should measure against, or the zero value when the
// repository cannot name one. The zero value has an empty Ref, which is what every tree-based question
// reads as "cannot tell, so do not refuse" — the failure mode a caller wants: a clone that has not worked
// out which branch is main keeps answering about the work instead of blaming it for the clone.
func (a *app) destination(ctx context.Context, repo *git.Repo) changeset.DefaultBranchRef {
	db, err := a.resolveDefaultBranch(ctx, repo)
	if err != nil {
		return changeset.DefaultBranchRef{}
	}
	return db
}

// measureBase is the ref this changeset's diff is measured against, which the recorded `base:` usually is
// and the fetched copy of the integration branch is when the record names that branch. Every span resolves
// through here so that the range a reviewer reads and the base printed above it are one answer; the record
// itself is what `base:` and `--json`'s `base` report, because that is the value the file carries and other
// machines read.
func (s *session) measureBase(ctx context.Context) string {
	return changeset.MeasureBase(ctx, s.repo, s.cs, s.trunk, s.head)
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
	db, err := a.resolveDefaultBranch(ctx, repo)
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
	scan, err := changeset.ScanBranches(ctx, repo, db)
	if err != nil {
		return changeset.Changeset{}, lifecycle.Summary{}, "", err
	}
	var branches []string
	selected := map[string]changeset.Candidate{}
	for _, br := range scan.Branches {
		if br.Err != nil || br.Resolution.Selected == nil {
			continue
		}
		if br.Resolution.Selected.Changeset.Slug == slug {
			branches = append(branches, br.Branch)
			selected[br.Branch] = *br.Resolution.Selected
		}
	}
	if len(branches) == 0 {
		// No branch carries the slug, so the destination's own history is the only place its record can be
		// read — which is exactly what a reader asking about a landed changeset wants. For a changeset that
		// never landed there is nothing to read: the branch was the record, and it is gone. The walk reports
		// what the destination carries and nothing more, which is why it answers rather than becoming a second
		// source of state (PRD §12).
		if db.Ref == "" {
			return changeset.Changeset{}, lifecycle.Summary{}, "", &usageError{
				fmt.Errorf("no branch carries changeset %q, and this clone cannot name the destination to look in: pass --default-branch", slug)}
		}
		chain, err := changeset.LandedChain(ctx, repo, db.Ref, slug)
		if errors.Is(err, changeset.ErrNoChain) {
			return changeset.Changeset{}, lifecycle.Summary{}, "", &usageError{
				fmt.Errorf("no branch carries changeset %q, and %s holds no directory for it either: `git pair queue` lists what this repository has",
					slug, displayRef(db.LocalName()))}
		}
		if err != nil {
			return changeset.Changeset{}, lifecycle.Summary{}, "", err
		}
		// The range end is the newest commit in the run, and the range start is what the run sits on, so the
		// markers, the verdict and the thread files are read from the same span the branch carried before it
		// was merged away.
		anchor := chain.Head
		// The stack keys come from the same read as the base, because the landed read needs them for the same
		// reason a branch read does: a changeset that landed on a branch since tidied away has only its yaml
		// to say what it was stacked on, and the chain above it is the difference between "this reached
		// trunk" and "this reached a branch that later did".
		stack, err := changeset.StackAt(ctx, repo, anchor, slug)
		if errors.Is(err, git.ErrUnknownPath) {
			return changeset.Changeset{}, lifecycle.Summary{}, "", &usageError{
				fmt.Errorf("changeset %q is on %s at %s but carries no %s", slug, displayRef(db.LocalName()),
					short(anchor), changeset.MetadataFile)}
		}
		if err != nil {
			return changeset.Changeset{}, lifecycle.Summary{}, "", err
		}
		// The chain's own start is the base for the read: it is where the run sits, which is the range the
		// markers live in. It is a derived base, so it is named as one — the reader of a landed changeset
		// sees a commit and the reason for it rather than a bare object id. A stacked child whose parent
		// branch is gone gets the same derivation every other surface uses, so the chain read and the
		// branch read cannot print two bases for one changeset.
		base := chain.Base
		baseWhy := "the run the destination's chain carries"
		// Derived, so that nothing downstream mistakes it for a place work can land: a commit the run sits
		// on is a measurement point, and the question "where does this go" has to be answered one level up.
		baseDerived := true
		if stack.Parent != "" && stack.BaseChangeset != "" {
			if _, err := repo.RevParse(ctx, "refs/heads/"+stack.Parent); errors.Is(err, git.ErrUnknownRevision) {
				b, berr := changeset.BaseFor(ctx, repo, changeset.Changeset{Slug: slug, Base: base,
					ParentBranch: stack.Parent, BaseChangeset: stack.BaseChangeset}, chain.Head, db)
				if berr != nil && !errors.Is(berr, changeset.ErrNoDefaultBranch) {
					return changeset.Changeset{}, lifecycle.Summary{}, "", berr
				}
				if b.Ref != "" {
					base, baseWhy, baseDerived = b.Ref, b.Why, b.Derived
				}
			}
		}
		cs := changeset.Changeset{
			Slug:          slug,
			Dir:           filepath.Join(changeset.Root, slug),
			Base:          base,
			BaseWhy:       baseWhy,
			BaseDerived:   baseDerived,
			ParentBranch:  stack.Parent,
			BaseChangeset: stack.BaseChangeset,
			Exists:        true,
		}
		summary, err := lifecycle.Summarize(ctx, repo, slug, base, anchor)
		if err != nil {
			return changeset.Changeset{}, lifecycle.Summary{}, "", err
		}
		// The reviews a record read reports come from the archived chain, not from the span. After a merge
		// landing the archived head sits below the base — the landing put the reviewed work inside the
		// destination — so `base..anchor` is empty for exactly the changeset whose verdicts matter most, and
		// a reader asking about a landed child would be told "no reviews yet" about a head its reviewer
		// approved. State is deliberately left alone: PRD §13.4 says the archive is not a second source of
		// state, and a full lineage walk would have a landed child read as READY.
		if len(summary.Reviews) == 0 {
			reviews, rerr := lifecycle.ReviewsInLineage(ctx, repo, slug, anchor)
			if rerr != nil {
				return changeset.Changeset{}, lifecycle.Summary{}, "", rerr
			}
			summary.Reviews = reviews
			if len(reviews) > 0 {
				latest := reviews[len(reviews)-1]
				summary.LatestReview = &latest
			}
		}
		// With nothing in the span, the span's reason ("no commits above the base yet") is a true statement
		// about a range nobody meant to ask about. Say what the read was.
		if len(summary.Events) == 0 {
			summary.Reason = fmt.Sprintf("read from %s's history; the chain is below the base, so there is no span to report",
				displayRef(db.LocalName()))
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
	db, err := a.resolveDefaultBranch(ctx, repo)
	if err != nil {
		return nil, usageWrap(err)
	}
	cs, err := changeset.RequireCurrentOn(ctx, repo, db)
	if err != nil {
		return nil, usageWrap(err)
	}
	s, err := a.sessionFor(ctx, repo, cs, db)
	if err != nil {
		return nil, a.explainBrokenStack(ctx, repo, cs, db, err)
	}
	return s, nil
}

// explainBrokenStack turns "the base does not resolve" into what that means for a stack. A child is
// measured against its parent branch; when that branch is gone and the integration branch holds no landing
// to measure against instead, every command that measures answers with git's own unknown-revision error,
// which names neither the parent nor the way out. The stack needs a decision from its author, so the
// message says so (PRD §21).
func (a *app) explainBrokenStack(ctx context.Context, repo *git.Repo, cs changeset.Changeset,
	db changeset.DefaultBranchRef, err error) error {
	if cs.ParentBranch == "" || !errors.Is(err, git.ErrUnknownRevision) {
		return err
	}
	if _, e := repo.RevParse(ctx, "refs/heads/"+cs.ParentBranch); e == nil {
		return err
	}
	if cs.BaseChangeset != "" && db.Ref != "" {
		if landed, _ := changeset.CarriesDir(ctx, repo, db.Ref, cs.BaseChangeset); landed {
			return err
		}
	}
	return fmt.Errorf("%s is stacked on %s, which is gone and has no landing in %s: the stack is unreconciled — "+
		"choose a new base with `git pair init --parent <branch> --set-parent`, or land the parent first: %w",
		cs.Slug, cs.ParentBranch, displayRef(db.LocalName()), err)
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
	// A command run is one observation of a repository the command itself does not move, so repeated
	// git reads within it have one answer. The two surfaces that outlive a run — the review session and
	// `change wait` — turn this back off where they start, because across them refs really do move.
	//
	// `--no-cache` turns both off. It is not a correctness escape hatch — a cached fact is derived from the
	// same commits the uncached derivation reads, and an invalid key is a miss rather than a stale answer —
	// so it exists to let a slow command be measured against a fast one, and to let a machine that would
	// rather nothing were written under its git directory say so.
	if !a.cachingOff() {
		repo.Memoize(true)
		// The derived-fact cache is on for every handle this hands out, including the long-lived ones: its
		// answers are keyed on commit ids, and a commit does not change under a session that kept reading it.
		repo.FactCache(true)
	}
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
// answer, and both ways out — `--changeset <id>` for one command, the two exits for the
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

// orEmpty is the empty-list half of git-pair's `--json` contract: an array is `[]` for "asked, and
// none", never null. A null says the question was not asked, which is true of no command here — a key
// that does not apply is left out with `omitempty`, and a field that reports it cannot answer is an
// object or a bool (`status`'s `latest_review`, `uncommitted`). README's JSON contracts section states the
// rule, and TestNoJSONArrayIsEverNull keeps it true for keys nobody has thought about yet.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
