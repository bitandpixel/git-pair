package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The pane's search: `/` to look for a term in the file on show, its matches underlined as the term is
// typed, `enter` to stand on one and `n`/`N` to walk them. Matching is done against git's own line rather
// than the drawn row, so a term the column broke in half is still found; the marks are put on the line
// before it is broken into rows, so both halves of that term are marked.

// --- the marks ---------------------------------------------------------------------------------

// TestTheHighlightMarksTheMatchWithoutTouchingTheColour is about how the marks are drawn: the diff's green
// and red are git's bytes, and a highlight that rebuilt the row from its plain text would draw the diff in
// white.
func TestTheHighlightMarksTheMatchWithoutTouchingTheColour(t *testing.T) {
	line := "\x1b[31m+func a() { return 1\x1b[0m"
	got := highlightRow(line, "return", false)
	if !strings.Contains(got, sgrUnderline+"return"+sgrUnderlineOff) {
		t.Errorf("the match is not underlined: %q", got)
	}
	if !strings.HasPrefix(got, "\x1b[31m") || !strings.HasSuffix(got, sgrReset) {
		t.Errorf("git's own colour was lost: %q", got)
	}
	if strings.Contains(got, sgrReverse) {
		t.Errorf("a match the reviewer is not standing on is picked out anyway: %q", got)
	}
	if again := highlightRow(line, "return", true); !strings.Contains(again, sgrReverse+"return"+sgrReverseOff) {
		t.Errorf("the match the reviewer is standing on is not picked out: %q", again)
	}
}

// TestTheHighlightMarksEveryMatchOnTheLine: `n` lands on one match, but the rest of them have to be visible
// too, or a line with the name in it twice reads as a line with it in it once.
func TestTheHighlightMarksEveryMatchOnTheLine(t *testing.T) {
	got := highlightRow("  x lock lock y", "lock", false)
	if n := strings.Count(got, sgrUnderline); n != 2 {
		t.Errorf("%d matches marked in %q, want 2", n, got)
	}
	if strip := ansi.Strip(got); strip != "  x lock lock y" {
		t.Errorf("the marks changed the text: %q", strip)
	}
}

// TestTheSearchIsFoldedUnlessTheTermHasACapital is the smart-case rule, and why it is worth having: a
// reviewer looking for a function does not know or care whether the file capitalises it.
func TestTheSearchIsFoldedUnlessTheTermHasACapital(t *testing.T) {
	rows := []previewRow{
		{line: "+  func ab() {"},
		{line: "\x1b[31m+Abc\x1b[0m"},
	}
	if got := matchRows(rows, "ab"); len(got) != 2 {
		t.Errorf("a lower-case term matched %v, want both rows", got)
	}
	if got := matchRows(rows, "Ab"); len(got) != 1 || got[0] != 1 {
		t.Errorf("a term with a capital matched %v, want only the row that matches it exactly", got)
	}
}

// TestTheSearchLooksInGitLineNotTheRow is the reason a row carries the line it came from: a line wider than
// the column is drawn as several rows, and the term the reviewer is looking for is as likely as not to sit
// where one of those rows ends.
func TestTheSearchLooksInGitLineNotTheRow(t *testing.T) {
	// Narrow enough that the column's break falls inside the term.
	rows := previewBody(Patch{Lines: []string{"+aaaa bbbb cccc"}}, 12, marks{term: "bbbb", current: -1})
	if len(rows) < 2 {
		t.Fatalf("the line was not broken: %d rows", len(rows))
	}
	if strings.Contains(ansi.Strip(rows[0].text), "bbbb") {
		t.Fatalf("the fixture does not break the term after all: %q", rows[0].text)
	}
	got := matchRows(rows, "bbbb")
	if len(got) != 1 || got[0] != 0 {
		t.Errorf("the broken term matched rows %v, want the one row the line starts on", got)
	}
	// Both halves of the term are marked, each on the row it is drawn on.
	for i, row := range rows {
		if !strings.Contains(row.text, sgrUnderline) {
			t.Errorf("row %d carries none of the mark: %q", i, row.text)
		}
	}
}

// --- the field ---------------------------------------------------------------------------------

// searchModel is the fixture with the keys in the pane: 60 numbered lines in a body that shows about a
// third of them, so there is a term to look for and somewhere to jump to.
func searchModel(t *testing.T) reviewModel {
	t.Helper()
	return focusPane(t, focusFixture(t, 60))
}

// searchRow is the row one of the fixture's "+line 0NN" lines is drawn on, past git's two header rows.
func searchRow(n int) int { return n + 1 }

