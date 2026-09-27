// Package reviewref owns git-pair's durable refs.
//
// There are exactly two families, both flat, both named by the changeset id rather than by any
// branch, and both written by one command at one moment — `git pair integration record`, after the
// work has landed:
//
//	refs/git-pair/archive/<id>      the real unsquashed, unrebased tip: implementation and review
//	                                commits interleaved, the whole conversation
//	refs/git-pair/integrations/<id> the commit that introduced the changeset into the destination
//	                                branch
//
// Together they are the permanent paper trail: what was reviewed, and what it became. The pair is
// written in that order — archive first, integration second — because the ref whose existence means
// "this changeset is finished" is the integration one, so a crash between the two leaves a
// changeset that reads as recorded-but-not-finished and a retry completes it. The reverse order
// would leave a finished changeset whose chain nothing anchors.
//
// Neither ref ever moves and neither is ever deleted by git-pair. Nothing in this package writes a
// ref while work is in flight: during a review the branch is the whole story, and the only durable
// fact worth a ref is the one that does not exist until landing. That is also why publishing them
// is a refspec rather than a protocol — both are append-only, so no fetch or push in this
// repository needs a force.
//
// Create-only is tolerant of an identical re-create: asking for the commit the ref already names is
// a no-op, and asking for a different one is a refusal naming both. Agents retry, and a re-run
// after a partial write has to be the way you finish the write rather than the way you discover you
// need a command git-pair does not have.
package reviewref

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gitpair/internal/git"
)

// NamespaceRoot is the ref namespace holding every durable git-pair ref. Callers that need to look
// for those refs ask for it rather than rebuilding a path, so the layout lives here — including in
// the messages that tell a reader how to fetch what is missing.
const NamespaceRoot = "refs/git-pair"

// The two families are separate namespaces rather than children of one path per changeset, so a
// flat `<family>/<id>` leaf is legal and a changeset id never has to be parsed out of a ref path.
const (
	integrationsRoot = NamespaceRoot + "/integrations"
	archiveRoot      = NamespaceRoot + "/archive"
)

// Integration returns the changeset's integration ref: the commit the changeset became in the
// destination branch.
func Integration(id string) string { return integrationsRoot + "/" + id }

// Archive returns the changeset's archive ref: the unsquashed, unrebased tip the record was made
// from. It exists only once the changeset has landed, which is the whole point — before that the
// branch holds the chain, and a ref that moved with the branch would be a second, staler copy of
// it.
func Archive(id string) string { return archiveRoot + "/" + id }

// FetchRefspec brings the durable refs into a clone. They are not fetched by default — a clone
// takes refs/heads/* into refs/remotes/*, and these are neither — so a CI job that was handed the
// branch has to ask for them, and every message that says so spells it this way. There is no `+`:
// both families are append-only, so a fetch that would have to move one of them is the bug, not the
// case to enable.
const FetchRefspec = NamespaceRoot + "/*:" + NamespaceRoot + "/*"

// FetchCommand is the whole command that fixes an empty namespace, spelled once so the guidance in
// a failure and the guidance in the README cannot drift.
const FetchCommand = "git fetch origin '" + FetchRefspec + "'"

// PushRefspec sends this clone's durable refs to a remote. It is the same spelling as FetchRefspec —
// each ref maps to the path it already has — and is named separately because the two go into different
// config keys, and a reader of `remote.origin.push` should read a push constant, not infer the value
// from a fetch one.
//
// Like the fetch refspec it carries no `+`. Both families are create-only, so a remote that holds a
// different value rejects the push instead of being overwritten by it, which is `integration publish`'s
// conflict policy expressed in configuration: a repository can make an ordinary `git push` publish the
// namespace, and even then it cannot move a record somebody else wrote.
const PushRefspec = NamespaceRoot + "/*:" + NamespaceRoot + "/*"

// MirrorRoot is where a clone keeps its copies of *another* repository's durable refs. It sits under
// the remote-tracking namespace on purpose: like `refs/remotes/origin/feature/x`, a mirror is somebody
// else's state seen from here, and git's own conventions already say what happens to it on a prune.
func MirrorRoot(remote string) string {
	return "refs/remotes/" + remote + "/" + NamespaceRoot
}

