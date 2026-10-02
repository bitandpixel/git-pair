package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The row area is shared by two regions, and the split is the feature: a changeset with forty threads
// is a reason to scroll the changeset box, not a reason to hide the file tree behind it. These pin the
// arithmetic of the split, the keys that move between the regions and within one, what each region's
// shortcut bar promises, and the span row -- the one row in the box a reviewer changes.

// withThreads grows the box to n threads, which is the case the split exists for. The rows go inside
// the box's half of the list, because the list is one slice holding two regions and a file row
// appended past the split would be a row of the tree.
func withThreads(t *testing.T, m reviewModel, n int) reviewModel {
	t.Helper()
	extra := make([]row, 0, n)
	for i := range n {
		extra = append(extra, row{kind: rowThread, name: fmt.Sprintf("thread-%02d.md", i),
			path: fmt.Sprintf("thread-%02d.md", i), file: -1})
	}
	rows := make([]row, 0, len(m.rows)+n)
	rows = append(rows, m.rows[:m.metaStart]...)
	rows = append(rows, extra...)
	rows = append(rows, m.rows[m.metaStart:]...)
	m.rows = rows
	m.clamp()
	if len(m.metaRows()) < n {
		t.Fatalf("the box holds %d rows after adding %d threads", len(m.metaRows()), n)
	}
	return m
}

// manyFileModel is a session whose span changed n files at the top of the tree, which is the shape the
// tree's scroll counts are for: more rows than the window has, and no directory rows among them. The
// files are real because the list is rebuilt from the session on every key, so a model with rows added
// by hand loses them before the key arrives.
func manyFileModel(t *testing.T, n int) reviewModel {
	t.Helper()
	files := make(map[string]string, n)
	for i := range n {
		name := fmt.Sprintf("file-%02d.go", i)
		files[name] = "package main\n\n// " + name + "\n"
	}
	m := newFileListModelWith(t, files)
	m.height = 24
	m.clamp()
	return m
}

// The box asks for what it needs and is capped at a third of the area, so the tree keeps two thirds of
// it whatever the changeset says. The floor is there so a short box is still a box.
func TestTheChangesetBoxIsCappedAndTheTreeKeepsTheRest(t *testing.T) {
	for _, height := range []int{16, 24, 40} {
		m := navModel(t)
		m.height = height
		m = withThreads(t, m, 40)

		meta, files := m.regionHeights()
		area := m.rowArea()
		if meta+files != area {
			t.Errorf("at height %d the regions take %d+%d rows, want the area's %d", height, meta, files, area)
		}
		// The share is a third of the area, or the floor that keeps the box a box when the area is
		// too small for a third of it to hold a border and a row.
		if cap := max(metaMinRows, area/metaShare); meta > cap {
			t.Errorf("at height %d the box takes %d rows, more than its share of %d (area %d)", height, meta, cap, area)
		}
		if files < m.filesWindow() || files <= 0 {
			t.Errorf("at height %d the tree got %d rows", height, files)
		}
		if got := len(m.visibleFiles()); got != min(len(m.fileRows()), files) {
			t.Errorf("at height %d the tree shows %d rows, want %d", height, got, min(len(m.fileRows()), files))
		}
	}
}

// The frame is written one line per row, so a box that grew past its share would push the shortcut bar
// off the bottom of the terminal -- the failure a reviewer would feel as keys that do nothing.
func TestALongChangesetBoxStillFitsTheTerminal(t *testing.T) {
	m := navModel(t)
	m = withThreads(t, m, 40)

	rows := viewRows(m.View())
	if len(rows) > m.height {
		t.Errorf("the frame is %d rows in a %d-row window", len(rows), m.height)
	}
	if !strings.Contains(strings.Join(rows, "\n"), "q quit") {
		t.Errorf("the shortcut bar is not on the screen:\n%s", strings.Join(rows, "\n"))
	}
	// The box hides what it cannot show and says how much is hidden, inside its own borders so the
	// counts cannot change the column's height. The scroll is an index into the list, so the count of
	// what the window hides starts from where the box starts.
	if above, below := m.metaHidden(); above > 0 || below > 0 {
		view := strings.Join(rows, "\n")
		if above > 0 && !strings.Contains(view, "\u2191") {
			t.Errorf("the box hides %d rows above its window without saying so:\n%s", above, view)
		}
		if below > 0 && !strings.Contains(view, "\u2193") {
			t.Errorf("the box hides %d rows below its window without saying so:\n%s", below, view)
		}
	}
}

