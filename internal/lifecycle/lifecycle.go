// Package lifecycle derives changeset state from git history.
//
// There is no mutable state file. Lifecycle markers are ordinary commits
// carrying Review-* trailers, so "an implementation commit invalidates the
// previous marker" is just commit ordering.
package lifecycle

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"gitpair/internal/git"
	"gitpair/internal/model"
)

// Kind classifies a commit's role in the review lifecycle.
type Kind int

const (
	// KindImplementation is an ordinary commit: no recognised git-pair marker.
	KindImplementation Kind = iota
	KindReady
	KindReview
	// KindUnready is `change unready`: a marker that ends readiness on purpose
	// instead of leaving it to be inferred from a later commit.
	KindUnready
	// KindAbandoned is `change abandon`: the terminal record. It is a marker, so it
	// holds the newest-marker rule the same way the others do, but it names no state —
	// see Summary.Abandoned for why.
	KindAbandoned
)

func (k Kind) String() string {
	switch k {
	case KindReady:
		return "ready"
	case KindReview:
		return "review"
	case KindUnready:
		return "unready"
	case KindAbandoned:
		return "abandoned"
	}
	return "implementation"
}

// Event is one commit in a changeset's history.
type Event struct {
	SHA     string
	Short   string
	When    time.Time
	Subject string
	Kind    Kind
	Outcome model.Outcome
	// Author is the commit author name, useful when explaining a marker.
	Author string
	// ReviewedHead is the commit a review submission spoke about, from its
	// `Review-Head` trailer. It is the value `check` tests ancestry against, because it is
	// the one a rebase changes while the message survives: the rewritten marker still
	// names a head that is no longer in this line of history (PRD §11.3, §12).
	//
	// Empty means the marker names nothing — written before the trailer existed, or by a
	// hand-edited commit. That is reported rather than guessed at: the review commit's own
	// first parent would be the same value before a rebase and a *rewritten* parent after
	// one, so deriving it would make the rule pass in exactly the case it exists for.
	ReviewedHead string
	// UnrecognisedMarker is true when the commit carries Review-* trailers but
	// not a complete, valid marker for this changeset. Such a commit is
	// treated as an implementation commit — the conservative reading, since it
	// invalidates any approval rather than silently honouring a broken marker.
	UnrecognisedMarker bool
}

// Marker is true when the commit establishes a lifecycle state.
func (e Event) Marker() bool { return e.Kind != KindImplementation }

// Summary is the derived view of a changeset's history.
type Summary struct {
	Events  []Event // chronological
	Reviews []Event // chronological review submissions only
	// Marker is the newest lifecycle event, or nil if the branch has none.
	Marker *Event
	// LatestReview is the newest review submission, or nil.
	LatestReview *Event
	// Abandoned is the newest `change abandon` marker in the range, or nil. It is a
	// fact reported beside the state rather than a state value: an abandoned changeset
	// reports WORKING, and anything that advises a next step or decides whether an
	// operation may record a marker has to look here (PRD §9.7).
	Abandoned *Event
	State     model.State
	// Reason explains State in one line, for humans and `status --json`.
	Reason string
	// Stale is true when commits other than the newest marker follow it. Most callers
	// can ignore it: state is whatever the newest marker says, and a commit does not
	// change it (PRD §12). ReconcileStaleness is the one place the observation
	// becomes a verdict, and only archiving asks for it.
	Stale bool
	// Trailing counts the non-marker commits after the newest marker.
	Trailing int
	// Drifted lists the paths outside changesets/<slug>/ that differ between the newest
	// marker and headRef. Only ReconcileStaleness fills it in, so it is empty for every
	// caller that asks the marker question and not the tree question.
	Drifted []string
	// TrailingUnrecognised counts the trailing commits that carry Review-*
	// trailers git-pair could not interpret. They always invalidate a marker:
	// a newer git-pair may read them fine, and guessing from the tree would be a
	// guess about someone else's protocol.
	TrailingUnrecognised int
	// Unrecognised lists markers git-pair could not interpret.
	Unrecognised []Event
}

