package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"gitpair/internal/survival"
)

// now is the clock source, kept in one place so age reporting is testable.
var now = func() time.Time { return time.Now() }

// short renders a SHA for humans.
func short(sha string) string {
	if utf8.RuneCountInString(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// survivalCheck runs the surviving-review-additions diagnostic against the most
// recent review submission, or returns nil when the changeset has none.
//
// The check intentionally covers only the latest review: older feedback was
// already resolved or acknowledged across a previous boundary.
func survivalCheck(ctx context.Context, s *session) (*survival.Report, error) {
	if s.summary.LatestReview == nil {
		return nil, nil
	}
	report, err := survival.Check(ctx, s.repo, s.summary.LatestReview.SHA)
	if err != nil {
		return nil, err
	}
	return &report, nil
}

// printSurvivalReport renders blocking additions in the shape specified by PRD §19.2.
func printSurvivalReport(w io.Writer, r survival.Report, header, hint string) {
	fmt.Fprintf(w, "\n%s\n\n", header)
	noun := "additions"
	if len(r.Code) == 1 {
		noun = "addition"
	}
	fmt.Fprintf(w, "%d %s from review %s still survive unchanged:\n\n", len(r.Code), noun, r.ReviewShort)
	for _, a := range r.Code {
		fmt.Fprintf(w, "%s:%d\n", a.Path, a.Line)
		fmt.Fprintf(w, "  %s\n", a.Text)
		if a.Count > 1 {
			fmt.Fprintf(w, "  (%d identical lines were added by the review)\n", a.Count)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "\nReview these additions before continuing.\n")
	if hint != "" {
		fmt.Fprintf(w, "\nTo intentionally preserve them:\n  %s\n", hint)
	}
}

// printArtifactSurvivals reports surviving reviewer text inside the changeset
// directory. It never blocks: a thread answered by appending still contains
// every line the reviewer wrote, and ABOUT.md edits are normally kept.
func printArtifactSurvivals(w io.Writer, r survival.Report) {
	if len(r.Artifacts) == 0 {
		return
	}
	fmt.Fprintf(w, "\nAlso still present from review %s (changeset artifacts, not blocking):\n", r.ReviewShort)
	for _, a := range r.Artifacts {
		fmt.Fprintf(w, "  %s:%d  %s\n", a.Path, a.Line, firstLine(a.Text))
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + "…"
	}
	return s
}
