package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gitpair/internal/changeset"
	"gitpair/internal/factcache"
	"gitpair/internal/git"
	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
)

// A landed changeset whose chain carries no permitting verdict is work that reached the destination
// without a review that permitted it. Nothing else in git-pair says so. The tree rule (PRD §12) makes a
// directory the destination carries stop being a claim, so the changeset leaves `status` and `queue` at
// the moment it becomes most worth looking at, and the merge itself happens outside git-pair — nothing in
// this tool sees it happen.
//
// The detector is the cheapest thing in the design that protects the reviewer: the directory is in the
// destination, and the run of history behind it carries no approve. It gets its own heading in `queue`
// rather than a line among the skip notes, because the finding is not "nothing to do here" — it is
// "what is on the destination branch was never approved", and no command closes it.
//
// The destination branch is the one the queue can name without guessing, which is also where the gap is
// silent: a landing on a release branch still shows its branch, its markers, and its own refusals.

// unreviewedDisplayCap bounds the printed list. A repository with fifty landed changesets nobody
// approved has a workflow problem a fifty-line queue will not fix, and the queue is also a notification
// surface — so it prints a counted sample and `--json` carries the whole answer.
const unreviewedDisplayCap = 10

// unreviewedLanding is work that reached the integration branch with no permitting verdict in the chain
// the destination carries. It is the finding that survived deleting the durable refs, and it is a
// different finding from the one it replaces. `LANDED, UNRECORDED` reported that a command had not been
// run — a fact about git-pair's own paper trail, closable by running the command it printed. This one
// reports that the reviewers never approved what is now in trunk, which no command closes.
//
// The ways to be in this state are worth separating, because they are different questions to ask of the
// history, and the reason line names which one the reader is looking at rather than leaving them to guess.
//
// A chain that carries no verdict means the work reached the branch without a review, or the verdict was
// rewritten away. A landing that carried no ancestry — a squash, a cherry-pick — means the review may have
// happened and nothing in the destination kept it. And a chain that carries an approval whose commit the
// destination does not hold means the approval was about commits that did not arrive: the run was replayed
// between the approval and the landing, so what main holds is not what was approved. The last of the three
// is what keeps `reviewed` from meaning "somebody approved this at some point", and it is the same test
// `check` runs against the branch, which is why the two surfaces cannot disagree.
type unreviewedLanding struct {
	Changeset string `json:"changeset"`
	// Commit is the landing commit, shortened the way every other surface shortens a SHA it prints.
	Commit string `json:"commit"`
	// Chain is the range the verdict was looked for in, `base..head`, empty when there was none to read.
	Chain string `json:"chain"`
	// Reason says what the chain said, in the reader's terms rather than the derivation's.
	Reason string `json:"reason"`
}

// unreviewedLandings asks, of each directory the integration branch carries, whether the destination holds
// an approval covering what it now holds.
//
// Cost: one chain derivation, one lifecycle walk and one `merge-base --is-ancestor` per landed changeset,
// which follows the landings the destination holds rather than the branches this clone has. It is the price of the finding being a fact
// about the destination — a clone that has fetched nothing but trunk gives the same answer, which is the
// whole argument for reading the tree. A read that fails is skipped rather than reported: a destination
// this clone cannot walk is not a finding about the work in it.
//
// The per-changeset work is cached as one record against the destination's tip, which is what keeps this
// from being the command's whole cost as a repository accumulates landings. Everything the record holds is
// derived from that one commit and from the chain behind it, so it cannot go stale: a destination that has
// moved since is a different commit, which is a different key, which asks git again. The record is the
// answer to this one question rather than the intermediate reads, because asking it as one question is what
// turns ten git invocations into none.
func (a *app) unreviewedLandings(ctx context.Context, repo *git.Repo, trunk changeset.DefaultBranchRef,
	ids []string) []unreviewedLanding {
	found := []unreviewedLanding{}
	if trunk.Ref == "" {
		return found
	}
	// The key is the commit the destination points at, not the ref it was named by: `refs/remotes/origin/main`
	// and `refs/heads/main` are the same branch read twice, and an answer about one is an answer about the
	// other. A destination this clone cannot resolve has no answers either, so the loop below still runs and
	// simply caches nothing.
	tip, tipErr := repo.RevParse(ctx, trunk.Ref)
	for _, id := range ids {
		key := ""
		if tipErr == nil {
			key = factcache.Key("unreviewed", tip, id)
			var hit unreviewedRecord
			if repo.Facts().Get(key, &hit) {
				if !hit.Licensed {
					found = append(found, hit.landing(id))
				}
				continue
			}
		}
		record, ok := a.unreviewedRecord(ctx, repo, trunk, id)
		if !ok {
			continue
		}
		// Only a record that was actually derived is written. An unreadable destination is not an answer
		// about the work in it — it is this clone failing to walk its own history — and keeping it would pin
		// a transient read failure against this commit for as long as the cache lives. It costs almost nothing
		// to re-derive: every id in `ids` is a directory the destination carries, so the unreadable case is
		// the rare one, and it is the one where the derivation was cheap anyway.
		if key != "" {
			repo.Facts().Put(key, record)
		}
		if record.Licensed {
			continue
		}
		found = append(found, record.landing(id))
	}
	return found
}

