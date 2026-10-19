package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gitpair/internal/gittest"
)

// A count typed before a navigation key says how far that key goes: `10j` walks ten rows, `2 ctrl-f`
// pages two pages, and the digits wait for the key they belong to without moving anything. What these
// tests pin is the two halves of that bargain -- the motion honours the number typed in front of it, and
// the number dies with the first key after it, whether that key knew the number was there or not.

// countKeys is a count and the motion it belongs to as a terminal sends them: one message per digit and
// one for the key. `10j` is three keystrokes, so a test that hands `Update` one message carrying "10j"
// is testing something no keyboard can produce -- and a motion that read the whole run at once would
// never have to accumulate anything.
func countKeys(digits string, motion tea.KeyMsg) []tea.KeyMsg {
	keys := make([]tea.KeyMsg, 0, len(digits)+1)
	for _, r := range digits {
		keys = append(keys, runeKey(r))
	}
	return append(keys, motion)
}

// pressAll is a run of keystrokes through `Update`, one message each: what a count and its motion look
// like arriving.
func pressAll(t *testing.T, m reviewModel, keys []tea.KeyMsg) reviewModel {
	t.Helper()
	for _, k := range keys {
		m = pressKey(t, m, k)
	}
	return m
}

// pressAllPane is pressAll for the keys a focused pane reads, which lets git answer between them so the
// model that comes back is a pane with a diff in it rather than one still waiting on a patch.
func pressAllPane(t *testing.T, m reviewModel, keys []tea.KeyMsg) reviewModel {
	t.Helper()
	for _, k := range keys {
		m = paneKey(t, m, k)
	}
	return m
}

// firstMarkableRow is the first row of the tree that takes a reviewed mark: one of the span's own files.
// The reviewer's working-tree rows sit in the tree beside them and `space` refuses those, so a test that
// means to prove `space` did something cannot stand on one.
func firstMarkableRow(t *testing.T, m reviewModel) int {
	t.Helper()
	for i, r := range m.rows[:m.metaStart] {
		if r.kind == rowFile && !r.outside {
			return i
		}
	}
	t.Fatalf("the tree holds no file of the span, so nothing can be marked:\n%s", rowList(m))
	return -1
}

// The tree of a long changeset is read by counting, and the count is the difference between ten
// keystrokes and one. The row keys of the region holding the keys honour it in both directions, under the
// letter and under the arrow, and a step of ten clamps inside the reviewer's own half exactly as a single
// step does at the edge.
func TestTenJWalksTenRowsOfTheTree(t *testing.T) {
	m := manyFileModel(t, 24)
	last := m.metaStart - 1
	start := 12
	if last < start+10 {
		t.Fatalf("the tree has %d rows; a counted walk of 10 from row %d needs at least %d",
			last+1, start, start+10)
	}
	boxAt := m.metaCursor

	for _, tc := range []struct {
		name string
		keys []tea.KeyMsg
		want int
	}{
		{"10j walks ten rows", countKeys("10", runeKey('j')), start + 10},
		{"5k walks five back", countKeys("5", runeKey('k')), start - 5},
		{"the down arrow counts too", countKeys("7", keyMsg(tea.KeyDown)), start + 7},
		{"the up arrow counts too", countKeys("4", keyMsg(tea.KeyUp)), start - 4},
	} {
		here := focusOnRow(t, m, start)
		here = pressAll(t, here, tc.keys)
		if here.cursor != tc.want {
			t.Errorf("%s: cursor = %d, want %d:\n%s", tc.name, here.cursor, tc.want, rowList(here))
		}
		if here.metaCursor != boxAt {
			t.Errorf("%s moved the changeset box from %d to %d", tc.name, boxAt, here.metaCursor)
		}
		if here.status != "" {
			t.Errorf("%s said %q; typing a number is not a reason to speak", tc.name, here.status)
		}
		assertFrameFits(t, here)
	}
}

// The box and the tree are two windows over one list, and the edge they share is passable to a step of
// one only. Anything counted stays inside the window it started in, because the two halves have different
// heights and a distance counted in one means nothing in the other -- so a held digit lands the reviewer
// on the end of the region they were reading, never in the one beside it.
func TestACountedStepStopsAtTheEndOfItsOwnHalf(t *testing.T) {
	m := manyFileModel(t, 24)
	if m.metaStart < 4 || len(m.rows)-m.metaStart < 2 {
		t.Fatalf("the fixture is %d rows with the box at %d: neither half has ends to stop at",
			len(m.rows), m.metaStart)
	}

	down := focusOnRow(t, m, 0)
	down = pressAll(t, down, countKeys("9999", runeKey('j')))
	if down.focus != focusFiles || down.cursor != m.metaStart-1 {
		t.Errorf("9999j from the tree's top gave %v on row %d, want the tree on its last row %d",
			down.focus, down.cursor, m.metaStart-1)
	}

	up := focusOnRow(t, m, m.metaStart-1)
	up = pressAll(t, up, countKeys("9999", runeKey('k')))
	if up.focus != focusFiles || up.cursor != 0 {
		t.Errorf("9999k from the tree's bottom gave %v on row %d, want the tree on its first row",
			up.focus, up.cursor)
	}

	intoBox := focusOnRow(t, m, m.metaStart)
	intoBox = pressAll(t, intoBox, countKeys("9999", runeKey('j')))
	if !intoBox.metaHasFocus() || intoBox.metaCursor != len(intoBox.rows)-1 {
		t.Errorf("9999j from the box's top gave %v on row %d, want the box on its last row %d",
			intoBox.focus, intoBox.metaCursor, len(intoBox.rows)-1)
	}

	out := focusOnRow(t, m, len(m.rows)-1)
	out = pressAll(t, out, countKeys("9999", runeKey('k')))
	if !out.metaHasFocus() || out.metaCursor != m.metaStart {
		t.Errorf("9999k from the box's bottom gave %v on row %d, want the box on its first row %d",
			out.focus, out.metaCursor, m.metaStart)
	}
	assertFrameFits(t, down)
	assertFrameFits(t, intoBox)
}

