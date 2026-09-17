package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/gittest"
	"gitpair/internal/lifecycle"
	"gitpair/internal/span"
)

// A changeset that touches twelve files in four packages is twelve rows of path prefix when it is
// a flat list. These are the tests for laying the same list out as a tree: the rows it prints, the
// folds that hide them, and what marking a directory does to the files under it — including the
// ones a fold has hidden.

// --- the tree itself --------------------------------------------------------

// filesOf is the session's file list as the tree sees it: paths in git's order, none reviewed.
func filesOf(paths ...string) []File {
	out := make([]File, 0, len(paths))
	for _, p := range paths {
		out = append(out, File{Path: p, Key: "k" + p})
	}
	return out
}

// treeShape is what a test compares a flattened tree against, one line per row: depth, kind, path.
func treeShape(rows []treeRow) string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		kind := "file"
		if r.dir {
			kind = "dir "
		}
		out = append(out, fmt.Sprintf("%d %s %s", r.depth, kind, r.path))
	}
	return strings.Join(out, "\n")
}

// The nesting, the ordering (a directory's subdirectories before the files beside them), and the
// path compression all in one: a run of directories that hold nothing but one directory is a row
// apiece for nothing, so `docs/plans/active/` is one row and `internal/`, which really does branch,
// is not folded into anything.
func TestTreeNestsFilesUnderTheirDirectories(t *testing.T) {
	rows := flattenTree(filesOf(
		"docs/notes.md",
		"docs/plans/active/x.md",
		"internal/git/git.go",
		"internal/tui/session.go",
		"internal/tui/tui.go",
		"main.go",
	), nil)

	want := strings.Join([]string{
		"0 dir  docs/",
		"1 dir  docs/plans/active/",
		"2 file docs/plans/active/x.md",
		"1 file docs/notes.md",
		"0 dir  internal/",
		"1 dir  internal/git/",
		"2 file internal/git/git.go",
		"1 dir  internal/tui/",
		"2 file internal/tui/session.go",
		"2 file internal/tui/tui.go",
		"0 file main.go",
	}, "\n")
	if got := treeShape(rows); got != want {
		t.Errorf("the tree came out as:\n%s\nwant:\n%s", got, want)
	}
}

// A file row's name is the part the tree has not already said: the directory above it is a row, so
// repeating it on every child is the noise the tree exists to remove.
func TestFileRowsPrintTheNameTheTreeHasNotSaidYet(t *testing.T) {
	rows := flattenTree(filesOf("internal/tui/tui.go"), nil)
	for _, r := range rows {
		if r.dir {
			continue
		}
		if r.name != "tui.go" {
			t.Errorf("the file row prints %q, want tui.go", r.name)
		}
	}
}

// A changeset with nothing nested in it must look exactly like it did before there was a tree:
// one row per file, no indent, no directory rows in the way.
func TestATreeWithNothingInTheWayIsTheFlatList(t *testing.T) {
	// In git's order, which is how the session's list arrives: the tree keeps it, so a changeset
	// that touches nothing nested looks exactly as it did before there was a tree.
	files := filesOf("handler.go", "main.go", "service.go")
	rows := flattenTree(files, nil)
	if len(rows) != len(files) {
		t.Fatalf("the tree made %d rows for %d files:\n%s", len(rows), len(files), treeShape(rows))
	}
	for i, r := range rows {
		if r.dir || r.depth != 0 || r.path != files[i].Path {
			t.Errorf("row %d is %+v, want a top-level file row for %s", i, r, files[i].Path)
		}
	}
}

