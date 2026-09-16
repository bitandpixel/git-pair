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

// One list, two sections: the files in the span, then what the changeset says about them.
// `j` runs off the bottom of the files into ABOUT.md and the threads rather than into a
// separate mode, and Tab jumps between the halves.

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

func TestJRunsFromTheFilesIntoTheChangesetSection(t *testing.T) {
	m := navModel(t)
	m.cursor = lastIndexOfType(t, m, rowFile)

	want := []rowKind{rowAbout, rowThreadsHead, rowThread, rowThread, rowNewThread}
	for i, kind := range want {
		m = press(m, tea.KeyDown)
		got, row := selectedKind(t, m)
		if got != kind {
			t.Errorf("press %d of j: cursor on %q (kind %d), want kind %d\n%s", i+1, row.name, got, kind, rowList(m))
		}
		_ = m.View()
	}

	// The action row is the end of the list: j past it stays there.
	before := m.cursor
	m = press(m, tea.KeyDown)
	if m.cursor != before {
		t.Errorf("cursor moved from the last row to %d:\n%s", m.cursor, rowList(m))
	}

	// And k walks back out of the section into the threads it came from.
	if got, _ := selectedKind(t, press(m, tea.KeyUp)); got != rowThread {
		t.Errorf("k landed on kind %d, want a thread", got)
	}
}

func TestTabSwitchesBetweenTheTwoSections(t *testing.T) {
	m := navModel(t)
	m.cursor = 0

	toSection := press(m, tea.KeyTab)
	if got, row := selectedKind(t, toSection); got != rowAbout {
		t.Errorf("Tab from the files landed on %q (kind %d), want ABOUT.md", row.name, got)
	}

	back := press(toSection, tea.KeyTab)
	if got, row := selectedKind(t, back); got != rowFile {
		t.Errorf("Tab from the changeset section landed on %q (kind %d), want the first file", row.name, got)
	}
	if back.cursor != 0 {
		t.Errorf("Tab back landed on row %d, want the first file row", back.cursor)
	}

	// Shift+Tab is the same toggle, so a reviewer whose terminal sends it for the other
	// direction still gets between the two sections.
	if got, _ := selectedKind(t, press(toSection, tea.KeyShiftTab)); got != rowFile {
		t.Error("shift-tab should return to the files")
	}
	if got, _ := selectedKind(t, press(m, tea.KeyShiftTab)); got != rowAbout {
		t.Errorf("shift-tab from the files landed on kind %d, want the changeset section", got)
	}
}

func TestThreadsHeadingCollapsesAndExpands(t *testing.T) {
	m := navModel(t)
	open := len(m.rows)
	head := indexOf(t, m, rowThreadsHead)
	m.cursor = head

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
	if collapsed.cursor != head {
		t.Errorf("collapse moved the cursor from the heading at %d to %d", head, collapsed.cursor)
	}
	if strings.Contains(collapsed.View(), "locking.md") {
		t.Error("a collapsed section still showed its threads")
	}
	if !strings.Contains(m.View(), "locking.md") {
		t.Error("the expanded view does not show the nested threads")
	}

	// Enter on the heading is the same toggle, for a reviewer who never reads the hint.
	expanded := press(collapsed, tea.KeyEnter)
	if !expanded.threadsOpen {
		t.Error("Enter on the heading did not expand the threads")
	}
	if len(expanded.rows) != open {
		t.Errorf("re-expanding gave %d rows, want the original %d", len(expanded.rows), open)
	}
	if expanded.cursor != head {
		t.Errorf("expanding moved the cursor to %d, want the heading at %d", expanded.cursor, head)
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
	m.cursor = indexOf(t, m, rowThreadsHead)
	after, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("the heading launched a process instead of collapsing")
	}
	if after.(reviewModel).threadsOpen {
		t.Error("Enter on the heading did not collapse it")
	}

	m.cursor = indexOf(t, m, rowNewThread)
	after, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	prompting := after.(reviewModel)
	if prompting.mode != modePrompt || prompting.promptKind != promptThread {
		t.Errorf("Enter on %q gave mode %d/kind %d, want the thread prompt",
			newThreadLabel, prompting.mode, prompting.promptKind)
	}

	// A file row hands off to the difftool and a thread row to the editor; both return a
	// command, and only the table above distinguishes which program.
	m = navModel(t)
	m.cursor = indexOf(t, m, rowThread)
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Error("Enter on a thread opened nothing")
	}
	m.cursor = indexOfNameBySuffix(t, m, ".go")
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

	m.cursor = indexOf(t, m, rowAbout)
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

	m.cursor = indexOf(t, m, rowThread)
	m = press(m, tea.KeySpace)
	if marked() != 0 {
		t.Error("marking a thread marked a file")
	}

	m.cursor = indexOf(t, m, rowFile)
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
	for _, r := range m.rows[indexOf(t, m, rowThreadsHead)+1:] {
		if r.kind == rowThread && filepath.Base(r.path) != r.name {
			t.Errorf("thread row prints %q, want the file name of %q", r.name, r.path)
		}
	}
	// Nested rows are indented, so the group reads as one block under its heading.
	view := m.View()
	if !strings.Contains(view, threadIndent+"locking.md") {
		t.Errorf("threads are not nested under the heading:\n%s", view)
	}
	// Expanded, the heading does not repeat a count that the rows below it already give.
	if !strings.Contains(view, "▾ Threads\n") && !strings.Contains(view, "▾ Threads\r") {
		if strings.Contains(view, "▾ Threads (") {
			t.Errorf("the expanded heading still shows a count:\n%s", view)
		} else {
			t.Errorf("the thread heading is missing from:\n%s", view)
		}
	}
	collapsed := m
	collapsed.threadsOpen = false
	if got := collapsed.rowText(collapsed.rows[indexOf(t, m, rowThreadsHead)]); !strings.Contains(ansiCodes.ReplaceAllString(got, ""), "▸ Threads (2)") {
		t.Errorf("the collapsed heading reads %q, want it to count the threads it hides", got)
	}
}

