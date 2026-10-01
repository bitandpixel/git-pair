package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"gitpair/internal/span"
)

// The reviewer's own marks are one half of "what have I done in this review"; the other half is the
// file they stopped reading at and wrote a comment into instead. Those changes are uncommitted, so they
// are in no span and carry no sign — the tree had nothing to say about them. These are the tests for the
// row that does: what the session reads from git, what the tree does with it, and what the four
// combinations of "read" and "written" print.

// --- the session ------------------------------------------------------------

// rescan re-reads the list the way an external handoff or a refresh does, so a test that changed the
// working tree can ask what the screen would now say.
func rescan(t *testing.T, sess *Session) {
	t.Helper()
	if err := sess.Rescan(context.Background()); err != nil {
		t.Fatalf("Rescan: %v", err)
	}
}

func dirtyByPath(sess *Session) map[string]File {
	byPath := map[string]File{}
	for _, f := range sess.Files() {
		byPath[f.Path] = f
	}
	return byPath
}

// Staged, unstaged and untouched are one question to the tree — has anything happened to this file that
// HEAD does not have yet — and one answer from git.
func TestAnUncommittedChangeIsOnTheFileRow(t *testing.T) {
	f := treeFixture(t)
	sess := treeSession(t, f)

	f.Append("internal/tui/tui.go", "// reviewer: is this the only caller?\n") // unstaged
	f.Append("main.go", "// reviewer: and here\n")
	f.MustGit("add", "main.go") // staged, and still uncommitted
	rescan(t, sess)

	got := dirtyByPath(sess)
	for _, path := range []string{"internal/tui/tui.go", "main.go"} {
		if !got[path].Dirty {
			t.Errorf("%s does not carry an uncommitted change", path)
		}
	}
	for _, path := range []string{"internal/tui/session.go", "docs/notes.md", "internal/git/git.go"} {
		if got[path].Dirty {
			t.Errorf("%s carries an uncommitted change; nothing was written there", path)
		}
	}
}

// A mark and an edit are about different things: the mark is keyed on the file's diff inside the span,
// and the span's ends are commits, so writing in the file leaves the mark standing. Both answers are on
// one row, and a reviewer who commented and then marked the file read is the common case, not the corner.
func TestAReviewedFileCanAlsoCarryTheReviewersChange(t *testing.T) {
	m, f := treeModel(t)
	m = markFile(t, m, "internal/tui/tui.go")
	if len(markedFiles(m.sess)) != 1 {
		t.Fatalf("the mark did not land: %v", markedFiles(m.sess))
	}

	f.Append("internal/tui/tui.go", "// reviewer: leave a comment in the file itself\n")
	rescan(t, m.sess)
	m.refresh()

	got := dirtyByPath(m.sess)["internal/tui/tui.go"]
	if !got.Reviewed || !got.Dirty {
		t.Fatalf("the row lost one of its two answers: reviewed=%v dirty=%v", got.Reviewed, got.Dirty)
	}
	if text := rowTextOf(t, m, "internal/tui/tui.go"); text != "        ✓ tui.go" {
		t.Errorf("a reviewed file with an uncommitted change reads %q, want the tick it earned", text)
	}
}

// Over history the working tree is not part of what is on screen, so nothing in it belongs to the span:
// the pane drops its working section for the same reason, and the tree shows no marks at all there.
func TestHistoryCarriesNoUncommittedChanges(t *testing.T) {
	f := treeFixture(t)
	sess := treeSession(t, f)
	f.Append("main.go", "// written while a historical span is on screen\n")

	if err := sess.SetSpan(context.Background(),
		span.Selector{Base: span.ChangesetBase(), Head: span.Commit("HEAD")}); err != nil {
		t.Fatalf("SetSpan: %v", err)
	}
	if !sess.Span().Historical() {
		t.Fatalf("the span is %v, want historical", sess.Span())
	}
	for _, file := range sess.Files() {
		if file.Dirty {
			t.Errorf("%s carries an uncommitted change over a historical span", file.Path)
		}
	}
}

