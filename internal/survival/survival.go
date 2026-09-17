// Package survival implements the surviving-review-additions check (PRD §19).
//
// A line "survives" when the most recent review submission introduced it and
// the exact same line still exists unchanged at HEAD. Anything the author left
// untouched disappears from a `review..HEAD` diff, so without this check
// reviewer feedback can silently return to the reviewer unaddressed.
package survival

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
)

// Addition is one distinct line added by a review submission.
type Addition struct {
	Path string `json:"path"`
	// Line is a 1-based line number: the position inside the review commit for
	// AddedLines, and the position at HEAD for a surviving addition.
	Line int    `json:"line"`
	Text string `json:"text"`
	// Count is how many identical copies the review added. Reporting one
	// location for N copies avoids a wall of near-identical output; the count
	// keeps that honest.
	Count int `json:"count"`
}

// Report is the outcome of a survival check.
type Report struct {
	ReviewSHA   string `json:"review_commit"`
	ReviewShort string `json:"review_short"`
	// Code holds additions outside the changeset directory. These block
	// `change ready` and `change archive`.
	Code []Addition `json:"code"`
	// Artifacts holds additions inside a changesets/ directory. Reviewer text
	// there is conversation surface rather than code to resolve — a thread
	// answered by appending still contains every original line — so it is
	// reported but never blocks. See
	// docs/plans/completed/gitpr-mvp/research/git-plumbing-findings.md.
	Artifacts []Addition `json:"artifacts"`
	// AddedLines is the total number of non-blank lines the review added.
	AddedLines int `json:"added_lines"`
}

// Clean reports whether nothing blocks the caller.
func (r Report) Clean() bool { return len(r.Code) == 0 }

// Total is the number of surviving additions, blocking and non-blocking.
func (r Report) Total() int { return len(r.Code) + len(r.Artifacts) }

// ErrMergeCommit is returned for a review submission with several parents.
// git-pair only creates single-parent reviews; a merged review has no unambiguous
// set of "lines this review added".
var ErrMergeCommit = errors.New("review submission is a merge commit")

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// Check reports which of review's additions still exist unchanged at HEAD.
func Check(ctx context.Context, repo *git.Repo, review string) (Report, error) {
	additions, total, err := AddedLines(ctx, repo, review)
	if err != nil {
		return Report{}, err
	}
	report := Report{ReviewSHA: review, ReviewShort: short(review), AddedLines: total}

	// One blob read per touched path; nil means the path is gone at HEAD.
	blobs := map[string][]string{}
	for _, a := range additions {
		blob, cached := blobs[a.Path]
		if !cached {
			content, err := repo.ShowFile(ctx, "HEAD", a.Path)
			switch {
			case err == nil:
				blob = strings.Split(content, "\n")
			case errors.Is(err, git.ErrUnknownPath):
				blob = nil // deleted at HEAD, so nothing added there can survive
			default:
				return Report{}, err
			}
			blobs[a.Path] = blob
		}
		if blob == nil {
			continue
		}
		if line := findLine(blob, a.Text); line > 0 {
			a.Line = line
			if isArtifactPath(a.Path) {
				report.Artifacts = append(report.Artifacts, a)
			} else {
				report.Code = append(report.Code, a)
			}
		}
	}
	sortReport(&report)
	return report, nil
}

