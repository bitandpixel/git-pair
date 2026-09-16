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
  gitpr diff --since-review=0 -- changesets/`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(cmd.Context(), a, opts, args)
		},
	}
	cmd.Flags().BoolVar(&opts.unreviewed, "unreviewed", false, "span since the most recent review submission")
	cmd.Flags().StringVar(&opts.sinceReview, "since-review", "",
		"span since review N (bare --since-review means -1)")
	cmd.Flags().BoolVar(&opts.stat, "stat", false, "show diffstat instead of the patch")
	cmd.Flags().BoolVar(&opts.tool, "tool", false, "launch the configured difftool instead of printing")
	// A bare --since-review means "the latest review", matching --unreviewed.
	cmd.Flags().Lookup("since-review").NoOptDefVal = "-1"
	return cmd
}

// spanOptions is the CLI shape of span.Options.
type spanOptions struct {
	unreviewed  bool
	sinceReview string
	stat        bool
	tool        bool
}

func (o spanOptions) toSpanOptions() (span.Options, error) {
	if o.unreviewed && o.sinceReview != "" {
		return span.Options{}, &usageError{errors.New("--unreviewed and --since-review are mutually exclusive")}
	}
	out := span.Options{Unreviewed: o.unreviewed}
	if o.sinceReview != "" {
		n, err := strconv.Atoi(o.sinceReview)
		if err != nil {
			return out, &usageError{fmt.Errorf("--since-review expects an integer index, got %q", o.sinceReview)}
		}
		out.SinceReview = &n
	}
	return out, nil
}

func runDiff(ctx context.Context, a *app, opts *spanOptions, paths []string) error {
	s, err := a.load(ctx)
	if err != nil {
		return err
	}
	so, err := opts.toSpanOptions()
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
		return console.DiffToolCommand(s.repo, sp.From, sp.To, paths).Run()
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