// searchRows are the rows carrying term, worked out from the fixture's own lines.
func searchRows(term string) []int {
	var out []int
	for i := 1; i <= 60; i++ {
		if strings.Contains(fmt.Sprintf("+line %03d", i), term) {
			out = append(out, searchRow(i))
		}
	}
	return out
}

// typeSearch presses `/` and types a term, leaving the field open.
func typeSearch(t *testing.T, m reviewModel, term string) reviewModel {
	t.Helper()
	m = paneKey(t, m, runeKey('/'))
	for _, r := range term {
		m = paneKey(t, m, runeKey(r))
	}
	return m
}

// TestTypingSearchesWithoutMoving is what the field does before `enter`: the marks follow the term as it is
// typed and the pane keeps its place, so the reviewer can see what a term means before committing to
// jumping somewhere with it.
func TestTypingSearchesWithoutMoving(t *testing.T) {
	m := searchModel(t)
	before := m.previewOffset

	m = typeSearch(t, m, "line 007")
	if !m.searching {
		t.Fatal("the field did not open")
	}
	if m.previewSearch != "" {
		t.Errorf("typing committed the term: %q", m.previewSearch)
	}
	if m.searchTerm() != "line 007" {
		t.Errorf("the pane is marking %q, want what was typed", m.searchTerm())
	}
	if m.previewOffset != before {
		t.Errorf("typing moved the pane to %d", m.previewOffset)
	}
	if m.currentMatch() >= 0 {
		t.Error("a match is picked out before `enter` chose one")
	}
	var marked int
	for _, row := range m.previewLines() {
		if strings.Contains(row, sgrUnderline) {
			marked++
		}
	}
	if marked == 0 {
		t.Error("nothing is marked while the term is being typed")
	}
	// The field is the pane's bottom row -- the row the note about the rest of the file uses, which is why
	// opening it moves nothing above it.
	rows := m.previewLines()
	if last := rows[len(rows)-1]; !strings.Contains(last, "/ line 007"+threadPromptCursor) {
		t.Errorf("the field is not on the pane's bottom row: %q", last)
	}
}

// TestEnterChoosesTheMatch says what `enter` is for: the term becomes the term, and the pane moves to the
// first match at or below what was already on screen.
func TestEnterChoosesTheMatch(t *testing.T) {
	m := searchModel(t)
	m = typeSearch(t, m, "line 060")
	m = paneKey(t, m, keyMsg(tea.KeyEnter))

	if m.searching {
		t.Error("enter left the field open")
	}
	if m.previewSearch != "line 060" {
		t.Errorf("the term is %q", m.previewSearch)
	}
	if m.previewMatchTerm != "line 060" {
		t.Errorf("the match belongs to %q, want the term it was found with", m.previewMatchTerm)
	}
	if want := searchRows("line 060")[0]; m.previewMatch != want {
		t.Errorf("the pane is on row %d, want the match at %d", m.previewMatch, want)
	}
	body := m.previewBodyRows()
	if m.previewMatch < m.previewOffset || m.previewMatch >= m.previewOffset+body {
		t.Errorf("the match at %d is off screen: the pane starts at %d and shows %d rows", m.previewMatch, m.previewOffset, body)
	}
	// The count is part of what the pane says, because it is what tells the reviewer whether `n` has
	// anywhere left to go.
	rows := m.previewLines()
	if note := rows[len(rows)-1]; !strings.Contains(note, `·  1 for "line 060"`) {
		t.Errorf("the pane's bottom row does not count the matches: %q", note)
	}
}

// TestTheMatchWalkWrapsAtBothEnds: every match has to be reachable from anywhere, and a reviewer at the
// bottom of a long diff should find the match at the top rather than a status line.
func TestTheMatchWalkWrapsAtBothEnds(t *testing.T) {
	m := searchModel(t)
	want := searchRows("line 03") // ten of them, all below the first screen
	if len(want) < 3 {
		t.Fatalf("the fixture has %d matches for the term, want a walk worth testing", len(want))
	}

	m = typeSearch(t, m, "line 03")
	m = paneKey(t, m, keyMsg(tea.KeyEnter))
	for _, row := range want {
		if m.previewMatch != row {
			t.Fatalf("on row %d, want %d", m.previewMatch, row)
		}
		if m.previewMatch < m.previewOffset || m.previewMatch >= m.previewOffset+m.previewBodyRows() {
			t.Fatalf("row %d is off screen: the pane starts at %d", m.previewMatch, m.previewOffset)
		}
		m = paneKey(t, m, runeKey('n'))
	}
	if m.previewMatch != want[0] {
		t.Errorf("after the last match, `n` went to %d, want the first at %d", m.previewMatch, want[0])
	}
	m = paneKey(t, m, runeKey('N'))
	if m.previewMatch != want[len(want)-1] {
		t.Errorf("`N` from the first match went to %d, want the last at %d", m.previewMatch, want[len(want)-1])
	}
}