// ctrl-d used to quit, which is why paging had to be ctrl-f and ctrl-b and nothing else. It pages now,
// in whichever half of the list column has the keys, and the two keys that quit are ctrl-c and q.
func TestCtrlDPagesTheFocusedRegionAndDoesNotQuit(t *testing.T) {
	m := withThreads(t, navModel(t), 12)
	m = focusOnRow(t, m, m.metaStart)
	before := m.metaCursor

	m = paneKey(t, m, keyMsg(tea.KeyCtrlD))
	if m.quitting {
		t.Fatal("ctrl-d quit the session")
	}
	if m.metaCursor <= before {
		t.Errorf("ctrl-d in the box left it on %d, want it further down from %d", m.metaCursor, before)
	}
	at := m.metaCursor
	m = paneKey(t, m, keyMsg(tea.KeyCtrlU))
	if m.metaCursor >= at {
		t.Errorf("ctrl-u in the box left it on %d, want it back up from %d", m.metaCursor, at)
	}

	// And the same two keys page the tree, half a window at a time, without quitting.
	m = pressRune(pressRune(m, 'f'), 'G')
	tree, window := m.cursor, m.activeWindow()
	m = paneKey(t, m, keyMsg(tea.KeyCtrlU))
	if m.cursor >= tree {
		t.Errorf("ctrl-u in the tree left it on %d, want it up from %d (a window is %d rows)", m.cursor, tree, window)
	}
	m = paneKey(t, m, keyMsg(tea.KeyCtrlC))
	if !m.quitting {
		t.Error("ctrl-c no longer quits, and it is the key that always has")
	}
}

// The frame has to be the same height whatever holds the keys: the layout budgets for the longest bar,
// so moving the keys cannot move the rule under the list.
func TestMovingTheKeysDoesNotMoveTheLayout(t *testing.T) {
	m := navModel(t)
	before := len(viewRows(m.View()))
	beforeRows, _ := m.regionHeights()

	// The box takes the keys from the tree's top row: walking up out of the tree is how the column's
	// other half is reached, and the screen it arrives on must be the one that was left. The band is
	// budgeted for the longest bar the session can show, so the frame may not change height on the way.
	after := press(focusOnRow(t, m, 0), tea.KeyUp)
	got := viewRows(after.View())
	if len(got) != before {
		t.Errorf("the frame was %d rows and became %d when the keys moved to the box", before, len(got))
	}
	if meta, _ := after.regionHeights(); meta != beforeRows {
		t.Errorf("the box had %d rows and got %d when it took the keys", beforeRows, meta)
	}
	if !after.metaHasFocus() {
		t.Fatalf("the keys did not cross into the box: %v", after.focus)
	}
	if !strings.Contains(strings.Join(got, "\n"), "space span") {
		t.Errorf("the box's bar does not name its own keys:\n%s", strings.Join(got, "\n"))
	}
}

// Each half of the list column has its own bar, naming what it reads. A key that belongs to the other
// half is not hidden by an asterisk or a mode -- it is simply not offered, which is the same rule the
// read-only bar uses for a span the reviewer cannot act on. `m` named the box while the box was a stop on
// the ring; with the ring down to two stops there is nothing left for it to name.
func TestEachRegionHasItsOwnShortcutBar(t *testing.T) {
	m := navModel(t)
	files := m.helpTextFor(focusFiles)
	box := m.helpTextFor(focusMeta)

	for _, want := range []string{"h/l fold", "space reviewed", "s submit", "a about", "t threads", "f files", "tab preview"} {
		if !strings.Contains(files, want) {
			t.Errorf("the tree's bar does not mention %q: %q", want, files)
		}
	}
	for _, want := range []string{"space span", "T new thread", "a about", "t threads", "f files", "tab preview"} {
		if !strings.Contains(box, want) {
			t.Errorf("the box's bar does not mention %q: %q", want, box)
		}
	}
	for _, gone := range []string{"h/l fold", "s submit", "space reviewed"} {
		if strings.Contains(box, gone) {
			t.Errorf("the box's bar offers %q, which it does not read: %q", gone, box)
		}
	}
	for _, gone := range []string{"m changeset", "tab focus", "tab cycles"} {
		if strings.Contains(files, gone) || strings.Contains(box, gone) {
			t.Errorf("a bar still names %q, which is the ring the session no longer has: %q / %q", gone, files, box)
		}
	}
	if files == box {
		t.Error("the two regions offer the same bar, so the bar says nothing about the keys")
	}
}

