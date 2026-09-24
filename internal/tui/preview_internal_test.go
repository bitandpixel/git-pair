package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// previewModel is the navigation fixture with room for a pane and a fake git, so a test can say
// exactly what the preview should be showing without running git.
func previewModel(t *testing.T) reviewModel {
	t.Helper()
	m := navModel(t)
	m.width, m.height = 140, 24
	m.previewOn = true
	m.patchFor = func(_ context.Context, path string) Patch {
		return Patch{
			Lines:   []string{"diff --git a/" + path, "@@ -1 +1 @@", "+added line"},
			Added:   1,
			Deleted: 0,
		}
	}
	// The reviewer's own edits are empty unless a test says otherwise: that is the ordinary case,
	// and it keeps every other expectation in this file about what the pane looks like.
	m.workingFor = func(_ context.Context, _ string) Patch { return Patch{} }
	// Files the pane reads as text are git's answer as much as a patch is, and a test that has stubbed
	// git must not have a real repository answering behind its back. The text is nothing any patch this
	// file asserts about contains, so a test that means to look at a diff cannot mistake this for one.
	m.contentFor = func(_ context.Context, _ string) Document {
		return Document{Sections: []DocSection{{Lines: []string{"the file's own text"}}}}
	}
	return m
}

// deliver runs whatever fetches the model asked for, which is a batch now that the pane asks git
// about two sources per file -- and descends into nested batches, because Update wraps a handler's
// command around its own fetch. A harness that unwrapped only the outer layer would quietly leave the
// pane waiting on a diff that had already arrived, and every test written against it would be testing
// an empty pane.
func deliver(t *testing.T, m reviewModel, cmd tea.Cmd) reviewModel {
	t.Helper()
	if cmd == nil {
		return m
	}
	return deliverMsg(t, m, cmd())
}

func deliverMsg(t *testing.T, m reviewModel, msg tea.Msg) reviewModel {
	t.Helper()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			m = deliverMsg(t, m, c())
		}
		return m
	}
	updated, _ := m.Update(msg)
	rm, ok := updated.(reviewModel)
	if !ok {
		t.Fatalf("a preview answer produced %T", updated)
	}
	return rm
}

// askPreview moves the pane onto the cursor and delivers whatever git has to say about it. A
// patch already in the cache needs no fetch, which is the point of the cache.
func askPreview(t *testing.T, m reviewModel) reviewModel {
	t.Helper()
	m, cmd := m.ensurePreview()
	if cmd == nil && m.previewPath == "" {
		t.Fatal("the pane has nothing to show and asked for nothing")
	}
	return deliver(t, m, cmd)
}

func TestPreviewFollowsTheCursor(t *testing.T) {
	m := previewModel(t)
	// main.go is the one file in this fixture that the span only changed, so its pane is a diff. The
	// files beside it are new, and their pane is the file's own text.
	at := indexOfNameBySuffix(t, m, "main.go")
	m.cursor = at
	m = askPreview(t, m)

	if want := m.rows[at].path; m.previewPath != want {
		t.Errorf("the pane shows %q, want the file under the cursor %q", m.previewPath, want)
	}
	view := m.View()
	if !strings.Contains(view, "│") {
		t.Errorf("no divider between the list and the preview:\n%s", view)
	}
	if !strings.Contains(view, "+added line") {
		t.Errorf("the pane does not show the patch:\n%s", view)
	}

	// Moving the cursor moves the pane, and asks for the new file.
	other := indexOf(t, m, rowFile)
	if other == at {
		other = indexOfNameBySuffix(t, m, ".yaml")
	}
	m.cursor = other
	m = askPreview(t, m)
	if m.previewPath != m.rows[other].path {
		t.Errorf("after moving, the pane shows %q, want %q", m.previewPath, m.rows[other].path)
	}
}

// The pane is a wide-terminal luxury. Below the threshold the list keeps the whole terminal, and `p`
// takes the screen with the diff instead of squeezing the list -- pinned in overlay_internal_test.go.
// What is pinned here is that the pane never appears uninvited, that the two columns always add up, and
// that a terminal too small for even the overlay says which way it is short.
func TestPreviewOnlyAppearsWhenThereIsRoom(t *testing.T) {
	narrow := previewModel(t)
	narrow.width = 72
	if narrow.paneWidth() != 0 {
		t.Errorf("a %d-column terminal gets a %d-column pane", narrow.width, narrow.paneWidth())
	}
	if drawsDivider(narrow) {
		t.Error("the narrow layout draws a divider anyway")
	}

	wide := previewModel(t)
	pane := wide.paneWidth()
	list := wide.width - pane - previewGap
	if pane+list+previewGap != wide.width {
		t.Errorf("pane %d + list %d + gap %d != the terminal's %d columns", pane, list, previewGap, wide.width)
	}
	// The list is sized to its own rows rather than to a share of the screen: a changeset of
	// short paths hands the columns it cannot use to the diff.
	if list != wide.listContentWidth() {
		t.Errorf("the list took %d columns, want the %d its own rows need", list, wide.listContentWidth())
	}
	if list < previewListMin || list > previewListMax {
		t.Errorf("the list took %d columns, outside the %d to %d it is allowed", list, previewListMin, previewListMax)
	}
	if old := (wide.width - previewGap) * 60 / 100; pane <= old {
		t.Errorf("the pane got %d columns; the fixed 60/40 split used to give it %d, so adapting bought nothing", pane, old)
	}
	if !drawsDivider(wide) {
		t.Error("a wide terminal draws no preview")
	}

	off := previewModel(t)
	off.previewOn = false
	if drawsDivider(off) {
		t.Error("`p` off did not remove the pane")
	}

	// Toggling in a terminal too small for even the overlay must explain, not stay silent -- and name
	// the smaller of the two asks, since that is the one the reviewer can actually grow to.
	crowded := previewModel(t)
	crowded.width = 30
	crowded.previewOn = false // `p` turns it on, which is the case that cannot fit
	updated, _ := crowded.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	crowded = updated.(reviewModel)
	if crowded.mode != modeFiles {
		t.Fatalf("a 30-column terminal opened the overlay; it cannot show a diff at that width")
	}
	if !strings.Contains(crowded.status, fmt.Sprint(previewOverlayMinWidth)) || !strings.Contains(crowded.status, "30") {
		t.Errorf("toggling in a 30-column terminal said %q, want the width it needs", crowded.status)
	}
}