// `ctrl-f` is the window the reviewer is looking at, so the number in front of it counts windows: two
// pages is twice what the region draws, and the two page keys stay symmetrical -- three pages down and
// three pages back is the row the reviewer stood on.
func TestTwoCtrlFPagesTwoWindowsOfTheRegion(t *testing.T) {
	m := manyFileModel(t, 40)
	start := 0
	window := focusOnRow(t, m, start).activeWindow()
	if m.metaStart < 3*window+2 {
		t.Fatalf("the tree has %d rows and its window is %d: two pages down and three back is not a thing to "+
			"test here", m.metaStart, window)
	}

	for _, tc := range []struct {
		name string
		keys []tea.KeyMsg
		want int
	}{
		{"ctrl-f alone is one window", []tea.KeyMsg{keyMsg(tea.KeyCtrlF)}, window},
		{"2 ctrl-f is two windows", countKeys("2", keyMsg(tea.KeyCtrlF)), 2 * window},
		{"3 ctrl-b pages three windows back",
			append(countKeys("3", keyMsg(tea.KeyCtrlF)), countKeys("3", keyMsg(tea.KeyCtrlB))...), 0},
	} {
		here := focusOnRow(t, m, start)
		here = pressAll(t, here, tc.keys)
		if here.cursor != tc.want {
			t.Errorf("%s: cursor = %d, want %d (the window is %d rows)",
				tc.name, here.cursor, tc.want, window)
		}
	}
	assertFrameFits(t, m)
}

// The half-page pairs promise a distance with a line of context left in it, which is a property of the
// window rather than of whatever was typed before the key. Five half-pages is not a thing, so `5 ctrl-d`
// moves exactly what `ctrl-d` moves -- and a key the reviewer did not press is not paid for by the count.
func TestFiveCtrlDStillMovesHalfAWindow(t *testing.T) {
	m := manyFileModel(t, 40)
	if m.metaStart < 20 {
		t.Fatalf("the tree has %d rows, want enough for a half page and a counted one to differ", m.metaStart)
	}
	half := max(1, focusOnRow(t, m, 0).activeWindow()/2)

	plain := pressAll(t, focusOnRow(t, m, 0), []tea.KeyMsg{keyMsg(tea.KeyCtrlD)})
	counted := pressAll(t, focusOnRow(t, m, 0), countKeys("5", keyMsg(tea.KeyCtrlD)))
	if plain.cursor != half {
		t.Fatalf("ctrl-d moved %d rows, want half the window (%d): the fixture is not testing the counted case",
			plain.cursor, half)
	}
	if counted.cursor != half {
		t.Errorf("5 ctrl-d moved %d rows, want the same %d the plain key moves", counted.cursor, half)
	}

	// ... and the same at the other end of the region, where the half page has to stop as well.
	down := pressAll(t, focusOnRow(t, m, 0), []tea.KeyMsg{keyMsg(tea.KeyCtrlF)})
	back := pressAll(t, down, []tea.KeyMsg{keyMsg(tea.KeyCtrlU)})
	countedBack := pressAll(t, down, countKeys("5", keyMsg(tea.KeyCtrlU)))
	if back.cursor != max(0, down.cursor-half) {
		t.Errorf("ctrl-u from %d went to %d, want %d", down.cursor, back.cursor, max(0, down.cursor-half))
	}
	if countedBack.cursor != back.cursor {
		t.Errorf("5 ctrl-u from %d went to %d, want the same %d the plain key goes",
			down.cursor, countedBack.cursor, back.cursor)
	}
	assertFrameFits(t, counted)
}

