package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/skills"
)

// An install has one job that everything else is in service of: the directory it leaves behind holds
// exactly this binary's skill, and nothing that was already there is lost. These tests are the four
// answers it can give — nothing there, that same content, content that differs, and content that is not
// git-pair's — plus the ways a caller can ask for the wrong directory.

// skillInstallJSON is one `skill install --json` answer, read here rather than through the
// implementation's type so a renamed field fails a test instead of compiling.
type skillInstallJSON struct {
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
}

func installed(t *testing.T, res result) skillInstallJSON {
	t.Helper()
	var out skillInstallJSON
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("decode install output: %v\n%s", err, res.stdout)
	}
	return out
}

// onDisk reads one file of an installed skill, /-separated as the skill names it.
func onDisk(t *testing.T, dir, rel string) (string, error) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	return string(data), err
}

func TestSkillInstallWritesThisBinarysSkill(t *testing.T) {
	f := newRepo(t)
	isolatedHome(t)

	dry := installed(t, runIn(t, f.Dir(), "skill", "install", "--dry-run", "--json").
		mustSucceed(t, "skill", "install"))
	if !dry.DryRun || dry.Path == "" {
		t.Fatalf("dry run reported dry_run=%v path=%q, want both set\n%s", dry.DryRun, dry.Path, dry.Written)
	}
	if len(dry.Written) != len(skills.Files()) {
		t.Errorf("dry run would write %d files, want the whole skill (%d)", len(dry.Written), len(skills.Files()))
	}
	if _, err := os.Stat(dry.Path); !os.IsNotExist(err) {
		t.Errorf("--dry-run created %s", dry.Path)
	}

	res := runIn(t, f.Dir(), "skill", "install", "--json").mustSucceed(t, "skill", "install")
	got := installed(t, res)
	if got.Skill != "git-pair" || got.Harness != "agents" || got.Scope != "repo" {
		t.Errorf("install reported %s/%s into %q, want git-pair/agents/repo\n%s",
			got.Harness, got.Scope, got.Skill, res.stdout)
	}
	if want := filepath.Join(f.Dir(), ".agents", "skills", "git-pair"); got.Path != want {
		t.Errorf("path = %q, want %q", got.Path, want)
	}
	for _, rel := range skills.Files() {
		want, err := skills.Read(rel)
		if err != nil {
			t.Fatalf("read %s from the skill: %v", rel, err)
		}
		have, err := onDisk(t, got.Path, rel)
		if err != nil {
			t.Errorf("install left no %s: %v", rel, err)
			continue
		}
		if have != string(want) {
			t.Errorf("installed %s differs from the compiled skill", rel)
		}
	}

	// A second run is a success that changed nothing, and says so: an agent that installs on every
	// setup must be able to tell that from having just written the files.
	again := installed(t, runIn(t, f.Dir(), "skill", "install", "--json").
		mustSucceed(t, "skill", "install"))
	if len(again.Written) != 0 || len(again.Unchanged) != len(skills.Files()) {
		t.Errorf("second install wrote %v and left %v unchanged, want nothing written and all unchanged",
			again.Written, again.Unchanged)
	}

	// The point of compiling it in: the install is what makes `list` answer `current`.
	list := runIn(t, f.Dir(), "skill", "list", "--json").mustSucceed(t, "skill", "list")
	if state := findSkillTarget(t, skillTargetsOf(t, list), "agents", "repo").State; state != "current" {
		t.Errorf(`after install, agents/repo reads %q, want "current"`, state)
	}
}

