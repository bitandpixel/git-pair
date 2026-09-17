package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// In a terminal wide enough for two columns, `p` moves the keys into the diff instead of taking the
// pane off the screen: a reviewer reads a long file with the keys a diff is read with, in the same
// screen that still shows the list. These are the tests for that contract — which keys `p` hands over
// and which it hands back, what the pane must not be able to do while it holds the keyboard, how the
// frame says which column has the keys, and what happens when the terminal stops having room for the
// column that holds them. The overlay is the narrow terminal's half of the same feature, so the last
// test here is the one that pins that `p` still toggles it rather than focusing it.

// focusFixture is the preview fixture wide enough for the pane, holding a diff long enough that
// scrolling it is a different answer from not scrolling it. The lines are numbered with width so that
// "+line 001" cannot be found inside "+line 0017" and a test cannot pass by reading the wrong row.
func focusFixture(t *testing.T, rows int) reviewModel {
	t.Helper()
	m := previewModel(t)
	m.width, m.height = 140, 24
	lines := []string{"diff --git a/x.go b/x.go", "@@ -1 +1 @@"}
	for i := 1; i <= rows; i++ {
		lines = append(lines, fmt.Sprintf("+line %03d", i))
	}
	m.patchFor = func(_ context.Context, _ string) Patch {
		return Patch{Lines: lines, Added: rows, Deleted: 0}
	}
	return m
}

// keyMsg is a control key, which has no rune to build it from.
func keyMsg(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }

// paneKey presses one key with the pane holding the keys and lets git answer, so the model that comes
// back is a pane with a diff in it rather than one still waiting on a patch.
func paneKey(t *testing.T, m reviewModel, k tea.KeyMsg) reviewModel {
	t.Helper()
	updated, cmd := m.Update(k)
	rm, ok := updated.(reviewModel)
	if !ok {
		t.Fatalf("a key produced %T", updated)
	}
	return deliver(t, rm, cmd)
}

// focusPane presses `p` and insists it did what `p` now means in a wide terminal: the pane is still on
// the screen and it has the keys.
func focusPane(t *testing.T, m reviewModel) reviewModel {
	t.Helper()
	rm := paneKey(t, m, runeKey('p'))
	if !rm.previewHasFocus() {
		t.Fatalf("`p` at %dx%d left the keys with the list: focus=%v pane=%d mode=%v status %q",
			rm.width, rm.height, rm.focus, rm.paneWidth(), rm.mode, rm.status)
	}
	return rm
}

func TestPFocusesThePaneInsteadOfHidingIt(t *testing.T) {
	m := focusFixture(t, 40)
	before := m.cursor
	rm := focusPane(t, m)

	if !rm.previewOn || rm.paneWidth() == 0 {
		t.Errorf("`p` took the pane off the screen instead of moving into it: previewOn=%v pane=%d",
			rm.previewOn, rm.paneWidth())
	}
	if rm.mode != modeFiles {
		t.Errorf("`p` changed the mode to %v; the pane is not a screen of its own", rm.mode)
	}
	if rm.cursor != before {
		t.Errorf("`p` moved the list cursor from %d to %d", before, rm.cursor)
	}
}

