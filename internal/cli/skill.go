package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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
Skills standard reads a directory of skill folders: ` + "`.agents/skills`" + ` serves Codex CLI and pi,
` + "`.pi/skills`" + ` and ` + "`.claude/skills`" + ` serve pi and Claude Code, and ` +
			"`git pair skill install --dest`" + ` reaches anywhere else. Each is the container; the skill
itself is the ` + "`git-pair`" + ` directory inside it.`,
		// Without a RunE cobra treats an unmatched subcommand as a help request and exits 0,
		// which is indistinguishable from success for an agent that typo'd the verb.
		RunE: groupUsage("skill"),
	}
	cmd.AddCommand(newSkillListCommand(a), newSkillShowCommand(a), newSkillInstallCommand(a),
		newSkillAgentsMDCommand(a))
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
	for _, rel := range skills.Files() {
		want, err := skills.Read(rel)
		if err != nil {
			continue
		}
		have, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil || !bytes.Equal(want, have) {
			state.State = skillStateStale
		}
	}
	state.Unmanaged = unmanagedFiles(root)
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
	return displayedSkillPath(t.Path, repoRoot, home)
}

// displayedSkillPath shortens a path the way a terminal wants it: relative to the repository when it is
// inside one, under $HOME when it is in the home directory, and absolute otherwise — which is what
// `--dest` produces, and the case where an absolute path is the clearest answer.
func displayedSkillPath(path, repoRoot, home string) string {
	if repoRoot != "" && strings.HasPrefix(path, repoRoot+string(filepath.Separator)) {
		return strings.TrimPrefix(path, repoRoot+string(filepath.Separator))
	}
	if home != "" && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "$HOME/" + strings.TrimPrefix(path, home+string(filepath.Separator))
	}
	return path
}

// --- install ----------------------------------------------------------------

// skillInstallOptions is what the caller asked for. Every field is optional, and the defaults are the
// point: `git pair skill install` run inside a repository puts the skill where the harnesses that read
// `.agents/skills` will find it, in one directory the author then commits.
type skillInstallOptions struct {
	harness string
	scope   string
	dest    string
	dryRun  bool
	force   bool
}

// skillHarnessAliases are spellings that mean another harness's locations. Codex CLI reads
// `.agents/skills` and `~/.agents/skills`, so the name its users type resolves to the row that is
// already true for it rather than to a fourth copy of the skill.
var skillHarnessAliases = map[string]string{"codex": "agents"}

func newSkillInstallCommand(a *app) *cobra.Command {
	opts := &skillInstallOptions{}
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Write the skill into a harness's skills directory",
		Long: `Write the skill compiled into this binary into a skills directory, so an agent in this
repository or on this machine reads it.

The destination is a skills directory; the skill itself is written as the ` + "`git-pair`" + ` directory
inside it, with its ` + "`references/`" + ` beside ` + "`SKILL.md`" + `. ` + "`--harness agents`" + ` writes
` + "`.agents/skills`" + `, which Codex CLI and pi both read, and is the default.

` + "`--scope repo`" + ` (the default inside a repository) writes into the repository, which is the way to
make the contract travel: commit the directory and every clone, worktree and CI job gets the same copy.
` + "`--scope user`" + ` writes into the home directory, which is the way to give it to yourself everywhere.

