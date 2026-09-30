// Resolution of "which changeset is this revision working on".
//
// The rule is a content test, not a pointer: the changesets on a revision are the
// `changesets/<id>/` directories present in its tree and absent from the integration
// branch's tree. A directory that has reached the integration branch is landed work, so it
// drops out without needing a ref of any kind and without depending on whether the landing
// was a merge, a squash or a cherry-pick. A directory that exists only here is work in
// progress, whichever branch line it sits on — which is what lets a parent branch and the
// child branched off it resolve to the same changeset instead of one of them inventing a
// second answer (PRD §4, §7).
//
// Three properties are worth keeping in mind when changing this file:
//
//   - Nothing here asks which branch is checked out. Resolution reads trees, so a detached
//     HEAD, a CI checkout and a human's branch all answer the same question. Branch names
//     are not part of the durable data model.
//   - Nothing here reads a ref. Durable refs exist only once a changeset has been recorded, and
//     the tree rule already excludes landed work, so resolution asks git about trees and commit
//     lists and nothing else.
//   - The cost is bounded by the changesets on this revision, not by how many changesets have
//     ever existed. One revision costs one tree listing and one batch read; only a revision
//     carrying more than one unlanded changeset pays the two calls per candidate that order
//     them, because nothing has to be decided when there is one answer. The formulation this
//     replaces needed an ancestry test per archive ref, which measured 1,802 git invocations
//     for one resolution at 300 refs; this one measures five or six.
package changeset

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gitpair/internal/git"
)

// ErrNoDefaultBranch means nothing identifies the integration branch, so "has this landed?"
// cannot be answered. Refusing is deliberate: treating an unresolvable integration branch as
// "no such branch" would make every changeset directory on the revision a candidate, and a
// confident "this branch holds three changesets" is worse than an error naming the fix.
var ErrNoDefaultBranch = errors.New("cannot tell which branch is the integration branch")

// ErrAmbiguousChangeset means the revision carries more than one changeset directory and
// nothing in the durable data orders them. Guessing would silently read the wrong diff base.
var ErrAmbiguousChangeset = errors.New("this revision contains more than one changeset")

// AmbiguityError explains a tie. The candidates are named because the reader has no other way
// to see them — nothing has shown them yet — and both escape hatches are named, because a
// refusal without one is a dead end: `--changeset` answers for one command, `change use` settles
// it for the branch.
func AmbiguityError(res Resolution) error {
	ids := make([]string, 0, len(res.Candidates))
	for _, c := range res.Candidates {
		ids = append(ids, c.Changeset.Slug)
	}
	return fmt.Errorf("%w: %s; name the one you mean with --changeset <id>, or record the choice with `git pair change use <id>`",
		ErrAmbiguousChangeset, strings.Join(ids, " and "))
}

// Where the integration branch came from. Reported in `status --json`, because a run that
// misreports what has landed is the expensive failure mode of this rule, and the output
// should be explainable on its own rather than from what the machine happened to fetch.
const (
	DefaultBranchFlag          = "flag"
	DefaultBranchRemoteHead    = "origin-head"
	DefaultBranchSoleCandidate = "sole-candidate"
)

// DefaultBranchRef is the integration branch and how this process learned it.
type DefaultBranchRef struct {
	// Ref is a git revision expression, always fully qualified when detected so that a
	// branch and a remote-tracking ref of the same name cannot be confused.
	Ref string
	// Source is one of the DefaultBranch* constants.
	Source string
}

// LocalName is the integration branch without its refs/heads/ prefix, which is what belongs
// in `base:` and in a message. The fully qualified ref is what comparing two commits wants;
// in CHANGESET.yaml it is noise.
func (d DefaultBranchRef) LocalName() string {
	return strings.TrimPrefix(d.Ref, "refs/heads/")
}

