package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Two regions share the row area: the file tree, and the changeset box above it holding the span,
// ABOUT.md and the threads. Each keeps its own cursor, and Tab, f and m move the keys between them --
// which is what these tests are mostly about, along with what a region does with a key once it has it.

// viewRows splits a rendered frame into rows with the padding removed, which is how tests compare
// against what the model wrote: the frame is padded to the edges of the terminal now.
func viewRows(view string) []string {
	rows := strings.Split(strings.TrimSuffix(view, "\n"), "\n")
	for i, row := range rows {
		rows[i] = strings.TrimRight(row, " ")
	}
	return rows
}

func hasRow(rows []string, want string) bool {
	for _, row := range rows {
		if row == want {
			return true
		}
	}
	return false
}

func navModel(t *testing.T) reviewModel {
	t.Helper()
	m := newFileListModel(t)
	if len(m.rows) < 5 {
		t.Fatalf("fixture has %d rows, want files plus ABOUT.md, the heading and two threads", len(m.rows))
	}
	return m
}

func indexOf(t *testing.T, m reviewModel, kind rowKind) int {
	t.Helper()
	for i, r := range m.rows {
		if r.kind == kind {
			return i
		}
	}
	t.Fatalf("no row of kind %d in:\n%s", kind, rowList(m))
	return -1
}

// lastIndexOfType is where `j` stops: the last file row, or the last row overall.
func lastIndexOfType(t *testing.T, m reviewModel, kind rowKind) int {
	t.Helper()
	last := -1
	for i, r := range m.rows {
		if r.kind == kind {
			last = i
		}
	}
	if last < 0 {
		t.Fatalf("no row of kind %d in:\n%s", kind, rowList(m))
	}
	return last
}

