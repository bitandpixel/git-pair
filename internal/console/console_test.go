package console

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpr/internal/git"
	"gitpr/internal/gittest"
)

// resolvedEditor asks EditorCommand for an editor and reports the command line it chose.
func resolvedEditor(t *testing.T, repo *git.Repo) string {
	t.Helper()
	cmd, err := EditorCommand(context.Background(), repo, filepath.Join(repo.Dir, "ABOUT.md"))
	if err != nil {
		t.Fatalf("EditorCommand: %v", err)
	}
	for _, kv := range cmd.Env {
		if v, ok := strings.CutPrefix(kv, "GITPR_EDITOR="); ok {
			return v
		}
	}
	t.Fatalf("the editor command carries no resolved editor: %v", cmd.Env)
	return ""
}

// editorRepo builds an isolated repository whose git subprocesses see exactly the given
// GIT_EDITOR/VISUAL/EDITOR settings — and none of the developer's own — because which editor
// wins is the whole subject of these tests.
func editorRepo(t *testing.T, coreEditor string, env ...string) *git.Repo {
	t.Helper()
	f := gittest.New(t)
	if coreEditor != "" {
		f.Config("core.editor", coreEditor)
	}
	base := make([]string, 0, len(f.Env())+len(env))
	for _, kv := range f.Env() {
		if strings.HasPrefix(kv, "GIT_EDITOR=") || strings.HasPrefix(kv, "VISUAL=") || strings.HasPrefix(kv, "EDITOR=") {
			continue
		}
		base = append(base, kv)
	}
	return &git.Repo{Dir: f.Dir(), Env: append(base, env...)}
}

// core.editor is where git's own documentation tells people to put their editor, and it lives
// in config rather than the environment, so an environment lookup cannot see it at all.
func TestEditorComesFromGitConfig(t *testing.T) {
	repo := editorRepo(t, "myeditor --wait")
	if got := resolvedEditor(t, repo); got != "myeditor --wait" {
		t.Errorf("resolved %q, want core.editor including its flags", got)
	}
}

// The order git documents is GIT_EDITOR, core.editor, VISUAL, EDITOR, and then whatever the
// git build was configured to fall back to. Only the last two rungs used to be consulted, and
// in the wrong rank: EDITOR outranked core.editor here.
func TestEditorPrecedenceMatchesGit(t *testing.T) {
	cases := []struct {
		name       string
		coreEditor string
		env        []string
		want       string
	}{
		{"core.editor beats the environment", "giteditor", []string{"VISUAL=vs", "EDITOR=ed"}, "giteditor"},
		{"GIT_EDITOR beats core.editor", "giteditor", []string{"GIT_EDITOR=hookeditor", "VISUAL=vs"}, "hookeditor"},
		{"VISUAL beats EDITOR", "", []string{"VISUAL=vs", "EDITOR=ed"}, "vs"},
		{"EDITOR is the last environment rung", "", []string{"EDITOR=ed"}, "ed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolvedEditor(t, editorRepo(t, c.coreEditor, c.env...)); got != c.want {
				t.Errorf("resolved %q, want %q", got, c.want)
			}
		})
	}
}

// The environment chain is the last resort, for the git that cannot answer rather than for
// the common case of a configured editor. "vi" is git's historical default, kept here so a
// broken git install still opens something.
func TestFallsBackToTheEnvironmentWhenGitCannotAnswer(t *testing.T) {
	// The fallback reads the process environment, which is where an editor lives when git
	// cannot be asked.
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	broken := &git.Repo{Dir: filepath.Join(t.TempDir(), "not-a-repo"), Env: []string{"PATH=" + os.Getenv("PATH")}}

	for _, c := range []struct {
		visual, editor, want string
	}{
		{"vs", "ed", "vs"},
		{"", "ed", "ed"},
		{"", "", "vi"},
	} {
		t.Setenv("VISUAL", c.visual)
		t.Setenv("EDITOR", c.editor)
		if got := resolvedEditor(t, broken); got != c.want {
			t.Errorf("with VISUAL=%q EDITOR=%q resolved %q, want %q", c.visual, c.editor, got, c.want)
		}
	}
}

// The configured editor is a command line, and the flags are the reason it works: an editor
// that is asked to wait must be launched as a program plus its arguments, with the file last.
func TestEditorCommandLineIsSplitAndGivenTheFile(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "probe.log")
	probe := filepath.Join(dir, "probe.sh")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf 'ARGV:%s\\n' \"$a\" >> '" + log + "'; done\n"
	if err := os.WriteFile(probe, []byte(script), 0o755); err != nil {
		t.Fatalf("write probe: %v", err)
	}

	repo := editorRepo(t, probe+" --wait")
	target := filepath.Join(repo.Dir, "ABOUT.md")
	cmd, err := EditorCommand(context.Background(), repo, target)
	if err != nil {
		t.Fatalf("EditorCommand: %v", err)
	}
	if err := cmd.Run(); err != nil {
		t.Fatalf("editor exited with an error: %v", err)
	}
	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the configured editor never ran: %v", err)
	}
	if want := "ARGV:--wait\nARGV:" + target + "\n"; string(got) != want {
		t.Errorf("the editor saw %q, want its flags then the file", got)
	}
}

// GIT_EDITOR=true is how callers tell git "there is no editor". Honouring it means gitpr does
// not invent an editor where the user explicitly removed one.
func TestNoOpEditorIsHonoured(t *testing.T) {
	repo := editorRepo(t, "", "GIT_EDITOR=true")
	cmd, err := EditorCommand(context.Background(), repo, filepath.Join(repo.Dir, "ABOUT.md"))
	if err != nil {
		t.Fatalf("EditorCommand: %v", err)
	}
	if err := cmd.Run(); err != nil {
		t.Errorf("the no-op editor failed: %v", err)
	}
}