// Folding hides rows and nothing else. The counts survive because they are a fact about the files
// under the directory rather than about the rows on screen — and the count is what the reviewer
// folded the directory to keep seeing.
func TestFoldingHidesRowsButNotWhatTheyCount(t *testing.T) {
	files := filesOf("internal/tui/session.go", "internal/tui/tui.go", "main.go")
	files[0].Reviewed = true

	open := flattenTree(files, nil)
	folded := flattenTree(files, map[string]bool{"internal/tui/": true})

	if len(folded) != 2 {
		t.Fatalf("the folded tree has %d rows, want the directory and main.go:\n%s", len(folded), treeShape(folded))
	}
	for _, r := range folded {
		if r.path == "internal/tui/session.go" || r.path == "internal/tui/tui.go" {
			t.Errorf("the fold still prints %s", r.path)
		}
		if r.dir {
			if r.total != 2 || r.marked != 1 {
				t.Errorf("the folded directory says %d/%d, want 1/2", r.marked, r.total)
			}
		}
	}
	if len(open) == len(folded) {
		t.Fatal("folding changed nothing, so the fold is not being read")
	}
}

// The counts are what `Space` is about to set, so a directory that has nothing in it reviewed says
// 0 of everything, a full one says all, and the row in between has to be able to tell them apart.
func TestDirectoryCountsSayWhatSpaceWillSet(t *testing.T) {
	files := filesOf("a/one.go", "a/two.go", "a/three.go", "b/one.go")
	files[0].Reviewed = true
	files[1].Reviewed = true

	want := map[string][2]int{
		"a/": {3, 2},
		"b/": {1, 0},
	}
	for _, r := range flattenTree(files, nil) {
		if !r.dir {
			continue
		}
		w, ok := want[r.path]
		if !ok {
			t.Fatalf("unexpected directory row %q", r.path)
		}
		if r.total != int(w[0]) || r.marked != int(w[1]) {
			t.Errorf("%s counts %d of %d, want %d of %d", r.path, r.marked, r.total, w[1], w[0])
		}
	}
}

// `c` has to fold every directory there is, including the ones a folded run of single-child
// directories never gave a row of its own: miss those and the second press folds the tree a second
// time instead of opening it.
func TestTreeDirsNamesEveryDirectoryNotOnlyTheOnesWithRows(t *testing.T) {
	dirs := treeDirs(filesOf("a/b/c/deep.go", "a/top.go"))
	want := []string{"a/", "a/b/", "a/b/c/"}
	if strings.Join(dirs, " ") != strings.Join(want, " ") {
		t.Errorf("treeDirs = %v, want %v", dirs, want)
	}
}

func TestAncestorsOfWalksTheChainDownToTheDirectoryItself(t *testing.T) {
	got := ancestorsOf("a/b/c/")
	want := []string{"a/", "a/b/", "a/b/c/"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("ancestorsOf = %v, want %v", got, want)
	}
}

// The trailing separator is the whole comparison: `internal/tui/` covers the package, and not the
// package next door whose name happens to start the same way.
func TestADirectoryCoversItsOwnAndNotTheNameBesideIt(t *testing.T) {
	cases := []struct {
		path, dir string
		want      bool
	}{
		{"internal/tui/tui.go", "internal/tui/", true},
		{"internal/tui/deep/nested.go", "internal/tui/", true},
		{"internal/tuition/why.go", "internal/tui/", false},
		{"internal/tui.go", "internal/tui/", false},
		{"internal/tui/", "internal/tui/", false},
		{"main.go", "internal/", false},
	}
	for _, c := range cases {
		if got := isUnder(c.path, c.dir); got != c.want {
			t.Errorf("isUnder(%q, %q) = %v, want %v", c.path, c.dir, got, c.want)
		}
	}
}

// --- the fixture ------------------------------------------------------------