func TestThePaneReadsTheKeysTheOverlayReads(t *testing.T) {
	tests := []struct {
		name   string
		keys   []tea.KeyMsg
		offset func(body int) int
		want   []string
		dont   []string
	}{
		{
			name: "j moves one row",
			keys: []tea.KeyMsg{runeKey('j')},
			// One row off the top: the pane's own counting, not the list's.
			offset: func(int) int { return 1 },
		},
		{
			name:   "ctrl-d is half a page",
			keys:   []tea.KeyMsg{keyMsg(tea.KeyCtrlD)},
			offset: func(body int) int { return body / 2 },
		},
		{
			name:   "ctrl-f is a page",
			keys:   []tea.KeyMsg{keyMsg(tea.KeyCtrlF)},
			offset: func(body int) int { return body },
		},
		{
			name:   "ctrl-b pages back",
			keys:   []tea.KeyMsg{keyMsg(tea.KeyCtrlF), keyMsg(tea.KeyCtrlB)},
			offset: func(int) int { return 0 },
		},
		{
			name:   "k at the top stays at the top",
			keys:   []tea.KeyMsg{runeKey('k')},
			offset: func(int) int { return 0 },
		},
		{
			name: "G reads the end of the file",
			keys: []tea.KeyMsg{runeKey('G')},
			want: []string{"+line 040"},
			dont: []string{"+line 001"},
		},
		{
			name: "gg goes back to the top",
			keys: []tea.KeyMsg{runeKey('G'), runeKey('g'), runeKey('g')},
			want: []string{"+line 001"},
			dont: []string{"+line 040"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := focusPane(t, focusFixture(t, 40))
			body := m.previewBodyRows()
			for _, k := range tc.keys {
				m = paneKey(t, m, k)
			}
			if tc.offset != nil {
				if want := tc.offset(body); m.previewOffset != want {
					t.Errorf("offset is %d, want %d (body %d rows)", m.previewOffset, want, body)
				}
			}
			view := ansi.Strip(m.View())
			for _, want := range tc.want {
				if !strings.Contains(view, want) {
					t.Errorf("%s is not on screen:\n%s", want, view)
				}
			}
			for _, dont := range tc.dont {
				if strings.Contains(view, dont) {
					t.Errorf("%s is still on screen after %v:\n%s", dont, tc.keys, view)
				}
			}
		})
	}
}

func TestTheListDoesNotMoveWhileThePaneHasTheKeys(t *testing.T) {
	m := focusFixture(t, 40)
	before := m.cursor
	m = focusPane(t, m)

	furthest := 0
	for _, k := range []tea.KeyMsg{runeKey('j'), runeKey('j'), keyMsg(tea.KeyCtrlD), runeKey('G'), runeKey('g'), runeKey('g')} {
		m = paneKey(t, m, k)
		if m.previewOffset > furthest {
			furthest = m.previewOffset
		}
	}
	if m.cursor != before {
		t.Errorf("the list cursor moved from %d to %d while the diff had the keys", before, m.cursor)
	}
	if furthest == 0 {
		t.Error("the diff never scrolled, so the keys went nowhere at all")
	}
}

func TestNothingThatChangesTheReviewHappensWhileThePaneHasTheKeys(t *testing.T) {
	m := focusFixture(t, 40)
	m = focusPane(t, m)
	marked, folds, cursor := countMarked(m.sess), len(m.folded), m.cursor

	// `V` is on the list's bar and opens a screen of its own, so it is the read-shaped key most
	// likely to be pressed here by mistake. The keys that move the keys are not in this list: tab,
	// shift-tab, f and m do leave the pane, on purpose, and TestTabMovesTheKeysOutOfThePane is
	// where that is pinned.
	for _, k := range []tea.KeyMsg{keyMsg(tea.KeySpace), runeKey('s'), runeKey('t'), runeKey('e'), runeKey('a'), runeKey('c'), runeKey('V')} {
		m = paneKey(t, m, k)
	}

	if got := countMarked(m.sess); got != marked {
		t.Errorf("a file was marked from the pane: %d marked, was %d", got, marked)
	}
	if len(m.folded) != folds {
		t.Errorf("the tree folds changed from the pane: %d, was %d", len(m.folded), folds)
	}
	if m.cursor != cursor {
		t.Errorf("the list cursor moved from %d to %d", cursor, m.cursor)
	}
	if m.mode != modeFiles {
		t.Errorf("a key reached a screen of its own: mode %v, status %q", m.mode, m.status)
	}
	if m.quitting {
		t.Error("a key quit the session from the pane")
	}
	// `d` is the one inert key a reviewer is most likely to reach for, since the list bar names it
	// for the row they can still see. Inert is the answer, and the shortcut bar is the explanation.
	if _, cmd := m.handleDiffKey(runeKey('d')); cmd != nil {
		t.Error("`d` opened a difftool while the diff had the keys")
	}

	// The same keys do have their old effect once the list has them back, which is what makes the
	// assertions above about the focus rather than about a broken fixture.
	m = paneKey(t, m, keyMsg(tea.KeyEsc))
	m = paneKey(t, m, keyMsg(tea.KeySpace))
	if got := countMarked(m.sess); got == marked {
		t.Errorf("space marked nothing after the keys came back (%d marked), so the test above proved nothing", got)
	}
}