// MirrorRefspec maps the durable namespace onto its mirror.
//
// The `+` is the difference between a mirror and a record. FetchRefspec has none, because the records
// are append-only and a fetch that would have to move one is reporting a bug. A mirror exists to
// agree with the remote or be wrong, so it is allowed to move — and it is pruned, because a mirror of
// a deleted ref would otherwise go on reporting a record that no longer exists.
func MirrorRefspec(remote string) string {
	return "+" + NamespaceRoot + "/*:" + MirrorRoot(remote) + "/*"
}

// FetchPlan is what `--fetch` asks a remote for, split into the two asks because the two halves must be
// fetched differently and one `git fetch` cannot say both.
//
// Records come first and without `--prune`: they are records wherever they are read — a paper trail that
// replicates is the whole point, and this is what makes `Present` and `ResolveIntegration` answer
// properly in a clone that never did the landing — and pruning their destination would delete the local
// records of a landing that has not been published yet. Mirrors come with `--prune`, because a mirror is
// not a record and exists to agree with the remote or be wrong.
//
// This is two fetches where the first draft of the design wanted one. The cost is one extra git call per
// `--fetch`; the alternative is a read command that deletes the paper trail it came to read.
type FetchPlan struct {
	Records []string
	Mirrors []string
}

// FetchPlanFor is the plan for one remote.
func FetchPlanFor(remote string) FetchPlan {
	return FetchPlan{Records: []string{FetchRefspec}, Mirrors: []string{MirrorRefspec(remote)}}
}

// MirrorIntegration and MirrorArchive name one changeset's two mirrors. They are the comparison basis
// for published-not-published reporting, and nothing that answers "is this recorded" reads them —
// see TestMirrorsAreNeverRecords.
func MirrorIntegration(remote, id string) string { return MirrorRoot(remote) + "/integrations/" + id }
func MirrorArchive(remote, id string) string     { return MirrorRoot(remote) + "/archive/" + id }

// MirrorPresent reports whether this clone holds any mirror of the given remote's durable refs at
// all. "No mirrors" is not "nothing published": it usually means the refspec was never configured,
// which is a fact about this clone's fetch configuration and has to be reported as such.
func MirrorPresent(ctx context.Context, repo *git.Repo, remote string) (bool, error) {
	refs, err := repo.ForEachRef(ctx, MirrorRoot(remote)+"/")
	if err != nil {
		return false, err
	}
	return len(refs) > 0, nil
}

// RemoteList reads this clone's mirrors of a remote's durable refs, under
// `refs/remotes/<remote>/refs/git-pair/`.
//
// It is the comparison basis for "has the record travelled", and the only place mirrors are read. The
// rule it exists to keep out is the expensive one: nothing that answers "is this recorded" may consult
// it, because a remote-tracking copy is somebody else's state seen from here and not a fact about this
// repository (PRD §13).
func RemoteList(ctx context.Context, repo *git.Repo, remote string) ([]Entry, error) {
	root := MirrorRoot(remote)
	refs, err := repo.ForEachRef(ctx, root)
	if err != nil {
		return nil, err
	}
	var out []Entry
	prefix := root + "/"
	for _, r := range refs {
		// The name under the mirror root is the same shape git-pair writes at home, so the same
		// identification applies once the `refs/remotes/<remote>/` head is trimmed.
		rest, ok := strings.CutPrefix(r.Name, "refs/remotes/"+remote+"/")
		if !ok {
			continue
		}
		id, kind, ok := identify(rest)
		if !ok {
			continue
		}
		out = append(out, Entry{Ref: r.Name, SHA: r.SHA, ID: id, Kind: kind})
	}
	_ = prefix
	return out, nil
}

