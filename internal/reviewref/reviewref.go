// Package reviewref owns the durable refs that keep review history reachable.
//
// A changeset has one of them: its archive.
//
//	refs/reviews/<changeset>
//
// It holds the complete unsquashed implementation/review/fix chain, which is what lets a branch
// be squash-merged without losing the review conversation. It moves forward as review happens —
// `change ready` and `review submit` write it as they write their markers, and `change archive`
// advances it over commits that are review artifacts only — and never backwards, which is the
// guarantee that makes the ref worth resolving.
package reviewref

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gitpair/internal/git"
)

const (
	root        = "refs/reviews"
	fullPattern = "refs/reviews"
)

// Archive returns the changeset's archive ref.
func Archive(id string) string { return root + "/" + id }

// NamespaceRoot is the ref namespace holding every changeset's durable refs. Callers
// that need to look for a changeset's refs ask for this rather than rebuilding a path,
// so the layout changes in one place.
func NamespaceRoot() string { return root }

// Namespace is the ref namespace one changeset owns. Nothing lives at Namespace itself:
// a git ref cannot be both a leaf and a namespace, which git enforces by refusing the
// leaf once a child exists. Every ref for a changeset is therefore a child of this path.
func Namespace(id string) string { return root + "/" + id }

// Taken reports whether any durable ref already belongs to this changeset id, which is
// how `change init` refuses to hand out a name that is already someone's (PRD §5).
//
// Both shapes are checked because the refs are in transit between layouts: a leaf at the
// namespace (`refs/reviews/<id>`, today) or children of it
// (`refs/git-pair/changesets/<id>/*`, where this is heading, and the only shape git
// allows once a child exists). Matching is by path component, so `booking` is not blocked
// by `booking-v2`.
func Taken(ctx context.Context, repo *git.Repo, id string) (bool, error) {
	ns := Namespace(id)
	refs, err := repo.ForEachRef(ctx, root)
	if err != nil {
		return false, err
	}
	for _, r := range refs {
		if r.Name == ns || strings.HasPrefix(r.Name, ns+"/") {
			return true, nil
		}
	}
	return false, nil
}

// ChangesetRoot is the ref namespace where a changeset's durable refs live: the changeset id
// names the refs, not the branch, so the work stays findable after the branch is deleted and
// readable by a CI job that never had the branch (`refs/git-pair/changesets/<id>/archive`).
//
// Archive() still builds its ref under NamespaceRoot; this is where it is heading, and the two
// are the same namespace once the move lands. The resolver reads either spelling, because a
// `base:` that names its parent by ref is the same parent either way.
const ChangesetRoot = "refs/git-pair/changesets"

// ArchivedChangesetID reports the changeset id a durable archive ref names, so
// `refs/git-pair/changesets/booking/archive` answers `booking`.
//
// A stacked changeset records its parent as either the parent's id or the ref that names it,
// and both mean the same parent, so the resolution rule has to read the id out of either
// spelling. Only this namespace is accepted: a branch is allowed to be called
// `feature/archive`, and mistaking one for a durable ref would attribute the branch's work to
// a changeset called `feature`.
func ArchivedChangesetID(ref string) (string, bool) {
	rest, ok := strings.CutPrefix(ref, ChangesetRoot+"/")
	if !ok {
		return "", false
	}
	id, ok := strings.CutSuffix(rest, "/archive")
	if !ok || id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}

// Slug extracts the changeset name from a review ref, or "" and false.
func Slug(ref string) (string, bool) {
	if !strings.HasPrefix(ref, root+"/") {
		return "", false
	}
	slug := strings.TrimPrefix(ref, root+"/")
	if slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

// Update points the changeset's archive ref at sha. Called in the same operation that
// creates a review commit, never later.
func Update(ctx context.Context, repo *git.Repo, id, sha string) (string, error) {
	ref := Archive(id)
	if err := repo.UpdateRef(ctx, ref, sha); err != nil {
		return ref, fmt.Errorf("updating %s: %w", ref, err)
	}
	return ref, nil
}

// Resolve returns the SHA the changeset's archive ref points at.
func Resolve(ctx context.Context, repo *git.Repo, id string) (string, error) {
	sha, err := repo.ResolveRef(ctx, Archive(id))
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNoArchiveRef, Archive(id))
	}
	return sha, nil
}

// ErrNoArchiveRef means the changeset has never had a review submission.
var ErrNoArchiveRef = errors.New("no archive ref for this changeset")

// Entry is a review ref with its changeset name.
type Entry struct {
	Ref  string
	SHA  string
	Slug string
}

// List returns every changeset's archive ref.
func List(ctx context.Context, repo *git.Repo) ([]Entry, error) {
	refs, err := repo.ForEachRef(ctx, fullPattern)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, r := range refs {
		slug, ok := Slug(r.Name)
		if !ok {
			continue
		}
		out = append(out, Entry{Ref: r.Name, SHA: r.SHA, Slug: slug})
	}
	return out, nil
}