// unreviewedRecord is one changeset's answer, in the form this detector wants, kept against the destination
// commit it was derived from. Only the rendered fields are stored, because that is all the caller produces
// from them: an entry written by a build that renders the sentence differently is an entry the `format`
// check in factcache turns into a miss.
type unreviewedRecord struct {
	// Licensed says the destination holds an approval of what it holds, so there is nothing to report.
	// It is the field that makes the cache pay: a repository whose landings were all reviewed is the
	// repository with the most landed directories, and each of them used to cost a full derivation to say
	// nothing.
	Licensed bool   `json:"licensed"`
	Commit   string `json:"commit"`
	Chain    string `json:"chain"`
	Reason   string `json:"reason"`
}

// landing renders the record as the row the two surfaces print. The changeset id is not in the record: it
// is part of the cache key, and storing it would let a file written under one id report itself under
// another.
func (r unreviewedRecord) landing(id string) unreviewedLanding {
	return unreviewedLanding{Changeset: id, Commit: r.Commit, Chain: r.Chain, Reason: r.Reason}
}

// unreviewedRecord derives one changeset's answer. ok is false when the destination carries no readable
// record for it — the case the caller prints nothing for, and the case that is never cached.
func (a *app) unreviewedRecord(ctx context.Context, repo *git.Repo, trunk changeset.DefaultBranchRef,
	id string) (unreviewedRecord, bool) {
	chain, err := changeset.LandedChain(ctx, repo, trunk.Ref, id)
	if err != nil {
		return unreviewedRecord{}, false
	}
	summary, err := lifecycle.Summarize(ctx, repo, id, chain.Base, chain.Head)
	if err != nil {
		return unreviewedRecord{}, false
	}
	licensed, reason := a.landingLicence(ctx, repo, trunk.Ref, chain, integrationVerdict(summary))
	return unreviewedRecord{
		Licensed: licensed,
		Commit:   short(chain.Landing),
		Chain:    chainRange(chain),
		Reason:   reason,
	}, true
}

// chainRange names the range the verdict was looked for in, so a reader can go and look. An unreadable
// chain prints nothing, which is the difference between "we looked here" and "there was nowhere to look".
func chainRange(c changeset.Chain) string {
	if c.Base == "" || c.Head == "" || c.Squash {
		return ""
	}
	return short(c.Base) + ".." + short(c.Head)
}

