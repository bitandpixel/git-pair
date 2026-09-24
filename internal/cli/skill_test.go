package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/skills"
)

// The skill is only a surface if the copy an agent reads is the copy the tool ships. `skill list` is the
// check, so its tests are about the three answers it can give about a directory: nothing there, that
// same content, and content that is not the same.

// isolatedHome moves $HOME somewhere empty for the duration of the test, so a user-scoped row is a fact
// the test wrote rather than whatever the machine running the suite happens to have.
func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// skillTargetJSON is one row of `skill list --json`'s table, read here rather than through the
// implementation's type so a renamed field fails a test instead of compiling.
type skillTargetJSON struct {
	Harness   string   `json:"harness"`
	Scope     string   `json:"scope"`
	Path      string   `json:"path"`
	State     string   `json:"state"`
	Unmanaged []string `json:"unmanaged"`
}

func skillTargetsOf(t *testing.T, res result) []skillTargetJSON {
	t.Helper()
	var targets []skillTargetJSON
	raw, err := json.Marshal(res.json(t)["targets"])
	if err != nil {
		t.Fatalf("re-encode targets: %v", err)
	}
	if err := json.Unmarshal(raw, &targets); err != nil {
		t.Fatalf("decode targets: %v\n%s", err, res.stdout)
	}
	return targets
}

func findSkillTarget(t *testing.T, targets []skillTargetJSON, harness, scope string) skillTargetJSON {
	t.Helper()
	for _, target := range targets {
		if target.Harness == harness && target.Scope == scope {
			return target
		}
	}
	t.Fatalf("no %s/%s row in %v", harness, scope, targets)
	return skillTargetJSON{}
}

func TestSkillListNamesEveryTargetItKnows(t *testing.T) {
	f := newRepo(t)
	isolatedHome(t)

	res := runIn(t, f.Dir(), "skill", "list", "--json").mustSucceed(t, "skill", "list")
	if got := res.json(t)["skill"]; got != "git-pair" {
		t.Errorf(`"skill" = %v, want "git-pair"`, got)
	}
	if got := res.json(t)["version"]; got == "" || got == nil {
		t.Errorf(`"version" = %v, want the version this binary reports`, got)
	}
	if files := res.jsonList(t, "files"); len(files) == 0 {
		t.Error("files is empty; the skill it reports on has to be the one in this build")
	}

	repo, _ := res.json(t)["repository"].(string)
	if repo == "" {
		t.Fatal("repository is empty inside a repository")
	}
	want := []struct{ harness, scope, dir string }{
		{"agents", "repo", ".agents/skills"},
		{"agents", "user", ".agents/skills"},
		{"pi", "repo", ".pi/skills"},
		{"pi", "user", ".pi/agent/skills"},
		{"claude", "repo", ".claude/skills"},
		{"claude", "user", ".claude/skills"},
	}
	targets := skillTargetsOf(t, res)
	if len(targets) != len(want) {
		t.Fatalf("%d target rows, want %d\n%s", len(targets), len(want), res.stdout)
	}
	for i, w := range want {
		got := targets[i]
		if got.Harness != w.harness || got.Scope != w.scope {
			t.Errorf("row %d is %s/%s, want %s/%s", i, got.Harness, got.Scope, w.harness, w.scope)
			continue
		}
		base := repo
		if w.scope == "user" {
			base, _ = os.UserHomeDir()
		}
		expect := filepath.Join(base, filepath.FromSlash(w.dir), "git-pair")
		if got.Path != expect {
			t.Errorf("%s/%s path = %q, want %q", w.harness, w.scope, got.Path, expect)
		}
	}
}

// A repository row outside a repository, and a home row with no home, are reported rather than dropped:
// the table answers "where could this go" as well as "where is it", and a missing row reads as a harness
// git-pair does not know.
func TestSkillListReportsWhatHasNoHome(t *testing.T) {
	isolatedHome(t)
	res := runIn(t, t.TempDir(), "skill", "list", "--json").mustSucceed(t, "skill", "list")

	if got := res.json(t)["repository"]; got != "" {
		t.Errorf("repository = %v outside a repository, want \"\"", got)
	}
	targets := skillTargetsOf(t, res)
	for _, target := range targets {
		switch target.Scope {
		case "repo":
			if target.State != "unavailable" {
				t.Errorf("%s/repo state = %q outside a repository, want \"unavailable\"", target.Harness, target.State)
			}
		default:
			if target.State == "unavailable" || target.State == "" {
				t.Errorf("%s/%s state = %q, want a real answer with $HOME set", target.Harness, target.Scope, target.State)
			}
		}
	}

	// The other half of the row's name: with no home to resolve, the user rows report it too, and say so
	// in the form a person reads.
	t.Setenv("HOME", "")
	out := runIn(t, t.TempDir(), "skill", "list").mustSucceed(t, "skill", "list")
	if !strings.Contains(out.stdout, "(no home directory)") {
		t.Errorf("with no home, the table does not say which rows that leaves\n%s", out.stdout)
	}
	for _, target := range skillTargetsOf(t, runIn(t, t.TempDir(), "skill", "list", "--json").
		mustSucceed(t, "skill", "list")) {
		if target.Scope == "user" && target.State != "unavailable" {
			t.Errorf("%s/user state = %q with no home, want \"unavailable\"", target.Harness, target.State)
		}
	}
}

