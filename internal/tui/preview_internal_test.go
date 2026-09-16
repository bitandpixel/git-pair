package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
