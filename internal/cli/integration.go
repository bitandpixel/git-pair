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
		Long: `Integration records connect the archived head of a changeset to the commit it became in the
target history. The link is recorded rather than inferred: squash, rebase and cherry-pick rewrite
commit identity, so no ancestry or patch-ID reading can tell you that a commit on the default
branch is someone's reviewed work.`,
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
		Short: "Record the commit a changeset became in the target history",
		Long: `Records that the work archived at --source became --commit, by creating
refs/git-pair/changesets/<id>/integration. The changeset id is derived from the archive ref that
points at --source, so the integration process needs the two SHAs it already has and not the name
a human gave the work.

The record is created once and never rewritten, and it freezes the archive: after it exists, no
git-pair command moves the archive ref, because the pair of refs is the mapping from what was
reviewed to where it landed.

This command works from any branch and writes no commit. It takes a position rather than a
checkout: it is meant to run in CI, after the merge, where no one has the author's branch.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runIntegrationRecord(integrationRecordInput{source: source, commit: commit, target: target, changeset: id})
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "the commit the changeset was archived as (the SHA your pipeline built)")
	cmd.Flags().StringVar(&commit, "commit", "", "the commit the changeset became in the target history")
	cmd.Flags().StringVar(&target, "target", "", "ref to verify the landing commit is reachable from, for example origin/main")
	cmd.Flags().StringVar(&id, "changeset", "", "which changeset to record, when more than one archive points at --source")
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
	// Source is the full sha of the archived head the caller named.
	Source string
	// Commit is the full sha of the landing commit.
	Commit string
	// Target is the ref the landing was verified against, empty when none was given.
	Target string
	// Ref is the integration ref about to be created.
	Ref string
}

// verifyIntegrationRecord decides whether the record may be written, and says no in the order that
// makes the failures useful: first the things the caller got wrong, then the facts about the
// repository. The last check in the sequence — the record that already exists — is made by the caller,
// beside the create-only write it protects.
//
// Two of these are worth defending. The source is resolved through `rev-parse` before discovery, so
// an abbreviated SHA pasted from a CI log finds the archive rather than matching nothing. And the
// landing commit's tree is checked for the changeset directory rather than believed: `changesets/`
// is committed content that travels with the change through merge, squash and cherry-pick, so a
// release-branch record pointing at an unrelated commit fails here instead of quietly succeeding.
func verifyIntegrationRecord(ctx context.Context, repo *git.Repo, in integrationRecordInput) (*integrationRecord, error) {
	if in.source == "" || in.commit == "" {
		return nil, &usageError{fmt.Errorf("--source and --commit are both required: the commit the changeset was archived as, and the commit it became")}
	}
	source, err := resolveCommit(ctx, repo, "--source", in.source)
	if err != nil {
		return nil, err
	}
	candidates, err := reviewref.ArchivesAt(ctx, repo, source)
	if err != nil {
		return nil, err
	}
	// §24: "no archive points at this commit" and "this clone has no git-pair refs" are different
	// failures with different fixes, so the difference is worth asking about — but only on the path
	// where nothing matched, so a run that succeeds pays nothing for the question.
	namespacePresent := true
	if len(candidates) == 0 {
		if namespacePresent, err = reviewref.Present(ctx, repo); err != nil {
			return nil, err
		}
	}
	id, err := chooseIntegrationCandidate(candidates, source, in.changeset, namespacePresent)
	if err != nil {
		return nil, err
	}
	// A changeset that ended was never integrated, and a record would read as though the work
	// landed. One commit is enough: `change abandon` moves the archive onto its own marker, and no
	// git-pair command writes anything after that, so the tip is where an ending is recorded.
	if err := refuseAbandonedSource(ctx, repo, id, source); err != nil {
		return nil, err
	}
	landing, err := resolveCommit(ctx, repo, "--commit", in.commit)
	if err != nil {
		return nil, err
	}
	rec := &integrationRecord{ID: id, Source: source, Commit: landing, Ref: reviewref.Integration(id)}
	if in.target != "" {
		if _, err := resolveCommit(ctx, repo, "--target", in.target); err != nil {
			return nil, err
		}
		rec.Target = in.target
		// Reachability is the one thing `--target` can check without assumptions, and it is
		// verifying a ref the caller named rather than deriving where the work landed — which is
		// why it does not reopen the question the anchored-lifecycle plan closed. Note the
		// asymmetry the fixtures showed: the landing commit need not descend from the archived
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

// chooseIntegrationCandidate applies §17 and §19: exactly one archive must point at the source,
// unless the caller named the changeset — in which case that changeset still has to be one of the
// matches. Naming a changeset disambiguates; it never substitutes for an archive that is not there.
//
// `namespacePresent` separates §18's two causes. With refs in the namespace, the source is simply not
// an archived head, and the three possibilities are listed. With none, the command says so and prints
// the fetch: a CI job told "the changeset was never archived" will go re-run `change ready`, which is
// someone else's command on someone else's branch, when the fix was one line in its checkout.
func chooseIntegrationCandidate(candidates []string, source, named string, namespacePresent bool) (string, error) {
	if len(candidates) == 0 {
		if !namespacePresent {
			return "", fmt.Errorf("no changeset archive points at %s, and this repository holds no %s refs at all\n\nFetch them before recording:\n  %s\n\ngit-pair never pushes these refs, so if they were never published, the author's clone still holds the only copy",
				short(source), reviewref.NamespaceRoot()+"/*", reviewref.FetchCommand)
		}
		if named != "" {
			return "", fmt.Errorf("no changeset archive points at %s, including %s: the archive of a changeset you name must still be at that commit", short(source), named)
		}
		return "", fmt.Errorf("no changeset archive points at %s, so integration cannot be recorded automatically\n\nThis usually means one of:\n  the archive refs were never fetched — refs/git-pair/changesets/* is not fetched by default\n  the changeset was never archived — `git pair change ready` writes the archive\n  the wrong commit was supplied — --source is the head the archive names, not the merge commit",
			short(source))
	}
	if named != "" {
		for _, c := range candidates {
			if c == named {
				return named, nil
			}
		}
		return "", fmt.Errorf("changeset %s is not among the changesets archived at %s (%s): --changeset picks between them, it does not replace the archive",
			named, short(source), strings.Join(candidates, ", "))
	}
	if len(candidates) > 1 {
		list := ""
		for _, c := range candidates {
			list += "\n  " + c
		}
		// Ambiguity is a usage error for the same reason it is one for every other command: the
		// repository is not broken, and the caller has to say which one they mean.
		return "", &usageError{fmt.Errorf("more than one changeset archive points at %s:%s\n\nName the one you mean with --changeset <id>", short(source), list)}
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
// pipeline does with them is compare against the SHA it already holds.
type integrationRecordJSON struct {
	Changeset      string `json:"changeset"`
	Source         string `json:"source"`
	Commit         string `json:"commit"`
	Target         string `json:"target"`
	IntegrationRef string `json:"integration_ref"`
	Recorded       bool   `json:"recorded"`
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
// for. Work that retired into `release/2.x` and never reached the default branch must not read like
// a default-branch landing, and a squash landing has no ancestry to consult anyway.
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
	// §22: the record is created once. The existence check prints this explanation rather than
	// git's `refusing to update ref`, and the create-only write behind it is what makes the check
	// un-raceable — two CI jobs recording the same landing cannot both win.
	existing, err := reviewref.ResolveIntegration(ctx, repo, rec.ID)
	if err != nil && !errors.Is(err, reviewref.ErrNotIntegrated) {
		return err
	}
	if existing != "" {
		return integrationAlreadyRecordedError(rec.ID, rec.Source, existing, rec.Target)
	}
	created, err := reviewref.CreateIntegration(ctx, repo, rec.ID, rec.Commit)
	if err != nil {
		return err
	}
	if !created {
		return integrationAlreadyRecordedError(rec.ID, rec.Source, rec.Commit, rec.Target)
	}
	if a.json {
		return a.emitJSON(integrationRecordJSON{
			Changeset:      rec.ID,
			Source:         rec.Source,
			Commit:         rec.Commit,
			Target:         rec.Target,
			IntegrationRef: rec.Ref,
			Recorded:       true,
		})
	}
	a.printf("%s: recorded %s as the integration of %s\n", rec.ID, short(rec.Commit), short(rec.Source))
	a.printf("  %s -> %s\n", rec.Ref, short(rec.Commit))
	if rec.Target != "" {
		a.printf("  verified reachable from %s\n", rec.Target)
	}
	return nil
}

// integrationAlreadyRecordedError is §22's loud refusal. It names the record that exists rather
// than the one being attempted, because the question a re-run asks is "what did we already say?".
// The backport case matters most: a changeset merged into `release/2.x` after `main` has one
// record, and git-pair does not keep a second ref per landing — the release branch's own history is
// the record of the backport.
func integrationAlreadyRecordedError(id, source, existing, target string) error {
	asked := ""
	if target != "" {
		asked = fmt.Sprintf("\n  asked:      --target %s", target)
	}
	return fmt.Errorf("integration already recorded for %s:\n\n  source:      %s\n  integrated:  %s%s\n\nRefusing to rewrite integration history. A changeset has one integration record: a backport to another branch is a fact about that branch's history, which git already records",
		id, short(source), short(existing), asked)
}