// Nothing may wrap: a row that wrapped would push the divider out of its column and skew every
// row under it.
func TestRowsAndPreviewStayInsideTheirColumns(t *testing.T) {
	m := previewModel(t)
	// A path long enough that the old byte-wise truncation would have let it wrap.
	m.rows[0].name = strings.Repeat("deep/", 20) + "service.go"
	m = askPreview(t, m)

	for i, line := range strings.Split(strings.TrimSuffix(m.View(), "\n"), "\n") {
		if w := lipgloss.Width(line); w > m.width {
			t.Errorf("line %d is %d columns wide in a %d-column terminal: %q", i, w, m.width, line)
		}
	}
	if !strings.Contains(m.View(), "…") {
		t.Error("the over-long row was not clipped")
	}
}

// Paging belongs to the pane, not to the list: the cursor does not move, and the offset cannot
// walk past the end of the diff.
func TestPreviewPagingStaysInsideTheDiff(t *testing.T) {
	m := previewModel(t)
	m.patchFor = func(_ context.Context, _ string) Patch {
		lines := make([]string, 0, 60)
		for i := range 60 {
			lines = append(lines, fmt.Sprintf("line %02d", i))
		}
		return Patch{Lines: lines, Added: 60}
	}
	// ctrl-f and ctrl-b page whichever region holds the keys, so the diff has to be holding them
	// before the test asks it to page.
	m = focusPane(t, askPreview(t, m))
	body := m.previewBodyRows()
	max := 60 - body

	for range max + 10 {
		m = paneKey(t, m, keyMsg(tea.KeyCtrlF))
	}
	if m.previewOffset != max {
		t.Errorf("paged to offset %d, want it to stop at %d (%d lines, %d shown)",
			m.previewOffset, max, 60, body)
	}
	view := m.View()
	if !strings.Contains(view, "line 59") {
		t.Errorf("at the end of the diff the pane does not show its last line:\n%s", view)
	}

	for range max + 10 {
		m = paneKey(t, m, keyMsg(tea.KeyCtrlB))
	}
	if m.previewOffset != 0 {
		t.Errorf("paged back to %d, want 0", m.previewOffset)
	}

	// The cursor moves, the pane starts over: a stale offset in a new file is a blank pane.
	before := m.previewOffset
	m.cursor = indexOf(t, m, rowFile)
	m.previewPath, m.previewOffset = "", before
	m = askPreview(t, m)
	if m.previewOffset != 0 {
		t.Errorf("after moving to another file the pane is still offset by %d", m.previewOffset)
	}
}

// The pane is a window onto git's output, not a rendering of it: the escape sequences git chose
// have to survive the trip, or the preview has become the diff renderer PRD §3 rules out.
func TestPreviewPassesGitsColoursThrough(t *testing.T) {
	m := previewModel(t)
	m.patchFor = func(_ context.Context, _ string) Patch {
		return Patch{Lines: []string{"\x1b[32m+added\x1b[0m", "\x1b[31m-removed\x1b[0m"}, Added: 1, Deleted: 1}
	}
	m = askPreview(t, m)
	view := m.View()
	if !strings.Contains(view, "\x1b[32m+added\x1b[0m") {
		t.Errorf("git's green was not passed through:\n%q", view)
	}
	if !strings.Contains(view, "+1 \u22121") {
		t.Errorf("the pane header does not carry git's own counts:\n%s", view)
	}
}

// A file the span did not change has nothing to preview, and the pane says which case it is in
// rather than showing an empty column.
func TestPreviewSaysWhenThereIsNothingToPreview(t *testing.T) {
	m := previewModel(t)
	m.patchFor = func(_ context.Context, _ string) Patch { return Patch{} }
	m = askPreview(t, m)
	if !strings.Contains(m.View(), "no changes in this span") {
		t.Errorf("the pane of an untouched file said nothing about it:\n%s", m.View())
	}

	m = previewModel(t)
	m.patchFor = func(_ context.Context, _ string) Patch { return Patch{Err: "git diff failed: boom"} }
	m = askPreview(t, m)
	if !strings.Contains(m.View(), "git diff failed") {
		t.Error("a failed fetch left the pane blank instead of reporting itself")
	}
	if m.statusErr {
		t.Error("a failed preview must not be a session error")
	}
}

