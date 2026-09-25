package cli

// `git pair integration configure` is consent, spelled as a command.
//
// The alternative was a prompt on first record — "would you like this clone to keep fetching the durable
// refs?" — and it was rejected for a reason that is not about politeness. PRD §22 makes this CLI the agent
// surface, so a TTY-dependent question would make one command line mean two things depending on where it
// ran; and in CI an unanswered prompt is indistinguishable from a declined one, which is precisely the
// failure that leaves a paper trail unpublished. Consent that is visible in a pipeline definition and
// greppable in a log is worth more than consent that asks nicely in the right terminal. If interactive
// confirmation is wanted later it belongs in the TUI, which is already a conversation.
//
// It is a command rather than a flag on `record` because the clone that wants the configuration is not
// always a clone that is recording something. `record` needs the two SHAs a landing produced, and a clone
// that arrived after the fact — a fresh CI runner, a colleague's checkout, a repository whose owner
// decided last week — has neither, and had nothing to run. Configuration is a property of the clone, so
// it gets a command of its own, and every clone can run it.
//
// What it writes is also a decision. The *mirror* refspec goes into `remote.<name>.fetch`, never the
// record refspec: a record is a claim that a landing happened, and a clone should acquire claims by asking
// (`--fetch`), not because a configuration line written weeks earlier keeps delivering them. The mirrors are
// the comparison, and the comparison is what an ordinary `git fetch` should keep able to make — that is the
// difference between a clone that can tell published from unpublished and one that has to be told to look.
//
// The push half is the records themselves, and it is the half a caller has to ask for with intent:
// `remote.<name>.push` makes every plain `git push` from this clone publish review history. It is written
// unforced, for the reason PRD §13.4 gives — publishing review history is a statement about who gets to
// read it, and a remote that holds a different value should reject the push rather than be overwritten.

import (
	"context"
	"fmt"
	"slices"

	"github.com/spf13/cobra"

	"gitpair/internal/git"
	"gitpair/internal/reviewref"
)

// configuredRefspec is one configuration line git-pair wrote or found already present: the exact key and
// value, and whether they were already there. Reported rather than implied, because "I configured it" and
// "it was already configured" are different answers to the question a pipeline is asking. The remote is on
// the answer above it, not repeated in each half.
type configuredRefspec struct {
	Key     string `json:"key"`
	Refspec string `json:"refspec"`
	// Already is git-pair reporting that it wrote nothing, which an idempotent step must be able to say.
	Already bool `json:"already_configured"`
}

// integrationConfigJSON is what `integration configure` did. Each half is absent when the command was not
// asked to write it — `--fetch-only` leaves `push` out — so a caller can tell "not asked" from "asked, and
// it was already there".
type integrationConfigJSON struct {
	Remote string             `json:"remote"`
	Fetch  *configuredRefspec `json:"fetch,omitempty"`
	Push   *configuredRefspec `json:"push,omitempty"`
}

// fetchConfigKey is the key the mirror refspec belongs to, and pushConfigKey the one the record refspec
// belongs to. Both are lists, so both are read with `--get-all` and written with `--add` (see
// git.Repo.ConfigAdd) — replacing either key would silently drop the refspec the clone was created with.
func fetchConfigKey(remote string) string {
	return "remote." + remote + ".fetch"
}

func pushConfigKey(remote string) string {
	return "remote." + remote + ".push"
}

