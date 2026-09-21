package cli

import (
	"context"
	"errors"
	"fmt"
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
	return cmd
}

func newIntegrationRecordCommand(a *app) *cobra.Command {
	var source, commit, target, id string
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
` + "`--commit`" + ` is the merge or squash you just made. Both are flags because CI has neither — the
command takes a position rather than a checkout, writes no commit, and runs from any branch.

git-pair performs no merge. The contract is ` + "`git pair check`" + `, then an ordinary git merge into
the base branch by whoever owns it, then this command.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runIntegrationRecord(integrationRecordInput{source: source, commit: commit, target: target, changeset: id})
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "the unsquashed tip that was reviewed (the head `change ready` and `review submit` were writing on)")
	cmd.Flags().StringVar(&commit, "commit", "", "the commit the changeset became in the destination branch")
	cmd.Flags().StringVar(&target, "target", "", "ref to verify the landing commit is reachable from, for example origin/main")
	cmd.Flags().StringVar(&id, "changeset", "", "which changeset to record, when --source carries more than one changeset directory")
	return cmd
}

// integrationRecordInput is what the caller supplied.
type integrationRecordInput struct {
	source, commit, target, changeset string
}

// integrationRecord is a verified record: everything `integration record` writes and reports.
type integrationRecord struct {
	// ID is the derived (or disambiguated) changeset id.
	ID string
	// Source is the full sha of the reviewed head the caller named.
	Source string
	// Commit is the full sha of the landing commit.
	Commit string
	// Target is the ref the landing was verified against, empty when none was given.
	Target string
}

// verifyIntegrationRecord decides whether the record may be written, and says no in the order that
// makes the failures useful: first the things the caller got wrong, then the facts about the
// repository.
//
// Two of these are worth defending. The source is resolved through `rev-parse` before discovery, so
// an abbreviated SHA pasted from a CI log finds the changeset rather than matching nothing. And the
// landing commit's tree is checked for the changeset directory rather than believed: `changesets/` is
// committed content that travels with the change through merge, squash and cherry-pick, so a
// release-branch record pointing at an unrelated commit fails here instead of quietly succeeding.
func verifyIntegrationRecord(ctx context.Context, repo *git.Repo, in integrationRecordInput) (*integrationRecord, error) {
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
	// A changeset that ended was never integrated, and a record would read as though the work
	// landed. One commit is enough: the terminal marker is the newest thing a branch of that kind
	// carries, and git-pair writes nothing further for it.
	if err := refuseAbandonedSource(ctx, repo, id, source); err != nil {
		return nil, err
	}
	landing, err := resolveCommit(ctx, repo, "--commit", in.commit)
	if err != nil {
		return nil, err
	}
	rec := &integrationRecord{ID: id, Source: source, Commit: landing}
	if in.target != "" {
		if _, err := resolveCommit(ctx, repo, "--target", in.target); err != nil {
			return nil, err
		}
		rec.Target = in.target
		// Reachability is the one thing `--target` can check without assumptions, and it is
		// verifying a ref the caller named rather than deriving where the work landed — which is
		// why it does not reopen the question the anchored-lifecycle plan closed. Note the
		// asymmetry the fixtures showed: the landing commit need not descend from the reviewed
		// head (a squash has no ancestry between the two), but it must be in the target's history.
		contains, err := repo.IsAncestor(ctx, landing, in.target)
		if err != nil {
			return nil, err
		}
		if !contains {
			return nil, fmt.Errorf("%s is not reachable from %s: the record would say the work landed somewhere it is not", short(landing), in.target)
		}
	}
	// Verify the record rather than believe it. `changesets/` is committed content that travels
	// with the change through merge, squash and cherry-pick, so a release-branch record pointing at
	// an unrelated commit fails here instead of quietly succeeding.
	if !repo.PathExistsAt(ctx, landing, "changesets/"+id) {
		return nil, fmt.Errorf("changesets/%s/ does not exist in %s: that commit does not carry this changeset, so it cannot be where the work landed", id, short(landing))
	}
	return rec, nil
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

// refuseAbandonedSource is §21's rejection of a CLOSED changeset — CLOSED being what git-pair
// calls abandoned, since there is no separate closed state.
func refuseAbandonedSource(ctx context.Context, repo *git.Repo, id, source string) error {
	trailers, err := lifecycle.MarkerAt(ctx, repo, source)
	if err != nil {
		return err
	}
	if trailers[model.TrailerState] == model.StateValueAbandoned {
		return fmt.Errorf("changeset %s was abandoned by %s, so there is nothing to integrate", id, short(source))
	}
	return nil
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
// pipeline does with them is compare against the SHA it already holds. `recorded` says this call
// wrote at least one ref, and `already_recorded` says both already named this exact pair — a retry
// is a success, and the two successes are worth telling apart in a log.
type integrationRecordJSON struct {
	Changeset       string `json:"changeset"`
	Source          string `json:"source"`
	Commit          string `json:"commit"`
	Target          string `json:"target"`
	ArchiveRef      string `json:"archive_ref"`
	IntegrationRef  string `json:"integration_ref"`
	Recorded        bool   `json:"recorded"`
	AlreadyRecorded bool   `json:"already_recorded"`
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
	"fetch them before trusting anything that says never recorded: " + reviewref.FetchCommand + "\n"

func (a *app) runIntegrationRecord(in integrationRecordInput) error {
	ctx := context.Background()
	repo, err := a.loadRepo(ctx)
	if err != nil {
		return err
	}
	rec, err := verifyIntegrationRecord(ctx, repo, in)
	if err != nil {
		return err
	}
	// "The namespace is empty" is a fact about this clone, not about the changeset, and the two look
	// identical from here. The pair is written locally either way, so this does not stop the write; it
	// says so, because a record written in a clone that could not see the one already pushed produces
	// two refs for one changeset and a push that cannot explain itself.
	if ok, err := reviewref.Present(ctx, repo); err == nil && !ok {
		a.warn("%s\n", namespaceAbsentWarning)
	}
	res, err := reviewref.CreatePair(ctx, repo, reviewref.Pair{ID: rec.ID, Archive: rec.Source, Integration: rec.Commit})
	if err != nil {
		return err
	}
	wrote := res.ArchiveCreated || res.IntegrationCreated
	if a.json {
		return a.emitJSON(integrationRecordJSON{
			Changeset:       rec.ID,
			Source:          rec.Source,
			Commit:          rec.Commit,
			Target:          rec.Target,
			ArchiveRef:      res.ArchiveRef,
			IntegrationRef:  res.IntegrationRef,
			Recorded:        wrote,
			AlreadyRecorded: !wrote,
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
	if rec.Target != "" {
		a.printf("  verified reachable from %s\n", rec.Target)
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
