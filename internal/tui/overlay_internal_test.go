package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The overlay is what a small terminal gets instead of a pane: the same diff, over the whole screen,
// scrolled with the vim primitives and dismissed with q, esc or enter. These tests are the contract for
// that -- which layout `p` picks, what the screen may and may not draw, which keys scroll, and which
// keys must not be able to reach the review underneath.

// overlayModel is the preview fixture at a size where the pane cannot fit, holding a diff long enough
// to have to scroll. previewOn starts off, so opening it exercises the toggle rather than the default.
func overlayModel(t *testing.T, rows int) reviewModel {
	t.Helper()
	m := previewModel(t)
	m.width, m.height = 80, 20
	m.previewOn = false
	lines := []string{"diff --git a/x.go b/x.go", "@@ -1 +1 @@"}
	for i := 1; i <= rows; i++ {
		lines = append(lines, fmt.Sprintf("+line %d", i))
	}
	m.patchFor = func(_ context.Context, _ string) Patch {
		return Patch{Lines: lines, Added: rows, Deleted: 0}
	}
	return m
}

// openOverlay presses `p` and lets git answer, so the model that comes back is the overlay a reviewer
// is actually looking at rather than one still waiting on a patch.
func openOverlay(t *testing.T, m reviewModel) reviewModel {
	t.Helper()
	updated, cmd := m.Update(runeKey('p'))
	rm, ok := updated.(reviewModel)
	if !ok {
		t.Fatalf("`p` produced %T", updated)
	}
	if rm.mode != modePreview {
		t.Fatalf("`p` in %dx%d gave mode %v and status %q, want the overlay",
			rm.width, rm.height, rm.mode, rm.status)
	}
	return deliver(t, rm, cmd)
}

func TestSmallTerminalGivesThePreviewTheWholeScreen(t *testing.T) {
	m := openOverlay(t, overlayModel(t, 40))
	view := m.View()

	if strings.Contains(view, "│") {
		t.Errorf("the overlay is drawn as a split, so the list is still taking columns:\n%s", view)
	}
	if !strings.Contains(view, "+line 1") {
		t.Errorf("the overlay does not show the diff:\n%s", view)
	}
	if !strings.Contains(view, "x.go") {
		t.Errorf("the overlay does not say which file it is showing:\n%s", view)
	}
	// The list's chrome is what the diff is being bought with. If any of it survives, the rows it
	// costs come straight off the diff, which is the whole point.
	for _, absent := range []string{"reviewed", "base: ", "Threads"} {
		if strings.Contains(view, absent) {
			t.Errorf("the overlay still draws the list's %q:\n%s", absent, view)
		}
	}
	if !strings.Contains(view, "ctrl-f/b page") || !strings.Contains(view, "close") {
		t.Errorf("the shortcut bar does not name the keys this screen answers:\n%s", view)
	}
}

// The overlay is the fallback, not the preference: a terminal with room for two columns keeps the pane,
// because reading a diff while keeping the list is the better of the two.
func TestWideTerminalStillGetsAPaneNotAnOverlay(t *testing.T) {
	m := previewModel(t)
	m.previewOn = false
	updated, _ := m.Update(runeKey('p'))
	rm := updated.(reviewModel)
	if rm.mode != modeFiles {
		t.Errorf("`p` in %d columns took the whole screen; it has room for a pane of %d",
			rm.width, rm.paneWidth())
	}
	if rm.paneWidth() == 0 {
		t.Error("the pane did not come back on")
	}
}

func TestOverlayScrollsWithTheVimPrimitives(t *testing.T) {
	m := openOverlay(t, overlayModel(t, 60))
	total, body := m.previewRowsTouched()
	if total <= body {
		t.Fatalf("the fixture's diff (%d rows) fits the pane's window (%d): nothing to scroll", total, body)
	}
	down, up := tea.KeyMsg{Type: tea.KeyDown}, tea.KeyMsg{Type: tea.KeyUp}

	for _, tc := range []struct {
		name string
		keys []tea.KeyMsg
		want int
	}{
		{"j moves a row", []tea.KeyMsg{runeKey('j'), runeKey('j')}, 2},
		{"k gives one back", []tea.KeyMsg{runeKey('j'), runeKey('j'), runeKey('k')}, 1},
		{"the arrows do the same", []tea.KeyMsg{down, down, up}, 1},
		{"ctrl-d is half a page", []tea.KeyMsg{tea.KeyMsg{Type: tea.KeyCtrlD}}, body / 2},
		{"ctrl-u gives it back", []tea.KeyMsg{{Type: tea.KeyCtrlD}, {Type: tea.KeyCtrlU}}, 0},
		{"ctrl-f is a page", []tea.KeyMsg{{Type: tea.KeyCtrlF}}, body},
		{"ctrl-b comes back", []tea.KeyMsg{{Type: tea.KeyCtrlF}, {Type: tea.KeyCtrlB}}, 0},
		{"G goes to the bottom", []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("G")}}, total - body},
		{"gg goes back to the top", []tea.KeyMsg{
			{Type: tea.KeyRunes, Runes: []rune("G")},
			runeKey('g'), runeKey('g'),
		}, 0},
	} {
		m.previewOffset = 0
		for _, k := range tc.keys {
			m = pressKey(t, m, k)
		}
		if m.previewOffset != tc.want {
			t.Errorf("%s: offset = %d, want %d", tc.name, m.previewOffset, tc.want)
		}
	}

	// What the reviewer reads, not just the number: the bottom of the diff is on screen.
	m.previewOffset = 0
	m = pressKey(t, m, runeKey('G'))
	view := m.View()
	if !strings.Contains(view, "+line 60") {
		t.Errorf("G did not bring the last line on screen:\n%s", view)
	}
	if strings.Contains(view, "+line 1\n") {
		t.Errorf("G left the top of the diff on screen:\n%s", view)
	}
}

