package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/gittest"
	"gitpair/internal/lifecycle"
	"gitpair/internal/span"
)

// The paths `changeFixture` leaves in the span, named so a test says which file it means. The two under
// `moved/` are the two moves: one git scored as perfect, one as almost.
const (
	freshFile = "fresh.go"
	keptFile  = "keep.go"
	goneFile  = "gone.go"
	movedPure = "moved/pure.go"
	movedEdit = "moved/edited.go"
	aboutFile = "changesets/booking/ABOUT.md"
)

// thirty is the text a file is seeded with: long enough that moving it and adding one line is still the
// same file to git, which is the difference between the two rows the tree calls `~`.
func thirty(word string) string { return strings.Repeat(word+"\n", 30) }

// changeFixture is a changeset whose span does every git can do to a file: change it, delete it, move it
// with its bytes unchanged, move it and edit it, create one, and add one no pane can draw. The optional
// config lines are written to the repository before the span's work is committed, so a test can say what
// git itself was told.
func changeFixture(t *testing.T, config ...[2]string) *gittest.Fixture {
	t.Helper()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFiles(map[string]string{
		keptFile:    thirty("keep"),
		"pure.go":   thirty("pure"),
		"edited.go": thirty("edited"),
		goneFile:    "package main\n\nfunc Gone() {}\n",
	}))
	for _, kv := range config {
		f.Config(kv[0], kv[1])
	}
	f.CreateBranch("booking")
	f.CommitChangeset("booking", "main")
	f.Write(keptFile, thirty("keep")+"// a comment to read\n")
	f.Remove(goneFile)
	f.Rename("pure.go", movedPure)
	f.Rename("edited.go", movedEdit)
	f.Append(movedEdit, "// one line after the move\n")
	f.Write(freshFile, "package main\n\nfunc Fresh() {}\n")
	f.Write("blob.bin", "PNG\x00\x01\x02\x00binary\n")
	f.Commit("rearrange")
	return f
}

// changeModel is a model over that fixture with no seams in the way: the signs and the panes are git's
// answers about the repository the fixture built.
func changeModel(t *testing.T, config ...[2]string) reviewModel {
	t.Helper()
	return modelOver(t, changeFixture(t, config...))
}

