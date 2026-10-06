package tui

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gitpair/internal/git"
	"gitpair/internal/gittest"
)

// The wheel is the reviewer's scroll, offered as a setting rather than a constant because asking
// for it costs the terminal its own click-drag selection. What these tests hold the code to: one
// notch moves the diff by one notch, the setting actually decides it, and a handoff does not take
// the wheel away for the rest of the session.

// tall is a file with enough diff to have a top and a bottom, with the wheel switched on. The
// switch is part of the fixture rather than each test because a test about the wheel is always a
// test about a session that asked the terminal for it -- and one test here is about the session
// that did not, and says so by putting the zero value back.
func tall(m reviewModel, lines int) reviewModel {
	m.mouse = MouseSetting{Report: true}
	m.patchFor = func(_ context.Context, _ string) Patch {
		out := make([]string, 0, lines)
		for i := range lines {
			out = append(out, fmt.Sprintf("line %02d", i))
		}
		return Patch{Lines: out, Added: lines}
	}
	return m
}

func wheelMsg(b tea.MouseButton, a tea.MouseAction) tea.MouseMsg {
	return tea.MouseMsg{Action: a, Button: b}
}

// roll is one notch of the wheel, delivered the way Update delivers a key that may fetch.
func roll(t *testing.T, m reviewModel, b tea.MouseButton) reviewModel {
	t.Helper()
	updated, cmd := m.Update(wheelMsg(b, tea.MouseActionPress))
	rm, ok := updated.(reviewModel)
	if !ok {
		t.Fatalf("a wheel event produced %T", updated)
	}
	return deliver(t, rm, cmd)
}

func TestWheelScrollsTheDiff(t *testing.T) {
	m := focusPane(t, askPreview(t, tall(previewModel(t), 60)))

	m = roll(t, m, tea.MouseButtonWheelDown)
	if m.previewOffset != wheelStep {
		t.Errorf("one notch down moved the pane to %d, want %d", m.previewOffset, wheelStep)
	}
	m = roll(t, m, tea.MouseButtonWheelDown)
	if m.previewOffset != 2*wheelStep {
		t.Errorf("two notches down moved the pane to %d, want %d", m.previewOffset, 2*wheelStep)
	}
	m = roll(t, m, tea.MouseButtonWheelUp)
	if m.previewOffset != wheelStep {
		t.Errorf("one notch back moved the pane to %d, want %d", m.previewOffset, wheelStep)
	}

	// The cursor is where it was: the wheel scrolls the pane, it does not move through the list.
	if m.previewPath == "" {
		t.Error("the wheel left the pane with nothing in it")
	}
}

func TestWheelStopsAtBothEndsOfTheDiff(t *testing.T) {
	m := focusPane(t, askPreview(t, tall(previewModel(t), 60)))
	body := m.previewBodyRows()
	max := 60 - body

	for range max + 10 {
		m = roll(t, m, tea.MouseButtonWheelDown)
	}
	if m.previewOffset != max {
		t.Errorf("rolled to %d, want it to stop at %d (%d lines, %d shown)", m.previewOffset, max, 60, body)
	}
	if view := m.View(); !strings.Contains(view, "line 59") {
		t.Errorf("at the end of the diff the pane does not show its last line:\n%s", view)
	}

	for range max + 10 {
		m = roll(t, m, tea.MouseButtonWheelUp)
	}
	if m.previewOffset != 0 {
		t.Errorf("rolled back to %d, want 0", m.previewOffset)
	}
}

// The setting decides it. A session that did not ask the terminal for the wheel must not act on
// one, because that is the reviewer's opt-out and it has to be real.
func TestWheelNeedsTheSetting(t *testing.T) {
	m := focusPane(t, askPreview(t, tall(previewModel(t), 60)))
	m.mouse = MouseSetting{}

	m = roll(t, m, tea.MouseButtonWheelDown)
	if m.previewOffset != 0 {
		t.Errorf("rolled to %d with the wheel switched off, want 0", m.previewOffset)
	}
}

// One notch is one event inside tmux and two on the terminals that report the release as well.
// Reading both would scroll six rows where the reviewer rolled three.
func TestWheelReadsOneNotchOnce(t *testing.T) {
	m := focusPane(t, askPreview(t, tall(previewModel(t), 60)))

	updated, _ := m.Update(wheelMsg(tea.MouseButtonWheelDown, tea.MouseActionPress))
	rm := updated.(reviewModel)
	updated, _ = rm.Update(wheelMsg(tea.MouseButtonWheelDown, tea.MouseActionRelease))
	rm = updated.(reviewModel)

	if rm.previewOffset != wheelStep {
		t.Errorf("press and release moved the pane to %d, want %d", rm.previewOffset, wheelStep)
	}
}