func TestSkillInstallRefusesToOverwriteADifferentFile(t *testing.T) {
	f := newRepo(t)
	isolatedHome(t)
	dir := filepath.Join(f.Dir(), ".agents", "skills", "git-pair")
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatalf("create the skill directory: %v", err)
	}
	const ours = "written by hand, not by git-pair\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(ours), 0o644); err != nil {
		t.Fatalf("write the hand-written SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("team notes\n"), 0o644); err != nil {
		t.Fatalf("write the unmanaged file: %v", err)
	}

	res := runIn(t, f.Dir(), "skill", "install")
	if res.code != exitRefusal {
		t.Fatalf("install over a different file exited %d, want %d\nstdout: %s", res.code, exitRefusal, res.stdout)
	}
	for _, want := range []string{"SKILL.md", "--force"} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("the refusal never names %q\nstderr: %s", want, res.stderr)
		}
	}
	if have, err := onDisk(t, dir, "SKILL.md"); err != nil || have != ours {
		t.Errorf("the refused install changed SKILL.md: %q", have)
	}

	forced := installed(t, runIn(t, f.Dir(), "skill", "install", "--force", "--json").
		mustSucceed(t, "skill", "install", "--force"))
	if have, err := onDisk(t, dir, "SKILL.md"); err != nil {
		t.Errorf("--force left no SKILL.md: %v", err)
	} else if want, _ := skills.Read("SKILL.md"); have != string(want) {
		t.Errorf("--force did not replace SKILL.md")
	}
	// "replaced" rather than "wrote": an overwrite of a file somebody else made is the thing a person
	// scanning this output needs to see, and it is not the same event as writing a new file. The file is
	// made to differ again because the run above has already brought it back to this binary's bytes.
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("somebody else's\n"), 0o644); err != nil {
		t.Fatalf("write the conflicting SKILL.md again: %v", err)
	}
	human := runIn(t, f.Dir(), "skill", "install", "--force").
		mustSucceed(t, "skill", "install", "--force")
	if !strings.Contains(human.stdout, "replaced   SKILL.md") {
		t.Errorf("--force did not say which files it replaced\n%s", human.stdout)
	}
	// The file git-pair did not write survives the install and is named, so the next reader knows it
	// is there and knows who did not write it.
	if have, err := onDisk(t, dir, "notes.md"); err != nil || have != "team notes\n" {
		t.Errorf("--force removed or changed notes.md: %q, %v", have, err)
	}
	if len(forced.Unmanaged) != 1 || forced.Unmanaged[0] != "notes.md" {
		t.Errorf("unmanaged = %v, want notes.md", forced.Unmanaged)
	}
}

// A dry run predicts the real run, and the real run refuses. A --dry-run that planned happily past a
// conflicting file would tell a caller to expect success where the command stops.
func TestSkillInstallDryRunRefusesTheSameThing(t *testing.T) {
	f := newRepo(t)
	isolatedHome(t)
	dir := filepath.Join(f.Dir(), ".agents", "skills", "git-pair")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create the skill directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("somebody else's\n"), 0o644); err != nil {
		t.Fatalf("write the conflicting SKILL.md: %v", err)
	}

	res := runIn(t, f.Dir(), "skill", "install", "--dry-run")
	if res.code != exitRefusal {
		t.Fatalf("dry run over a different file exited %d, want %d\nstdout: %s",
			res.code, exitRefusal, res.stdout)
	}
	if !strings.Contains(res.stderr, "--force") {
		t.Errorf("the dry-run refusal does not name the way out\nstderr: %s", res.stderr)
	}
	if have, err := onDisk(t, dir, "SKILL.md"); err != nil || have != "somebody else's\n" {
		t.Errorf("a refused dry run changed the file: %q, %v", have, err)
	}
}

// With no home to resolve, a user-scoped install has nowhere to write. That is the repository saying no
// to a correct command — exit 1 — and the message carries the way out.
func TestSkillInstallWithoutAHomeRefuses(t *testing.T) {
	t.Setenv("HOME", "")
	res := runIn(t, t.TempDir(), "skill", "install")
	if res.code != exitRefusal {
		t.Fatalf("install with no home exited %d, want %d\nstderr: %s", res.code, exitRefusal, res.stderr)
	}
	if !strings.Contains(res.stderr, "--dest") {
		t.Errorf("the refusal never names --dest, the only way to answer it\nstderr: %s", res.stderr)
	}
}

func TestSkillInstallResolvesTheDirectoryItWasAskedFor(t *testing.T) {
	f := newRepo(t)
	home := isolatedHome(t)

	cases := []struct {
		args      []string
		harness   string
		scope     string
		wantInDir string
	}{
		{args: []string{}, harness: "agents", scope: "repo", wantInDir: ".agents/skills/git-pair"},
		{args: []string{"--harness", "pi"}, harness: "pi", scope: "repo", wantInDir: ".pi/skills/git-pair"},
		{args: []string{"--harness", "claude", "--scope", "user"}, harness: "claude", scope: "user",
			wantInDir: ".claude/skills/git-pair"},
		// Codex CLI reads the `.agents/skills` locations, so the name its users type resolves to the
		// row that is already true for it rather than to a fourth copy of the skill.
		{args: []string{"--harness", "codex"}, harness: "agents", scope: "repo", wantInDir: ".agents/skills/git-pair"},
	}
	for _, tc := range cases {
		args := append([]string{"skill", "install", "--json"}, tc.args...)
		got := installed(t, runIn(t, f.Dir(), args...).mustSucceed(t, args...))
		if got.Harness != tc.harness || got.Scope != tc.scope {
			t.Errorf("git pair %v reported %s/%s, want %s/%s", tc.args, got.Harness, got.Scope, tc.harness, tc.scope)
		}
		base := f.Dir()
		if tc.scope == "user" {
			base = home
		}
		want := filepath.Join(base, filepath.FromSlash(tc.wantInDir))
		if got.Path != want {
			t.Errorf("git pair %v wrote into %q, want %q", tc.args, got.Path, want)
		}
	}
}