// --- the tree ---------------------------------------------------------------

// A folded directory is the row standing in for the rows it hides, so it is the row that has to say
// something is written in there. Unfolded, the children say it themselves and the directory says nothing
// — a mark repeated down a subtree is one more thing to look past to find the row that means it.
func TestAFoldedDirectoryWearsWhatItHides(t *testing.T) {
	m, f := treeModel(t)
	f.Append("internal/tui/tui.go", "// reviewer: a comment\n")
	rescan(t, m.sess)
	m.refresh()

	if got := rowTextOf(t, m, "internal/tui/"); got != "  ▾ ○ tui/" {
		t.Errorf("an open directory over a file with edits reads %q, want no mark of its own", got)
	}
	if got := rowTextOf(t, m, "internal/tui/tui.go"); got != "        ✱ tui.go" {
		t.Errorf("the file with edits reads %q, want the mark for it", got)
	}

	m = pressRune(cursorOnDir(t, m, "internal/tui/"), 'h')
	if got := rowTextOf(t, m, "internal/tui/"); got != "  ▸ ✱ tui/" {
		t.Errorf("the same directory folded reads %q, want it wearing what it hides", got)
	}
	if got := rowTextOf(t, m, "internal/tuition/"); got != "  ▾ ○ tuition/" {
		t.Errorf("a directory this fold left open reads %q, want the mark it always wore", got)
	}
}

// The count after a partial directory's name is what a reviewer folds it to look for, so it survives the
// mark: the gutter says something was written here, the count says how much has been read.
func TestAFoldedDirectoryKeepsItsCountAlongsideTheMark(t *testing.T) {
	m, f := treeModel(t)
	m = markFile(t, m, "internal/tui/session.go")
	f.Append("internal/tui/tui.go", "// reviewer: a comment\n")
	rescan(t, m.sess)
	m = pressRune(cursorOnDir(t, m, "internal/tui/"), 'h')

	if got := rowTextOf(t, m, "internal/tui/"); got != "  ▸ ✱ tui/  1/2" {
		t.Errorf("a folded partial directory with edits reads %q, want the mark and the count", got)
	}
}

// A subtree that is read all the way through and written in all the way through keeps the tick it earned:
// the tick is the answer the counter is made of, and the name says who wrote in it.
func TestAFoldedDirectoryThatIsReadAndWrittenKeepsItsTick(t *testing.T) {
	m, f := treeModel(t)
	m = press(cursorOnDir(t, m, "internal/tui/"), tea.KeySpace) // both files reviewed
	f.Append("internal/tui/tui.go", "// reviewer: a comment\n")
	rescan(t, m.sess)
	m = pressRune(cursorOnDir(t, m, "internal/tui/"), 'h')

	if got := rowTextOf(t, m, "internal/tui/"); got != "  ▸ ✓ tui/" {
		t.Errorf("a reviewed directory with edits reads %q, want the tick", got)
	}
	if got := nameStyle(rowAt(t, m, "internal/tui/")); got.GetForeground() != lipgloss.Color("13") {
		t.Error("a reviewed directory with edits does not name itself as the reviewer's")
	}
}

// --- the readings -----------------------------------------------------------

