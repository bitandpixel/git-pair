package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"gitpair/internal/git"
	"gitpair/skills"
)

// The skill is a surface of the tool, so the tool knows where it is.
//
// Two things follow from shipping an agent skill in the repository. It has to be compiled in, because a
// binary installed with `go install` has no source tree next to it, and its installed copies have to be
// checkable against it, because a skill that drifted from the commands it documents is the failure this
// repository has already lived through once. `git pair skill list` is that check: it answers "is the
// skill my agent reading the one this git-pair ships?" from the filesystem, without trusting anybody to
// have re-copied anything.

// skillTarget is one place a harness would read this skill from, and what this binary finds there.
type skillTarget struct {
	Harness string `json:"harness"`
	Scope   string `json:"scope"`
	// Path is the skill's own directory — the `git-pair` folder inside a skills directory a harness
	// scans. The state below is a statement about those bytes, and a reader comparing two of these
	// wants the directory that has to exist, not its parent.
	Path  string `json:"path"`
	State string `json:"state"`
	// Unmanaged names files in an installed skill that git-pair did not write. Anything that writes a
	// skill into a directory must leave them alone, and they are worth reporting everywhere else too:
	// a hand-written note beside the shipped skill is a thing the next reader should know is there.
	Unmanaged []string `json:"unmanaged,omitempty"`
}

// The states of an installed skill. `current` is the whole point of the command: bytes equal to this
// binary's copy, which is the only claim about a version that does not depend on a version number
// somebody remembered to bump.
const (
	skillStateCurrent     = "current"
	skillStateStale       = "stale"
	skillStateAbsent      = "absent"
	skillStateUnavailable = "unavailable"
)

// skillHarness is one family of skill locations. The two paths come from each harness's own documented
// discovery rules, and `agents` is first because it is the one place that serves more than one harness:
// Codex CLI and pi both read `.agents/skills/` in the repository and `~/.agents/skills/` in the home
// directory, so a committed `.agents/skills/git-pair/` needs no second copy for the other one.
type skillHarness struct {
	name string
	repo string
	user string
}

var skillHarnesses = []skillHarness{
	{name: "agents", repo: ".agents/skills", user: ".agents/skills"},
	{name: "pi", repo: ".pi/skills", user: ".pi/agent/skills"},
	{name: "claude", repo: ".claude/skills", user: ".claude/skills"},
}

func newSkillCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "The agent skill that ships with git-pair",
		Long: `git-pair ships its agent skill: the contract an agent follows in a repository that reviews
changes with git-pair. It lives in this repository at ` + "`skills/git-pair/`" + `, and it is
compiled into this binary, so an installed copy can be checked against the tool it describes rather
than against whoever copied it.

Skill directories are what coding agents read at startup. Every harness that implements the Agent
Skills standard reads a directory of skill folders; the names below are the ones git-pair knows.`,
		// Without a RunE cobra treats an unmatched subcommand as a help request and exits 0,
		// which is indistinguishable from success for an agent that typo'd the verb.
		RunE: groupUsage("skill"),
	}
	cmd.AddCommand(newSkillListCommand(a), newSkillShowCommand(a))
	return cmd
}

func newSkillListCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Show where the skill is installed, and whether it is current",
		Long: `List the skill compiled into this binary and every directory a harness would read it from.

Each location is current, stale, absent or unavailable. current means the installed bytes match this
binary's, which is the only claim about a version that does not depend on a version number somebody
remembered to bump; stale means an older copy is installed there, and an agent reading it is reading
instructions for a different tool than the one it is using.

unavailable means the location has no home here: a repository-scoped row run outside any repository,
or a user-scoped row with no home directory to resolve. Those rows are printed rather than dropped,
because the table is the answer to "where could this go" as much as to "where is it".

Read-only: nothing here touches the filesystem beyond stat and read.`,
		Example: `  git pair skill list
  git pair skill list --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillList(cmd.Context(), a)
		},
	}
	return cmd
}

func newSkillShowCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show [path]",
		Short: "Print the skill compiled into this binary",
		Long: `Print the skill as this binary holds it, so you can read or copy it without a checkout.

With no argument this prints SKILL.md. A path names a file inside the skill — ` +
			"`references/cli.md`" + `, or ` + "`SKILL.md`" + ` — as ` + "`git pair skill list`" + `
lists them, with or without the leading skill directory.