// At either end the frame stops moving rather than scrolling past what exists, and rather than
// appearing to drop the key.
func TestOverlayStopsAtBothEndsOfTheDiff(t *testing.T) {
	m := openOverlay(t, overlayModel(t, 30))
	total, body := m.previewRowsTouched()
	want := total - body
	if want < 1 {
		t.Fatalf("the fixture's diff does not overflow the overlay: %d rows, %d shown", total, body)
	}

	for i := 0; i < want*3; i++ {
		m = pressKey(t, m, runeKey('j'))
	}
	if m.previewOffset != want {
		t.Errorf("after scrolling past the end the offset is %d, clamped at %d", m.previewOffset, want)
	}
	for i := 0; i < want*3; i++ {
		m = pressKey(t, m, runeKey('k'))
	}
	if m.previewOffset != 0 {
		t.Errorf("after scrolling past the start the offset is %d, want 0", m.previewOffset)
	}
}

// The three keys the request names, plus the one that opened it. `q` closing rather than quitting is
// the case worth pinning: the bar says close, and a reviewer who means to leave the screen entirely
// presses it twice and gets out.
func TestOverlayDismissesWithQEscAndEnter(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{"q", runeKey('q')},
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}},
		{"enter", tea.KeyMsg{Type: tea.KeyEnter}},
		{"p", runeKey('p')},
	} {
		m := openOverlay(t, overlayModel(t, 40))
		before := m.cursor
		m = pressKey(t, m, runeKey('j'))
		m = pressKey(t, m, tc.key)

		if m.mode != modeFiles {
			t.Errorf("%s left the overlay in mode %v, want the list back", tc.name, m.mode)
		}
		if m.quitting {
			t.Errorf("%s quit the session; on this screen it closes the overlay", tc.name)
		}
		if m.cursor != before {
			t.Errorf("%s moved the cursor from %d to %d; scrolling a diff is not moving through files",
				tc.name, before, m.cursor)
		}
	}
}

// While the list is off the screen nothing that acts on it may be reachable: a mark set on a row the
// reviewer cannot see, a review submitted from a screen with no counter, an editor opened over the diff
// they were reading. The mode is what makes this hold without a list of exemptions.
func TestOverlayLetsNothingElseThrough(t *testing.T) {
	m := openOverlay(t, overlayModel(t, 40))
	before := marksOf(m.sess.Files())

	for _, tc := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{"space marks a file the reviewer cannot see", tea.KeyMsg{Type: tea.KeySpace}},
		{"s submits a review", runeKey('s')},
		{"e opens an editor", runeKey('e')},
		{"a opens ABOUT.md", runeKey('a')},
		{"d opens the difftool", runeKey('d')},
		{"t starts a thread", runeKey('t')},
		{"1 votes a thread", runeKey('1')},
		{"tab jumps section", tea.KeyMsg{Type: tea.KeyTab}},
		{"v walks spans", runeKey('v')},
		{"V opens the picker", runeKey('V')},
		{"r refreshes refs", runeKey('r')},
	} {
		updated, cmd := m.Update(tc.key)
		rm, ok := updated.(reviewModel)
		if !ok {
			t.Fatalf("%s produced %T", tc.name, updated)
		}
		if rm.mode != modePreview {
			t.Errorf("%s left the overlay for mode %v", tc.name, rm.mode)
		}
		if cmd != nil {
			t.Errorf("%s started something from the overlay; it should be unread here", tc.name)
		}
		if rm.status != "" {
			t.Errorf("%s left status %q; an unread key should be silent, not apologetic", tc.name, rm.status)
		}
	}

	after := marksOf(m.sess.Files())
	if len(after) != len(before) {
		t.Fatalf("the file list changed length: %d, want %d", len(after), len(before))
	}
	for i := range before {
		if after[i] != before[i] {
			t.Errorf("mark %d = %v, want %v", i, after[i], before[i])
		}
	}
}

// The one key that is not the overlay's own: ctrl-c leaves the program, from any screen.
func TestCtrlCLeavesFromTheOverlay(t *testing.T) {
	m := openOverlay(t, overlayModel(t, 40))
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	rm := updated.(reviewModel)
	if !rm.quitting || cmd == nil {
		t.Errorf("ctrl-c in the overlay: quitting = %v, cmd = %v, want both", rm.quitting, cmd != nil)
	}
}