// AddedLines returns the non-blank lines introduced by review, ordered by path
// then position, deduplicated by content with an occurrence count.
func AddedLines(ctx context.Context, repo *git.Repo, review string) ([]Addition, int, error) {
	parents, err := repo.Git(ctx, "rev-list", "--parents", "-n", "1", review)
	if err != nil {
		return nil, 0, err
	}
	if len(strings.Fields(parents)) > 2 {
		return nil, 0, fmt.Errorf("%w: %s", ErrMergeCommit, review)
	}

	// `git show` diffs a commit against its first parent and handles the
	// repository's root commit for free. --no-ext-diff and --no-textconv keep
	// the output parseable regardless of the user's diff configuration, and
	// --no-renames makes a rename read as delete-plus-add, which is the
	// conservative reading for "did this line survive".
	flags := []string{"-U0", "--no-ext-diff", "--no-textconv", "--no-renames", "--format=", review}
	diff, err := repo.Git(ctx, append([]string{"show"}, flags...)...)
	if err != nil {
		return nil, 0, err
	}
	numstat, err := repo.Git(ctx, append([]string{"show", "--numstat"}, flags...)...)
	if err != nil {
		return nil, 0, err
	}

	additions := parseAddedLines(diff, binaryPaths(numstat))
	total := 0
	for _, a := range additions {
		total += a.Count
	}
	return additions, total, nil
}

func sortReport(r *Report) {
	for _, list := range [][]Addition{r.Code, r.Artifacts} {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].Path != list[j].Path {
				return list[i].Path < list[j].Path
			}
			return list[i].Line < list[j].Line
		})
	}
}

// isArtifactPath reports whether path is review state rather than code.
func isArtifactPath(path string) bool {
	return path == changeset.Root || strings.HasPrefix(path, changeset.Root+"/")
}

// binaryPaths collects paths git reports as binary, using the literal dashes
// `--numstat` emits for binary change counts.
func binaryPaths(numstat string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(numstat, "\n"), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 || (parts[0] != "-" && parts[1] != "-") {
			continue
		}
		out[parts[2]] = true
	}
	return out
}

// parseAddedLines walks `git show -U0` output and returns distinct added lines.
//
// The hunk header's `+<n>` is the post-image line the hunk starts at, and every
// added or context line advances it, so the counter is read before it is bumped.
//
// It relies on four properties of that stream: the post-image path appears on a
// `+++ b/<path>` header before any of the file's hunks; a zero-context hunk
// body contains only `+` and `-` lines; `\ No newline at end of file` is not
// content; and binary files emit `Binary files ... differ` with no `+` lines.
func parseAddedLines(diff string, binary map[string]bool) []Addition {
	type key struct{ path, text string }
	var order []key
	seen := map[key]*Addition{}

	var path string
	var lineNo int
	inHunk := false

	for _, raw := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(raw, "diff --git "):
			path, inHunk = "", false
			continue
		case strings.HasPrefix(raw, "+++ "):
			// Prefer the post-image header over `diff --git`, which is
			// ambiguous for renames. /dev/null means the file is gone.
			p := strings.TrimPrefix(raw, "+++ ")
			if i := strings.Index(p, "/"); i == 1 {
				p = p[2:]
			}
			if p == "/dev/null" {
				p = ""
			}
			path, inHunk = p, false
			continue
		case strings.HasPrefix(raw, "@@"):
			inHunk = false
			if m := hunkHeader.FindStringSubmatch(raw); m != nil {
				fmt.Sscanf(m[1], "%d", &lineNo)
				inHunk = true
			}
			continue
		}

		if !inHunk || path == "" || binary[path] {
			continue
		}
		if strings.HasPrefix(raw, `\`) || strings.HasPrefix(raw, "-") {
			continue
		}
		if !strings.HasPrefix(raw, "+") {
			lineNo++ // zero context means this should not occur; count it anyway
			continue
		}
		text := raw[1:]
		// Blank lines cannot carry feedback and would dominate the report.
		if strings.TrimSpace(text) == "" {
			lineNo++
			continue
		}
		k := key{path, text}
		if a, ok := seen[k]; ok {
			a.Count++
		} else {
			seen[k] = &Addition{Path: path, Text: text, Line: lineNo, Count: 1}
			order = append(order, k)
		}
		lineNo++
	}

	out := make([]Addition, 0, len(order))
	for _, k := range order {
		out = append(out, *seen[k])
	}
	return out
}

// findLine returns the 1-based index of an exact, untrimmed line match.
func findLine(blob []string, text string) int {
	for i, l := range blob {
		if l == text {
			return i + 1
		}
	}
	return 0
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
