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
	return m
}

// askPreview moves the pane onto the cursor and delivers whatever git has to say about it. A
// patch already in the cache needs no fetch, which is the point of the cache.
func askPreview(t *testing.T, m reviewModel) reviewModel {
	t.Helper()
	m, cmd := m.ensurePreview()
	if cmd == nil {
		if m.previewPath == "" {
			t.Fatal("the pane has nothing to show and asked for nothing")
		}
		return m
	}
	updated, _ := m.Update(cmd())
	rm, ok := updated.(reviewModel)
	if !ok {
		t.Fatalf("preview answer produced %T", updated)
	}
	return rm
}

func TestPreviewFollowsTheCursor(t *testing.T) {
	m := previewModel(t)
	at := indexOfNameBySuffix(t, m, ".go")
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

// The pane is a wide-terminal luxury. Below the threshold the session is exactly what it was
// before the preview existed, and `p` in a narrow terminal says why nothing happened.
func TestPreviewOnlyAppearsWhenThereIsRoom(t *testing.T) {
	narrow := previewModel(t)
	narrow.width = 72
	if narrow.paneWidth() != 0 {
		t.Errorf("a %d-column terminal gets a %d-column pane", narrow.width, narrow.paneWidth())
	}
	if strings.Contains(narrow.View(), "│") {
		t.Error("the narrow layout draws a divider anyway")
	}

	wide := previewModel(t)
	pane := wide.paneWidth()
	list := wide.width - pane - previewGap
	if pane+list+previewGap != wide.width {
		t.Errorf("pane %d + list %d + gap %d != the terminal's %d columns", pane, list, previewGap, wide.width)
	}
	if list < 40 {
		t.Errorf("a %d-column terminal leaves the list %d columns, which cannot show a path", wide.width, list)
	}
	if !strings.Contains(wide.View(), "│") {
		t.Error("a wide terminal draws no preview")
	}

	off := previewModel(t)
	off.previewOn = false
	if strings.Contains(off.View(), "│") {
		t.Error("`p` off did not remove the pane")
	}

	// Toggling on in a terminal that cannot fit it must explain, not stay silent.
	crowded := previewModel(t)
	crowded.width = 60
	crowded.previewOn = false // `p` turns it on, which is the case that cannot fit
	updated, _ := crowded.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	crowded = updated.(reviewModel)
	if !strings.Contains(crowded.status, fmt.Sprint(previewMinWidth)) || !strings.Contains(crowded.status, "60") {
		t.Errorf("toggling in a 60-column terminal said %q, want the width it needs", crowded.status)
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
	m = askPreview(t, m)
	body := m.previewBodyRows()
	max := 60 - body

	for range max + 10 {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
		m = updated.(reviewModel)
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
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
		m = updated.(reviewModel)
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
	m.cursor = indexOf(t, m, rowThread)
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
	for _, idx := range []int{0, 1, 0, 2, 1, 0} {
		m.cursor = idx
		m = askPreview(t, m)
	}
	for path, n := range asks {
		if n != 1 {
			t.Errorf("%s was fetched %d times, want once", path, n)
		}
	}
	if len(asks) != 3 {
		t.Errorf("fetched %d paths, want the 3 files visited", len(asks))
	}

	// Changing the span invalidates every answer: the patches describe a span that is gone.
	m.forgetPatches()
	m.cursor = 0
	m = askPreview(t, m)
	if asks[m.rows[0].path] != 2 {
		t.Error("forgetting the patches did not make the next visit ask again")
	}
}

// A short terminal has no room for a column of diff either, and the two excuses are different:
// a reviewer with a 140x10 window is not being told about columns.
func TestPreviewNeedsRowsAsWellAsColumns(t *testing.T) {
	short := previewModel(t)
	short.height = 10
	if short.paneWidth() != 0 {
		t.Errorf("a %d-row terminal gets a %d-column pane", short.height, short.paneWidth())
	}

	short.previewOn = false
	updated, _ := short.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	short = updated.(reviewModel)
	if !strings.Contains(short.status, fmt.Sprint(previewMinHeight)) || !strings.Contains(short.status, "10") {
		t.Errorf("toggling in a 10-row terminal said %q, want the rows it needs", short.status)
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

	rows := strings.Split(strings.TrimSuffix(joinColumns(m.listBlock(), m.previewLines()), "\n"), "\n")
	if len(rows) < 5 {
		t.Fatalf("the block is %d rows, want a list with a pane beside it", len(rows))
	}
	divider := dividerColumn(rows[0])
	if divider < 0 {
		t.Fatal("no divider to hang the layout on")
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
		if at := dividerColumn(row); at != divider {
			t.Errorf("row %d puts the divider at column %d, not %d: %q", i, at, divider, row)
		}
	}
	if widest != m.width {
		t.Errorf("the widest row is %d columns in a %d-column terminal: pane %d + list %d + gap %d do not add up",
			widest, m.width, m.paneWidth(), m.listWidth(), previewGap)
	}
}

func dividerColumn(row string) int {
	i := strings.Index(row, "\u2502")
	if i < 0 {
		return -1
	}
	return lipgloss.Width(row[:i])
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

// A diff line wider than the pane is broken rather than cut, and the break must not lose anything:
// the whitespace in a diff is the code.
func TestPreviewWrapsWithoutLosingCharacters(t *testing.T) {
	long := "+\t\treturn service.Lock(ctx, r.URL.Path, tenant.ID, \"already held\")"
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

	rows := strings.Join(previewBody(patch, 60), "\n")
	if !strings.Contains(ansi.Strip(rows), "10  func a() {") {
		t.Errorf("a context line does not carry its number on the side being reviewed:\n%s", ansi.Strip(rows))
	}
	if !strings.Contains(ansi.Strip(rows), "11 -\told := 1") || !strings.Contains(ansi.Strip(rows), "11 +\tnew := 2") {
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
