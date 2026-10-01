package changeset

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"gitpair/internal/factcache"
	"gitpair/internal/git"
)

// ErrNoChain says the destination carries no readable chain for this changeset: the directory is not
// in its tree, so the changeset has not landed and there is no run of commits to read.
var ErrNoChain = errors.New("no landed chain")

// Chain is the run of commits on the destination's first-parent line that carries one changeset's
// directory, which is what the review record becomes once the branch that made it is gone.
//
// The two landing shapes are read differently, and the difference is worth naming because it decides how
// much of the record survives:
//
//   - A merge landing keeps its chain off the destination's own line. The landing commit's second parent
//     is the branch tip, and everything under it down to the merge base — implementation commits, review
//     submissions, verdict trailers, thread files — is in the object database and reachable from trunk.
//     That range is exact: it cannot include trunk's later commits, because they are not under the second
//     parent.
//   - A linear landing — fast-forward, rebase, squash — leaves no landing commit at all. Trunk's tip
//     simply moves to the branch tip, and nothing in the tree records where the branch stopped and trunk's
//     own history began. The run therefore extends to the newest commit that still carries the directory.
//     Markers are scoped by changeset id, so the reading stays correct; what is imprecise is the endpoint,
//     and `Linear` says so rather than letting a printed range look exact. It is also why `Head` cannot be
//     pulled back to `Landing`: the branch's own commits — its work, its ready marker, its review
//     submissions, most of which change no file and so leave no trace in a path listing — sit *after* the
//     commit that brought the directory, and a range that stopped at the landing would report a reviewed
//     fast-forward landing as a changeset nobody looked at.
type Chain struct {
	// Landing is the commit that brought the directory onto the destination's first-parent line: the
	// newest commit where either pathspec changed, which is the merge for a merge landing and the commit
	// that added the directory for a linear one.
	Landing string
	// Base is what the chain sits on — the merge base of a merge landing's parents, or the landing
	// commit's own parent. It is the range start for `lifecycle.Summarize`.
	Base string
	// Head is the newest commit in the run: a merge landing's second parent, or the newest commit on the
	// destination's line that still carries the directory.
	Head string
	// Merged says the chain is under the landing commit's second parent.
	Merged bool
	// Linear says the chain is on the destination's own line, which is the shape a fast-forward, a rebase
	// merge, or a squash leaves. It is not a claim that the run is imprecise — right after the landing the
	// endpoint is exact — it is the permission to ask why a later commit is in the range.
	Linear bool
	// ArrivedInOneCommit says the record reached the destination in one commit that is not a merge, which
	// is the shape a squash leaves: the directory came, and none of the run's history came with it.
	//
	// It is a statement about the arrival, and about nothing else. It stays true as the destination moves on,
	// which is what the surfaces that report a landing need: a fact about the shape of the landing must not
	// expire because somebody else committed to trunk. It is also true of a fast-forward landing whose
	// record happened to be committed once, whose run then continued in commits that touched other files —
	// and nothing in the destination's tree separates those two arrivals, because the difference is which
	// commits after the landing belonged to the branch. So this field never stands alone: a reporting
	// surface pairs it with whether the chain carries any markers for the changeset, which is the half that
	// tells a squashed review from a run that was never reviewed. See `carriedNoReviewRecord` in
	// `internal/cli/landed.go`, which is where the two are joined.
	ArrivedInOneCommit bool
	// Moved says the directory reached the destination at `changesets/.landed/<id>/` rather than
	// `changesets/<id>/`, which is what a tidied changeset looks like.
	Moved bool
}

