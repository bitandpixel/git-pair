package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
	"gitpair/internal/reviewref"
)

// newIntegrationCommand groups the commands that speak about a changeset landing.
func newIntegrationCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "integration",
		Short: "Record where a changeset landed",
		Long: `Integration records are git-pair's durable refs, and there are exactly two per changeset, both
written by one invocation of ` + "`git pair integration record`" + `:

  refs/git-pair/archive/<changeset-id>      the unsquashed tip: implementation and review, interleaved
  refs/git-pair/integrations/<changeset-id> the commit the changeset became in the destination branch

Together they are the permanent paper trail — what was reviewed, and what it became. The link is
recorded rather than inferred: squash, rebase and cherry-pick rewrite commit identity, so no ancestry
or patch-ID reading can tell you that a commit on the destination branch is someone's reviewed work.

Neither ref ever moves. While work is in flight git-pair writes no ref at all — the branch holds the
chain, and a ref that tracked it would be a staler copy of a story the branch tells better.`,
		// Without a RunE cobra treats an unmatched subcommand as a help request and exits 0,
		// which is indistinguishable from success for an agent that typo'd the verb.
		RunE: groupUsage("integration"),
	}
	cmd.AddCommand(newIntegrationRecordCommand(a))
	cmd.AddCommand(newIntegrationPublishCommand(a))
	return cmd
}