// Asking git about the same file twice is a bug, not a refresh: the list is walked with j and k.
func TestPatchesAreFetchedOncePerFile(t *testing.T) {
	m := previewModel(t)
	asks := map[string]int{}
	m.patchFor = func(_ context.Context, path string) Patch {
		asks[path]++
		return Patch{Lines: []string{"+x"}, Added: 1}
	}

	// The pane reads some rows as a file and others as a diff, and this is about the diffs. Walk the rows
	// whose pane is a diff, so the count below is a count of patch fetches and not of rows that asked for
	// something else entirely.
	var diffs []int
	for i := range m.rows {
		m.cursor = i
		if kind, _, ok := m.previewRowTarget(); ok && kind == previewDiff {
			diffs = append(diffs, i)
		}
	}
	if len(diffs) < 2 {
		t.Fatalf("the fixture offers %d rows whose pane is a diff, want at least 2", len(diffs))
	}
	walk := append([]int{}, diffs...)
	walk = append(walk, diffs...)
	for _, idx := range walk {
		m.cursor = idx
		m = askPreview(t, m)
	}
	for path, n := range asks {
		if n != 1 {
			t.Errorf("%s was fetched %d times, want once", path, n)
		}
	}
	if len(asks) != len(diffs) {
		t.Errorf("fetched %d paths, want the %d rows visited", len(asks), len(diffs))
	}

	// Changing the span invalidates every answer: the patches describe a span that is gone.
	m.forgetPatches()
	m.cursor = diffs[0]
	m = askPreview(t, m)
	if asks[m.rows[diffs[0]].path] != 2 {
		t.Error("forgetting the patches did not make the next visit ask again")
	}
}

// A short terminal has no room for a column of diff either, and the two excuses are different:
// a reviewer with a 140x10 window is not being told about columns.
// A short terminal has the same excuse as a narrow one, and gets the same answer: the number it is
// short by, from whichever layout was being tried. At 10 rows even the overlay is out -- it wants 12 --
// so the 12 is what gets reported rather than the 16 a pane would have wanted.
func TestPreviewNeedsRowsAsWellAsColumns(t *testing.T) {
	short := previewModel(t)
	short.height = 10
	if short.paneWidth() != 0 {
		t.Errorf("a %d-row terminal gets a %d-column pane", short.height, short.paneWidth())
	}

	short.previewOn = false
	updated, _ := short.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	short = updated.(reviewModel)
	if !strings.Contains(short.status, fmt.Sprint(previewOverlayMinHeight)) || !strings.Contains(short.status, "10") {
		t.Errorf("toggling in a 10-row terminal said %q, want the rows even the overlay needs", short.status)
	}
}

// The frame has to fit the terminal and the columns have to add up to it: an off-by-one in the
// gap around the divider wraps every row, which is the classic failure of putting a second column
// in a terminal program. It only shows up with diff lines wide enough to fill the pane, which is
// why the fixture's short lines are not enough.
func TestTheFrameIsExactlyTheWidthOfTheTerminal(t *testing.T) {
	m := previewModel(t)
	m.patchFor = func(_ context.Context, path string) Patch {
		return Patch{Lines: []string{strings.Repeat("x", 400), "short", strings.Repeat("y", 400)}, Added: 3}
	}
	m = askPreview(t, m)

	rows := strings.Split(strings.TrimSuffix(joinColumns(m.listBlock(), m.previewLines(), m.listWidth(), false), "\n"), "\n")
	if len(rows) < 5 {
		t.Fatalf("the block is %d rows, want a list with a pane beside it", len(rows))
	}
	// The layout fixes the divider's column; the box's own border is a different │ in a different
	// place, so the test asks the cell the divider occupies rather than looking for the glyph.
	divider := m.dividerAt()
	if dividerColumn(rows[0], divider) != "\u2502" {
		t.Fatalf("no divider to hang the layout on at column %d: %q", divider, rows[0])
	}

	widest := 0
	for i, row := range rows {
		if w := lipgloss.Width(row); w > m.width {
			t.Errorf("row %d is %d columns in a %d-column terminal, so it wraps: %q", i, w, m.width, row)
		} else if w > widest {
			widest = w
		}
		// Measured in cells, not bytes: the rows carry wide characters and styling, and a byte
		// offset would put this divider in a different place on every row.
		if at := dividerColumn(row, divider); at != "\u2502" {
			t.Errorf("row %d draws %q where the divider belongs (column %d): %q", i, at, divider, row)
		}
	}
	if widest != m.width {
		t.Errorf("the widest row is %d columns in a %d-column terminal: pane %d + list %d + gap %d do not add up",
			widest, m.width, m.paneWidth(), m.listWidth(), previewGap)
	}
}

func dividerColumn(row string, at int) string {
	plain := ansiCodes.ReplaceAllString(row, "")
	cells := []rune(plain)
	if at < 0 || at >= len(cells) {
		return ""
	}
	return string(cells[at])
}

// drawsDivider reports whether the frame separates two columns. The changeset box draws │ as well, so
// a search for the glyph can no longer tell a pane from a box; this asks the one cell the layout
// reserves for the divider, which is the cell that moves when the list column changes width.
func drawsDivider(m reviewModel) bool {
	at := m.dividerAt()
	for _, row := range viewRows(m.View()) {
		if g := dividerColumn(row, at); g == "\u2502" || g == "\u2551" {
			return true
		}
	}
	return false
}