// treeFixture is a changeset whose files sit at the top of the repository and three directories
// deep, with `internal/tuition` beside `internal/tui` so that a directory's reach can be told apart
// from a name that merely starts the same way. Every file it changes gains two lines, so the counts
// a directory diff reports are arithmetic a test can state.
func treeFixture(t *testing.T) *gittest.Fixture {
	t.Helper()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFiles(map[string]string{
		"main.go":                 "package main\n\nfunc main() {}\n",
		"docs/notes.md":           "# notes\n",
		"docs/plans/active/x.md":  "# x\n",
		"internal/git/git.go":     "package git\n",
		"internal/tui/session.go": "package tui\n",
		"internal/tui/tui.go":     "package tui\n",
		"internal/tuition/why.go": "package tuition\n",
	}))
	const slug = "booking"
	f.CreateBranch(slug)
	f.CommitChangeset(slug, "main")
	f.Commit("implement", gittest.WithFiles(map[string]string{
		"main.go":                 "package main\n\nfunc main() {\n\trun()\n}\n",
		"docs/notes.md":           "# notes\n\nSee the plan.\n",
		"docs/plans/active/x.md":  "# x\n\nThe plan.\n",
		"internal/git/git.go":     "package git\n\nfunc Git() {}\n",
		"internal/tui/session.go": "package tui\n\nfunc Run() {}\n",
		"internal/tui/tui.go":     "package tui\n\nfunc View() {}\n",
		"internal/tuition/why.go": "package tuition\n\nfunc Why() {}\n",
	}))
	return f
}

// treeSession opens a session over that fixture, the way `git pair review open` would.
func treeSession(t *testing.T, f *gittest.Fixture) *Session {
	t.Helper()
	ctx := context.Background()
	repo := &git.Repo{Dir: f.Dir()}
	// The fixture's branch is the changeset's slug, so this resolves the booking changeset the
	// way `git pair review open` would.
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
	return sess
}

// treeModel is a screen over that session with the preview off: these tests read the list, and a
// pane would ask git about a file every time one of them moves the cursor.
func treeModel(t *testing.T) (reviewModel, *gittest.Fixture) {
	t.Helper()
	f := treeFixture(t)
	m := reviewModel{ctx: context.Background(), sess: treeSession(t, f), width: 100, height: 24,
		threadsOpen: true}
	m.refresh()
	if len(m.sess.Files()) < 6 {
		t.Fatalf("the fixture changed %d files, want the five it touches plus the changeset's own",
			len(m.sess.Files()))
	}
	return m, f
}

// dirRow is where the directory is on screen, so a test folds or marks the directory it means
// rather than an offset that moves when the fixture changes.
func dirRow(t *testing.T, m reviewModel, dir string) int {
	t.Helper()
	for i, r := range m.rows {
		if r.kind == rowDir && r.path == dir {
			return i
		}
	}
	t.Fatalf("no row for the directory %s:\n%s", dir, rowList(m))
	return -1
}

func cursorOnDir(t *testing.T, m reviewModel, dir string) reviewModel {
	t.Helper()
	m.cursor = dirRow(t, m, dir)
	return m
}

// visibleTree is the list as a reviewer sees it: every row as it prints, colours out.
func visibleTree(m reviewModel) string {
	var b strings.Builder
	for _, r := range m.rows {
		b.WriteString(ansiCodes.ReplaceAllString(m.rowText(r), ""))
		b.WriteString("\n")
	}
	return b.String()
}

// The whole list at once, because it is the thing the change is for. The paths are the ones the
// fixture commits; directories come before the files beside them, a directory that really branches
// keeps its own row while a chain of single-child directories becomes one, and every name says only
// what the rows above it have not. The reviewed counter and the blank lines between the blocks are
// drawn around these rows by listBlock, and asserted where they are drawn.
func TestTheListAsItRenders(t *testing.T) {
	m, _ := treeModel(t)
	want := strings.Join([]string{
		"▾ ○ changesets/booking/",
		"  ○ ABOUT.md",
		"  ○ CHANGESET.yaml",
		"▾ ○ docs/",
		"  ▾ ○ plans/active/",
		"    ○ x.md",
		"  ○ notes.md",
		"▾ ○ internal/",
		"  ▾ ○ git/",
		"    ○ git.go",
		"  ▾ ○ tui/",
		"    ○ session.go",
		"    ○ tui.go",
		"  ▾ ○ tuition/",
		"    ○ why.go",
		"○ main.go",
		"ABOUT.md",
		"▾ Threads",
		"    + new thread…",
	}, "\n")
	if got := visibleTree(m); got != want+"\n" {
		t.Errorf("the list renders as:\n%s\nwant:\n%s\n", got, want)
	}
}

