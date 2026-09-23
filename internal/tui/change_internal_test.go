package tui

import (
	"context"
	"strings"
	"testing"

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
	if !strings.Contains(view, "3 lines") {
		t.Errorf("the pane does not count the file's lines:\n%s", view)
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

// A changeset document is on screen two ways at once: as a file the span created, read at the span's
// head, and as the document the reviewer is editing, read from disk. Two caches keep those apart, and the
// file row is the one that stays put when the reviewer writes.
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
	fileView := m.View()
	if strings.Contains(fileView, "written by the reviewer") {
		t.Errorf("the file row shows the reviewer's uncommitted edit instead of the span's bytes:\n%s", fileView)
	}

	// The About row is in the changeset box, so the keys have to be in the box to be standing on it.
	m = boxTo(t, m, rowAbout)
	m = askPreview(t, m)
	if m.previewKind != previewDocument {
		t.Fatalf("the About row is pane kind %d, want %d", m.previewKind, previewDocument)
	}
	if view := m.View(); !strings.Contains(view, "written by the reviewer") {
		t.Errorf("the About row does not read the working copy:\n%s", view)
	}

	// Back to the file row: the document's text must not have taken its place in the cache.
	m, _ = cursorOnPath(t, m, aboutFile)
	m = askPreview(t, m)
	if view := m.View(); strings.Contains(view, "written by the reviewer") {
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
