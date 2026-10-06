package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"gitpair/internal/git"
)

// The wheel, and the one setting that decides whether the terminal reports it to us.
//
// Asking for the wheel costs the terminal its own click-drag selection until the reviewer holds
// Shift, so the choice is a setting rather than a constant. It is worth asking: the session lives
// on the alternate screen, and a terminal inside tmux gives the wheel to whatever is on that
// screen — where a program that never asked for the mouse simply never sees it. So the wheel is
// already lost while the session runs; reporting is the only way to get it back.

// MouseSetting is the session's answer to whether the terminal should report wheel events,
// resolved once before the program starts.
type MouseSetting struct {
	// Report is whether to ask the terminal for button and wheel events.
	Report bool
	// Note is what to tell the reviewer about a value the session could not use. It is empty
	// for a value it read, which is every ordinary run.
	Note string
}

// mouseConfig is the setting that decides it. It is in the tool's own namespace because there is
// no git setting it overrides: git has no opinion about who scrolls a TUI.
const mouseConfig = "git-pair.mouse"

// ResolveMouse reads git-pair.mouse from the repository the session runs in. The wheel is on
// unless the reviewer turned it off, because the setting exists to opt out of a thing the session
// does for you rather than to unlock a thing you have to remember to ask for.
//
// git's own boolean vocabulary is git's, not this package's copy of it: true/yes/on/1 and
// false/no/off/0, in any case. A value outside it keeps the default and says so, because a
// reviewer who typed `diabled` should hear that it did nothing rather than wonder whether the
// wheel was ever meant to work.
func ResolveMouse(ctx context.Context, repo *git.Repo) MouseSetting {
	on := MouseSetting{Report: true}
	if repo == nil {
		return on
	}
	value, err := repo.Git(ctx, "config", "--get", mouseConfig)
	if err != nil {
		// `config --get` exits 1 for a key nobody set, which is the ordinary case. Anything else
		// git has to say is a reason to keep the default, not to refuse a review session over a
		// wheel.
		return on
	}
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "", "true", "yes", "on", "1":
		return on
	case "false", "no", "off", "0":
		return MouseSetting{Report: false}
	}
	return MouseSetting{
		Report: true,
		Note: fmt.Sprintf("%s=%q is not a boolean, so the wheel stayed on, which is the default",
			mouseConfig, value),
	}
}

// wheelStep is how many rows one notch of the wheel moves the diff. Three is the distance a notch
// moves a pager, and the reviewer who wants half a page has ctrl-d and ctrl-u, which keep a line
// of context across the break the way a wheel notch does not.
const wheelStep = 3

// handleWheel moves the diff by one notch. The wheel belongs to the diff wherever the pointer is
// standing — over the list column, over the divider, over the whole-screen overlay — because the
// pane is the thing a reviewer scrolls while reading, and a pointer that has to be held over a
// column to move it is a second thing to aim at.
//
// Nothing else reads the mouse. A click changes no review state, so a reviewer can rest a finger
// on the button without wondering what the screen is about to mark.
func (m reviewModel) handleWheel(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !m.mouse.Report || !m.previewShowing() {
		return m, nil
	}
	// One notch is one event on tmux and two on the terminals that report the release as well,
	// so only the press counts: reading both would move six rows where the reviewer rolled one.
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		return m.scrollPreview(-1, wheelStep)
	case tea.MouseButtonWheelDown:
		return m.scrollPreview(1, wheelStep)
	}
	return m, nil
}

// enableWheel asks bubbletea to start reporting the wheel again. A handoff is why this exists:
// tea.ExecProcess releases the terminal for the child, and bubbletea's RestoreTerminal puts
// bracketed paste and focus reporting back but leaves mouse reporting off. Without this, the
// first trip into vim or the difftool would take the wheel away for the rest of the session.
func enableWheel() tea.Msg {
	return tea.EnableMouseCellMotion()
}

// wheelBack returns the command a handoff owes for the wheel, folded into whatever the caller was
// already returning. A session that never asked for the mouse owes nothing back, so it returns the
// caller's command untouched.
func (m reviewModel) wheelBack(cmd tea.Cmd) tea.Cmd {
	if !m.mouse.Report {
		return cmd
	}
	if cmd == nil {
		return enableWheel
	}
	return tea.Batch(cmd, enableWheel)
}
