package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The changeset box's rows are documents rather than files: ABOUT.md and the threads are prose the
// reviewer is being asked to read, and a diff of a document against nothing shows every line as added,
// which says nothing about what it says. So the pane shows their text -- and it follows the box's cursor
// there the way it has always followed the tree's, because the pane is the place on this screen for
// reading text.

// aboutText and the threads are what the fixture's changeset contains. They are written as they would read
// in a real changeset, because the point of these tests is that the reviewer can read them here.
var aboutText = []string{
	"# booking transaction",
	"",
	"The lock moves into the store, where every caller already reaches it.",
	"Two threads discuss the naming.",
}

var threadSections = []DocSection{
	{Name: "locking.md", Lines: []string{"# Locking", "", "Why inside the store rather than beside it?"}},
	{Name: "naming.md", Lines: []string{"# Naming", "", "Why not call it reserve?"}},
}

// docModel is the preview fixture with the pane, a file in the span to come back to, and a changeset whose
// documents say what the tests then look for.
func docModel(t *testing.T) reviewModel {
	t.Helper()
	m := previewModel(t)
	m.docFor = func(_ context.Context, path string) Document {
		switch path {
		case m.sess.AboutPath():
			return Document{Sections: []DocSection{{Lines: aboutText}}}
		case m.sess.Changeset().Dir:
			return Document{Sections: threadSections}
		case "thread-00.md":
			return Document{Sections: []DocSection{{Lines: threadSections[0].Lines}}}
		}
		// What the Session says for a document that has not been written yet.
		return Document{Err: path + " does not exist yet"}
	}
	return askPreview(t, m)
}

// docView is what the pane is showing, with the styling stripped: these tests are about which text and in
// what order, not about how it is painted.
func docView(m reviewModel) string {
	return ansi.Strip(strings.Join(m.previewLines(), "\n"))
}

// TestThePaneShowsADocumentsOwnText is the change itself: ABOUT.md highlighted in the box, and ABOUT.md on
// screen -- not a diff of it, whose only information would be that every line is new.
func TestThePaneShowsADocumentsOwnText(t *testing.T) {
	m := boxTo(t, docModel(t), rowAbout)
	m = askPreview(t, m)

	view := docView(m)
	for _, want := range []string{"The lock moves into the store", "Two threads discuss the naming"} {
		if !strings.Contains(view, want) {
			t.Errorf("the pane does not show %q:\n%s", want, view)
		}
	}
	// A diff would say so with its own marks. Nothing here should.
	for _, absent := range []string{"+++", "---", "@@", "+added line"} {
		if strings.Contains(view, absent) {
			t.Errorf("the pane is showing a diff of the document (%q):\n%s", absent, view)
		}
	}
	// The file's own line numbers, which is how "the third line of ABOUT.md" gets said.
	if !strings.Contains(view, "1 # booking transaction") {
		t.Errorf("the document is not numbered from its first line:\n%s", view)
	}
	if !strings.Contains(view, "4 Two threads discuss") {
		t.Errorf("a blank line did not take a number of its own:\n%s", view)
	}
}

// TestThePaneFollowsTheCursorIntoTheBox: the pane is the row the keys are standing on, in both halves of the
// list column. `a` has always moved the cursor and the keys to ABOUT.md; the pane is where the reviewer
// finds out whether the changeset says what they need it to.
func TestThePaneFollowsTheCursorIntoTheBox(t *testing.T) {
	m := docModel(t)
	if !strings.Contains(docView(m), "+added line") {
		t.Fatalf("the fixture does not start on a diff:\n%s", docView(m))
	}

	m = paneKey(t, m, runeKey('a'))
	if want := m.sess.AboutPath(); m.previewPath != want {
		t.Errorf("after `a` the pane shows %q, want %q", m.previewPath, want)
	}
	if !strings.Contains(docView(m), "The lock moves into the store") {
		t.Errorf("`a` did not bring ABOUT.md into the pane:\n%s", docView(m))
	}

	m = paneKey(t, m, runeKey('t'))
	if want := m.sess.Changeset().Dir; m.previewPath != want {
		t.Errorf("after `t` the pane shows %q, want the threads at %q", m.previewPath, want)
	}
	// And the tree's file is one key away, because its cursor never moved.
	m = paneKey(t, m, runeKey('f'))
	if !strings.Contains(docView(m), "+added line") {
		t.Errorf("back at the tree the pane is not showing the diff:\n%s", docView(m))
	}
}

// TestTheThreadsHeadingIsTheWholeConversation: standing on the heading and reading the threads is one move.
// Each is named above its own text, because text with nothing saying where it came from reads as one file.
func TestTheThreadsHeadingIsTheWholeConversation(t *testing.T) {
	m := askPreview(t, boxTo(t, docModel(t), rowThreadsHead))

	view := docView(m)
	for _, want := range []string{"── locking.md", "Why inside the store", "── naming.md", "Why not call it reserve?"} {
		if !strings.Contains(view, want) {
			t.Errorf("the threads do not read as named sections (%q):\n%s", want, view)
		}
	}
	if strings.Index(view, "Why inside the store") > strings.Index(view, "── naming.md") {
		t.Errorf("the threads are out of order:\n%s", view)
	}
	if !strings.Contains(view, "2 threads") {
		t.Errorf("the header does not say how many threads this is:\n%s", view)
	}
}

// A thread's own row is one file, so it carries no caption -- the header already names it.
func TestAThreadRowShowsJustThatThread(t *testing.T) {
	m := askPreview(t, boxTo(t, withThreads(t, docModel(t), 1), rowThread))

	view := docView(m)
	if !strings.Contains(view, "Why inside the store") {
		t.Errorf("the thread's own text is not on screen:\n%s", view)
	}
	if strings.Contains(view, "Why not call it reserve?") {
		t.Errorf("a thread's row showed another thread too:\n%s", view)
	}
	if strings.Contains(view, "── thread-00.md") {
		t.Errorf("a single thread is captioned as though it were a list:\n%s", view)
	}
}