// BaseName is the integration branch as `base:` should record it: the branch name, without the root this
// clone happens to keep it under. `refs/remotes/origin/main` is the same branch read through the fetch root,
// and `DefaultBranch` reaches it first whenever there is no local `main` to find — which is the ordinary
// state of a clone that has fetched and not branched off trunk. A changeset file is read by other machines
// and printed in `status`; a base spelled as a fetch ref reads as a different destination there, and says
// "fetched" about a change that has never been pushed. Read-time resolution is unchanged: a name is tried
// under `refs/heads/` first and then under `refs/remotes/`, so the two spellings resolve to one branch.
func (d DefaultBranchRef) BaseName() string {
	if rest, ok := strings.CutPrefix(d.Ref, "refs/remotes/"); ok {
		if _, tail, ok := strings.Cut(rest, "/"); ok {
			return tail
		}
	}
	return d.LocalName()
}

// IsBranch reports whether a name means the integration branch, whichever root this clone happens to
// hold it under.
//
// It exists because `base:` is written as a name and `DefaultBranch` answers with a ref, and the two
// meet in the question "is this changeset measured against trunk, or stacked on a branch?". In a clone
// that has fetched and not branched off trunk — the ordinary state — the integration branch is only
// `refs/remotes/origin/main`, so a comparison of `main` to that ref says "stacked on a branch called
// main", which `status` prints as a parent line and `change integrate` refuses over: an unlanded parent
// is a refusal, and trunk is never a stack base. So every spelling of the same branch counts:
// the ref as found, the name with its root removed, and that name under `refs/heads/`.
//
// A branch really named `origin/main` would answer true here and be mistaken for trunk. That is the
// trade, and it is the one `BaseName` already makes for `base:` and for `status`.
func (d DefaultBranchRef) IsBranch(name string) bool {
	if d.Ref == "" || name == "" {
		return false
	}
	base := d.BaseName()
	return name == d.Ref || name == base || name == "refs/heads/"+base
}

// DefaultBranch resolves the integration branch.
//
// A caller-supplied ref wins outright; it is what CI passes, and it matches how
// the caller names the destination it merged into rather than git-pair storing where a landing went.
// Otherwise git's own answer is used: `git clone` records the remote's default branch in
// `refs/remotes/origin/HEAD`, so a human clone needs no configuration at all. A CI job
// built with `init`, `remote add` and a fetch of one branch does not have it, which is why
// the refusal names both `--default-branch` and `git remote set-head origin --auto`.
//
// No git config is read. The product reads none today, and a per-clone setting would let
// two machines holding the same commits disagree about what has landed.
func DefaultBranch(ctx context.Context, repo *git.Repo, override string) (DefaultBranchRef, error) {
	if override != "" {
		if _, err := repo.RevParse(ctx, override); err != nil {
			return DefaultBranchRef{}, fmt.Errorf("integration branch %q does not resolve: %w", override, err)
		}
		return DefaultBranchRef{Ref: override, Source: DefaultBranchFlag}, nil
	}

	// One listing answers every question below: what `refs/remotes/origin/HEAD` points at, and
	// which of main and master exist under `refs/remotes/origin` and under `refs/heads`. Asking
	// them one at a time cost up to seven git subprocesses on every command that resolves a
	// base, and each of them is a process that does not answer anything else.
	//
	// git drops an unresolvable symbolic ref from the listing, which is the same answer the
	// `rev-parse` this replaces reached when it followed a pointer to a branch that had since
	// been deleted: a default branch that does not resolve is no default, and the search goes on.
	refs, err := repo.ListRefs(ctx, "refs/remotes/origin", "refs/heads")
	if err != nil {
		// The probes this replaced each swallowed their own failure, so a repository git would
		// not talk to answered "no integration branch" rather than the git error. It still does:
		// callers branch on ErrNoDefaultBranch, and they would not recognise this one.
		refs = nil
	}

	if target := refs["refs/remotes/origin/HEAD"]; target != "" {
		return DefaultBranchRef{Ref: target, Source: DefaultBranchRemoteHead}, nil
	}

	// A remote carrying both main and master is a genuine coin flip, so it is refused rather
	// than guessed. Locally, `main` wins over `master` the way `init`'s base default
	// has always decided it: a repository holding both almost certainly means main, and a
	// second rule there would only make the same repository answer differently depending on
	// which command asked.
	var remote []string
	for _, ref := range []string{"refs/remotes/origin/main", "refs/remotes/origin/master"} {
		if _, ok := refs[ref]; ok {
			remote = append(remote, ref)
		}
	}
	switch len(remote) {
	case 1:
		return DefaultBranchRef{Ref: remote[0], Source: DefaultBranchSoleCandidate}, nil
	case 0:
	default:
		return DefaultBranchRef{}, fmt.Errorf("%w: %s and %s both exist and nothing says which is the integration branch; pass --default-branch <ref>",
			ErrNoDefaultBranch, remote[0], remote[1])
	}

	for _, ref := range []string{"refs/heads/main", "refs/heads/master"} {
		if _, ok := refs[ref]; ok {
			return DefaultBranchRef{Ref: ref, Source: DefaultBranchSoleCandidate}, nil
		}
	}

	// The common case is a CI job, and it reads like a repository problem when it is a checkout
	// problem: a job that fetched one branch has no integration branch to compare against, which says
	// nothing about the changeset it was asked about. Naming the fetch is what sends the reader to the
	// step that can fix it, rather than to someone's `change ready`.
	return DefaultBranchRef{}, fmt.Errorf("%w: no main or master branch found. A job that fetched one branch has nothing to compare against — fetch the default branch too (`git fetch origin '<branch>:refs/remotes/origin/<branch>'`), pass --default-branch <ref>, or run `git remote set-head origin --auto` to record the remote's default",
		ErrNoDefaultBranch)
}