func newIntegrationConfigureCommand(a *app) *cobra.Command {
	var remoteFlag string
	var fetchOnly bool
	cmd := &cobra.Command{
		Use:   "configure",
		Short: "Make ordinary fetches and pushes carry the durable refs",
		Long: `Write the configuration that sends git-pair's durable refs with the repository, and fetches them back.

  ` + "`remote.<name>.fetch`" + `  ` + reviewref.MirrorRefspec("<name>") + `
  ` + "`remote.<name>.push`" + `  ` + reviewref.PushRefspec + `

The fetch half brings the remote's copies into ` + "`refs/remotes/<name>/refs/git-pair/`" + `, which is what
lets an ordinary ` + "`git fetch`" + ` leave this clone able to tell a published record from one that never
left the clone. The push half sends this clone's own ` + "`refs/git-pair/*`" + ` with every branch, so a
record written here reaches the shared remote without anyone running ` + "`git pair integration publish`" + `.

Both lines are additive: appended with ` + "`git config --add`" + `, once each. Re-running the command writes
nothing and says so, which is what makes it safe in a pipeline and safe in a repository that has its own
refspecs already. This is the only configuration git-pair writes.

` + "`--fetch-only`" + ` writes just the first line, for a clone that should be able to compare a record
against the remote without being the thing that decides the record is public. Publishing one changeset's
pair, now, with verification, remains the job of ` + "`git pair integration publish`" + `: this command changes
what the repository's own git does from here on, and does not push anything itself.

The push refspec carries no ` + "`+`" + `. Both families are create-only, so a remote holding a different
value rejects the push instead of being overwritten by it — the same conflict policy publish uses, and the
reason no git-pair command can move a durable ref.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runIntegrationConfigure(cmd.Context(), remoteFlag, fetchOnly)
		},
	}
	cmd.Flags().StringVar(&remoteFlag, "remote", "", "configure this remote instead of the repository's own")
	cmd.Flags().BoolVar(&fetchOnly, "fetch-only", false,
		"write only the fetch refspec, so an ordinary push keeps publishing branches and not the durable refs")
	return cmd
}

// runIntegrationConfigure writes whichever halves were asked for. The fetch half comes first because it is
// the half every clone needs to read its own reports correctly, and because a failure in the second half
// then leaves a clone that is able to compare — a partial write that is a step forward rather than a
// repository with a push configuration nobody can audit.
func (a *app) runIntegrationConfigure(ctx context.Context, remoteFlag string, fetchOnly bool) error {
	repo, err := a.loadRepo(ctx)
	if err != nil {
		return err
	}
	remote, err := a.configureRemote(ctx, repo, remoteFlag)
	if err != nil {
		return err
	}

	view := integrationConfigJSON{Remote: remote}
	view.Fetch, err = a.configureRefspec(ctx, repo, fetchConfigKey(remote), reviewref.MirrorRefspec(remote))
	if err != nil {
		return err
	}
	if fetchOnly {
		return a.finishIntegrationConfigure(view)
	}
	view.Push, err = a.configureRefspec(ctx, repo, pushConfigKey(remote), reviewref.PushRefspec)
	if err != nil {
		// Half a configuration is a real state and a caller has to be told about it: the fetch line is
		// already in the file, and the next run has to know it is there.
		if view.Fetch != nil && !view.Fetch.Already {
			return fmt.Errorf("%w (%s was written; re-run to finish)", err, view.Fetch.Key)
		}
		return err
	}
	return a.finishIntegrationConfigure(view)
}

// configureRemote resolves the remote the configuration is written to: --remote when named, else the
// remote the durable refs belong to. A flag the caller named that cannot be honoured is a refusal with an
// exit code rather than a warning after the fact, because "succeeded, but the thing you asked for did not
// happen" is the answer a pipeline cannot branch on.
func (a *app) configureRemote(ctx context.Context, repo *git.Repo, remoteFlag string) (string, error) {
	if remoteFlag != "" {
		return namedRemote(ctx, repo, remoteFlag)
	}
	remote, err := a.remoteForDurableRefs(ctx, repo, "")
	if err != nil {
		return "", fmt.Errorf("git-pair: cannot tell which remote to configure: %w", err)
	}
	if remote == "" {
		return "", fmt.Errorf("git-pair: configuring the durable refs needs a remote, and this repository has none.\n" +
			"Add one with `git remote add origin <url>`, or name one with --remote")
	}
	return remote, nil
}

// namedRemote resolves a --remote the caller named. Both commands that take the flag share the refusal,
// because "no such remote" is a fact about the repository and not about the verb being attempted.
func namedRemote(ctx context.Context, repo *git.Repo, remoteFlag string) (string, error) {
	remotes, err := repo.Remotes(ctx)
	if err != nil {
		return "", err
	}
	for _, r := range remotes {
		if r == remoteFlag {
			return remoteFlag, nil
		}
	}
	return "", fmt.Errorf("git-pair: no remote %q; this repository has %s", remoteFlag, orNone(remotes))
}

// configureRefspec appends one refspec to one key, once.
func (a *app) configureRefspec(ctx context.Context, repo *git.Repo, key, refspec string) (*configuredRefspec, error) {
	have, err := repo.ConfigValues(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("git-pair: cannot read %s: %w", key, err)
	}
	if slices.Contains(have, refspec) {
		return &configuredRefspec{Key: key, Refspec: refspec, Already: true}, nil
	}
	if err := repo.ConfigAdd(ctx, key, refspec); err != nil {
		return nil, fmt.Errorf("git-pair: could not write %s: %w", key, err)
	}
	return &configuredRefspec{Key: key, Refspec: refspec}, nil
}

// finishIntegrationConfigure prints the answer. Nothing here can fail: the writes are done, and a report
// about configuration that itself needed configuration would be the wrong kind of clever.
func (a *app) finishIntegrationConfigure(view integrationConfigJSON) error {
	if a.json {
		return a.emitJSON(view)
	}
	wrote := (view.Fetch != nil && !view.Fetch.Already) || (view.Push != nil && !view.Push.Already)
	if wrote {
		a.printf("%s: configured for the durable refs\n", view.Remote)
	} else {
		a.printf("%s: already configured for the durable refs\n", view.Remote)
	}
	if view.Fetch != nil {
		a.printf("  fetch: %s\n", describeRefspec(view.Fetch, "fetches the durable mirrors"))
	}
	if view.Push != nil {
		a.printf("  push:  %s\n", describeRefspec(view.Push, "pushes the durable refs"))
	}
	if view.Push != nil && !view.Push.Already {
		// The configuration is about future pushes. The records this clone already holds have not gone
		// anywhere, and the command that sends them now, and verifies they arrived, is publish.
		a.printf("  next:  git pair integration publish, to send what this clone already holds\n")
	}
	return nil
}

// describeRefspec is one line about one key: the value and where it went, or the fact that it was already
// there. Naming the key in both cases is the point — a run that wrote nothing still has to say which line
// it checked.
func describeRefspec(c *configuredRefspec, done string) string {
	if c.Already {
		return fmt.Sprintf("%s already %s; nothing changed", c.Key, done)
	}
	return fmt.Sprintf("added %s to %s", c.Refspec, c.Key)
}

// configuredAlready reports whether the mirror refspec is already in the remote's fetch list, so the
// hint can stay quiet in a clone that asked for nothing because it has already been given.
func configuredAlready(ctx context.Context, repo *git.Repo, remote string) bool {
	if remote == "" {
		return false
	}
	have, err := repo.ConfigValues(ctx, fetchConfigKey(remote))
	if err != nil {
		return false
	}
	return slices.Contains(have, reviewref.MirrorRefspec(remote))
}

// configureHint is the one short line for the run that left the clone unconfigured: the remedy exists, it
// is one command away, and it is named rather than performed. Silent configuration and silent absence are
// the two answers that would be wrong here, so the line says which one happened.
func configureHint(remote string) string {
	if remote == "" {
		return ""
	}
	// Same value column as the lines above it, which means two spaces fewer after a longer label: the
	// report is a table, and a table whose columns drift is harder to read than one with a longer label.
	// The wrap is chosen so no line of a three-line hint is much longer than the others.
	return fmt.Sprintf("  configure:   git pair integration configure adds the durable refspecs to %s\n"+
		"               and %s, so an ordinary fetch keeps this clone able to tell published from\n"+
		"               unpublished, and an ordinary push keeps what it records published\n",
		fetchConfigKey(remote), pushConfigKey(remote))
}