func TestRefreshKeepsTheCursorOnTheSameRow(t *testing.T) {
	m := navModel(t)
	m.cursor = indexOf(t, m, rowThread)
	_, before := selectedKind(t, m)

	// A thread created elsewhere — in an editor, or another window — must not move the
	// reviewer off the row they were reading.
	path := filepath.Join(m.sess.Repo().Dir, "changesets", "booking", "third.md")
	if err := os.WriteFile(path, []byte("# Thread: third\n"), 0o644); err != nil {
		t.Fatalf("write thread: %v", err)
	}
	m.refresh()

	after, row := selectedKind(t, m)
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

// The counter counts files, so the files and the counter are one block and the changeset
// section sits below it: the block above answers "what did the diff touch", the one below
// "what is the review made of". Tab then reads as skipping to the next block.
func TestChangesetSectionRendersBelowTheCounter(t *testing.T) {
	m := navModel(t)
	lines := strings.Split(ansiCodes.ReplaceAllString(m.View(), ""), "\n")

	counter := lineWithPrefix(lines, "0 / ")
	if counter < 0 {
		t.Fatalf("no reviewed counter in:\n%s", strings.Join(lines, "\n"))
	}
	about := lineWithExact(lines, "ABOUT.md")
	head := lineWithPrefix(lines, "\u25be Threads") // expanded: no count on the heading
	if about < 0 || head < 0 {
		t.Fatalf("ABOUT.md (%d) or the thread heading (%d) is missing from:\n%s",
			about, head, strings.Join(lines, "\n"))
	}
	if about < counter || head < counter {
		t.Errorf("the changeset section is not below the counter (counter %d, ABOUT.md %d, heading %d)",
			counter, about, head)
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
		if at > counter {
			t.Errorf("file %q renders at %d, below the counter at %d", r.name, at, counter)
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
		if l == strings.Repeat("─", 60) {
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
		if lines[rule+1+i] != want {
			t.Errorf("line %d after the rule = %q, want the shortcut %q", i+1, lines[rule+1+i], want)
		}
	}
	for _, l := range lines[:rule] {
		if strings.Contains(l, "j/k move") {
			t.Errorf("the shortcut bar starts above the rule:\n%s", strings.Join(lines, "\n"))
		}
	}
}

// `d` is the difftool for whatever the cursor is on, so a reviewer does not have to be on the
// file block to diff a file. Rows that cannot be diffed say why instead of opening a difftool
// with nothing in it.
func TestDDiffsTheSelectedRow(t *testing.T) {
	m := navModel(t)
	if !strings.Contains(strings.Join(m.helpLines(), " "), "d diff") {
		t.Errorf("the shortcut bar does not mention d: %q", m.helpLines())
	}

	// A file row in the span: the same handoff Enter gives.
	m.cursor = indexOfNameBySuffix(t, m, ".go")
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
	m.cursor = about
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
	m.cursor = indexOf(t, m, rowThread)
	thread := m.rows[m.cursor]
	after, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = after.(reviewModel)
	if cmd == nil {
		t.Errorf("d on %q opened nothing, want the editor", thread.name)
	}
	if m.status != "" {
		t.Errorf("d on %q reported %q before the handoff, where nothing can read it", thread.name, m.status)
	}

	// The heading is not a file at all.
	m.cursor = indexOf(t, m, rowThreadsHead)
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
	m.cursor = indexOf(t, m, rowThread) // a thread the span does not touch
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
	m.cursor = indexOfNameBySuffix(t, m, ".go")
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")}); cmd == nil {
		t.Fatal("d on a file did not hand off")
	}
	updated, _ = m.Update(externalDoneMsg{})
	if got := updated.(reviewModel).status; got != "" {
		t.Errorf("after a real diff the status = %q, want nothing", got)
	}

	// A handoff that failed reports the failure, and drops the note it was carrying.
	m.cursor = indexOf(t, m, rowThread)
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
	m.cursor = indexOf(t, m, rowAbout)
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
