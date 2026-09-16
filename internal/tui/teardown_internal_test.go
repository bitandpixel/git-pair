package tui

import (
	"bytes"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// A child tool prints on the screen the alt screen was covering, and that debris is what
// reappears over the prompt when the session ends. The cleanup is owed only by sessions that
// handed the terminal away: one that never left the alt screen should not erase what was on
// the screen before it started.
func TestHandoffMarksTheScreenForCleanup(t *testing.T) {
	m := navModel(t)
	if m.handedOff {
		t.Error("a fresh session claims to have handed off the terminal")
	}

	m.cursor = indexOfNameBySuffix(t, m, ".go")
	after, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd == nil {
		t.Fatal("d did not hand off the terminal")
	}
	if !after.(reviewModel).handedOff {
		t.Error("a session that opened the difftool does not mark the screen for cleanup")
	}

	// The fallback into the editor is a handoff too, and leaves the same kind of debris.
	m = navModel(t)
	m.cursor = indexOf(t, m, rowThread)
	after, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd == nil {
		t.Fatal("the fallback did not hand off the terminal")
	}
	if !after.(reviewModel).handedOff {
		t.Error("a session that opened the editor does not mark the screen for cleanup")
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