// The four readings a file row can give, at once: read and untouched, read and written, untouched and
// unread, and written and unread. The gutter keeps the review answer wherever it can carry it, and the
// name is what says whose change it is.
func TestTheFourReadingsOfAFileRow(t *testing.T) {
	m, f := treeModel(t)
	m = markFile(t, m, "docs/notes.md")                             // read, untouched
	m = markFile(t, m, "internal/git/git.go")                       // read, and about to be written
	f.Append("internal/git/git.go", "// reviewer: a comment\n")     // read and written
	f.Append("internal/tuition/why.go", "// reviewer: a comment\n") // written, unread
	rescan(t, m.sess)
	m.refresh()

	for _, want := range []struct{ path, reads string }{
		{"main.go", "○ main.go"},
		{"docs/notes.md", "    ✓ notes.md"},
		{"internal/git/git.go", "        ✓ git.go"},
		{"internal/tuition/why.go", "        ✱ why.go"},
	} {
		if got := rowTextOf(t, m, want.path); got != want.reads {
			t.Errorf("%s reads %q, want %q", want.path, got, want.reads)
		}
	}

	// The name of a file that is both read and written is the reviewer's own, in colour and in weight;
	// the names of the three rows with nothing written in them are not.
	for _, want := range []struct {
		path   string
		styled bool
	}{
		{"internal/git/git.go", true},     // read and written
		{"internal/tuition/why.go", true}, // written, unread
		{"main.go", false},                // neither
		{"docs/notes.md", false},          // read only
	} {
		got := nameStyle(rowAt(t, m, want.path))
		if styled := got.GetForeground() == lipgloss.Color("13") && got.GetBold(); styled != want.styled {
			t.Errorf("%s: name styled as the reviewer's = %v, want %v", want.path, styled, want.styled)
		}
	}
}

// A file the span deleted and the reviewer then changed has two claims on the name: faint, because the
// subject is gone, and magenta, because the change is the reviewer's. The change wins; what the span did
// is still on the row, in the sign after the name. What a unit test can check is the choice, since
// lipgloss draws no colour for a terminal it does not believe in -- that the codes reach a real one is
// the pty walkthrough's claim.
func TestTheWritersMarkOutranksTheDeletedFilesFaint(t *testing.T) {
	if got := nameStyle(row{dirty: true, change: ChangeDeleted}); got.GetForeground() != lipgloss.Color("13") {
		t.Errorf("a deleted file with an uncommitted change is not named as the reviewer's: %v", got)
	}
	if got := nameStyle(row{change: ChangeDeleted}); !got.GetFaint() {
		t.Error("a deleted file with nothing written in it does not go faint the way it used to")
	}
	if got := nameStyle(row{}); got.GetForeground() != (lipgloss.NoColor{}) || got.GetBold() || got.GetFaint() {
		t.Errorf("an ordinary file's name wears styling (%v); only a mark or a deletion earns one", got)
	}
	if !styleDirtyName.GetBold() {
		t.Error("the reviewer's name style carries no weight, so a terminal with no colour to give cannot see it")
	}
	if got, want := styleDirtyMark.GetForeground(), lipgloss.Color("13"); got != want {
		t.Errorf("the gutter mark is %q, want %q, the same colour as the name", got, want)
	}
	if styleDirtyMark.GetBold() {
		t.Error("the gutter mark is bold: the glyph carries that row's difference, and the colour only joins it")
	}
}

// A historical span draws no gutter at all, so it must not draw the reviewer's name either.
func TestHistoryDrawsNeitherMark(t *testing.T) {
	m, f := treeModel(t)
	f.Append("main.go", "// reviewer: a comment\n")
	if err := m.sess.SetSpan(m.ctx, span.Selector{Base: span.ChangesetBase(), Head: span.Commit("HEAD")}); err != nil {
		t.Fatalf("SetSpan: %v", err)
	}
	m.refresh()

	if got := rowTextOf(t, m, "main.go"); got != "main.go" {
		t.Errorf("a row over a historical span reads %q, want the name alone", got)
	}
}

// --- refresh ----------------------------------------------------------------

// `r` is the key for "look again". The refs are the half it used to look at; a reviewer can write into
// the working tree from another window, and the key that asks the question again has to ask it of both
// halves — while still saying what it always said about the refs.
func TestRefreshReadsWhatAnotherWindowWrote(t *testing.T) {
	m, f := treeModel(t)
	if strings.Contains(rowTextOf(t, m, "main.go"), "✱") {
		t.Fatal("the fixture starts with an uncommitted change")
	}

	f.Append("main.go", "// written in another window\n")
	m = pressRune(m, 'r')

	if !strings.Contains(m.status, "nothing has moved") {
		t.Errorf("r said %q, want it to say what it says when no ref has moved", m.status)
	}
	if got := rowTextOf(t, m, "main.go"); !strings.Contains(got, "✱") {
		t.Errorf("r did not pick up the edit: %q", got)
	}
}