// --- marking a directory ----------------------------------------------------

// The point of the directory row: one keystroke reads the package. Everything under it is marked,
// and nothing outside it is.
func TestSpaceOnADirectoryMarksEverythingUnderIt(t *testing.T) {
	m, _ := treeModel(t)
	m = press(cursorOnDir(t, m, "internal/tui/"), tea.KeySpace)

	got := markedFiles(m.sess)
	want := map[string]bool{"internal/tui/session.go": true, "internal/tui/tui.go": true}
	if len(got) != len(want) {
		t.Fatalf("marking the directory marked %v, want the two files it holds\n%s", got, rowList(m))
	}
	for _, p := range got {
		delete(want, p)
	}
	for p := range want {
		t.Errorf("%s was not marked", p)
	}
	if !strings.Contains(m.status, "2 files") {
		t.Errorf("the screen said %q; a mark that moves two rows should say so", m.status)
	}
}

// A fold is a way of looking at the list, not a claim about what has been read. The files a fold
// hides are the ones the directory row is standing in for, so they are marked with the rest.
func TestSpaceOnADirectoryMarksWhatTheFoldHides(t *testing.T) {
	m, _ := treeModel(t)
	m = pressRune(cursorOnDir(t, m, "internal/"), 'c') // fold everything below
	hidden := 0
	for _, r := range m.rows {
		if r.kind == rowFile {
			hidden++
		}
	}
	if hidden != 1 { // main.go is the only file left on screen
		t.Fatalf("after folding the tree %d file rows are on screen, want 1:\n%s", hidden, rowList(m))
	}

	m = press(cursorOnDir(t, m, "internal/"), tea.KeySpace)
	got := map[string]bool{}
	for _, p := range markedFiles(m.sess) {
		got[p] = true
	}
	for _, p := range []string{"internal/git/git.go", "internal/tui/session.go",
		"internal/tui/tui.go", "internal/tuition/why.go"} {
		if !got[p] {
			t.Errorf("%s is under internal/ and was not marked: %v", p, markedFiles(m.sess))
		}
	}
	if got["main.go"] || got["docs/notes.md"] {
		t.Errorf("marking internal/ reached outside it: %v", markedFiles(m.sess))
	}
}

// The second press clears what the first one set, because Space flips the row under the cursor
// whatever the row is — and the row cannot say "all reviewed" and mean it twice.
func TestSpaceOnAFullyMarkedDirectoryClearsIt(t *testing.T) {
	m, _ := treeModel(t)
	m = press(cursorOnDir(t, m, "internal/tui/"), tea.KeySpace)
	if len(markedFiles(m.sess)) != 2 {
		t.Fatalf("the first space marked %v", markedFiles(m.sess))
	}
	m = press(cursorOnDir(t, m, "internal/tui/"), tea.KeySpace)
	if got := markedFiles(m.sess); len(got) != 0 {
		t.Errorf("the second space left %v marked, want nothing", got)
	}
	if !strings.Contains(m.status, "cleared") {
		t.Errorf("the screen said %q, want it to name the direction it went", m.status)
	}
}