func rowList(m reviewModel) string {
	var b strings.Builder
	for i, r := range m.rows {
		b.WriteString(m.rowText(r))
		if i == m.cursor {
			b.WriteString(" <- cursor")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func press(m reviewModel, key tea.KeyType) reviewModel {
	updated, _ := m.Update(tea.KeyMsg{Type: key})
	return updated.(reviewModel)
}

func pressRune(m reviewModel, r rune) reviewModel {
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return updated.(reviewModel)
}

func selectedKind(t *testing.T, m reviewModel) (rowKind, row) {
	t.Helper()
	r, ok := m.selectedRow()
	if !ok {
		t.Fatalf("nothing selected:\n%s", rowList(m))
	}
	return r.kind, r
}

// focusOnRow puts the keys on one row of either region, which is what a reviewer does before pressing a
// key: they stand on a row, in the region that holds the keys. Tests that act on a row come through here
// rather than setting m.cursor, because a cursor in the region that does not hold the keys is a cursor
// the keys will not see.
func focusOnRow(t *testing.T, m reviewModel, idx int) reviewModel {
	t.Helper()
	if idx < 0 || idx >= len(m.rows) {
		t.Fatalf("row %d is not in the list of %d", idx, len(m.rows))
	}
	if idx >= m.metaStart {
		m.metaCursor = idx
		// The test is placing the cursor, which is exactly what the reviewer doing it themselves
		// means to the box: it stops offering its default row on the way in.
		m.metaHome = true
		m.focusOn(focusMeta)
	} else {
		m.cursor = idx
		m.focusOn(focusFiles)
	}
	m.clamp()
	if _, _, ok := m.activeRow(); !ok {
		t.Fatalf("row %d (%q) did not become the row the keys are on", idx, m.rows[idx].name)
	}
	return m
}

// boxOn gives the keys to the changeset box, which is where the span, ABOUT.md and the threads are.
func boxOn(t *testing.T, m reviewModel) reviewModel {
	t.Helper()
	m.focusOn(focusMeta)
	if !m.metaHasFocus() {
		t.Fatal("the changeset box would not take the keys")
	}
	return m
}

// boxIndexOf is indexOf for a row of the box, which fails loudly if the row has ended up in the tree.
func boxIndexOf(t *testing.T, m reviewModel, kind rowKind) int {
	t.Helper()
	i := indexOf(t, m, kind)
	if i < m.metaStart {
		t.Fatalf("the %v row is at %d, above the box, which starts at %d", kind, i, m.metaStart)
	}
	return i
}

// activeKind is the row the keys are standing on, which is the row the keys act on.
// boxTo hands the keys to the box with its cursor on a row of a given kind, so a test that is about
// that row does not depend on where the box chose to open.
func boxTo(t *testing.T, m reviewModel, kind rowKind) reviewModel {
	t.Helper()
	m = boxOn(t, m)
	want := boxIndexOf(t, m, kind)
	for range len(m.rows) + 1 {
		if m.metaCursor == want {
			return m
		}
		if m.metaCursor > want {
			m = press(m, tea.KeyUp)
		} else {
			m = press(m, tea.KeyDown)
		}
	}
	t.Fatalf("the box's cursor would not come to rest on a %v row (at %d of %d)", kind, m.metaCursor, len(m.rows))
	return m
}

func activeKind(t *testing.T, m reviewModel) (rowKind, row) {
	t.Helper()
	r, _, ok := m.activeRow()
	if !ok {
		t.Fatalf("no row has the keys:\n%s", rowList(m))
	}
	return r.kind, r
}

// Each region has two ends, and j does not cross from one into the other: the box has a cursor of its
// own, and a keystroke that moved both would move the reviewer off a row they were reading in the other.
func TestJStopsAtTheEndOfTheFileTree(t *testing.T) {
	m := navModel(t)
	last := lastIndexOfType(t, m, rowFile)
	m = focusOnRow(t, m, last)
	boxAt := m.metaCursor

	for range 3 {
		m = press(m, tea.KeyDown)
		if m.cursor != last {
			t.Errorf("j past the last row of the tree moved the cursor to %d:\n%s", m.cursor, rowList(m))
		}
		if m.metaCursor != boxAt {
			t.Errorf("j in the tree moved the changeset box from %d to %d", boxAt, m.metaCursor)
		}
		_, row := selectedKind(t, m)
		if !inFileBlock(row.kind) {
			t.Errorf("the tree's last row is %q, want a file or a directory", row.name)
		}
		_ = m.View()
	}
}

// The box has the same two ends, and the same rule about the region it leaves alone.
func TestJStopsAtTheEndOfTheChangesetBox(t *testing.T) {
	m := navModel(t)
	m = focusOnRow(t, m, len(m.rows)-1)
	at, tree := m.metaCursor, m.cursor

	for range 3 {
		m = press(m, tea.KeyDown)
		if m.metaCursor != at {
			t.Errorf("j past the box's last row moved it to %d, want %d", m.metaCursor, at)
		}
		if m.cursor != tree {
			t.Errorf("j in the box moved the file tree from %d to %d", tree, m.cursor)
		}
		_ = m.View()
	}
}

// Tab is the ring: the file tree, the diff where there is room for one, the changeset box, round again.
// This fixture is too narrow for the pane, so the ring here walks through the overlay: the diff is the
// stop the pane would be. The two-target ring, where even the overlay does not fit, is
// TestTabSkipsTheDiffWhenEvenTheOverlayDoesNotFit -- a region that cannot be drawn at all is the one
// that stays off the ring.
func TestTabWalksTheFocusRing(t *testing.T) {
	m := navModel(t)
	if m.paneWidth() > 0 {
		t.Fatalf("the fixture has room for a pane, so the ring here is not the two-target one")
	}
	m.cursor = 2

	toDiff := press(m, tea.KeyTab)
	if toDiff.mode != modePreview || toDiff.focus != focusPreview {
		t.Fatalf("tab gave mode %v with %v, want the diff, which is the stop the pane would be",
			toDiff.mode, toDiff.focus)
	}
	if toDiff.cursor != 2 {
		t.Errorf("tab moved the file tree's cursor from 2 to %d", toDiff.cursor)
	}

	toBox := press(toDiff, tea.KeyTab)
	if !toBox.metaHasFocus() {
		t.Fatalf("tab from the diff left the keys with %v, want the changeset box", toBox.focus)
	}
	if toBox.cursor != 2 {
		t.Errorf("tab moved the file tree's cursor from 2 to %d", toBox.cursor)
	}
	back := press(toBox, tea.KeyTab)
	if back.focus != focusFiles {
		t.Errorf("tab from the box left the keys with %v, want the file tree", back.focus)
	}
	if back.cursor != 2 {
		t.Errorf("tab back landed on row %d, want the row the tree was on", back.cursor)
	}

	// Shift-tab goes the other way round the same ring, so a terminal that sends it for the other
	// direction does not send the reviewer the long way around.
	if got := press(m, tea.KeyShiftTab); !got.metaHasFocus() {
		t.Errorf("shift-tab from the tree left the keys with %v, want the box", got.focus)
	}
	if got := press(toBox, tea.KeyShiftTab); got.mode != modePreview {
		t.Errorf("shift-tab from the box gave mode %v, want the diff, the stop between the two", got.mode)
	}
	if got := press(press(toBox, tea.KeyShiftTab), tea.KeyShiftTab); got.focus != focusFiles {
		t.Errorf("shift-tab back round the ring left the keys with %v, want the tree", got.focus)
	}

	// f and m name a region instead of walking to the next one, from wherever the keys are.
	if got := pressRune(toBox, 'f'); got.focus != focusFiles {
		t.Errorf("f left the keys with %v, want the file tree", got.focus)
	}
	if got := pressRune(pressRune(m, 'f'), 'm'); !got.metaHasFocus() {
		t.Errorf("m left the keys with %v, want the changeset box", got.focus)
	}

	// The box keeps the row the keys were on, so tabbing away to read a diff and back lands on the
	// thread that was being read rather than on the top of the box.
	at := boxIndexOf(t, m, rowThread)
	there := press(focusOnRow(t, m, at), tea.KeyDown)
	if there.metaCursor <= at {
		t.Fatalf("j in the box stayed on %d rather than moving from %d", there.metaCursor, at)
	}
	left := press(there, tea.KeyTab)
	if left.focus != focusFiles {
		t.Errorf("tab left the keys with %v, want the tree", left.focus)
	}
	again := press(press(left, tea.KeyTab), tea.KeyTab)
	if again.metaCursor != there.metaCursor {
		t.Errorf("tab back into the box landed on %d, want the row it left, %d", again.metaCursor, there.metaCursor)
	}
}

func TestThreadsHeadingCollapsesAndExpands(t *testing.T) {
	m := navModel(t)
	open := len(m.rows)
	head := boxIndexOf(t, m, rowThreadsHead)
	m = focusOnRow(t, m, head)

	collapsed := pressRune(m, 'T')
	if collapsed.threadsOpen {
		t.Fatal("T did not collapse the threads")
	}
	if len(collapsed.rows) >= open {
		t.Errorf("collapsing left %d rows, want fewer than %d:\n%s", len(collapsed.rows), open, rowList(collapsed))
	}
	for _, r := range collapsed.rows {
		if r.kind == rowThread || r.kind == rowNewThread {
			t.Errorf("the collapsed section still lists %q", r.name)
		}
	}
	// The heading stays put through its own toggle: that is the row being looked at.
	if collapsed.metaCursor != head {
		t.Errorf("collapse moved the box's cursor from the heading at %d to %d", head, collapsed.metaCursor)
	}
	if strings.Contains(collapsed.View(), "locking.md") {
		t.Error("a collapsed box still showed its threads")
	}
	if !strings.Contains(m.View(), "locking.md") {
		t.Error("the expanded box does not show the nested threads")
	}

	// Enter on the heading is the same toggle, for a reviewer who never reads the hint.
	expanded := press(collapsed, tea.KeyEnter)
	if !expanded.threadsOpen {
		t.Error("Enter on the heading did not expand the threads")
	}
	if len(expanded.rows) != open {
		t.Errorf("re-expanding gave %d rows, want the original %d", len(expanded.rows), open)
	}
	if expanded.metaCursor != head {
		t.Errorf("expanding moved the box's cursor to %d, want the heading at %d", expanded.metaCursor, head)
	}
}

func TestEnterOpensWhatTheRowIsFor(t *testing.T) {
	// The table itself, then the two rows that do not hand off the terminal.
	cases := []struct {
		kind rowKind
		want action
	}{
		{rowFile, actionDiff},
		{rowThread, actionArtifact},
		{rowAbout, actionArtifact},
		{rowThreadsHead, actionCollapse},
		{rowNewThread, actionNewThread},
	}
	for _, c := range cases {
		if got := activateBy(row{kind: c.kind}); got != c.want {
			t.Errorf("kind %d activates as %d, want %d", c.kind, got, c.want)
		}
	}

	m := navModel(t)
	m = focusOnRow(t, m, boxIndexOf(t, m, rowThreadsHead))
	after, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("the heading launched a process instead of collapsing")
	}
	if after.(reviewModel).threadsOpen {
		t.Error("Enter on the heading did not collapse it")
	}

	m = focusOnRow(t, m, boxIndexOf(t, m, rowNewThread))
	after, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	prompting := after.(reviewModel)
	if prompting.mode != modePrompt || prompting.promptKind != promptThread {
		t.Errorf("Enter on %q gave mode %d/kind %d, want the thread prompt",
			newThreadLabel, prompting.mode, prompting.promptKind)
	}

	// A file row hands off to the difftool and a thread row to the editor; both return a
	// command, and only the table above distinguishes which program.
	m = navModel(t)
	m = focusOnRow(t, m, boxIndexOf(t, m, rowThread))
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Error("Enter on a thread opened nothing")
	}
	m = focusOnRow(t, m, indexOfNameBySuffix(t, m, ".go"))
	_, file := selectedKind(t, m)
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Errorf("Enter on %q opened nothing", file.name)
	}
	if file.kind != rowFile || !strings.HasSuffix(file.path, ".go") {
		t.Errorf("the row Enter was pressed on is %q, want a code file", file.name)
	}
}