// `gg` and `G` are the two ends of a region, and there is no such thing as the ninth end: the count in
// front of them is spent and ignored, and the reviewer lands where the key alone lands. What proves the
// count was spent rather than parked is the `j` afterwards, which moves one row.
func TestGGandGStayTheTwoEndsUnderACount(t *testing.T) {
	m := manyFileModel(t, 24)
	if m.metaStart < 6 {
		t.Fatalf("the tree has %d rows, want enough to stand in the middle of and be jumped over", m.metaStart)
	}

	mid := focusOnRow(t, m, 3)
	bottom := pressAll(t, mid, countKeys("9", runeKey('G')))
	if bottom.cursor != m.metaStart-1 {
		t.Errorf("9G gave row %d, want the tree's last row %d", bottom.cursor, m.metaStart-1)
	}
	after := pressKey(t, bottom, runeKey('j'))
	if after.cursor != m.metaStart-1 {
		t.Errorf("the j after 9G moved to %d; a count parked in front of G would move further", after.cursor)
	}

	top := pressAll(t, mid, append(countKeys("9", runeKey('g')), runeKey('g')))
	if top.cursor != 0 {
		t.Errorf("9gg gave row %d, want the tree's first row", top.cursor)
	}
	after = pressKey(t, top, runeKey('j'))
	if after.cursor != 1 {
		t.Errorf("the j after 9gg moved to %d, want one row: the count was spent by the jump, not kept",
			after.cursor)
	}

	// The box keeps the same two ends with its own cursor, counted or not.
	box := focusOnRow(t, m, m.metaStart)
	box = pressAll(t, box, countKeys("9", runeKey('G')))
	if box.metaCursor != len(box.rows)-1 {
		t.Errorf("9G in the box gave %d, want its last row %d", box.metaCursor, len(box.rows)-1)
	}
	box = pressAll(t, box, append(countKeys("9", runeKey('g')), runeKey('g')))
	if box.metaCursor != m.metaStart {
		t.Errorf("9gg in the box gave %d, want its first row %d", box.metaCursor, m.metaStart)
	}
	assertFrameFits(t, box)
}

// Between the number and the key it belongs to, vim keeps silence, and the silence is the feature: a
// digit that answered like a motion key would walk the reviewer somewhere they had not asked to go while
// they were still typing the distance, and would put a note on the screen for the typing of a number.
func TestADigitOnItsOwnMovesNothingAndSaysNothing(t *testing.T) {
	m := manyFileModel(t, 24)
	m = focusOnRow(t, m, 4)
	before := m
	if m.metaStart < 4+17 {
		t.Fatalf("the tree has %d rows, want room for 17 rows of walking from row 4", m.metaStart)
	}
	beforeView := m.View()

	for _, r := range "17" {
		updated, cmd := m.Update(runeKey(r))
		rm, ok := updated.(reviewModel)
		if !ok {
			t.Fatalf("a digit produced %T", updated)
		}
		if cmd != nil {
			t.Errorf("`%c` on its own started a command, want the screen to wait in silence", r)
		}
		m = rm
	}

	if m.cursor != before.cursor {
		t.Errorf("typing digits moved the cursor from %d to %d:\n%s", before.cursor, m.cursor, rowList(m))
	}
	if m.focus != before.focus {
		t.Errorf("typing digits moved the keys to %v", m.focus)
	}
	if m.metaCursor != before.metaCursor {
		t.Errorf("typing digits moved the changeset box from %d to %d", before.metaCursor, m.metaCursor)
	}
	if m.mode != before.mode || m.previewOffset != before.previewOffset {
		t.Errorf("typing digits changed the screen: mode %v, offset %d", m.mode, m.previewOffset)
	}
	if m.pick.list != nil || m.pick.col != before.pick.col || m.pick.cursor != before.pick.cursor {
		t.Errorf("typing digits disturbed the picker: col %d cursor %v list %v",
			m.pick.col, m.pick.cursor, m.pick.list)
	}
	if m.status != "" {
		t.Errorf("typing digits said %q on the status line", m.status)
	}
	if got := m.View(); got != beforeView {
		t.Errorf("the screen changed while the count was being typed:\n%s", got)
	}

	// Nothing was swallowed either: the key that follows walks the seventeen rows that were typed.
	m = pressKey(t, m, runeKey('j'))
	if m.cursor != 4+17 {
		t.Errorf("the j after the digits 17 went to row %d, want %d", m.cursor, 4+17)
	}
}

// A count belongs to exactly one key, and the key that spends it need not be a motion at all: a reviewer
// who types `5`, changes their mind, presses `space`, and only then reaches for `j`, expects the one row
// `j` has always meant. A count that outlived the key it was typed for would turn that `j` into a jump
// nobody asked for.
func TestAnUnrelatedKeySpendsTheCountInTheList(t *testing.T) {
	m := manyFileModel(t, 24)
	at := firstMarkableRow(t, m)
	if at+1 >= m.metaStart {
		t.Fatalf("row %d is the tree's last, so the j that proves the count died has nowhere to go", at)
	}
	m = focusOnRow(t, m, at)
	marked := countMarked(m.sess)

	m = pressAll(t, m, countKeys("5", tea.KeyMsg{Type: tea.KeySpace}))
	if got := countMarked(m.sess); got != marked+1 {
		t.Fatalf("`space` after a count marked %d files, want the one file under the cursor (was %d)",
			got, marked)
	}
	if m.cursor != at {
		t.Errorf("space after a count moved the cursor to %d, want it left on row %d", m.cursor, at)
	}

	m = pressKey(t, m, runeKey('j'))
	if m.cursor != at+1 {
		t.Errorf("the j after a spent count went to row %d, want one row to %d:\n%s",
			m.cursor, at+1, rowList(m))
	}
	assertFrameFits(t, m)
}