// Present reports whether this repository holds any durable git-pair ref at all.
//
// It answers a different question from ResolveArchive, and the two must not be conflated. "This
// changeset has no record" is a fact about the changeset — nobody ever recorded it. "This clone has
// no git-pair refs" is a fact about the fetch, and its fix is a refspec, not a lifecycle command. A
// CI job told "the record does not exist" goes off to re-run someone else's command; told the
// namespace is absent, it adds one line to its checkout.
//
// Callers reach for it on the failure path only: it is a `for-each-ref`, and a run that is going to
// succeed should not pay for asking.
func Present(ctx context.Context, repo *git.Repo) (bool, error) {
	refs, err := repo.ForEachRef(ctx, NamespaceRoot)
	if err != nil {
		return false, err
	}
	return len(refs) > 0, nil
}

// Taken reports whether either durable ref already belongs to this changeset id, which is how
// `init` refuses to hand out a name that is already someone's (PRD §5).
//
// Either family counts. A changeset with an integration ref and no archive ref has a history, and
// handing its name to a new changeset would attach that history to a stranger. Matching is on the
// two exact paths, so `booking` is not blocked by `booking-v2` — and a legacy ref left behind by the
// pre-two-ref layout (`refs/git-pair/changesets/<id>/archive`) does not block the name, because its
// id is no longer readable as a path component of these families.
func Taken(ctx context.Context, repo *git.Repo, id string) (bool, error) {
	refs, err := repo.ForEachRef(ctx, NamespaceRoot)
	if err != nil {
		return false, err
	}
	archive, integration := Archive(id), Integration(id)
	for _, r := range refs {
		if r.Name == archive || r.Name == integration {
			return true, nil
		}
	}
	return false, nil
}

// ResolveIntegration returns the commit a changeset was integrated as, or ErrNotIntegrated when the
// record does not exist. Integrated-ness is the presence of this ref and nothing else: squash,
// rebase and cherry-pick rewrite commit identity, so no ancestry or patch-ID reading can derive the
// fact (requirements §14).
func ResolveIntegration(ctx context.Context, repo *git.Repo, id string) (string, error) {
	sha, err := repo.ResolveRef(ctx, Integration(id))
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNotIntegrated, Integration(id))
	}
	return sha, nil
}

// ErrNotIntegrated means the changeset has no integration ref — which is not the same as saying it
// has not landed. It says nobody recorded it, which in a CI clone that never fetched
// refs/git-pair/* is a fetching problem, and elsewhere is the state the queue reports as landed but
// unrecorded.
var ErrNotIntegrated = errors.New("this changeset has not been integrated")

// ResolveArchive returns the commit a changeset's archive ref points at, or ErrNoArchiveRef when it
// does not exist — which is what a changeset that has never been recorded looks like.
func ResolveArchive(ctx context.Context, repo *git.Repo, id string) (string, error) {
	sha, err := repo.ResolveRef(ctx, Archive(id))
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNoArchiveRef, Archive(id))
	}
	return sha, nil
}

// ErrNoArchiveRef means the changeset has no archived chain: no `integration record` has ever run
// for it. Reading it as "never reviewed" would be wrong — before landing, the branch holds the
// chain and no ref does.
var ErrNoArchiveRef = errors.New("no archive ref for this changeset")

// Kind distinguishes the two durable ref families in a List entry.
type Kind string

const (
	// KindArchive is the unsquashed implementation-and-review chain.
	KindArchive Kind = "archive"
	// KindIntegration is the record of the commit the changeset became.
	KindIntegration Kind = "integration"
)

// Entry is one ref under the durable namespace.
type Entry struct {
	Ref string
	SHA string
	// ID is the changeset the ref belongs to.
	ID string
	// Kind is which of the two families this ref is in. It is empty for a ref that lives under the
	// namespace and belongs to neither family — a stray, a retired-layout path, a family spelled wrongly.
	// Such an entry is not a record of anything; it is in the listing because "is this namespace empty?"
	// is a question about what is under the root, and answering it from the classified entries alone
	// reports a fetched clone as an unfetched one (PRD §13.4).
	Kind Kind
}