// The human table is what a person reads, and README shows a transcript of it, so the header's file count
// and the shortened paths are claims worth pinning.
func TestSkillListHumanTableNamesTheSkillAndItsPlaces(t *testing.T) {
	f := newRepo(t)
	isolatedHome(t)

	res := runIn(t, f.Dir(), "skill", "list").mustSucceed(t, "skill", "list")
	if want := fmt.Sprintf("%d files, from git-pair", len(skills.Files())); !strings.Contains(res.stdout, want) {
		t.Errorf("the header does not count the compiled skill's files as %q\n%s", want, res.stdout)
	}
	for _, want := range []string{
		"agents  repo", ".agents/skills/git-pair", ".pi/skills/git-pair", ".claude/skills/git-pair",
		"$HOME/.agents/skills/git-pair", "absent",
	} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("the table never shows %q\n%s", want, res.stdout)
		}
	}
}

func TestSkillListComparesAnInstalledSkillWithThisBinary(t *testing.T) {
	f := newRepo(t)
	home := isolatedHome(t)
	dir := filepath.Join(home, ".agents", "skills", "git-pair")

	states := func() skillTargetJSON {
		res := runIn(t, f.Dir(), "skill", "list", "--json").mustSucceed(t, "skill", "list")
		return findSkillTarget(t, skillTargetsOf(t, res), "agents", "user")
	}

	if got := states().State; got != "absent" {
		t.Fatalf("state before anything is written = %q, want \"absent\"", got)
	}

	installSkill(t, dir)
	if got := states().State; got != "current" {
		t.Fatalf("state after writing this binary's bytes = %q, want \"current\"", got)
	}

	// A file the skill has that the installed copy does not, and content that differs: both are the
	// drift the command exists to catch, and neither is "absent" — something is installed there, and an
	// agent reading it is reading instructions for a different tool.
	if err := os.Remove(filepath.Join(dir, "references", "integration.md")); err != nil {
		t.Fatalf("remove an installed file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: git-pair\n---\n"), 0o644); err != nil {
		t.Fatalf("overwrite the installed SKILL.md: %v", err)
	}
	os.MkdirAll(filepath.Join(dir, "notes"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, "notes", "team.md"), []byte("ours\n"), 0o644); err != nil {
		t.Fatalf("write an unmanaged file: %v", err)
	}

	got := states()
	if got.State != "stale" {
		t.Errorf("state after the copy drifted = %q, want \"stale\"", got.State)
	}
	if len(got.Unmanaged) != 1 || got.Unmanaged[0] != filepath.Join("notes", "team.md") {
		t.Errorf("unmanaged = %v, want the one file git-pair did not write", got.Unmanaged)
	}

	// The same fact in the form a person reads: something sits beside the shipped skill that git-pair
	// did not write, and `list` is where a reviewer would learn it.
	human := runIn(t, f.Dir(), "skill", "list").mustSucceed(t, "skill", "list")
	if !strings.Contains(human.stdout, "notes/team.md") || !strings.Contains(human.stdout, "not written by git-pair") {
		t.Errorf("the table hides the file git-pair did not write\n%s", human.stdout)
	}
}

// installSkill writes the skill this binary carries into dir, the way an install would.
func installSkill(t *testing.T, dir string) {
	t.Helper()
	for _, name := range skills.Files() {
		data, err := skills.Read(name)
		if err != nil {
			t.Fatalf("read %s from the skill: %v", name, err)
		}
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
}

func TestSkillShowPrintsTheCompiledSkill(t *testing.T) {
	for _, args := range [][]string{
		{"skill", "show"},
		{"skill", "show", "SKILL.md"},
		{"skill", "show", "references/cli.md"},
		{"skill", "show", "git-pair/references/cli.md"},
	} {
		file := "SKILL.md"
		if len(args) == 3 {
			file = strings.TrimPrefix(args[2], "git-pair/")
		}
		want, err := skills.Read(file)
		if err != nil {
			t.Fatalf("read %s from the skill: %v", file, err)
		}
		res := run(t, args...).mustSucceed(t, args...)
		if res.stdout != string(want) {
			t.Errorf("git pair %v printed %d bytes, want the compiled %s's %d\n%s",
				args, len(res.stdout), file, len(want), firstLines(res.stdout, 3))
		}
	}
}

func TestSkillShowRefusesAFileTheSkillDoesNotHave(t *testing.T) {
	res := run(t, "skill", "show", "references/nope.md")
	if res.code != exitUsage {
		t.Fatalf("`skill show references/nope.md` exited %d, want %d\nstdout: %s\nstderr: %s",
			res.code, exitUsage, res.stdout, res.stderr)
	}
	for _, want := range []string{"references/nope.md", "SKILL.md", "references/cli.md"} {
		if !strings.Contains(res.stderr, want) {
			t.Errorf("the refusal never names %q, so the caller cannot see what is there\nstderr: %s", want, res.stderr)
		}
	}
}

func TestSkillGroupRefusesAnUnknownVerb(t *testing.T) {
	res := run(t, "skill", "instal")
	if res.code != exitUsage {
		t.Fatalf("`git pair skill instal` exited %d, want %d\nstdout: %s\nstderr: %s",
			res.code, exitUsage, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stderr, "skill") {
		t.Errorf("the refusal does not say which command group it is about\nstderr: %s", res.stderr)
	}
	if res = run(t, "skill"); res.code != exitOK || !strings.Contains(res.stdout, "list") {
		t.Errorf("`git pair skill` with no verb printed no command list (exit %d)\n%s", res.code, res.stdout)
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