// `0` is a digit like any other, so a count of zero is a count that reads as one rather than as a motion
// of no distance -- and a digit held down stops at the cap rather than overflowing the accumulator, with
// every motion clamping at its ends so the reviewer lands on a row that exists rather than on a panic.
func TestLeadingZerosAndAHeldDigitLandOnRealRows(t *testing.T) {
	m := manyFileModel(t, 24)
	last := m.metaStart - 1
	if last < 12 {
		t.Fatalf("the tree has %d rows, want enough for a walk of seven and a jump to the end", last+1)
	}

	for _, tc := range []struct {
		name string
		from int
		keys []tea.KeyMsg
		want int
	}{
		{"0j moves one row", 0, countKeys("0", runeKey('j')), 1},
		{"00j moves one row", 0, countKeys("00", runeKey('j')), 1},
		{"007j moves seven", 0, countKeys("007", runeKey('j')), 7},
		{"a held digit walks to the last row", 0, countKeys("99999999", runeKey('j')), last},
		{"a held digit from the bottom stays on it", last, countKeys("99999999", runeKey('j')), last},
		{"a held digit back reaches the first row", last, countKeys("99999999", runeKey('k')), 0},
		{"a held ctrl-f stops at the last row", 0, countKeys("99999999", keyMsg(tea.KeyCtrlF)), last},
	} {
		here := focusOnRow(t, m, tc.from)
		here = pressAll(t, here, tc.keys)
		if here.cursor != tc.want {
			t.Errorf("%s: cursor = %d, want %d:\n%s", tc.name, here.cursor, tc.want, rowList(here))
		}
		_ = here.View()
		assertFrameFits(t, here)
	}
}

// A diff is read by counting lines too -- "twenty lines down and look" -- so the pane takes the same
// number in front of the same keys, and the number counts rows of the file rather than of any list. The
// list the reviewer left behind does not move while the pane is being scrolled.
func TestTheDiffPaneScrollsByTheCountedRows(t *testing.T) {
	m := focusPane(t, focusFixture(t, 60))
	total, body := m.previewRowsTouched()
	if total == 0 {
		t.Fatal("the pane has nothing to scroll")
	}
	if total-body < 2*body+1 {
		t.Fatalf("the fixture's diff is %d rows and the pane shows %d: two pages is not a thing to test here",
			total, body)
	}
	if body/2 < 2 {
		t.Fatalf("half the pane's window is %d rows, too small to tell a counted half from a plain one", body/2)
	}
	listAt, metaAt := m.cursor, m.metaCursor

	for _, tc := range []struct {
		name string
		keys []tea.KeyMsg
		want int
	}{
		{"10j scrolls ten lines", countKeys("10", runeKey('j')), 10},
		{"the down arrow counts too", countKeys("7", keyMsg(tea.KeyDown)), 7},
		{"5k gives five of them back",
			append(countKeys("10", runeKey('j')), countKeys("5", runeKey('k'))...), 5},
		{"2 ctrl-f is two windows of the pane", countKeys("2", keyMsg(tea.KeyCtrlF)), 2 * body},
		{"3 ctrl-b pages three windows back",
			append(countKeys("3", keyMsg(tea.KeyCtrlF)), countKeys("3", keyMsg(tea.KeyCtrlB))...), 0},
		{"5 ctrl-d is still half a window", countKeys("5", keyMsg(tea.KeyCtrlD)), body / 2},
		{"5 d is still half a window", countKeys("5", runeKey('d')), body / 2},
		{"5 u from ten lines down gives half back",
			append(countKeys("10", runeKey('j')), countKeys("5", runeKey('u'))...), 10 - body/2},
		{"9G is still the end of the file", countKeys("9", runeKey('G')), total - body},
		{"9gg is still the top",
			append(countKeys("10", runeKey('j')), append(countKeys("9", runeKey('g')), runeKey('g'))...), 0},
		{"a held ctrl-f stops at the end of the file", countKeys("999", keyMsg(tea.KeyCtrlF)), total - body},
	} {
		here := m
		here.previewOffset = 0
		for _, k := range tc.keys {
			here = paneKey(t, here, k)
		}
		if here.previewOffset != tc.want {
			t.Errorf("%s: offset = %d, want %d (the pane's window is %d rows of %d)",
				tc.name, here.previewOffset, tc.want, body, total)
		}
		if here.cursor != listAt || here.metaCursor != metaAt {
			t.Errorf("%s moved the list out from under the reviewer: %d/%d", tc.name, here.cursor, here.metaCursor)
		}
		if here.status != "" {
			t.Errorf("%s said %q; typing a number is not a reason to speak", tc.name, here.status)
		}
		assertFrameFits(t, here)
	}
}