// The frame is the terminal now: filled top to bottom and padded to the edges. Ending with a
// newline matters -- the renderer writes a line per line and moves down afterwards, so a frame
// that already fills the screen has nowhere to put the cursor and the terminal scrolls.
func TestTheFrameFillsTheWindow(t *testing.T) {
	for _, preview := range []bool{false, true} {
		m := previewModel(t)
		m.previewOn = preview
		m.width, m.height = 140, 30
		if preview {
			m = askPreview(t, m)
		}
		view := m.View()
		if strings.HasSuffix(view, "\n") {
			t.Errorf("preview %v: the frame ends in a newline, which scrolls the terminal", preview)
		}
		rows := strings.Split(view, "\n")
		if len(rows) != m.height {
			t.Errorf("preview %v: the frame is %d rows in a %d-row window", preview, len(rows), m.height)
		}
		for i, row := range rows {
			if w := lipgloss.Width(row); w != m.width {
				t.Errorf("preview %v: row %d is %d columns, want the %d the window has", preview, i, w, m.width)
			}
		}
	}
}

// A diff line wider than the pane is broken rather than cut, and the break must not lose anything.
func TestPreviewWrapsWithoutLosingCharacters(t *testing.T) {
	long := "+return service.Lock(ctx, r.URL.Path, tenant.ID, \"already held\")"
	rows := wrapLine(long, 12)
	if len(rows) < 4 {
		t.Fatalf("a %d-column line became %d rows, want several", lipgloss.Width(long), len(rows))
	}
	for _, row := range rows {
		if w := lipgloss.Width(row); w > 12 {
			t.Errorf("row %q is %d columns, over the limit of 12", row, w)
		}
	}
	if got := strings.Join(rows, ""); got != long {
		t.Errorf("wrapping changed the text:\n in  %q\n out %q", long, got)
	}
}

// The renderer skips rows that have not changed, so a colour left open at a break would tint
// whatever got drawn under it. Each row carries its own styling in and out.
func TestWrappedRowsCarryTheirOwnColour(t *testing.T) {
	rows := wrapLine("\x1b[32m"+strings.Repeat("y", 25)+"\x1b[0m", 10)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	for _, row := range rows {
		if !strings.HasPrefix(row, "\x1b[32m") {
			t.Errorf("row %q does not open the colour it is drawn in", row)
		}
		if !strings.HasSuffix(row, "\x1b[0m") {
			t.Errorf("row %q leaves the colour open for whatever comes next", row)
		}
		if w := lipgloss.Width(row); w > 10 {
			t.Errorf("row %q is %d columns", row, w)
		}
	}
}

// The numbers come out of git's own @@ headers and the +,- and context lines under them. Nothing
// before a hunk header is numbered, and a line git gave no position keeps the gutter blank rather
// than inventing one.
func TestPreviewNumbersTheLinesItCan(t *testing.T) {
	patch := Patch{Lines: []string{
		"diff --git a/service.go b/service.go",
		"index 1111111..2222222 100644",
		"--- a/service.go",
		"+++ b/service.go",
		"@@ -10,4 +10,5 @@",
		" func a() {",
		"-\told := 1",
		"+\tnew := 2",
		" }",
		"\\ No newline at end of file",
		"diff --git a/other.go b/other.go",
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/other.go",
		"@@ -0,0 +1,2 @@",
		"+package main",
		"+",
	}}
	numbers, max := lineNumbers(patch.Lines)
	want := []int{0, 0, 0, 0, 0, 10, 11, 11, 12, 0, 0, 0, 0, 0, 0, 1, 2}
	for i := range want {
		if numbers[i] != want[i] {
			t.Errorf("line %d (%q) numbered %d, want %d", i, patch.Lines[i], numbers[i], want[i])
		}
	}
	if max != 12 {
		t.Errorf("the largest number is %d, want 12, which sets the gutter width", max)
	}

	rows := strings.Join(rowTexts(previewBody(patch, 60, unmarked)), "\n")
	if !strings.Contains(ansi.Strip(rows), "10  func a() {") {
		t.Errorf("a context line does not carry its number on the side being reviewed:\n%s", ansi.Strip(rows))
	}
	// The tabs are spelled out to their stops by then, so the expectation says the same.
	padded := func(rest string) string { return "11 " + strings.Replace(rest, "\t", strings.Repeat(" ", 7), 1) }
	if !strings.Contains(ansi.Strip(rows), padded("-\told := 1")) || !strings.Contains(ansi.Strip(rows), padded("+\tnew := 2")) {
		t.Errorf("the removed and added lines should each carry the number of their own side:\n%s", ansi.Strip(rows))
	}
}

// Colours are classification-proof: git colours the very + and - characters the numbering looks
// for, so the decision is made on the stripped line while the display keeps the original.
func TestNumberingSurvivesGitsColours(t *testing.T) {
	numbers, _ := lineNumbers([]string{
		"\x1b[32m@@ -1 +1,2 @@\x1b[0m",
		"\x1b[32m+added\x1b[0m",
	})
	if numbers[1] != 1 {
		t.Errorf("a coloured added line was numbered %d, want 1", numbers[1])
	}
}

// A patch with no hunk header -- a binary note, a truncated diff -- gets no numbers rather than
// numbers that would be wrong.
func TestNothingIsNumberedWithoutAHunkHeader(t *testing.T) {
	numbers, max := lineNumbers([]string{
		"diff --git a/logo.png b/logo.png",
		"index 1111111..2222222 100644",
		"Binary files a/logo.png and b/logo.png differ",
	})
	if max != 0 || numbers[2] != 0 {
		t.Errorf("metadata was numbered: %v", numbers)
	}
}