What is already there is never deleted. Files that match this binary are left alone, files that differ
are refused until ` + "`--force`" + ` says otherwise, and files git-pair did not write are reported and
kept.`,
		Example: `  git pair skill install
  git pair skill install --harness claude --scope user
  git pair skill install --dest /srv/shared/skills --dry-run`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillInstall(cmd, a, opts)
		},
	}
	cmd.Flags().StringVar(&opts.harness, "harness", "agents",
		"which harness's skill directories to write: agents, pi or claude")
	cmd.Flags().StringVar(&opts.scope, "scope", "",
		"repo (the default inside a git repository) or user (the default outside one)")
	cmd.Flags().StringVar(&opts.dest, "dest", "", "write into this skills directory instead of a harness's own")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "resolve the destination and report, writing nothing")
	cmd.Flags().BoolVar(&opts.force, "force", false, "replace installed files that differ from this binary's copy")
	return cmd
}

func runSkillInstall(cmd *cobra.Command, a *app, opts *skillInstallOptions) error {
	if opts.dest != "" && (cmd.Flags().Changed("harness") || cmd.Flags().Changed("scope")) {
		return &usageError{fmt.Errorf("--dest names the directory to write into, so --harness and --scope are not also needed")}
	}
	switch opts.scope {
	case "", "repo", "user":
	default:
		return &usageError{fmt.Errorf("unknown scope %q; use repo or user", opts.scope)}
	}

	repoRoot, err := skillRepoRoot(cmd.Context(), a)
	if err != nil {
		return err
	}
	home := homeDir()
	container, scope, harnessName, err := skillContainer(opts, repoRoot)
	if err != nil {
		return err
	}
	dir := filepath.Join(container, skills.Name)
	plan, err := planSkillInstall(dir)
	if err != nil {
		return err
	}
	// `--dry-run` refuses too. Its whole claim is that what it reports is what a real install would do,
	// and a dry run that quietly planned past a conflict would predict success where the real run stops.
	if len(plan.conflicts) > 0 && !opts.force {
		return fmt.Errorf("%s already holds files that differ from this git-pair's copy:\n  %s\n"+
			"Re-run with --force to replace them. Files git-pair did not write are left alone.",
			displayedSkillPath(dir, repoRoot, home), strings.Join(plan.conflicts, "\n  "))
	}

	written := append([]string{}, plan.wouldWrite...)
	if opts.force {
		written = append(written, plan.conflicts...)
	}
	if !opts.dryRun {
		for _, rel := range written {
			path := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
			}
			data, err := skills.Read(rel)
			if err != nil {
				return err
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
		}
	}
	return printSkillInstall(a, skillInstallReport{
		Harness: harnessName, Scope: scope, Dest: container, Path: dir,
		Shown:  displayedSkillPath(dir, repoRoot, home),
		DryRun: opts.dryRun, Written: written, Unchanged: plan.unchanged, Unmanaged: plan.unmanaged,
	})
}

// skillInstallReport is what an install did, in the shape both output forms want.
type skillInstallReport struct {
	Harness   string
	Scope     string
	Dest      string
	Path      string
	Shown     string
	DryRun    bool
	Written   []string
	Unchanged []string
	Unmanaged []string
}

func printSkillInstall(a *app, r skillInstallReport) error {
	if a.json {
		return a.emitJSON(struct {
			Skill     string   `json:"skill"`
			Version   string   `json:"version"`
			Harness   string   `json:"harness"`
			Scope     string   `json:"scope"`
			Dest      string   `json:"dest"`
			Path      string   `json:"path"`
			DryRun    bool     `json:"dry_run"`
			Written   []string `json:"written"`
			Unchanged []string `json:"unchanged"`
			Unmanaged []string `json:"unmanaged"`
		}{skills.Name, Version, r.Harness, r.Scope, r.Dest, r.Path, r.DryRun,
			orEmpty(r.Written), orEmpty(r.Unchanged), orEmpty(r.Unmanaged)})
	}

	verb := "installed"
	if r.DryRun {
		verb = "would install"
	}
	if len(r.Written) == 0 {
		a.printf("nothing to write: %s already holds this git-pair's copy\n", r.Shown)
	} else {
		a.printf("%s the %s skill into %s\n", verb, skills.Name, r.Shown)
		for _, rel := range r.Written {
			a.printf("  wrote      %s\n", rel)
		}
	}
	for _, rel := range r.Unchanged {
		a.printf("  unchanged  %s\n", rel)
	}
	for _, rel := range r.Unmanaged {
		a.printf("  kept       %s (not written by git-pair)\n", rel)
	}
	if r.DryRun {
		return nil
	}
	switch {
	case r.Dest == "":
	case r.Scope == "repo":
		a.printf("\nCommit %s so every clone, worktree and CI job gets the same copy.\n", r.Shown)
	default:
		a.printf("\nEvery repository this account opens now has the skill.\n")
	}
	a.printf("A harness that was already running has read its skill directories: restart it if the skill does not appear.\n")
	return nil
}

// skillHarnessNamed resolves a `--harness` value, alias included.
func skillHarnessNamed(name string) (skillHarness, error) {
	if alias, ok := skillHarnessAliases[name]; ok {
		name = alias
	}
	for _, h := range skillHarnesses {
		if h.name == name {
			return h, nil
		}
	}
	known := make([]string, 0, len(skillHarnesses))
	for _, h := range skillHarnesses {
		known = append(known, h.name)
	}
	aliases := make([]string, 0, len(skillHarnessAliases))
	for alias, target := range skillHarnessAliases {
		aliases = append(aliases, fmt.Sprintf("%s means %s", alias, target))
	}
	sort.Strings(known)
	sort.Strings(aliases)
	return skillHarness{}, &usageError{fmt.Errorf(
		"unknown harness %q; git-pair knows %s (%s) — point --dest at a skills directory for any other harness",
		name, strings.Join(known, ", "), strings.Join(aliases, "; "))}
}

// skillContainer is the skills directory the install writes into, the scope that produced it, and the
// harness it resolved to. An explicit `--dest` outranks everything: the scope and harness it reports are
// then empty, because neither was consulted.
func skillContainer(opts *skillInstallOptions, repoRoot string) (container, scope, harness string, err error) {
	if opts.dest != "" {
		return opts.dest, "", "", nil
	}
	h, err := skillHarnessNamed(opts.harness)
	if err != nil {
		return "", "", "", err
	}
	scope = opts.scope
	if scope == "" {
		scope = "user"
		if repoRoot != "" {
			scope = "repo"
		}
	}
	if scope == "repo" {
		if repoRoot == "" {
			return "", "", "", &usageError{fmt.Errorf("--scope repo needs a git repository: run it inside one, " +
				"use --scope user, or name a skills directory with --dest")}
		}
		return filepath.Join(repoRoot, h.repo), scope, h.name, nil
	}
	home, herr := os.UserHomeDir()
	if herr != nil {
		return "", "", "", fmt.Errorf("no home directory to install into; name one with --dest: %w", herr)
	}
	return filepath.Join(home, h.user), scope, h.name, nil
}

// skillInstallPlan is the difference between an installed skill and this binary's copy, sorted into
// what would be written, what is already right, and what is in the way.
type skillInstallPlan struct {
	wouldWrite []string
	unchanged  []string
	conflicts  []string
	unmanaged  []string
}

// planSkillInstall compares a directory against the compiled-in skill without touching it.
//
// A file that cannot be read is a conflict rather than an unchanged: nothing proves it matches, and an
// install that reported "already current" about bytes it never compared is the exact lie this command
// exists to prevent.
func planSkillInstall(dir string) (skillInstallPlan, error) {
	var plan skillInstallPlan
	for _, rel := range skills.Files() {
		want, err := skills.Read(rel)
		if err != nil {
			return plan, err
		}
		have, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			plan.wouldWrite = append(plan.wouldWrite, rel)
		case err != nil:
			plan.conflicts = append(plan.conflicts, rel)
		case bytes.Equal(want, have):
			plan.unchanged = append(plan.unchanged, rel)
		default:
			plan.conflicts = append(plan.conflicts, rel)
		}
	}
	if _, err := os.Stat(dir); err == nil {
		plan.unmanaged = unmanagedFiles(dir)
	}
	return plan, nil
}

// unmanagedFiles lists what an installed skill holds that git-pair did not write — a team's own notes
// beside the shipped contract, most often. They are reported and never removed.
func unmanagedFiles(root string) []string {
	shipped := map[string]bool{}
	for _, rel := range skills.Files() {
		shipped[rel] = true
	}
	var extra []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, cut := strings.CutPrefix(p, root+string(filepath.Separator))
		if cut && !shipped[rel] {
			extra = append(extra, rel)
		}
		return nil
	})
	return extra
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// --- agents-md ---------------------------------------------------------------

func newSkillAgentsMDCommand(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents-md",
		Short: "Print the AGENTS.md stanza for a repository that uses git-pair",
		Long: `Print the stanza that tells an agent this repository reviews changes with git-pair, for a
harness that reads AGENTS.md or CLAUDE.md and has no skill discovery of its own.

It is short by design: it says where the skill is and names the rules an agent most often gets wrong.
The skill itself stays the contract. Append the stanza and commit it:

  git pair skill agents-md >> AGENTS.md

A repository can carry both halves — the stanza for every harness, and the installed skill directory
for the ones that read it.`,
		Example: `  git pair skill agents-md >> AGENTS.md`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a.printf("%s", skills.AgentsMDPointer())
			return nil
		},
	}
	return cmd
}
