package tui

import (
	"bytes"
	"strings"
	"testing"
)

// The session keeps its own alt screen so that handing the terminal to an editor or difftool
// does not flash the shell screen behind it. The price is that bubbletea no longer clears that
// screen between frames, so the session has to: the child leaves its own output where the
// frame was, and the renderer — whose line count a handoff resets — would write the next
// frame onto it rather than over it.
func TestReturningFromAToolStartsOnACleanScreen(t *testing.T) {
	var buf bytes.Buffer
	m := navModel(t)
	m.out = &buf

	after, _ := m.Update(externalDoneMsg{label: "Editor exited with an error"})
	m = after.(reviewModel)
	if !strings.Contains(buf.String(), "\x1b[2J\x1b[H") {
		t.Errorf("no clean screen before the frame after the tool closed: %q", buf.String())
	}
	if m.statusErr {
		t.Errorf("a tool exiting cleanly was reported as an error: %q", m.status)
	}

	// A model with no terminal wired up — every other test in this package — must survive a
	// handoff returning to it.
	quiet := navModel(t)
	updated, _ := quiet.Update(externalDoneMsg{})
	if _, ok := updated.(reviewModel); !ok {
		t.Errorf("returning from a tool produced %T, want the session model back", updated)
	}
}

// The clear is screen-wide and homes the cursor, and deliberately leaves scrollback alone:
// what the child printed is history, not something to destroy.
func TestClearScreenErasesAndHomesWithoutTouchingScrollback(t *testing.T) {
	var buf bytes.Buffer
	clearScreen(&buf)
	if got := buf.String(); got != "\x1b[2J\x1b[H" {
		t.Errorf("clearScreen wrote %q, want erase screen and home", got)
	}
	if bytes.Contains(buf.Bytes(), []byte("\x1b[3J")) {
		t.Error("clearScreen clears scrollback, which is the user's history to keep")
	}
}

// The screen the session takes over has to be handed back in the state it was found: the two
// sequences are a pair, and neither is allowed to reach for the scrollback.
func TestAltScreenSequencesAreAPair(t *testing.T) {
	if !strings.Contains(enterAltScreen, "\x1b[?1049h") {
		t.Errorf("enterAltScreen %q does not switch to the alt screen", enterAltScreen)
	}
	if !strings.Contains(exitAltScreen, "\x1b[?1049l") {
		t.Errorf("exitAltScreen %q does not leave the alt screen", exitAltScreen)
	}
	for _, seq := range []string{enterAltScreen, exitAltScreen} {
		if !strings.Contains(seq, "\x1b[?25h") {
			t.Errorf("%q leaves the cursor state to chance, which some terminals keep per screen", seq)
		}
		if strings.Contains(seq, "\x1b[3J") {
			t.Errorf("%q discards scrollback, which belongs to the user", seq)
		}
	}
}