// The overlay is the narrow terminal's only diff, so it is where the count matters most and where a
// clamped motion has to be exact: a frame that scrolled past the end of the file repaints, and the row it
// loses off the top is the bar that says how to leave.
func TestTheOverlayScrollsByTheCountedRows(t *testing.T) {
	m := openOverlay(t, overlayModel(t, 60))
	total, body := m.previewRowsTouched()
	if total-body < 2*body+1 {
		t.Fatalf("the fixture's diff is %d rows and the overlay shows %d: two pages is not a thing to test here",
			total, body)
	}

	for _, tc := range []struct {
		name string
		keys []tea.KeyMsg
		want int
	}{
		{"10j scrolls ten lines", countKeys("10", runeKey('j')), 10},
		{"the up arrow counts back",
			append(countKeys("8", keyMsg(tea.KeyDown)), countKeys("3", keyMsg(tea.KeyUp))...), 5},
		{"2 ctrl-f is two windows of the overlay", countKeys("2", keyMsg(tea.KeyCtrlF)), 2 * body},
		{"5 ctrl-d is still half a window", countKeys("5", keyMsg(tea.KeyCtrlD)), body / 2},
		{"5 u is still half a window back",
			append(countKeys("10", runeKey('j')), countKeys("5", runeKey('u'))...), 10 - body/2},
		{"9G is still the end of the file", countKeys("9", runeKey('G')), total - body},
		{"9gg is still the top",
			append(countKeys("9", runeKey('G')), append(countKeys("9", runeKey('g')), runeKey('g'))...), 0},
		{"a held digit scrolls to the end and stops", countKeys("99999", runeKey('j')), total - body},
	} {
		here := m
		here.previewOffset = 0
		for _, k := range tc.keys {
			here = pressOverlay(t, here, k)
		}
		if here.previewOffset != tc.want {
			t.Errorf("%s: offset = %d, want %d (the overlay's window is %d rows of %d)",
				tc.name, here.previewOffset, tc.want, body, total)
		}
	}

	// What the reviewer reads at the end of a counted scroll, not just the number behind it.
	m.previewOffset = 0
	for _, k := range countKeys("9999", runeKey('G')) {
		m = pressOverlay(t, m, k)
	}
	view := m.View()
	if !strings.Contains(view, "+line 60") {
		t.Errorf("9999G did not bring the last line of the diff on screen:\n%s", view)
	}
	assertFrameFits(t, m)
}

// The two columns of the picker are the timeline a reviewer chooses a span out of, and the checkpoint they
// want is as likely as not ten rows down. The columns read the same count the rest of the screen reads --
// and the one key that does not read it, `tab`, spends it all the same, so the number cannot walk with
// the keys into the other column.
func TestThePickerColumnsWalkByTheCount(t *testing.T) {
	m, f := pickerFixture(t, 1)
	for i := range 12 {
		f.Commit(fmt.Sprintf("more %d", i), gittest.WithFile(fmt.Sprintf("m%d.go", i), "package main\n"))
	}
	m = open(t, m)
	items := m.endpointsFor(m.pick.col == 0)
	if len(items) < 13 {
		t.Fatalf("the column has %d rows, want enough for a counted walk of 10 and a page beyond it", len(items))
	}
	// The picker opens with each column's cursor on its pending end; `k` is how a reviewer gets to the
	// top from wherever that was.
	for range len(items) + 1 {
		if m.pick.cursor[m.pick.col] == 0 {
			break
		}
		m = pressKey(t, m, runeKey('k'))
	}
	if got := m.pick.cursor[m.pick.col]; got != 0 {
		t.Fatalf("the column's cursor would not come to the top (at %d of %d)", got, len(items))
	}

	// A digit on its own moves nothing here either: the columns are where a wrong span is chosen, and a
	// cursor that jumped while the number was being typed is a choice nobody made.
	before := m
	updated, cmd := m.Update(runeKey('1'))
	quiet := updated.(reviewModel)
	if cmd != nil {
		t.Errorf("a digit started a command in the picker, want the columns to wait")
	}
	if quiet.pick.cursor[quiet.pick.col] != 0 || quiet.pick.col != before.pick.col ||
		quiet.pick.list != before.pick.list || quiet.pick.err != before.pick.err {
		t.Errorf("typing a digit disturbed the picker: cursor %v col %d list %v err %q",
			quiet.pick.cursor, quiet.pick.col, quiet.pick.list, quiet.pick.err)
	}

	m = pressAll(t, m, countKeys("10", runeKey('j')))
	if got := m.pick.cursor[m.pick.col]; got != 10 {
		t.Errorf("10j moved the column to %d, want 10:\n%v", got, columnLabels(items))
	}
	m = pressAll(t, m, countKeys("4", keyMsg(tea.KeyUp)))
	if got := m.pick.cursor[m.pick.col]; got != 6 {
		t.Errorf("4k moved the column to %d, want 6:\n%v", got, columnLabels(items))
	}
	m = pressAll(t, m, countKeys("3", keyMsg(tea.KeyDown)))
	if got := m.pick.cursor[m.pick.col]; got != 9 {
		t.Errorf("3 down moved the column to %d, want 9:\n%v", got, columnLabels(items))
	}

	// The count dies with `tab`, which is the key that moves the keys to the other column.
	from := m.pick.col
	atOther := m.pick.cursor[1-from]
	m = pressAll(t, m, countKeys("5", tabKey()))
	if m.pick.col == from {
		t.Fatalf("tab did not move to the other column")
	}
	m = pressKey(t, m, runeKey('j'))
	if got := m.pick.cursor[m.pick.col]; got != atOther+1 {
		t.Errorf("`5 tab j` moved the other column to %d, want one row from %d: the count belonged to the "+
			"column it was typed in", got, atOther)
	}

	// A counted walk past the end of the column stops at its last row, which is where the next key finds
	// the reviewer standing.
	m = pressAll(t, m, countKeys("9999", runeKey('j')))
	m = pressKey(t, m, tabKey())
	m = pressKey(t, m, tabKey())
	if got := m.pick.cursor[m.pick.col]; got != len(items)-1 {
		t.Errorf("9999j left the column's cursor at %d, want the last row %d:\n%v",
			got, len(items)-1, columnLabels(items))
	}
	assertFrameFits(t, m)
}