// TestTheSearchSaysWhenTheTermIsNotThere. A pane that stays where it is and says nothing would leave the
// reviewer wondering whether the key was read at all.
func TestTheSearchSaysWhenTheTermIsNotThere(t *testing.T) {
	m := searchModel(t)
	before := m.previewOffset
	m = typeSearch(t, m, "zebra")
	m = paneKey(t, m, keyMsg(tea.KeyEnter))

	if !strings.Contains(m.View(), `no match for "zebra"`) {
		t.Errorf("no refusal for a term that is not in the file:\n%s", m.View())
	}
	if m.previewOffset != before || m.previewMatch >= 0 {
		t.Errorf("a search with no match moved the pane to %d", m.previewOffset)
	}
	if m.previewSearch != "zebra" {
		t.Errorf("the term was dropped for finding nothing: %q", m.previewSearch)
	}
}

// TestTheFieldOwnsTheKeysWhileItIsOpen. The field is where the reviewer is, so the pane's keys are paused
// there -- including `q`, which would otherwise quit on a reviewer typing a term that starts with q.
func TestTheFieldOwnsTheKeysWhileItIsOpen(t *testing.T) {
	m := searchModel(t)
	m = paneKey(t, m, runeKey('/'))
	for _, r := range "qdunN" {
		m = paneKey(t, m, runeKey(r))
	}
	if m.quitting {
		t.Error("typing a q quit")
	}
	if m.searchInput != "qdunN" {
		t.Errorf("the field holds %q, want the keys as typed", m.searchInput)
	}
	if m.previewOffset != 0 {
		t.Errorf("typing scrolled the pane to %d", m.previewOffset)
	}
	m = paneKey(t, m, keyMsg(tea.KeyBackspace))
	if m.searchInput != "qdun" {
		t.Errorf("backspace left %q", m.searchInput)
	}
	m = paneKey(t, m, keyMsg(tea.KeyCtrlH))
	if m.searchInput != "qdu" {
		t.Errorf("ctrl-h left %q", m.searchInput)
	}
}

// TestEscapeClosesTheFieldAndKeepsTheTerm: a reviewer who mistypes should not lose the match they were
// reading, and should not have to type the term again to look for it further down.
func TestEscapeClosesTheFieldAndKeepsTheTerm(t *testing.T) {
	m := searchModel(t)
	m = typeSearch(t, m, "line 007")
	m = paneKey(t, m, keyMsg(tea.KeyEsc))
	if m.searching || m.searchInput != "" {
		t.Error("esc did not close the field")
	}
	if m.previewSearch != "" {
		t.Errorf("esc committed what was typed: %q", m.previewSearch)
	}

	m = typeSearch(t, m, "line 007")
	m = paneKey(t, m, keyMsg(tea.KeyEnter))
	m = paneKey(t, m, runeKey('/'))
	if m.searchInput != "line 007" {
		t.Errorf("the field opened with %q, want the last term so that `enter` means the next match", m.searchInput)
	}
	m = paneKey(t, m, keyMsg(tea.KeyEsc))
	if m.previewSearch != "line 007" {
		t.Errorf("the committed term did not survive the field: %q", m.previewSearch)
	}
	if m.currentMatch() < 0 {
		t.Error("closing the field lost the match the reviewer was standing on")
	}
}