// unreviewedReason is the half of the decision that needs no git: what the chain itself said, in the
// reader's terms. Empty means the chain carries an approval, which is not yet the whole answer — see
// landingLicence, which then asks whether the destination holds the commit that approval names.
func unreviewedReason(c changeset.Chain, verdict *lifecycle.Event) string {
	if c.Squash {
		return "the landing carried the directory in one commit, so no review markers came with it"
	}
	if verdict != nil && verdict.Outcome == model.OutcomeApprove {
		return ""
	}
	switch {
	case verdict == nil:
		return "the chain carries no review verdict"
	case verdict.Outcome == model.OutcomeFeedback:
		return fmt.Sprintf("the newest verdict is feedback (%s), which does not license a merge", verdict.Short)
	case verdict.Outcome == model.OutcomeBlock:
		return fmt.Sprintf("the newest verdict is a block (%s)", verdict.Short)
	default:
		return fmt.Sprintf("the newest verdict is %s (%s), not an approval", verdict.Outcome, verdict.Short)
	}
}

// landingLicence decides whether the destination holds an approval that covers the commits it now holds.
// Both halves have to be true, and they fail for different reasons.
//
// The chain behind the directory must carry an approving verdict, which is what `unreviewedReason` decides.
// And the commit that verdict names has to be in the destination as well. An approval is a statement about
// a commit (`Review-Head`), and a landing that replayed the run — a rebase merge, or an amend or rebase the
// author made between the approval and the merge — carries the marker commits across and leaves the
// approved commits behind. The destination then holds a record of approving commits it does not have, which
// is not an approval of what it does have. The two ways that happens leave the same history behind, so no
// reading separates the merge that did the rewriting from the author who rewrote under an approval and was
// merged anyway; the strict answer covers both. An approval that names no commit cannot be checked, so it
// covers nothing either.
//
// The ancestor question is asked of the destination's tip rather than of the landing commit, because the
// landing commit is the first commit of the run that carried the directory and usually sits *behind* the
// approval it is being checked against.
//
// IsAncestor answers an error for a name this repository cannot resolve. That is taken as the same answer as
// false: a destination that does not have the commit does not license it, whatever the reason the commit is
// missing.
func (a *app) landingLicence(ctx context.Context, repo *git.Repo, dest string, chain changeset.Chain,
	verdict *lifecycle.Event) (bool, string) {
	if reason := unreviewedReason(chain, verdict); reason != "" {
		return false, reason
	}
	head := verdict.ReviewedHead
	if head == "" {
		return false, fmt.Sprintf("the approval (%s) names no commit, so the destination cannot be checked against it",
			verdict.Short)
	}
	in, err := repo.IsAncestor(ctx, head, dest)
	if err != nil {
		in = false
	}
	if !in {
		return false, fmt.Sprintf("the approval (%s) names %s, which the destination does not carry: the landing "+
			"replayed the run, so what was approved is not what landed", verdict.Short, short(head))
	}
	return true, ""
}

// landingView is what `status` reports about a changeset that has reached the integration branch: the
// commit the directory arrived in, the run of work that came with it, and whether that run carries a
// permitting verdict. Every field is read out of the destination, so a clone with no branches and no
// git-pair refs in it answers the question the same way the machine that did the merge does — which is the
// property the durable refs were invented to provide and never could, since a ref is only as current as
// the last fetch.
//
// `reviewed` is the field a reader will ask about, and it is the one with a limit worth stating in its own
// terms: it says whether the destination holds an approval of the commits it holds, not whether the work
// was ever approved. It is false when no approval came with the run — a squash or a cherry-pick brings the
// tree and leaves the history behind — and false when an approval came but names a commit the destination
// does not have, which is what a replayed run looks like. `chain_base` and `chain_head` are empty in the
// first case and not in the second, so the three fields together say which of them you are looking at.
// `state` keeps its own meaning throughout: it is what the markers in the run recorded, so a replayed
// landing can read APPROVED while `reviewed` says the approval does not cover what landed.
type landingView struct {
	Landed bool `json:"landed"`
	// Commit is the commit that put the directory on the integration branch, and Branch is that branch's
	// display name. Landing is not a marker, so it sits beside `state` for the reason `abandoned` does.
	Commit string `json:"landed_commit,omitempty"`
	Branch string `json:"landed_branch,omitempty"`
	// ChainBase and ChainHead bound the run behind the directory: the span a reviewer read, and where
	// their markers are. Empty when the landing carried no chain to bound.
	ChainBase string `json:"chain_base,omitempty"`
	ChainHead string `json:"chain_head,omitempty"`
	// Reviewed says the chain carries a permitting verdict. See the type comment for what it does not say.
	Reviewed bool `json:"reviewed"`
}