// List reads the durable namespace in one pass.
//
// One `for-each-ref` over the namespace is the whole cost, which is what lets the resolver answer
// "which of these landed?" for every changeset in a repository without a question per changeset.
// Entries are returned in git's ref order; callers that care about an ordering say so themselves.
//
// Every ref under the root comes back, including the ones that are not durable refs — see Entry.Kind.
// What the two families are is git-pair's; what lives under the root at all is a fact about the clone,
// and only the second of those two is safe to report as an absent fetch.
func List(ctx context.Context, repo *git.Repo) ([]Entry, error) {
	refs, err := repo.ForEachRef(ctx, NamespaceRoot)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, r := range refs {
		id, kind, ok := identify(r.Name)
		if !ok {
			out = append(out, Entry{Ref: r.Name, SHA: r.SHA})
			continue
		}
		out = append(out, Entry{Ref: r.Name, SHA: r.SHA, ID: id, Kind: kind})
	}
	return out, nil
}

// identify reads a changeset id and a family out of a durable ref path, and answers no for anything
// that is not one — a stray ref someone else put under the namespace, or a family directory spelled
// wrongly. Both families are one level deep, so a path with another slash in it names no changeset.
//
// The retired layout (`refs/git-pair/changesets/<id>/{archive,integration}`, written before the two
// families existed) answers no. It is not a record of either fact, and a clone holding it is not a clone
// holding a durable ref: see List for what the namespace holding only such refs still proves.
func identify(ref string) (string, Kind, bool) {
	for _, family := range []struct {
		prefix string
		kind   Kind
	}{
		{archiveRoot + "/", KindArchive},
		{integrationsRoot + "/", KindIntegration},
	} {
		id, ok := strings.CutPrefix(ref, family.prefix)
		if !ok || id == "" || strings.Contains(id, "/") {
			continue
		}
		return id, family.kind, true
	}
	return "", "", false
}

// IntegrationID returns the changeset an integration ref names, and answers false for anything that is
// not one — an archive ref, a mirror, a stray, or a retired-layout path.
//
// It exists for the caller holding a ref and needing to know whose it is: a child's measurement base is
// its parent's integration ref once the parent has landed, and reading where the parent's work went means
// turning that ref back into an id. `identify` does the matching; this says which of the two families the
// caller meant to ask about, because an archive ref holds the chain and not the landing, and guessing
// from the last path segment would read it as a landing too.
func IntegrationID(ref string) (string, bool) {
	id, kind, ok := identify(ref)
	return id, ok && kind == KindIntegration
}

// ErrRefConflict is returned by CreateOnly when the ref exists at a different commit than the one
// requested. It is the loud half of create-only: the record is written once, and an attempt to
// write a different one is the case where somebody is about to lose paper trail.
var ErrRefConflict = errors.New("the durable ref already exists at a different commit")

// CreateOnly points ref at sha, and refuses rather than moving a ref that exists.
//
// It reports whether this call created the ref. Re-asking for the commit the ref already names is a
// no-op returning false, not an error: agents retry, and the retry has to complete the record
// instead of failing on the half that succeeded. Asking for a different commit is ErrRefConflict,
// and the error names both commits — the question a re-run asks is "what did we already say?".
//
// The write itself is create-only at the git level (`update-ref` with the all-zeros old value), so
// two processes recording the same landing cannot both win, and this function's read of an
// existing ref is a report rather than a check.
func CreateOnly(ctx context.Context, repo *git.Repo, ref, sha string) (bool, error) {
	created, err := repo.CreateRefIfAbsent(ctx, ref, sha)
	if err == nil {
		return created, nil
	}
	// The create failed. Either somebody held the name the whole time or the write raced, and both
	// mean the ref this call did not write names something — so answer with what it names rather than
	// with git's wording.
	existing, readErr := repo.ResolveRef(ctx, ref)
	if readErr != nil || existing == sha {
		return false, fmt.Errorf("creating %s: %w", ref, err)
	}
	return false, conflictError(ref, existing, sha)
}