func TestSpaceMarksFilesAndRefusesTheRest(t *testing.T) {
	m := navModel(t)
	marked := func() int {
		n := 0
		for _, f := range m.sess.Files() {
			if f.Reviewed {
				n++
			}
		}
		return n
	}
	if marked() != 0 {
		t.Fatalf("the fixture starts with %d marked files", marked())
	}

	m = focusOnRow(t, m, boxIndexOf(t, m, rowAbout))
	m = press(m, tea.KeySpace)
	if marked() != 0 {
		t.Error("marking ABOUT.md marked a file")
	}
	if !strings.Contains(m.status, "file rows") {
		t.Errorf("refusing to mark ABOUT.md said %q, want an explanation about file rows", m.status)
	}
	if m.statusErr {
		t.Error("refusing to mark a changeset row is a hint, not an error")
	}

	m = focusOnRow(t, m, boxIndexOf(t, m, rowThread))
	m = press(m, tea.KeySpace)
	if marked() != 0 {
		t.Error("marking a thread marked a file")
	}

	m = focusOnRow(t, m, indexOf(t, m, rowFile))
	m = press(m, tea.KeySpace)
	if marked() != 1 {
		t.Errorf("marking a file marked %d files", marked())
	}
}

func TestNewThreadRowSitsUnderItsThreads(t *testing.T) {
	m := navModel(t)
	last := m.rows[len(m.rows)-1]
	if last.kind != rowNewThread || last.name != newThreadLabel {
		t.Errorf("the last row is %q (kind %d), want %q", last.name, last.kind, newThreadLabel)
	}
	for _, r := range m.rows[boxIndexOf(t, m, rowThreadsHead)+1:] {
		if r.kind == rowThread && filepath.Base(r.path) != r.name {
			t.Errorf("thread row prints %q, want the file name of %q", r.name, r.path)
		}
	}
	// Nested rows are indented, so the group reads as one block under its heading.
	view := m.View()
	if !strings.Contains(view, threadIndent+"locking.md") {
		t.Errorf("threads are not nested under the heading:\n%s", view)
	}
	// Expanded, the heading does not repeat a count that the rows below it already give. It is a row of
	// the box rather than a row of the frame, so it is read off the row itself.
	if head := m.rowText(m.rows[boxIndexOf(t, m, rowThreadsHead)]); strings.Contains(ansiCodes.ReplaceAllString(head, ""), "Threads (") {
		t.Errorf("the expanded heading still shows a count the rows under it give: %q\n%s", head, view)
	}
	collapsed := m
	collapsed.threadsOpen = false
	if got := collapsed.rowText(collapsed.rows[boxIndexOf(t, m, rowThreadsHead)]); !strings.Contains(ansiCodes.ReplaceAllString(got, ""), "▸ Threads (2)") {
		t.Errorf("the collapsed heading reads %q, want it to count the threads it hides", got)
	}
}