func (a *app) landingView(ctx context.Context, repo *git.Repo, trunk changeset.DefaultBranchRef,
	branchName, id string) (landingView, error) {
	out := landingView{}
	if trunk.Ref == "" {
		return out, nil
	}
	if present, _ := changeset.CarriesDir(ctx, repo, trunk.Ref, id); !present {
		return out, nil
	}
	out.Landed, out.Branch = true, branchName
	chain, err := changeset.LandedChain(ctx, repo, trunk.Ref, id)
	if errors.Is(err, changeset.ErrNoChain) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Commit = short(chain.Landing)
	if !chain.Squash {
		out.ChainBase, out.ChainHead = short(chain.Base), short(chain.Head)
	}
	summary, err := lifecycle.Summarize(ctx, repo, id, chain.Base, chain.Head)
	if err != nil {
		return out, err
	}
	licensed, _ := a.landingLicence(ctx, repo, trunk.Ref, chain, integrationVerdict(summary))
	out.Reviewed = licensed
	return out, nil
}

// printUnreviewed writes the queue's own section for the finding. It gets a heading rather than a line
// among the skip notes because the finding is not "nothing to do here": work reached trunk that trunk holds
// no approval of, whether because nobody approved it or because the approval is about commits that did not
// arrive, and the reader has to decide what that means. There is no command to print, which is the
// difference between this heading and the one it replaced — the action is to read the chain, and the
// line names the read that shows it.
func (a *app) printUnreviewed(found []unreviewedLanding, dest string, gap bool) {
	if len(found) == 0 {
		return
	}
	if gap {
		a.printf("\n")
	}
	a.printf("LANDED UNREVIEWED\n\n")
	shown := found
	if len(shown) > unreviewedDisplayCap {
		shown = shown[:unreviewedDisplayCap]
	}
	for _, u := range shown {
		a.printf("  %s\n", u.Changeset)
		if u.Chain != "" {
			a.printf("    on %s at %s, chain %s: %s\n", dest, u.Commit, u.Chain, u.Reason)
			continue
		}
		a.printf("    on %s at %s: %s\n", dest, u.Commit, u.Reason)
	}
	if extra := len(found) - len(shown); extra > 0 {
		a.printf("  and %d more (`git pair queue --json` lists every one)\n", extra)
	}
	a.printf("\n  read one with `git pair status --changeset <id>`\n\n")
}

// unreviewedInStatus is the same finding placed where `status` can put it: on the error that says this
// branch carries no changeset. That error is the right host for the same reason it was the host before —
// the destination branch has nothing to report about work in progress, and exit 2 is still the honest
// code — and what the branch *can* answer is that it holds landed work nobody approved. A reader told only
// "no changeset for this branch" goes looking for a branch they forgot rather than for a review that never
// happened. The original error stays at the front: it is still true, and it is why the exit code is 2.
func unreviewedInStatus(orig error, found []unreviewedLanding, dest string) error {
	if len(found) == 0 {
		return orig
	}
	lines := make([]string, 0, unreviewedDisplayCap)
	for i, u := range found {
		if i == unreviewedDisplayCap {
			lines = append(lines, fmt.Sprintf("  and %d more", len(found)-i))
			break
		}
		lines = append(lines, fmt.Sprintf("  %s on %s at %s: %s", u.Changeset, dest, u.Commit, u.Reason))
	}
	msg := fmt.Sprintf("%s\n\nLANDED UNREVIEWED\n\n%s in %s with no approving verdict in its chain: the work reached\n"+
		"the destination and no review in it approved what landed. Read one with\n"+
		"`git pair status --changeset <id>`:\n\n%s",
		orig, plural(len(found), "changeset directory is", "changeset directories are"), dest,
		strings.Join(lines, "\n"))
	return &usageError{fmt.Errorf("%s", msg)}
}
