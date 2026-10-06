package console

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/git"
	"gitpair/internal/gittest"
)

// resolvedEditor asks EditorCommand for an editor and reports the command line it chose.
func resolvedEditor(t *testing.T, repo *git.Repo) string {
	t.Helper()
	cmd, err := EditorCommand(context.Background(), repo, filepath.Join(repo.Dir, "ABOUT.md"))
	if err != nil {
		t.Fatalf("EditorCommand: %v", err)
	}
	for _, kv := range cmd.Env {
		if v, ok := strings.CutPrefix(kv, "GIT_PAIR_EDITOR="); ok {
			return v
		}
	}
	t.Fatalf("the editor command carries no resolved editor: %v", cmd.Env)
	return ""
}

// editorRepo builds an isolated repository whose git subprocesses see exactly the given
// GIT_EDITOR/VISUAL/EDITOR settings — and none of the developer's own — because which editor
// wins is the whole subject of these tests.
//
// term is what the fixture tells git the terminal is called, and it is a parameter for the same
// reason as the editor variables: git reads VISUAL only when it believes the terminal can show
// one, counting TERM unset or "dumb" as not able to (editor.c `is_terminal_dumb()`). Left to
// inherited surroundings, the ladder below passes on a developer's shell and fails on a CI step
// process, which has no terminal — a difference in TERM, not in git-pair. term == "" is TERM
// unset; the variable is dropped from the fixture's environment rather than set empty, because
// git treats an empty TERM as usable and "unset" as not.
func editorRepo(t *testing.T, coreEditor, term string, env ...string) *git.Repo {
	t.Helper()
	f := gittest.New(t)
	if coreEditor != "" {
		f.Config("core.editor", coreEditor)
	}
	base := make([]string, 0, len(f.Env())+len(env)+1)
	for _, kv := range f.Env() {
		if strings.HasPrefix(kv, "GIT_EDITOR=") || strings.HasPrefix(kv, "VISUAL=") ||
			strings.HasPrefix(kv, "EDITOR=") || strings.HasPrefix(kv, "TERM=") {
			continue
		}
		base = append(base, kv)
	}
	if term != "" {
		env = append([]string{"TERM=" + term}, env...)
	}
	return &git.Repo{Dir: f.Dir(), Env: append(base, env...)}
}

// A terminal name git considers usable, and one it does not. The value itself is irrelevant —
// git only compares against "dumb" and against unset — so these say what the case is about.
const (
	termUsable = "xterm"
	termDumb   = "dumb"
)

// core.editor is where git's own documentation tells people to put their editor, and it lives
// in config rather than the environment, so an environment lookup cannot see it at all.
func TestEditorComesFromGitConfig(t *testing.T) {
	repo := editorRepo(t, "myeditor --wait", termUsable)
	if got := resolvedEditor(t, repo); got != "myeditor --wait" {
		t.Errorf("resolved %q, want core.editor including its flags", got)
	}
}

