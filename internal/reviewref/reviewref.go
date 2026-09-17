// Package reviewref owns the durable refs that keep review history reachable.
//
// A changeset has one of them: its archive, named by the changeset id rather than by any
// branch, so the work stays findable after the branch is deleted and readable by a CI job that
// never had the branch.
//
//	refs/git-pair/changesets/<id>/archive
//
// The archive holds the complete unsquashed implementation/review/fix chain, which is what lets
// a branch be squash-merged without losing the review conversation. It moves forward as review
// happens — `change ready` and `review submit` write it as they write their markers, and
// `change archive` advances it over commits that are review artifacts only — and never
// backwards, which is the guarantee that makes the ref worth resolving.
//
// Nothing lives at `refs/git-pair/changesets/<id>` itself. A git ref cannot be a leaf and a
// namespace at once — git enforces that by refusing to create the leaf once a child exists — so
// every ref a changeset owns is a child of that path. `archive` is one; `integration`, the record
// of the work landing, is reserved beside it and written by `git pair integration record`.
package reviewref

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gitpair/internal/git"
)

// root is the ref namespace holding every changeset's durable refs. Callers that need to look
// for those refs ask for NamespaceRoot rather than rebuilding a path, so the layout lives here.
const root = "refs/git-pair/changesets"

// archiveChild is the child of a changeset's namespace that holds its archive. It is a child
// rather than the namespace itself because a ref cannot be both a leaf and a namespace, and the
// namespace already holds — and will hold more than — one ref per changeset.
const archiveChild = "archive"

// Archive returns the changeset's archive ref.
func Archive(id string) string { return root + "/" + id + "/" + archiveChild }

// NamespaceRoot is the ref namespace holding every changeset's durable refs.
func NamespaceRoot() string { return root }

// Namespace is the ref namespace one changeset owns. Nothing lives at Namespace itself:
// a git ref cannot be both a leaf and a namespace, which git enforces by refusing the
// leaf once a child exists. Every ref for a changeset is therefore a child of this path.
func Namespace(id string) string { return root + "/" + id }

// Taken reports whether any durable ref already belongs to this changeset id, which is
// how `change init` refuses to hand out a name that is already someone's (PRD §5).
//
// Any child counts, not just the archive: a changeset that has an integration ref and no
// archive ref has a history, and handing its name to a new changeset would attach that history
// to a stranger. Matching is by path component, so `booking` is not blocked by `booking-v2`.
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

// ChangesetID reports the changeset id a durable ref names, so
// `refs/git-pair/changesets/booking/archive` answers `booking`, and anything else — including a
// ref in some other namespace — answers no. Callers that want only one kind of child say so: a
// namespace holding only an integration ref belongs to a changeset with no archived history.
//
// A stacked changeset records its parent as either the parent's id or the ref that names it, and
// both mean the same parent, so the resolution rule has to read the id out of either spelling.
// Only this namespace is accepted, and only as `<id>/<child>`: a branch is allowed to be called
// `feature/archive`, and mistaking one for a durable ref would attribute the branch's work to a
// changeset called `feature`.
func ChangesetID(ref string) (string, bool) {
	rest, ok := strings.CutPrefix(ref, root+"/")
	if !ok {
		return "", false
	}
	id, child, ok := strings.Cut(rest, "/")
	if !ok || id == "" || child == "" || strings.Contains(child, "/") {
		return "", false
	}
	return id, true
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

// Entry is a changeset's archive ref.
type Entry struct {
	Ref string
	SHA string
	// ID is the changeset the ref belongs to.
	ID string
}

// List returns every changeset's archive ref.
//
// Refs in the namespace that are not archives are skipped rather than reported: a namespace
// holding only an integration ref belongs to a changeset with no archived history, and a caller
// asking for archives would be wrong to treat it as one.
func List(ctx context.Context, repo *git.Repo) ([]Entry, error) {
	refs, err := repo.ForEachRef(ctx, root)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, r := range refs {
		id, ok := ChangesetID(r.Name)
		if !ok || !strings.HasSuffix(r.Name, "/"+archiveChild) {
			continue
		}
		out = append(out, Entry{Ref: r.Name, SHA: r.SHA, ID: id})
	}
	return out, nil
}
