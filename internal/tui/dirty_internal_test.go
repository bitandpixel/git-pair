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

// --- the files the span never touched ---------------------------------------

// A reviewer who stops reading and writes into a file the changeset never touched has made a change this
// screen is the only place they will see it. It is in no span, so it is in no `diff --name-status`, and a
// change with no row is a change nobody comes back for. git's `status` says the path is uncommitted, so the
// tree holds a place for it beside the files the span did change, wearing the same mark for the reviewer's
// own hand.
func TestAFileOnlyTheReviewerChangedGetsARow(t *testing.T) {
	m, f := treeModel(t)
	if m.rowIndex(rowFile, "notes/plan.md") >= 0 {
		t.Fatal("the file the changeset never touched is on the tree before anyone has written in it")
	}

	f.Append("notes/plan.md", "\nreviewer: which lock is this about?\n")
	rescan(t, m.sess)
	m.refresh()

	if got := rowTextOf(t, m, "notes/plan.md"); got != "    ✱ plan.md" {
		t.Errorf("the reviewer's own file reads %q, want the mark for it", got)
	}
	if got := nameStyle(rowAt(t, m, "notes/plan.md")); got.GetForeground() != lipgloss.Color("13") {
		t.Error("the reviewer's own file does not name itself as the reviewer's")
	}
	// No sign: `Change` is git's answer about the span, and the honest answer about this file is that the
	// span did nothing to it.
	if got := rowAt(t, m, "notes/plan.md").change.Sign(); got != "" {
		t.Errorf("the row wears sign %q, want none: the span did not create, delete or move this file", got)
	}
	// The directory above it is on the tree for the same reason, and says nothing about what has been read:
	// every file under it is one this span has no patch for.
	if got := rowTextOf(t, m, "notes/"); got != "▾   notes/" {
		t.Errorf("a directory holding only the reviewer's files reads %q, want no review mark of its own", got)
	}
}

// A file the reviewer created is the same case as a file the reviewer edited, with one thing added: git has
// never been told about the path, which is why the pane has to ask git the other question about it (see
// Session.WorkingPatch) and why Enter does not hand it to a difftool with no left side.
func TestAFileTheReviewerCreatedGetsARow(t *testing.T) {
	m, f := treeModel(t)
	f.Write("scratch/idea.md", "a note of the reviewer's own\n")
	rescan(t, m.sess)
	m.refresh()

	if got := rowTextOf(t, m, "scratch/idea.md"); got != "    ✱ idea.md" {
		t.Errorf("a file the reviewer created reads %q, want the mark for it", got)
	}
	file, ok := m.sess.fileAt("scratch/idea.md")
	if !ok || !file.OutsideSpan || !file.Untracked {
		t.Errorf("the list says %+v, want a working-tree file git has never tracked", file)
	}
	// The file the reviewer *edited* is tracked, and its comparison — the revision under review against the
	// bytes on disk — is worth opening. The one git has never seen has no other side to open.
	if got := activateBy(rowAt(t, m, "scratch/idea.md")); got != actionEdit {
		t.Errorf("Enter on a file git has never tracked = %d, want the editor", got)
	}
}

// The counter counts the span, which is what makes it mean "how much of this changeset I have read". The
// rows the working tree put on the list are in neither half of that number: there is no patch of theirs in
// the span to have read, and a file that cannot be read is not something left to do.
func TestTheCounterCountsTheSpanNotTheWorkingTree(t *testing.T) {
	m, f := treeModel(t)
	_, before := m.sess.Count()

	f.Append("notes/plan.md", "reviewer: a question\n")
	f.Write("scratch/idea.md", "a note of the reviewer's own\n")
	f.Write("internal/tui/scratch.md", "reviewer: a note to self\n")
	rescan(t, m.sess)
	m.refresh()

	if reviewed, total := m.sess.Count(); reviewed != 0 || total != before {
		t.Errorf("the counter reads %d/%d after three working-tree files, want 0/%d", reviewed, total, before)
	}
	if got := rowAt(t, m, "notes/").total; got != 0 {
		t.Errorf("the reviewer's own directory counts %d files, want 0", got)
	}
	// A directory the span changed keeps counting the files the span changed, with the reviewer's own file
	// sitting inside it uncounted.
	if got := rowAt(t, m, "internal/tui/"); got.total != 2 || got.marked != 0 {
		t.Errorf("a directory the span changed reads %d/%d, want 0 of the two it touched", got.marked, got.total)
	}
}