func TestRefreshKeepsTheCursorOnTheSameRow(t *testing.T) {
	m := navModel(t)
	m = focusOnRow(t, m, boxIndexOf(t, m, rowThread))
	_, before := activeKind(t, m)

	// A thread created elsewhere — in an editor, or another window — must not move the
	// reviewer off the row they were reading.
	path := filepath.Join(m.sess.Repo().Dir, "changesets", "booking", "third.md")
	if err := os.WriteFile(path, []byte("# Thread: third\n"), 0o644); err != nil {
		t.Fatalf("write thread: %v", err)
	}
	m.refresh()

	after, row := activeKind(t, m)
	if after != row.kind || row.path != before.path {
		t.Errorf("refresh moved the cursor from %q to %q:\n%s", before.path, row.path, rowList(m))
	}
	if _, ok := indexOfName(m, "third.md"); !ok {
		t.Errorf("the new thread is not listed:\n%s", rowList(m))
	}
}

// codeRow finds a source file in the list: the fixture's span also covers the changeset
// directory, so the first row is not necessarily code.
func indexOfNameBySuffix(t *testing.T, m reviewModel, suffix string) int {
	t.Helper()
	for i, r := range m.rows {
		if r.kind == rowFile && strings.HasSuffix(r.path, suffix) {
			return i
		}
	}
	t.Fatalf("no %s row in:\n%s", suffix, rowList(m))
	return -1
}

