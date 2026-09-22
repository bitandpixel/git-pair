package cli

import (
	"context"
	"fmt"

	"gitpair/internal/git"
	"gitpair/internal/reviewref"
)

// `--configure-fetch` is consent, spelled as a flag.
//
// The alternative was a prompt on first record — "would you like this clone to keep fetching the durable
// refs?" — and it was rejected for a reason that is not about politeness. PRD §22 makes this CLI the agent
// surface, so a TTY-dependent question would make one command line mean two things depending on where it
// ran; and in CI an unanswered prompt is indistinguishable from a declined one, which is precisely the
// failure that leaves a paper trail unpublished. Consent that is visible in a pipeline definition and
// greppable in a log is worth more than consent that asks nicely in the right terminal. If interactive
// confirmation is wanted later it belongs in the TUI, which is already a conversation.
//
// What it writes is also a decision. Only the *mirror* refspec goes into `remote.<name>.fetch`, never the
// record refspec: a record is a claim that a landing happened, and a clone should acquire claims by asking
// (`--fetch`), not because a configuration line written weeks earlier keeps delivering them. The mirrors are
// the comparison, and the comparison is what an ordinary `git fetch` should keep able to make — that is the
// difference between a clone that can tell published from unpublished and one that has to be told to look.

// fetchConfig is the result of `--configure-fetch`: the exact key and value, and whether they were already
// there. Reported rather than implied, because "I configured it" and "it was already configured" are
// different answers to the question a pipeline is asking.
type fetchConfig struct {
	Remote  string `json:"remote"`
	Key     string `json:"key"`
	Refspec string `json:"refspec"`
	// Already is git-pair reporting that it wrote nothing, which an idempotent step must be able to say.
	Already bool `json:"already_configured"`
}

// fetchConfigKey is the config key the refspec belongs to. Fetch refspecs are a list, so this is read with
// `--get-all` and written with `--add` (see git.Repo.ConfigAdd) — replacing the key would silently drop the
// branch fetch refspec the clone was created with.
func fetchConfigKey(remote string) string {
	return "remote." + remote + ".fetch"
}

// requireConfigurableRemote resolves the remote `--configure-fetch` would write, or refuses. Checked
// before the refs are written and separate from the write itself: a flag the caller asked for that cannot
// be honoured is a refusal with an exit code, not a warning after the fact — "succeeded, but the thing you
// asked for did not happen" is the answer a pipeline cannot branch on.
func (a *app) requireConfigurableRemote(ctx context.Context, repo *git.Repo) (string, error) {
	remote, err := a.remoteForDurableRefs(ctx, repo, "")
	if err != nil {
		return "", err
	}
	if remote == "" {
		return "", fmt.Errorf("git-pair: --configure-fetch needs a remote to configure, and this repository has none.\n" +
			"Add one with `git remote add origin <url>`; the record itself needs no remote and can be re-run")
	}
	return remote, nil
}

// configureFetchRefSpecs writes the mirror refspec for one remote, once.
func (a *app) configureFetchRefSpecs(ctx context.Context, repo *git.Repo, remote string) (*fetchConfig, error) {
	if remote == "" {
		return nil, fmt.Errorf("git-pair: --configure-fetch needs a remote to configure, and this repository has none")
	}
	key, spec := fetchConfigKey(remote), reviewref.MirrorRefspec(remote)
	have, err := repo.ConfigValues(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("git-pair: cannot read %s: %w", key, err)
	}
	for _, v := range have {
		if v == spec {
			return &fetchConfig{Remote: remote, Key: key, Refspec: spec, Already: true}, nil
		}
	}
	if err := repo.ConfigAdd(ctx, key, spec); err != nil {
		return nil, fmt.Errorf("git-pair: could not write %s: %w", key, err)
	}
	return &fetchConfig{Remote: remote, Key: key, Refspec: spec}, nil
}

// configuredAlready reports whether the mirror refspec is already in the remote's fetch list, so the
// hint can stay quiet in a clone that asked for nothing because it has already been given.
func configuredAlready(ctx context.Context, repo *git.Repo, remote string) bool {
	spec := reviewref.MirrorRefspec(remote)
	have, err := repo.ConfigValues(ctx, fetchConfigKey(remote))
	if err != nil {
		return false
	}
	for _, v := range have {
		if v == spec {
			return true
		}
	}
	return false
}

// fetchHint is the one short line for the run that did not ask to be configured: the remedy exists, it is
// one flag away, and it is named rather than performed. Silent configuration and silent absence are the two
// answers that would be wrong here, so the line says which one happened.
func fetchHint(remote string) string {
	if remote == "" {
		return ""
	}
	// Same value column as the lines above it, which means two spaces fewer after a longer label: the
	// report is a table, and a table whose columns drift is harder to read than one with a longer label.
	return fmt.Sprintf("  configure:   --configure-fetch adds %s to %s, so an ordinary fetch keeps "+
		"this clone able to tell published from unpublished\n", reviewref.MirrorRefspec(remote), fetchConfigKey(remote))
}
