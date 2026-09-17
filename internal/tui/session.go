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

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/lifecycle"
	"gitpair/internal/reviewmark"
	"gitpair/internal/span"
)

// Options configures a review session.
type Options struct {
	Repo      *git.Repo
	Changeset changeset.Changeset
	Summary   lifecycle.Summary
	// Span is the starting span. Use span.Full() or span.SinceReview(n): the zero
	// Selector names the changeset base as both ends, which is not a span.
	Span span.Selector
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
	sel       span.Selector
	current   span.Span
	files     []File
	reviewRef string

	// marks and markErr hold the resolved store, so a repository without a usable git directory
	// does not pay for it on every toggle.
	marks   *reviewmark.Store
	markErr error
	// Marks are persisted outside the working tree so a review can be resumed; see
	// internal/reviewmark for where and why. persisted is the set recorded against the commit
	// the *current* span ends at, re-read on every scan: `v` can bring the session back to a
	// span whose marks it made an hour ago, and the list it is rebuilding is the only place
	// those marks live.
	persisted reviewmark.Set
	// marksRead is how many of the marks in the list just built came off disk, and resumed is
	// that number as the session opened, which is what the opening status line reports.
	marksRead int
	resumed   int

	// ring holds every span this session has been in, in the order they were first
	// entered, and ringIdx is the stop it is standing on. `v` walks it; see StepSpan.
	ring    []span.Selector
	ringIdx int

	// drift holds the ref checkpoints that have moved since this span pinned them, as of the
	// last check. It is written on the update path only: the banner reads it, and a check
	// running off the event loop hands its answer back as a message rather than writing here.
	drift []span.Drift
}

// NewSession resolves the span and scans the changed files.
func NewSession(ctx context.Context, opts Options) (*Session, error) {
	s := &Session{
		repo: opts.Repo, cs: opts.Changeset, summary: opts.Summary, sel: opts.Span,
		reviewRef: "refs/reviews/" + opts.Changeset.Slug,
	}
	if err := s.Rescan(ctx); err != nil {
		return nil, err
	}
	s.resumed = s.marksRead
	s.seedRing()
	return s, nil
}

// seedRing puts the two common spans on the ring beside the one the session opened on, so
// the first press of `v` still reaches the unreviewed span and back (PRD §17.2). Custom
// spans join later; the ring grows rather than replacing that behaviour.
func (s *Session) seedRing() {
	s.ringIdx = s.noteSpan(s.sel)
	if s.CanToggleSpan() {
		s.noteSpan(span.Full())
		s.noteSpan(span.SinceReview(-1))
	}
}

// Rescan recomputes the span and file list, preserving review marks whose file
// content in the span has not changed.
func (s *Session) Rescan(ctx context.Context) error {
	sp, err := span.Resolve(ctx, s.repo, s.cs.Base, s.summary, s.sel)
	if err != nil {
		return err
	}
	if err := s.scan(ctx, sp); err != nil {
		return err
	}
	s.sel = pinSelector(s.sel, sp)
	s.noteSpan(s.sel)
	s.setDrift(s.CheckDrift(ctx))
	return nil
}

// SetSpan moves the session to a span the reviewer chose, which is what `V`'s Enter does.
// Resolution happens before anything is replaced: a span git refuses leaves the session
// showing what it showed before, rather than caught half-way between two.
func (s *Session) SetSpan(ctx context.Context, sel span.Selector) error {
	sp, err := span.Resolve(ctx, s.repo, s.cs.Base, s.summary, sel)
	if err != nil {
		return err
	}
	s.sel = pinSelector(sel, sp)
	if err := s.scan(ctx, sp); err != nil {
		return err
	}
	// The endpoints were pinned to where the refs point just now, so nothing about this span
	// has drifted yet.
	s.setDrift(nil)
	s.ringIdx = s.noteSpan(s.sel)
	return nil
}

// pinSelector writes the pins from a resolved span back into the selector that produced it.
// Without this the next rescan resolves the ref again and follows it, which is the quiet
// version of what requirements §13 exists to prevent: the ground would move under a review
// that is part way through. The checkpoint keeps its ref identity and only gains an OID, and
// drift is what compares the two.
func pinSelector(sel span.Selector, sp span.Span) span.Selector {
	if sel.Base.Kind == span.KindRef {
		sel.Base.OID = sp.Base.OID
	}
	if sel.Head.Kind == span.KindRef {
		sel.Head.OID = sp.Head.OID
	}
	return sel
}

// Selector is the span as it was chosen rather than as it resolved, which is what the
// picker seeds its pending endpoints from.
func (s *Session) Selector() span.Selector { return s.sel }