func indexOfName(m reviewModel, name string) (int, bool) {
	for i, r := range m.rows {
		if r.name == name {
			return i, true
		}
	}
	return 0, false
}

// A thread title is prose, and bubbletea reports a lone space as KeySpace instead of
// KeyRunes: without handling it, every space in a title disappeared before the file name
// was derived from it.
func TestThreadPromptKeepsSpacesInATitle(t *testing.T) {
	m := navModel(t)
	m = pressRune(m, 't')
	if m.mode != modePrompt {
		t.Fatalf("t did not open the thread prompt (mode %d)", m.mode)
	}

	for _, step := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("does the lock")},
		{Type: tea.KeySpace},
		{Type: tea.KeyRunes, Runes: []rune("cover the map")},
	} {
		updated, _ := m.Update(step)
		m = updated.(reviewModel)
	}
	if m.input != "does the lock cover the map" {
		t.Fatalf("prompt input = %q, want the title with its spaces", m.input)
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(reviewModel)
	if cmd == nil {
		t.Fatal("creating the thread did not hand off to the editor")
	}
	// The report rides the handoff, so it is on screen when the editor closes.
	updated, _ = m.Update(externalDoneMsg{label: "Editor exited with an error"})
	m = updated.(reviewModel)
	if !strings.Contains(m.status, "Created") || !strings.Contains(m.status, "does-the-lock-cover-the-map.md") {
		t.Errorf("creating the thread reported %q, want the created path:\n%s", m.status, rowList(m))
	}
	path := filepath.Join(m.sess.Repo().Dir, "changesets", "booking", "does-the-lock-cover-the-map.md")
	if info, statErr := os.Stat(path); statErr != nil || info.IsDir() {
		t.Errorf("the thread file is missing at %s: %v", path, statErr)
	}
}

// The prompt is one field, not two labels: the ghost title says what the field wants, so the line
// under it carries only the keys, and neither repeats the other.
func TestThreadPromptShowsAGhostTitleAndTheKeys(t *testing.T) {
	m := pressRune(navModel(t), 't')
	if m.mode != modePrompt {
		t.Fatalf("t did not open the thread prompt (mode %d)", m.mode)
	}

	rows := viewRows(ansiCodes.ReplaceAllString(m.View(), ""))
	field := lineWithPrefix(rows, threadPromptLabel)
	if field < 0 {
		t.Fatalf("no title field in:\n%s", strings.Join(rows, "\n"))
	}
	if want := threadPromptLabel + threadTitlePlaceholder; rows[field] != want {
		t.Errorf("an empty field reads %q, want the caret on the ghost's first cell", rows[field])
	}
	// The caret over the ghost is a reverse-video cell, which the stripped frame cannot show; what
	// the frame can show is that the caret is not waiting after a title nobody typed.
	if strings.HasSuffix(rows[field], threadPromptCursor) {
		t.Errorf("the caret sits after the ghost rather than on it: %q", rows[field])
	}
	if hint := lineWithExact(rows, threadPromptHint); hint != field+1 {
		t.Errorf("the keys are on row %d, want them on the row under the field (%d):\n%s",
			hint, field+1, strings.Join(rows, "\n"))
	}
	for _, row := range rows {
		if strings.Contains(row, "New thread title") {
			t.Errorf("the field's label is repeated below it: %q", row)
		}
	}

	// The ghost stands in for a title, it is not one already entered: what the reviewer types
	// replaces it.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Does the lock")})
	m = updated.(reviewModel)
	rows = viewRows(ansiCodes.ReplaceAllString(m.View(), ""))
	field = lineWithPrefix(rows, threadPromptLabel)
	if want := threadPromptLabel + "Does the lock" + threadPromptCursor; rows[field] != want {
		t.Errorf("the field reads %q, want the title typed over the ghost", rows[field])
	}
	if strings.Contains(strings.Join(rows, "\n"), threadTitlePlaceholder) {
		t.Errorf("the ghost is still under the typed title:\n%s", strings.Join(rows, "\n"))
	}
	// The hint is the band's second row, whose height the frame already counts: neither line the
	// prompt adds may push a row past the bottom of the terminal.
	assertFrameFits(t, m)
}