// ReviewIndex returns the chronological index of review i, accepting negative
// indexes (-1 is the most recent review). ok is false when out of range.
func (s Summary) ReviewIndex(i int) (Event, bool) {
	if i < 0 {
		i += len(s.Reviews)
	}
	if i < 0 || i >= len(s.Reviews) {
		return Event{}, false
	}
	return s.Reviews[i], true
}

// RangeForHead renders the `git log` range covering a changeset's own history,
// measured against a specific head ref. headRef is "HEAD" for the checked-out
// branch and a branch name when inspecting another changeset.
func RangeForHead(base, headRef string) string { return base + ".." + headRef }

// Summarize walks a changeset's history and derives its effective state.
func Summarize(ctx context.Context, repo *git.Repo, slug, base, headRef string) (Summary, error) {
	if headRef == "" {
		headRef = "HEAD"
	}
	if base == "" {
		return Summary{}, fmt.Errorf("changeset %s has no base configured", slug)
	}
	if _, err := repo.RevParse(ctx, base); err != nil {
		return Summary{}, fmt.Errorf("cannot resolve changeset base %q: %w", base, err)
	}
	if _, err := repo.RevParse(ctx, headRef); err != nil {
		return Summary{}, fmt.Errorf("cannot resolve %q: %w", headRef, err)
	}
	fields := []string{"%H", "%h", "%ct", "%an", "%s", "%(trailers:only,unfold)"}
	records, err := repo.LogFields(ctx, RangeForHead(base, headRef), fields...)
	if err != nil {
		return Summary{}, err
	}
	events := make([]Event, 0, len(records))
	for _, rec := range records {
		if len(rec) < len(fields) {
			continue
		}
		events = append(events, parseEvent(slug, rec))
	}
	// derive reads markers, and markers are written by commands. Whether the commits
	// after a marker matter is a question about the tree, asked only by the one
	// caller that needs it; see SummarizeAgainstTree.
	return derive(events), nil
}

// SummarizeHEAD derives state for the checked-out branch.
func SummarizeHEAD(ctx context.Context, repo *git.Repo, slug, base string) (Summary, error) {
	return Summarize(ctx, repo, slug, base, "HEAD")
}

// SummarizeAgainstTree derives state, then asks whether the content the newest
// marker spoke about is still at headRef. A marker whose code has been changed
// underneath it reads as WORKING here and nowhere else.
//
// `check` is the caller: the gate decides that a head is safe to hand on, and it needs the
// derivation and the drift in one reading rather than two that could disagree (PRD §9.5, §11.3).
// Everywhere else state moves when a git-pair command records a marker, not when the author
// commits (PRD §12).
func SummarizeAgainstTree(ctx context.Context, repo *git.Repo, slug, base, headRef string) (Summary, error) {
	s, err := Summarize(ctx, repo, slug, base, headRef)
	if err != nil {
		return s, err
	}
	return ReconcileStaleness(ctx, repo, slug, headRef, s)
}

// SummarizeAgainstTreeHEAD is SummarizeAgainstTree for the checked-out branch.
func SummarizeAgainstTreeHEAD(ctx context.Context, repo *git.Repo, slug, base string) (Summary, error) {
	return SummarizeAgainstTree(ctx, repo, slug, base, "HEAD")
}

// MarkerAt reads the Review-* trailers one commit carries.
//
// The range summaries answer "what happened between base and head". This answers the narrower
// question a caller asks when it holds one commit and wants to know what that commit records —
// the commit `integration record --source` names, or the landing whose record must not be written
// for work that was abandoned. It uses the same
// trailer parsing as the range walk, so the two cannot disagree about what a marker says, and it
// needs no base: a caller holding a SHA from a ref should not have to resolve a branch that may
// never have been fetched.
func MarkerAt(ctx context.Context, repo *git.Repo, rev string) (map[string]string, error) {
	block, err := repo.Git(ctx, "log", "-1", "--format=%(trailers:only,unfold)", rev)
	if err != nil {
		return nil, err
	}
	return parseTrailers(block), nil
}