This is the copy an agent reads once it is installed, so what you read here is what your repository
would be telling it.`,
		Example: `  git pair skill show
  git pair skill show references/cli.md`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillShow(a, args)
		},
	}
	return cmd
}

func runSkillList(ctx context.Context, a *app) error {
	repoRoot, err := skillRepoRoot(ctx, a)
	if err != nil {
		return err
	}
	targets := skillTargets(repoRoot)
	home, _ := os.UserHomeDir()
	if a.json {
		return a.emitJSON(struct {
			Skill      string        `json:"skill"`
			Version    string        `json:"version"`
			Files      []string      `json:"files"`
			Repository string        `json:"repository"`
			Targets    []skillTarget `json:"targets"`
		}{skills.Name, Version, skills.Files(), repoRoot, orEmpty(targets)})
	}

	a.printf("git-pair skill %q — %d files, from git-pair %s\n\n", skills.Name, len(skills.Files()), Version)
	width := 0
	for _, t := range targets {
		if n := len(t.display(repoRoot, home)); n > width {
			width = n
		}
	}
	for _, t := range targets {
		a.printf("  %-7s %-5s  %-*s  %s\n", t.Harness, t.Scope, width, t.display(repoRoot, home), t.State)
	}
	a.printf("\ncurrent is the same bytes this git-pair ships; stale is an older copy, read as fact.\n")
	return nil
}

func runSkillShow(a *app, args []string) error {
	file := "SKILL.md"
	if len(args) == 1 {
		file = strings.TrimPrefix(args[0], skills.Name+"/")
	}
	data, err := skills.Read(file)
	if err != nil {
		return &usageError{fmt.Errorf("no %q in the %s skill; it has: %s",
			file, skills.Name, strings.Join(skills.Files(), ", "))}
	}
	a.printf("%s", data)
	return nil
}

// skillRepoRoot is the repository a repository-scoped install would write into, or the empty string
// when there is none.
//
// It does not carry `loadRepo`'s exit 3 for an absent repository, because these commands read the
// filesystem rather than the repository and the absence of one is a row in a table. A repository that
// is present and broken is a different matter, and still fails.
func skillRepoRoot(ctx context.Context, a *app) (string, error) {
	repo, err := a.loadRepo(ctx)
	if errors.Is(err, git.ErrNotRepository) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return repo.Dir, nil
}

// skillTargets builds the table `skill list` prints, in harness order with each harness's repository
// row before its home row. Nothing about this is a search: a skill directory is a documented location,
// and guessing at a fourth one would install the skill somewhere no harness reads.
func skillTargets(repoRoot string) []skillTarget {
	home, homeErr := os.UserHomeDir()
	var out []skillTarget
	for _, h := range skillHarnesses {
		for _, scope := range []string{"repo", "user"} {
			target := skillTarget{Harness: h.name, Scope: scope}
			switch {
			case scope == "repo" && repoRoot == "":
				target.State = skillStateUnavailable
			case scope == "user" && homeErr != nil:
				target.State = skillStateUnavailable
			case scope == "repo":
				target.Path = filepath.Join(repoRoot, h.repo, skills.Name)
			default:
				target.Path = filepath.Join(home, h.user, skills.Name)
			}
			if target.Path != "" {
				state := inspectSkill(target.Path)
				target.State, target.Unmanaged = state.State, state.Unmanaged
			}
			out = append(out, target)
		}
	}
	return out
}

// inspectSkill compares one installed skill directory against the skill compiled into this binary.
//
// A directory that cannot be read is stale rather than current: nothing proves it matches, and reporting
// a match that was not verified is the failure mode this whole command exists to avoid.
func inspectSkill(root string) skillTarget {
	if _, err := os.Stat(root); err != nil {
		return skillTarget{State: skillStateAbsent}
	}
	state := skillTarget{State: skillStateCurrent}
	shipped := map[string]bool{}
	for _, rel := range skills.Files() {
		shipped[rel] = true
		want, err := skills.Read(rel)
		if err != nil {
			continue
		}
		have, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil || !bytes.Equal(want, have) {
			state.State = skillStateStale
		}
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, cut := strings.CutPrefix(p, root+string(filepath.Separator))
		if cut && !shipped[rel] {
			state.Unmanaged = append(state.Unmanaged, rel)
		}
		return nil
	})
	return state
}

// display shortens a target path for a terminal: repository paths relative to the repository, home
// paths under $HOME. The JSON carries the absolute path, because a machine comparing two of these
// wants the whole thing.
func (t skillTarget) display(repoRoot, home string) string {
	if t.Path == "" {
		if t.Scope == "repo" {
			return "(not inside a git repository)"
		}
		return "(no home directory)"
	}
	if repoRoot != "" && strings.HasPrefix(t.Path, repoRoot+string(filepath.Separator)) {
		return strings.TrimPrefix(t.Path, repoRoot+string(filepath.Separator))
	}
	if home != "" && strings.HasPrefix(t.Path, home+string(filepath.Separator)) {
		return "$HOME/" + strings.TrimPrefix(t.Path, home+string(filepath.Separator))
	}
	return t.Path
}