// A ghost wider than the window would be cut rather than continued, and half a hint reads as a
// rendering fault instead of an invitation, so a narrow field is left empty.
func TestNarrowThreadPromptDropsItsGhostTitle(t *testing.T) {
	m := pressRune(navModel(t), 't')
	m.width = 20 // less than the label, the ghost and the caret together

	view := ansiCodes.ReplaceAllString(m.View(), "")
	if strings.Contains(view, threadTitlePlaceholder) {
		t.Errorf("the ghost survived a %d-column window it does not fit:\n%s", m.width, view)
	}
	for _, line := range wrapProse(threadPromptHint, m.width) {
		if !strings.Contains(view, line) {
			t.Errorf("the keys lost %q at %d columns:\n%s", line, m.width, view)
		}
	}
	assertFrameFits(t, m)
}

// The box is over the tree, and the counter is under it: what the review is made of is what a reviewer
// reads before choosing a file, and the count of files read belongs with the files it counts.
func TestTheChangesetBoxRendersAboveTheFiles(t *testing.T) {
	m := navModel(t)
	lines := strings.Split(ansiCodes.ReplaceAllString(m.View(), ""), "\n")

	counter := lineWithPrefix(lines, "0 / ")
	if counter < 0 {
		t.Fatalf("no reviewed counter in:\n%s", strings.Join(lines, "\n"))
	}
	top := lineWithPrefix(lines, "\u256d")
	about := lineWithPrefix(lines, "\u2502 ABOUT.md")
	head := lineWithPrefix(lines, "\u2502 \u25be Threads") // expanded: no count on the heading
	if top < 0 || about < 0 || head < 0 {
		t.Fatalf("the box's top (%d), ABOUT.md (%d) or the thread heading (%d) is missing from:\n%s",
			top, about, head, strings.Join(lines, "\n"))
	}
	if top >= about || about > counter || head > counter {
		t.Errorf("the box does not frame its rows above the counter (top %d, ABOUT.md %d, heading %d, counter %d)",
			top, about, head, counter)
	}
	if bottom := lineWithPrefix(lines, "\u2570"); bottom < head || bottom > counter {
		t.Errorf("the box's bottom border is at %d, which does not close the rows it frames:\n%s",
			bottom, strings.Join(lines, "\n"))
	}

	for _, r := range m.rows {
		if r.kind != rowFile {
			continue
		}
		at := lineWithSuffix(lines, r.name)
		if at < 0 {
			t.Errorf("file %q is not rendered:\n%s", r.name, strings.Join(lines, "\n"))
			continue
		}
		if at > counter || at < head {
			t.Errorf("file %q renders at %d, outside the space between the box at %d and the counter at %d",
				r.name, at, head, counter)
		}
	}
}

func lineWithPrefix(lines []string, prefix string) int {
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) {
			return i
		}
	}
	return -1
}

func lineWithExact(lines []string, want string) int {
	for i, l := range lines {
		if strings.TrimSpace(l) == want {
			return i
		}
	}
	return -1
}

func lineWithSuffix(lines []string, suffix string) int {
	for i, l := range lines {
		if strings.HasSuffix(strings.TrimSpace(l), suffix) {
			return i
		}
	}
	return -1
}

