package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"gitpr/internal/console"
	"gitpr/internal/span"
)

func newDiffCommand(a *app) *cobra.Command {
	opts := &spanOptions{}
	cmd := &cobra.Command{
		Use:   "diff [path...]",
		Short: "Show or launch the diff for a review span",
		Long: `Resolve a logical review span and hand it to git.

Spans:
  (default)              changeset base ... HEAD        the whole changeset
  --unreviewed           latest review .. HEAD          what happened since you reviewed
  --since-review[=N]     Nth review .. HEAD             N is chronological, -1 is latest

Either end can be named instead, which is how you look at history:
  --head-review=N        end at review N                a historical span
  --head-commit=SHA      end at a commit
  --head-ref=NAME        end at a ref, pinned to where it points now
A historical span is a read-only look: it is history, and history cannot be marked,
edited, or submitted against. A ref keeps its name and the commit it was pinned to;
if the branch moves, gitpr can tell you and keep using the pinned commit.

A review-relative span deliberately includes deletions and edits of lines the
reviewer added, which is what makes resolution visible: a removed
"// Please use a transaction here" is the author telling you they handled it.

gitpr renders nothing itself. Output goes through git, so your pager and colour
settings apply.`,
		Example: `  gitpr diff
  gitpr diff --stat
  gitpr diff src/booking/service.ts
  gitpr diff --unreviewed
  gitpr diff --since-review=-3
  gitpr diff --since-review=0 -- changesets/
  gitpr diff --since-review=-2 --head-review=-1`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(cmd.Context(), a, opts, args)
		},
	}
	opts.register(cmd)
	opts.registerHead(cmd)
	cmd.Flags().BoolVar(&opts.stat, "stat", false, "show diffstat instead of the patch")
	cmd.Flags().BoolVar(&opts.tool, "tool", false, "launch the configured difftool; it compares the span start against the working tree, so edits persist")
	return cmd
}

// spanOptions is the CLI shape of a span selector: the flags `diff` and `review
// open` share, so both resolve spans the same way the picker will.
type spanOptions struct {
	unreviewed  bool
	sinceReview string
	headReview  string
	headCommit  string
	headRef     string
	stat        bool
	tool        bool
}

// register adds the base-end span flags. `stat` and `tool` are diff's own and stay
// with the caller.
func (o *spanOptions) register(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&o.unreviewed, "unreviewed", false, "span since the most recent review submission")
	cmd.Flags().StringVar(&o.sinceReview, "since-review", "",
		"span since review N (bare --since-review means -1)")
	cmd.Flags().Lookup("since-review").NoOptDefVal = "-1"
}

// registerHead adds the flags that name the head, which is what turns a span into a
// read-only look at history. Only commands whose job is read-only get them until the
// review screen enforces the mode.
func (o *spanOptions) registerHead(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.headReview, "head-review", "",
		"end the span at review N rather than the working tree")
	cmd.Flags().StringVar(&o.headCommit, "head-commit", "",
		"end the span at a commit rather than the working tree")
	cmd.Flags().StringVar(&o.headRef, "head-ref", "",
		"end the span at a ref, pinned to the commit it points at now")
	cmd.Flags().Lookup("head-review").NoOptDefVal = "-1"
}

// selector turns the flags into a span selector.
func (o *spanOptions) selector() (span.Selector, error) {
	if o.unreviewed && o.sinceReview != "" {
		return span.Selector{}, &usageError{errors.New("--unreviewed and --since-review are mutually exclusive")}
	}
	base := span.ChangesetBase()
	if o.sinceReview != "" {
		n, err := strconv.Atoi(o.sinceReview)
		if err != nil {
			return span.Selector{}, &usageError{fmt.Errorf("--since-review expects an integer index, got %q", o.sinceReview)}
		}
		base = span.Review(n)
	} else if o.unreviewed {
		base = span.Review(-1)
	}

	head := span.WorkingTree()
	if named := o.headNamed(); named > 1 {
		return span.Selector{}, &usageError{errors.New(
			"--head-review, --head-commit and --head-ref name the same end; pick one")}
	}
	switch {
	case o.headReview != "":
		n, err := strconv.Atoi(o.headReview)
		if err != nil {
			return span.Selector{}, &usageError{fmt.Errorf("--head-review expects an integer index, got %q", o.headReview)}
		}
		head = span.Review(n)
	case o.headCommit != "":
		head = span.Commit(o.headCommit)
	case o.headRef != "":
		head = span.Ref(o.headRef)
	}
	return span.Selector{Base: base, Head: head}, nil
}

func (o *spanOptions) headNamed() int {
	n := 0
	for _, v := range []string{o.headReview, o.headCommit, o.headRef} {
		if v != "" {
			n++
		}
	}
	return n
}

func runDiff(ctx context.Context, a *app, opts *spanOptions, paths []string) error {
	s, err := a.load(ctx)
	if err != nil {
		return err
	}
	so, err := opts.selector()
	if err != nil {
		return err
	}
	sp, err := span.Resolve(ctx, s.repo, s.cs.Base, s.summary, so)
	if err != nil {
		if errors.Is(err, span.ErrNoReviews) {
			return &usageError{fmt.Errorf("%w; run `gitpr diff` for the full changeset", err)}
		}
		return &usageError{err}
	}

	if len(paths) > 0 {
		if err := requirePathsInSpan(ctx, s, sp, paths); err != nil {
			return err
		}
	}

	a.warn("gitpr diff: %s\n", sp.Label)
	if opts.stat {
		args := append([]string{"diff", "--stat", sp.From, sp.To}, pathArgs(paths)...)
		return s.repo.GitInherit(ctx, args...)
	}
	if opts.tool {
		// A live span compares the start against the working tree, so edits made in the tool
		// survive; a historical one compares its two pins.
		to := ""
		if sp.Historical() {
			to = sp.To
		}
		return console.DiffToolCommand(s.repo, sp.From, to, paths).Run()
	}
	return console.DiffCommand(s.repo, sp.From, sp.To, paths).Run()
}

// pathArgs appends a `--` separator only when paths are present.
func pathArgs(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	return append([]string{"--"}, paths...)
}

// requirePathsInSpan turns "you asked for a file that is not in this span" from
// a silently empty diff into an actionable error.
func requirePathsInSpan(ctx context.Context, s *session, sp span.Span, paths []string) error {
	changed, err := s.repo.DiffNames(ctx, sp.From, sp.To)
	if err != nil {
		return err
	}
	for _, want := range paths {
		if !anyPrefix(changed, want) {
			return &usageError{fmt.Errorf(
				"%q does not appear in %s; changed paths:\n  %s",
				want, sp.Label, strings.Join(changed, "\n  "))}
		}
	}
	return nil
}

func anyPrefix(paths []string, prefix string) bool {
	prefix = strings.TrimPrefix(prefix, "./")
	for _, p := range paths {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}