// The overlay hides the list, and the list is where the span is named. A diff with nothing on screen
// saying which span it is is how a reviewer reads history and expects to be able to mark it.
func TestOverlayNamesTheSpanItShows(t *testing.T) {
	m := openOverlay(t, overlayModel(t, 8))
	if view := m.View(); !strings.Contains(view, "current") || !strings.Contains(view, "·") {
		t.Errorf("the overlay does not carry the span with the file:\n%s", view)
	}
}

// A historical span is read-only, and reading is exactly what the overlay does. This is the case the
// gate could easily have broken, because the overlay is entered from a screen that refuses everything.
func TestOverlayWorksOnAHistoricalSpan(t *testing.T) {
	m, _ := readonlyModel(t, historySel())
	// The floor size, because the fixture's diffs are short and this test also has to show that the
	// overlay scrolls: at 80 columns every historical diff fits without moving.
	m.width, m.height = previewOverlayMinWidth, previewOverlayMinHeight
	m.previewOn = false
	m = openOverlay(t, m)

	view := m.View()
	for _, want := range []string{"main...last review", "@@"} {
		if !strings.Contains(view, want) {
			t.Errorf("the historical overlay never shows %q:\n%s", want, view)
		}
	}
	total, body := m.previewRowsTouched()
	if total <= body {
		t.Fatalf("the fixture's diff (%d rows) fits the overlay (%d): nothing to scroll", total, body)
	}
	m = pressKey(t, m, runeKey('j'))
	if m.previewOffset != 1 {
		t.Errorf("a historical overlay scrolls to offset %d, want 1", m.previewOffset)
	}
	m = pressKey(t, m, runeKey('q'))
	if m.mode != modeFiles {
		t.Errorf("closing the historical overlay left mode %v", m.mode)
	}
}

// The note under the diff used to point at enter, which in the pane opens the difftool. On this screen
// enter closes it, so the note says nothing a key would contradict.
func TestOverlayNoteDoesNotPromiseEnter(t *testing.T) {
	m := openOverlay(t, overlayModel(t, 60))
	view := m.View()
	if !strings.Contains(view, "more rows") {
		t.Fatalf("the note does not say there is more to read:\n%s", view)
	}
	if strings.Contains(view, "enter opens") {
		t.Errorf("the pane's note travelled into the overlay, where enter closes it:\n%s", view)
	}

	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlF})
	view = m.View()
	if !strings.Contains(view, "rows ") {
		t.Fatalf("the note does not say where in the diff the overlay is:\n%s", view)
	}
	if strings.Contains(view, "enter opens") {
		t.Errorf("the position note promises enter:\n%s", view)
	}
}

// Closing and reopening returns to the same place, because the reviewer who pressed `q` to check the
// list and `p` to come back was not done reading.
func TestOverlayKeepsItsPlaceWhenReopened(t *testing.T) {
	m := openOverlay(t, overlayModel(t, 60))
	for i := 0; i < 5; i++ {
		m = pressKey(t, m, runeKey('j'))
	}
	m = pressKey(t, m, runeKey('q'))
	if m.mode != modeFiles {
		t.Fatalf("q left mode %v", m.mode)
	}
	m = openOverlay(t, m)
	if m.previewOffset != 5 {
		t.Errorf("reopening the overlay reset the scroll to %d, want 5", m.previewOffset)
	}
}

// The floor has to be arithmetic rather than a guess. At the smallest size the overlay accepts, the
// diff still has rows to show, and the frame is still the size of the terminal: a shortcut bar that
// wrapped past that point would push the frame over the bottom, and a frame taller than the terminal
// scrolls a row off the top on every repaint.
func TestOverlayFloorHasRoomToRead(t *testing.T) {
	m := openOverlay(t, overlayModel(t, 60))
	m.width, m.height = previewOverlayMinWidth, previewOverlayMinHeight

	if m.overlayShortfall() != "" {
		t.Fatalf("the overlay refuses its own floor: %s", m.overlayShortfall())
	}
	if m.previewBodyRows() < 1 {
		t.Fatalf("at %dx%d the diff gets %d rows", m.width, m.height, m.previewBodyRows())
	}
	rows := strings.Split(strings.TrimSuffix(m.View(), "\n"), "\n")
	if len(rows) > m.height {
		t.Errorf("the frame is %d rows tall in a %d-row terminal:\n%s", len(rows), m.height, m.View())
	}
	for _, row := range rows {
		// Cells, not bytes or runes: the diff carries colour escapes, and a row padded to the
		// terminal is only wrong if its *visible* width is over.
		if ansi.StringWidth(row) > m.width {
			t.Errorf("a row is %d cells wide in a %d-column terminal: %q", ansi.StringWidth(row), m.width, row)
			break
		}
	}

	m.height--
	if m.overlayShortfall() == "" {
		t.Errorf("at %dx%d the overlay still opened, and there is nothing left to read", m.width, m.height)
	}
}