// A directory with one file read is the state a reviewer folds it to find, so the row counts what
// is left rather than wearing a tick true of the file nobody opened.
func TestADirectoryPartWayThroughCountsWhatIsLeft(t *testing.T) {
	m, _ := treeModel(t)
	m = cursorOn(t, m, "internal/tui/tui.go")
	m = press(m, tea.KeySpace)

	at := dirRow(t, m, "internal/tui/")
	text := ansiCodes.ReplaceAllString(m.rowText(m.rows[at]), "")
	if !strings.Contains(text, "◐") || !strings.Contains(text, "1/2") {
		t.Errorf("a directory with one of two files read prints %q, want a half mark and 1/2", text)
	}

	m = press(cursorOn(t, m, "internal/tui/session.go"), tea.KeySpace)
	text = ansiCodes.ReplaceAllString(m.rowText(m.rows[dirRow(t, m, "internal/tui/")]), "")
	if strings.Contains(text, "/2") || !strings.Contains(text, "✓") {
		t.Errorf("a fully read directory prints %q, want the mark its files agree on", text)
	}
}

// Marks are per file in the store, so marking a package has to survive quitting exactly the way
// marking its files one by one does — and come back to the same files, not to the directory.
func TestDirectoryMarksComeBackFromTheStore(t *testing.T) {
	m, f := treeModel(t)
	m = press(cursorOnDir(t, m, "internal/tui/"), tea.KeySpace)
	if err := m.sess.SaveMarks(m.ctx); err != nil {
		t.Fatalf("SaveMarks: %v", err)
	}

	again := treeSession(t, f)
	got := map[string]bool{}
	for _, p := range markedFiles(again) {
		got[p] = true
	}
	if !got["internal/tui/session.go"] || !got["internal/tui/tui.go"] {
		t.Errorf("reopening the commit lost the subtree: %v", markedFiles(again))
	}
	if got["main.go"] || got["internal/git/git.go"] {
		t.Errorf("the marks spread past the directory on the way back: %v", markedFiles(again))
	}
}

// --- folding ----------------------------------------------------------------

// Enter does for a directory what it already does for the thread heading: toggles the group. It is
// not the difftool, which is `d` — and which stays one key away, not two.
func TestEnterFoldsADirectoryAndOpensItAgain(t *testing.T) {
	m, _ := treeModel(t)
	before := len(m.rows)
	m = press(cursorOnDir(t, m, "internal/"), tea.KeyEnter)

	if !m.folded["internal/"] {
		t.Fatal("Enter did not fold the directory")
	}
	if len(m.rows) >= before {
		t.Errorf("folding left %d rows, want fewer than %d:\n%s", len(m.rows), before, rowList(m))
	}
	if m.cursor != dirRow(t, m, "internal/") {
		t.Errorf("the cursor left the row it folded: %s", rowList(m))
	}

	m = press(m, tea.KeyEnter)
	if m.folded["internal/"] {
		t.Fatal("Enter did not open the directory again")
	}
	if len(m.rows) != before {
		t.Errorf("opening the directory left %d rows, want the %d it started with", len(m.rows), before)
	}
}

// Folding a directory around the cursor must not leave the cursor at its old index, which after a
// fold is some unrelated row further down the list: it belongs on the row that now stands for
// everything it was looking at.
func TestFoldingMovesTheCursorOntoTheDirectory(t *testing.T) {
	m, _ := treeModel(t)
	m = cursorOn(t, m, "internal/tui/tui.go")
	m = pressRune(m, 'c')

	r, ok := m.selectedRow()
	if !ok {
		t.Fatalf("nothing selected after folding the tree")
	}
	if r.kind != rowDir || r.path != "internal/" {
		t.Errorf("folding the tree from a file left the cursor on %q, want the directory above it\n%s",
			r.path, rowList(m))
	}
}