func TestQFromThePaneClosesThePreviewAndNotTheSession(t *testing.T) {
	m := focusPane(t, focusFixture(t, 40))
	m = paneKey(t, m, runeKey('q'))

	if m.quitting {
		t.Error("`q` in the preview quit the session; quitting is the list's key")
	}
	if m.previewOn || m.previewHasFocus() {
		t.Errorf("`q` left the preview on screen: previewOn=%v focus=%v", m.previewOn, m.focus)
	}
	if m.mode != modeFiles {
		t.Errorf("`q` left the session in mode %v", m.mode)
	}

	// Closing is not forgetting: `p` puts the pane back, and puts it back unfocused, because the
	// reviewer asked to look at it and not to be trapped in it.
	m = paneKey(t, m, runeKey('p'))
	if !m.previewOn || m.paneWidth() == 0 {
		t.Error("`p` after `q` did not bring the pane back")
	}
	if m.previewHasFocus() {
		t.Error("`p` after `q` took the keys as well as showing the pane")
	}
}

func TestQStillQuitsFromTheList(t *testing.T) {
	m := focusFixture(t, 40)
	updated, cmd := m.Update(runeKey('q'))
	rm, ok := updated.(reviewModel)
	if !ok {
		t.Fatalf("`q` produced %T", updated)
	}
	if !rm.quitting || cmd == nil {
		t.Error("`q` with the keys in the list no longer ends the session")
	}
}

func TestEscHandsTheKeysBackWithoutClosingThePreview(t *testing.T) {
	m := focusPane(t, focusFixture(t, 40))
	m = paneKey(t, m, keyMsg(tea.KeyEsc))

	if m.previewHasFocus() {
		t.Error("esc did not give the keys back to the list")
	}
	if !m.previewOn {
		t.Error("esc closed the preview; that is `q`, and esc was asked only for the keys")
	}
	before := m.cursor
	m = paneKey(t, m, runeKey('j'))
	if m.cursor == before {
		t.Error("the list did not take the keys back")
	}
}

func TestEnterOpensTheFileOnShow(t *testing.T) {
	m := focusPane(t, focusFixture(t, 40))
	path := m.previewPath
	if path == "" {
		t.Fatal("the pane has no file on show to open")
	}
	updated, cmd := m.handleDiffKey(tea.KeyMsg{Type: tea.KeyEnter})
	rm, ok := updated.(reviewModel)
	if !ok {
		t.Fatalf("enter produced %T", updated)
	}
	if cmd == nil {
		t.Error("enter did not hand the terminal to the difftool for the file being read")
	}
	if !rm.previewHasFocus() {
		t.Error("enter took the keys away from the pane it was reading")
	}

	// A pane with nothing in it says so rather than opening the whole span.
	empty := focusFixture(t, 3)
	empty.previewPath = ""
	updated, cmd = empty.handleDiffKey(tea.KeyMsg{Type: tea.KeyEnter})
	rm = updated.(reviewModel)
	if cmd != nil {
		t.Error("enter opened something with no file on show")
	}
	if !strings.Contains(rm.status, "no file on show") {
		t.Errorf("enter over an empty pane said %q, want the reason", rm.status)
	}
}