// scan rebuilds the file list for an already-resolved span.
func (s *Session) scan(ctx context.Context, sp span.Span) error {
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
	s.persisted = s.marksFor(ctx, sp.To)

	files := make([]File, 0, len(names))
	read := 0
	for _, name := range names {
		key := keys[name]
		marked := false
		if old, ok := previous[name]; ok && old.Key == key {
			marked = old.Reviewed
		}
		if !marked && s.persisted.Has(name, key) {
			// Same file, same diff content as when it was marked — in an earlier session, or
			// in this one before `v` took the reviewer elsewhere. Anything else — new commit,
			// rebase, different span — keys differently and so stays unreviewed.
			marked, read = true, read+1
		}
		files = append(files, File{Path: name, Key: key, Reviewed: marked})
	}
	s.files = files
	s.marksRead = read
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
	if err := s.Rescan(ctx); err != nil {
		return err
	}
	// The presets can become available while an external tool is open — the first review
	// submission does that — and the ring has to know about them by the time `v` is next
	// pressed. Seeding is idempotent, so this only adds stops that were not there.
	s.seedRing()
	return nil
}

// StepSpan moves to the next span this session has been in, wrapping: the span it opened on, the two
// presets, and any span the reviewer chose with `V`. Walking the ring is what makes a custom span
// reachable again without opening the picker, and the presets stay reachable from a custom span rather
// than being replaced by it.
//
// It is a walk out of every stop, including a read-only one, and that is the whole contract: the same
// press means "next", so a reviewer can learn where a keypress lands. Two rules lived here before and
// were removed. Escaping unconditionally out of a historical span closed a loop that made the *second*
// historical stop unreachable ("stuck cycling"); making that escape conditional on a refusal made the
// next `v` land somewhere different depending on what happened a few keystrokes ago, which is the same
// unpredictability with extra steps. Reaching a span you can review is `V`'s job -- its head column
// always offers `Current` -- and the walk reaches every live stop anyway.
//
// A stop that no longer resolves -- the tag it named was deleted, the review ref is gone, the branch
// was force-pushed away -- is passed over, and reported in StepResult.Skipped. Blocking there would
// make one dead stop a wall: the same press would fail the same way forever, with `V` the only way
// past. Skipping is safe because SetSpan resolves before it replaces anything, so a stop that fails
// leaves the session exactly where it was and the next candidate is a whole span, never half of one.
// The stop stays on the ring: a tag that comes back is a stop again.
func (s *Session) StepSpan(ctx context.Context) (StepResult, error) {
	total := len(s.ring)
	res := StepResult{Pos: s.ringIdx + 1, Total: total}
	if total < 2 {
		return res, fmt.Errorf("this session has only been in this span so far \u2014 V chooses another")
	}

	var why error
	for i := 0; i < total; i++ {
		target := (s.ringIdx + 1 + i) % total
		if target == s.ringIdx {
			continue // where the session is standing is not somewhere to step to
		}
		err := s.SetSpan(ctx, s.ring[target])
		if err == nil {
			res.Pos = s.ringIdx + 1
			return res, nil
		}
		if why == nil {
			why = err
		}
		res.Skipped = append(res.Skipped, SkippedStop{Selector: s.ring[target], Reason: err.Error()})
	}
	if why == nil {
		why = fmt.Errorf("no other span to step to")
	}
	return res, why
}

// StepResult is where a step landed.
type StepResult struct {
	// Pos and Total are the stop it landed on and how many there are, 1-based, which is how the
	// status line says it: "3 of 4".
	Pos, Total int
	// Skipped are the stops the step passed over because they no longer resolve, in the order it met
	// them. Reporting them is the whole deal: silently skipping would look like a shorter ring, and a
	// reviewer would go looking for the span that is really still there.
	Skipped []SkippedStop
}

// SkippedStop is a span the step could not enter, with git's reason.
type SkippedStop struct {
	Selector span.Selector
	Reason   string
}

// SpanPosition is the stop the session stands on, 1-based, and how many stops there are.
func (s *Session) SpanPosition() (pos, total int) { return s.ringIdx + 1, len(s.ring) }

// SpanRing is the spans on the ring in the order `v` walks them.
func (s *Session) SpanRing() []span.Selector { return s.ring }

// noteSpan records a chosen span as a stop on the ring and says where it sits. It does not
// move the session's position: a rescan after an external tool closed is not a visit, and
// counting it as one would fill the ring with near-duplicates.
func (s *Session) noteSpan(sel span.Selector) int {
	key := sel.Key()
	for i, existing := range s.ring {
		if existing.Key() == key {
			return i
		}
	}
	s.ring = append(s.ring, sel)
	return len(s.ring) - 1
}

// --- drift ------------------------------------------------------------------

// Drifted is the named-ref endpoints that have moved since this span pinned them, as of the
// last check. It is empty for a span without a ref endpoint, and it is a report rather than
// a change: the span stays on the commit the reviewer chose (requirements §13).
func (s *Session) Drifted() []span.Drift { return s.drift }

// CheckDrift asks git where the span's named-ref endpoints point now and returns the ones
// that have moved. It changes nothing and remembers nothing: a session asks when it can
// afford to, and stores the answer on the update path so the render path never reads what a
// goroutine wrote. A span with no ref endpoint asks git nothing.
//
// A ref that no longer resolves is not drift — there is nothing to refresh to, and the pin is
// still the commit the reviewer chose — so the answer is "nothing has moved" rather than an
// alarm about a span that is still exactly what was asked for.
func (s *Session) CheckDrift(ctx context.Context) []span.Drift {
	if !s.current.TracksRefs() {
		return nil
	}
	moved, err := s.current.Drift(ctx, s.repo)
	if err != nil {
		return nil
	}
	return moved
}

