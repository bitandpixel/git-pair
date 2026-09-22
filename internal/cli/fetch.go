package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"gitpair/internal/git"
	"gitpair/internal/reviewref"
)

// The read side of the durable refs. `integration record` writes them locally; everything else about
// them lives in other clones, and a command that answers from one clone alone has to be able to say
// so. `--fetch` is how a reader asks for the wider view, and it asks for two things in one network
// trip: the records themselves, and the mirrors the published-not-published comparison needs.
//
// It is a flag rather than a default because a command that quietly reaches the network is a command
// whose answer depends on where it was run, and because `queue` in a cron loop should not be buying
// a fetch per tick without anyone deciding that. Fetch failure is a warning and the stale answer,
// never a refusal: "I could not ask" is not a verdict about the work.

const fetchFlagHelp = "fetch the durable refs and their mirrors from the remote first"

// fetchFlag registers --fetch on a command. Registered per command rather than as a persistent root
// flag because for the commands that do *not* have it — `review submit`, `change ready`, and the rest
// of the write path — the flag would be a promise the command has no intention of keeping.
func fetchFlag(cmd *cobra.Command, into *bool) {
	cmd.Flags().BoolVar(into, "fetch", false, fetchFlagHelp)
}

// remoteForDurableRefs names the remote the durable refs are compared against: the branch's upstream
// where it has one, otherwise `origin`, otherwise nothing.
//
// "Otherwise nothing" is deliberate. `fetchTargets` will fetch every remote it can find, which is the
// right answer for keeping a branch current and the wrong one here: the mirror namespace is per-remote,
// and picking whichever remote sorted first would compare a record against a remote nobody intended to
// publish to. No answer is the honest answer, and the caller says so.
func (a *app) remoteForDurableRefs(ctx context.Context, repo *git.Repo, branch string) (string, error) {
	if a.durableRemoteKnown {
		return a.durableRemote, nil
	}
	remote, err := lookupDurableRemote(ctx, repo, branch)
	if err == nil {
		a.durableRemote, a.durableRemoteKnown = remote, true
	}
	return remote, err
}

func lookupDurableRemote(ctx context.Context, repo *git.Repo, branch string) (string, error) {
	// The caller names the branch it is standing on, because asking git for it is another
	// invocation and the caller always knows it. An empty branch means "no particular branch",
	// which is `queue`'s answer: the queue spans every branch, so no branch's upstream is the
	// right answer for it, and the repository's origin is.
	if branch != "" {
		if up, err := repo.Upstream(ctx, branch); err == nil {
			if remote, _, found := strings.Cut(up, "/"); found {
				// No `git remote` check: if the remote named by the upstream is gone, the fetch
				// below says so and the warning is more useful than the lookup was.
				return remote, nil
			}
		}
	}
	remotes, err := repo.Remotes(ctx)
	if err != nil {
		return "", err
	}
	for _, r := range remotes {
		if r == "origin" {
			return "origin", nil
		}
	}
	return "", nil
}

// fetchDurableRefs brings the records and their mirrors into this clone, and says out loud whatever it
// could not do. The answer it produces is the warning stream's; the command goes on to answer from
// whatever the clone now holds.
func (a *app) fetchDurableRefs(ctx context.Context, repo *git.Repo, branch string) {
	remote, err := a.remoteForDurableRefs(ctx, repo, branch)

	if err != nil {
		a.warn("warning: cannot tell which remote to ask for the durable refs: %v — answering from this clone\n", err)
		return
	}
	if remote == "" {
		a.warn("warning: this repository has no remote to ask, so nothing reported here can be about one\n")
		return
	}
	// Records first, then the mirrors they are compared against: if the first fetch cannot reach the
	// remote there is nothing to refresh, and if only the second fails the records are already home and
	// the reader is told which half of the answer is stale.
	plan := reviewref.FetchPlanFor(remote)
	if err := repo.FetchRefs(ctx, remote, plan.Records...); err != nil {
		a.warn("warning: fetching the durable refs from %s failed: %v — answering from this clone\n", remote, err)
		return
	}
	if err := repo.FetchPruned(ctx, remote, plan.Mirrors...); err != nil {
		a.warn("warning: the durable refs arrived from %s, but its copies could not be refreshed (%v) — "+
			"published\", if reported, is reported against the last answer\n", remote, err)
	}
}

// durableFetchNote is the one-line form used where a section already explains itself: what the reader
// is being told, and what would have made it certain.
func durableFetchNote(remote string) string {
	if remote == "" {
		return "no remote configured, so this clone cannot know what any other clone published"
	}
	return fmt.Sprintf("no %s refs fetched here — `--fetch`, or configure the refspec once: %s",
		remote, reviewref.FetchCommand)
}
