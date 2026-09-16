// Package lifecycle derives changeset state from git history.
//
// There is no mutable state file. Lifecycle markers are ordinary commits
// carrying GitPR-* trailers, so "an implementation commit invalidates the
// previous marker" is just commit ordering.
package lifecycle

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gitpr/internal/git"
	"gitpr/internal/model"
)

// Kind classifies a commit's role in the review lifecycle.
type Kind int

const (
	// KindImplementation is an ordinary commit: no recognised gitpr marker.
	KindImplementation Kind = iota
	KindReady
	KindReview
	KindClosed
)

func (k Kind) String() string {
	switch k {
	case KindReady:
		return "ready"
	case KindReview:
		return "review"
	case KindClosed:
		return "closed"
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
	// UnrecognisedMarker is true when the commit carries GitPR-* trailers but
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
	State        model.State
	// Reason explains State in one line, for humans and `status --json`.
	Reason string
	// Stale is true when implementation commits follow the newest marker.
	Stale bool
	// Unrecognised lists markers gitpr could not interpret.
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
	return derive(events), nil
}

// SummarizeHEAD derives state for the checked-out branch.
func SummarizeHEAD(ctx context.Context, repo *git.Repo, slug, base string) (Summary, error) {
	return Summarize(ctx, repo, slug, base, "HEAD")
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
		} else {
			e.UnrecognisedMarker = true
		}
	case hasState:
		switch {
		case changeset != slug:
			e.UnrecognisedMarker = true
		case state == model.StateValueReady:
			e.Kind = KindReady
		case state == model.StateValueClosed:
			e.Kind = KindClosed
		default:
			e.UnrecognisedMarker = true
		}
	}
	return e
}

func derive(events []Event) Summary {
	s := Summary{Events: events, State: model.StateWorking}
	for _, e := range events {
		if e.Kind == KindReview {
			s.Reviews = append(s.Reviews, e)
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
			s.Reason = "no gitpr lifecycle markers on this branch"
		}
		return s
	}
	marker := events[last]
	s.Marker = &events[last]

	var trailing int
	for _, e := range events[last+1:] {
		if !e.Marker() {
			trailing++
		}
	}
	if trailing > 0 {
		s.Stale = true
		s.State = model.StateWorking
		s.Reason = fmt.Sprintf("%d implementation commit(s) after %s %s",
			trailing, marker.Kind, marker.Short)
		return s
	}

	s.State = marker.State()
	switch marker.Kind {
	case KindReady:
		s.Reason = "marked ready by " + marker.Short
	case KindClosed:
		s.Reason = "closed by " + marker.Short
	case KindReview:
		s.Reason = fmt.Sprintf("review %s (%s) is the newest commit", marker.Short, marker.Outcome)
	}
	return s
}

// State maps a marker onto the state it establishes.
func (e Event) State() model.State {
	switch e.Kind {
	case KindReady:
		return model.StateReady
	case KindClosed:
		return model.StateClosed
	case KindReview:
		return e.Outcome.State()
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
		if !strings.HasPrefix(key, "GitPR-") {
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