// The drill-in is a list of commits read with the keys the rest of the screen uses, so it counts them the
// same way: `10j` is ten commits and `2 f` two screens of them. The half-page pairs keep their half, and
// `gg` and `G` keep the two ends, whatever was typed in front of them.
func TestTheDrillCountsItsRowsAndPages(t *testing.T) {
	m, f := pickerFixture(t, 1)
	for i := range 20 {
		f.Commit(fmt.Sprintf("work %02d", i), gittest.WithFile(fmt.Sprintf("w%d.go", i), "package main\n"))
	}
	m.height = 12
	m = drill(t, m, "commit")
	rows := m.listRows()
	total := len(m.pick.list.visible())
	if total < 3*rows+2 {
		t.Fatalf("the drill lists %d rows and a page is %d: counted pages are not a thing to test here",
			total, rows)
	}

	for _, tc := range []struct {
		name string
		keys []tea.KeyMsg
		want int
	}{
		{"10j walks ten rows", countKeys("10", runeKey('j')), 10},
		{"the down arrow counts too", countKeys("6", keyMsg(tea.KeyDown)), 6},
		{"5k walks five back",
			append(countKeys("10", runeKey('j')), countKeys("5", runeKey('k'))...), 5},
		{"2 f is two pages", countKeys("2", runeKey('f')), 2 * rows},
		{"2 ctrl-f is two pages", countKeys("2", keyMsg(tea.KeyCtrlF)), 2 * rows},
		{"3 b pages three back to the top",
			append(countKeys("3", runeKey('f')), countKeys("3", runeKey('b'))...), 0},
		{"5 d is still half a page", countKeys("5", runeKey('d')), rows / 2},
		{"5 ctrl-d is still half a page", countKeys("5", keyMsg(tea.KeyCtrlD)), rows / 2},
		{"9G is still the bottom", countKeys("9", runeKey('G')), total - 1},
		{"9gg is still the top",
			append(countKeys("9", runeKey('G')), append(countKeys("9", runeKey('g')), runeKey('g'))...), 0},
	} {
		here := m
		here.pick.list.sel = 0
		here = pressAll(t, here, tc.keys)
		if got := here.pick.list.sel; got != tc.want {
			t.Errorf("%s: selection = %d, want %d (a page is %d rows of %d)",
				tc.name, got, tc.want, rows, total)
		}
	}

	// A count that outlived the drill's own keys would be paid for by the next innocent `j`.
	m.pick.list.sel = 0
	m = pressAll(t, m, countKeys("5", runeKey('/')))
	m = pressKey(t, m, escKey())
	m = pressKey(t, m, runeKey('j'))
	if got := m.pick.list.sel; got != 1 {
		t.Errorf("the j after a count spent by / went to %d, want one row", got)
	}
	assertFrameFits(t, m)
}