// `h` and `l` are the tree's up and down: `h` closes what is open and otherwise steps up to the
// directory containing the row, `l` only ever opens. A key that could close the directory the
// reviewer is reading is not the key for moving into it.
func TestHFoldsUpAndLOpensDown(t *testing.T) {
	m, _ := treeModel(t)

	m = pressRune(cursorOn(t, m, "internal/tui/tui.go"), 'h')
	if kind, _ := selectedKind(t, m); kind != rowDir {
		t.Errorf("h from a file landed on kind %d, want the directory holding it\n%s", kind, rowList(m))
	}
	if m.folded["internal/tui/"] {
		t.Error("h from a file folded the directory; it should have moved to it")
	}

	m = pressRune(m, 'h')
	if !m.folded["internal/tui/"] {
		t.Error("h on an open directory did not fold it")
	}

	m = pressRune(m, 'l')
	if m.folded["internal/tui/"] {
		t.Error("l on a folded directory did not open it")
	}

	m = pressRune(cursorOn(t, m, "main.go"), 'h')
	if !strings.Contains(m.status, "top of the tree") {
		t.Errorf("h at the top of the tree said %q, want it to say there is nothing above", m.status)
	}
	m = pressRune(m, 'l')
	if !strings.Contains(m.status, "file") {
		t.Errorf("l on a file said %q, want it to name the key that works", m.status)
	}
}

// `h` and `l` are keys of the file tree. The changeset section under the counter has paths in it
// too, and one of them sits under a directory the tree prints — jumping the cursor into the tree
// from there would be the key doing something the screen gives no sign of doing.
func TestFoldKeysStayInTheTree(t *testing.T) {
	m, _ := treeModel(t)
	m.cursor = indexOf(t, m, rowAbout)

	m = pressRune(m, 'h')
	if kind, r := selectedKind(t, m); kind != rowAbout {
		t.Errorf("h from ABOUT.md moved the cursor to %q, want it to stay", r.name)
	}
	if !strings.Contains(m.status, "under the counter") {
		t.Errorf("h under the counter said %q, want it to say the keys belong to the tree", m.status)
	}
	m = pressRune(m, 'l')
	if !strings.Contains(m.status, "under the counter") {
		t.Errorf("l under the counter said %q, want it to say the keys belong to the tree", m.status)
	}
}

// The left and right arrows are the same two keys, because a reviewer who reaches for the arrow
// keys should not have to learn a second grammar for the tree.
func TestTheArrowKeysFoldTheTreeToo(t *testing.T) {
	m, _ := treeModel(t)
	m = press(cursorOnDir(t, m, "internal/tui/"), tea.KeyLeft)
	if !m.folded["internal/tui/"] {
		t.Error("the left arrow did not fold the directory")
	}
	m = press(m, tea.KeyRight)
	if m.folded["internal/tui/"] {
		t.Error("the right arrow did not open it again")
	}
}

// `c` is the key for a changeset too big to read row by row: the shape of it first. Pressing it
// again is the way back, and the way back has to include the directories a folded run hid.
func TestCFoldsTheWholeTreeAndOpensItAgain(t *testing.T) {
	m, _ := treeModel(t)
	before := len(m.rows)

	m = pressRune(m, 'c')
	top := 0
	for _, r := range m.rows {
		if !inFileBlock(r.kind) {
			continue // the changeset section is not the tree, and stays as it was
		}
		// What is left is the top of the tree: every directory, and the files that sit in none.
		if r.depth != 0 {
			t.Errorf("the folded tree still prints %q at depth %d:\n%s", r.path, r.depth, rowList(m))
		}
		top++
	}
	if top < 4 {
		t.Errorf("folding left %d rows, want the top of every directory:\n%s", top, rowList(m))
	}
	if !m.treeFolded(treeDirs(m.sess.Files())) {
		t.Error("c did not fold every directory, so the second press will not open them")
	}

	m = pressRune(m, 'c')
	if len(m.rows) != before {
		t.Errorf("opening the tree left %d rows, want the %d it started with:\n%s",
			len(m.rows), before, rowList(m))
	}
}