func parseEvent(slug string, rec []string) Event {
	e := Event{SHA: rec[0], Short: rec[1], Subject: rec[4], Author: rec[3]}
	if secs, err := parseInt(rec[2]); err == nil {
		e.When = time.Unix(secs, 0).UTC()
	}
	trailers := parseTrailers(rec[5])
	outcome, hasOutcome := trailers[model.TrailerOutcome]
	state, hasState := trailers[model.TrailerState]
	changeset := trailers[model.TrailerChangeset]

	switch {
	case hasOutcome:
		if o, ok := model.ParseOutcome(outcome); ok && changeset == slug {
			e.Kind, e.Outcome = KindReview, o
			e.ReviewedHead = reviewedHead(trailers[model.TrailerHead])
		} else {
			e.UnrecognisedMarker = true
		}
	case hasState:
		switch {
		case changeset != slug:
			e.UnrecognisedMarker = true
		case state == model.StateValueReady:
			e.Kind = KindReady
		case state == model.StateValueWorking:
			e.Kind = KindUnready
		case state == model.StateValueAbandoned:
			e.Kind = KindAbandoned
		default:
			// `Review-State: closed` was read here until ending a changeset became a
			// ref move instead of a commit. It now falls through to the
			// unrecognised path, which is the conservative reading and keeps the
			// retired vocabulary out of the model.
			e.UnrecognisedMarker = true
		}
	}
	return e
}

// reviewedHead accepts the value of a `Review-Head` trailer: a hex object id of plausible
// length, abbreviated or full. Anything else — a branch name, a typo, an empty value — is read
// as no head named, which `check` reports rather than resolving and hoping for the best.
func reviewedHead(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) < 7 || len(raw) > 40 {
		return ""
	}
	for _, r := range raw {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return ""
		}
	}
	return raw
}

func derive(events []Event) Summary {
	s := Summary{Events: events, State: model.StateWorking}
	for i, e := range events {
		if e.Kind == KindReview {
			s.Reviews = append(s.Reviews, e)
		}
		if e.Kind == KindAbandoned {
			// The terminal record, newest wins. It is reported beside the state rather
			// than as one, so `state` keeps its five values.
			s.Abandoned = &s.Events[i]
		}
		if e.UnrecognisedMarker {
			s.Unrecognised = append(s.Unrecognised, e)
		}
	}
	if len(s.Reviews) > 0 {
		s.LatestReview = &s.Reviews[len(s.Reviews)-1]
	}

	last := -1
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Marker() {
			last = i
			break
		}
	}
	if last == -1 {
		if len(events) == 0 {
			s.Reason = "no commits above the base yet"
		} else {
			s.Reason = "no git-pair lifecycle markers on this branch"
		}
		return s
	}
	marker := events[last]
	s.Marker = &events[last]

	var trailing, unreadable int
	for _, e := range events[last+1:] {
		if e.Marker() {
			continue
		}
		trailing++
		if e.UnrecognisedMarker {
			unreadable++
		}
	}
	s.Trailing = trailing
	s.TrailingUnrecognised = unreadable
	if trailing > 0 {
		// Commits after the newest marker are an observation, not a verdict. State
		// moves when a git-pair command records a marker, so readiness survives an
		// author who keeps working and ends at `change unready` (PRD §12). What the
		// count is for is that one caller which does ask about the tree, and the
		// reason, which tells a reviewer the branch has moved since it was offered.
		s.Stale = true
		s.State = marker.State()
		s.Reason = fmt.Sprintf("%s (%d commit%s since)",
			markerReason(marker), trailing, plural(trailing))
		return s
	}

	s.State = marker.State()
	s.Reason = markerReason(marker)
	return s
}

// markerLabel names a marker for the middle of a sentence, e.g. "ready 952d74e"
// or "review 952d74e (BLOCKED)".
func markerLabel(m Event) string {
	switch m.Kind {
	case KindReady:
		return "ready " + m.Short
	case KindReview:
		return fmt.Sprintf("review %s (%s)", m.Short, m.Outcome)
	case KindUnready:
		return "unready " + m.Short
	case KindAbandoned:
		return "abandoned " + m.Short
	}
	return m.Short
}

// markerReason explains an up to date marker in one line.
func markerReason(m Event) string {
	switch m.Kind {
	case KindReady:
		return "marked ready by " + m.Short
	case KindReview:
		return fmt.Sprintf("review %s (%s) is the newest commit", m.Short, m.Outcome)
	case KindUnready:
		return "marked unready by " + m.Short
	case KindAbandoned:
		return "abandoned by " + m.Short
	}
	return m.Subject
}