// conflictError is the refusal every create-only write shares. It answers the question a re-run is
// actually asking — what is already on the record, and what did I ask for — and says in the same
// breath that there is no flag for overriding it, because there is no operation that would implement
// one.
func conflictError(ref, existing, wanted string) error {
	return fmt.Errorf("%w: %s records %s, and %s was asked for\n\n"+
		"git-pair never moves a durable ref; if this pair is wrong, the record was written from the wrong commit",
		ErrRefConflict, ref, short(existing), short(wanted))
}

// RecordedPair reads both halves of a changeset's record. A half that does not exist is the empty
// string rather than an error, because the caller is asking "what is on the record, if anything" and a
// half pair is a real answer — it is the crash case `CreatePair` completes.
func RecordedPair(ctx context.Context, repo *git.Repo, id string) (Pair, error) {
	out := Pair{ID: id}
	sha, err := ResolveArchive(ctx, repo, id)
	if err != nil && !errors.Is(err, ErrNoArchiveRef) {
		return Pair{}, err
	}
	out.Archive = sha
	sha, err = ResolveIntegration(ctx, repo, id)
	if err != nil && !errors.Is(err, ErrNotIntegrated) {
		return Pair{}, err
	}
	out.Integration = sha
	return out, nil
}

// Conflict reports the refusal `CreatePair` would return for pair, without writing anything.
//
// It exists so the recorder can refuse an already-recorded changeset before it starts verifying a
// claim it has no way to write. The order matters for what the reader learns: a second landing of a
// changeset whose directory is already in the destination branch fails the landing check too, and
// "this changeset is recorded at <sha>, and refs do not move" is the answer to the question that was
// actually asked. The write keeps its own conflict handling — this is a read, and a race is still
// resolved by git's create-only update.
func Conflict(ctx context.Context, repo *git.Repo, pair Pair) error {
	recorded, err := RecordedPair(ctx, repo, pair.ID)
	if err != nil {
		return err
	}
	if recorded.Archive != "" && recorded.Archive != pair.Archive {
		return conflictError(Archive(pair.ID), recorded.Archive, pair.Archive)
	}
	if recorded.Integration != "" && recorded.Integration != pair.Integration {
		return conflictError(Integration(pair.ID), recorded.Integration, pair.Integration)
	}
	return nil
}

// Pair is what one `integration record` invocation writes: both halves of the record.
type Pair struct {
	// ID is the changeset the pair belongs to.
	ID string
	// Archive is the unsquashed tip `refs/git-pair/archive/<id>` holds.
	Archive string
	// Integration is the landing commit `refs/git-pair/integrations/<id>` holds.
	Integration string
}

// PairResult reports what a write did, so a command can say "recorded" and "already recorded"
// about each half of the pair separately rather than claiming something it did not do.
type PairResult struct {
	ArchiveCreated     bool
	IntegrationCreated bool
	ArchiveRef         string
	IntegrationRef     string
	Archive            string
	IntegrationCommit  string
}

// CreatePair writes both refs for one changeset, archive first.
//
// The order is the reason a half-written pair is recoverable: the integration ref is the one whose
// existence means "this changeset is finished", so if the process dies between the two writes the
// changeset still reads as not-yet-recorded and the next invocation completes it. Writing
// integration first would leave a finished changeset whose chain nothing holds — the exact loss the
// archive ref exists to prevent.
//
// Re-running with the same pair creates nothing and fails nothing. Re-running with a pair whose
// other half disagrees is a refusal from CreateOnly, and the refusal says which ref and which two
// commits disagree.
func CreatePair(ctx context.Context, repo *git.Repo, pair Pair) (PairResult, error) {
	res := PairResult{
		ArchiveRef:        Archive(pair.ID),
		IntegrationRef:    Integration(pair.ID),
		Archive:           pair.Archive,
		IntegrationCommit: pair.Integration,
	}
	archive, err := CreateOnly(ctx, repo, res.ArchiveRef, pair.Archive)
	if err != nil {
		return res, err
	}
	res.ArchiveCreated = archive
	created, err := CreateOnly(ctx, repo, res.IntegrationRef, pair.Integration)
	if err != nil {
		return res, err
	}
	res.IntegrationCreated = created
	return res, nil
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
