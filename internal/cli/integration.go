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
			return a.runIntegrationRecord(integrationRecordInput{source: source, commit: commit, target: target, changeset: id, allowFeedback: allowFeedback})
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "the unsquashed tip that was reviewed (the head `change ready` and `review submit` were writing on)")
	cmd.Flags().StringVar(&commit, "commit", "", "the commit the changeset became in the destination branch")
	cmd.Flags().StringVar(&target, "target", "", "ref the landing commit must be reachable from; defaults to the changeset's base branch")
	cmd.Flags().StringVar(&id, "changeset", "", "which changeset to record, when --source carries more than one changeset directory")
	cmd.Flags().BoolVar(&allowFeedback, "allow-feedback", false, "accept a changeset whose newest verdict is non-blocking feedback, as `git pair check --allow-feedback` does")
	return cmd
}

// integrationRecordInput is what the caller supplied.
type integrationRecordInput struct {
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
	candidates, err := changesetsAtSource(ctx, repo, source, in)
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
		candidates, err := changesetsAtSource(ctx, repo, source, in)
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
	if in.source == "" {
		head, err := deriveArchiveTip(ctx, repo, dests, id)
		if err != nil {
			return in, err
		}
		in.source = head
		rec.Derived = append(rec.Derived, "source")
	}
	return in, nil
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
	if cur, err := changeset.Current(ctx, repo, in.defaultBranch); err == nil {
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
func changesetToDerive(ctx context.Context, repo *git.Repo, dests []string) (string, error) {
	recorded := map[string]bool{}
	if entries, err := reviewref.List(ctx, repo); err == nil {
		for _, e := range entries {
			if e.Kind == reviewref.KindIntegration {
				recorded[e.ID] = true
			}
		}
	}
	var all, unrecorded []string
	for _, dest := range dests {
		dirs, err := changeset.DirsAt(ctx, repo, dest)
		if err != nil {
			return "", err
		}
		for _, id := range dirs {
			if !slices.Contains(all, id) {
				all = append(all, id)
			}
			if !recorded[id] && !slices.Contains(unrecorded, id) {
				unrecorded = append(unrecorded, id)
			}
		}
	}
	names := make([]string, 0, len(dests))
	for _, d := range dests {
		names = append(names, displayRef(d))
	}
	found, noun := unrecorded, "is missing its integration record"
	if len(unrecorded) == 0 {
		found, noun = all, "has no integration record to check a retry against"
	}
	switch {
	case len(found) == 1:
		return found[0], nil
	case len(found) == 0:
		return "", &usageError{fmt.Errorf("no changeset directory on %s, so there is nothing here to record\n\n`git pair integration record` derives the pair from the repository: the landing is the commit that added changesets/<id>/ to %s's first-parent line, and the reviewed head is the branch still carrying the directory. Name --source and --commit to record something else — an older landing, or one on a branch git-pair was not told about",
			strings.Join(names, " or "), strings.Join(names, " or "))}
	default:
		return "", &usageError{fmt.Errorf("more than one changeset on %s %s:%s\n\nName the one this record is for with --changeset <id>",
			strings.Join(names, " or "), noun, "\n  "+strings.Join(found, "\n  "))}
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
func deriveArchiveTip(ctx context.Context, repo *git.Repo, dests []string, id string) (string, error) {
	exclude := map[string]bool{}
	for _, dest := range dests {
		if sha, err := repo.RevParse(ctx, dest+"^{commit}"); err == nil {
			exclude[sha] = true
		}
		exclude[displayRef(dest)] = true
	}
	tips, err := repo.RefTips(ctx, "refs/heads/*")
	if err != nil {
		return "", err
	}
	path := "changesets/" + id
	var found []string
	for _, tip := range tips {
		name := displayRef(tip.Name)
		if exclude[name] || exclude[tip.Commit] {
			continue
		}
		if repo.PathExistsAt(ctx, tip.Commit, path) {
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
	if parent, err := repo.RevParse(ctx, rec.Commit+"^1"); err == nil && repo.PathExistsAt(ctx, parent, "changesets/"+rec.ID) {
		return fmt.Errorf("%s does not add changesets/%s/ over its first parent %s: the directory was already there, so this commit is not where the work landed\n\nRecord the commit that brought changesets/%s/ into %s — a merge, a squash or a cherry-pick of the reviewed branch. `git log --first-parent %s -- changesets/%s/` lists the candidates.",
			short(rec.Commit), rec.ID, short(parent), rec.ID, displayRef(rec.Target), displayRef(rec.Target), rec.ID)
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
// directories in its tree, minus the ones the destination branch already carries.
//
// That is PRD §4's rule read at the commit the record names, which is the only discovery that works
// from a CI checkout: it asks two trees, so it needs no ref to have been written first, no fetch of a
// namespace, and no branch to be standing around. When the destination branch cannot be identified
// the subtraction is skipped rather than guessed — a directory that has landed is still a directory
// this commit carries, and over-reporting candidates is safe because --changeset decides between them.
func changesetsAtSource(ctx context.Context, repo *git.Repo, source string, in integrationRecordInput) ([]string, error) {
	here, err := changeset.DirsAt(ctx, repo, source)
	if err != nil {
		return nil, err
	}
	// One candidate needs no subtraction, and the subtraction must not be allowed to empty the set:
	// recording a changeset whose landing has already reached trunk is the ordinary order of work, and
	// there the directory is in trunk precisely because this command is being asked to record it.
	if len(here) < 2 {
		return here, nil
	}
	db, err := changeset.DefaultBranch(ctx, repo, "")
	if err != nil {
		return here, nil
	}
	trunk, err := changeset.DirsAt(ctx, repo, db.Ref)
	if err != nil {
		return here, nil
	}
	landed := map[string]bool{}
	for _, id := range trunk {
		landed[id] = true
	}
	var out []string
	for _, id := range here {
		if !landed[id] {
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
	Changeset       string   `json:"changeset"`
	Source          string   `json:"source"`
	Commit          string   `json:"commit"`
	Target          string   `json:"target"`
	TargetDerived   bool     `json:"target_derived,omitempty"`
	Derived         []string `json:"derived,omitempty"`
	ArchiveRef      string   `json:"archive_ref"`
	IntegrationRef  string   `json:"integration_ref"`
	Recorded        bool     `json:"recorded"`
	AlreadyRecorded bool     `json:"already_recorded"`
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
	"; trust nothing that says never recorded until they are here\n"

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
		}, false)
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
	return a.reportIntegration(rec, res, res.ArchiveCreated || res.IntegrationCreated)
}

// reportIntegration is the answer, in whichever shape was asked for. It is one function so the no-op
// short-circuit above and the write below cannot drift into saying different things about the same pair.
func (a *app) reportIntegration(rec *integrationRecord, res reviewref.PairResult, wrote bool) error {
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
			NextAction:      "git pair integration publish " + rec.ID,
		})
	}
	switch {
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
	a.printf("  archive:     %s -> %s\n", res.ArchiveRef, short(rec.Source))
	a.printf("  integration: %s -> %s\n", res.IntegrationRef, short(rec.Commit))
	if wrote {
		a.printf("  next:        git pair integration publish %s\n", rec.ID)
	}
	if rec.Target != "" {
		a.printf("  verified reachable from %s%s\n", displayRef(rec.Target), derivedNote(rec))
	}
	if len(rec.Derived) > 0 {
		// Which parts of the record the repository supplied rather than the caller. Both values are
		// printed on the lines above, so this line is about provenance: a record whose SHAs came from the
		// graph is a different act of writing than one whose SHAs were typed, and a reader auditing it
		// later should know which they are looking at.
		a.printf("  derived:     --%s\n", strings.Join(rec.Derived, " and --"))
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
