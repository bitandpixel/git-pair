// Package span resolves "what should this review look at" into a pair of pinned
// commits, per PRD §17 and docs/plans/completed/review-span-selection/requirements.md.
//
// A span is two checkpoints, a base and a head. A checkpoint stays a *name* — the
// changeset base, the working tree, a review by index, a commit id, a ref — and
// resolving it is a separate step that pins it to a commit for the life of the
// session. Nothing outside this package asks git for a rev on its own behalf.
package span

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gitpair/internal/git"
	"gitpair/internal/lifecycle"
)

// CheckpointKind says what sort of thing an end of the span names.
type CheckpointKind int

const (
	// KindChangesetBase is the merge base of the changeset's base ref and the head.
	// It names a base, never a head.
	KindChangesetBase CheckpointKind = iota
	// KindWorkingTree is the working tree, which for a diff means HEAD as pinned
	// when the span was resolved. It names a head, never a base: the working tree
	// is where the reviewer's own edits live, and a base cannot contain them.
	KindWorkingTree
	// KindReview is a review submission, chosen by chronological index.
	KindReview
	// KindCommit is an immutable commit. It cannot drift.
	KindCommit
	// KindRef is a named ref kept for its identity, pinned to the commit it
	// pointed at when it was chosen.
	KindRef
)