func newIntegrationRecordCommand(a *app) *cobra.Command {
	var source, commit, target, id string
	var allowFeedback bool
	var configureFetch bool
	cmd := &cobra.Command{
		Use:   "record",
		Short: "Write the two refs that make a changeset permanent",
		Long: `Write both durable refs for one changeset, create-only:

  refs/git-pair/archive/<id>       = --source, the unsquashed tip that was reviewed
  refs/git-pair/integrations/<id>  = --commit, the commit the changeset became

They are written in that order, archive first, because the integration ref is the one whose existence
means "this changeset is finished": if the process stops between the two writes, the changeset still
reads as not-yet-recorded and the next invocation completes the pair. The reverse order could leave a
finished changeset whose chain nothing holds.

Create-only means neither ref is ever moved. Asking for the commit a ref already names is a no-op that
succeeds, so a retry after a partial write finishes the record instead of failing on the half that
already worked; asking for a different commit is refused, and the refusal names both the commit that
is recorded and the one that was asked for.

The changeset id comes from content, not from a ref: it is the ` + "`changesets/<id>/`" + ` directory
` + "`--source`" + ` carries and the destination branch does not. Name it with ` + "`--changeset`" + ` when
` + "`--source`" + ` carries more than one, which is what a stacked child does — it carries its parent's
directory too.

The two SHAs are the two you have after a landing: ` + "`--source`" + ` is the branch you were reviewing,
` + "`--commit`" + ` is the merge or squash you just made. Name neither and the repository is asked: the
landing is the newest commit on the destination's first-parent line that added ` + "`changesets/<id>/`" + `,
and the reviewed head is the branch still carrying that directory. Either flag can be named on its own, and
anything ambiguous or out of sight is a refusal naming the candidates rather than a guess — CI, which has
neither branch nor a fresh merge in its checkout, passes both.

The command takes a position rather than a checkout, writes no commit, and runs from any branch.

Before it writes, four things are verified, and each refuses rather than warns:

  1. the changeset directory in --source is the changeset the markers in --source's history name
  2. that changeset's newest verdict there permits integration — approve, or feedback with
     ` + "`--allow-feedback`" + `
  3. --commit is reachable from the destination branch: the one --target names, else the changeset's own
     ` + "`base:`" + `, else the default branch
  4. --commit is the commit that brought changesets/<id>/ into that history, not one that inherited it

The first two keep an unreviewed head out of the permanent record. The last two keep a record from
pointing at a commit that is not where the work landed: the changeset directory is committed content, so
its arrival is the fact that identifies a landing commit through merge, squash and cherry-pick alike.

git-pair performs no merge. The contract is ` + "`git pair check`" + `, then an ordinary git merge into
the base branch by whoever owns it, then this command.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runIntegrationRecord(integrationRecordInput{
				source: source, commit: commit, target: target, changeset: id,
				allowFeedback: allowFeedback, configureFetch: configureFetch,
			})
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "the unsquashed tip that was reviewed (the head `change ready` and `review submit` were writing on)")
	cmd.Flags().StringVar(&commit, "commit", "", "the commit the changeset became in the destination branch")
	cmd.Flags().StringVar(&target, "target", "", "ref the landing commit must be reachable from; defaults to the changeset's base branch")
	cmd.Flags().StringVar(&id, "changeset", "", "which changeset to record, when --source carries more than one changeset directory")
	cmd.Flags().BoolVar(&configureFetch, "configure-fetch", false,
		"also add the durable-refs mirror refspec to remote.<name>.fetch, so ordinary fetches keep this "+
			"clone able to tell published from unpublished")
	cmd.Flags().BoolVar(&allowFeedback, "allow-feedback", false, "accept a changeset whose newest verdict is non-blocking feedback, as `git pair check --allow-feedback` does")
	return cmd
}

// integrationRecordInput is what the caller supplied.
type integrationRecordInput struct {
	// configureFetch is consent to write the durable-refs fetch refspec into this clone's config. It is an
	// argument and never a question, for the reason PRD §22 gives: this CLI is the agent surface, and a
	// prompt that goes unanswered in CI reads exactly like a prompt that was declined.
	configureFetch                    bool
	source, commit, target, changeset string
	// allowFeedback is the recorder's copy of `git pair check --allow-feedback`: a changeset whose newest
	// verdict is feedback may be recorded. Without it the recorder is as strict as check's default policy,
	// which is the pair a landing deserves — the two commands a person runs one after the other must not
	// disagree about what counts as reviewed.
	allowFeedback bool
	// defaultBranch is --default-branch, threaded through so a derived destination is the same branch
	// every other command calls the destination.
	defaultBranch string
}

// integrationRecord is a verified record: everything `integration record` writes and reports.
type integrationRecord struct {
	// ID is the derived (or disambiguated) changeset id.
	ID string
	// Source is the full sha of the reviewed head the caller named.
	Source string
	// Commit is the full sha of the landing commit.
	Commit string
	// Target is the ref the landing was verified against, empty when nothing identified one.
	Target string
	// TargetDerived names where Target came from when the caller did not name one: "base" for the
	// changeset's own `base:` field, "default" for the default branch. Empty means the caller named it,
	// which is the only case where the reachability check was promised to them.
	TargetDerived string
	// Derived lists the flags git-pair filled in itself ("source", "commit"), so the answer can say
	// which parts of the record the caller named and which the repository supplied.
	Derived []string
	// FastForward says the destination's own first-parent line carries the reviewed chain, so the archive
	// and the integration name one commit — the shape a fast-forward leaves, and the reason the record can
	// be written with no flags and no branch present. It is reported rather than left for the reader to
	// infer from two refs holding the same sha, because it is also the one case where the recorder knowingly
	// names a commit that did not itself introduce the changeset directory: the commit that carries the
	// approval is the commit the work became.
	FastForward bool
}

// resolveIntegrationRecord turns the flags into the pair to write: the two commits, and which changeset
// they are about. It asks no policy questions, because runIntegrationRecord has to read the record that
// may already exist before it decides whether to verify a new one.
func resolveIntegrationRecord(ctx context.Context, repo *git.Repo, in integrationRecordInput) (*integrationRecord, error) {
	rec := &integrationRecord{}
	if in.source == "" || in.commit == "" {
		derived, err := deriveMissingTips(ctx, repo, in, rec)
		if err != nil {
			return nil, err
		}
		in = derived
	}
	if in.source == "" || in.commit == "" {
		return nil, &usageError{fmt.Errorf("--source and --commit are both required: the head that was reviewed, and the commit the work became")}
	}
	source, err := resolveCommit(ctx, repo, "--source", in.source)
	if err != nil {
		return nil, err
	}
	candidates, err := changesetsAtSource(ctx, repo, source, in, in.changeset)
	if err != nil {
		return nil, err
	}
	id, err := chooseChangeset(candidates, source, in.changeset)
	if err != nil {
		return nil, err
	}
	landing, err := resolveCommit(ctx, repo, "--commit", in.commit)
	if err != nil {
		return nil, err
	}
	rec.ID, rec.Source, rec.Commit = id, source, landing
	return rec, nil
}

// derivationWindow is how far back the landing search walks a destination branch. A landing older than
// this is a changeset someone is remembering rather than one they merged a moment ago, and the flags are
// the answer for that: a guess made from a window the caller cannot see is a worse contract than a
// refusal that names the window.
const derivationWindow = 200

// deriveMissingTips fills in whichever of the two SHAs the caller did not name, and refuses rather than
// guess.
//
// The local flow is the reason the flags are optional at all: the person who merged knows they merged and
// should not have to translate that into two object ids. The repository knows too — the landing is the
// commit that added `changesets/<id>/` to the destination's first-parent line, and the reviewed head is
// the branch still carrying the directory — so both are read from the graph rather than asked for. What
// is *not* done is choosing between two readings: an ambiguity here is a usage error naming the
// candidates, because the thing being written is a durable record and the caller is the one who knows
// which of the two they meant.
func deriveMissingTips(ctx context.Context, repo *git.Repo, in integrationRecordInput, rec *integrationRecord) (integrationRecordInput, error) {
	dests, err := derivationDestinations(ctx, repo, in)
	if err != nil {
		return in, err
	}
	id := in.changeset
	if id == "" && in.source != "" {
		// A named source names its own changesets by the directories in its tree. That reading is the
		// one the resolution below applies anyway; it runs first here because the landing search needs
		// an id.
		source, err := resolveCommit(ctx, repo, "--source", in.source)
		if err != nil {
			return in, err
		}
		candidates, err := changesetsAtSource(ctx, repo, source, in, in.changeset)
		if err != nil {
			return in, err
		}
		if id, err = chooseChangeset(candidates, source, ""); err != nil {
			return in, err
		}
	}
	if id == "" {
		if id, err = changesetToDerive(ctx, repo, dests); err != nil {
			return in, err
		}
	}
	if in.commit == "" {
		landing, err := deriveLanding(ctx, repo, dests, id)
		if err != nil {
			return in, err
		}
		in.commit = landing
		rec.Derived = append(rec.Derived, "commit")
	}
	// The pair already exists, so the rest of this function would now go looking for the branch that
	// carries the directory — the thing a tidy-up deletes — and refuse over a fact that is already on the
	// record. §22 says a retry is not a failure, and this function's own comment has always promised the CI
	// re-run this answer; it is taken here, once the landing is known, rather than after both ends have been
	// derived from assumptions the retry no longer has to satisfy.
	if recorded, err := reviewref.RecordedPair(ctx, repo, id); err == nil &&
		recorded.Archive != "" && recorded.Integration == in.commit {
		in.changeset, in.source = id, recorded.Archive
		rec.Derived = append(rec.Derived, "source")
		return in, nil
	}
	// The id this pass settled on is the id the caller is being asked about. Handing it down is not a
	// performance note: `resolveIntegrationRecord` reads the changeset out of the source tree a second
	// time, and if that second reading is allowed to re-open the choice it can refuse a question that has
	// already been answered — which is what it did, with a weaker filter, until the id was threaded.
	in.changeset = id
	if in.source == "" {
		head, derived, err := deriveSource(ctx, repo, dests, id, in, in.commit, rec)
		if err != nil {
			return in, err
		}
		in.source = head
		if rec.FastForward {
			// The destination's line carried the reviewed chain, so the commit the work became is the commit
			// that carries the approval, not the one that first added the directory.
			in.commit = head
		}
		if derived {
			rec.Derived = append(rec.Derived, "source")
		}
	}
	return in, nil
}

// deriveSource answers which commit was reviewed, from the landing the recorder already settled on.
//
// The graph answers it before any branch is consulted, because a landing names both ends: the commit that
// introduced `changesets/<id>/` over its first parent is the landing, and the side it introduced is the tip
// of the reviewed chain. That is what makes a merge landing recordable after the branch has been deleted —
// the shape §13.1's archive ref exists for, and the reason `git branch -D` after a merge stopped being a
// race with the record.
//
// A one-parent introduction is the fast-forward and the rebase-merge: the destination's own first-parent
// line carries the reviewed chain, so archive and integration name the same commit — which `CreatePair`
// already permits. That reading needs a licence the tree alone cannot give, because a squash can carry a
// marker's *text* into its own message (`parseTrailers` matches any `Review-*:` line) while it cannot carry
// the reviewed head, which is the commit a squash deliberately keeps out of trunk. So the marker has to say
// the work was reviewed *and* the ancestry has to say this commit is that work.
//
// When neither holds — no introduction on the destination's line, no permission, no reviewed-head ancestry,
// which is the squash and the cherry-pick — the answer comes from the branch that still carries the
// directory, exactly as before, and `--source` is the answer after that. The layering never invents a
// reviewed head: it changes which of the repository's own commits is offered.
func deriveSource(ctx context.Context, repo *git.Repo, dests []string, id string,
	in integrationRecordInput, landing string, rec *integrationRecord) (string, bool, error) {
	path := "changesets/" + id
	if landing != "" && repo.PathExistsAt(ctx, landing, path) {
		first, ferr := repo.RevParse(ctx, landing+"^1")
		if ferr != nil || !repo.PathExistsAt(ctx, first, path) {
			// The landing is the introduction. Two parents: the merge brought a side in, and that side is the
			// reviewed chain.
			if side, err := repo.RevParse(ctx, landing+"^2"); err == nil && repo.PathExistsAt(ctx, side, path) {
				return side, true, nil
			}
			// One parent: the destination's line may itself carry the chain, as far as a marker says it was
			// reviewed and the reviewed head is in it.
			if tip, err := ffChainTip(ctx, repo, dests, landing, id, in.allowFeedback); err != nil {
				return "", false, err
			} else if tip != "" {
				rec.FastForward = true
				return tip, true, nil
			}
		}
	}
	tip, err := deriveArchiveTip(ctx, repo, dests, id, landing)
	if err != nil {
		return "", false, err
	}
	return tip, true, nil
}

// ffChainTip is the fast-forward reading: the newest commit on a destination's own first-parent line, at or
// above the landing, that carries a permitting marker for this changeset whose `Review-Head` is an ancestor
// of it.
//
// That commit is the reviewed chain's tip inside the destination, which is what the archive names and — for
// a fast-forward — what the integration names too. The two conditions do different work: the marker says a
// human approved this changeset at that commit, and the ancestry says the commit is the approved work rather
// than a copy of it. A squash can copy a marker's text and cannot copy the reviewed head; a rebase-merge
// replays the marker but leaves the head it reviewed behind — which is the half that declines, and sends the
// caller to the branch or to `--source`.
func ffChainTip(ctx context.Context, repo *git.Repo, dests []string, landing, id string, allowFeedback bool) (string, error) {
	for _, dest := range dests {
		tip, err := repo.RevParse(ctx, dest+"^{commit}")
		if err != nil || tip == "" || tip == landing {
			continue
		}
		line, err := repo.FirstParentLine(ctx, dest, derivationWindow)
		if err != nil {
			continue
		}
		onLine := map[string]bool{}
		for _, sha := range line {
			onLine[sha] = true
		}
		if !onLine[landing] {
			continue
		}
		records, err := repo.LogFields(ctx, landing+".."+tip, "%H", "%b")
		if err != nil {
			continue
		}
		for i := len(records) - 1; i >= 0; i-- { // LogFields is oldest first; the newest verdict is the one
			// that speaks for the changeset (§10.6), and the first-parent filter is the map, because the range
			// itself also reaches commits that came in from the side.
			found := records[i]
			if len(found) < 2 || !onLine[found[0]] {
				continue
			}
			tr := lifecycle.Trailers(found[1])
			if tr[model.TrailerChangeset] != id {
				continue
			}
			outcome := tr[model.TrailerOutcome]
			permits := outcome == string(model.OutcomeApprove) ||
				(allowFeedback && outcome == string(model.OutcomeFeedback))
			if !permits {
				continue
			}
			head := tr[model.TrailerHead]
			if head == "" {
				continue
			}
			sha, err := repo.RevParse(ctx, head+"^{commit}")
			if err != nil {
				continue
			}
			if in, err := repo.IsAncestor(ctx, sha, found[0]); err == nil && in {
				return found[0], nil
			}
		}
	}
	return "", nil
}

// derivationDestinations are the branches a landing could have gone to: the --target the caller named,
// else the branch the current checkout's changeset was measured against plus the branch git-pair calls
// the default. Deduplicated by commit, because a changeset based on trunk has one destination and a
// refusal that lists it twice reads like a bug.
func derivationDestinations(ctx context.Context, repo *git.Repo, in integrationRecordInput) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(ref string) {
		if ref == "" {
			return
		}
		sha, err := repo.RevParse(ctx, ref+"^{commit}")
		if err != nil || seen[sha] {
			return
		}
		seen[sha] = true
		out = append(out, ref)
	}
	if in.target != "" {
		add(in.target)
		return out, nil
	}
	// A base under refs/git-pair/ is a measurement base, not a destination. The relink points a child at its
	// parent's record, and `--target` was never meant to name one: offering git-pair's own ref as "a branch
	// this landing could have reached" would put a durable ref in the list of places work went to, and the
	// refusal that names its candidates would read as though a record were a branch.
	if cur, err := changeset.Current(ctx, repo, in.defaultBranch); err == nil &&
		!strings.HasPrefix(cur.Base, reviewref.NamespaceRoot+"/") {
		add(cur.Base)
	}
	if db, err := changeset.DefaultBranch(ctx, repo, in.defaultBranch); err == nil {
		add(db.Ref)
	}
	return out, nil
}

// changesetToDerive is the reading for someone standing on the branch they just merged into: which
// changeset the record is for, from the directories the destination carries.
//
// Two passes, because two things are being asked. Normally the answer is the one directory with no
// integration record — that is what a landing looks like before it is recorded, and it is why a partially
// recorded repository still resolves uniquely. When every directory on the branch already has a record the
// answer is the same directory again, because this is a CI job running the command a second time: it must
// hear "already recorded" rather than a usage error it will report as a failed build. The record decides
// what the command *does* — write, complete, or no-op — and discovery decides only which changeset the
// question is about.
//
// Either way an ambiguity is a usage error naming the candidates. Choosing between two changesets is not
// something a durable record should do by itself.
// changesetToDerive is the reading for someone standing on the branch they just merged into: which
// changeset the record is for, from the directories the destination carries.
//
// The answer is the one directory whose record is not finished — that is what a landing looks like before
// it is recorded, and it is why a partially recorded repository still resolves uniquely. "Finished" is both
// halves: an archive ref without its integration ref is a half-pair, and git-pair calls that "not a record"
// wherever else it prints (§13.2), so it is listed here too, and completing it is the work.
//
// When every directory on the destinations has its pair, there is nothing to write. That is a usage error,
// not a success, for two reasons. The command could not say which changeset it meant, which is the same
// class as the refusal below it for a destination with no directories at all; and a green run that wrote
// nothing is the failure mode this command exists to prevent — a landing stays unrecorded, and the archive
// chain survives only until somebody deletes the branch. It is also the case whose wording this function's
// comment used to promise was impossible: a CI job running the command a second time "must hear `already
// recorded` rather than a usage error it will report as a failed build". That promise belongs to a run that
// identified one changeset — by `--changeset`, or by being the only candidate — and that run gets it, from
// `deriveMissingTips` reading `RecordedPair` and exiting 0 with `already_recorded`. A run that cannot name
// one is not that run, and the candidate list is not its answer either: a record is create-only, so "record
// it again" is never the finding, and nine names for nine finished records reads as nine gaps.
//
// An ambiguity between changesets that still need work stays a usage error naming the candidates. Choosing
// between two changesets is not something a durable record should do by itself.
func changesetToDerive(ctx context.Context, repo *git.Repo, dests []string) (string, error) {
	integrations, archives := map[string]bool{}, map[string]bool{}
	if entries, err := reviewref.List(ctx, repo); err == nil {
		for _, e := range entries {
			switch e.Kind {
			case reviewref.KindIntegration:
				integrations[e.ID] = true
			case reviewref.KindArchive:
				archives[e.ID] = true
			}
		}
	}
	var all, unfinished []string
	for _, dest := range dests {
		dirs, err := changeset.DirsAt(ctx, repo, dest)
		if err != nil {
			return "", err
		}
		for _, id := range dirs {
			if !slices.Contains(all, id) {
				all = append(all, id)
			}
			// A directory with neither half, or with the archive half only, is waiting for the record.
			if !integrations[id] || !archives[id] {
				if !slices.Contains(unfinished, id) {
					unfinished = append(unfinished, id)
				}
			}
		}
	}
	names := make([]string, 0, len(dests))
	for _, d := range dests {
		names = append(names, displayRef(d))
	}
	switch {
	case len(unfinished) == 1:
		return unfinished[0], nil
	case len(unfinished) == 0 && len(all) == 0:
		return "", &usageError{fmt.Errorf("no changeset directory on %s, so there is nothing here to record\n\n`git pair integration record` derives the pair from the repository: the landing is the commit that added changesets/<id>/ to %s's first-parent line, and the reviewed head is the branch still carrying the directory. Name --source and --commit to record something else — an older landing, or one on a branch git-pair was not told about",
			strings.Join(names, " or "), strings.Join(names, " or "))}
	case len(unfinished) == 0 && len(all) == 1:
		// One directory is one changeset, and a run that can name it is the identified run the comment above
		// promises the `already recorded` answer to. It is handed back and `deriveMissingTips` reads the pair
		// and exits 0 — the CI re-run of an idempotent command, which is the case §22 says is not a failure.
		return all[0], nil
	case len(unfinished) == 0:
		return "", &usageError{fmt.Errorf("nothing to record: each of the %d changeset directories on %s already has its archive and integration refs here\n\nA record is written once and never moved, so there is nothing left for this command to do on those branches. A landing that reached a branch git-pair did not search is named with --target <ref>; a record written in another clone arrives with `git fetch origin '%s'`",
			len(all), strings.Join(names, " or "), reviewref.FetchRefspec)}
	default:
		return "", &usageError{fmt.Errorf("more than one changeset on %s is missing its integration record:%s\n\nName the one this record is for with --changeset <id>",
			strings.Join(names, " or "), "\n  "+strings.Join(unfinished, "\n  "))}
	}
}

// deriveLanding is the first-parent transition rule: the newest commit on a destination's own line whose
// tree carries the changeset directory and whose first parent's does not. It is the same rule M4's
// unrecorded-landing detector will use, and the same reason it works is the reason the record is needed at
// all — the directory is committed content, so its arrival survives the squash, rebase and cherry-pick
// that destroy the ancestry.
func deriveLanding(ctx context.Context, repo *git.Repo, dests []string, id string) (string, error) {
	path := "changesets/" + id
	var names []string
	for _, dest := range dests {
		names = append(names, displayRef(dest))
		line, err := repo.FirstParentLine(ctx, dest, derivationWindow)
		if err != nil {
			return "", err
		}
		for _, sha := range line {
			if !repo.PathExistsAt(ctx, sha, path) {
				continue
			}
			if parent, err := repo.RevParse(ctx, sha+"^1"); err == nil && repo.PathExistsAt(ctx, parent, path) {
				continue
			}
			return sha, nil
		}
	}
	return "", &usageError{fmt.Errorf("no commit in the last %d on %s added changesets/%s/, so git-pair cannot see where this changeset landed\n\nPass --commit <sha> for the merge, squash or cherry-pick you made. The search stops at %d commits deliberately: guessing from a window nobody named is worse than asking, and an older landing is one you remember rather than one you just made.",
		derivationWindow, strings.Join(names, " or "), id, derivationWindow)}
}

// deriveArchiveTip is the other end: the branch still carrying the changeset directory is the one whose
// tip was reviewed. Excluding the destinations matters — after a merge landing the destination carries the
// directory too, and naming its tip as the reviewed head would record the merge commit as its own source.
//
// When no branch carries it the derivation stops and says so. That is the case the archive ref exists for
// (§13.1): with the branch deleted, nothing in this clone holds the reviewed head unless the durable refs
// were fetched, and a guess about which commit was approved is exactly what this command must not make.
// branchKey is the branch a ref names, without the spelling that reached it. `refs/heads/feat/ux` and
// `refs/remotes/origin/feat/ux` are one branch — the first is the branch in the working tree, the second is
// the same branch as of the last fetch — and the derivation asks which branch carries a changeset, not
// which path spells it. Without this, a pushed branch would be two candidates and every derivation of it
// would refuse as ambiguous for a reason nobody would believe.
//
// It answers "" for a ref naming no branch, which is what a remote's symbolic `HEAD` is: a pointer to a
// branch, not a branch.
func branchKey(ref string) string {
	if !strings.HasPrefix(ref, "refs/remotes/") {
		return displayRef(ref)
	}
	name := displayRef(ref) // "origin/feat/ux"
	_, rest, ok := strings.Cut(name, "/")
	if !ok || rest == "" || rest == "HEAD" {
		return ""
	}
	return rest
}

func deriveArchiveTip(ctx context.Context, repo *git.Repo, dests []string, id, landing string) (string, error) {
	exclude := map[string]bool{}
	excludeKey := map[string]bool{}
	for _, dest := range dests {
		if sha, err := repo.RevParse(ctx, dest+"^{commit}"); err == nil {
			exclude[sha] = true
		}
		exclude[displayRef(dest)] = true
		// …and under the other spelling of the same branch. A destination fetched into the clone is
		// still the destination: `refs/remotes/origin/main` carries the changeset directory once the
		// merge has been pushed and fetched, and offering it as the reviewed head would record a landing
		// as its own source from a spelling the caller never named.
		if k := branchKey(dest); k != "" {
			excludeKey[k] = true
		}
	}
	// The pattern is a `for-each-ref` pattern, not a shell glob: it matches path-name aware, so `*` does
	// not cross a `/`. `refs/heads/*` answers the branches whose name is one component long, which in a
	// repository that names its branches `feat/…` is no branches at all — and the derivation would report
	// that as no branch carrying the changeset. The trailing slash is the prefix form, which does see them.
	//
	// Both roots are read, and the local one first. A CI runner is handed the branch by git, which puts it
	// under `refs/remotes/origin/` and nowhere else; asking only the local branches told it that the branch
	// it was standing on did not exist. Local first is not a preference between two answers — it is the
	// order that makes `seenKey` below keep the branch in the working tree when both spellings are here.
	locals, err := repo.RefTips(ctx, "refs/heads/")
	if err != nil {
		return "", err
	}
	remotes, err := repo.RefTips(ctx, "refs/remotes/")
	if err != nil {
		return "", err
	}
	path := "changesets/" + id
	var found []string
	seenKey := map[string]bool{}
	for _, tip := range append(append([]git.RefTip{}, locals...), remotes...) {
		key := branchKey(tip.Name)
		if key == "" || seenKey[key] {
			continue
		}
		if excludeKey[key] || exclude[displayRef(tip.Name)] || exclude[tip.Commit] {
			continue
		}
		// A branch downstream of the landing is not a candidate either. After a merge the destination's own
		// line carries the directory, and every branch cut from the destination afterwards inherits it — so
		// without this a landed changeset turns every trunk-descended branch into a claim about where its
		// reviewed head is, and the derivation refuses as ambiguous in the repository that has simply been
		// kept up to date. One `merge-base --is-ancestor` per candidate.
		if landing != "" {
			if downstream, err := repo.IsAncestor(ctx, landing, tip.Commit); err == nil && downstream {
				continue
			}
		}
		if repo.PathExistsAt(ctx, tip.Commit, path) {
			seenKey[key] = true
			found = append(found, tip.Name)
		}
	}
	switch {
	case len(found) == 1:
		return found[0], nil
	case len(found) == 0:
		return "", &usageError{fmt.Errorf("no branch here carries changesets/%s/, so git-pair cannot see which head was reviewed\n\nPass --source <sha> for the branch tip the approval spoke about. If the branch has been deleted the durable refs are what holds that chain (§13.4): git fetch origin '%s'",
			id, reviewref.FetchRefspec)}
	default:
		return "", &usageError{fmt.Errorf("more than one branch carries changesets/%s:%s\n\nName the one that was reviewed with --source <ref>",
			id, "\n  "+strings.Join(found, "\n  "))}
	}
}

// verifyIntegrationRecord decides whether the record may be written, in the order that makes the refusal
// the shortest one that is true: first what the source is (a head that was never reviewed, a changeset
// that ended), then where the landing sits.
//
// Two of these deserve their reasoning at the call site rather than in a comment elsewhere. The source is
// resolved through `rev-parse` before discovery, so an abbreviated SHA pasted from a CI log finds the
// changeset rather than matching nothing. And the landing commit is checked against the changeset
// directory rather than believed: `changesets/` is committed content that travels with the change through
// merge, squash and cherry-pick, so its arrival is what identifies a landing commit when ancestry has
// been destroyed — and a release-branch record pointing at an unrelated commit fails here instead of
// quietly succeeding.
func verifyIntegrationRecord(ctx context.Context, repo *git.Repo, rec *integrationRecord, in integrationRecordInput) error {
	if err := verifyReviewedSource(ctx, repo, rec, in.allowFeedback); err != nil {
		return err
	}
	if err := verifyLandingReachable(ctx, repo, rec, in); err != nil {
		return err
	}
	if !repo.PathExistsAt(ctx, rec.Commit, "changesets/"+rec.ID) {
		return fmt.Errorf("changesets/%s/ does not exist in %s: that commit does not carry this changeset, so it cannot be where the work landed", rec.ID, short(rec.Commit))
	}
	// The transition, not just the presence. A commit whose first parent already carries the directory did
	// not bring the changeset in, so recording it as the landing would point the record at a commit after
	// the fact — a follow-up on the destination branch, or a second merge of a branch that landed earlier.
	// A root commit has no first parent, so it has nothing to have inherited from.
	//
	// The fast-forward reading is the one case exempt, and it is exempt because it was established rather
	// than assumed: the destination's own line carried the approval marker whose reviewed head is in that
	// history, so the commit the work became is the commit that carries the verdict, not the one that first
	// added the directory. The refusal stays for a follow-up commit on the destination, which is what this
	// check was written for.
	if !rec.FastForward {
		if parent, err := repo.RevParse(ctx, rec.Commit+"^1"); err == nil && repo.PathExistsAt(ctx, parent, "changesets/"+rec.ID) {
			return fmt.Errorf("%s does not add changesets/%s/ over its first parent %s: the directory was already there, so this commit is not where the work landed\n\nRecord the commit that brought changesets/%s/ into %s — a merge, a squash or a cherry-pick of the reviewed branch. `git log --first-parent %s -- changesets/%s/` lists the candidates.",
				short(rec.Commit), rec.ID, short(parent), rec.ID, displayRef(rec.Target), displayRef(rec.Target), rec.ID)
		}
	}
	return nil
}

// verifyReviewedSource is the recorder's reading of "was this reviewed", from --source's own history: the
// changeset being recorded must be the changeset the markers there speak about, and its newest verdict
// must permit integration.
//
// The two questions share one walk because they are asked of the same evidence, and the walk is the whole
// ancestry rather than `base..source` (see lifecycle.ScanLineage): after a merge landing the reviewed head
// is inside the destination branch, so a range that excludes the destination is empty precisely when a
// record is being written.
//
// Naming the changeset is checked separately from approving it because they fail differently. A source
// whose markers name a *different* changeset is the wrong commit for this record — usually a stacked
// parent recorded as though it were the child, or the other way round — and the reader has to be told
// which ids disagree. A source with no markers at all is simply unreviewed, and that is what the refusal
// says. Both refusals are the recorder keeping its own end of §21: the pair of refs is the durable answer
// to "where did this reviewed work go", and an unreviewed head written into it makes that answer wrong.
func verifyReviewedSource(ctx context.Context, repo *git.Repo, rec *integrationRecord, allowFeedback bool) error {
	scans, err := lifecycle.ScanLineage(ctx, repo, rec.Source)
	if err != nil {
		return err
	}
	var mine []lifecycle.MarkerScan
	var others []string
	for _, scan := range scans {
		_, hasOutcome := scan.Trailers[model.TrailerOutcome]
		_, hasState := scan.Trailers[model.TrailerState]
		if !hasOutcome && !hasState {
			continue // an ordinary commit that happens to carry one of our trailer names
		}
		switch name := scan.Trailers[model.TrailerChangeset]; {
		case name == rec.ID:
			mine = append(mine, scan)
		case name != "":
			if !slices.Contains(others, name) {
				others = append(others, name)
			}
		}
	}
	if len(mine) == 0 {
		if len(others) == 0 {
			return fmt.Errorf("nothing in %s's history is a git-pair marker for changeset %s, so this head has never been offered for review and cannot be recorded as integrated\n\n`git pair change ready` offers the work and `git pair review submit --approve` records the verdict; the landing follows a verdict, not the other way round",
				short(rec.Source), rec.ID)
		}
		return fmt.Errorf("no marker in %s's history names changeset %s; the markers there name %s\n\nThe record would say %s is the reviewed head of %s, and the commits in its own history disagree. Record the changeset those markers name, or record the head whose markers name this one.",
			short(rec.Source), rec.ID, strings.Join(others, ", "), short(rec.Source), rec.ID)
	}
	// Newest first, which is the only order that answers "what does this head have on the record": a
	// superseded approval is not the verdict, and the reviewer corrected it by submitting again (§10.6).
	newest := mine[0]
	outcome, hasOutcome := newest.Trailers[model.TrailerOutcome]
	state, hasState := newest.Trailers[model.TrailerState]
	if hasOutcome {
		switch model.Outcome(outcome) {
		case model.OutcomeApprove:
			return nil
		case model.OutcomeFeedback:
			if allowFeedback {
				return nil
			}
			return fmt.Errorf("the newest review of %s (%s) is feedback, which is non-blocking but is not an approval\n\nLand it after an approval, or pass --allow-feedback if that is how this pair works — the same switch `git pair check` offers", rec.ID, newest.Short)
		case model.OutcomeBlock:
			return fmt.Errorf("the newest review of %s (%s) blocks it, so %s is not a head to record as integrated\n\nAddress the review, re-offer with `git pair change ready`, and land what the next review accepts", rec.ID, newest.Short, short(rec.Source))
		default:
			return fmt.Errorf("the newest review of %s (%s) carries `Review-Outcome: %s`, which this build of git-pair cannot read\n\nAn older or hand-written marker may mean something this build would read as approval, so the record waits for a verdict git-pair can interpret", rec.ID, newest.Short, outcome)
		}
	}
	if hasState {
		if state == model.StateValueAbandoned {
			return fmt.Errorf("changeset %s was abandoned by %s, so there is nothing to integrate", rec.ID, newest.Short)
		}
		return fmt.Errorf("the newest marker for %s in %s's history is `Review-State: %s` (%s), which is an offer rather than a verdict\n\nThe verdict comes from `git pair review submit --approve`; this head has not had one. Review it, then record the landing",
			rec.ID, short(rec.Source), state, newest.Short)
	}
	return fmt.Errorf("the newest marker for %s in %s's history (%s) carries neither `Review-Outcome` nor `Review-State`, so git-pair cannot tell what it decided and will not record an integration on the strength of it",
		rec.ID, short(rec.Source), newest.Short)
}

// destination is a ref the landing commit could be verified against.
type destination struct {
	ref string
	// why records how git-pair arrived at ref, for the refusal and the output line: "base" for the
	// changeset's own `base:` field, "default" for the default branch. Empty means the caller named it.
	why string
}

// verifyLandingReachable puts the landing commit in the history of the branch it is claimed to have
// landed on.
//
// A named --target is one check against one ref: what the caller said is what the record will say. When
// the destination was not named, git-pair tries the two branches it can name on the caller's behalf, in
// the order a reader would trust them — the `base:` the changeset was measured against when the work
// started, then the default branch — and takes the first that contains the commit. Trying both rather
// than only the first is what keeps a stacked child recordable: its `base:` is its parent branch, and a
// stack merged into trunk in one go lands the child on trunk. It is not a licence to be wrong: a commit
// that is in neither history is still refused, and the refusal names what it tried.
//
// When nothing identifies a destination at all the check is not made and Target stays empty. "This
// repository cannot say where work lands" is a different fact from "the work did not land where it was
// said to", and only the second one refuses.
func verifyLandingReachable(ctx context.Context, repo *git.Repo, rec *integrationRecord, in integrationRecordInput) error {
	if in.target != "" {
		if _, err := resolveCommit(ctx, repo, "--target", in.target); err != nil {
			return err
		}
		holds, err := repo.IsAncestor(ctx, rec.Commit, in.target)
		if err != nil {
			return err
		}
		if !holds {
			return fmt.Errorf("%s is not reachable from %s: the record would say the work landed somewhere it is not",
				short(rec.Commit), displayRef(in.target))
		}
		rec.Target = in.target
		return nil
	}
	var tried []destination
	seen := map[string]bool{}
	// Deduplicated by commit rather than by spelling: a changeset whose `base:` is the default branch is
	// one destination, not two, and a refusal that says "not reachable from main or main" reads like a
	// bug in the sentence rather than a fact about the work.
	add := func(ref, why string) {
		if ref == "" {
			return
		}
		sha, err := repo.RevParse(ctx, ref+"^{commit}")
		if err != nil || seen[sha] {
			return
		}
		seen[sha] = true
		tried = append(tried, destination{ref: ref, why: why})
	}
	if base, err := changeset.BaseAt(ctx, repo, rec.Source, rec.ID); err == nil {
		add(base, "base")
	}
	if db, err := changeset.DefaultBranch(ctx, repo, in.defaultBranch); err == nil {
		add(db.Ref, "default")
	}
	for _, d := range tried {
		holds, err := repo.IsAncestor(ctx, rec.Commit, d.ref)
		if err != nil {
			return err
		}
		if holds {
			rec.Target, rec.TargetDerived = d.ref, d.why
			return nil
		}
	}
	if len(tried) == 0 {
		return nil
	}
	names := make([]string, 0, len(tried))
	for _, d := range tried {
		names = append(names, displayRef(d.ref))
	}
	return fmt.Errorf("%s is not reachable from %s: the record would say the work landed somewhere it is not\n\nNo --target was given, so the destination was %s. Landing somewhere else is allowed — a release branch is a destination too — but the record has to name it: --target <ref>",
		short(rec.Commit), strings.Join(names, " or "), unaskedDestination(tried))
}

// unaskedDestination phrases where an assumed destination came from, in the order it was tried.
func unaskedDestination(tried []destination) string {
	phrases := make([]string, 0, len(tried))
	for _, d := range tried {
		switch d.why {
		case "base":
			phrases = append(phrases, "taken from the changeset's own `base:` ("+displayRef(d.ref)+")")
		case "default":
			phrases = append(phrases, "taken from the default branch ("+displayRef(d.ref)+")")
		}
	}
	return strings.Join(phrases, ", and then ")
}

// derivedNote marks the one verification in the output that the caller did not ask for. It is short on
// purpose — the refusal is where the explanation belongs — but a log line that says "verified reachable
// from main" is worth labelling when git-pair chose main itself.
func derivedNote(rec *integrationRecord) string {
	switch rec.TargetDerived {
	case "base":
		return " (the changeset's base branch)"
	case "default":
		return " (the default branch)"
	}
	return ""
}

// changesetsAtSource reads the changeset ids --source could be a record for: the `changesets/<id>/`
// directories in its tree, minus the ones the destination branch already carries and the ones whose record
// is already written.
//
// That is PRD §4's rule read at the commit the record names, which is the only discovery that works from a
// CI checkout: it asks two trees, so it needs no ref to have been written first, no fetch of a namespace,
// and no branch to be standing around. When the destination branch cannot be identified the subtraction is
// skipped rather than guessed — a directory that has landed is still a directory this commit carries.
//
// The subtraction used to stop at "the destination carries it", and used `--default-branch` for nothing,
// which made this the weaker of the two discovery passes and let it refuse a question the first pass had
// already answered: landing is what puts a directory in the destination, so from the second landing onward a
// branch cut after the first carries two directories and the destination holds both, and the run asked the
// caller to choose between changesets that were already recorded. The recorded half is now subtracted too,
// so this pass and the destination-side one answer the same question the same way. Over-reporting was never
// safe: the flagless flow has no `--changeset` to decide between candidates, and a caller who is handed a
// choice between two finished records cannot tell that the command has nothing to do.
func changesetsAtSource(ctx context.Context, repo *git.Repo, source string, in integrationRecordInput, named string) ([]string, error) {
	here, err := changeset.DirsAt(ctx, repo, source)
	if err != nil {
		return nil, err
	}
	// The caller named one and the source carries it: that is the answer, and no filter below is allowed to
	// take it away. A re-run names the changeset whose record already exists, and dropping recorded ids from
	// the candidates would turn §22's idempotent retry into "that changeset is not here".
	if named != "" && slices.Contains(here, named) {
		return []string{named}, nil
	}
	// One candidate needs no subtraction, and the subtraction must not be allowed to empty the set:
	// recording a changeset whose landing has already reached trunk is the ordinary order of work, and
	// there the directory is in trunk precisely because this command is being asked to record it.
	if len(here) < 2 {
		return here, nil
	}
	// The destination is where a landed directory is expected. `--target` when the caller named one, and the
	// default branch otherwise — read with `--default-branch`, which this call used to pass an empty string
	// for, so a CI clone that had been told which branch is its destination got no answer and kept every
	// directory as a candidate.
	var dests []string
	if in.target != "" {
		dests = append(dests, in.target)
	} else if db, err := changeset.DefaultBranch(ctx, repo, in.defaultBranch); err == nil {
		dests = append(dests, db.Ref)
	}
	landed := map[string]bool{}
	for _, dest := range dests {
		// Both spellings of the destination are one branch, and a clone that fetched it has it under
		// `refs/remotes/` rather than `refs/heads/`. `DirsAt` reads a tree, so each spelling it can resolve
		// answers the same question.
		dirs, err := changeset.DirsAt(ctx, repo, dest)
		if err != nil {
			continue
		}
		for _, id := range dirs {
			landed[id] = true
		}
	}
	// And a directory whose record is already written is not a candidate for having one written. This is the
	// same filter the destination-side discovery applies, and the two have to agree: the flagless run and a
	// run that names `--source` are answers to one question.
	recorded := map[string]bool{}
	if entries, err := reviewref.List(ctx, repo); err == nil {
		for _, e := range entries {
			if e.Kind == reviewref.KindIntegration {
				recorded[e.ID] = true
			}
		}
	}
	var out []string
	for _, id := range here {
		if !landed[id] && !recorded[id] {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		return here, nil
	}
	return out, nil
}

// chooseChangeset turns the candidates into the one id to record. Naming it disambiguates; it never
// substitutes for a directory that is not there.
func chooseChangeset(candidates []string, source, named string) (string, error) {
	if len(candidates) == 0 {
		return "", fmt.Errorf("no changesets/<id>/ directory exists in %s, so there is nothing to record\n\nThis usually means one of:\n  the wrong commit was supplied — --source is the head that was reviewed, not the merge commit\n  the changeset directory was never committed — it is the changeset's identity, and it lands with the work",
			short(source))
	}
	if named != "" {
		for _, c := range candidates {
			if c == named {
				return named, nil
			}
		}
		return "", fmt.Errorf("changeset %s is not among the changesets %s carries (%s): --changeset picks between them, it does not replace the directory the changeset is identified by",
			named, short(source), strings.Join(candidates, ", "))
	}
	if len(candidates) > 1 {
		list := ""
		for _, c := range candidates {
			list += "\n  " + c
		}
		// Ambiguity is a usage error for the same reason it is one for every other command: the
		// repository is not broken, and the caller has to say which one they mean. A stacked child
		// carries its parent's directory, which is the case this asks about.
		return "", &usageError{fmt.Errorf("more than one changeset directory exists in %s:%s\n\nName the one you mean with --changeset <id>", short(source), list)}
	}
	return candidates[0], nil
}

// resolveCommit turns a caller-supplied revision into a full commit sha. An unresolvable revision
// is a repository fact rather than a git failure: `rev-parse --verify` says so by exiting 1 with no
// stderr, and a shallow CI clone and a typo look identical from here — so the message names both.
func resolveCommit(ctx context.Context, repo *git.Repo, flag, rev string) (string, error) {
	sha, err := repo.RevParse(ctx, rev+"^{commit}")
	if err != nil {
		// `rev-parse --verify --quiet` reports an absent revision by exiting 1 with no stderr, and
		// git surfaces that as ErrUnknownRevision rather than as a git failure. A shallow CI clone
		// and a typo look identical from here, so the message names both possibilities instead of
		// making the reader guess which one they have.
		if errors.Is(err, git.ErrUnknownRevision) {
			return "", fmt.Errorf("%s %s is not a commit this repository has; a shallow clone or a wrong sha both look like this", flag, rev)
		}
		return "", err
	}
	return sha, nil
}

// integrationRecordJSON is the machine-readable answer. Both SHAs are full, because the thing a
// pipeline does with them is compare against the SHA it already holds. `recorded` says this call wrote at
// least one ref, and `already_recorded` says both already named this exact pair — a retry is a success,
// and the two successes are worth telling apart in a log. `target` is empty when nothing identified a
// destination and so no reachability check was made; `target_derived` says git-pair chose it (the
// changeset's `base:`, else the default branch) rather than the caller naming it.
type integrationRecordJSON struct {
	Changeset     string   `json:"changeset"`
	Source        string   `json:"source"`
	Commit        string   `json:"commit"`
	Target        string   `json:"target"`
	TargetDerived bool     `json:"target_derived,omitempty"`
	Derived       []string `json:"derived,omitempty"`
	// FastForward says the pair names one commit, because the destination's own first-parent line carries
	// the reviewed chain — the fast-forward and the rebase-merge shape. Reported rather than left to be
	// inferred from two equal shas: the equality is also what a half-written pair can look like.
	FastForward    bool   `json:"fast_forward,omitempty"`
	ArchiveRef     string `json:"archive_ref"`
	IntegrationRef string `json:"integration_ref"`
	// Fetch reports what `--configure-fetch` did, and is absent when the flag was not given: a pipeline
	// that asked should be able to see whether the line was written or was already there.
	Fetch           *fetchConfig `json:"fetch_config,omitempty"`
	Recorded        bool         `json:"recorded"`
	AlreadyRecorded bool         `json:"already_recorded"`
	// CarriedBy is the commit this run asked for when the record already names one it descends from: the
	// stacked case, where the changeset landed on its base branch and that branch later reached trunk. It
	// is absent whenever the run wrote a record, repeated one exactly, or refused — so a pipeline can tell
	// "the record covers this commit" from "this commit is the record" without comparing SHAs.
	CarriedBy string `json:"carried_by,omitempty"`
	// NextAction names the step that makes the record durable outside this clone. A record that lives
	// only where the merge ran is a record that dies there, and the moment to say so is the moment the
	// record was written — not a warning the reader has to run a different command to hear.
	NextAction string `json:"next_action,omitempty"`
}

// landing is what git-pair can honestly say about a recorded integration commit.
type landing struct {
	// Commit is the full sha the integration ref holds.
	Commit string
	// DefaultBranch is the local name of the branch git-pair calls the integration branch, empty
	// when nothing identifies one.
	DefaultBranch string
	// InDefaultBranch says the landing commit is in that branch's history.
	InDefaultBranch bool
	// BranchKnown separates "not in main" from "cannot tell which branch is main". Collapsing
	// them would report a fetching problem as a fact about the work.
	BranchKnown bool
}

// describeLanding says where a recorded landing commit sits.
//
// The name of the branch a landing reached is not stored anywhere. A ref holds an object id and
// nothing else, and the alternative — pointing the integration ref at an annotated tag object to
// carry the name — would put a peel in front of every reader of the ref and contradict the shape
// requirements §13 gives it. So the question is answered from the other end: is the recorded commit
// in the history of the branch the reader is asking about? That is the distinction the field exists
// for. Work that retired into `release/2.x` and never reached the default branch must not read like a
// default-branch landing, and a squash landing has no ancestry to consult anyway.
func (a *app) describeLanding(ctx context.Context, repo *git.Repo, commit string) (landing, error) {
	out := landing{Commit: commit}
	db, err := changeset.DefaultBranch(ctx, repo, a.defaultBranch)
	if errors.Is(err, changeset.ErrNoDefaultBranch) {
		return out, nil
	}
	if err != nil {
		return landing{}, err
	}
	in, err := repo.IsAncestor(ctx, commit, db.Ref)
	if err != nil {
		return landing{}, err
	}
	// A name in prose should read like a branch, not like a path git keeps it at: a CI clone's
	// integration branch is `origin/main` to the person reading the log.
	out.DefaultBranch = displayRef(db.LocalName())
	out.InDefaultBranch = in
	out.BranchKnown = true
	return out, nil
}

// displayRef trims the prefixes a reader already knows, so a sentence can name a branch without
// printing where git keeps it. A ref outside those namespaces — a tag, a raw revision a pipeline
// passed to --default-branch — is printed as supplied, because shortening it would guess.
func displayRef(ref string) string {
	for _, prefix := range []string{"refs/heads/", "refs/remotes/"} {
		if strings.HasPrefix(ref, prefix) {
			return strings.TrimPrefix(ref, prefix)
		}
	}
	return ref
}

// reach phrases the containment fact for a sentence: "reachable from main", or nothing when the
// branch it would be measured against is unknown.
func (l landing) reach() string {
	if !l.BranchKnown {
		return ""
	}
	if l.InDefaultBranch {
		return ", reachable from " + l.DefaultBranch
	}
	return ", not reachable from " + l.DefaultBranch
}

// namespaceAbsentWarning is §24's fetch guidance, and it travels with the commands that read the
// durable namespace rather than with the ones that do not: an empty namespace is a clone that was
// never given the refspec, and a verdict built from it is about the clone. `integration record` needs
// it because a record written without seeing the one already pushed is a duplicate; `queue` needs it
// because an orphan whose record is unreachable may only be unfetched. `check` no longer carries it:
// its verdict reads the derivation and the trunk, and no ref at all.
const namespaceAbsentWarning = "warning: this clone holds no refs/git-pair/* refs at all; " +
	"`--fetch` brings them, or run " + reviewref.FetchCommand +
	"; `--configure-fetch` makes an ordinary fetch keep bringing them — and trust nothing that says " +
	"never recorded until they are here\n"

func (a *app) runIntegrationRecord(in integrationRecordInput) error {
	ctx := context.Background()
	repo, err := a.loadRepo(ctx)
	if err != nil {
		return err
	}
	in.defaultBranch = a.defaultBranch
	rec, err := resolveIntegrationRecord(ctx, repo, in)
	if err != nil {
		return err
	}
	// Fail the optional half before the irreversible one: if the caller asked for configuration and this
	// repository has nowhere to put it, nothing is written and the exit code says so.
	if in.configureFetch {
		if _, err := a.requireConfigurableRemote(ctx, repo); err != nil {
			return err
		}
	}

	pair := reviewref.Pair{ID: rec.ID, Archive: rec.Source, Integration: rec.Commit}
	recorded, err := reviewref.RecordedPair(ctx, repo, rec.ID)
	if err != nil {
		return err
	}
	// The record already says exactly this, so answer with what it says and write nothing — before any of
	// the checks below. A retry often arrives with fewer flags than the run that wrote the pair, and
	// re-verifying a fact that is already on the record against a different set of assumptions can only
	// produce a refusal about the assumptions.
	if recorded.Archive == rec.Source && recorded.Integration == rec.Commit {
		return a.reportIntegration(rec, reviewref.PairResult{
			ArchiveRef:        reviewref.Archive(rec.ID),
			IntegrationRef:    reviewref.Integration(rec.ID),
			Archive:           rec.Source,
			IntegrationCommit: rec.Commit,
		}, false, a.recordExtras(ctx, repo, in))
	}
	// A second landing that *carries* the recorded commit is the stacked case, and it is not a conflict.
	// The child landed on the branch it was based on, that branch later landed on trunk, and a run
	// measured against trunk names a descendant of what the record names. The record answers "what did this
	// changeset become"; the asked commit answers "what carried it here". Both are true, so this answers
	// with the record instead of refusing — and what the record names still does not move, because the
	// write below is create-only and a pair that is simply different still conflicts.
	if carried, err := recordCarried(ctx, repo, recorded, rec); err != nil {
		return err
	} else if carried != "" {
		// The one landing check this answer needs is the one it prints: that the commit it calls a carrier
		// is in the destination it names. The review and directory checks are about writing a record, and
		// this run writes none — re-running them here would refuse a carrying answer over an assumption the
		// original record never had to satisfy.
		if err := verifyLandingReachable(ctx, repo, rec, in); err != nil {
			return err
		}
		extras := a.recordExtras(ctx, repo, in)
		extras.carried = carried
		return a.reportIntegration(rec, reviewref.PairResult{
			ArchiveRef:        reviewref.Archive(rec.ID),
			IntegrationRef:    reviewref.Integration(rec.ID),
			Archive:           recorded.Archive,
			IntegrationCommit: recorded.Integration,
		}, false, extras)
	}

	// A different pair for a changeset that already has one is refused here rather than by the write
	// below, so the reader gets the refusal that answers the question — "what did we already say?" —
	// instead of an incidental complaint from the landing checks, which a second landing into a branch
	// that already carries the directory would also fail. CreatePair keeps its own conflict handling: this
	// is a read, and a race is still settled by git's create-only update.
	if err := reviewref.Conflict(ctx, repo, pair); err != nil {
		return err
	}
	if err := verifyIntegrationRecord(ctx, repo, rec, in); err != nil {
		return err
	}
	// "The namespace is empty" is a fact about this clone, not about the changeset, and the two look
	// identical from here. The pair is written locally either way, so this does not stop the write; it
	// says so, because a record written in a clone that could not see the one already pushed produces
	// two refs for one changeset and a push that cannot explain itself.
	if ok, err := reviewref.Present(ctx, repo); err == nil && !ok {
		a.warn("%s\n", namespaceAbsentWarning)
	}
	res, err := reviewref.CreatePair(ctx, repo, pair)
	if err != nil {
		return err
	}
	// Configuration follows the refs. A refused record leaves the repository as it found it — including
	// its config, which the caller asked to change but did not get to yet: `record` is idempotent, so the
	// next run writes the refs it refused and the line together. Writing config first would let a refusal
	// about the review still mutate the clone's fetch behaviour, which is a side effect nobody invoking
	// `record` agreed to.
	return a.reportIntegration(rec, res, res.ArchiveCreated || res.IntegrationCreated, a.recordExtras(ctx, repo, in))
}

// reportIntegration is the answer, in whichever shape was asked for. It is one function so the no-op
// short-circuit above and the write below cannot drift into saying different things about the same pair.
// recordExtras resolves the configuration half of a record run, once the refs are settled.
func (a *app) recordExtras(ctx context.Context, repo *git.Repo, in integrationRecordInput) recordReportExtras {
	var extras recordReportExtras
	remote, err := a.remoteForDurableRefs(ctx, repo, "")
	if err != nil {
		return extras
	}
	if in.configureFetch {
		fc, err := a.configureFetchRefSpecs(ctx, repo, remote)
		if err != nil {
			// A record that succeeded and could not configure says so rather than pretending the
			// invocation did everything: the refs are the durable half, and they are already written.
			a.warn("warning: %v\n", err)
			return extras
		}
		extras.fetch = fc
		return extras
	}
	// Not configured, and nothing will be unless the caller says so — but the reader should know the
	// option exists, since the alternative to naming it is a clone that keeps answering from a namespace
	// it never fetches. Except in the clone where it is already configured: reminding someone of a thing
	// they already did is how a hint becomes noise.
	if configuredAlready(ctx, repo, remote) {
		return extras
	}
	extras.hint = fetchHint(remote)
	return extras
}

// recordReportExtras is what the record run decided about configuration, carried to the report so the
// answer about the refs and the answer about the clone's fetch behaviour arrive together.
// recordCarried answers the stacked question: does the record already cover this commit? True answer is
// the commit the caller asked for, and it holds when three things do: the record names an integration
// commit, the asked commit descends from it, and the asked archive half is the same reviewed head the
// record names.
//
// The third condition is what keeps this from swallowing a genuine mistake. A different reviewed head is a
// different claim about what was approved, not a later position on the same chain, so an archive mismatch
// falls through to the conflict — which is the refusal that should be loud. Nothing here compares contents:
// a rewrite that re-creates the recorded commit under a new SHA is not a carrier and does not read like
// one.
func recordCarried(ctx context.Context, repo *git.Repo, recorded reviewref.Pair, rec *integrationRecord) (string, error) {
	if recorded.Integration == "" || recorded.Integration == rec.Commit || recorded.Archive != rec.Source {
		return "", nil
	}
	descends, err := repo.IsAncestor(ctx, recorded.Integration, rec.Commit)
	if err != nil || !descends {
		return "", err
	}
	// Descent alone is not enough, or every commit after a landing would be a carrier of it. The question
	// is whether *this* commit is what brought the recorded one in: if the asked commit's own first parent
	// already held the record, then the destination had the changeset on its line before this commit, and
	// the asked commit is a second landing on that branch — a backport, which git-pair keeps out of the
	// record (§11.4) because it is a fact about that branch's history and not a new arrival of the
	// changeset. When only this commit brings the record with it, this commit is the carrier, which is what
	// a stacked child's landing on trunk looks like from the trunk side.
	//
	// A commit with no first parent has no "before" to compare against. That is not a carrier, and falling
	// through gives the caller the conflict refusal rather than a guess.
	before, err := repo.IsAncestor(ctx, recorded.Integration, rec.Commit+"^")
	if err != nil {
		if errors.Is(err, git.ErrUnknownRevision) {
			return "", nil
		}
		return "", err
	}
	if before {
		return "", nil
	}
	return rec.Commit, nil
}

type recordReportExtras struct {
	// fetch is set only when `--configure-fetch` was asked for.
	fetch *fetchConfig
	// hint is the one line naming the flag, for a run that did not ask.
	hint string
	// carried is the commit the caller asked for when it descends from what the record already names.
	// Empty for every run that writes a record or repeats one exactly.
	carried string
}

func (a *app) reportIntegration(rec *integrationRecord, res reviewref.PairResult, wrote bool, extras recordReportExtras) error {
	if a.json {
		return a.emitJSON(integrationRecordJSON{
			Changeset:       rec.ID,
			Source:          rec.Source,
			Commit:          rec.Commit,
			Target:          rec.Target,
			TargetDerived:   rec.TargetDerived != "",
			Derived:         rec.Derived,
			ArchiveRef:      res.ArchiveRef,
			IntegrationRef:  res.IntegrationRef,
			Recorded:        wrote,
			AlreadyRecorded: !wrote,
			CarriedBy:       extras.carried,
			NextAction:      "git pair integration publish " + rec.ID,
			Fetch:           extras.fetch,
		})
	}
	switch {
	case extras.carried != "":
		into := ""
		if rec.Target != "" {
			into = " into " + displayRef(rec.Target)
		}
		a.printf("%s: already recorded at %s, and %s carries that commit%s\n",
			rec.ID, short(res.IntegrationCommit), short(extras.carried), into)
	case res.ArchiveCreated && res.IntegrationCreated:
		a.printf("%s: recorded %s as the integration of %s\n", rec.ID, short(rec.Commit), short(rec.Source))
	case !wrote:
		a.printf("%s: already recorded %s as the integration of %s; nothing changed\n",
			rec.ID, short(rec.Commit), short(rec.Source))
	default:
		// A half pair completed. This is the case the write order exists for: the crash left a
		// changeset that read as unfinished, and this invocation finished it.
		a.printf("%s: completed the record for %s (the %s ref already existed)\n",
			rec.ID, short(rec.Source), existingHalf(res))
	}
	// The refs are printed from the result, not from what was asked for: for a write and for an exact
	// repeat they are the same values, and for a carrying answer they are what the record holds while the
	// headline names the carrier. Printing the asked pair there would show a commit no ref points at.
	a.printf("  archive:     %s -> %s\n", res.ArchiveRef, short(res.Archive))
	a.printf("  integration: %s -> %s\n", res.IntegrationRef, short(res.IntegrationCommit))
	if rec.Target != "" {
		a.printf("  verified reachable from %s%s\n", displayRef(rec.Target), derivedNote(rec))
	}
	if len(rec.Derived) > 0 {
		// Which parts of the record the repository supplied rather than the caller. Both values are
		// printed on the lines above, so this line is about provenance: a record whose SHAs came from the
		// graph is a different act of writing than one whose SHAs were typed, and a reader auditing it
		// later should know which they are looking at.
		a.printf("  derived:     --%s\n", strings.Join(rec.Derived, " and --"))
		if rec.FastForward {
			// The one derivation worth naming: archive and integration hold the same commit, and that is not
			// a pair written badly — it is a destination that carried the reviewed chain itself.
			a.printf("               fast-forward: the destination's own line carries the reviewed chain,\n")
			a.printf("               so the archive and the integration name the same commit\n")
		}
	}

	switch {
	case extras.fetch != nil && extras.fetch.Already:
		a.printf("  fetch:         %s already fetches the durable mirrors; nothing changed\n", extras.fetch.Key)
	case extras.fetch != nil:
		a.printf("  fetch:         added %s to %s\n", extras.fetch.Refspec, extras.fetch.Key)
	case extras.hint != "":
		a.printf("%s", extras.hint)
	}
	if wrote {
		a.printf("  next:        git pair integration publish %s, then the branch can go\n", rec.ID)
	}
	return nil
}

// existingHalf names the ref this call did not have to write, for the completion message.
func existingHalf(res reviewref.PairResult) string {
	if !res.ArchiveCreated {
		return "archive"
	}
	return "integration"
}
