// Resolution of "which changeset is this revision working on".
//
// The rule is a content test, not a pointer: the changesets on a revision are the
// `changesets/<id>/` directories present in its tree and absent from the integration
// branch's tree. A directory that has reached the integration branch is landed work, so it
// drops out without needing an integration ref and without depending on whether the landing
// was a merge, a squash or a cherry-pick. A directory that exists only here is work in
// progress, whichever branch line it sits on — which is what lets a parent branch and the
// child branched off it resolve to the same changeset instead of one of them inventing a
// second answer (PRD §4, §7).
//
// Two properties are worth keeping in mind when changing this file:
//
//   - Nothing here asks which branch is checked out. Resolution reads trees, so a detached
//     HEAD, a CI checkout and a human's branch all answer the same question. Branch names
//     are not part of the durable data model.
//   - The cost is bounded by the changesets on this revision, not by how many changesets have
//     ever existed. The formulation this replaces needed an ancestry test per archive ref,
//     which measured 1,802 git invocations for one resolution at 300 refs; this one measures
//     five or six with the same refs present.
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
	"gitpair/internal/model"
	"gitpair/internal/reviewref"
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

// DefaultBranch resolves the integration branch.
//
// A caller-supplied ref wins outright; it is what CI passes, and it matches how
// `integration record` takes `--target` rather than storing where a landing went.
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

	if full, err := repo.Git(ctx, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil {
		if ref := strings.TrimSpace(full); ref != "" {
			if _, err := repo.RevParse(ctx, ref); err == nil {
				return DefaultBranchRef{Ref: ref, Source: DefaultBranchRemoteHead}, nil
			}
		}
	}

	// A remote carrying both main and master is a genuine coin flip, so it is refused rather
	// than guessed. Locally, `main` wins over `master` the way `change init`'s base default
	// has always decided it: a repository holding both almost certainly means main, and a
	// second rule there would only make the same repository answer differently depending on
	// which command asked.
	var remote []string
	for _, ref := range []string{"refs/remotes/origin/main", "refs/remotes/origin/master"} {
		if _, err := repo.RevParse(ctx, ref); err == nil {
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
		if _, err := repo.RevParse(ctx, ref); err == nil {
			return DefaultBranchRef{Ref: ref, Source: DefaultBranchSoleCandidate}, nil
		}
	}

	return DefaultBranchRef{}, fmt.Errorf("%w: no main or master branch found; pass --default-branch <ref>, or run `git remote set-head origin --auto` to record the remote's default",
		ErrNoDefaultBranch)
}

// Candidate is one changeset directory this revision carries.
type Candidate struct {
	Changeset Changeset
	// Review is the commit the changeset's review ref points at, "" when it has never had
	// one. It is an anchor for history, not an oracle for state.
	Review string
	// Distance is the number of commits between the review ref and the resolved revision.
	// -1 means there is no review ref, which sorts after every real distance rather than
	// competing with it.
	Distance int
	// Terminal is true when the review ref's own commit carries a terminal marker, i.e. the
	// changeset was abandoned. The directory is still in the tree, which is why this is a
	// fact reported beside the candidate rather than a reason to drop it.
	Terminal bool
	// Ignores lists the ids this changeset declares it is merely sharing a branch with.
	Ignores []string
}

// Resolution is what the rule says about one revision.
type Resolution struct {
	DefaultBranch DefaultBranchRef
	// Candidates is every changeset directory the revision carries that the integration
	// branch does not have, ordered by nearness to the revision.
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
// integration branch has, and where each changeset's review ref points. Both are the same for
// every branch in the repository, so a command that asks about many revisions builds one of
// these instead of re-listing the same two things once per branch.
type resolver struct {
	db      DefaultBranchRef
	onTrunk map[string]bool
	tips    map[string]string
}

func newResolver(ctx context.Context, repo *git.Repo, db DefaultBranchRef) (*resolver, error) {
	landed, err := DirsAt(ctx, repo, db.Ref)
	if err != nil {
		return nil, err
	}
	onTrunk := map[string]bool{}
	for _, id := range landed {
		onTrunk[id] = true
	}
	tips, err := reviewTips(ctx, repo)
	if err != nil {
		return nil, err
	}
	return &resolver{db: db, onTrunk: onTrunk, tips: tips}, nil
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
		c, err := candidateFor(ctx, repo, revSHA, id, mdFor[id], r.tips)
		if err != nil {
			return res, err
		}
		candidates = append(candidates, c)
	}
	res.Candidates = candidates
	return choose(res), nil
}

// ResolveCurrent resolves the checked-out revision.
//
// It adds what the working tree holds and HEAD does not, which is the difference that makes
// `change init` usable: it scaffolds a changeset directory and leaves it for the author to
// commit, and `status` has to answer about it in between. The trees are still what decide
// everything — a directory removed from HEAD is not a candidate however it sits on disk — so
// this is an addition of uncommitted work, not a second rule.
func ResolveCurrent(ctx context.Context, repo *git.Repo, defaultBranchOverride string) (Resolution, error) {
	db, err := DefaultBranch(ctx, repo, defaultBranchOverride)
	if err != nil {
		return Resolution{}, err
	}
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
		c, err := candidateFor(ctx, repo, head, id, md, r.tips)
		if err != nil {
			return res, err
		}
		added = append(added, c)
	}
	res.Candidates = added
	return choose(res), nil
}

