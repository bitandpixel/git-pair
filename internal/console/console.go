// Package console resolves and launches the external programs git-pair delegates
// to: the editor, the pager-less git diff, and the configured difftool.
//
// Commands are built rather than run so that the plain CLI can run them
// directly while the TUI hands the same *exec.Cmd to bubbletea, which knows how
// to release and restore the terminal around it.
package console

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"gitpair/internal/git"
)

// EditorCommand builds the command opening path in the user's editor.
//
// The editor is whatever git would use, so git-pair asks git instead of searching on its own:
// GIT_EDITOR, then core.editor, then VISUAL, then EDITOR, then whatever that git build falls
// back to. Two things come from that beyond getting the order right — an EDITOR override does not
// outrank core.editor in git, and used to here — repo-local core.editor becomes available, which
// an environment lookup can never see, and a wrapper that injects GIT_EDITOR (a hook, another
// tool) is honoured the way every other git consumer honours it. The value is a command line, so
// the launch keeps git's shape: `myeditor --wait` is a program plus flags, not a program named
// "myeditor --wait".
//
// The third rung is conditional, and the man page does not say so: git reads VISUAL only when it
// believes the terminal can show an editor, counting TERM unset or "dumb" as not able to
// (editor.c `is_terminal_dumb()`). On such a terminal git passes over VISUAL and takes EDITOR, so
// headless callers — CI, an agent, `ssh host git commit` — should set EDITOR or core.editor.
// With neither set on a dumb terminal git answers nothing and exits 1, refusing to name a
// full-screen editor it could not draw. That is where the fallback below runs, and it does not
// copy the refusal: someone who configured only VISUAL meant it, and an error opens no file. The
// difference is deliberate and asserted in console_test.go, because it is invisible until
// somebody reads git's source.
func EditorCommand(ctx context.Context, repo *git.Repo, path string) (*exec.Cmd, error) {
	value, err := repo.Git(ctx, "var", "GIT_EDITOR")
	value = strings.TrimSpace(value)
	if err != nil || value == "" {
		// Only a git that cannot answer gets here. Falling back is also kinder than git,
		// which would try to run an empty editor name.
		value = firstSet(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
	}
	if runtime.GOOS == "windows" {
		// No shell splitting: treat the value as a program name.
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return nil, errors.New("editor setting is empty")
		}
		return command(repo, fields[0], append(fields[1:], path)...), nil
	}
	// eval + an unquoted expansion reproduces git's own handling of the value, so
	// EDITOR="code --wait" splits into a program and its flags. Quoting the
	// expansion instead treats the whole value as one program name. A value
	// containing a literal space in the program name needs its own quoting
	// (core.editor="/my editor.sh" --wait), exactly as it does for git.
	//
	// Only the value gets that parse; the path must be kept out of it. eval builds a
	// command string from the expanded words and parses that string again, so a path
	// holding shell metacharacters is read as shell rather than passed through:
	// src/routes/api/businesses/$businessId/activate.ts — an ordinary Next.js
	// dynamic route — loses both dynamic directories to parameter expansion, and the
	// editor opens the missing path that is left as an empty buffer. A backtick runs
	// a command, `*` can be replaced by other files, and a space splits the path in
	// two. So the value goes through eval into the positional parameters, and the
	// path travels in a variable, quoted at the exec and assigned from $1 before
	// `set --` overwrites it, so nothing re-reads what it contains.
	//
	// The empty check is not decoration. A value that expands to nothing —
	// core.editor=$UNSET — would leave `exec "$@" "$path"` with one word, which
	// execs the reviewed file instead of an editor.
	const launch = `path=$1
eval "set -- ${GIT_PAIR_EDITOR}"
if [ "$#" -eq 0 ]; then
	printf '%s\n' 'git-pair: editor setting is empty' >&2
	exit 127
fi
exec "$@" "$path"`
	cmd := exec.Command("/bin/sh", "-c", launch, "git-pair", path)
	cmd.Dir = repo.Dir
	cmd.Env = append(os.Environ(), "GIT_PAIR_EDITOR="+value)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd, nil
}

// DiffToolCommand builds the `git difftool` invocation for a span, so review uses whatever
// the user configured (vimdiff, meld, ...) instead of a renderer of git-pair's own.
// --no-prompt avoids a per-file confirmation for what is already an explicit, single-file
// request.
//
// One revision, not two, and that is the whole point. `git difftool <from> <to>`
// materialises both sides as temporary blob files, so the tool edits throwaway
// copies: keystrokes that look like an edit have nowhere to be written, and
// changes made outside the tool never appear in it. Against the working tree the
// right-hand buffer is the actual file, so edits persist and are visible the next
// time the tool is opened. The printed and --stat diffs stay on the committed
// span: they describe review state, while the tool is for a human working on it.
//
// A historical span passes `to` and takes the throwaway copies: there is nothing
// there to edit, and comparing a historical head against today's working tree would
// put work in the window that the span does not contain. That form hands the tool
// two temp files and empty $LOCAL_LABEL/$REMOTE_LABEL, which is an acceptable price
// for a read-only look.
func DiffToolCommand(repo *git.Repo, from, to string, paths []string) *exec.Cmd {
	args := []string{"difftool", "--no-prompt", from}
	if to != "" {
		args = append(args, to)
	}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	return command(repo, "git", args...)
}

// DiffCommand builds `git diff <from> <to> -- <paths>` for terminal viewing.
func DiffCommand(repo *git.Repo, from, to string, paths []string) *exec.Cmd {
	args := []string{"-c", "core.quotePath=false", "diff", from, to}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	return command(repo, "git", args...)
}

func command(repo *git.Repo, name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Dir = repo.Dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd
}

// PromptLine asks a single-line question on the terminal. It fails rather than
// blocking when there is no terminal to read from, which keeps every git-pair
// command usable from an agent.
func PromptLine(question string) (string, error) {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return "", fmt.Errorf("%w: %s needs a terminal (pass the value as an argument instead)",
			ErrNotInteractive, question)
	}
	fmt.Fprint(os.Stderr, question+" ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return "", fmt.Errorf("%w: %s", ErrNotInteractive, question)
	}
	return strings.TrimSpace(line), nil
}

// ErrNotInteractive means a value could only be supplied by a human at a terminal.
var ErrNotInteractive = errors.New("interactive input required")

// IsTerminal reports whether stdout is attached to a terminal.
func IsTerminal() bool {
	return isCharDevice(os.Stdout)
}

// Interactive reports whether a full-duplex terminal is available for an
// editor or difftool to take over. Both stdin and stdout must be character
// devices: an editor launched with a pipe or file on stdin inherits a terminal
// it cannot drive, and wedges.
func Interactive() bool {
	return isCharDevice(os.Stdin) && isCharDevice(os.Stdout)
}

func isCharDevice(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// ReadPipedStdin returns piped or redirected stdin as text, trimmed, and
// returns "" when there is nothing to read.
//
// A terminal stdin is never read, so a command that offers piped input cannot
// block waiting for a human who is not typing. /dev/null is a character device
// and therefore also reads as "nothing supplied", which is what you want from
// `cmd </dev/null`.
func ReadPipedStdin() string {
	if isCharDevice(os.Stdin) {
		return ""
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func firstSet(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