// The list stops growing at the cap: one vendored path should not cost the reviewer the columns
// the diff is read in, and the path that would have widened it is clipped instead.
func TestTheListStopsGrowingAtTheCap(t *testing.T) {
	m := previewModel(t)
	m.rows[0].name = strings.Repeat("vendor/", 30) + "service.go"
	if got := m.listContentWidth(); got <= previewListMax {
		t.Fatalf("the widest row is %d columns; the test needs one over the cap of %d", got, previewListMax)
	}
	list, pane := m.previewLayout()
	if list != previewListMax {
		t.Errorf("the list took %d columns, want the cap of %d", list, previewListMax)
	}
	if pane != m.width-list-previewGap {
		t.Errorf("the pane got %d columns, want the %d left over", pane, m.width-list-previewGap)
	}
	if !strings.Contains(clip(m.rowText(m.rows[0]), list), "…") {
		t.Error("the path over the cap was not clipped to the column")
	}
}

// The column is sized for the whole changeset, not the rows currently in the window: a divider
// that moved as a longer path scrolled into view would make the screen jump under the cursor.
func TestTheDividerDoesNotMoveWithTheWindow(t *testing.T) {
	m := previewModel(t)
	m.height = previewMinHeight
	// The synthetic rows go into the file region: the list is one slice holding two regions, and a
	// file row appended past the split would be a row of the changeset box.
	extra := make([]row, 0, 13)
	for i := range 12 {
		extra = append(extra, row{kind: rowFile, name: fmt.Sprintf("f%02d.go", i), path: fmt.Sprintf("f%02d.go", i)})
	}
	long := strings.Repeat("deep/", 6) + "thing.go"
	extra = append(extra, row{kind: rowFile, name: long, path: long})
	rows := make([]row, 0, len(m.rows)+len(extra))
	rows = append(rows, m.rows[:m.metaStart]...)
	rows = append(rows, extra...)
	rows = append(rows, m.rows[m.metaStart:]...)
	m.rows = rows
	last := m.metaStart + len(extra) - 1

	list := m.listWidth()
	visible := 0
	for _, idx := range m.visibleFiles() {
		if w := lipgloss.Width(m.rowText(m.rows[idx])); w > visible {
			visible = w
		}
	}
	if list <= visible {
		t.Errorf("the column is %d columns, which is the window's own width (%d): it should be sized for the path below", list, visible)
	}
	m.scroll = last
	if got := m.listWidth(); got != list {
		t.Errorf("scrolling to the long path moved the divider from %d to %d", list, got)
	}
}

// The minimums are arithmetic rather than branches, so they need a test instead of a guard: at
// the narrowest terminal that gets a pane, the diff still gets a readable column even with the
// list at its cap.
func TestPreviewHasRoomAtTheThreshold(t *testing.T) {
	m := previewModel(t)
	m.width = previewMinWidth
	m.rows[0].name = strings.Repeat("vendor/", 30) + "service.go"

	list, pane := m.previewLayout()
	if list != previewListMax {
		t.Fatalf("the list took %d columns at the threshold, want the cap of %d", list, previewListMax)
	}
	if pane < previewMinPane {
		t.Errorf("at %d columns the pane gets %d, want at least %d", previewMinWidth, pane, previewMinPane)
	}
}

// With the pane off, the list is the whole terminal again: hiding the diff must not leave the
// paths clipped to a column that is no longer there.
func TestHidingThePreviewGivesTheListBackItsWidth(t *testing.T) {
	m := previewModel(t)
	m.rows[0].name = strings.Repeat("deep/", 12) + "service.go"
	if clip(m.rowText(m.rows[0]), m.listWidth()) == m.rowText(m.rows[0]) {
		t.Fatal("the long path fits the pane's list column; the test needs one that does not")
	}
	m.previewOn = false
	if got := m.listWidth(); got != m.width {
		t.Errorf("with the preview hidden the list is %d columns, want the terminal's %d", got, m.width)
	}
	if drawsDivider(m) {
		t.Error("the divider is still drawn with the preview hidden")
	}
}

// A tab is the one character the pane cannot take as git wrote it: the terminal moves it to the
// next stop, so a row measured with the tab worth nothing is a row the terminal wraps, and a
// wrapped row shifts every row under it. Tabs become the spaces they advance to, counted where
// they land.
func TestPreviewExpandsTabsToTheirStops(t *testing.T) {
	long := "+\treturn " + strings.Repeat("x", 40)
	rows := wrapLine(long, 14)
	for _, row := range rows {
		if strings.ContainsRune(row, '\t') {
			t.Errorf("a row still carries a tab, which the terminal will move: %q", row)
		}
		if w := lipgloss.Width(row); w > 14 {
			t.Errorf("row %q is %d columns, over the limit of 14", row, w)
		}
	}
	if !strings.HasPrefix(rows[0], "+       ") {
		t.Errorf("the tab after the + should be spelled out to the next stop of 8: %q", rows[0])
	}
	if got := strings.Count(strings.Join(rows, ""), "x"); got != 40 {
		t.Errorf("wrapping around the tab lost %d of the 40 characters", 40-got)
	}
}

// A tab that would be pushed past the column by its own padding is not left dangling on the next
// row as phantom indentation.
func TestATabAtTheBreakDoesNotIndentTheNextRow(t *testing.T) {
	rows := wrapLine("+-\t\tmore", 4)
	for _, row := range rows {
		if strings.HasPrefix(row, " ") {
			t.Errorf("a row opens with padding a tab left behind: %q", row)
		}
	}
}