// TestTheFieldIsTheOnlyThingThePaneReadsWhileItIsOpen. The pane's ways out -- `esc`, `tab`, `f` -- are
// paused while a term is being typed, and the first `esc` closes the field rather than the pane. Two presses
// is the price of aborting a search; what was committed keeps marking the file either way, because the marks
// are on the diff rather than on the field.
func TestTheFieldIsTheOnlyThingThePaneReadsWhileItIsOpen(t *testing.T) {
	m := searchModel(t)
	m = typeSearch(t, m, "line 007")
	m = paneKey(t, m, keyMsg(tea.KeyEnter))
	m = typeSearch(t, m, "x")
	before := m.previewOffset

	m = paneKey(t, m, keyMsg(tea.KeyTab))
	if !m.searching || !m.previewHasFocus() {
		t.Errorf("`tab` took the keys out of the pane while a term was being typed: searching=%v focus=%v",
			m.searching, m.focus)
	}
	m = paneKey(t, m, keyMsg(tea.KeyEsc))
	if m.searching {
		t.Error("`esc` did not close the field")
	}
	if !m.previewHasFocus() {
		t.Error("the `esc` that closes the field also closed the pane")
	}
	m = paneKey(t, m, keyMsg(tea.KeyEsc))
	if m.previewHasFocus() {
		t.Error("the second `esc` did not give the keys back to the list")
	}
	if m.previewOffset != before {
		t.Errorf("aborting a search moved the pane to %d", m.previewOffset)
	}
	if m.searchTerm() != "line 007" {
		t.Errorf("the pane is marking %q, want what was committed", m.searchTerm())
	}
	// The one match in this file is also the one `enter` landed on, so it is the reverse-videoed one. The
	// marks belong to the file rather than to the pane's keys, and giving the keys back keeps them.
	var marked bool
	for _, row := range m.previewLines() {
		if strings.Contains(row, sgrUnderline) || strings.Contains(row, sgrReverse) {
			marked = true
		}
	}
	if !marked {
		t.Error("giving the keys back dropped the marks the reviewer had committed to")
	}
}

// --- reading the pane ---------------------------------------------------------------------------

// TestTheHalfPageKeysKeepTheirContext is what `d` and `u` are for in `less`, and why the pane has them
// beside the ctrl pairs it already had: consecutive presses keep a line of what was just read on screen.
func TestTheHalfPageKeysKeepTheirContext(t *testing.T) {
	m := searchModel(t)
	half := m.previewBodyRows() / 2
	if half < 2 {
		t.Fatalf("the fixture's pane is %d rows, too short to page by halves", m.previewBodyRows())
	}

	m = paneKey(t, m, runeKey('d'))
	if m.previewOffset != half {
		t.Errorf("`d` moved to %d, want half a body (%d)", m.previewOffset, half)
	}
	m = paneKey(t, m, keyMsg(tea.KeyCtrlD))
	if m.previewOffset != 2*half {
		t.Errorf("ctrl-d moved to %d, want %d", m.previewOffset, 2*half)
	}
	m = paneKey(t, m, runeKey('u'))
	if m.previewOffset != half {
		t.Errorf("`u` moved back to %d, want %d", m.previewOffset, half)
	}
	m = paneKey(t, m, keyMsg(tea.KeyCtrlU))
	if m.previewOffset != 0 {
		t.Errorf("ctrl-u moved back to %d, want the top", m.previewOffset)
	}
}

// The overlay is the diff on a narrow terminal, and there it is the only form the diff has. A search the
// pane had and the overlay did not would be a feature narrow terminals do not get.
func TestTheOverlaySearchesToo(t *testing.T) {
	m := openOverlay(t, overlayModel(t, 40))
	m = typeSearch(t, m, "line 3")
	if m.mode != modePreview {
		t.Fatalf("the field took the overlay down to mode %v", m.mode)
	}
	if !m.searching {
		t.Fatal("the overlay does not read `/`")
	}
	assertFrameFits(t, m)
	view := m.View()
	if !strings.Contains(view, "/ line 3"+threadPromptCursor) {
		t.Errorf("the overlay does not show the field:\n%s", view)
	}
	if !strings.Contains(view, sgrUnderline) {
		t.Errorf("the overlay does not mark the match:\n%s", view)
	}
	m = paneKey(t, m, keyMsg(tea.KeyEnter))
	if m.mode != modePreview {
		t.Errorf("committing a search closed the overlay: mode %v", m.mode)
	}
}

// A term is worth keeping when the file changes under it: the name a reviewer is chasing across a changeset
// is the same name in the next file, which is how a term gets used more than once. The row it pointed at is
// not worth keeping, because it means nothing in the new file.
func TestTheTermTravelsAndTheRowDoesNot(t *testing.T) {
	m := searchModel(t)
	m = typeSearch(t, m, "line 007")
	m = paneKey(t, m, keyMsg(tea.KeyEnter))
	if m.previewMatch < 0 {
		t.Fatal("the fixture did not land on a match")
	}

	m = paneKey(t, m, keyMsg(tea.KeyEsc)) // the keys back on the list, which is where files are chosen
	other := indexOf(t, m, rowFile)
	if m.rows[other].path == m.previewPath {
		other = indexOfNameBySuffix(t, m, ".yaml")
	}
	m.cursor = other
	m = askPreview(t, m)

	if m.previewSearch != "line 007" {
		t.Errorf("changing files dropped the term: %q", m.previewSearch)
	}
	if m.previewMatch >= 0 {
		t.Errorf("the pane still claims to be on row %d of the file it left", m.previewMatch)
	}
}