// The borders are the box's focus light, and they are glyphs rather than styling: a terminal that
// renders no bold still has to show which region the keys are in.
func TestTheBoxBorderSaysWhenItHoldsTheKeys(t *testing.T) {
	m := navModel(t)
	idle := ansiCodes.ReplaceAllString(m.View(), "")
	// The box by name rather than by tab: the ring here walks through the diff first, and what is under
	// test is the border, not the walk.
	m.focusOn(focusMeta)
	focused := ansiCodes.ReplaceAllString(m.View(), "")

	if !strings.Contains(idle, "╭") || strings.Contains(idle, "╔") {
		t.Errorf("the box without the keys does not draw a single-rule frame:\n%s", idle)
	}
	if !strings.Contains(focused, "╔") || strings.Contains(focused, "╭") {
		t.Errorf("the box with the keys does not draw a double-rule frame:\n%s", focused)
	}
}

// The span row is the box's one control: what the review is measured against is worth choosing, and
// the picker changes nothing until its own Enter -- which is why it is reachable over history too.
func TestTheSpanRowOpensThePicker(t *testing.T) {
	m := navModel(t)
	m = focusOnRow(t, m, boxIndexOf(t, m, rowSpan))

	m = press(m, tea.KeyEnter)
	if m.mode != modeSpan {
		t.Fatalf("Enter on the span row gave mode %v, want the span picker", m.mode)
	}
}

// Space marks a file and chooses a span, depending on where it lands. The read-only gate is what makes
// the difference worth spelling out to it: on a file it is a change to the review, and on the span row
// it is the first keystroke of a read.
func TestSpaceOnTheSpanRowOpensThePickerEvenOverHistory(t *testing.T) {
	m, _ := readonlyModel(t, historySel())
	m = focusOnRow(t, m, boxIndexOf(t, m, rowSpan))

	m = press(m, tea.KeySpace)
	if m.mode != modeSpan {
		t.Fatalf("space on the span row over history gave mode %v, want the span picker", m.mode)
	}

	// The same key on a file row is still the change the read-only screen will not make.
	back := press(m, tea.KeyEsc)
	if back.mode != modeFiles {
		t.Fatalf("esc left the picker in mode %d", back.mode)
	}
	back = focusOnRow(t, back, indexOfNameBySuffix(t, back, ".go"))
	back = press(back, tea.KeySpace)
	if !strings.Contains(back.status, "read-only") {
		t.Errorf("space on a file over history said %q, want the read-only refusal", back.status)
	}
}

// The span row says it goes somewhere, which is the difference between a control and a caption; and
// the box's own base line, which goes nowhere, stays a line of the frame rather than a row.
func TestTheSpanRowIsARowAndTheBaseLineIsNot(t *testing.T) {
	m := navModel(t)
	spanRow := m.rowText(m.rows[boxIndexOf(t, m, rowSpan)])
	if !strings.Contains(ansiCodes.ReplaceAllString(spanRow, ""), "▸") {
		t.Errorf("the span row does not say it opens: %q", spanRow)
	}
	if strings.Contains(m.baseLine(), "▸") {
		t.Errorf("the base line offers an action it does not have: %q", m.baseLine())
	}
	for _, r := range m.fileRows() {
		if r.kind == rowSpan || r.kind == rowAbout {
			t.Errorf("the changeset row %q is inside the file tree", r.name)
		}
	}
	if m.metaStart == 0 || m.metaStart == len(m.rows) {
		t.Errorf("the split between the regions is %d of %d rows", m.metaStart, len(m.rows))
	}
}