func TestSkillInstallRefusesADestinationItCannotResolve(t *testing.T) {
	f := newRepo(t)
	isolatedHome(t)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown harness", []string{"--harness", "cursor"}, "cursor"},
		{"unknown scope", []string{"--scope", "machine"}, "machine"},
		{"dest with harness", []string{"--dest", "x", "--harness", "pi"}, "--dest"},
		{"dest with scope", []string{"--dest", "x", "--scope", "user"}, "--dest"},
	}
	for _, tc := range cases {
		args := append([]string{"skill", "install"}, tc.args...)
		res := runIn(t, f.Dir(), args...)
		if res.code != exitUsage {
			t.Errorf("%s: git pair %v exited %d, want %d\nstderr: %s", tc.name, tc.args, res.code, exitUsage, res.stderr)
			continue
		}
		if !strings.Contains(res.stderr, tc.want) {
			t.Errorf("%s: the refusal never names %q\nstderr: %s", tc.name, tc.want, res.stderr)
		}
	}
	// The unknown-harness refusal is where a caller learns about --dest, so it has to say it.
	res := runIn(t, f.Dir(), "skill", "install", "--harness", "cursor")
	if !strings.Contains(res.stderr, "--dest") || !strings.Contains(res.stderr, "agents") {
		t.Errorf("the unknown-harness refusal names neither --dest nor the harnesses that exist\nstderr: %s", res.stderr)
	}
}

// Outside a repository the default destination is the home directory, and asking for the repository is a
// usage error with both ways out in the message.
func TestSkillInstallOutsideARepository(t *testing.T) {
	home := isolatedHome(t)
	dir := t.TempDir()

	got := installed(t, runIn(t, dir, "skill", "install", "--json").mustSucceed(t, "skill", "install"))
	if got.Scope != "user" {
		t.Errorf("scope = %q outside a repository, want \"user\"", got.Scope)
	}
	if want := filepath.Join(home, ".agents", "skills", "git-pair"); got.Path != want {
		t.Errorf("path = %q, want %q", got.Path, want)
	}

	res := runIn(t, dir, "skill", "install", "--scope", "repo")
	if res.code != exitUsage {
		t.Fatalf("`skill install --scope repo` outside a repository exited %d, want %d\nstderr: %s",
			res.code, exitUsage, res.stderr)
	}
	for _, want := range []string{"--scope user", "--dest"} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("the refusal never offers %q\nstderr: %s", want, res.stderr)
		}
	}
}

func TestSkillInstallToAnArbitraryDirectory(t *testing.T) {
	f := newRepo(t)
	isolatedHome(t)
	dest := filepath.Join(t.TempDir(), "shared", "skills")

	got := installed(t, runIn(t, f.Dir(), "skill", "install", "--dest", dest, "--json").
		mustSucceed(t, "skill", "install"))
	if got.Dest != dest || got.Path != filepath.Join(dest, "git-pair") {
		t.Errorf("dest = %q, path = %q, want %q and %q", got.Dest, got.Path, dest, filepath.Join(dest, "git-pair"))
	}
	if got.Harness != "" || got.Scope != "" {
		t.Errorf("--dest still reported %s/%s, neither of which it consulted", got.Harness, got.Scope)
	}
	if _, err := os.Stat(filepath.Join(dest, "git-pair", "SKILL.md")); err != nil {
		t.Errorf("nothing was written under --dest: %v", err)
	}

	// Neither closing sentence is true of a directory git-pair was handed: it is not in a repository
	// somebody commits, and it is not the home directory of one account.
	human := runIn(t, f.Dir(), "skill", "install", "--dest", dest+"/other").
		mustSucceed(t, "skill", "install", "--dest", dest+"/other")
	for _, not := range []string{"Every repository this account opens", "Commit ", "not written by git-pair"} {
		if strings.Contains(human.stdout, not) {
			t.Errorf("a --dest install said %q, which it has no way to know\n%s", not, human.stdout)
		}
	}
	if !strings.Contains(human.stdout, "restart it") {
		t.Errorf("a --dest install gave no advice a caller can act on\n%s", human.stdout)
	}
}

func TestSkillAgentsMDPrintsTheStanza(t *testing.T) {
	res := run(t, "skill", "agents-md").mustSucceed(t, "skill", "agents-md")
	if res.stdout != skills.AgentsMDPointer() {
		t.Errorf("`skill agents-md` did not print the compiled stanza (%d bytes vs %d)",
			len(res.stdout), len(skills.AgentsMDPointer()))
	}
	// It is a pointer, not a second contract: enough to route an agent to the skill, and the three
	// rules an agent most often gets wrong.
	for _, want := range []string{"git pair change ready", "git pair change feedback", "git pair check"} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("the stanza never names %q\n%s", want, res.stdout)
		}
	}
}
