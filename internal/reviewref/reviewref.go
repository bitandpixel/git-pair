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
// every ref a changeset owns is a child of that path. `archive` is one, and `integration` — the
// record of where the work landed, written once and never moved — is the other.
package reviewref

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

// integrationChild is the child holding the record of the work landing: the commit the changeset
// became in the target history. It is written once by `git pair integration record` and never
// moved, and its existence freezes the archive (§23): the pair of refs is a mapping from the
// archived head to the integrated commit, and moving either end would silently rewrite that
// mapping for every later reader.
const integrationChild = "integration"

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

// RefuseIntegrated returns ErrArchiveFrozen when the changeset already has an integration record.
//
// It is the check `Update` makes, exposed for callers that want to refuse *before* writing. A
// marker commit and the archive move it triggers are one operation, so a command that committed
// first and then learned the ref was frozen would leave a marker on the branch with nothing
// pointing at it — a half-write no command can undo without a rewrite.
func RefuseIntegrated(ctx context.Context, repo *git.Repo, id string) error {
	integrated, err := ResolveIntegration(ctx, repo, id)
	if errors.Is(err, ErrNotIntegrated) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: %s is recorded as integrated at %s, so its archive does not move and git-pair records nothing further for it; the mapping from the archived head to that commit is the record",
		ErrArchiveFrozen, id, short(integrated))
}

// Update points the changeset's archive ref at sha. Called in the same operation that
// creates a review commit, never later.
//
// It is also the one place the archive freeze lives. Every command that moves the archive —
// `change ready`, `change unready`, `review submit`, `change abandon`, `change archive` — comes
// through here, so the rule "an integrated changeset's archive does not move" (requirements §23)
// is enforced once rather than remembered in five places, and a command added next month cannot
// forget it. The cost is one `rev-parse` per marker commit, which is what the guarantee is worth:
// the mapping `archive A → integration B` is the whole product of integration recording, and a
// later `change ready` on a branch someone forgot to delete would silently change what it says.
func Update(ctx context.Context, repo *git.Repo, id, sha string) (string, error) {
	if err := RefuseIntegrated(ctx, repo, id); err != nil {
		return Archive(id), err
	}
	ref := Archive(id)
	if err := repo.UpdateRef(ctx, ref, sha); err != nil {
		return ref, fmt.Errorf("updating %s: %w", ref, err)
	}
	return ref, nil
}

// ErrArchiveFrozen is returned by Update for a changeset whose integration ref exists.
var ErrArchiveFrozen = errors.New("the archive is frozen once the changeset is integrated")

// Integration returns the changeset's integration ref: the commit the changeset became in the
// target history, as recorded by `git pair integration record`.
func Integration(id string) string { return root + "/" + id + "/" + integrationChild }

// ResolveIntegration returns the commit a changeset was integrated as, or ErrNotIntegrated when
// the record does not exist. Integrated-ness is the presence of this ref and nothing else:
// squash, rebase and cherry-pick rewrite commit identity, so no ancestry or patch-ID reading can
// derive the fact (requirements §14).
func ResolveIntegration(ctx context.Context, repo *git.Repo, id string) (string, error) {
	sha, err := repo.ResolveRef(ctx, Integration(id))
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNotIntegrated, Integration(id))
	}
	return sha, nil
}

// ErrNotIntegrated means the changeset has no integration ref — which is not the same as saying
// it has not landed. It says nobody recorded it, which in a CI clone that never fetched
// refs/git-pair/changesets/* is a fetching problem (requirements §24).
var ErrNotIntegrated = errors.New("this changeset has not been integrated")

// CreateIntegration writes the integration ref, and refuses when one already exists: the record
// is created once and never rewritten (requirements §22). The existence check is what lets the
// command print its own explanation instead of git's `fatal: refusing to update ref`, and the
// create-only write behind it is what makes the check un-raceable — two CI jobs recording the same
// landing cannot both win.
func CreateIntegration(ctx context.Context, repo *git.Repo, id, sha string) (bool, error) {
	ref := Integration(id)
	created, err := repo.CreateRefIfAbsent(ctx, ref, sha)
	if err != nil {
		return false, fmt.Errorf("recording the integration of %s: %w", id, err)
	}
	return created, nil
}

// ArchivesAt returns every changeset whose archive ref points exactly at commit, sorted by id.
//
// This is the discovery a forge needs (requirements §16): the integration process knows the source
// SHA it built and not the changeset id, and the archive ref is the only thing that connects the
// two. It is an exact-object question, so callers resolve an abbreviated SHA before asking — and
// zero matches is a real answer, not a failure to interpret: it may mean the refs were never
// fetched, or the wrong SHA was supplied (§18).
func ArchivesAt(ctx context.Context, repo *git.Repo, commit string) ([]string, error) {
	refs, err := repo.ForEachRef(ctx, root)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, r := range refs {
		if r.SHA != commit {
			continue
		}
		id, ok := ChangesetID(r.Name)
		if !ok || !strings.HasSuffix(r.Name, "/"+archiveChild) {
			continue
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
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