// Closing the editor or difftool drops every cached patch -- the tool may have changed the file,
// which is the one moment a stale preview is definitely wrong -- and that is exactly when the pane
// on the file under the cursor has to refill itself rather than wait for the reviewer to move.
func TestThePreviewRefillsAfterAToolCloses(t *testing.T) {
	m := previewModel(t)
	m = askPreview(t, m)
	at := m.previewPath
	if at == "" {
		t.Fatal("the fixture pane is empty before the handoff")
	}

	updated, cmd := m.Update(externalDoneMsg{})
	m = updated.(reviewModel)
	if len(m.patches) != 0 {
		t.Error("the cached patch survived the tool that may have changed the file")
	}
	if m.previewPath != at {
		t.Errorf("after the tool closed the pane shows %q, want the file under the cursor %q", m.previewPath, at)
	}
	if cmd == nil {
		t.Fatal("nothing was fetched, so the pane stays blank until the cursor moves")
	}
	m = deliver(t, m, cmd)
	if !strings.Contains(m.View(), "+added line") {
		t.Errorf("the pane did not come back after the tool:\n%s", m.View())
	}
}

// Your uncommitted edits are a diff too, but git's bytes do not say who wrote them -- an added
// line you typed and one the author typed are the same green -- so the pane prints them under a
// caption that says whose they are, above the author's span rather than below it: the section you came to
// check is the one that should not need paging. Each of your lines carries the marker too, because the
// caption is one row and scrolls away while the rows it names stay.
func TestPreviewShowsYourEditsUnderTheirOwnCaption(t *testing.T) {
	m := previewModel(t)
	m.workingFor = func(_ context.Context, _ string) Patch {
		return Patch{Lines: []string{
			"diff --git a/main.go b/main.go",
			"index 1234567..89abcde 100644",
			"--- a/main.go",
			"+++ b/main.go",
			"@@ -1 +1,3 @@",
			"+a note you typed",
			" a line of the file you did not touch",
			"-and one you removed",
		}, Added: 1, Deleted: 1}
	}
	m = askPreview(t, m)
	shown := ansi.Strip(m.View())

	if !strings.Contains(shown, "you \u00b7 uncommitted  +1 \u22121") {
		t.Errorf("your edits are on screen without a caption naming them and their size:\n%s", shown)
	}
	if !strings.Contains(shown, "+a note you typed") {
		t.Errorf("your edits are missing:\n%s", shown)
	}
	if !strings.Contains(shown, "+added line") {
		t.Errorf("the author's span vanished when your edits arrived:\n%s", shown)
	}
	// The caption first, then your lines, then the author's span: a section at the bottom of a diff longer
	// than the pane is a section below the fold.
	if strings.Index(shown, "you \u00b7 uncommitted") > strings.Index(shown, "+a note you typed") {
		t.Errorf("your edits are not the first thing the pane shows:\n%s", shown)
	}
	if strings.Index(shown, "+a note you typed") > strings.Index(shown, "+added line") {
		t.Errorf("the author's span comes before your edits:\n%s", shown)
	}
	// Your lines carry line numbers of their own, from git's headers in your diff.
	if !strings.Contains(shown, "1 +a note you typed") {
		t.Errorf("your lines are not numbered:\n%s", shown)
	}
	// Your two lines carry the marker. The context line between them belongs to the file, and the author's
	// span below carries none of it: two markers, not four.
	if n := strings.Count(shown, "\u2190 you"); n != 2 {
		t.Errorf("%d rows carry the marker, want the two lines you changed:\n%s", n, shown)
	}
	if !strings.Contains(shown, "-and one you removed  \u2190 you") {
		t.Errorf("the line you removed is not marked:\n%s", shown)
	}
	if strings.Contains(shown, "a line of the file you did not touch  \u2190 you") {
		t.Errorf("a context line is marked as yours:\n%s", shown)
	}
	// The lines above the first hunk carry a git sign without being a line of the file: `--- a/main.go` is
	// the old path, not a line the reviewer deleted.
	if strings.Contains(shown, "--- a/main.go  \u2190 you") || strings.Contains(shown, "+++ b/main.go  \u2190 you") {
		t.Errorf("git's own metadata is marked as the reviewer's work:\n%s", shown)
	}
}

// The file's own header counts belong to the span, so a reviewer with edits of their own must not
// read a number about the author's work as a tally that includes their typing.
func TestYourEditsDoNotMoveTheHeadersCounts(t *testing.T) {
	m := previewModel(t)
	m.workingFor = func(_ context.Context, _ string) Patch {
		return Patch{Lines: []string{"@@ -1 +1,4 @@", "+one", "+two", "+three", "+four"}, Added: 4, Deleted: 2}
	}
	m = askPreview(t, m)
	if !strings.Contains(ansi.Strip(m.View()), "  +1 \u22120") {
		t.Error("the header no longer shows the span's own counts")
	}
}

// Most files carry no reviewer edits, and the pane should not mention any.
func TestPreviewSaysNothingAboutYourEditsWhenThereAreNone(t *testing.T) {
	m := askPreview(t, previewModel(t))
	if strings.Contains(ansi.Strip(m.View()), "you \u00b7 uncommitted") {
		t.Errorf("the pane claimed edits the reviewer never made:\n%s", ansi.Strip(m.View()))
	}
}