// "There is no ABOUT.md yet" is the answer a reviewer looking at that row wants, and the box says it in the
// row's own note -- so the pane should agree rather than show an empty column.
func TestADocumentThatIsNotThereSaysSo(t *testing.T) {
	m := docModel(t)
	m.docFor = func(_ context.Context, path string) Document {
		return Document{Err: path + " does not exist yet"}
	}
	m = askPreview(t, boxTo(t, m, rowAbout))
	if view := docView(m); !strings.Contains(view, "does not exist yet") {
		t.Errorf("the pane has nothing to say about a document that is not there:\n%s", view)
	}
}

// TestADocumentsHeaderCountsWhatItHas: the pane's header carries git's counts for a diff, and a document has
// no additions and deletions to report. Leaving the space blank would invite reading the absence as a zero.
func TestADocumentsHeaderCountsWhatItHas(t *testing.T) {
	m := askPreview(t, boxTo(t, docModel(t), rowAbout))
	view := docView(m)
	if !strings.Contains(view, "ABOUT.md") {
		t.Errorf("the header does not name the file:\n%s", view)
	}
	if !strings.Contains(view, "4 lines") {
		t.Errorf("the header does not count the lines:\n%s", view)
	}
	if strings.Contains(view, "+1 −0") {
		t.Errorf("the header shows the diff's counts for a document:\n%s", view)
	}
}

// The search is the pane's, not the diff's: the name a reviewer is chasing through a changeset is as likely
// to be in the prose as in the code.
func TestADocumentIsSearchedTheSameWay(t *testing.T) {
	m := focusPane(t, askPreview(t, boxTo(t, docModel(t), rowAbout)))
	m = paneKey(t, m, runeKey('/'))
	for _, r := range "store" {
		m = paneKey(t, m, runeKey(r))
	}
	m = paneKey(t, m, keyMsg(tea.KeyEnter))

	if m.previewMatch < 0 {
		t.Fatal("the search found nothing in a document that says the word")
	}
	if want := 2; m.previewMatch != want { // "The lock moves into the store" is the file's third line
		t.Errorf("the match is row %d, want %d", m.previewMatch, want)
	}
	var marked bool
	for _, row := range m.previewLines() {
		if strings.Contains(row, sgrReverse) {
			marked = true
		}
	}
	if !marked {
		t.Error("the match in the document is not marked")
	}
}

// The overlay is the narrow terminal's only form of the pane, and a changeset's documents are read on a
// narrow terminal too -- `p` is asked for from the box as much as from the tree.
func TestTheOverlayShowsTheDocumentToo(t *testing.T) {
	m := docModel(t)
	m.width, m.height = 80, 20
	m.previewOn = false
	m = boxTo(t, m, rowAbout)

	updated, cmd := m.Update(runeKey('p'))
	rm, ok := updated.(reviewModel)
	if !ok {
		t.Fatalf("`p` produced %T", updated)
	}
	if rm.mode != modePreview {
		t.Fatalf("`p` from the box gave mode %v, want the overlay", rm.mode)
	}
	rm = deliver(t, rm, cmd)
	if !strings.Contains(ansi.Strip(strings.Join(rm.previewLines(), "\n")), "The lock moves into the store") {
		t.Errorf("the overlay is not showing the document the box's cursor is on:\n%s", ansi.Strip(rm.View()))
	}
}

// The bar is what tells a reviewer what `enter` will do here, and here it does not open a difftool.
func TestTheBarSaysWhatEnterOpens(t *testing.T) {
	doc := askPreview(t, boxTo(t, docModel(t), rowAbout))
	doc = focusPane(t, doc)
	if !strings.Contains(doc.View(), "enter open") {
		t.Errorf("the bar does not say what enter opens over a document:\n%s", doc.View())
	}
	file := paneKey(t, doc, runeKey('f'))
	file = paneKey(t, file, runeKey('p'))
	if !strings.Contains(file.View(), "enter diff") {
		t.Errorf("the bar does not say what enter opens over a diff:\n%s", file.View())
	}
}

// `enter` in the pane opens what the pane is showing, with the rules the row itself would apply -- which for
// a document is the editor, since a difftool has nothing to compare.
func TestEnterOpensTheDocumentThePaneIsShowing(t *testing.T) {
	m := focusPane(t, askPreview(t, boxTo(t, docModel(t), rowThread)))

	updated, cmd := m.Update(keyMsg(tea.KeyEnter))
	got, ok := updated.(reviewModel)
	if !ok {
		t.Fatalf("enter produced %T", updated)
	}
	if cmd == nil {
		t.Errorf("enter opened nothing; the pane says %q", got.status)
	}
}

// The span line and "+ new thread…" have nothing to read, so the cursor passing over them leaves the pane
// where it was -- the same rule that keeps a diff on screen when the tree's cursor is on a directory row.
func TestAHeadingWithNothingToReadKeepsThePane(t *testing.T) {
	m := boxTo(t, docModel(t), rowAbout)
	m = askPreview(t, m)
	m = boxTo(t, m, rowSpan)
	m = askPreview(t, m)
	if want := m.sess.AboutPath(); m.previewPath != want {
		t.Errorf("the pane moved off ABOUT.md to %q", m.previewPath)
	}
	if !strings.Contains(docView(m), "The lock moves into the store") {
		t.Errorf("the pane lost the document:\n%s", docView(m))
	}
}
