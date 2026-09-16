// Package console resolves and launches the external programs gitpr delegates
// to: the editor, the pager-less git diff, and the configured difftool.
//
// Commands are built rather than run so that the plain CLI can run them
// directly while the TUI hands the same *exec.Cmd to bubbletea, which knows how
// to release and restore the terminal around it.
package console

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"gitpr/internal/git"
)

// EditorCommand builds the command opening path in the user's editor, honouring
// $VISUAL then $EDITOR then vi.
//
// Resolution order is $VISUAL, then $EDITOR, then vi — the same precedence git
// uses, so a machine with VISUAL=vim ignores an EDITOR override.
func EditorCommand(repo *git.Repo, path string) (*exec.Cmd, error) {
	value := firstSet(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
	if runtime.GOOS == "windows" {
		// No shell splitting: treat the value as a program name.
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return nil, errors.New("editor setting is empty")
		}
		return command(repo, fields[0], append(fields[1:], path)...), nil
	}
	// eval + an unquoted expansion reproduces git's own handling, so
	// EDITOR="code --wait" splits into a program and its flags. Quoting the
	// expansion instead treats the whole value as one program name. A value
	// containing a literal space in the program name needs its own quoting
	// ("EDITOR=\"/my editor.sh\" --wait"), exactly as it does for git.
	cmd := exec.Command("/bin/sh", "-c", `eval exec ${VISUAL:-${EDITOR:-vi}} "$@"`, "gitpr", path)
	cmd.Dir = repo.Dir
	cmd.Env = append(os.Environ(), "VISUAL="+value)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd, nil
}

// DiffToolCommand builds `git difftool <from> <to> -- <paths>` so review uses
// whatever the user configured (vimdiff, meld, ...) instead of a renderer of
// gitpr's own. --no-prompt avoids a per-file confirmation for what is already
// an explicit, single-file request.
func DiffToolCommand(repo *git.Repo, from, to string, paths []string) *exec.Cmd {
	args := []string{"difftool", "--no-prompt", from, to}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	return command(repo, "git", args...)
}

// DiffCommand builds `git git diff <from> <to> -- <paths>` for terminal viewing.
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
// blocking when there is no terminal to read from, which keeps every gitpr
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

func firstSet(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