// BranchResolution is one local branch and what the rule says about it. A branch that fails to
// resolve is a result with Err set rather than a failed scan: one branch with a broken
// CHANGESET.yaml is not a reason to stop answering about the others.
type BranchResolution struct {
	Branch     string
	Resolution Resolution
	Err        error
}

// BranchResolutions resolves every local branch against the integration branch. It is the
// enumeration `queue` wants, and the one `--changeset <id>` filters: the branches carrying work
// are the ones with a candidate, and a changeset with no branch behind it is reported from its
// ref instead of invented here.
//
// The trunk listing and the ref listing are taken once for the whole scan, so the per-branch
// cost is a tree listing, one batch read, and a distance for whichever candidate has a ref.
// Archive refs with no branch behind them are deliberately not included: with refs created at
// `change init`, a branchless ref is as likely an abandoned attempt as a deleted branch.
func BranchResolutions(ctx context.Context, repo *git.Repo, db DefaultBranchRef) ([]BranchResolution, error) {
	branches, err := localBranches(ctx, repo)
	if err != nil {
		return nil, err
	}
	r, err := newResolver(ctx, repo, db)
	if err != nil {
		return nil, err
	}
	out := make([]BranchResolution, 0, len(branches))
	for _, b := range branches {
		res, err := r.at(ctx, repo, "refs/heads/"+b)
		out = append(out, BranchResolution{Branch: b, Resolution: res, Err: err})
	}
	return out, nil
}

// candidateFor turns one directory's metadata into a candidate.
func candidateFor(ctx context.Context, repo *git.Repo, rev, id string, md map[string]string, tips map[string]string) (Candidate, error) {
	if id2 := md["id"]; id2 != "" && id2 != id {
		return Candidate{}, fmt.Errorf("%w: %s records id %q but sits in %q; the directory name is the id, so rename the directory or correct %s",
			ErrIDMismatch, filepath.Join(Root, id, MetadataFile), id2, id, MetadataFile)
	}
	c := Candidate{
		Changeset: Changeset{
			Slug:   id,
			Base:   md["base"],
			Dir:    filepath.Join(Root, id),
			Exists: true,
		},
		Distance: -1,
		Ignores:  strings.Fields(md[IgnoresKey]),
	}
	tip, ok := tips[id]
	if !ok {
		return c, nil
	}
	c.Review = tip
	d, err := distance(ctx, repo, tip, rev)
	if err != nil {
		return c, err
	}
	c.Distance = d
	terminal, err := refIsTerminal(ctx, repo, tip)
	if err != nil {
		return c, err
	}
	c.Terminal = terminal
	return c, nil
}