// Candidate is one changeset directory this revision carries.
type Candidate struct {
	Changeset Changeset
	// Distance is the number of commits between the commit that added this changeset's own
	// directory to this line and the resolved revision: 0 when the revision itself is that
	// commit. -1 means this line never added the directory, which sorts after every real
	// distance rather than competing with it.
	//
	// It is the tie-break for candidates that the records did not separate, and it is read only
	// when more than one candidate is still standing. Creation is used rather than the last edit
	// because a directory this branch is not working on gets edited too: a commit to a parent's
	// ABOUT.md on a child branch is not a statement about which work is live.
	Distance int
	// Ignores lists the ids this changeset declares it is merely sharing a branch with.
	Ignores []string
}

// Resolution is what the rule says about one revision.
type Resolution struct {
	DefaultBranch DefaultBranchRef
	// Candidates is every changeset directory the revision carries that the integration
	// branch does not have, ordered with the most recently added first.
	Candidates []Candidate
	// Selected is the candidate this revision is working on: nil when there is none, and
	// only meaningfully different from Candidates[0] when callers want the answer without
	// re-reading the list.
	Selected *Candidate
	// Ambiguous is true when more than one candidate survived and nothing in the durable
	// data could order them.
	Ambiguous bool
}

// Resolve answers which changeset the revision rev is working on.
func Resolve(ctx context.Context, repo *git.Repo, rev string, db DefaultBranchRef) (Resolution, error) {
	r, err := newResolver(ctx, repo, db)
	if err != nil {
		return Resolution{DefaultBranch: db}, err
	}
	return r.at(ctx, repo, rev)
}

// resolver holds what every revision in one command compares against: the directories the
// integration branch has. It is the same for every branch in the repository, so a command that asks
// about many revisions builds one of these instead of listing the same tree once per branch.
//
// `onTrunk` is built from LandedIDs, so a directory the destination carries under `changesets/.landed/`
// counts as landed exactly as one it carries under `changesets/` does. That is the question the field
// answers — "is this id already on the destination" — and answering it from one spelling alone would
// offer a tidied changeset as work in progress on every branch that still carries its directory.
type resolver struct {
	db      DefaultBranchRef
	onTrunk map[string]bool
}

func newResolver(ctx context.Context, repo *git.Repo, db DefaultBranchRef) (*resolver, error) {
	landed, err := LandedIDs(ctx, repo, db.Ref)
	if err != nil {
		return nil, err
	}
	onTrunk := map[string]bool{}
	for _, id := range landed {
		onTrunk[id] = true
	}
	return &resolver{db: db, onTrunk: onTrunk}, nil
}