// LandedChain derives the chain a landed changeset has on its destination.
//
// It costs one `rev-list`, one `rev-list --parents`, one `merge-base` in the merge case, and up to two
// tree presence checks. It reads only the destination's own history, so a fresh clone with no refs and no
// fetch of anything but the destination answers the same question as a clone that has the whole repository.
//
// The destination is resolved to a commit before anything is read, and every read below goes through that
// commit rather than the ref it came in as. That is what makes the answer cacheable: the chain a changeset
// has behind one commit never changes, and a destination that has moved since is a different commit and so
// a different cache key. Resolving first also makes the ref name cheap — one `rev-parse` for the whole
// derivation instead of git resolving it again for each read.
//
// The answer is ErrNoChain when the destination does not carry the directory. That is the case the message
// in `status --changeset` has to explain rather than paper over: a changeset whose branch is gone and whose
// destination never carried the directory had only its branch as a record, and there is nothing to invent.
func LandedChain(ctx context.Context, repo *git.Repo, trunkRef, id string) (Chain, error) {
	tip, err := repo.RevParse(ctx, trunkRef)
	if err != nil {
		if git.IsUnknownRevision(err) {
			return Chain{}, fmt.Errorf("%w for %s: %s is not a revision this clone can read", ErrNoChain, id, trunkRef)
		}
		return Chain{}, err
	}
	specs := DirPathspecs(id)
	cacheKey := factcache.Key("chain", tip, id)
	var cached Chain
	if repo.Facts().Get(cacheKey, &cached) {
		return cached, nil
	}
	// One `rev-list` over the two pathspecs returns the commits on the first-parent line where either one
	// changed, newest first. There are few of them per changeset — the commit that added the directory,
	// and any later move of it — which is what makes the walk below cheap.
	changing, err := pathChanges(ctx, repo, tip, specs)
	if err != nil {
		if git.IsUnknownRevision(err) {
			return Chain{}, fmt.Errorf("%w for %s: %s is not a revision this clone can read", ErrNoChain, id, trunkRef)
		}
		return Chain{}, err
	}
	if len(changing) == 0 {
		return Chain{}, fmt.Errorf("%w for %s: %s carries neither %s nor %s",
			ErrNoChain, id, trunkRef, specs[0], specs[1])
	}

	atTip, moved := CarriesDir(ctx, repo, tip, id)
	if !atTip {
		// The newest change to either path removed it rather than bringing it in, so the destination does
		// not carry the changeset. `LandedIDs` reads the same tree and says the same thing.
		return Chain{}, fmt.Errorf("%w for %s: %s carries neither %s nor %s at its tip",
			ErrNoChain, id, trunkRef, specs[0], specs[1])
	}
	ch := Chain{Landing: changing[0], Head: tip, Linear: true, Moved: moved}

	// The newest change is not always the landing: a tidying move changed the path too, and its parent
	// already carried the directory. The landing is the newest change whose parent did not.
	for _, c := range changing {
		if has, _ := CarriesDir(ctx, repo, c+"^", id); has {
			continue
		}
		ch.Landing = c
		break
	}
	landing := ch.Landing

	parents, err := parentsOf(ctx, repo, landing)
	if err != nil {
		return Chain{}, err
	}
	if parents >= 2 {
		// The merge case, and the exact one. The chain is the branch that was merged, so it is under the
		// second parent, and the range cannot widen into trunk's later commits.
		second := landing + "^2"
		if has, _ := CarriesDir(ctx, repo, second, id); has {
			base, err := repo.Git(ctx, "merge-base", landing+"^1", second)
			if err != nil {
				return Chain{}, err
			}
			head, err := repo.RevParse(ctx, second)
			if err != nil {
				return Chain{}, err
			}
			ch.Base = strings.TrimSpace(base)
			ch.Head = head
			ch.Merged = true
			ch.Linear = false
			repo.Facts().Put(cacheKey, ch)
			return ch, nil
		}
		// A merge that brought the directory in on its first parent — a conflict resolved in the merge
		// itself, or a merge of a branch that had already been folded in — has no second-parent chain to
		// read. The linear answer below is then the honest one: the run is on the destination's line.
	}

	// The linear case. `landing` added the directory on this line, so the run starts at its parent.
	base, err := repo.RevParse(ctx, landing+"^")
	if err != nil && !errors.Is(err, git.ErrUnknownRevision) {
		return Chain{}, err
	}
	if base == "" {
		// The repository's first commit carried the directory: the run is the whole line, and there is no
		// parent to name as its start. `lifecycle.Summarize` refuses an empty base, so it gets git's empty
		// tree, which is what a parentless commit diffs against.
		base = git.EmptyTree
	}
	ch.Base = base
	// The arrival's shape is a property of the landing commit, not of where the destination's tip happens
	// to be. Asking `Head == landing` instead was the old test, and it was the same fact with an expiry
	// date: it held only while the landing was trunk's newest commit, so a squash landing's reading — and
	// with it the sentence that says the review did not come with the record — quietly became "the chain
	// carries no review verdict" the first time an unrelated commit landed. A merge commit is not a one-commit
	// arrival either, which covers the first-parent merge above: the directory came with a second parent, even
	// though the run below is read off this line.
	ch.ArrivedInOneCommit = parents == 1
	repo.Facts().Put(cacheKey, ch)
	return ch, nil
}