// While the answer about your edits is still in flight, the pane must not say there is nothing to
// show -- that message would be wrong for the length of one git call.
func TestPreviewWaitsForYourEditsBeforeSayingNothingChanged(t *testing.T) {
	m := previewModel(t)
	m.patchFor = func(_ context.Context, _ string) Patch { return Patch{} }

	// Keep the model ensurePreview returned: it is the one that knows which file the pane is on.
	m, cmd := m.ensurePreview()
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("the pane asked for %T, want a batch of fetches", cmd())
	}
	for _, c := range batch {
		if pm, isPatch := c().(previewMsg); isPatch && pm.kind == patchSpan {
			updated, _ := m.Update(pm)
			m = updated.(reviewModel)
		}
	}

	shown := ansi.Strip(m.View())
	if !strings.Contains(shown, "reading your edits") {
		t.Errorf("the pane did not say it was still looking:\n%s", shown)
	}
	if strings.Contains(shown, "no changes in this span") {
		t.Error("it declared nothing changed before the answer about your edits had arrived")
	}
}

// --- the text pane with the reviewer's edits in it ---------------------------

// freshDoc is the file the span created, as the pane gets it: the text at the span's head, which is what
// the reviewer is being asked about.
func freshDoc() Document {
	return Document{Sections: []DocSection{{Lines: []string{
		"package main",
		"",
		"func Fresh() {}",
	}}}}
}

// freshEdit is git's own answer for the same file after the reviewer typed in it and did not commit:
// metadata, one hunk, one line removed and two put in its place.
func freshEdit() Patch {
	return Patch{Lines: []string{
		"diff --git a/fresh.go b/fresh.go",
		"index 1111111..2222222 100644",
		"--- a/fresh.go",
		"+++ b/fresh.go",
		"@@ -1,3 +1,4 @@",
		" package main",
		" ",
		"-func Fresh() {}",
		"+func Fresh() { return nil }",
		"+// reviewer: why?",
	}, Added: 2, Deleted: 1}
}

// The reviewer's lines land where they land: the removed line keeps the number it has in the file under
// review and stands in place of that file's row, the added lines follow it with no number, and none of
// git's patch chrome arrives in a pane that reads the file as a file.
func TestEditedRowsPutTheReviewersLinesWhereTheyLand(t *testing.T) {
	rows := editedDocRows(freshDoc(), freshEdit(), 70, unmarked)
	shown := strings.Join(rowTexts(rows), "\n")
	plain := ansi.Strip(shown)

	for _, chrome := range []string{"diff --git", "index 1111", "@@", "--- a/", "+++ b/"} {
		if strings.Contains(plain, chrome) {
			t.Errorf("the text pane grew patch chrome (%q):\n%s", chrome, plain)
		}
	}
	if n := strings.Count(plain, "package main"); n != 1 {
		t.Errorf("%q appears %d times, want the file's own row once:\n%s", "package main", n, plain)
	}
	if n := strings.Count(plain, "-func Fresh() {}"); n != 1 {
		t.Errorf("the removed line appears %d times, want it once:\n%s", n, plain)
	}
	// It stands in place of the file's own row rather than beside it: one row carries the number 3.
	numbered := 0
	for _, row := range rows {
		if strings.HasPrefix(strings.TrimLeft(ansi.Strip(row.text), " "), "3 ") {
			numbered++
		}
	}
	if numbered != 1 {
		t.Errorf("%d rows are numbered 3, want the removed line alone:\n%s", numbered, plain)
	}

	for _, want := range []string{"-func Fresh() {}", "+func Fresh() { return nil }", "+// reviewer: why?"} {
		if !strings.Contains(plain, want) {
			t.Errorf("the reviewer's line %q is missing:\n%s", want, plain)
		}
	}
	// One marker per reviewer's line: the two added lines and the removed one, and no others.
	if n := strings.Count(plain, "\u2190 you"); n != 3 {
		t.Errorf("%d rows carry the marker, want the reviewer's three lines:\n%s", n, plain)
	}
	// The removed line is a line of the reviewed file, so it carries that file's number.
	if !strings.Contains(plain, "3 -func Fresh() {}") {
		t.Errorf("the removed line is not numbered with the file's own number:\n%s", plain)
	}
	// The added lines are in no file under review, so they are numbered in none: the gutter beside a
	// `+` is empty, where the file's own rows and the removed line carry a number.
	for _, row := range rows {
		if p := ansi.Strip(row.text); strings.HasPrefix(strings.TrimLeft(p, " "), "+") && !strings.HasPrefix(p, "    ") {
			t.Errorf("an added line carries a number, which belongs to a file this pane is not showing:\n%s", plain)
		}
	}
	// And they land at the line they belong to, in the order git wrote them: the line removed, then the
	// lines that replaced it. The pane puts lines where they belong and reorders nothing, which is what
	// keeps it the same patch `d` and `git pair diff` show.
	if strings.Index(plain, "package main") > strings.Index(plain, "-func Fresh()") {
		t.Error("the edit landed above the file's first line")
	}
	if strings.Index(plain, "-func Fresh()") > strings.Index(plain, "+// reviewer: why?") {
		t.Error("the reviewer's lines were reordered away from git's order")
	}
}