// Marking a directory marks what the span changed under it. The file the reviewer left lying in the middle of
// the package is not part of that: one keystroke says which of the two it means by how many files it moves,
// and the reviewer's own row keeps saying whose change it is.
func TestMarkingADirectorySkipsTheReviewersOwnFiles(t *testing.T) {
	m, f := treeModel(t)
	f.Write("internal/tui/scratch.md", "reviewer: a note to self\n")
	rescan(t, m.sess)
	m = press(cursorOnDir(t, m, "internal/tui/"), tea.KeySpace)

	if got := markedFiles(m.sess); len(got) != 2 {
		t.Errorf("marking the package marked %v, want its two files alone", got)
	}
	if got := rowTextOf(t, m, "internal/tui/scratch.md"); got != "        ✱ scratch.md" {
		t.Errorf("the reviewer's file inside a marked package reads %q, want the mark for it", got)
	}
	if got := rowTextOf(t, m, "internal/tui/"); got != "  ▾ ✓ tui/" {
		t.Errorf("a package read all the way through loses its tick to a file outside the span: %q", got)
	}
}

// `Space` marks what the span changed. A row with no patch in the span has nothing to have read, so the key
// says so rather than ticking a row the counter will never count — a tick there would be a claim about a
// diff that does not exist.
func TestSpaceRefusesAFileTheSpanNeverTouched(t *testing.T) {
	m, f := treeModel(t)
	f.Append("notes/plan.md", "reviewer: a question\n")
	f.Write("scratch/idea.md", "a note of the reviewer's own\n")
	rescan(t, m.sess)
	m.refresh()

	at, _ := cursorOnPath(t, m, "notes/plan.md")

	got := press(at, tea.KeySpace)
	if got.status == "" || got.statusKind != notifSticky {
		t.Fatalf("space on the reviewer's own file said %q (kind=%d), want a refusal that waits",
			got.status, got.statusKind)
	}
	if !strings.Contains(got.status, "working tree") {
		t.Errorf("the refusal says %q, want the reason and where the change is", got.status)
	}
	if marked := markedFiles(got.sess); len(marked) != 0 {
		t.Errorf("space marked %v; the span has no patch of either file", marked)
	}

	// The same refusal belongs to a directory that exists only because of what the reviewer wrote under it:
	// the keystroke would set nothing and say something.
	got = press(cursorOnDir(t, m, "scratch/"), tea.KeySpace)
	if got.status == "" || got.statusKind != notifSticky {
		t.Fatalf("space on the reviewer's own directory said %q (kind=%d), want a refusal that waits",
			got.status, got.statusKind)
	}
	if !strings.Contains(got.status, "scratch/") {
		t.Errorf("the refusal says %q, want the directory it refused", got.status)
	}
}

// Over history the working tree is not part of what is on screen, so nothing the reviewer has written belongs
// to the span on show — and a row here would be an invitation to mark, edit and submit against a span that
// refuses all three.
func TestHistoryListsNoFilesOfTheReviewersOwn(t *testing.T) {
	m, f := treeModel(t)
	f.Append("notes/plan.md", "reviewer: a question\n")
	f.Write("scratch/idea.md", "a note of the reviewer's own\n")
	if err := m.sess.SetSpan(m.ctx, span.Selector{Base: span.ChangesetBase(), Head: span.Commit("HEAD")}); err != nil {
		t.Fatalf("SetSpan: %v", err)
	}
	m.refresh()

	for _, path := range []string{"notes/plan.md", "scratch/idea.md"} {
		if m.rowIndex(rowFile, path) >= 0 {
			t.Errorf("%s is on the tree over a historical span; the working tree is not in it", path)
		}
	}
	if _, total := m.sess.Count(); total == 0 {
		t.Error("the historical span's own files left the list")
	}
}