// The base row names what the span is measured against. Once a landed parent moves that onto the
// destination, the base row stops saying why, and the line under it does. These pin the line's own shape:
// it is a line of the frame like the base rather than a row a reviewer presses, it costs a row to no other
// changeset, and its value starts in the column the box already aligns on.
func TestTheParentLineSaysWhatTheBaseRowCannot(t *testing.T) {
	m := navModel(t)
	plain := func(s string) string { return ansiCodes.ReplaceAllString(s, "") }

	if got := plain(m.parentLine()); got != "" {
		t.Errorf("a changeset with no parent draws %q, want the line to cost nothing", got)
	}
	_, section := m.window()
	before := len(m.boxLines(section))

	m.sess.parentLanded = "alpha"
	got := plain(m.parentLine())
	if !strings.HasPrefix(got, "parent ") || !strings.Contains(got, "alpha") || !strings.Contains(got, "landed") {
		t.Errorf("parent line = %q, want the label, the parent's id and the fact", got)
	}
	if strings.Contains(got, "\u25b8") {
		t.Errorf("the parent line offers an action it does not have: %q", got)
	}
	if after := len(m.boxLines(section)); after != before+1 {
		t.Errorf("the box drew %d lines where a parent landed and %d where none did: the line is the shape's own cost",
			after, before)
	}
	// The label column is what makes the box read as one answer rather than as notes.
	if base := plain(m.baseLine()); strings.Index(got, "alpha") != strings.Index(base, "main") {
		t.Errorf("the parent value and the base value do not start in one column: %q / %q", base, got)
	}
}

// The ring does not end at the diff: a reviewer who arrived with `tab` leaves with it, and `f` names the
// tree from inside the pane. Over the overlay the same keys take the diff screen down on their way, which
// is what keeps the ring walkable where there is no pane (TestTabWalksTheDiffWhereThereIsNoPane).
func TestTabMovesTheKeysOutOfThePane(t *testing.T) {
	// The pane hands the keys back to the half of the column that gave them up -- the box, here, because
	// that is where the reviewer was standing when they pressed `p`.
	toBox := press(focusPane(t, boxOn(t, focusFixture(t, 40))), tea.KeyTab)
	if !toBox.metaHasFocus() {
		t.Errorf("tab from the pane left the keys with %v, want the box they came from", toBox.focus)
	}
	if !strings.Contains(toBox.helpText(), "space span") {
		t.Errorf("tab from the pane did not bring the box's bar with it: %q", toBox.helpText())
	}

	back := press(focusPane(t, focusFixture(t, 40)), tea.KeyShiftTab)
	if back.focus != focusFiles {
		t.Errorf("shift-tab from the pane left the keys with %v, want the file tree", back.focus)
	}
	if got := press(focusPane(t, focusFixture(t, 40)), tea.KeyTab); got.focus != focusFiles {
		t.Errorf("tab from a pane opened from the tree left the keys with %v, want the tree", got.focus)
	}
	// `f` is the absolute version of the same move: it names the tree whatever half the keys were in.
	if got := pressRune(focusPane(t, boxOn(t, focusFixture(t, 40))), 'f'); got.focus != focusFiles {
		t.Errorf("f from the pane left the keys with %v, want the file tree", got.focus)
	}
	if !strings.Contains(focusFixture(t, 40).helpTextFor(focusPreview), "tab list") {
		t.Error("the pane's bar does not name the key that leaves it")
	}

	overlay := openOverlay(t, overlayModel(t, 40))
	if got := press(overlay, tea.KeyTab); got.mode != modeFiles || got.focus != focusFiles {
		t.Errorf("tab over the overlay left mode %v with %v, want the list drawn again and the tree holding the keys",
			got.mode, got.focus)
	}
	if got := pressRune(overlay, 'f'); got.mode != modeFiles || got.focus != focusFiles {
		t.Errorf("f over the overlay left mode %v with %v, want the list drawn again and the tree holding the keys",
			got.mode, got.focus)
	}
}