// The order git documents is GIT_EDITOR, core.editor, VISUAL, EDITOR, and then whatever the
// git build was configured to fall back to. Only the last two rungs used to be consulted, and
// in the wrong rank: EDITOR outranked core.editor here.
//
// VISUAL is the rung with a condition on it that the documentation leaves out: git skips it
// entirely when the terminal is dumb, so the two dumb-terminal cases below are not variations
// on the same assertion — they are git's actual rule, and the reason TERM is a parameter.
func TestEditorPrecedenceMatchesGit(t *testing.T) {
	cases := []struct {
		name       string
		coreEditor string
		term       string
		env        []string
		want       string
	}{
		{"core.editor beats the environment", "giteditor", termUsable, []string{"VISUAL=vs", "EDITOR=ed"}, "giteditor"},
		{"GIT_EDITOR beats core.editor", "giteditor", termUsable, []string{"GIT_EDITOR=hookeditor", "VISUAL=vs"}, "hookeditor"},
		{"VISUAL beats EDITOR on a usable terminal", "", termUsable, []string{"VISUAL=vs", "EDITOR=ed"}, "vs"},
		{"VISUAL is skipped on a dumb terminal", "", termDumb, []string{"VISUAL=vs", "EDITOR=ed"}, "ed"},
		{"core.editor still wins on a dumb terminal", "giteditor", termDumb, []string{"VISUAL=vs"}, "giteditor"},
		{"EDITOR is the last environment rung", "", termDumb, []string{"EDITOR=ed"}, "ed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolvedEditor(t, editorRepo(t, c.coreEditor, c.term, c.env...)); got != c.want {
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

// A dumb terminal with no editor configured is the other way git can refuse to answer: it
// exits 1 rather than put a full-screen editor on a screen that cannot show one. git-pair's
// fallback does not copy that refusal — someone who set VISUAL and nothing else meant it, and
// an error helps nobody — so this pins the divergence rather than leaving it to be discovered
// by the next person who reads git's source.
func TestFallsBackWhereADumbTerminalLeavesGitRefusing(t *testing.T) {
	// Nothing in the repository's own environment names an editor: TERM=dumb, and no
	// GIT_EDITOR/VISUAL/EDITOR, which is what editorRepo leaves behind.
	refusing := editorRepo(t, "", termDumb)
	t.Setenv("VISUAL", "vs")
	t.Setenv("EDITOR", "")
	if got := resolvedEditor(t, refusing); got != "vs" {
		t.Errorf("resolved %q, want the process VISUAL that git would not name", got)
	}
}

// editorProbe writes a stand-in editor that appends each argument it receives to a log, so a
// test can assert on the argv the real editor was given rather than on how git-pair described
// the launch. The arguments are bracketed because a log that only joins them cannot tell one
// argument containing a space from two arguments.
func editorProbe(t *testing.T) (probe, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "probe.log")
	probe = filepath.Join(dir, "probe.sh")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf 'ARGV:[%s]\\n' \"$a\" >> '" + log + "'; done\n"
	if err := os.WriteFile(probe, []byte(script), 0o755); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	return probe, log
}

// The configured editor is a command line, and the flags are the reason it works: an editor
// that is asked to wait must be launched as a program plus its arguments, with the file last.
func TestEditorCommandLineIsSplitAndGivenTheFile(t *testing.T) {
	probe, log := editorProbe(t)

	repo := editorRepo(t, probe+" --wait", termUsable)
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
	if want := "ARGV:[--wait]\nARGV:[" + target + "]\n"; string(got) != want {
		t.Errorf("the editor saw %q, want its flags then the file", got)
	}
}

// The launch hands the editor value to a shell, because only a shell can split
// `code --wait` the way git does. The path must not go through that same parse: eval reads
// the command string it has just assembled, so a path containing shell metacharacters is
// interpreted instead of passed. Every layer above looks correct — the TUI lists the right
// row and hands over the right string — and the reviewer is left with an empty buffer, because
// the editor was given a path that is no longer the file. Each case below is a legal filename
// and a different thing a shell does to a path it is allowed to read.
func TestEditorPathIsNotReadAsShell(t *testing.T) {
	// The report was a Next.js dynamic route, so the first case keeps that shape: the
	// metacharacter is in a directory, where losing it changes which file the editor opens
	// rather than just its name.
	const dynamic = "src/routes/api/businesses/$businessId/offering-schedules/$offeringScheduleId/activate.ts"
	cases := []struct {
		name string
		path string
	}{
		{"a Next.js dynamic route", dynamic},
		{"parameter expansion", "src/$businessId/activate.ts"},
		{"braced parameter expansion", "src/${businessId}/activate.ts"},
		{"an expansion that has a value", "src/$PROBE_HOME/x.ts"},
		{"command substitution", "src/$(touch $PROBE_HOME/pwned)/x.ts"},
		{"backtick substitution", "src/`touch $PROBE_HOME/pwned-backtick`/x.ts"},
		{"pathname expansion", "src/*/*.ts"},
		{"a space", "src/two words/x.ts"},
		{"a backslash", `src/a\b/x.ts`},
		{"a single quote", "src/a'b/x.ts"},
		{"a double quote", `src/a"b/x.ts`},
		{"a semicolon", "src/a;b/x.ts"},
		{"an operator", "src/a&&b/x.ts"},
		{"a pipe", "src/a|b/x.ts"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			probe, log := editorProbe(t)
			repo := editorRepo(t, probe+" --wait", termUsable)

			target := filepath.Join(repo.Dir, filepath.FromSlash(c.path))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(target, []byte("export default function handler() {}\n"), 0o644); err != nil {
				t.Fatalf("write target: %v", err)
			}

			// The path survives unexpanded is not enough on its own: it would also survive
			// unexpanded with nothing named by it. These are the variables a substitution
			// would have used, so the case fails if the shell was allowed to read the path.
			t.Setenv("businessId", "EVIL")
			t.Setenv("PROBE_HOME", repo.Dir)

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
			if want := "ARGV:[--wait]\nARGV:[" + target + "]\n"; string(got) != want {
				t.Errorf("the editor saw %q, want the file byte for byte", got)
			}
			for _, stray := range []string{"pwned", "pwned-backtick"} {
				if _, err := os.Stat(filepath.Join(repo.Dir, stray)); err == nil {
					t.Errorf("the shell ran the substitution in the path: %s was created", stray)
				}
			}
		})
	}
}

// A value that expands to nothing is different from an empty setting, which the fallback
// above already replaces: `core.editor=$UNSET` reaches the shell as words that vanish. Left
// unchecked, `exec "$@" "$path"` has one word and execs the reviewed file — which is what the
// reviewer is standing in front of, and which is why the probe here is an executable script.
func TestEditorValueThatExpandsToNothingNamesNoProgram(t *testing.T) {
	_, log := editorProbe(t)
	repo := editorRepo(t, "$GIT_PAIR_NO_SUCH_EDITOR", termUsable)

	target := filepath.Join(repo.Dir, "ABOUT.md")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nprintf 'the file ran\\n'\n"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}

	cmd, err := EditorCommand(context.Background(), repo, target)
	if err != nil {
		t.Fatalf("EditorCommand: %v", err)
	}
	if err := cmd.Run(); err == nil {
		t.Error("an editor that names no program exited as though it had run")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Error("something ran as the editor; nothing should have")
	}
}

// GIT_EDITOR=true is how callers tell git "there is no editor". Honouring it means git-pair does
// not invent an editor where the user explicitly removed one.
func TestNoOpEditorIsHonoured(t *testing.T) {
	repo := editorRepo(t, "", termUsable, "GIT_EDITOR=true")
	cmd, err := EditorCommand(context.Background(), repo, filepath.Join(repo.Dir, "ABOUT.md"))
	if err != nil {
		t.Fatalf("EditorCommand: %v", err)
	}
	if err := cmd.Run(); err != nil {
		t.Errorf("the no-op editor failed: %v", err)
	}
}

// The second revision is the difference between a span you can edit and a span you can
// only read, so the argument order is worth pinning: git takes the revs before the `--`.
func TestDiffToolCommandCarriesTheSecondRevisionWhenThereIsOne(t *testing.T) {
	repo := &git.Repo{Dir: t.TempDir()}

	live := strings.Join(DiffToolCommand(repo, "abc1234", "", []string{"src/a.go"}).Args, " ")
	if want := "git difftool --no-prompt abc1234 -- src/a.go"; live != want {
		t.Errorf("live span = %q, want %q", live, want)
	}

	history := strings.Join(DiffToolCommand(repo, "abc1234", "def5678", []string{"src/a.go"}).Args, " ")
	if want := "git difftool --no-prompt abc1234 def5678 -- src/a.go"; history != want {
		t.Errorf("historical span = %q, want %q", history, want)
	}

	all := strings.Join(DiffToolCommand(repo, "abc1234", "def5678", nil).Args, " ")
	if want := "git difftool --no-prompt abc1234 def5678"; all != want {
		t.Errorf("whole span = %q, want %q", all, want)
	}
}