// The row exists because of what is on disk, so the pane has to be able to show what is on disk. git compares
// only what it tracks, so `git diff <rev> -- <path>` — the call behind every other patch on the screen —
// prints nothing at all for a file the reviewer created. This is the answer the pane of that row shows
// instead: the file's own lines, as the addition git would have printed had it ever been told about the path.
func TestThePaneShowsAFileGitNeverTracked(t *testing.T) {
	f := treeFixture(t)
	sess := treeSession(t, f)
	f.Write("scratch/idea.md", "first line\nsecond line\n")
	rescan(t, sess)

	if _, ok := sess.fileAt("scratch/idea.md"); !ok {
		t.Fatal("a file the reviewer created is not on the list")
	}
	got := sess.WorkingPatch(context.Background(), "scratch/idea.md")
	if got.Err != "" {
		t.Fatalf("the patch of the reviewer's own new file failed: %s", got.Err)
	}
	if len(got.Lines) == 0 {
		t.Fatal("the pane would show nothing about a file that exists because of what is on disk")
	}
	if !strings.Contains(strings.Join(got.Lines, "\n"), "first line") {
		t.Errorf("the patch does not carry the file's own bytes:\n%s", strings.Join(got.Lines, "\n"))
	}
	if got.Added != 2 || got.Deleted != 0 {
		t.Errorf("the patch counts +%d −%d, want +2 −0", got.Added, got.Deleted)
	}

	// A file git *does* know about keeps the comparison it has always had: the revision under review against
	// the bytes on disk, and not the whole file as an addition.
	f.Append("notes/plan.md", "reviewer: a question\n")
	rescan(t, sess)
	got = sess.WorkingPatch(context.Background(), "notes/plan.md")
	if got.Err != "" {
		t.Fatalf("the patch of the reviewer's edit failed: %s", got.Err)
	}
	if got.Added != 1 || got.Deleted != 0 {
		t.Errorf("a reviewer's one-line edit counts +%d −%d, want +1 −0", got.Added, got.Deleted)
	}
}

// The row's own Enter follows the same split the pane's bar says out loud. The file the reviewer created has
// no left side anywhere git keeps, so a difftool handed it opens a window on nothing and the file itself is
// the thing to read. The file the reviewer *edited* has a comparison worth opening: the revision under review
// against the bytes on disk, which is the reviewer's own typing and nothing else.
func TestEnterOnTheReviewersOwnFiles(t *testing.T) {
	m, f := treeModel(t)
	f.Append("notes/plan.md", "reviewer: a question\n")
	f.Write("scratch/idea.md", "a note of the reviewer's own\n")
	rescan(t, m.sess)
	m.refresh()

	created, _ := cursorOnPath(t, m, "scratch/idea.md")
	got := press(created, tea.KeyEnter)
	if !strings.Contains(got.pendingNote, "opened in the editor") {
		t.Errorf("Enter on a file git has never tracked did not reach the editor: note %q", got.pendingNote)
	}

	edited, _ := cursorOnPath(t, m, "notes/plan.md")
	got = press(edited, tea.KeyEnter)
	if got.pendingNote != "" {
		t.Errorf("Enter on a file the reviewer edited went to the editor rather than the difftool: note %q",
			got.pendingNote)
	}
}

// A directory the reviewer created files under has nothing for git to print: it compares only what it tracks,
// so a pathspec over a subtree of new files is an empty answer. The tree says the files are there, so the pane
// says why it cannot show them as a diff rather than leaving a blank column beside the rows that do.
//
// The file itself is the other half: git asked the way a new file is always asked, it prints the whole thing.
func TestThePaneOfTheReviewersOwnNewFiles(t *testing.T) {
	m, f := treeModel(t)
	m.previewOn = true
	f.Write("scratch/idea.md", "a note of the reviewer's own\n")
	rescan(t, m.sess)
	m.refresh()

	dir := askPreview(t, cursorOnDir(t, m, "scratch/"))
	if got := dir.previewNotice(); got != "new files git has not been told about" {
		t.Errorf("the pane of a directory of new files says %q, want the reason it has no diff", got)
	}

	at, _ := cursorOnPath(t, dir, "scratch/idea.md")
	file := askPreview(t, at)
	if got := file.previewNotice(); got != "" {
		t.Fatalf("the pane of the reviewer's new file says %q, want the file itself", got)
	}
	body := ansiCodes.ReplaceAllString(strings.Join(rowTexts(file.previewContent(unmarked)), "\n"), "")
	if !strings.Contains(body, "a note of the reviewer's own") {
		t.Errorf("the pane of the reviewer's new file does not carry its bytes:\n%s", body)
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