// On a narrow terminal the diff has no column to focus, so the other stop on the ring is the overlay
// rather than nothing: `tab` opens it from either half of the list column and closes it back onto the
// half that opened it. The reviewer with a small terminal reaches the diff with the same gesture everyone
// else has, instead of remembering that one of the regions is a different key.
func TestTabWalksTheDiffWhereThereIsNoPane(t *testing.T) {
	m := overlayModel(t, 40)
	if m.paneWidth() != 0 {
		t.Fatalf("the fixture has a pane of %d columns; the ring under test is the narrow one", m.paneWidth())
	}

	toDiff := pressOverlay(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if toDiff.mode != modePreview || toDiff.focus != focusPreview {
		t.Fatalf("tab from the tree gave mode %v with %v, want the overlay holding the keys", toDiff.mode, toDiff.focus)
	}
	if !strings.Contains(ansiCodes.ReplaceAllString(toDiff.View(), ""), "+line 1") {
		t.Errorf("tab opened the overlay without the diff in it:\n%s", toDiff.View())
	}

	// The keys came from the tree, so `esc` and `tab` both return them there.
	if home := pressOverlay(t, toDiff, tea.KeyMsg{Type: tea.KeyEsc}); home.mode != modeFiles || home.focus != focusFiles {
		t.Errorf("esc from a diff opened by tab left mode %v with %v, want the tree it came from",
			home.mode, home.focus)
	}
	if home := pressOverlay(t, toDiff, tea.KeyMsg{Type: tea.KeyTab}); home.mode != modeFiles || home.focus != focusFiles {
		t.Errorf("tab from the diff left mode %v with %v, want the tree it came from", home.mode, home.focus)
	}

	// From the box it is the same stop -- and the box is what the keys come back to, on the row they left.
	inBox := focusOnRow(t, m, boxIndexOf(t, m, rowThread))
	at := inBox.metaCursor
	fromBox := pressOverlay(t, inBox, tea.KeyMsg{Type: tea.KeyTab})
	if fromBox.mode != modePreview {
		t.Fatalf("tab from the box gave mode %v, want the overlay", fromBox.mode)
	}
	back := pressOverlay(t, fromBox, tea.KeyMsg{Type: tea.KeyTab})
	if back.mode != modeFiles || !back.metaHasFocus() || back.metaCursor != at {
		t.Errorf("tab back from the diff gave %v on %d, want the box on the thread at %d",
			back.focus, back.metaCursor, at)
	}

	// Shift-tab is the same toggle in the other direction, from either half of the column.
	if out := pressOverlay(t, m, tea.KeyMsg{Type: tea.KeyShiftTab}); out.mode != modePreview {
		t.Errorf("shift-tab from the tree gave mode %v, want the overlay", out.mode)
	}
	if out := pressOverlay(t, fromBox, tea.KeyMsg{Type: tea.KeyShiftTab}); out.mode != modeFiles || !out.metaHasFocus() {
		t.Errorf("shift-tab from the diff gave mode %v with %v, want the box", out.mode, out.focus)
	}
}

// A terminal too small for even the overlay has no diff to walk to, and the ring says so: the target that
// cannot be drawn is the one whose keys would go nowhere. With the diff gone there is one stop left, and
// Tab has nowhere to take the keys.
func TestTabSkipsTheDiffWhenEvenTheOverlayDoesNotFit(t *testing.T) {
	m := overlayModel(t, 40)
	m.width, m.height = 30, 8
	if m.previewIsOverlayOnly() {
		t.Fatalf("at %dx%d the overlay still fits; this is the case where nothing does", m.width, m.height)
	}

	if ring := m.focusRing(); len(ring) != 1 || ring[0] != m.listFocus() {
		t.Fatalf("the ring is %v, want the list column alone", ring)
	}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyTab}, {Type: tea.KeyShiftTab}} {
		still := pressOverlay(t, m, key)
		if still.mode != modeFiles || still.focus != focusFiles {
			t.Errorf("tab with no diff to walk to left mode %v with %v, want the list still holding the keys",
				still.mode, still.focus)
		}
	}
}

// --- the box keeps its own counsel, and closes on all four sides -------------------------------

// The box's borders say which region holds the keys. Its cursor row is highlighted only when the box
// holds them: a row in the box wearing a highlight while the tree has the keys looks chosen and is not,
// and the reviewer marks the wrong thing on the strength of it.
func TestTheBoxCursorIsHighlightedOnlyWhenTheBoxHasTheKeys(t *testing.T) {
	m := newFileListModel(t)
	m.focusOn(focusMeta)
	lit := m.boxLine(m.boxFrame(), "ABOUT.md", true)

	m.focusOn(focusFiles)
	cold := m.boxLine(m.boxFrame(), "ABOUT.md", true)

	if lit == cold {
		t.Error("the box's cursor row is drawn the same with and without the keys, so the highlight says nothing")
	}
	if strings.ContainsRune(cold, '\x1b') {
		t.Errorf("the box's cursor row is still styled while the tree holds the keys: %q", cold)
	}
}