// --- the indent -------------------------------------------------------------

// A directory spends two cells on its fold arrow and a file does not, which used to put a nested
// directory's name two cells right of the names beside it. The arrow now comes out of the indent, so a
// depth is a column: at any depth below the top, a directory and the files beside it name themselves in
// one column. A level still costs four cells, so a child's name sits four cells right of the row holding
// it -- except directly under the top of the tree, where a directory has no indent to spend its arrow on
// and its children land two cells right of its name.
func TestOneDepthIsOneColumn(t *testing.T) {
	m, _ := treeModel(t)

	// `docs/plans/active/` and `docs/notes.md` are both one level under `docs/`; `internal/tui/` is one
	// level under `internal/`. Three directories and a file, one depth, one column.
	at := nameStart(t, m, "docs/notes.md")
	for _, dir := range []string{"docs/plans/active/", "internal/tui/", "internal/tuition/"} {
		if got := nameStart(t, m, dir); got != at {
			t.Errorf("%s names itself at %d and the file beside it at %d; want one column per depth", dir, got, at)
		}
	}
	// One level deeper is four cells deeper, for a file and a directory alike.
	if got := nameStart(t, m, "internal/git/git.go"); got != at+4 {
		t.Errorf("a row one level deeper starts at %d, want %d", got, at+4)
	}
	// At the top of the tree there is no indent to take the arrow out of: the files start at the left
	// edge of the gutter and the directories two cells right of them, which is where they always started.
	if file, dir := nameStart(t, m, "main.go"), nameStart(t, m, "docs/"); dir-file != 2 {
		t.Errorf("a top-level file names itself at %d and a top-level directory at %d; want two cells apart",
			file, dir)
	}
	// The rows the fold arrow moved are still under their directory, not beside it.
	if parent, child := nameStart(t, m, "docs/"), nameStart(t, m, "docs/notes.md"); child <= parent {
		t.Errorf("`docs/` names itself at %d and its child at %d; want the child further right", parent, child)
	}
}

// markFile puts the cursor on a file row and marks it, which is what a reviewer's Space does.
func markFile(t *testing.T, m reviewModel, path string) reviewModel {
	t.Helper()
	at, _ := cursorOnPath(t, m, path)
	return press(at, tea.KeySpace)
}

// rowTextOf is one row as a reviewer sees it, colours out.
func rowTextOf(t *testing.T, m reviewModel, path string) string {
	t.Helper()
	return ansiCodes.ReplaceAllString(rawRowText(t, m, path), "")
}

// rowAt is the row itself, for a test that asks what a row says rather than what it prints.
func rowAt(t *testing.T, m reviewModel, path string) row {
	t.Helper()
	i := m.rowIndex(rowFile, path)
	if i < 0 {
		i = m.rowIndex(rowDir, path)
	}
	if i < 0 {
		t.Fatalf("no row for %s:\n%s", path, rowList(m))
	}
	return m.rows[i]
}

// rawRowText is one row with the escapes left in.
func rawRowText(t *testing.T, m reviewModel, path string) string {
	t.Helper()
	return m.rowText(rowAt(t, m, path))
}

// nameStart is the cell a row's name begins at, which is what the indent is for. It measures cells, not
// bytes: the fold arrow and the mark gutter are three bytes each.
func nameStart(t *testing.T, m reviewModel, path string) int {
	t.Helper()
	r := rowAt(t, m, path)
	text := rowTextOf(t, m, path)
	i := strings.Index(text, r.name)
	if i < 0 {
		t.Fatalf("the row for %s is %q, which does not contain its own name %q", path, text, r.name)
	}
	return lipgloss.Width(text[:i])
}
