package tui

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// The shortcut helper is the only place the TUI names its keys, so a narrow window has to
// move the overflow to the next line rather than let the terminal break it — a shortcut cut
// in half explains nothing, and a line the terminal wraps on its own also steals rows the
// file list thought it had.

const keyHelp = "j/k move  enter difftool  e edit  space reviewed  a about  t thread  T browse  v span  s submit  q quit"

var ansiCodes = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestWrapGroupsKeepsEveryShortcutWhole(t *testing.T) {
	groups := strings.Split(keyHelp, "  ")
	widest := 0
	for _, g := range groups {
		if n := utf8.RuneCountInString(g); n > widest {
			widest = n
		}
	}

	for _, width := range []int{14, 20, 32, 40, 79} {
		lines := wrapGroups(keyHelp, width)
		if width >= widest {
			for _, line := range lines {
				if n := utf8.RuneCountInString(line); n > width {
					t.Errorf("width %d: line is %d wide (%q)", width, n, line)
				}
			}
		}
		for _, group := range groups {
			found := false
			for _, line := range lines {
				if strings.Contains(line, group) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("width %d: %q was split across lines: %q", width, group, lines)
			}
		}
		if got := strings.Join(lines, "  "); got != keyHelp {
			t.Errorf("width %d: reassembled helper = %q, want the original", width, got)
		}
	}
}

func TestWrapGroupsFillsEachLineBeforeWrapping(t *testing.T) {
	// Putting every shortcut on its own line would also "not overflow", and be just as
	// useless: 40 columns fit several of them.
	got := wrapGroups(keyHelp, 40)
	want := []string{
		"j/k move  enter difftool  e edit",
		"space reviewed  a about  t thread",
		"T browse  v span  s submit  q quit",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("wrapGroups(40) =\n%q\nwant\n%q", got, want)
	}
}

func TestWrapGroupsLeavesTheHelperAloneWhenItFits(t *testing.T) {
	for _, width := range []int{len(keyHelp), len(keyHelp) + 40} {
		if got := wrapGroups(keyHelp, width); len(got) != 1 || got[0] != keyHelp {
			t.Errorf("width %d: %q, want the helper on one line", width, got)
		}
	}
	for _, width := range []int{0, -1} {
		if got := wrapGroups(keyHelp, width); len(got) != 1 || got[0] != keyHelp {
			t.Errorf("width %d must not wrap: %q", width, got)
		}
	}
	if got := wrapGroups("", 20); len(got) != 1 || got[0] != "" {
		t.Errorf("empty text = %q, want one empty line", got)
	}
}

func TestWrapGroupsGivesAnOversizedGroupItsOwnLine(t *testing.T) {
	// "a-very-long-key" cannot be packed anywhere at width 8. Breaking it would invent a
	// key that does not exist, so it takes the whole line and overflows alone.
	got := wrapGroups("alpha  a-very-long-key-group  beta", 8)
	want := []string{"alpha", "a-very-long-key-group", "beta"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("wrapGroups = %q, want %q", got, want)
	}
}

func TestRenderedViewWrapsTheHelperInsideTheWindow(t *testing.T) {
	m := newFileListModel(t)
	m.width, m.height = 30, 24

	want := m.helpLines()
	if len(want) < 2 {
		t.Fatalf("the helper needs %d rows at width 30, want it to wrap at all", len(want))
	}
	view := ansiCodes.ReplaceAllString(m.View(), "")
	lines := strings.Split(view, "\n")
	for _, wantLine := range want {
		if utf8.RuneCountInString(wantLine) > 30 {
			t.Errorf("helper line %q is %d columns wide in a 30-column window",
				wantLine, utf8.RuneCountInString(wantLine))
		}
		found := false
		for _, line := range lines {
			if line == wantLine {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the view is missing the helper row %q:\n%s", wantLine, view)
		}
	}
	if !strings.Contains(view, "q quit") {
		t.Error("wrapping lost the last shortcut")
	}
}

func TestSubmitHelpWrapsToo(t *testing.T) {
	m := newFileListModel(t)
	m.width, m.height = 24, 24
	m.mode = modeSubmit

	lines := m.helpLines()
	if len(lines) < 2 {
		t.Fatalf("submit help stayed on %d line(s) at width 24: %q", len(lines), lines)
	}
	for _, line := range lines {
		if n := utf8.RuneCountInString(line); n > 24 {
			t.Errorf("line %q is %d wide", line, n)
		}
	}
	if !strings.Contains(strings.Join(lines, "  "), "[esc] cancel") {
		t.Errorf("wrapping lost the escape hint: %q", lines)
	}
}

func TestWindowRowsShrinkWhenTheHelperWraps(t *testing.T) {
	m := newFileListModel(t)
	m.height = 40

	m.width = 200 // one helper row
	wide := m.windowRows()
	if wide != 28 {
		t.Fatalf("window = %d rows at width 200, want 28", wide)
	}

	m.width = 20
	rows := m.windowRows()
	if want := 40 - 12 - (len(m.helpLines()) - 1); rows != want {
		t.Errorf("window = %d rows, want %d (the helper now takes %d rows)",
			rows, want, len(m.helpLines()))
	}
	if rows >= wide {
		t.Errorf("the list kept %d rows while the helper grew to %d rows", rows, len(m.helpLines()))
	}
	// The fixture has fewer files than either window, so nothing scrolls yet; the point is
	// that the window stops claiming rows the helper is now using.
	total := len(m.sess.Files())
	if got := len(m.visibleRows()); got != min(total, rows) {
		t.Errorf("visibleRows = %d, want %d (total %d, window %d)", got, min(total, rows), total, rows)
	}
}