// The box is a box in both layouts. It used to leave its right side open where the preview column was
// drawn, on the grounds that the divider was a rule two cells away; without that side the rows inside it
// read as a column of loose text rather than as the changeset's own block.
func TestTheBoxClosesOnTheRightInBothLayouts(t *testing.T) {
	for _, width := range []int{80, 140} {
		m := newFileListModel(t)
		m.width = width
		pane := m.paneWidth() > 0
		for _, focused := range []bool{false, true} {
			if focused {
				m.focusOn(focusMeta)
			} else {
				m.focusOn(focusFiles)
			}
			_, section := m.window()
			lines := m.boxLines(section)
			top, bottom := lines[0], lines[len(lines)-1]
			corners := []string{"\u256d", "\u256e", "\u2570", "\u256f"}
			if focused {
				corners = []string{"\u2554", "\u2557", "\u255a", "\u255d"}
			}
			if !strings.HasSuffix(top, corners[1]) {
				t.Errorf("width %d, focused %v (pane %v): the box's top border ends %q, not its top-right corner %q",
					width, focused, pane, top[len(top)-1:], corners[1])
			}
			if !strings.HasSuffix(bottom, corners[3]) {
				t.Errorf("width %d, focused %v: the box's bottom border ends %q, not its bottom-right corner %q",
					width, focused, bottom[len(bottom)-1:], corners[3])
			}
			rule := "\u2502"
			if focused {
				rule = "\u2551"
			}
			for _, row := range lines[1 : len(lines)-1] {
				if !strings.HasSuffix(row, rule) {
					t.Errorf("width %d, focused %v: a row of the box is not closed: %q", width, focused, row)
				}
			}
		}
	}
}

// The handoff to the editor for a new thread used to move the file tree's cursor onto the thread's own
// row. The consequence was on the other side of the screen: the pane is the file under the tree's cursor,
// so the reviewer came back from the editor to a different diff -- or to none.
func TestThePreviewSurvivesANewThread(t *testing.T) {
	m := previewModel(t)
	focusOnRow(t, m, 0)
	m, cmd := m.ensurePreview()
	m = deliver(t, m, cmd)
	if !strings.Contains(m.View(), "+added line") {
		t.Fatalf("the fixture's pane is not showing a diff to begin with:\n%s", m.View())
	}
	file, cursor := m.previewPath, m.cursor

	m = boxOn(t, m)
	m = pressKey(t, m, runeKey('T'))
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a note about the lock")})
	// Enter creates the thread and asks for the editor. The command is deliberately not run: it hands
	// this process's terminal to $EDITOR. The handoff itself is tested where the editor is faked.
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.mode != modeFiles {
		t.Fatalf("the thread prompt left mode %v", m.mode)
	}
	if kind, _ := activeKind(t, m); kind != rowThread {
		t.Errorf("the new thread left the box's cursor on a %v row, want the thread itself", kind)
	}
	if m.cursor != cursor {
		t.Errorf("the new thread moved the tree's cursor from %d to %d; the pane belongs to that row", cursor, m.cursor)
	}
	// The pane is the row the keys are standing on, and the keys are in the box, so what is on show is the
	// thread the editor has just been opened on -- which is the file the reviewer is coming back to write.
	if m.previewPath == file {
		t.Fatalf("the box has the keys and the pane is still showing %q: the pane follows the cursor", file)
	}

	updated, cmd := m.Update(externalDoneMsg{})
	m = updated.(reviewModel)
	m = deliver(t, m, cmd)
	// And the diff is one key away, because the cursor it belongs to never moved.
	m = paneKey(t, m, runeKey('f'))
	if m.previewPath != file {
		t.Errorf("back at the tree the pane shows %q, not the %q it showed before the box had the keys",
			m.previewPath, file)
	}
	if !strings.Contains(m.View(), "+added line") {
		t.Errorf("coming back from the editor the pane has lost its diff:\n%s", m.View())
	}
}

// --- the file tree's own rules ---------------------------------------------------------------

// The tree is marked by two rules rather than a frame. A frame costs two cells each side, and the
// terminal that needs the region named most -- the narrow one, where nothing else about the layout
// distinguishes the regions -- is the one with no columns to spare. Both rules are drawn whatever holds
// the keys, so the keys moving never resizes the screen; only the glyph changes.
func TestTheTreeIsRuledAboveAndBelowAndTheRulesAreItsFocusLight(t *testing.T) {
	spine := func(m reviewModel, rule string) string { return strings.Repeat(rule, m.listWidth()) }

	m := newFileListModel(t)
	m.focusOn(focusFiles)
	lit := strings.Split(strings.TrimSuffix(m.listBlock(), "\n"), "\n")
	if n := countEqual(lit, spine(m, "\u2550")); n != 2 {
		t.Errorf("the tree is bounded by %d double rules, want two (above and below)\n%s", n, m.listBlock())
	}

	m.focusOn(focusMeta)
	cold := strings.Split(strings.TrimSuffix(m.listBlock(), "\n"), "\n")
	if n := countEqual(cold, spine(m, "\u2550")); n != 0 {
		t.Errorf("%d of the tree's rules stayed double after the keys left it", n)
	}
	if n := countEqual(cold, spine(m, "\u2500")); n < 2 {
		t.Errorf("the tree lost its rules rather than dimming them: %d single rules\n%s", n, m.listBlock())
	}

	if len(lit) != len(cold) {
		t.Errorf("the column is %d rows with the keys and %d without", len(lit), len(cold))
	}
}