// The keys that move the keys -- tab, esc, z, a, V -- leave one region and take another's, and a number
// typed for the first has no meaning in the second. A count that crossed with the reviewer would move a
// region they were not counting in, which is the one thing a count must never do.
func TestACountDoesNotFollowTheReviewerOntoAnotherScreen(t *testing.T) {
	// tab, and the way back with esc: the pane is the stop the focus ring walks to from the list.
	m := focusFixture(t, 40)
	listAt := m.cursor
	m = pressAllPane(t, m, countKeys("5", tabKey()))
	if !m.previewHasFocus() {
		t.Fatalf("tab did not move the keys into the pane (focus %v, pane %d wide)", m.focus, m.paneWidth())
	}
	m = paneKey(t, m, runeKey('j'))
	if m.previewOffset != 1 {
		t.Errorf("`5 tab j` scrolled the diff %d rows, want one: the count was typed for the list", m.previewOffset)
	}
	if m.cursor != listAt {
		t.Errorf("the count crossed into the pane and moved the list's cursor from %d to %d", listAt, m.cursor)
	}
	m = paneKey(t, m, escKey())
	if m.previewHasFocus() {
		t.Fatalf("esc left the keys with the diff")
	}
	m = pressKey(t, m, runeKey('j'))
	if m.cursor != listAt+1 {
		t.Errorf("the j after coming back out of the pane went to %d, want one row to %d", m.cursor, listAt+1)
	}

	// z takes the whole screen, and the number typed for the list does not travel with the keys.
	full := askPreview(t, focusFixture(t, 40))
	full = pressAllPane(t, full, countKeys("5", runeKey('z')))
	if full.mode != modePreview {
		t.Fatalf("`z` did not take the screen (mode %v, status %q)", full.mode, full.status)
	}
	full = paneKey(t, full, runeKey('j'))
	if full.previewOffset != 1 {
		t.Errorf("`5 z j` scrolled the overlay %d rows, want one", full.previewOffset)
	}

	// a jumps into the changeset box and takes the keys along; the j that follows is a row, not five.
	jump := focusOnRow(t, manyFileModel(t, 24), 0)
	jump = pressAll(t, jump, countKeys("5", runeKey('a')))
	if !jump.metaHasFocus() {
		t.Fatalf("`a` did not take the keys into the changeset box (focus %v, status %q)", jump.focus, jump.status)
	}
	about := boxIndexOf(t, jump, rowAbout)
	if jump.metaCursor != about {
		t.Fatalf("`a` landed on row %d, want the ABOUT.md row %d", jump.metaCursor, about)
	}
	jump = pressKey(t, jump, runeKey('j'))
	if want := min(about+1, len(jump.rows)-1); jump.metaCursor != want {
		t.Errorf("the j after a count spent by a went to %d in the box, want one row to %d", jump.metaCursor, want)
	}

	// V opens the picker, whose columns then answer the row keys with one row each.
	pick, _ := pickerFixture(t, 1)
	pick = pressAll(t, pick, countKeys("5", runeKey('V')))
	if pick.mode != modeSpan {
		t.Fatalf("V did not open the picker (mode %v)", pick.mode)
	}
	at := pick.pick.cursor[pick.pick.col]
	pick = pressKey(t, pick, runeKey('j'))
	if got := pick.pick.cursor[pick.pick.col]; got != at+1 {
		t.Errorf("`5 V j` moved the column to %d, want one row from %d", got, at)
	}
	assertFrameFits(t, m)
}

// `/` opens a field to type into, and from there every digit is part of the term: the error code a
// reviewer is chasing is made of digits, and the count they may have been typing when they reached for the
// key is over by the time the field is open.
func TestDigitsTypedIntoTheSearchFieldStayText(t *testing.T) {
	m := focusPane(t, focusFixture(t, 40))
	for _, r := range "79" {
		m = paneKey(t, m, runeKey(r))
	}
	if m.searching {
		t.Fatal("a digit opened the search field")
	}
	m = paneKey(t, m, runeKey('/'))
	if !m.searching {
		t.Fatalf("`/` did not open the field")
	}
	if m.searchInput != "" {
		t.Fatalf("the field opened holding %q, want the digits typed before `/` left outside it", m.searchInput)
	}

	at := m.previewOffset
	m = typeText(t, m, "line 007")
	if m.searchInput != "line 007" {
		t.Errorf("the field holds %q, want the term typed at it", m.searchInput)
	}
	if m.previewOffset != at {
		t.Errorf("typing digits in the field scrolled the diff from %d to %d", at, m.previewOffset)
	}
	if !m.searching {
		t.Fatal("typing in the field closed it")
	}

	m = paneKey(t, m, escKey())
	if m.searching {
		t.Fatal("esc did not hand the keys back")
	}
	m = paneKey(t, m, runeKey('j'))
	if m.previewOffset != at+1 {
		t.Errorf("the j after typing digits in the field scrolled %d rows, want one: those digits were text, "+
			"and the count typed before `/` was spent by `/` itself", m.previewOffset-at)
	}
	assertFrameFits(t, m)
}