// The shortcut bar sits under a rule, so it reads as chrome rather than as more rows of the
// list it follows.
func TestRuleSeparatesTheListFromTheShortcuts(t *testing.T) {
	m := navModel(t)
	m.width = 60
	lines := strings.Split(ansiCodes.ReplaceAllString(m.View(), ""), "\n")

	rule := -1
	for i, l := range lines {
		if strings.TrimRight(l, " ") == strings.Repeat("─", 60) { // padded to the window now
			rule = i
			break
		}
	}
	if rule < 0 {
		t.Fatalf("no rule across the window:\n%s", strings.Join(lines, "\n"))
	}
	help := m.helpLines()
	if len(lines) <= rule+len(help) {
		t.Fatalf("nothing after the rule:\n%s", strings.Join(lines, "\n"))
	}
	for i, want := range help {
		if got := strings.TrimRight(lines[rule+1+i], " "); got != want {
			t.Errorf("line %d after the rule = %q, want the shortcut %q", i+1, got, want)
		}
	}
	for _, l := range lines[:rule] {
		if strings.Contains(l, "j/k move") {
			t.Errorf("the shortcut bar starts above the rule:\n%s", strings.Join(lines, "\n"))
		}
	}
}

// `d` is the difftool for whatever the keys are on, so a reviewer does not have to be in the file tree
// to diff a file -- ABOUT.md and a thread are diffable from the box. Rows that cannot be diffed say why
// instead of opening a difftool with nothing in it.
func TestDDiffsTheSelectedRow(t *testing.T) {
	m := navModel(t)
	if !strings.Contains(strings.Join(m.helpLines(), " "), "d diff") {
		t.Errorf("the shortcut bar does not mention d: %q", m.helpLines())
	}

	// A file row in the span: the same handoff Enter gives.
	m = focusOnRow(t, m, indexOfNameBySuffix(t, m, ".go"))
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if cmd == nil {
		t.Error("d on a file opened nothing")
	}
	if m.status != "" {
		t.Errorf("d on a file reported %q", m.status)
	}

	// The fixture's ABOUT.md is in the span but was invented by the changeset, so there is
	// nothing on the left of the comparison: the file opens, and says why.
	about := indexOf(t, m, rowAbout)
	if !m.inSpan[m.rows[about].path] {
		t.Skipf("the fixture's ABOUT.md is not in the span, so this case needs a different fixture")
	}
	m = focusOnRow(t, m, about)
	after, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = after.(reviewModel)
	if cmd == nil {
		t.Fatalf("d on %q opened nothing", m.rows[about].name)
	}
	if !strings.Contains(m.pendingNote, "added by this changeset") {
		t.Errorf("d on a document the changeset invented said %q, want that it has nothing to compare against",
			m.pendingNote)
	}

	// A thread written this session is not in the span: no empty difftool, the file instead.
	m = navModel(t)
	m = focusOnRow(t, m, boxIndexOf(t, m, rowThread))
	_, at, _ := m.activeRow()
	thread := m.rows[at]
	after, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = after.(reviewModel)
	if cmd == nil {
		t.Errorf("d on %q opened nothing, want the editor", thread.name)
	}
	if m.status != "" {
		t.Errorf("d on %q reported %q before the handoff, where nothing can read it", thread.name, m.status)
	}

	// The heading is not a file at all.
	m = focusOnRow(t, m, boxIndexOf(t, m, rowThreadsHead))
	after, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = after.(reviewModel)
	if cmd != nil || !strings.Contains(m.status, "not a file") {
		t.Errorf("d on the heading gave cmd=%v status=%q", cmd != nil, m.status)
	}
}

