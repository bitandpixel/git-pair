// Package tui implements the small review orchestration screen from PRD §14.
//
// Session is the headless half: it resolves the span, lists changed files, and
// tracks which ones the reviewer has looked at. It has no rendering or terminal
// dependency, so it is unit-testable without a TTY. tui.go is the Bubble Tea
// half that draws it and hands the terminal to external programs.
package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gitpr/internal/changeset"
	"gitpr/internal/git"
	"gitpr/internal/lifecycle"
	"gitpr/internal/reviewmark"
	"gitpr/internal/span"
)

// Options configures a review session.
type Options struct {
	Repo      *git.Repo
	Changeset changeset.Changeset
	Summary   lifecycle.Summary
	// Span is the requested starting span; a missing value means the full
	// changeset.
	Span span.Options
	// Out receives the one-line summary printed after the session ends, so a
	// submission is visible in the normal screen once the alt screen is gone.
	// Defaults to os.Stdout.
	Out io.Writer
}

// Header is the session's identity line.
type Header struct {
	Title     string
	Base      string
	SpanLabel string
}

// File is one changed file with its local review state.
type File struct {
	Path string
	// Key identifies the file's diff content within the current span. When the
	// key changes, the file has moved back to unreviewed (PRD §16).
	Key      string
	Reviewed bool
}

// Session is the review state the TUI renders.
type Session struct {
	repo      *git.Repo
	cs        changeset.Changeset
	summary   lifecycle.Summary
	spanOpts  span.Options
	current   span.Span
	files     []File
	reviewRef string

	// Marks are persisted outside the working tree so a review can be resumed; see
	// internal/reviewmark for where and why.
	marks     *reviewmark.Store
	markErr   error
	persisted reviewmark.Set
	resumed   int
}

// NewSession resolves the span and scans the changed files.
func NewSession(ctx context.Context, opts Options) (*Session, error) {
	s := &Session{
		repo: opts.Repo, cs: opts.Changeset, summary: opts.Summary, spanOpts: opts.Span,
		reviewRef: "refs/reviews/" + opts.Changeset.Slug,
	}
	if err := s.Rescan(ctx); err != nil {
		return nil, err
	}
	s.loadMarks(ctx)
	return s, nil
}

// Rescan recomputes the span and file list, preserving review marks whose file
// content in the span has not changed.
func (s *Session) Rescan(ctx context.Context) error {
	sp, err := span.Resolve(ctx, s.repo, s.cs.Base, s.summary, s.spanOpts)
	if err != nil {
		return err
	}
	s.current = sp

	names, err := s.repo.DiffNames(ctx, sp.From, sp.To)
	if err != nil {
		return err
	}
	keys, err := s.diffKeys(ctx, sp)
	if err != nil {
		return err
	}
	previous := map[string]File{}
	for _, f := range s.files {
		previous[f.Path] = f
	}

	files := make([]File, 0, len(names))
	for _, name := range names {
		key := keys[name]
		marked := false
		if old, ok := previous[name]; ok && old.Key == key {
			marked = old.Reviewed
		}
		if !marked && s.persisted[name] == key {
			// Same file, same diff content as when it was marked in an earlier
			// session. Anything else — new commit, rebase, different span — keys
			// differently and so stays unreviewed.
			marked = true
		}
		files = append(files, File{Path: name, Key: key, Reviewed: marked})
	}
	s.files = files
	return nil
}

// Reload re-derives the lifecycle state and rescans. Called after an external
// process may have changed the repository, per PRD §15.
func (s *Session) Reload(ctx context.Context) error {
	summary, err := lifecycle.SummarizeHEAD(ctx, s.repo, s.cs.Slug, s.cs.Base)
	if err != nil {
		return err
	}
	s.summary = summary
	return s.Rescan(ctx)
}