// The drill's filter is typed at, and the subjects it is fed are full of numbers -- "fix 500" is a commit
// message a reviewer looks for. A digit that armed a count while the reviewer was writing a term would
// move the list they were typing at, which is the opposite of what typing does.
func TestDigitsTypedIntoTheDrillFilterStayText(t *testing.T) {
	m, f := pickerFixture(t, 1)
	for i, subject := range []string{"fix 500 the parser", "fix 500 the writer", "fix 500 the socket"} {
		f.Commit(subject, gittest.WithFile(fmt.Sprintf("fix-%d.go", i), fmt.Sprintf("package main\n\n// %d\n", i)))
	}
	m = drill(t, m, "commit")

	// A count in the navigation mode, then the key that leaves it for the filter.
	m = pressKey(t, m, runeKey('5'))
	m = pressKey(t, m, runeKey('/'))
	if !m.pick.filtering {
		t.Fatalf("`/` did not start a filter")
	}
	m = typeText(t, m, "500")
	if got := m.pick.list.filter; got != "500" {
		t.Errorf("the filter holds %q, want the digits typed at it", got)
	}

	m = pressKey(t, m, escKey())
	if m.pick.filtering {
		t.Fatal("esc did not hand the keys back to the list")
	}
	if got := len(m.pick.list.visible()); got < 3 {
		t.Fatalf("the term 500 matched %d rows, want the three commits named after it", got)
	}
	if got := m.pick.list.sel; got != 0 {
		t.Fatalf("the list opened on %d, want the top of what matches", got)
	}
	m = pressKey(t, m, runeKey('j'))
	if got := m.pick.list.sel; got != 1 {
		t.Errorf("the j after typing digits in the filter went to %d, want one row: the digits were text, and "+
			"the count typed before `/` was spent by `/` itself", got)
	}
	assertFrameFits(t, m)
}

// A turn of the wheel is the reviewer doing something to the screen, so a count typed before it is over
// by the time the hand comes back to the keys: a notch rolled between `10` and `j` is a reviewer who
// changed their mind about the distance, not one who wants ten rows after a scroll. The notch keeps its
// own fixed three rows -- the wheel is not a motion key and takes no count -- and a notch that had
// nothing to scroll spends the count just the same, because the reviewer still moved the pointer.
func TestTheWheelSpendsACountWithoutMultiplyingIt(t *testing.T) {
	m := focusPane(t, askPreview(t, tall(previewModel(t), 60)))
	total, body := m.previewRowsTouched()
	if total-body < wheelStep+3 {
		t.Fatalf("the fixture's diff is %d rows and the pane shows %d: a notch and a row is not a thing to test here",
			total, body)
	}
	at := m.previewOffset

	counted := pressKey(t, m, runeKey('5'))
	counted = roll(t, counted, tea.MouseButtonWheelDown)
	if counted.previewOffset != at+wheelStep {
		t.Errorf("one notch after a count of 5 scrolled %d rows, want the notch's own %d: the wheel is not a "+
			"motion key", counted.previewOffset-at, wheelStep)
	}
	counted = pressKey(t, counted, runeKey('j'))
	if want := at + wheelStep + 1; counted.previewOffset != want {
		t.Errorf("the j after a count spent by the wheel went to %d, want one row past the notch at %d",
			counted.previewOffset, want)
	}
	assertFrameFits(t, counted)

	// ... and the same where the notch has nothing to move: the pointer was still moved, and the
	// number is still the reviewer's own, so the next `j` is one row rather than a jump.
	off := focusOnRow(t, manyFileModel(t, 24), 4)
	off = pressKey(t, off, runeKey('7'))
	updated, _ := off.Update(wheelMsg(tea.MouseButtonWheelDown, tea.MouseActionPress))
	off = updated.(reviewModel)
	off = pressKey(t, off, runeKey('j'))
	if off.cursor != 5 {
		t.Errorf("the j after a count and a wheel event with no diff on screen went to row %d, want one row to 5",
			off.cursor)
	}
}

// A key nobody can see is a key nobody uses, and a count is the least visible thing on this screen: each
// region's bar has to say that a number can stand in front of the row keys, in the unit that region moves
// in -- rows for a list, lines for a file. The drill's filter bar names none of it, because while the
// reviewer is typing, a digit is a letter like any other.
func TestTheShortcutBarsNameTheCount(t *testing.T) {
	m := manyFileModel(t, 12)
	for _, tc := range []struct {
		name string
		bar  string
		want string
	}{
		{"the tree's bar", m.helpTextFor(focusFiles), "10j 10 rows"},
		{"the changeset box's bar", m.helpTextFor(focusMeta), "10j 10 rows"},
		{"the diff pane's bar", m.helpTextFor(focusPreview), "10j 10 lines"},
		{"the picker's columns", helpSpan(false, false), "10j 10 rows"},
		{"the drill's navigation", helpSpan(true, false), "10j 10 lines"},
	} {
		if !strings.Contains(tc.bar, tc.want) {
			t.Errorf("%s does not name the count as %q: %q", tc.name, tc.want, tc.bar)
		}
	}

	overlay := openOverlay(t, overlayModel(t, 40))
	if bar := overlay.overlayHelp(); !strings.Contains(bar, "10j 10 lines") {
		t.Errorf("the overlay's bar does not name the count as %q: %q", "10j 10 lines", bar)
	}

	if bar := helpSpan(true, true); strings.Contains(bar, "10j") {
		t.Errorf("the drill's filter bar offers a count it cannot read: %q", bar)
	}
	assertFrameFits(t, m)
}