func TestAResizeAwayFromThePaneGivesTheKeysBack(t *testing.T) {
	m := focusPane(t, focusFixture(t, 40))

	// A terminal dragged narrower than the pane's floor takes the column away. The flag is still set,
	// which is exactly why nothing reads it directly: a keyboard held by a column that is no longer
	// drawn is a session that reads no keys at all.
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	rm, ok := updated.(reviewModel)
	if !ok {
		t.Fatalf("a resize produced %T", updated)
	}
	if rm.focus != focusPreview {
		t.Fatal("the fixture stopped holding the keys, so this tests nothing")
	}
	if rm.previewHasFocus() {
		t.Error("the pane kept the keys after the terminal took the pane away")
	}
	before := rm.cursor
	rm = paneKey(t, rm, runeKey('j'))
	if rm.cursor == before {
		t.Error("no column had the keys: the list did not take them back")
	}
}

// ruleAt is the glyph the frame draws in the divider's cell, from whichever row reaches it first.
func ruleAt(view string, at int) string {
	for _, row := range strings.Split(view, "\n") {
		if g := dividerColumn(row, at); g != "" {
			return g
		}
	}
	return ""
}

func TestTheFocusedColumnSaysSo(t *testing.T) {
	// Two of the three signals are text, on purpose: the terminal is not obliged to render bold or
	// faint, and a focus the reviewer cannot see is a focus that eats keystrokes. The divider glyph and
	// the shortcut bar survive a terminal with no styling at all; the list cursor losing its reverse
	// video is the one signal that is styling only, and so the one here that cannot be asserted.
	unfocusedModel := focusFixture(t, 40)
	paneModel := focusPane(t, focusFixture(t, 40))
	unfocused := ansi.Strip(unfocusedModel.View())
	focused := ansi.Strip(paneModel.View())
	// The divider is read in its own cell rather than by searching for the glyph: the changeset box
	// draws a rule down its left edge, and an ordinary rule is what a box is made of.
	at := paneModel.dividerAt()

	if got := ruleAt(unfocused, at); got != "│" {
		t.Errorf("the divider draws %q rather than a plain rule while the list has the keys:\n%s", got, unfocused)
	}
	if !strings.Contains(unfocused, "space reviewed") {
		t.Errorf("the list's own shortcut bar is missing its keys:\n%s", unfocused)
	}
	if got := ruleAt(focused, at); got != "║" {
		t.Errorf("the divider draws %q rather than the double rule that says which column has the keys:\n%s", got, focused)
	}
	if !strings.Contains(focused, "q close preview") {
		t.Errorf("the bar does not name the key that closes the preview:\n%s", focused)
	}
	// The bar is the whole set of what the pane reads: a key left on it would be a key promised and
	// not delivered, which is the failure the read-only bar is careful about for a different reason.
	for _, gone := range []string{"space reviewed", "V picker", "d diff"} {
		if strings.Contains(focused, gone) {
			t.Errorf("the pane's bar still offers %q, which it does not read:\n%s", gone, focused)
		}
	}
}

// The overlay is the narrow terminal's half of this, and `p` means the old thing there: the diff takes
// the whole screen and gives it back. Nothing about the focus applies, because there is no pane to
// move into — which is what these assert, alongside the overlay's own keys that the overlay tests
// already cover.
func TestTheOverlayStillTogglesRatherThanTakingFocus(t *testing.T) {
	m := openOverlay(t, overlayModel(t, 40))
	if m.previewHasFocus() {
		t.Error("opening the overlay set the pane's flag; the two layouts are not the same thing")
	}

	rm := paneKey(t, m, runeKey('p'))
	if rm.mode != modeFiles {
		t.Errorf("`p` in the overlay did not close it (mode %v, status %q)", rm.mode, rm.status)
	}
	if rm.previewHasFocus() {
		t.Error("closing the overlay left the pane flag set")
	}
	if rm.paneWidth() > 0 {
		t.Error("the pane appeared in a terminal with no room for it")
	}
}