// modelOver is a model over a fixture the caller still holds, so a test can change the working copy and
// watch what the pane reads.
func modelOver(t *testing.T, f *gittest.Fixture) reviewModel {
	t.Helper()
	ctx := context.Background()
	repo := &git.Repo{Dir: f.Dir()}
	cs, err := changeset.Current(ctx, repo, "")
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	summary, err := lifecycle.SummarizeHEAD(ctx, repo, cs.Slug, cs.Base)
	if err != nil {
		t.Fatalf("SummarizeHEAD: %v", err)
	}
	sess, err := NewSession(ctx, Options{Repo: repo, Changeset: cs, Summary: summary, Span: span.Full()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	m := reviewModel{ctx: ctx, sess: sess, width: 140, height: 30, threadsOpen: true, previewOn: true}
	m.refresh()
	return m
}

// filesByPath is the span's answer keyed by path, which is how the tests below say what they expect.
func filesByPath(m reviewModel) map[string]File {
	byPath := map[string]File{}
	for _, f := range m.sess.Files() {
		byPath[f.Path] = f
	}
	return byPath
}

// cursorOnPath puts the cursor on one file's row, with the keys in the list, and answers with the model
// and the row's index. The keys matter: a test that came away from the changeset box leaves them there,
// and the pane follows the row the keys are standing on.
func cursorOnPath(t *testing.T, m reviewModel, path string) (reviewModel, int) {
	t.Helper()
	for i, r := range m.rows {
		if r.kind == rowFile && r.path == path {
			m.focusOn(focusFiles)
			m.cursor = i
			return m, i
		}
	}
	t.Fatalf("no row for %s:\n%s", path, rowList(m))
	return m, -1
}

// Enter on a file the span added goes to the editor; every other change goes to the difftool. The
// added file is the one change with nothing on the span's left side, so its patch is the file again
// with a `+` in front of every line -- the same reason the pane reads it as a file rather than as a
// patch, and the same reason openArtifact gives a document the changeset invented to the editor.
func TestEnterOnAnAddedFileGoesToTheEditor(t *testing.T) {
	for _, c := range []struct {
		name   string
		change Change
		want   action
	}{
		{"a file the span added", ChangeAdded, actionEdit},
		{"a modification", ChangeChanged, actionDiff},
		{"a deletion", ChangeDeleted, actionDiff},
		{"a move with the bytes unchanged", ChangeMovedWhole, actionDiff},
		{"a move with edits", ChangeMoved, actionDiff},
	} {
		if got := activateBy(row{kind: rowFile, change: c.change}); got != c.want {
			t.Errorf("Enter on %s = %d, want %d", c.name, got, c.want)
		}
	}

	m := changeModel(t)
	fresh, _ := cursorOnPath(t, m, freshFile)
	if got := fresh.rows[fresh.cursor].change; got != ChangeAdded {
		t.Fatalf("the row for %s carries %v, want an added file", freshFile, got)
	}
	after, cmd := fresh.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := after.(reviewModel)
	if cmd == nil {
		t.Errorf("enter on %s handed the terminal to nothing; status %q", freshFile, got.status)
	}
	if got.status != "" {
		t.Errorf("enter on %s was refused: %q", freshFile, got.status)
	}

	// `d` is the key for the patch of the same row, added or not: the reviewer who wanted to see the
	// `+` on every line still has one key that says so.
	if _, cmd := got.Update(runeKey('d')); cmd == nil {
		t.Errorf("d on %s handed the terminal to nothing", freshFile)
	}
}

// The pane's bar says what enter will do, and over a file the span added that is the editor rather
// than the difftool -- the same difference the row itself makes, said in the one place a reviewer
// looks to find out what a key means here.
func TestThePaneSaysEnterOpensAnAddedFile(t *testing.T) {
	m := changeModel(t)
	fresh, _ := cursorOnPath(t, m, freshFile)
	pane := focusPane(t, fresh)
	if pane.previewKind != previewContent {
		t.Fatalf("the pane is showing a %v, want the added file read as a file", pane.previewKind)
	}
	if !strings.Contains(pane.View(), "enter open") {
		t.Errorf("the bar does not say what enter opens over an added file:\n%s", ansi.Strip(pane.View()))
	}
	if strings.Contains(pane.View(), "enter diff") {
		t.Errorf("the bar still promises a diff for a file with nothing on one side of it:\n%s", ansi.Strip(pane.View()))
	}

	kept, _ := cursorOnPath(t, m, keptFile)
	if got := focusPane(t, kept); !strings.Contains(got.View(), "enter diff") {
		t.Errorf("the bar does not say enter opens a diff for a modification:\n%s", ansi.Strip(got.View()))
	}
}

// The tree's one character per file is git's answer, not a guess from what a diff looks like: a file the
// span created, deleted, or moved says so, and one it only changed says nothing.
func TestTheTreeSignIsWhatGitSaysTheSpanDid(t *testing.T) {
	byPath := filesByPath(changeModel(t))
	for _, want := range []struct {
		path, sign, from string
		change           Change
	}{
		{freshFile, "+", "", ChangeAdded},
		{keptFile, "", "", ChangeChanged},
		{goneFile, "-", "", ChangeDeleted},
		{movedPure, "~", "pure.go", ChangeMovedWhole},
		{movedEdit, "~", "edited.go", ChangeMoved},
	} {
		got, ok := byPath[want.path]
		if !ok {
			t.Errorf("%s is not in the span, which has %d files", want.path, len(byPath))
			continue
		}
		if got.Change != want.change || got.Change.Sign() != want.sign || got.MovedFrom != want.from {
			t.Errorf("%s: change %d (sign %q, from %q), want %d (sign %q, from %q)",
				want.path, got.Change, got.Change.Sign(), got.MovedFrom, want.change, want.sign, want.from)
		}
	}
}

// The sign goes after the name and nowhere else, so the name stays the thing the row is about.
func TestTheRowPutsTheSignAfterTheName(t *testing.T) {
	m := changeModel(t)
	for _, want := range []struct{ path, ends string }{
		{freshFile, "fresh.go +"},
		{goneFile, "gone.go -"},
		{movedPure, "pure.go ~"},
		{movedEdit, "edited.go ~"},
		{keptFile, "keep.go"},
	} {
		at, i := cursorOnPath(t, m, want.path)
		text := ansiCodes.ReplaceAllString(at.rowText(at.rows[i]), "")
		if !strings.HasSuffix(text, want.ends) {
			t.Errorf("the row for %s is %q, want it to end with %q", want.path, text, want.ends)
		}
	}
}

// The colour belongs to the sign and to nothing else on the row, and each change wears the colour
// git's own diff wears for the same fact -- the tree and the pane beside it say one thing in one
// colour. Green is the reviewed mark's colour, red is a refusal's and blue is what the pane gives a
// line you typed yourself; the palette in tui.go says why none of those readings collide with a sign.
// What a unit test can check is the choice, since lipgloss draws no colour for a terminal it does not
// believe in -- that the codes reach a real one is the pty walkthrough's claim.
func TestEachChangeWearsItsOwnColour(t *testing.T) {
	for _, want := range []struct {
		name   string
		change Change
		fg     lipgloss.Color
	}{
		{"a file the span created", ChangeAdded, lipgloss.Color("10")},
		{"a file the span deleted", ChangeDeleted, lipgloss.Color("9")},
		{"a file the span moved", ChangeMoved, lipgloss.Color("12")},
		{"a move with the bytes unchanged", ChangeMovedWhole, lipgloss.Color("12")},
	} {
		got := signStyle(want.change).GetForeground()
		if got != want.fg {
			t.Errorf("%s: the sign is drawn in %v, want %v", want.name, got, want.fg)
		}
		if signStyle(want.change).GetFaint() {
			t.Errorf("%s: the sign is faint as well as coloured", want.name)
		}
	}
	// The modification has no sign, so there is no character here to colour.
	if st := signStyle(ChangeChanged); st.GetForeground() != (lipgloss.NoColor{}) || st.GetFaint() {
		t.Errorf("a modification's sign is drawn in %v, faint %v, want nothing at all",
			st.GetForeground(), st.GetFaint())
	}
}

// Two changes read better as the file itself: one the span created, whose patch is the file with a `+`
// on every line, and one moved with its bytes unchanged, whose patch is two lines about a path. A rename
// with edits keeps the patch, because the edits are what is under review, and so does a deletion, which
// is the only place the removed text still is.
func TestANewFileAndAnUnchangedMoveAreReadAndTheRestAreDiffed(t *testing.T) {
	m := changeModel(t)
	for _, want := range []struct {
		path string
		kind previewKind
	}{
		{freshFile, previewContent},
		{movedPure, previewContent},
		{movedEdit, previewDiff},
		{keptFile, previewDiff},
		{goneFile, previewDiff},
	} {
		at, _ := cursorOnPath(t, m, want.path)
		kind, path, ok := at.previewRowTarget()
		if !ok || kind != want.kind || path != want.path {
			t.Errorf("%s: pane %d for %q (ok %v), want %d for %q",
				want.path, kind, path, ok, want.kind, want.path)
		}
	}
}

// The pane of a file the span created is the file: its own text, with its own line count, and nothing
// that looks like a patch. `enter` still means the difftool, because the file is still a file.
func TestTheTextPaneShowsTheFileRatherThanItsPatch(t *testing.T) {
	m := changeModel(t)
	m, _ = cursorOnPath(t, m, freshFile)
	m = askPreview(t, m)
	view := m.View()
	if !strings.Contains(view, "func Fresh() {}") {
		t.Errorf("the pane does not show the file's own text:\n%s", view)
	}
	if strings.Contains(view, "+package main") {
		t.Errorf("the pane shows a patch of the file the span created:\n%s", view)
	}
	// The header counts the file, in the place a diff's `+N −M` sits: one slot for "what is this, and how
	// much of it", whichever kind of thing the pane is showing.
	if !strings.Contains(view, freshFile+"  3 lines") {
		t.Errorf("the pane does not count the file's lines beside its name:\n%s", view)
	}
	// `enter` is still the real comparison. A file the pane reads as text is a file, and the key that
	// means "show me this in the tool built for it" has to keep meaning that.
	opened, cmd := m.openPreview()
	if om, isModel := opened.(reviewModel); !isModel || om.statusErr {
		t.Errorf("enter on a file shown as text refused: %+v", opened)
	}
	if cmd == nil {
		t.Error("enter on a file shown as text launched nothing")
	}
}

// The tree has room for one character about a move. The pane carries the rest of the answer: the path the
// file came from.
func TestTheTextPaneNamesWhereAMoveCameFrom(t *testing.T) {
	m := changeModel(t)
	m, _ = cursorOnPath(t, m, movedPure)
	m = askPreview(t, m)
	if view := m.View(); !strings.Contains(view, "from pure.go") {
		t.Errorf("the pane of a move does not say where it came from:\n%s", view)
	}
}

// The text on show is the file at the span's head. The reviewer's own uncommitted edits are not in it, so
// the pane reports that they exist rather than letting a reviewer read their own typing as reviewed work.
func TestTheTextPaneSaysWhenTheReviewerEditedTheFile(t *testing.T) {
	edited := changeModel(t)
	edited.workingFor = func(_ context.Context, _ string) Patch {
		return Patch{Lines: []string{"+mine"}, Added: 1, Deleted: 0}
	}
	edited, _ = cursorOnPath(t, edited, freshFile)
	edited = askPreview(t, edited)
	if view := edited.View(); !strings.Contains(view, "you edited it") {
		t.Errorf("the pane hides the reviewer's edits:\n%s", view)
	}

	// With nothing of the reviewer's in the file, the pane says nothing of the kind: an invented warning
	// is how a reviewer stops believing the real ones.
	untouched := changeModel(t)
	untouched.workingFor = func(_ context.Context, _ string) Patch { return Patch{} }
	untouched, _ = cursorOnPath(t, untouched, freshFile)
	untouched = askPreview(t, untouched)
	if view := untouched.View(); strings.Contains(view, "you edited it") {
		t.Errorf("the pane claims edits that are not there:\n%s", view)
	}
}

// The reviewer's uncommitted typing is drawn into the file at the position it lands: git's own `-` and `+`
// lines, each with the marker that says who typed it. This pane has no caption to file them under — it is
// the pane that reads a file as a file — so the marker is what keeps their line from reading as the
// author's, and the removed line is what keeps their edit from reading as the whole story. The file is one the
// span created, so every line in it is the span's work: the line the reviewer deleted is drawn as a deletion
// of the reviewed work, which is what `×` is for.
func TestTheTextPaneDrawsTheReviewersEditsWhereTheyLand(t *testing.T) {
	f := changeFixture(t)
	m := modelOver(t, f)
	// The reviewer edits the file the span created and commits nothing: the span's head still holds what
	// the author wrote, and the working copy holds the typing on top of it.
	f.Write(freshFile, "package main\n\nfunc Fresh() { return nil }\n\n// reviewer: why?\n")

	m, _ = cursorOnPath(t, m, freshFile)
	m = askPreview(t, m)
	shown := ansi.Strip(m.View())

	for _, want := range []string{"\u00d7func Fresh() {}", "+func Fresh() { return nil }", "+// reviewer: why?"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the pane does not draw the reviewer's line %q:\n%s", want, shown)
		}
	}
	if n := strings.Count(shown, "\u2190 you"); n != 4 {
		t.Errorf("%d rows carry the marker, want one per line of the reviewer's edit (+3 \u22121):\n%s", n, shown)
	}
	if !strings.Contains(shown, "you edited it  +3 \u22121") {
		t.Errorf("the header does not count the reviewer's own lines:\n%s", shown)
	}
	// The author's file is still what is being read: the edit is drawn into it, not instead of it.
	if !strings.Contains(shown, "package main") {
		t.Errorf("the file itself is gone:\n%s", shown)
	}
	if strings.Contains(shown, "diff --git") {
		t.Errorf("the text pane grew patch chrome:\n%s", shown)
	}
}

// A historical span has no reviewer edits inside it: the working tree is not one of its endpoints. The pane
// marks nothing and says nothing about them, the way it refuses every key that would change something.
func TestTheTextPaneMarksNothingOverAHistoricalSpan(t *testing.T) {
	m, f := readonlyModel(t, historySel())
	m.width = 140
	m.previewOn = true
	// handler.go is a file this span created, and the working copy now differs from the head it is read at.
	f.Write("handler.go", "package main\n\nfunc Serve() { ctx() }\n\n// a note typed afterwards\n")

	m, _ = cursorOnPath(t, m, "handler.go")
	m = askPreview(t, m)
	shown := ansi.Strip(m.View())

	if strings.Contains(shown, "\u2190 you") || strings.Contains(shown, "you edited it") {
		t.Errorf("a historical pane spoke of the reviewer's edits:\n%s", shown)
	}
	if !strings.Contains(shown, "func Serve()") {
		t.Errorf("the historical file's own text is missing:\n%s", shown)
	}
	if strings.Contains(shown, "a note typed afterwards") {
		t.Errorf("the working copy leaked into a historical span:\n%s", shown)
	}
}

// A changeset document is on screen two ways at once: as a file the span created, read at the span's head
// with the reviewer's uncommitted typing marked into it, and as the document the reviewer is editing, read
// from disk as it stands. Two caches keep those apart, and the file row is the one that keeps showing the
// span's bytes with the edit drawn into them.
func TestAFileRowAndTheDocumentOfTheSamePathAreDifferentPanes(t *testing.T) {
	f := changeFixture(t)
	m := modelOver(t, f)
	// The reviewer edits ABOUT.md and does not commit it: what the span contains is still what it was.
	f.Write(aboutFile, "# ABOUT.md written by the reviewer\n\nNot committed.\n")

	m, _ = cursorOnPath(t, m, aboutFile)
	m = askPreview(t, m)
	if m.previewKind != previewContent {
		t.Fatalf("the file row of a created ABOUT.md is pane kind %d, want %d", m.previewKind, previewContent)
	}
	fileView := ansi.Strip(m.View())
	// The reviewer's typing is on this row, and it is on it as an edit — git's `-` and `+`, each with the
	// marker — rather than as the file's own text.
	if !strings.Contains(fileView, "written by the reviewer") || !strings.Contains(fileView, "\u2190 you") {
		t.Errorf("the file row neither shows the reviewer's edit nor marks it:\n%s", fileView)
	}
	if !strings.Contains(fileView, "\u00d7# Changeset") {
		t.Errorf("the file row no longer shows what the edit replaced:\n%s", fileView)
	}

	// The About row is in the changeset box, so the keys have to be in the box to be standing on it.
	m = boxTo(t, m, rowAbout)
	m = askPreview(t, m)
	if m.previewKind != previewDocument {
		t.Fatalf("the About row is pane kind %d, want %d", m.previewKind, previewDocument)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "written by the reviewer") ||
		strings.Contains(view, "\u2190 you") {
		// The document is the file as it stands, which is what the reviewer is writing: nothing on it is
		// somebody's edit, because the whole of it is.
		t.Errorf("the About row is not the working copy read as a document:\n%s", view)
	}

	// Back to the file row: the document's text must not have taken its place in the cache. The row is
	// still the span's file with the edit marked into it, and not the document as it now stands.
	m, _ = cursorOnPath(t, m, aboutFile)
	m = askPreview(t, m)
	if view := ansi.Strip(m.View()); !strings.Contains(view, "\u00d7# Changeset") ||
		!strings.Contains(view, "\u2190 you") {
		t.Errorf("the document's text replaced the file row's:\n%s", view)
	}
}

// Asking git twice about one file is a bug, and the text of a file is git's answer like a patch is. The
// rows the pane reads are walked twice over, so a cache that never fills cannot pass for one that works.
func TestTheTextIsFetchedOncePerFile(t *testing.T) {
	m := changeModel(t)
	asks := map[string]int{}
	m.contentFor = func(_ context.Context, path string) Document {
		asks[path]++
		return Document{Sections: []DocSection{{Lines: []string{"the file's own text"}}}}
	}
	var rows []int
	for i := range m.rows {
		m.cursor = i
		if kind, _, ok := m.previewRowTarget(); ok && kind == previewContent {
			rows = append(rows, i)
		}
	}
	if len(rows) < 2 {
		t.Fatalf("the fixture offers %d rows the pane reads as text, want at least 2", len(rows))
	}
	walk := append([]int{}, rows...)
	walk = append(walk, rows...)
	for _, idx := range walk {
		m.cursor = idx
		m = askPreview(t, m)
	}
	for path, n := range asks {
		if n != 1 {
			t.Errorf("%s was read %d times, want once", path, n)
		}
	}
	if len(asks) != len(rows) {
		t.Errorf("read %d paths, want the %d rows visited", len(asks), len(rows))
	}

	// A span that is gone takes its cached text with it: the next visit asks again.
	m.forgetPatches()
	m.cursor = rows[0]
	m = askPreview(t, m)
	if asks[m.rows[rows[0]].path] != 2 {
		t.Error("forgetting the patches did not make the next visit ask again")
	}
}

// A blob is bytes, and the pane has nothing to draw. It says which file is unreadable instead of printing
// the noise or leaving the column blank.
func TestTheTextPaneSaysAFileIsNotText(t *testing.T) {
	m := changeModel(t)
	m, _ = cursorOnPath(t, m, "blob.bin")
	m = askPreview(t, m)
	if view := m.View(); !strings.Contains(view, "blob.bin is not text") {
		t.Errorf("the pane of a binary file does not say what it is:\n%s", view)
	}
}

// Rename detection is git's setting, not this code's: the list of files and the sign beside each one come
// from two calls that have to agree. With detection off, git answered a delete and an add, and the tree
// says `-` and `+` about that pair rather than `~` about a move it was never told about.
func TestWithoutRenameDetectionTheTreeSaysDeleteAndAdd(t *testing.T) {
	byPath := filesByPath(changeModel(t, [2]string{"diff.renames", "false"}))
	gone, ok := byPath["pure.go"]
	if !ok || gone.Change != ChangeDeleted {
		t.Errorf("pure.go: %+v, want a deletion", gone)
	}
	moved, ok := byPath[movedPure]
	if !ok || moved.Change != ChangeAdded {
		t.Errorf("%s: %+v, want an addition", movedPure, moved)
	}
	if moved.MovedFrom != "" {
		t.Errorf("%s claims to come from %q, which git did not say", movedPure, moved.MovedFrom)
	}
}

// git's status letters, one row each. An unknown letter is a modification: a sign has to be earned, and
// `T`, `C` and anything git invents next say nothing this code has read.
func TestGitStatusLettersBecomeChanges(t *testing.T) {
	for _, want := range []struct {
		status string
		change Change
	}{
		{"M", ChangeChanged},
		{"A", ChangeAdded},
		{"D", ChangeDeleted},
		{"R100", ChangeMovedWhole},
		{"R097", ChangeMoved},
		{"R050", ChangeMoved},
		{"T", ChangeChanged},
		{"C100", ChangeChanged},
		{"", ChangeChanged},
	} {
		if got := changeOf(want.status); got != want.change {
			t.Errorf("git status %q became %d, want %d", want.status, got, want.change)
		}
	}
}

// What the pane opens is a decision the change makes, in one place.
func TestWhichChangesReadAsFiles(t *testing.T) {
	for _, want := range []struct {
		change Change
		read   bool
	}{
		{ChangeAdded, true},
		{ChangeMovedWhole, true},
		{ChangeChanged, false},
		{ChangeMoved, false},
		{ChangeDeleted, false},
	} {
		if got := want.change.ReadsAsFile(); got != want.read {
			t.Errorf("change %d ReadsAsFile() = %v, want %v", want.change, got, want.read)
		}
	}
}