func countEqual(lines []string, want string) int {
	n := 0
	for _, l := range lines {
		if l == want {
			n++
		}
	}
	return n
}

// --- where the box opens, and what the prompts leave on screen --------------------------------

// The box keeps the row it was left on, wherever the keys went: it is one half of a column the reviewer
// walks, and a cursor that reset on every visit would lose their place in a changeset with forty threads.
// `a` is the key that names a row of it -- ABOUT.md, what a reviewer comes to the box to read.
func TestTheBoxKeepsItsRowAndATakesTheCursorToAbout(t *testing.T) {
	m := newFileListModel(t)
	m = focusOnRow(t, m, boxIndexOf(t, m, rowThread))
	put := m.metaCursor

	m.focusOn(focusFiles)
	m.focusOn(focusMeta)
	if m.metaCursor != put {
		t.Errorf("the box reopened on %d rather than the %d it was left on", m.metaCursor, put)
	}

	if got := pressRune(m, 'a'); !got.metaHasFocus() || got.rows[got.metaCursor].kind != rowAbout {
		t.Errorf("a left %v on a %v row, want the box on ABOUT.md",
			got.focus, got.rows[got.metaCursor].kind)
	}
}

// The thread prompt is a line of the footer, not a screen of its own. It used to be a mode the diff
// column refused to be drawn in, so pressing `T` next to a diff took the diff away for as long as the
// title was being typed -- and the editor then covered what was left.
func TestThePaneSurvivesTheThreadPrompt(t *testing.T) {
	m := previewModel(t)
	focusOnRow(t, m, 0)
	m, cmd := m.ensurePreview()
	m = deliver(t, m, cmd)
	file, wide := m.previewPath, m.paneWidth()
	if wide == 0 {
		t.Fatal("the fixture has no diff column to lose")
	}

	m = pressRune(m, 'T')
	if m.mode != modePrompt {
		t.Fatalf("`T` gave mode %v, want the title prompt", m.mode)
	}
	if got := m.paneWidth(); got != wide {
		t.Errorf("the prompt took the diff column: %d columns became %d", wide, got)
	}
	if m.previewPath != file {
		t.Errorf("the prompt moved the pane from %q to %q", file, m.previewPath)
	}
	view := m.View()
	if !strings.Contains(view, "+added line") {
		t.Errorf("the pane lost its diff while a title was typed:\n%s", view)
	}
	if !strings.Contains(view, "New thread:") {
		t.Errorf("the prompt is not on screen:\n%s", view)
	}

	// Cancelling is the same screen again, and the box's cursor has not been touched by the trip.
	if back := press(m, tea.KeyEsc); back.mode != modeFiles || back.paneWidth() != wide {
		t.Errorf("esc left mode %v with a %d-column pane", back.mode, back.paneWidth())
	}
}

// --- the box's window when the tree above it changes size ---------------------------------------

// The box's scroll is a position in the box, not in the list. The two regions are one slice, so the
// box's rows sit at whatever index the tree leaves them: fold a directory and they all move up. A
// scroll left at the index the region used to start at then skips the box's first rows -- the span
// among them, which is the one row in the box a reviewer changes.
func TestFoldingTheTreeKeepsTheBoxAtItsTop(t *testing.T) {
	m, _ := treeModel(t)
	// The box opens on ABOUT.md, one row under the span, and the keys go back to the tree, which is
	// where a fold is a key the reviewer is actually pressing.
	m.focusOn(focusMeta)
	m.focusOn(focusFiles)
	at := boxIndexOf(t, m, rowSpan)

	m = cursorOnDir(t, m, "docs/")
	m = pressRune(m, 'h')

	_, section := m.window()
	for _, r := range section {
		if r.row.kind == rowSpan {
			return
		}
	}
	t.Errorf("folding docs/ scrolled the span row (row %d, box starts at %d, box scrolled to %d) out of the box:\n%s",
		at, m.metaStart, m.metaScroll, m.View())
}