// setDrift stores the answer from a check.
func (s *Session) setDrift(moved []span.Drift) { s.drift = moved }

// RefreshDrift moves every drifted ref endpoint to where its ref points now and recomputes
// the span. It is `r`, and it is the only way an endpoint moves without the reviewer choosing
// a different span (requirements §14). It reports what moved and how many reviewed marks
// stopped applying: a mark is keyed on the commit the span ends at and on each file's diff
// within it, so a span that now compares different commits simply matches nothing it matched
// before — which is the honest outcome, and why the count is reported rather than hidden.
//
// The stop on the span ring is rewritten in place. The reviewer chose `main`, not
// `main at abc123`, and `main` is what they should step back onto.
func (s *Session) RefreshDrift(ctx context.Context) (moved []span.Drift, reset int, err error) {
	if len(s.drift) == 0 {
		return nil, 0, fmt.Errorf("nothing has moved since this span was chosen")
	}
	next := s.current
	for _, d := range s.drift {
		if next, err = next.RefreshRef(ctx, s.repo, d.Name); err != nil {
			return nil, 0, err
		}
	}
	before, _ := s.Count()
	sel := pinSelector(s.sel, next)
	s.sel = sel
	if err := s.scan(ctx, next); err != nil {
		return nil, 0, err
	}
	if s.ringIdx < len(s.ring) {
		s.ring[s.ringIdx] = sel
	}
	moved = s.drift
	s.setDrift(nil)
	after, _ := s.Count()
	if before > after {
		reset = before - after
	}
	return moved, reset, nil
}

// CanToggleSpan reports whether an unreviewed span is available.
func (s *Session) CanToggleSpan() bool { return len(s.summary.Reviews) > 0 }

// Unreviewed reports whether the session is showing what is actually unreviewed: everything
// after the newest submission, against the working tree. A review further back as the base is a
// different span and keeps the label it resolved to, because "unreviewed" over a span that
// starts three submissions back is a claim the header has no way to take back. The predicate
// is the newest review rather than "some review": after a submission, the span that named the
// previous newest one by index stops being the unreviewed span, and says so.
func (s *Session) Unreviewed() bool {
	latest := len(s.summary.Reviews) - 1
	if latest < 0 {
		return false
	}
	return s.current.Head.Kind == span.KindWorkingTree &&
		s.current.Base.Kind == span.KindReview &&
		s.current.Base.ResolvedIndex == latest
}

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
	return s.diff(ctx, s.current.From, s.current.To, path)
}

// WorkingPatch is the reviewer's own uncommitted edits to a file, measured from the revision
// under review rather than from the span's start. The order is the whole point: whatever sits
// between the span's ends is the author's work, so whatever sits after its end is the reviewer's.
//
// Those bytes look like any other diff, which is why the pane prints them under a caption naming
// who they belong to, and why the reviewed counter keeps counting the span alone. It is also the
// same working tree the difftool opens, so an edit made there shows up here.
//
// An empty patch is the ordinary case: most files carry no reviewer edits.
func (s *Session) WorkingPatch(ctx context.Context, path string) Patch {
	return s.diff(ctx, s.current.To, "", path)
}

// diff asks git for one path's two-way diff, in colour. With one revision the other side is the
// working tree.
func (s *Session) diff(ctx context.Context, from, to, path string) Patch {
	revs := []string{from}
	if to != "" {
		revs = append(revs, to)
	}
	show := append([]string{"-c", "core.quotePath=false", "diff",
		"--no-ext-diff", "--no-textconv", "--color=always"}, revs...)
	out, err := s.repo.Git(ctx, append(show, "--", path)...)
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
	numstat := append([]string{"diff", "--no-ext-diff", "--numstat"}, revs...)
	num, err := s.repo.Git(ctx, append(numstat, "--", path)...)
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

// marksFor reads the marks recorded against a commit. Failing to find or read them is not a
// reason to refuse to open a review, so this reports nothing.
func (s *Session) marksFor(ctx context.Context, commit string) reviewmark.Set {
	store, err := s.store(ctx)
	if err != nil {
		return nil
	}
	set, err := store.Load(commit)
	if err != nil || len(set) == 0 {
		return nil
	}
	return set
}

// SaveMarks writes the reviewer's answers for this span against the commit under review. Every
// file in the span is answered, not only the marked ones: clearing a mark has to overwrite what
// was stored, or the mark would reappear next session.
func (s *Session) SaveMarks(ctx context.Context) error {
	store, err := s.store(ctx)
	if err != nil {
		return err
	}
	answers := make([]reviewmark.Answer, 0, len(s.files))
	for _, f := range s.files {
		answers = append(answers, reviewmark.Answer{Path: f.Path, Key: f.Key, Reviewed: f.Reviewed})
	}
	return store.Save(s.current.To, answers)
}

// Resumed is how many marks came from an earlier session.
func (s *Session) Resumed() int { return s.resumed }