// The tree is how a big changeset gets smaller, and a historical span is exactly where a reviewer
// wants that. Folding changes nothing in the review, so the read-only gate has no business with it
// — but neither does the mark gutter, which over history is not the reviewer's to show.
func TestTheTreeFoldsOverHistoryWithoutMarks(t *testing.T) {
	m, _ := readonlyModel(t, historySel())
	if m.sess.Span().CanMark() {
		t.Fatal("this is meant to be a read-only span")
	}
	at := dirRow(t, m, "changesets/booking/")
	text := ansiCodes.ReplaceAllString(m.rowText(m.rows[at]), "")
	for _, absent := range []string{"○", "✓", "◐"} {
		if strings.Contains(text, absent) {
			t.Errorf("a directory row over history shows %q:\n%s", absent, rowList(m))
		}
	}

	m = press(m, tea.KeyEnter)
	if !m.folded["changesets/booking/"] {
		t.Error("a read-only span refused to fold, which is not a thing it changes")
	}
}

// --- a directory as a thing to diff -----------------------------------------

// The pane shows the row under the cursor, so on a directory it shows what the directory changed.
// The path it asks git for carries the trailing separator, which is what makes it the subtree and
// not a prefix of something longer.
func TestPreviewOfADirectoryIsTheSubtreesDiff(t *testing.T) {
	m, _ := treeModel(t)
	m.width, m.height = 140, 24
	m.previewOn = true
	var asked []string
	m.patchFor = func(_ context.Context, path string) Patch {
		asked = append(asked, path)
		return Patch{Lines: []string{"diff --git a/" + path + "tui.go", "@@ -1 +1,3 @@", "+func View() {}"},
			Added: 4, Deleted: 0}
	}
	m.workingFor = func(_ context.Context, _ string) Patch { return Patch{} }

	// Esc with nothing in the status line changes nothing, and every key makes the pane ask for
	// whatever the cursor has moved on to -- so the fetch has to come back from the same call.
	m = cursorOnDir(t, m, "internal/tui/")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = deliver(t, updated.(reviewModel), cmd)

	if strings.Join(asked, " ") != "internal/tui/" {
		t.Errorf("the pane asked git about %v, want the directory pathspec internal/tui/", asked)
	}
	if !strings.Contains(m.View(), "internal/tui/") {
		t.Errorf("the pane does not name the directory it is showing:\n%s", m.View())
	}
}

// git answers a directory with one numstat line per file under it. The header of a directory's
// preview is the size of the directory, so those lines have to be added rather than read one at a
// time — the old code showed the first file's counts beside a name standing for all of them.
func TestDirectoryPatchCountsEveryFileUnderIt(t *testing.T) {
	m, _ := treeModel(t)
	sess := m.sess

	one := sess.Patch(context.Background(), "internal/tui/tui.go")
	if one.Added != 2 || one.Deleted != 0 {
		t.Fatalf("one file reported +%d −%d, want +2 −0", one.Added, one.Deleted)
	}
	dir := sess.Patch(context.Background(), "internal/tui/")
	if dir.Added != 4 || dir.Deleted != 0 {
		t.Errorf("the directory reported +%d −%d, want the two files' +4 −0", dir.Added, dir.Deleted)
	}
	if len(dir.Lines) <= len(one.Lines) {
		t.Error("the directory's diff is no longer than one file's, so it is not the subtree's")
	}
}

// `d` on a directory hands the difftool the subtree, which is what git does with a directory
// pathspec: one look per file the span changed under it. `e` is the exception — an editor finds its
// own way in, and the keys that want everything under the row already exist.
func TestDiffsAndEditorsKnowWhatADirectoryIs(t *testing.T) {
	m, _ := treeModel(t)
	m = cursorOnDir(t, m, "internal/tui/")

	_, cmd := m.openDiffOfSelection()
	if cmd == nil {
		t.Error("d on a directory did nothing, want the difftool for the subtree")
	}
	updated, _ := m.openEditor()
	got := updated.(reviewModel)
	if got.status == "" || !strings.Contains(got.status, "directory") {
		t.Errorf("e on a directory said %q, want it to name the keys that do work", got.status)
	}
}
