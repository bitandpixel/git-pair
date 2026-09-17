// Package reviewref owns the refs that keep review history reachable.
//
// Two kinds exist:
//
//	refs/reviews/<changeset>                     movable, current review HEAD
//	refs/reviews/archive/<changeset>/<short-sha> immutable, written at completion
//
// Together they hold the complete unsquashed implementation/review/fix chain
// so a branch can be squash-merged without losing the review conversation.
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
	archive     = "refs/reviews/archive"
	fullPattern = "refs/reviews"
)

// Head returns the movable review ref for a changeset.
func Head(slug string) string { return root + "/" + slug }

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
// by `booking-v2`. The archive refs are not checked separately: they are written by the
// same commands as the movable ref and nothing deletes refs, so an archive always has a
// movable ref alongside it.
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

// Archive returns the immutable archival ref for a changeset at a commit.
func Archive(slug, shortSHA string) string {
	return fmt.Sprintf("%s/%s/%s", archive, slug, shortSHA)
}

// ArchivePattern matches every archive ref belonging to a changeset.
func ArchivePattern(slug string) string {
	return fmt.Sprintf("%s/%s/*", archive, slug)
}

// IsArchive reports whether ref is one of the immutable archive refs.
func IsArchive(ref string) bool { return strings.HasPrefix(ref, archive+"/") }

// Slug extracts the changeset name from a review ref, or "" and false.
func Slug(ref string) (string, bool) {
	if IsArchive(ref) {
		return "", false
	}
	if !strings.HasPrefix(ref, root+"/") {
		return "", false
	}
	slug := strings.TrimPrefix(ref, root+"/")
	if slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

// Update points the changeset's review ref at sha. Called in the same
// operation that creates a review commit, never later.
func Update(ctx context.Context, repo *git.Repo, slug, sha string) (string, error) {
	ref := Head(slug)
	if err := repo.UpdateRef(ctx, ref, sha); err != nil {
		return ref, fmt.Errorf("updating %s: %w", ref, err)
	}
	return ref, nil
}

// Resolve returns the SHA the changeset's review ref points at.
func Resolve(ctx context.Context, repo *git.Repo, slug string) (string, error) {
	sha, err := repo.ResolveRef(ctx, Head(slug))
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNoReviewRef, Head(slug))
	}
	return sha, nil
}

// ErrNoReviewRef means the changeset has never had a review submission.
var ErrNoReviewRef = errors.New("no review ref for this changeset")

// ArchiveCommit creates refs/reviews/archive/<slug>/<shortSHA> pointing at sha
// if it does not already exist, and reports whether it was newly created. The
// ref is never moved, which is what makes the archive immutable: completing a
// changeset twice at the same head reuses the one ref.
func ArchiveCommit(ctx context.Context, repo *git.Repo, slug, sha string) (ref string, created bool, err error) {
	short := sha
	if len(short) > 7 {
		short = short[:7]
	}
	ref = Archive(slug, short)
	created, err = repo.CreateRefIfAbsent(ctx, ref, sha)
	if err != nil {
		return ref, false, fmt.Errorf("archiving to %s: %w", ref, err)
	}
	return ref, created, nil
}

// Entry is a review ref with its changeset name.
type Entry struct {
	Ref  string
	SHA  string
	Slug string
}

// List returns every review ref under refs/reviews, excluding archives.
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