// ReconcileStaleness answers the question derive cannot: has the content the newest
// marker spoke about been changed underneath it? Where it has, the marker no longer
// describes HEAD and the state is WORKING.
//
// Only SummarizeAgainstTree calls it, and only `check` uses that. The gate is the one place
// git-pair asks whether reviewed content is still there, because a merge is about to act on the
// answer (PRD §9.5). Everywhere else a commit is not something that changes state: `change ready`,
// `change unready` and a review submission are (PRD §12).
//
// The verdict comes from the tree rather than from the commit count, because committing
// a fix to ABOUT.md or a review thread is not an implementation change and the reviewer
// reads those files from HEAD anyway: anything outside changesets/<slug>/ differing
// between the marker's parent and HEAD means the reviewed code is gone.
//
// Comparing trees rather than counting commits also folds in the cases counting
// gets wrong: a merge of the base, a rebase that rewrote every SHA, and a change
// followed by its own revert all end at "is the approved code still here".
//
// That is the content question and not the lineage one. A tree-identical rebase answers this
// check "still here" and must still refuse, because the approval was about a commit, not about a
// tree: `check`'s ancestry condition on `Review-Head` is what catches it (PRD §12).
func ReconcileStaleness(ctx context.Context, repo *git.Repo, slug, headRef string, s Summary) (Summary, error) {
	if !s.Stale || s.Marker == nil {
		return s, nil
	}
	if headRef == "" {
		headRef = "HEAD"
	}
	marker := *s.Marker

	// The marker's own tree is the content that was reviewed. For the empty markers
	// git-pair writes this is indistinguishable from its parent, and for a review
	// submission that carried the reviewer's edits it is the only right answer: the
	// parent is the state before the reviewer spoke, so comparing from there would
	// count their own files as drift the author has to explain.
	from := marker.SHA

	if s.TrailingUnrecognised > 0 {
		// The tree cannot speak for a trailer set git-pair cannot read.
		s.State = model.StateWorking
		s.Reason = fmt.Sprintf("%d unrecognised review marker(s) after %s",
			s.TrailingUnrecognised, markerLabel(marker))
		return s, nil
	}

	changed, err := repo.PathsChangedOutside(ctx, from, headRef, filepath.Join("changesets", slug))
	if err != nil {
		return s, err
	}
	if len(changed) > 0 {
		s.State = model.StateWorking
		s.Drifted = changed
		s.Reason = fmt.Sprintf("code changed since %s", markerLabel(marker))
		return s, nil
	}

	// Nothing outside the changeset directory moved: the approved state is still
	// what is at HEAD, so the marker stands and the commits get named in the
	// reason rather than counted against it.
	s.Stale = false
	s.Reason = fmt.Sprintf("%s (%d changeset-only commit%s since)",
		markerReason(marker), s.Trailing, plural(s.Trailing))
	return s, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// State maps a marker onto the state it establishes.
func (e Event) State() model.State {
	switch e.Kind {
	case KindReady:
		return model.StateReady
	case KindReview:
		return e.Outcome.State()
	case KindUnready:
		// Retiring readiness lands on the state a changeset with no marker would
		// derive anyway. Naming it here keeps the answer from depending on the
		// fallback below, which is the answer for an implementation commit.
		return model.StateWorking
	case KindAbandoned:
		// The terminal record names no state of its own. WORKING is what it falls back
		// to, and it is not a lie: the changeset is not in review and nothing is owed
		// on it. What makes it recognisable is Summary.Abandoned, which every surface
		// that advises a next step has to consult (PRD §9.7).
		return model.StateWorking
	}
	return model.StateWorking
}

// parseTrailers reads `Key: value` lines from a git trailer block. The first
// value wins, so a duplicated key cannot be smuggled past a check.
func parseTrailers(block string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(block, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if !strings.HasPrefix(key, "Review-") {
			continue
		}
		if _, seen := out[key]; seen {
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
	return out
}

func parseInt(s string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n)
	return n, err
}

// Age renders a commit time as a compact duration for tables and JSON.
func Age(t time.Time, now time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