// Nothing on screen to scroll means nothing the wheel does: no jump, no panic, and no scrolling
// of a pane the reviewer cannot see.
func TestWheelWithoutADiffOnScreen(t *testing.T) {
	t.Run("no file under the cursor yet", func(t *testing.T) {
		m := previewModel(t)
		m.mouse = MouseSetting{Report: true}
		m.previewPath, m.previewOffset = "", 0
		m = roll(t, m, tea.MouseButtonWheelDown)
		if m.previewOffset != 0 {
			t.Errorf("rolled an empty pane to %d, want 0", m.previewOffset)
		}
	})

	t.Run("the span picker holds the screen", func(t *testing.T) {
		m := focusPane(t, askPreview(t, tall(previewModel(t), 60)))
		m = roll(t, m, tea.MouseButtonWheelDown)
		scrolled := m.previewOffset
		if scrolled == 0 {
			t.Fatal("the fixture cannot show the wheel being ignored if it never scrolled")
		}
		m.mode = modeSpan
		m = roll(t, m, tea.MouseButtonWheelDown)
		if m.previewOffset != scrolled {
			t.Errorf("the wheel moved the hidden pane from %d to %d", scrolled, m.previewOffset)
		}
	})
}

// A handoff is the reason the wheel has to be asked for again: bubbletea releases the terminal for
// the child and puts bracketed paste and focus reporting back on its return, but not mouse
// reporting. Without this the first trip into vim ends the wheel for the session.
func TestHandoffAsksForTheWheelBack(t *testing.T) {
	m := previewModel(t)
	m.mouse = MouseSetting{Report: true}
	_, cmd := m.Update(externalDoneMsg{})
	if !wheelAskedFor(cmd) {
		t.Error("the session came back from a handoff without asking for the wheel again")
	}

	m.mouse = MouseSetting{}
	_, cmd = m.Update(externalDoneMsg{})
	if wheelAskedFor(cmd) {
		t.Error("a session with the wheel switched off asked the terminal for it anyway")
	}
}

// The value is git's boolean vocabulary, and a value outside it says so rather than being
// silently dropped.
func TestResolveMouse(t *testing.T) {
	tests := []struct {
		value string
		set   bool
		want  bool
		warns bool
		name  string
	}{
		{name: "unset", want: true},
		{name: "true", value: "true", set: true, want: true},
		{name: "yes", value: "yes", set: true, want: true},
		{name: "on", value: "on", set: true, want: true},
		{name: "one", value: "1", set: true, want: true},
		{name: "upper", value: "TRUE", set: true, want: true},
		{name: "false", value: "false", set: true},
		{name: "no", value: "no", set: true},
		{name: "off", value: "off", set: true},
		{name: "zero", value: "0", set: true},
		{name: "unusable", value: "diabled", set: true, want: true, warns: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := gittest.New(t)
			f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
			if tc.set {
				f.Config(mouseConfig, tc.value)
			}
			got := ResolveMouse(context.Background(), &git.Repo{Dir: f.Dir()})
			if got.Report != tc.want {
				t.Errorf("git-pair.mouse=%q reported %v, want %v", tc.value, got.Report, tc.want)
			}
			if got.Note == "" != !tc.warns {
				t.Errorf("git-pair.mouse=%q note %q, want a note: %v", tc.value, got.Note, tc.warns)
			}
		})
	}

	// A repository the caller could not name is not a reason to lose the wheel either.
	if got := ResolveMouse(context.Background(), nil); !got.Report || got.Note != "" {
		t.Errorf("with no repository the answer was %+v, want the wheel on and nothing to say", got)
	}
}

// wheelAskedFor reports whether the command bubbletea was handed includes the request to start
// reporting the wheel. The message itself is bubbletea's, so the question is asked of its type.
func wheelAskedFor(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	return msgAsksForWheel(cmd())
}

func msgAsksForWheel(msg tea.Msg) bool {
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil && msgAsksForWheel(c()) {
				return true
			}
		}
		return false
	}
	return reflect.TypeOf(msg) == reflect.TypeOf(tea.EnableMouseCellMotion())
}
