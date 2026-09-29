package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
	"gitpair/internal/reviewref"
)

// A changeset directory in the destination branch with no integration record is work that landed and
// lost its paper trail. Nothing else in git-pair says so. The tree rule (PRD §12) makes a directory the
// destination carries stop being a claim, so the changeset leaves `status` and `queue` at the
// moment it becomes most worth remembering, and the command that would have caught it — `integration
// record` — is the one step the integration contract (PRD §22) allows to be skipped: the merge happens
// outside git-pair, so nothing in this tool sees it.
//
// The detector is the reason that contract is enforceable rather than hoped for, and it is the cheapest
// thing in the design that protects the durable-memory goal: directory present in the destination, no
// integration ref ⇒ landed, unrecorded. It gets its own heading in `queue` rather than a line among the
// skip notes, because the finding is not "nothing to do here" — it is "someone stopped one command
// early, and the history is only in the merge commit until they come back".
//
// The destination branch is the one the queue can name without guessing, which is also where the loss is
// silent: a landing on a release branch still shows its branch, its markers, and its own refusals.

// unrecordedDisplayCap bounds the printed list. A repository with fifty unrecorded landings has a
// workflow problem that a fifty-line queue will not fix, and the queue is also a notification surface —
// so it prints a counted sample and `--json` carries the whole answer.
const unrecordedDisplayCap = 10

// refIndex is one read of the durable namespace, indexed by changeset.
//
// It exists because three questions in `queue` are questions about the same refs — has this changeset
// landed, does this orphan have a chain to read, which landings carry no record — and each of them used
// to ask git separately, once per changeset. One `for-each-ref` answers all three, and the count of git
// invocations a queue makes stops following the number of changesets a repository has ever had.
type refIndex struct {
	// Archive holds the changesets whose unsquashed chain this clone can read.
	Archive map[string]string
	// Integrated holds the changesets whose landing this clone has a record of, and the commit each
	// record names. Only the two families are records: a ref under the namespace that is not one of them —
	// a stray, or the retired `refs/git-pair/changesets/<id>/integration` the pre-two-ref code wrote —
	// records nothing here, and a landing written down only there is reported as unrecorded.
	Integrated map[string]string
	// IntegratedRef is the ref that holds each of those records, keyed the same way. It is in the index
	// because a surface that names a ref has to name the one that exists rather than rebuild a path from
	// the changeset id, which is the rule the whole namespace lives by.
	IntegratedRef map[string]string
	// NamespaceEmpty says this clone holds no ref of any kind under `refs/git-pair/`. That is a fact about
	// the fetch, not about any changeset (PRD §13.4), and it is deliberately not "holds no durable ref of
	// ours": a namespace holding only a retired-layout ref, or only a stray, has been fetched, and a report
	// saying it never was points the reader at a fetch that cannot help them. It belongs in the index
	// because the read that produced the answers already knows it — a caller that printed a fetch hint per
	// changeset would be
	// blaming the work for a clone.
	NamespaceEmpty bool
}

func indexDurableRefs(ctx context.Context, repo *git.Repo) (refIndex, error) {
	idx := refIndex{
		Archive:       map[string]string{},
		Integrated:    map[string]string{},
		IntegratedRef: map[string]string{},
	}
	entries, err := reviewref.List(ctx, repo)
	if err != nil {
		return idx, err
	}
	idx.NamespaceEmpty = len(entries) == 0
	for _, e := range entries {
		switch e.Kind {
		case reviewref.KindArchive:
			idx.Archive[e.ID] = e.SHA
		case reviewref.KindIntegration:
			idx.Integrated[e.ID] = e.SHA
			idx.IntegratedRef[e.ID] = e.Ref
		}
	}
	return idx, nil
}

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
// The two ways to be in this state are worth separating, because they are different questions to ask of
// the history. A chain that carries no verdict means the work reached the branch without a review, or the
// verdict was rewritten away. A landing that carried no ancestry — a squash, a cherry-pick — means the
// review may have happened and nothing in the destination kept it: PRD §13 states that limit, and the
// reason line here names it instead of leaving the reader to guess which of the two they are looking at.
type unreviewedLanding struct {
	Changeset string `json:"changeset"`
	// Commit is the landing commit, shortened the way every other surface shortens a SHA it prints.
	Commit string `json:"commit"`
	// Chain is the range the verdict was looked for in, `base..head`, empty when there was none to read.
	Chain string `json:"chain"`
	// Reason says what the chain said, in the reader's terms rather than the derivation's.
	Reason string `json:"reason"`
}

// unreviewedLandings asks, of each directory the integration branch carries, whether the chain behind it
// carries a permitting verdict.
//
// Cost: one chain derivation and one lifecycle walk per landed changeset, which follows the landings the
// destination holds rather than the branches this clone has. It is the price of the finding being a fact
// about the destination — a clone that has fetched nothing but trunk gives the same answer, which is the
// whole argument for reading the tree. A read that fails is skipped rather than reported: a destination
// this clone cannot walk is not a finding about the work in it.
func (a *app) unreviewedLandings(ctx context.Context, repo *git.Repo, trunk changeset.DefaultBranchRef,
	ids []string) []unreviewedLanding {
	found := []unreviewedLanding{}
	if trunk.Ref == "" {
		return found
	}
	for _, id := range ids {
		chain, err := changeset.LandedChain(ctx, repo, trunk.Ref, id)
		if err != nil {
			continue
		}
		summary, err := lifecycle.Summarize(ctx, repo, id, chain.Base, chain.Head)
		if err != nil {
			continue
		}
		verdict := integrationVerdict(summary)
		if verdict != nil && verdict.Outcome == model.OutcomeApprove {
			continue
		}
		found = append(found, unreviewedLanding{
			Changeset: id,
			Commit:    short(chain.Landing),
			Chain:     chainRange(chain),
			Reason:    unreviewedReason(chain, verdict),
		})
	}
	return found
}

// chainRange names the range the verdict was looked for in, so a reader can go and look. An unreadable
// chain prints nothing, which is the difference between "we looked here" and "there was nowhere to look".
func chainRange(c changeset.Chain) string {
	if c.Base == "" || c.Head == "" || c.Squash {
		return ""
	}
	return short(c.Base) + ".." + short(c.Head)
}

// unreviewedReason is the sentence that tells the reader which of the two findings this is.
func unreviewedReason(c changeset.Chain, verdict *lifecycle.Event) string {
	if c.Squash {
		return "the landing carried the directory in one commit, so no review markers came with it"
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

// landingView is what `status` reports about a changeset that has reached the integration branch: the
// commit the directory arrived in, the run of work that came with it, and whether that run carries a
// permitting verdict. Every field is read out of the destination, so a clone with no branches and no
// git-pair refs in it answers the question the same way the machine that did the merge does — which is the
// property the durable refs were invented to provide and never could, since a ref is only as current as
// the last fetch.
//
// `reviewed` is the field a reader will ask about, and it is the one with a limit worth stating in its own
// terms: it says whether the chain the destination carries holds an approval, not whether the work was
// ever approved. A squash or a cherry-pick brings the tree and leaves the history behind, and then the
// honest answer is false. `chain_base` and `chain_head` are empty in exactly that case, so the two fields
// together distinguish "no verdict" from "nothing to read".
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
	verdict := integrationVerdict(summary)
	out.Reviewed = verdict != nil && verdict.Outcome == model.OutcomeApprove
	return out, nil
}

// printUnreviewed writes the queue's own section for the finding. It gets a heading rather than a line
// among the skip notes because the finding is not "nothing to do here": work reached trunk that nobody
// approved, and the reader has to decide what that means. There is no command to print, which is the
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