// choose orders the candidates and decides whether the answer is one of them.
//
// Two filters run before the ordering, and they answer different questions. A stacked changeset
// names its parent in `base:`, so the parent's directory being present does not make it the work
// in hand. `ignores:` then removes the changesets this one has declared it is only sharing a
// branch with — the recorded answer to an ambiguity `change use` was asked about.
func choose(res Resolution) Resolution {
	res.Candidates = dropNamed(res.Candidates, func(c Candidate) []string {
		return []string{parentID(c.Changeset.Base)}
	})
	res.Candidates = dropNamed(res.Candidates, func(c Candidate) []string { return c.Ignores })

	sortCandidates(res.Candidates)
	res.Selected = nil
	res.Ambiguous = false
	if len(res.Candidates) > 0 {
		res.Selected = &res.Candidates[0]
		if len(res.Candidates) > 1 && equalDistance(res.Candidates[0], res.Candidates[1]) {
			res.Ambiguous = true
			res.Selected = nil
		}
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
	return choose(res)
}

// reviewTips maps changeset id to the commit its movable review ref points at, in one call.
func reviewTips(ctx context.Context, repo *git.Repo) (map[string]string, error) {
	entries, err := reviewref.List(ctx, repo)
	if err != nil {
		return nil, err
	}
	tips := map[string]string{}
	for _, e := range entries {
		tips[e.Slug] = e.SHA
	}
	return tips, nil
}

// distance counts the commits between a ref's target and the revision, and answers whether
// that target is on this line of history at the same time.
//
// `git rev-list --count <tip>..<rev>` is 0 exactly when the tip is an ancestor of, or equal
// to, the revision: if it is not an ancestor then the revision itself is counted. So one
// call gives the ordering the rule needs without a separate ancestry test.
func distance(ctx context.Context, repo *git.Repo, tip, rev string) (int, error) {
	out, err := repo.Git(ctx, "rev-list", "--count", tip+".."+rev)
	if err != nil {
		return -1, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return -1, fmt.Errorf("counting commits from %s to %s: %w", tip, rev, err)
	}
	return n, nil
}

// refIsTerminal asks the commit the ref points at, which is where `change abandon` writes its
// marker: the ending is the commit the ref names, so no walk is needed.
func refIsTerminal(ctx context.Context, repo *git.Repo, sha string) (bool, error) {
	msg, err := repo.Git(ctx, "show", "-s", "--format=%B", sha)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(msg, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || key != model.TrailerState {
			continue
		}
		if strings.TrimSpace(value) == model.StateValueAbandoned {
			return true, nil
		}
	}
	return false, nil
}

// parentID reads the changeset id out of a base value. A stacked base is either the parent's
// id or the ref that names it — `booking`, `refs/heads/booking`, or
// `refs/git-pair/changesets/booking/archive` — and all mean the same parent, so the rule reads
// the id out of any of them rather than depending on which spelling `change init` was handed.
func parentID(base string) string {
	if base == "" {
		return ""
	}
	if id, ok := reviewref.ArchivedChangesetID(base); ok {
		return id
	}
	return strings.TrimPrefix(base, "refs/heads/")
}

// dropNamed removes the candidates that another candidate names, keeping the list untouched
// if that would empty it.
//
// The second half matters: `ignores:` is a decision one changeset records about another, and
// if the two point at each other — which a hand-edited file can easily do — an unguarded
// filter would report "no changeset here" for a branch that visibly has two.
func dropNamed(candidates []Candidate, named func(Candidate) []string) []Candidate {
	drop := map[string]bool{}
	for _, c := range candidates {
		for _, id := range named(c) {
			if id != "" && id != c.Changeset.Slug {
				drop[id] = true
			}
		}
	}
	if len(drop) == 0 {
		return candidates
	}
	kept := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		if !drop[c.Changeset.Slug] {
			kept = append(kept, c)
		}
	}
	if len(kept) == 0 {
		return candidates
	}
	return kept
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

// equalDistance reports whether the top of the ordering is undecided. One comparison does the
// work because of how the sentinel is set: absence (-1) equals absence, so two candidates that
// were never archived tie rather than being split by the order `ls-tree` happens to list them
// in — which is what decides a fresh clone, where there are no refs at all. Absence does not
// equal a distance, so a candidate that has been sent for review beats one that never has.
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