// Two hunks in one file both land, and the untouched lines between them are the file's own.
func TestEditedRowsPlaceEveryHunk(t *testing.T) {
	doc := Document{Sections: []DocSection{{Lines: []string{
		"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten",
	}}}}
	work := Patch{Lines: []string{
		"@@ -1,2 +1,2 @@",
		" one",
		"-two",
		"+TWO",
		"@@ -8,2 +8,2 @@",
		" eight",
		"-nine",
		"+NINE",
	}, Added: 2, Deleted: 2}

	plain := ansi.Strip(strings.Join(rowTexts(editedDocRows(doc, work, 60, unmarked)), "\n"))
	// git's order within each hunk: what came out, then what went in.
	order := []string{"one", "-two", "+TWO", "three", "eight", "-nine", "+NINE", "ten"}
	at := -1
	for _, want := range order {
		found := strings.Index(plain, want)
		if found <= at {
			t.Errorf("%q is missing or out of place after offset %d:\n%s", want, at, plain)
			return
		}
		at = found
	}
	if n := strings.Count(plain, "\u2190 you"); n != 4 {
		t.Errorf("%d rows carry the marker, want the reviewer's four lines across both hunks:\n%s", n, plain)
	}
}

// The pane chooses this function only when there are edits, but the function itself is asked about an
// empty patch too, and then it is exactly what the pane drew before: the file, and nothing beside it.
func TestEditedRowsWithNoEditsAreTheFileAlone(t *testing.T) {
	got, want := rowTexts(editedDocRows(freshDoc(), Patch{}, 70, unmarked)), rowTexts(docRows(freshDoc(), 70, unmarked))
	if len(got) != len(want) {
		t.Fatalf("an unedited file draws %d rows, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, ansi.Strip(got[i]), ansi.Strip(want[i]))
		}
	}
	if strings.Contains(ansi.Strip(strings.Join(got, "\n")), "\u2190 you") {
		t.Error("an unedited file carries the reviewer's marker")
	}
}

// A capped file carries less than the patch describes. The merge stops where the pane stops: the rows the
// reviewer can read are the file's own, and the note above them is what still says edits exist.
func TestEditedRowsStopWhereThePaneStops(t *testing.T) {
	capped := Document{Sections: []DocSection{{Lines: []string{"one", "two"}}}, Capped: true}
	work := Patch{Lines: []string{
		"@@ -1,3 +1,3 @@",
		" one",
		"-two",
		"-three",
		"+TWO",
	}, Added: 1, Deleted: 2}

	plain := ansi.Strip(strings.Join(rowTexts(editedDocRows(capped, work, 60, unmarked)), "\n"))
	if !strings.Contains(plain, "one") || !strings.Contains(plain, "two") {
		t.Errorf("the capped file lost its own rows:\n%s", plain)
	}
	if strings.Contains(plain, "-three") {
		t.Errorf("a removed line past what the pane was given was drawn anyway:\n%s", plain)
	}
}

// The marker is drawn inside the column, not past it: the pane wraps what it draws, and a frame the
// terminal wraps for it shifts every row under the break.
func TestTheMarkerDoesNotWidenThePane(t *testing.T) {
	const width = 44
	long := Patch{Lines: []string{
		"@@ -3 +3 @@",
		"-func Fresh() {}",
		"+func Fresh() { return aVeryLongExpression(after: the, reviewer: typed, all: of, this: line) }",
	}, Added: 1, Deleted: 1}

	for _, row := range rowTexts(editedDocRows(freshDoc(), long, width, unmarked)) {
		if w := ansi.StringWidth(row); w > width {
			t.Errorf("a marked row is %d columns wide in a %d-column pane: %q", w, width, ansi.Strip(row))
		}
	}
}

// The marker is the pane's own word, and the search looks for a term in what the reviewer wrote rather
// than in the word beside it: `/you` finds a file about yourself, not every line the reviewer typed.
func TestTheMarkerIsNotWhatTheSearchFinds(t *testing.T) {
	found := rowTexts(editedDocRows(freshDoc(), freshEdit(), 70, marks{term: "you", current: -1}))
	for _, row := range found {
		if strings.Contains(row, sgrUnderline) || strings.Contains(row, sgrReverse) {
			t.Errorf("/you matched the marker rather than the text:\n%s", ansi.Strip(row))
		}
	}
	// And the marker is not decoration the search cannot reach: a term in the reviewer's own line still
	// lands, on a row that carries it.
	for _, row := range editedDocRows(freshDoc(), freshEdit(), 70, marks{term: "nil", current: -1}) {
		if strings.Contains(ansi.Strip(row.text), "+func Fresh() { return nil }") &&
			!strings.Contains(row.text, sgrUnderline) {
			t.Errorf("the search could not find a term inside the reviewer's line: %q", ansi.Strip(row.text))
		}
	}
}

// Your edits are the first thing on screen, and paging counts both sections, so the author's span is still
// reachable at the end of a long edit of your own.
func TestPagingReachesYourEdits(t *testing.T) {
	m := previewModel(t)
	m.workingFor = func(_ context.Context, _ string) Patch {
		lines := make([]string, 0, 40)
		for i := range 40 {
			lines = append(lines, fmt.Sprintf("+yours %02d", i))
		}
		return Patch{Lines: lines, Added: 40}
	}
	m = focusPane(t, askPreview(t, m))
	if !strings.Contains(ansi.Strip(m.View()), "+yours 00") {
		t.Errorf("your edits are not on the first screen:\n%s", ansi.Strip(m.View()))
	}
	for range 30 {
		m = paneKey(t, m, keyMsg(tea.KeyCtrlF))
	}
	if !strings.Contains(ansi.Strip(m.View()), "+yours 39") {
		t.Errorf("paging never reached the end of your edits:\n%s", ansi.Strip(m.View()))
	}
	if !strings.Contains(ansi.Strip(m.View()), "+added line") {
		t.Errorf("paging never reached the author's span below them:\n%s", ansi.Strip(m.View()))
	}
}