// ToggleSpan flips between the full changeset and the unreviewed span, which is
// what `v` does. It is a no-op when there is nothing to compare against.
func (s *Session) ToggleSpan(ctx context.Context) error {
	if !s.CanToggleSpan() {
		return fmt.Errorf("no review submissions yet, so there is nothing to compare HEAD against")
	}
	switch {
	case s.spanOpts.Covered:
		// Covered is only reached when nothing landed after the review, so the
		// since-review span has no content to toggle to; the whole changeset does.
		s.spanOpts = span.Options{}
	case s.spanOpts.Unreviewed:
		s.spanOpts = span.Options{}
	default:
		s.spanOpts = span.Options{Unreviewed: true}
	}
	return s.Rescan(ctx)
}

// CanToggleSpan reports whether an unreviewed span is available.
func (s *Session) CanToggleSpan() bool { return len(s.summary.Reviews) > 0 }

// Unreviewed reports whether the session currently shows the unreviewed span.
func (s *Session) Unreviewed() bool { return s.spanOpts.Unreviewed }

// Span is the resolved span being reviewed.
func (s *Session) Span() span.Span { return s.current }

// Files is the changed-file list for the current span.
func (s *Session) Files() []File { return s.files }

// Toggle flips the reviewed mark for index i.
func (s *Session) Toggle(i int) {
	if i < 0 || i >= len(s.files) {
		return
	}
	s.files[i].Reviewed = !s.files[i].Reviewed
}

// Count returns reviewed and total file counts.
func (s *Session) Count() (reviewed, total int) {
	for _, f := range s.files {
		if f.Reviewed {
			reviewed++
		}
	}
	return reviewed, len(s.files)
}

// Header renders the identity line.
func (s *Session) Header() Header {
	return Header{Title: s.cs.Slug, Base: s.cs.Base, SpanLabel: s.current.Label}
}

// AboutPath is the changeset's ABOUT.md, relative to the repository root.
func (s *Session) AboutPath() string { return s.cs.AboutPath() }

// Threads lists the changeset's review thread files.
func (s *Session) Threads() ([]string, error) { return s.cs.Threads(s.repo) }

// HasVersionAt reports whether path existed in rev. It is what separates a document the span
// rewrote from one the span invented, which is the difference between a diff with two sides
// and a diff against nothing.
func (s *Session) HasVersionAt(ctx context.Context, rev, path string) bool {
	return s.repo.PathExistsAt(ctx, rev, path)
}

// maxPreviewBytes bounds what a preview pane may carry. A patch longer than this is a file to
// open in the difftool, not one to read in a side column.
const maxPreviewBytes = 256 << 10

// Patch is one path's diff across the session's span, in git's own colours.
//
// The preview shows these bytes and adds nothing to them, which is deliberate: PRD §3 rules out
// building a diff renderer, and the way to show a diff without becoming one is to show git's.
type Patch struct {
	Lines   []string // git's coloured output, verbatim
	Added   int      // from git's numstat; -1 when git reported a binary file
	Deleted int
	Capped  bool   // longer than a preview can carry
	Err     string // why there is no patch, if there is none
}

// Patch asks git for one path's diff across the current span.
//
// It returns no error: a preview that cannot be drawn is a line in the pane, not a problem the
// reviewer has to handle, and this runs in the background while they keep moving the cursor.
func (s *Session) Patch(ctx context.Context, path string) Patch {
	sp := s.current
	out, err := s.repo.Git(ctx, "-c", "core.quotePath=false", "diff",
		"--no-ext-diff", "--no-textconv", "--color=always", sp.From, sp.To, "--", path)
	if err != nil {
		return Patch{Err: err.Error()}
	}
	p := Patch{Added: -1, Deleted: -1}
	if len(out) > maxPreviewBytes {
		cut := strings.LastIndex(out[:maxPreviewBytes], "\n")
		if cut < 0 {
			cut = maxPreviewBytes
		}
		out, p.Capped = out[:cut], true
	}
	if out != "" {
		p.Lines = strings.Split(out, "\n")
	}
	// The counts come from git rather than by counting these lines, so a capped patch still
	// reports the file's real size.
	num, err := s.repo.Git(ctx, "diff", "--no-ext-diff", "--numstat", sp.From, sp.To, "--", path)
	if err == nil {
		if line := strings.TrimSpace(strings.SplitN(num, "\n", 2)[0]); line != "" {
			if fields := strings.Fields(line); len(fields) >= 2 {
				// A binary file reports "-" for both counts, which leaves the counts at -1
				// and the header without a size to show.
				if a, e := strconv.Atoi(fields[0]); e == nil {
					p.Added = a
				}
				if d, e := strconv.Atoi(fields[1]); e == nil {
					p.Deleted = d
				}
			}
		}
	}
	return p
}