// The same confusion one step further out: the box's scroll counts say what its window hides from the
// box's own first row. A scroll read as a position in the list under-counts by the height of the tree,
// so a box with rows still hidden says it has shown everything there is.
func TestTheBoxCountsWhatItHidesFromItsOwnStart(t *testing.T) {
	m := withThreads(t, navModel(t), 12)
	m = focusOnRow(t, m, m.metaStart)
	_, hidden := m.metaHidden()
	if hidden <= 0 {
		t.Fatalf("the box shows all %d of its rows in a %d-row window", len(m.metaRows()), m.metaWindow())
	}

	view := strings.Join(viewRows(m.View()), "\n")
	if !strings.Contains(view, fmt.Sprintf("\u2193 %d", hidden)) {
		t.Errorf("the box hides %d of its %d rows and does not say so:\n%s", hidden, len(m.metaRows()), view)
	}
}

// countRows are the frame rows carrying the two scroll counts, with the styling stripped: the arrows are
// meant to sit in one column, and a test cannot count cells through escape codes.
func countRows(m reviewModel) (up, down string) {
	for _, row := range viewRows(m.View()) {
		plain := ansi.Strip(row)
		if strings.Contains(plain, "\u2191") {
			up = plain
		}
		if strings.Contains(plain, "\u2193") {
			down = plain
		}
	}
	return up, down
}

// The tree counts what its window hides on its own two rules, one count at each end. It counts them
// there because a rule is not a row: the count used to be a row of its own, which cost the window a row
// the moment the list was scrolled -- and the row it cost was the one a page had just landed the cursor
// on. So this asserts three things: the two counts, that the window is the same height scrolled or not,
// and that the cursor's own row survives the page that moved it.
func TestTheTreeCountsWhatItHidesInEachDirection(t *testing.T) {
	m := manyFileModel(t, 40)
	window := m.filesWindow()
	if window < 3 {
		t.Fatalf("the fixture's tree has a %d-row window, too short to hide rows both ways", window)
	}
	m = focusOnRow(t, m, 0)

	// A page down leaves the cursor on the window's last row -- the position the note used to cover.
	m = press(m, tea.KeyCtrlF)
	above, below := m.filesHidden()
	if above <= 0 || below <= 0 {
		t.Fatalf("a page down left %d rows above the window and %d below, want rows hidden both ways",
			above, below)
	}
	if len(fmt.Sprint(above)) == len(fmt.Sprint(below)) {
		t.Fatalf("the fixture counts %d above and %d below, the same number of digits each, so "+
			"nothing here would show the arrows failing to line up", above, below)
	}
	if m.cursor != m.scroll+window-1 {
		t.Errorf("the page left the cursor on %d with the window at %d, want the window's last row %d",
			m.cursor, m.scroll, m.scroll+window-1)
	}
	if got := m.filesWindow(); got != window {
		t.Errorf("scrolling the tree changed its window from %d rows to %d", window, got)
	}

	view := strings.Join(viewRows(m.View()), "\n")
	up, down := countRows(m)
	if up == "" || down == "" {
		t.Fatalf("the tree's rules do not count both ways (above %q, below %q):\n%s", up, down, view)
	}
	for _, want := range []struct {
		row   string
		count int
	}{{up, above}, {down, below}} {
		if !strings.Contains(want.row, fmt.Sprintf("%d", want.count)) {
			t.Errorf("a rule of the tree does not say %d:\n%s\n%s", want.count, want.row, view)
		}
	}
	// The two counts share a numeric field the width of the longer of them, so the arrows line up however
	// many digits each has: two notes a cell apart read as two unrelated remarks.
	if at, to := strings.Index(up, "\u2191"), strings.Index(down, "\u2193"); at != to {
		t.Errorf("the tree hides %d rows above and %d below, and its arrows sit at cells %d and %d, want one column:\n%s\n%s",
			above, below, at, to, up, down)
	}
	if strings.Contains(view, "hidden above") {
		t.Errorf("the tree still spends a row on its scroll note:\n%s", view)
	}
	if name := m.rows[m.cursor].name; !strings.Contains(view, name) {
		t.Errorf("the row the cursor is on (%q) is not on the screen:\n%s", name, view)
	}
	if got := len(m.visibleFiles()); got != window {
		t.Errorf("the tree drew %d rows into a %d-row window", got, window)
	}
	assertFrameFits(t, m)
}