// The fallback note has to arrive after the editor closes: set before the handoff, it is
// under the editor's own screen and gone by the time the reviewer looks again.
func TestDiffFallbackNoteSurvivesTheEditor(t *testing.T) {
	m := navModel(t)
	m = focusOnRow(t, m, boxIndexOf(t, m, rowThread)) // a thread the span does not touch
	after, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = after.(reviewModel)
	if cmd == nil {
		t.Fatal("the fallback did not open the editor")
	}
	if !strings.Contains(m.pendingNote, "has not changed in this span") {
		t.Fatalf("the handoff carries nothing to say afterwards: %q", m.pendingNote)
	}

	updated, _ := m.Update(externalDoneMsg{label: "Editor exited with an error"})
	m = updated.(reviewModel)
	if !strings.Contains(m.status, "has not changed in this span") {
		t.Errorf("after the editor closed, status = %q, want the reason it opened the editor", m.status)
	}
	if !strings.Contains(m.status, "editor") {
		t.Errorf("status %q does not say the file was opened in the editor", m.status)
	}
	if m.statusErr {
		t.Error("the fallback is a hint, not an error")
	}
	if m.pendingNote != "" {
		t.Errorf("the note outlived the handoff it belonged to: %q", m.pendingNote)
	}

	// A diff that actually diffed has nothing to report, so the screen comes back clean.
	m = navModel(t)
	m = focusOnRow(t, m, indexOfNameBySuffix(t, m, ".go"))
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")}); cmd == nil {
		t.Fatal("d on a file did not hand off")
	}
	updated, _ = m.Update(externalDoneMsg{})
	if got := updated.(reviewModel).status; got != "" {
		t.Errorf("after a real diff the status = %q, want nothing", got)
	}

	// A handoff that failed reports the failure, and drops the note it was carrying.
	m = focusOnRow(t, m, boxIndexOf(t, m, rowThread))
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = updated.(reviewModel)
	updated, _ = m.Update(externalDoneMsg{err: errors.New("no editor configured"),
		label: "Editor exited with an error"})
	m = updated.(reviewModel)
	if !strings.Contains(m.status, "no editor configured") || !m.statusErr {
		t.Errorf("a failed handoff said %q (err=%v), want the failure", m.status, m.statusErr)
	}
	if strings.Contains(m.status, "has not changed") || m.pendingNote != "" {
		t.Errorf("a failed handoff kept its note: status %q, pending %q", m.status, m.pendingNote)
	}
}

// A document is diffable only with two sides to the comparison: the span has to have changed
// it, and it has to have existed where the span starts.
func TestArtifactActionNeedsTwoSides(t *testing.T) {
	cases := []struct {
		inSpan, hasPrior bool
		want             action
		why              string
	}{
		{true, true, actionDiff, "the unreviewed span rewrote a document from the last round"},
		{true, false, actionEdit, "the changeset invented the document"},
		{false, true, actionEdit, "the span left the document alone"},
		{false, false, actionEdit, "the document is new and outside the span"},
	}
	for _, c := range cases {
		if got := artifactAction(c.inSpan, c.hasPrior); got != c.want {
			t.Errorf("artifactAction(%v, %v) = %d, want %d (%s)", c.inSpan, c.hasPrior, got, c.want, c.why)
		}
	}
}

// The two reasons a document opens in the editor instead of a difftool read differently, and
// the reviewer should get the right one.
func TestArtifactNoteNamesTheReason(t *testing.T) {
	inSpan := reviewModel{inSpan: map[string]bool{"changesets/x/locking.md": true}}
	got := inSpan.artifactNote(row{path: "changesets/x/locking.md", name: "locking.md"})
	if !strings.Contains(got, "added by this changeset") {
		t.Errorf("note for a new document = %q", got)
	}

	outside := reviewModel{inSpan: map[string]bool{}}
	got = outside.artifactNote(row{path: "changesets/x/locking.md", name: "locking.md"})
	if !strings.Contains(got, "has not changed in this span") {
		t.Errorf("note for a document the span left alone = %q", got)
	}
	for _, n := range []string{got} {
		if !strings.Contains(n, "opened in the editor") {
			t.Errorf("note %q does not say what happened instead", n)
		}
	}
}

// The rule is only as good as the lookup under it: it asks about the file the row names, at
// the revision the span starts from.
func TestSessionAnswersWhetherAFileExistedAtTheSpanStart(t *testing.T) {
	m := navModel(t)
	ctx := context.Background()
	from := m.sess.Span().From

	if m.sess.HasVersionAt(ctx, from, m.sess.AboutPath()) {
		t.Errorf("%s existed at %s, but the fixture's changeset adds it", m.sess.AboutPath(), from)
	}
	if !m.sess.HasVersionAt(ctx, from, "main.go") {
		t.Errorf("main.go did not exist at %s, but the fixture seeds it before the branch", from)
	}
}

// Enter on a changeset document goes through the same decision as `d`, so the first review of
// a changeset reads ABOUT.md rather than diffing it against nothing.
func TestEnterOnADocumentFollowsTheSpan(t *testing.T) {
	m := navModel(t)
	m = focusOnRow(t, m, boxIndexOf(t, m, rowAbout))
	after, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = after.(reviewModel)
	if cmd == nil {
		t.Fatal("Enter on ABOUT.md opened nothing")
	}
	if !strings.Contains(m.pendingNote, "added by this changeset") {
		t.Errorf("Enter on a document the changeset invented said %q, want the editor with a reason",
			m.pendingNote)
	}
}