// Changeset is the changeset under review.
func (s *Session) Changeset() changeset.Changeset { return s.cs }

// Repo is the repository under review.
func (s *Session) Repo() *git.Repo { return s.repo }

// Summary is the derived lifecycle state, needed to submit from the TUI.
func (s *Session) Summary() lifecycle.Summary { return s.summary }

// ReviewRef is where this changeset's review history is anchored.
func (s *Session) ReviewRef() string { return s.reviewRef }

// UnreviewedCount is how many files still need attention.
func (s *Session) UnreviewedCount() int {
	reviewed, total := s.Count()
	return total - reviewed
}

// diffKeys hashes each changed file's diff section, so a review mark can be
// invalidated when that file's diff changes. One `git diff` call covers every
// file, and the sections are split on the same `diff --git` boundary git emits.
func (s *Session) diffKeys(ctx context.Context, sp span.Span) (map[string]string, error) {
	out, err := s.repo.Git(ctx, "diff", "--no-color", "--no-ext-diff", "--no-textconv", sp.From, sp.To)
	if err != nil {
		return nil, err
	}
	keys := map[string]string{}
	var section []string
	var path string
	flush := func() {
		if path == "" {
			return
		}
		sum := sha256.Sum256([]byte(strings.Join(section, "\n")))
		keys[path] = hex.EncodeToString(sum[:8])
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			section, path = nil, pathFromDiffGit(line)
			continue
		}
		if len(section) == 0 && strings.HasPrefix(line, "+++ ") {
			// Prefer the post-image name; it is unambiguous for renames.
			p := strings.TrimPrefix(line, "+++ ")
			if strings.HasPrefix(p, "b/") {
				p = p[2:]
			}
			if p != "/dev/null" {
				path = p
			}
			continue
		}
		section = append(section, line)
	}
	flush()
	return keys, nil
}

// pathFromDiffGit extracts the b-side path from a `diff --git a/x b/x` header.
func pathFromDiffGit(line string) string {
	rest := strings.TrimPrefix(line, "diff --git ")
	i := strings.LastIndex(rest, " b/")
	if i < 0 {
		return ""
	}
	return rest[i+3:]
}

// store resolves the mark store for this changeset, remembering a failure so a
// repository without a usable git directory does not pay for it on every toggle.
func (s *Session) store(ctx context.Context) (*reviewmark.Store, error) {
	if s.marks != nil || s.markErr != nil {
		return s.marks, s.markErr
	}
	gitDir, err := s.repo.GitDir(ctx)
	if err == nil {
		s.marks, s.markErr = reviewmark.New(gitDir, s.cs.Slug)
	} else {
		s.markErr = err
	}
	return s.marks, s.markErr
}

// loadMarks restores marks recorded for the commit under review. Failing to find or read
// them is not a reason to refuse to open a review, so this reports nothing.
func (s *Session) loadMarks(ctx context.Context) {
	store, err := s.store(ctx)
	if err != nil {
		return
	}
	set, err := store.Load(s.current.To)
	if err != nil || len(set) == 0 {
		return
	}
	s.persisted = set
	for i, f := range s.files {
		if !s.files[i].Reviewed && set[f.Path] == f.Key {
			s.files[i].Reviewed = true
			s.resumed++
		}
	}
}

// SaveMarks writes the current marks against the commit under review. An empty set is
// written too: clearing every mark has to overwrite the stored set, or the marks would
// reappear next session.
func (s *Session) SaveMarks(ctx context.Context) error {
	store, err := s.store(ctx)
	if err != nil {
		return err
	}
	set := reviewmark.Set{}
	for _, f := range s.files {
		if f.Reviewed {
			set[f.Path] = f.Key
		}
	}
	return store.Save(s.current.To, set)
}

// Resumed is how many marks came from an earlier session.
func (s *Session) Resumed() int { return s.resumed }