func (k CheckpointKind) String() string {
	switch k {
	case KindChangesetBase:
		return "changeset base"
	case KindWorkingTree:
		return "working tree"
	case KindReview:
		return "review"
	case KindCommit:
		return "commit"
	case KindRef:
		return "ref"
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Checkpoint is one end of a span. It is meaningful before git has seen it —
// "review -1" is a thing a reviewer chose — and resolution is what pins it to a
// commit. The pinning matters: a ref that kept resolving to whatever it points at
// now would move the ground under a review that is already part way through.
type Checkpoint struct {
	Kind CheckpointKind
	// Index is the requested review index. Negative counts back from the newest
	// submission and is relative to the whole review history, so it does not move
	// when the other end of the span does.
	Index int
	// ResolvedIndex is Index once resolved, or -1 for every other kind.
	ResolvedIndex int
	// Name is the ref for KindRef, the id as typed for KindCommit, and a
	// display name for the rest.
	Name string
	// OID is the commit the checkpoint is pinned to. Empty until resolved.
	OID string
}

// ChangesetBase names the merge base of the changeset's base ref and the head.
func ChangesetBase() Checkpoint { return Checkpoint{Kind: KindChangesetBase, ResolvedIndex: -1} }

// WorkingTree names the reviewer's working tree, which for a diff means HEAD as
// pinned at resolution. A span ending here is a live review; any other head is a
// look at history.
func WorkingTree() Checkpoint { return Checkpoint{Kind: KindWorkingTree, ResolvedIndex: -1} }

// Review names a review submission by index, negative counted from the newest.
func Review(index int) Checkpoint {
	return Checkpoint{Kind: KindReview, Index: index, ResolvedIndex: -1}
}

// Commit names an immutable commit. It is never re-resolved, so it cannot drift.
func Commit(id string) Checkpoint {
	return Checkpoint{Kind: KindCommit, Name: id, ResolvedIndex: -1}
}

// Ref names a branch, tag, remote-tracking ref or any other ref git can resolve.
// The name survives resolution so drift can be detected later.
func Ref(name string) Checkpoint { return Checkpoint{Kind: KindRef, Name: name, ResolvedIndex: -1} }

// String renders the checkpoint the way a reviewer named it, plus the pin once
// there is one: "main @ abc1234".
func (c Checkpoint) String() string {
	switch c.Kind {
	case KindReview:
		return reviewName(c)
	case KindCommit:
		// Chosen from a list, the name is a full sha; typed, it is whatever the reviewer
		// wrote ("HEAD^", "v0.4.0"), which is worth more to them than a truncated hex id.
		if id := c.OID; id != "" {
			return short(id)
		}
		if isHex(c.Name) && len(c.Name) > 7 {
			return short(c.Name)
		}
		return c.Name
	case KindRef:
		if c.OID != "" {
			return ShortRef(c.Name) + " @ " + short(c.OID)
		}
		return ShortRef(c.Name)
	}
	return c.Kind.String()
}

// Selector is a span as a reviewer chooses it: both ends named, neither resolved.
type Selector struct {
	Base Checkpoint
	Head Checkpoint
}

// Full is the changeset as a whole: base to working tree. It is the default span.
func Full() Selector { return Selector{Base: ChangesetBase(), Head: WorkingTree()} }

// SinceReview is what landed after a review, which is what `v` and `--unreviewed`
// mean.
func SinceReview(index int) Selector { return Selector{Base: Review(index), Head: WorkingTree()} }

// Validate rejects the two selectors that cannot mean anything: a working tree as
// a base, and a changeset base as a head.
func (s Selector) Validate() error {
	switch {
	case s.Base.Kind == KindWorkingTree:
		return errors.New("the working tree cannot be the base of a span: it is where your own edits are")
	case s.Head.Kind == KindChangesetBase:
		return errors.New("the changeset base cannot be the head of a span: a head is a commit or the working tree")
	case s.Base.Kind == KindCommit && s.Base.Name == "",
		s.Head.Kind == KindCommit && s.Head.Name == "",
		s.Base.Kind == KindRef && s.Base.Name == "",
		s.Head.Kind == KindRef && s.Head.Name == "":
		return errors.New("a commit or ref checkpoint needs a name")
	}
	return nil
}

// ErrNoReviews is returned when a review-relative checkpoint is requested but the
// changeset has never been reviewed.
var ErrNoReviews = errors.New("changeset has no review submissions yet")

// Span is a resolved range: both ends pinned to commits, so callers can hand them
// to git as two separate revs and never rely on `...` semantics.
type Span struct {
	From string
	To   string
	// Base and Head are the checkpoints as resolved, pins included.
	Base, Head Checkpoint
	// ChangesetBase is the changeset's base ref, kept so labels and refreshes can
	// name the base the way the changeset does.
	ChangesetBase string
	// Label is the human-facing rendering: "main...current", "last review..current",
	// "review -2..review 0", "main@abc1234..8ab932f".
	Label string
}

// Live reports whether this span is a review the reviewer can act on. Only a
// working-tree head is: everything else is a commit, and a commit cannot be
// edited, marked, or submitted against.
func (s Span) Live() bool { return s.Head.Kind == KindWorkingTree }

// Historical is the read-only half of the mode matrix.
func (s Span) Historical() bool { return !s.Live() }

// CanEdit is editing product files and review documents.
func (s Span) CanEdit() bool { return s.Live() }

// CanMark is marking files reviewed, which is state about a span under review.
func (s Span) CanMark() bool { return s.Live() }

// CanSubmit is writing a review submission.
func (s Span) CanSubmit() bool { return s.Live() }

// Resolve turns a selector into pinned commits against the current HEAD.
func Resolve(ctx context.Context, repo *git.Repo, base string, summary lifecycle.Summary, sel Selector) (Span, error) {
	if err := sel.Validate(); err != nil {
		return Span{}, err
	}
	head, err := repo.Head(ctx)
	if err != nil {
		return Span{}, err
	}
	from, err := sel.Base.resolve(ctx, repo, base, summary, head)
	if err != nil {
		return Span{}, err
	}
	to, err := sel.Head.resolve(ctx, repo, base, summary, head)
	if err != nil {
		return Span{}, err
	}
	return Span{
		From: from.OID, To: to.OID, Base: from, Head: to,
		ChangesetBase: base, Label: label(from, to, base),
	}, nil
}

// resolve pins one checkpoint. It takes HEAD because two kinds need it: the
// changeset base is a merge base against it, and the working tree is it.
func (c Checkpoint) resolve(ctx context.Context, repo *git.Repo, base string, summary lifecycle.Summary, head string) (Checkpoint, error) {
	out := c
	switch c.Kind {
	case KindChangesetBase:
		mb, err := repo.MergeBase(ctx, base, head)
		if err != nil {
			return out, fmt.Errorf("cannot resolve changeset span against base %q: %w", base, err)
		}
		out.OID, out.Name = mb, base
		return out, nil
	case KindWorkingTree:
		out.OID, out.Name = head, "working tree"
		return out, nil
	case KindReview:
		if len(summary.Reviews) == 0 {
			return out, ErrNoReviews
		}
		review, ok := summary.ReviewIndex(c.Index)
		if !ok {
			return out, fmt.Errorf("no review at index %d: this changeset has %d review(s) (valid: 0..%d or -1..-%d)",
				c.Index, len(summary.Reviews), len(summary.Reviews)-1, len(summary.Reviews))
		}
		idx := c.Index
		if idx < 0 {
			idx += len(summary.Reviews)
		}
		out.OID, out.Name, out.ResolvedIndex = review.SHA, review.Short, idx
		return out, nil
	case KindCommit:
		oid, err := repo.RevParse(ctx, c.Name+"^{commit}")
		if err != nil {
			return out, fmt.Errorf("no commit %q in this repository: %w", c.Name, err)
		}
		out.OID = oid
		out.Name = short(oid)
		return out, nil
	case KindRef:
		// A checkpoint that arrives already pinned resolves to its pin. The pin is the answer
		// to "where was main when I chose it", and re-resolving here would follow the branch
		// on the next rescan — which is the thing requirements §13 forbids, and would make
		// drift undetectable, since the span would quietly arrive at wherever main got to.
		if c.OID != "" {
			return out, nil
		}
		oid, err := repo.RevParse(ctx, c.Name+"^{commit}")
		if err != nil {
			return out, fmt.Errorf("cannot resolve %q to a commit: %w", c.Name, err)
		}
		out.OID = oid
		return out, nil
	}
	return out, fmt.Errorf("unknown checkpoint kind %d", int(c.Kind))
}

// label renders a resolved span. The two shapes reviewers see every day keep the
// wording they have always had; everything a picker can reach is spelled out.
func label(base, head Checkpoint, changesetBase string) string {
	// Three dots only for the changeset base, because that is git's spelling of "from the merge
	// base", which is what that endpoint is. Every other pair of endpoints is an ordinary
	// two-dot range between two named commits.
	if base.Kind == KindChangesetBase {
		return changesetBase + "..." + display(head)
	}
	return display(base) + ".." + display(head)
}

// reviewName is what a reviewer called a submission, in the words they used. The spelling they
// chose is the spelling they get back: `--since-review=-2` reads "review -2" even though it lands
// on the same submission as "review 0", because the alias is what they typed, what the picker
// listed, and what `v` shows them when they step back onto it. Two ways of naming one span
// should not look like two different spans were chosen.
//
// The newest submission gets a word rather than a number: "review -1" asks the reader to do the
// counting, while "last review" is the thing they meant.
func reviewName(c Checkpoint) string {
	if c.Index == -1 {
		return "last review"
	}
	return fmt.Sprintf("review %d", c.Index)
}

func display(c Checkpoint) string {
	switch c.Kind {
	case KindWorkingTree:
		// "current", not "HEAD". This endpoint is the newest commit *plus* the reviewer's own
		// uncommitted work, and `HEAD` names only the commit — and on a screen whose live/history
		// distinction turns on exactly this endpoint, a git word for "a commit" reads like a
		// historical point. `main...current` also cannot be mistaken for a range you can paste.
		return "current"
	case KindReview:
		return reviewName(c)
	case KindChangesetBase:
		// resolve names it after the changeset's base ref, which is how a reviewer names it.
		return c.Name
	case KindRef:
		return ShortRef(c.Name) + "@" + short(c.OID)
	}
	return short(c.OID)
}

// ShortRef trims a full `refs/...` name to the form a reviewer would type, keeping enough
// to identify it: `refs/heads/main` becomes `main`, `refs/remotes/origin/main` keeps the
// remote, and anything else (`refs/git-pair/archive/booking`) keeps its full name rather than
// becoming ambiguous. The checkpoint itself keeps the full name, so resolution stays
// unambiguous and drift can be checked against the right ref.
func ShortRef(name string) string {
	for _, prefix := range []string{"refs/heads/", "refs/remotes/", "refs/tags/"} {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			return rest
		}
	}
	return name
}

// Drift is a ref checkpoint whose ref has moved since it was pinned.
// TracksRefs says whether the span has a named-ref endpoint, which is the only kind that
// can move underneath a review. A span without one has nothing to watch, and its session
// should not ask.
func (s Span) TracksRefs() bool {
	return s.Base.Kind == KindRef || s.Head.Kind == KindRef
}

type Drift struct {
	// Name is the ref, as the reviewer chose it.
	Name string
	// Pinned is the commit the span is using. Current is where the ref is now.
	Pinned, Current string
}

// String renders the drift the way the banner will: "main abc1234 → def5678".
func (d Drift) String() string { return fmt.Sprintf("%s %s → %s", d.Name, d.Pinned, d.Current) }

// Display is the drift as a reviewer reads it: the name they typed rather than the ref git
// stores, which is what makes the banner say `main moved` instead of `refs/heads/main moved`.
// The full name stays in Name, because that is what re-resolves and what tells a branch and a
// tag of the same name apart.
func (d Drift) Display() string {
	return fmt.Sprintf("%s %s → %s", ShortRef(d.Name), d.Pinned, d.Current)
}

// Drift re-resolves every ref checkpoint in the span and reports the ones that
// have moved. It changes nothing: a moved ref stays pinned until the reviewer says
// otherwise, because following a branch mid-review would silently change what the
// already-reviewed files meant.
func (s Span) Drift(ctx context.Context, repo *git.Repo) ([]Drift, error) {
	var out []Drift
	for _, c := range [2]Checkpoint{s.Base, s.Head} {
		if c.Kind != KindRef {
			continue
		}
		now, err := repo.RevParse(ctx, c.Name+"^{commit}")
		if err != nil {
			return nil, fmt.Errorf("cannot check whether %s has moved: %w", c.Name, err)
		}
		if now != c.OID {
			out = append(out, Drift{Name: c.Name, Pinned: short(c.OID), Current: short(now)})
		}
	}
	return out, nil
}

// RefreshRef re-pins one ref checkpoint to where the ref points now and recomputes
// the span. It is the only way a live span's endpoints move, and it exists because
// the reviewer asked for it.
func (s Span) RefreshRef(ctx context.Context, repo *git.Repo, name string) (Span, error) {
	next := s
	moved := false
	for i, c := range [2]Checkpoint{s.Base, s.Head} {
		if c.Kind != KindRef || c.Name != name {
			continue
		}
		now, err := repo.RevParse(ctx, name+"^{commit}")
		if err != nil {
			return Span{}, fmt.Errorf("cannot refresh %q: %w", name, err)
		}
		c.OID = now
		if i == 0 {
			next.Base, next.From = c, now
		} else {
			next.Head, next.To = c, now
		}
		moved = true
	}
	if !moved {
		return Span{}, fmt.Errorf("this span has no ref checkpoint named %q", name)
	}
	next.Label = label(next.Base, next.Head, next.ChangesetBase)
	return next, nil
}

// Key names a chosen span the way it was chosen, before and after resolution: the same
// review index, the same ref, the same id as typed. The session's span ring uses it so that
// rescanning a span is not mistaken for visiting a new one, while a span reached twice by
// two different routes is still two stops a keystroke apart.
func (s Selector) Key() string { return s.Base.key() + "\x1f" + s.Head.key() }

// Display names a span from what the reviewer chose, without resolving anything. That is the point:
// it is how `v` talks about a stop it could not enter, where the commits are unknown to git just then
// -- which is the reason for skipping it.
func (s Selector) Display() string {
	return s.Base.String() + ".." + s.Head.String()
}

func (c Checkpoint) key() string {
	switch c.Kind {
	case KindReview:
		return fmt.Sprintf("review:%d", c.Index)
	case KindRef:
		return "ref:" + c.Name
	case KindCommit:
		return "commit:" + strings.ToLower(c.Name)
	default:
		return c.Kind.String()
	}
}

// short is the form a reviewer can type back.
func short(oid string) string {
	if len(oid) > 7 {
		return oid[:7]
	}
	return oid
}

// isHex recognises what git printed as an object id, so a checkpoint knows the
// difference between an id to abbreviate and a revision to quote back as typed.
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