// trunkIDs returns the destination's directories in a stable order, for callers reporting them.
// Both spellings are in here, so a report built from it cannot name a landed changeset as work.
func (r *resolver) trunkIDs() []string {
	out := make([]string, 0, len(r.onTrunk))
	for id := range r.onTrunk {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (r *resolver) at(ctx context.Context, repo *git.Repo, rev string) (Resolution, error) {
	res := Resolution{DefaultBranch: r.db}
	revSHA, err := repo.RevParse(ctx, rev)
	if err != nil {
		return res, err
	}

	// One tree listing plus one batch read: the ids come from the paths and the metadata
	// from the objects those paths name, so the cost follows the directories on this
	// revision rather than the directories that exist.
	entries, err := repo.TreeEntries(ctx, revSHA, Root+"/")
	if err != nil {
		return res, err
	}
	oids := map[string]string{}
	var order []string
	for _, e := range entries {
		id, ok := changesetDir(e.Path)
		if !ok || r.onTrunk[id] {
			continue
		}
		if _, seen := oids[id]; !seen {
			order = append(order, id)
		}
		oids[id] = e.OID
	}
	sort.Strings(order)

	blobs, err := repo.CatFileBlobs(ctx, mapValues(oids))
	if err != nil {
		return res, err
	}
	mdFor := map[string]map[string]string{}
	for id, oid := range oids {
		md, err := parseMetadata(blobs[oid])
		if err != nil {
			return res, fmt.Errorf("%s: %w", filepath.Join(Root, id, MetadataFile), err)
		}
		mdFor[id] = md
	}

	var candidates []Candidate
	for _, id := range order {
		c, err := candidateFor(id, mdFor[id])
		if err != nil {
			return res, err
		}
		candidates = append(candidates, c)
	}
	if err := applyBases(ctx, repo, candidates, r.db, revSHA); err != nil {
		return res, err
	}
	res.Candidates = candidates
	return choose(ctx, repo, revSHA, res)
}

// ResolveCurrent resolves the checked-out revision.
//
// It adds what the working tree holds and HEAD does not, which is the difference that makes
// `init` usable: it scaffolds a changeset directory and leaves it for the author to
// commit, and `status` has to answer about it in between. The trees are still what decide
// everything — a directory removed from HEAD is not a candidate however it sits on disk — so
// this is an addition of uncommitted work, not a second rule.
func ResolveCurrent(ctx context.Context, repo *git.Repo, defaultBranchOverride string) (Resolution, error) {
	db, err := DefaultBranch(ctx, repo, defaultBranchOverride)
	if err != nil {
		return Resolution{}, err
	}
	return ResolveCurrentOn(ctx, repo, db)
}

// ResolveCurrentOn is ResolveCurrent for a process that resolved the integration branch already.
//
// It exists so a command that reports the comparison can hold on to what it compared against, rather
// than resolving it twice and risking two different answers in one run — which for "has this landed?"
// would mean an output that cannot be explained from itself.
func ResolveCurrentOn(ctx context.Context, repo *git.Repo, db DefaultBranchRef) (Resolution, error) {
	r, err := newResolver(ctx, repo, db)
	if err != nil {
		return Resolution{}, err
	}
	res, err := r.at(ctx, repo, "HEAD")
	if err != nil {
		return res, err
	}
	known := map[string]bool{}
	for _, c := range res.Candidates {
		known[c.Changeset.Slug] = true
	}
	for id := range r.onTrunk {
		known[id] = true
	}

	dirs, err := worktreeDirs(repo)
	if err != nil {
		return res, err
	}
	var fresh []string
	for _, id := range dirs {
		if !known[id] {
			fresh = append(fresh, id)
		}
	}
	if len(fresh) == 0 {
		return res, nil
	}
	head, err := repo.RevParse(ctx, "HEAD")
	if err != nil {
		return res, err
	}
	added := append([]Candidate{}, res.Candidates...)
	for _, id := range fresh {
		md, err := readMetadata(filepath.Join(repo.Dir, Root, id, MetadataFile))
		if err != nil {
			return res, err
		}
		c, err := candidateFor(id, md)
		if err != nil {
			return res, err
		}
		added = append(added, c)
	}
	res.Candidates = added
	return choose(ctx, repo, head, res)
}

// BranchResolution is one local branch and what the rule says about it. A branch that fails to
// resolve is a result with Err set rather than a failed scan: one branch with a broken
// CHANGESET.yaml is not a reason to stop answering about the others.
type BranchResolution struct {
	Branch     string
	Resolution Resolution
	Err        error
}

// Scan is one pass over the repository: every local branch and what the rule says about it, plus
// the changeset directories the destination branch carries.
type Scan struct {
	DefaultBranch DefaultBranchRef
	Branches      []BranchResolution
	// TrunkIDs names the changeset directories present in the destination branch, sorted, in either
	// spelling: `changesets/<id>/` and `changesets/.landed/<id>/`. They are landed work whether or not
	// anyone wrote a record — the tree rule (PRD §12) makes a directory the destination carries no claim
	// on anything — which is why the listing travels with the scan instead of being read again by whoever
	// asks "has anything landed unrecorded?".
	TrunkIDs []string
}

// ScanBranches resolves every local branch against the integration branch. It is the enumeration
// `queue` wants, and the one `--changeset <id>` filters: the branches carrying work are the ones
// with a candidate, and a changeset with no branch behind it is a record rather than work, which is
// where `status --changeset` goes looking instead.
//
// The trunk listing is taken once for the whole scan and returned with the answers, so the per-branch
// cost is a tree listing and one batch read — plus two calls per candidate on the rare branch that
// carries more than one unlanded changeset and has to order them.
func ScanBranches(ctx context.Context, repo *git.Repo, db DefaultBranchRef) (Scan, error) {
	branches, err := localBranches(ctx, repo)
	if err != nil {
		return Scan{}, err
	}
	r, err := newResolver(ctx, repo, db)
	if err != nil {
		return Scan{}, err
	}
	out := Scan{DefaultBranch: db, TrunkIDs: r.trunkIDs()}
	out.Branches = make([]BranchResolution, 0, len(branches))
	for _, b := range branches {
		res, err := r.at(ctx, repo, "refs/heads/"+b)
		out.Branches = append(out.Branches, BranchResolution{Branch: b, Resolution: res, Err: err})
	}
	return out, nil
}

// candidateFor turns one directory's metadata into a candidate.
func candidateFor(id string, md map[string]string) (Candidate, error) {
	if id2 := md["id"]; id2 != "" && id2 != id {
		return Candidate{}, fmt.Errorf("%w: %s records id %q but sits in %q; the directory name is the id, so rename the directory or correct %s",
			ErrIDMismatch, filepath.Join(Root, id, MetadataFile), id2, id, MetadataFile)
	}
	stack, err := stackOf(md)
	if err != nil {
		return Candidate{}, fmt.Errorf("%s: %w", filepath.Join(Root, id, MetadataFile), err)
	}
	return Candidate{
		Changeset: Changeset{
			Slug:            id,
			Base:            stack.Base,
			ParentBranch:    stack.Parent,
			ParentChangeset: stack.ParentChangeset,
			Dir:             filepath.Join(Root, id),
			Exists:          true,
		},
		Distance: -1,
		Ignores:  strings.Fields(md[IgnoresKey]),
	}, nil
}

// choose orders the candidates and decides whether the answer is one of them.
//
// Two filters run before any ordering, and they answer different questions. A stacked changeset
// names its parent in `base:`, so the parent's directory being present does not make it the work in
// hand. `ignores:` removes the changesets this one has declared it is only sharing a branch with -
// the recorded answer to an ambiguity `change use` was asked about.
//
// The ordering is last, and it is the only part that asks git anything. A revision carrying one
// candidate has nothing to order, so no history read happens: on the ordinary branch this function
// answers from the two records and returns.
func choose(ctx context.Context, repo *git.Repo, rev string, res Resolution) (Resolution, error) {
	res, contradicted := filterCandidates(res)
	if contradicted {
		// The declarations contradict each other, and every one of them is kept rather than dropped.
		// Which one the author meant is not in the files, and the age of a directory is not the
		// author's intent, so this is the point to refuse rather than to order.
		res.Selected = nil
		res.Ambiguous = true
		return res, nil
	}
	if len(res.Candidates) > 1 {
		if err := rankByCreation(ctx, repo, rev, res.Candidates); err != nil {
			return res, err
		}
	}
	return decide(res), nil
}

// filterCandidates removes what the two records say cannot be the work in hand. Two passes, and the
// order between them is a rule: `ignores:` is the author saying which changeset this branch is
// working on, so it is read first - a pass that removes the changeset which wrote the declaration
// leaves the declaration unread, and the candidates that remain get an answer invented for them. The
// stack link is an inference from a recorded value, and an inference outranks nothing.
func filterCandidates(res Resolution) (Resolution, bool) {
	var contradicted bool
	res.Candidates, contradicted = dropNamedKeeping(res.Candidates, func(c Candidate) []string { return c.Ignores })
	kept, contradictedLink := dropNamedKeeping(res.Candidates, func(c Candidate) []string {
		return []string{stackParentID(c)}
	})
	res.Candidates = kept
	return res, contradicted || contradictedLink
}

// decide orders what the filters left and answers it. Sorting costs nothing, so it runs here on
// whatever Distance holds: filled in by rankByCreation when the caller had several candidates to
// order, and still -1 each when the filters left one, where the order changes nothing.
func decide(res Resolution) Resolution {
	sortCandidates(res.Candidates)
	res.Selected = nil
	res.Ambiguous = false
	if len(res.Candidates) == 0 {
		return res
	}
	res.Selected = &res.Candidates[0]
	if len(res.Candidates) > 1 && equalDistance(res.Candidates[0], res.Candidates[1]) {
		res.Ambiguous = true
		res.Selected = nil
	}
	return res
}

// WithIgnores reports what this resolution would say if the named candidate recorded that it is
// merely sharing the branch with ids — the write `change use` is about to make, evaluated first.
//
// A decision that would leave the branch undecided is not a decision, and `change use` finds
// that out before committing rather than leaving the author with a file to edit back. The
// simulation runs the real filter, so a record another candidate left behind is weighed exactly
// as the next resolution will weigh it.
func (r Resolution) WithIgnores(id string, ignores []string) Resolution {
	res := r
	res.Candidates = make([]Candidate, len(r.Candidates))
	copy(res.Candidates, r.Candidates)
	for i := range res.Candidates {
		if res.Candidates[i].Changeset.Slug == id {
			res.Candidates[i].Ignores = append([]string(nil), ignores...)
		}
	}
	res, contradicted := filterCandidates(res)
	if contradicted {
		res.Selected = nil
		res.Ambiguous = true
		return res
	}
	return decide(res)
}

// rankByCreation fills in the tie-break for candidates that the records did not separate. The
// caller asks only when more than one candidate survived the filters, so the walk back to each add
// commit happens on the branches where something has to be ordered and nowhere else.
func rankByCreation(ctx context.Context, repo *git.Repo, rev string, candidates []Candidate) error {
	for i := range candidates {
		d, err := distanceFromAdd(ctx, repo, rev, candidates[i].Changeset.Slug)
		if err != nil {
			return err
		}
		candidates[i].Distance = d
	}
	return nil
}

// distanceFromAdd counts the commits between the commit that added this changeset's directory to
// this line and the revision, and answers whether this line added it at all.
//
// Creation is asked about rather than the last edit because the last edit is not evidence about
// which work is live: a child branch carries its ancestors' directories, and a commit to a parent's
// ABOUT.md from there is housekeeping, not a decision to work on the parent. The add commit is also
// stable - it does not move as the branch is worked on - and it is history, so a fresh clone that
// holds the branch measures the same thing. What git is asked for is the newest commit adding
// something under the directory, which is the right answer after a rebase or a cherry-pick moved the
// original, and after a directory was deleted and created again.
func distanceFromAdd(ctx context.Context, repo *git.Repo, rev, id string) (int, error) {
	added, err := repo.Git(ctx, "log", "-1", "--format=%H", "--diff-filter=A", rev, "--", filepath.Join(Root, id))
	if err != nil {
		return -1, err
	}
	added = strings.TrimSpace(added)
	if added == "" {
		// The directory is in the revision's tree, so some commit created it; an empty answer
		// means this line reaches it only through a graft or a shallow boundary. Absence is the
		// honest answer, and it ties with other absences rather than inventing an order.
		return -1, nil
	}
	out, err := repo.Git(ctx, "rev-list", "--count", added+".."+rev)
	if err != nil {
		return -1, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return -1, fmt.Errorf("counting commits from %s to %s: %w", added, rev, err)
	}
	return n, nil
}

// parentID reads the changeset id out of a base value. A stacked base is either the parent's id or
// the branch that carries it — `booking` or `refs/heads/booking` — and both mean the same parent, so
// the rule reads the id out of either rather than depending on which spelling `init` was
// handed.
func parentID(base string) string {
	return strings.TrimPrefix(base, "refs/heads/")
}

// stackParentID names the branch a candidate is stacked on, whether the stack recorded it as a
// branch or the resolver redirected the measurement base to the parent's landing commit.
func stackParentID(c Candidate) string {
	if c.Changeset.ParentBranch != "" {
		return c.Changeset.ParentBranch
	}
	return parentID(c.Changeset.Base)
}

// applyBases sets each stacked candidate's measurement base to the answer `BaseFor` gives, which is the
// point where the durable record used to sit.
//
// Without a derived base a stacked child whose parent has landed answers nothing at all, or worse, answers
// wrongly: every command measures against the base, and a base that names a branch the landing never moved
// reads the landed work as still sitting under the child — a child rebased onto trunk after a merge landing
// prints trunk's commits as its own work. `BaseFor` derives the answer from the destination instead: the
// parent branch while it stands, the run this branch shares with the destination once it does not, and the
// destination itself as the last resort. Nothing is invented: the branch name stays in `ParentBranch`, so a
// child can still be told its parent has landed rather than merely moved, and `BaseWhy` travels with the
// value so a surface printing a SHA can say which rule produced it.
//
// Whether an approval survives the move is not decided here. PRD §21 makes that a question about content,
// and the content comparison belongs where the approval is read (`internal/cli/stacked.go`), which has a
// head to compare against and this resolver does not.
func applyBases(ctx context.Context, repo *git.Repo, candidates []Candidate, db DefaultBranchRef, head string) error {
	for i, c := range candidates {
		if c.Changeset.ParentBranch == "" && c.Changeset.ParentChangeset == "" {
			continue
		}
		b, err := BaseFor(ctx, repo, c.Changeset, head, db)
		if errors.Is(err, ErrNoDefaultBranch) {
			// No destination to measure against, so the recorded base stays: that is the answer this clone is
			// able to give, and `status` names the half of the relationship it is missing.
			continue
		}
		if err != nil {
			return err
		}
		candidates[i].Changeset.Base = b.Ref
		candidates[i].Changeset.BaseWhy = b.Why
		candidates[i].Changeset.BaseDerived = b.Derived
	}
	return nil
}

// dropNamed removes the candidates that another candidate names, keeping the list untouched
// if that would empty it.
//
// The second half matters: `ignores:` is a decision one changeset records about another, and
// if the two point at each other — which a hand-edited file can easily do — an unguarded
// filter would report "no changeset here" for a branch that visibly has two.
// dropNamedKeeping removes the candidates another candidate names, and reports when the removal
// would have emptied the set. An empty answer would mean "this branch carries nothing", which is
// false: what happened is that the records contradict each other, and the candidates stay for the
// author to read.
func dropNamedKeeping(candidates []Candidate, named func(Candidate) []string) ([]Candidate, bool) {
	drop := map[string]bool{}
	for _, c := range candidates {
		for _, id := range named(c) {
			if id != "" && id != c.Changeset.Slug {
				drop[id] = true
			}
		}
	}
	if len(drop) == 0 {
		return candidates, false
	}
	kept := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if !drop[c.Changeset.Slug] {
			kept = append(kept, c)
		}
	}
	if len(kept) == 0 {
		return candidates, true
	}
	return kept, false
}

func sortCandidates(candidates []Candidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i].Distance, candidates[j].Distance
		if a < 0 {
			a = int(^uint(0) >> 1)
		}
		if b < 0 {
			b = int(^uint(0) >> 1)
		}
		if a != b {
			return a < b
		}
		return candidates[i].Changeset.Slug < candidates[j].Changeset.Slug
	})
}

// equalDistance reports whether the top of the ordering is undecided. Absence (-1) equals absence,
// so two candidates that no commit on this line has touched tie rather than being split by the order
// `ls-tree` happens to list them in — which is what decides a branch onto which two changeset
// directories were dropped by one merge. Absence does not equal a distance, so a changeset this
// branch has actually worked on beats one it is only carrying.
func equalDistance(a, b Candidate) bool {
	return a.Distance == b.Distance
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