// LandingCommit names the commit that brought a changeset's directory onto a revision's first-parent
// line, or "" when the revision does not carry it.
//
// It is the one question `status`'s stack walk asks per ancestor, and it is answered with two git calls:
// one `rev-list` over the directory's two pathspecs, and one parent check for the case where the newest
// change to those paths is a tidying move rather than the arrival. `LandedChain` answers this too, and
// answers more — the range, its shape, whether it is under a merge's second parent — which the walk does
// not ask and would pay for on every step.
//
// It answers only for a revision that carries the directory at its tip. A revision that carried it and
// dropped it is not a landing, and the caller is told so by the empty answer rather than by an error: the
// stack walk reads ancestors that may be live work, and a missing directory there is not a failure.
func LandingCommit(ctx context.Context, repo *git.Repo, rev, id string) string {
	changing, err := pathChanges(ctx, repo, rev, DirPathspecs(id))
	if err != nil || len(changing) == 0 {
		return ""
	}
	if has, _ := CarriesDir(ctx, repo, changing[0]+"^", id); has {
		// The newest change to either path only moved the directory; the arrival is the next one down.
		if len(changing) < 2 {
			return ""
		}
		return changing[1]
	}
	return changing[0]
}

// pathChanges lists the commits on a revision's first-parent line where any of the given paths changed,
// newest first. It is the cheap boundary: the count follows the times a directory arrived, moved, or left,
// not the length of the branch.
func pathChanges(ctx context.Context, repo *git.Repo, rev string, specs []string) ([]string, error) {
	args := append([]string{"rev-list", "--first-parent", rev, "--"}, specs...)
	out, err := repo.Git(ctx, args...)
	if err != nil {
		return nil, err
	}
	var shas []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			shas = append(shas, line)
		}
	}
	return shas, nil
}

// CarriesDir reports whether one revision's tree holds a changeset's directory, and whether it holds it
// under the landed spelling. It is the cheapest question the tree model answers — two `rev-parse --verify`
// calls at most, with the active path tried first because that is where a directory usually is — and it is
// exported because the write gate asks it directly: "may this changeset still be written to?" is the same
// question as "is it on the destination?".
func CarriesDir(ctx context.Context, repo *git.Repo, rev, id string) (present, moved bool) {
	if repo.PathExistsAt(ctx, rev, ActiveDirPath(id)) {
		return true, false
	}
	if repo.PathExistsAt(ctx, rev, LandedDirPath(id)) {
		return true, true
	}
	return false, false
}

// parentsOf counts the parents of one commit. One `rev-list --parents` per chain read, because a shape
// test that shells out per parent is a shape test that gets skipped for cost.
func parentsOf(ctx context.Context, repo *git.Repo, sha string) (int, error) {
	out, err := repo.Git(ctx, "rev-list", "-n", "1", "--parents", sha)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) == 0 {
		return 0, fmt.Errorf("git returned no parents for %s", sha)
	}
	return len(fields) - 1, nil
}

// ChainIDsFor lists the ids whose chain a caller can read on one destination, for a surface that reports
// each landed changeset. It is LandedIDs minus the ones whose derivation fails, which in practice is
// nothing: presence in the tree is the same test the derivation ends with.
func ChainIDsFor(ctx context.Context, repo *git.Repo, trunkRef string) ([]string, error) {
	ids, err := LandedIDs(ctx, repo, trunkRef)
	if err != nil {
		return nil, err
	}
	readable := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := LandedChain(ctx, repo, trunkRef, id); err != nil {
			if errors.Is(err, ErrNoChain) {
				continue
			}
			return nil, err
		}
		readable = append(readable, id)
	}
	sort.Strings(readable)
	return readable, nil
}
