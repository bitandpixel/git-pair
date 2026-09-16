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
	"strings"

	"gitpr/internal/changeset"
	"gitpr/internal/git"
	"gitpr/internal/lifecycle"
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
	if s.spanOpts.Unreviewed {
		s.spanOpts = span.Options{}
	} else {
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
