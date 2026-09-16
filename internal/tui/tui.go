package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"gitpr/internal/console"
	gitmodel "gitpr/internal/model"
	"gitpr/internal/reviewops"
)

// ErrQuit is returned when the reviewer leaves the session normally.
var ErrQuit = errors.New("review session ended")

// mode is the session's input mode.
type mode int

const (
	modeFiles mode = iota
	modePrompt
	modeSubmit
)

type promptKind int

const (
	promptNone promptKind = iota
	promptThread
)

// rowKind says what a navigable line stands for.
type rowKind uint8

const (
	rowFile rowKind = iota
	rowAbout
	rowThreadsHead
	rowThread
	rowNewThread
)

// Labels the section rows print. A thread is shown by file name: they all live in one
// directory, so the name is the whole distinction.
const (
	newThreadLabel = "+ new thread…"
	threadIndent   = "    "
)

// row is one line of the navigable list: a file in the span, or an entry of the changeset
// section — ABOUT.md, the thread heading, one thread nested under it, or the action that
// creates another. Both sections are in one list because a reviewer works down the screen:
// the code, then what the changeset says about it, with `j` running off the bottom of the
// files and into the artifacts instead of into a separate mode.
type row struct {
	kind rowKind
	path string // repository-relative, for the rows that name a file
	name string // what the row prints
	file int    // index into Session.Files() for a file row, -1 otherwise
	note string // set on the thread heading when the threads could not be listed
}

// action is what Enter does with a row.
type action uint8

const (
	actionNone action = iota
	actionDiff
	actionEdit
	actionAbout
	actionCollapse
	actionNewThread
)

// activateBy is the Enter table: one row kind, one action. A file opens in the difftool
// because that is the thing under review; the changeset artifacts are markdown a reviewer
// reads, so they open in the editor. The heading toggles its own group, and the last row of
// the group creates another thread.
func activateBy(r row) action {
	switch r.kind {
	case rowFile:
		return actionDiff
	case rowThread:
		return actionEdit
	case rowAbout:
		return actionAbout
	case rowThreadsHead:
		return actionCollapse
	case rowNewThread:
		return actionNewThread
	}
	return actionNone
}

// externalDoneMsg reports that a launched editor or difftool has exited.
type externalDoneMsg struct {
	err   error
	label string
}

// reviewModel is the Bubble Tea model. All review state lives in Session;
// reviewModel holds only view state.
type reviewModel struct {
	ctx  context.Context
	sess *Session

	mode   mode
	cursor int
	scroll int
	width  int
	height int

	// rows is the navigable list: the files in the span, then the changeset section.
	// It is rebuilt by refresh whenever the session or the view could have changed.
	rows        []row
	threadsOpen bool

	promptKind promptKind
	input      string

	status    string
	statusErr bool
	quitting  bool
	// submitted is the one-line summary of a review submitted from inside the
	// session, printed after the alt screen closes.
	submitted string
}

// Run starts the review session. It blocks until the reviewer quits.
func Run(ctx context.Context, opts Options) error {
	sess, err := NewSession(ctx, opts)
	if err != nil {
		return err
	}
	m := reviewModel{ctx: ctx, sess: sess, width: 80, height: 24, threadsOpen: true}
	m.refresh()
	if n := sess.Resumed(); n > 0 {
		m.setStatus(fmt.Sprintf("resumed %d reviewed mark%s from an earlier session", n, plural(n)), false)
	}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	final, err := p.Run()
	if err != nil {
		return err
	}
	if fm, ok := final.(reviewModel); ok {
		if fm.submitted != "" {
			out := opts.Out
			if out == nil {
				out = os.Stdout
			}
			fmt.Fprintln(out, fm.submitted)
		}
		if fm.quitting {
			return ErrQuit
		}
	}
	return nil
}

func (m reviewModel) Init() tea.Cmd { return nil }

func (m reviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.refresh()
		return m, nil

	case externalDoneMsg:
		// An editor or difftool just exited: refresh repository state so the
		// file list and review marks reflect what it changed (PRD §15).
		if msg.err != nil {
			m.setStatus(fmt.Sprintf("%s: %v", msg.label, msg.err), true)
			return m, nil
		}
		if err := m.sess.Reload(m.ctx); err != nil {
			m.setStatus(err.Error(), true)
		} else {
			m.setStatus("", false)
		}
		// The editor may have written a new thread, so the section below the files
		// has to be listed again.
		m.refresh()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m reviewModel) handleKey(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch m.mode {
	case modeSubmit:
		return m.handleSubmitKey(key)
	case modePrompt:
		return m.handlePromptKey(key)
	}
	m.refresh()

	switch {
	case key.Type == tea.KeyCtrlC, key.Type == tea.KeyCtrlD,
		(key.Type == tea.KeyRunes && len(key.Runes) == 1 && key.Runes[0] == 'q'):
		m.quitting = true
		return m, tea.Quit
	case key.Type == tea.KeyEsc && m.status != "":
		m.setStatus("", false)
		return m, nil
	case key.Type == tea.KeyDown, key.Type == tea.KeyRunes && firstRune(key) == 'j':
		m.move(1)
	case key.Type == tea.KeyUp, key.Type == tea.KeyRunes && firstRune(key) == 'k':
		m.move(-1)
	case key.Type == tea.KeyTab, key.Type == tea.KeyShiftTab:
		m.jumpSection()
	case key.Type == tea.KeySpace:
		m.toggleMark()
	case key.Type == tea.KeyEnter:
		return m.activate()
	case key.Type == tea.KeyRunes && firstRune(key) == 'e':
		return m.openEditor()
	case key.Type == tea.KeyRunes && firstRune(key) == 'a':
		return m.openAbout()
	case key.Type == tea.KeyRunes && firstRune(key) == 't':
		m.mode, m.input, m.promptKind = modePrompt, "", promptThread
		m.setStatus("New thread title (Enter to create, Esc to cancel)", false)
	case key.Type == tea.KeyRunes && firstRune(key) == 'T':
		m.toggleThreads()
	case key.Type == tea.KeyRunes && firstRune(key) == 'v':
		if err := m.sess.ToggleSpan(m.ctx); err != nil {
			m.setStatus(err.Error(), true)
		} else {
			m.refresh()
			m.setStatus("", false)
		}
	case key.Type == tea.KeyRunes && firstRune(key) == 's':
		if !m.sess.CanToggleSpan() {
			// Still allowed: submitting a first review is legitimate.
			m.setStatus("", false)
		}
		m.mode = modeSubmit
	}
	return m, nil
}

// toggleMark marks the file under the cursor reviewed, or explains why the row it is on
// cannot be: ABOUT.md and threads are read rather than diffed, and the group rows are not
// things at all.
func (m *reviewModel) toggleMark() {
	r, ok := m.selectedRow()
	if !ok {
		return
	}
	if r.kind != rowFile {
		m.setStatus("reviewed marks apply to file rows: "+r.name+" is not one", false)
		return
	}
	m.sess.Toggle(r.file)
	// Marks persist locally so the review can be resumed. A failure is reported
	// and otherwise ignored: the review itself does not depend on them.
	if err := m.sess.SaveMarks(m.ctx); err != nil {
		m.setStatus("marks not saved: "+err.Error(), true)
	} else {
		m.setStatus("", false)
	}
}

// toggleThreads collapses or expands the thread list under its heading.
func (m *reviewModel) toggleThreads() {
	m.threadsOpen = !m.threadsOpen
	m.refresh()
}

func (m reviewModel) handleSubmitKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Type == tea.KeyEsc || (key.Type == tea.KeyRunes && firstRune(key) == 'q') {
		m.mode = modeFiles
		m.setStatus("Review not submitted", false)
		return m, nil
	}
	var outcome gitmodel.Outcome
	switch {
	case key.Type == tea.KeyRunes && firstRune(key) == 'b':
		outcome = gitmodel.OutcomeBlock
	case key.Type == tea.KeyRunes && firstRune(key) == 'f':
		outcome = gitmodel.OutcomeFeedback
	case key.Type == tea.KeyRunes && (firstRune(key) == 'a' || firstRune(key) == 'p'):
		outcome = gitmodel.OutcomeApprove
	default:
		return m, nil
	}
	m.mode = modeFiles
	result, err := reviewops.Submit(m.ctx, m.sess.Repo(), m.sess.Changeset(), m.sess.Summary(),
		outcome, "", true)
	if err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	files := "no files"
	if n := len(result.Files); n == 1 {
		files = "1 file"
	} else if n > 1 {
		files = fmt.Sprintf("%d files", n)
	}
	// Submitting ends the session. The review is committed and the ref has moved,
	// so there is nothing left to review in this run: staying put would invite a
	// second submission of the same state, and the file list would be describing
	// a span that no longer means what it did.
	m.submitted = fmt.Sprintf("Submitted %s review (%s) covering %s", outcome, short(result.Commit), files)
	m.quitting = true
	return m, tea.Quit
}

func (m reviewModel) handlePromptKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.mode, m.input, m.promptKind = modeFiles, "", promptNone
		m.setStatus("", false)
		return m, nil
	case tea.KeyEnter:
		title := strings.TrimSpace(m.input)
		m.mode, m.input, m.promptKind = modeFiles, "", promptNone
		if title == "" {
			m.setStatus("A thread needs a title", true)
			return m, nil
		}
		return m.openThreadTitle(title)
	case tea.KeyBackspace:
		runes := []rune(m.input)
		if len(runes) > 0 {
			m.input = string(runes[:len(runes)-1])
		}
		return m, nil
	case tea.KeySpace:
		// A lone space arrives as KeySpace rather than KeyRunes, and titles are prose:
		// "Does the lock cover the map?" used to arrive as "Doesthelockcoverthemap?".
		m.input += " "
	case tea.KeyRunes:
		m.input += string(key.Runes)
		return m, nil
	}
	return m, nil
}

func (m reviewModel) openThreadTitle(title string) (tea.Model, tea.Cmd) {
	path, created, err := m.sess.Changeset().EnsureThread(m.sess.Repo(), title)
	if err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	if created {
		m.setStatus("Created "+path, false)
	} else {
		m.setStatus("Opened existing thread "+path, false)
	}
	// The new file belongs in the section the reviewer was just in, so put the cursor on
	// it instead of leaving them where the prompt started.
	m.refresh()
	for i, r := range m.rows {
		if r.kind == rowThread && r.path == path {
			m.cursor = i
			break
		}
	}
	m.clamp()
	return m.openPath(path)
}

// --- external process handoff ----------------------------------------------

// activate does whatever the row under the cursor is for. The mapping from row to action is
// activateBy, kept apart from the process handoff so the table is testable without handing
// the terminal to an editor.
func (m reviewModel) activate() (tea.Model, tea.Cmd) {
	r, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	switch activateBy(r) {
	case actionDiff:
		return m.openDiff(r.path)
	case actionEdit:
		return m.openPath(r.path)
	case actionAbout:
		return m.openAbout()
	case actionCollapse:
		m.toggleThreads()
	case actionNewThread:
		m.mode, m.input, m.promptKind = modePrompt, "", promptThread
		m.setStatus("New thread title (Enter to create, Esc to cancel)", false)
	}
	return m, nil
}

func (m reviewModel) openDiff(path string) (tea.Model, tea.Cmd) {
	sp := m.sess.Span()
	return m.runExternal(console.DiffToolCommand(m.sess.Repo(), sp.From, []string{path}),
		"Difftool exited with an error")
}

// openEditor edits the row under the cursor: a file, a thread, or ABOUT.md.
func (m reviewModel) openEditor() (tea.Model, tea.Cmd) {
	r, ok := m.selectedRow()
	if !ok || r.path == "" {
		return m, nil
	}
	return m.openPath(r.path)
}

func (m reviewModel) openAbout() (tea.Model, tea.Cmd) {
	if _, err := m.sess.Changeset().EnsureAbout(m.sess.Repo()); err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	return m.openPath(m.sess.AboutPath())
}

func (m reviewModel) openPath(relPath string) (tea.Model, tea.Cmd) {
	repo := m.sess.Repo()
	cmd, err := console.EditorCommand(repo, absPath(repo.Dir, relPath))
	if err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	return m.runExternal(cmd, "Editor exited with an error")
}

// runExternal hands the terminal to an editor or difftool. tea.ExecCommand
// suspends the program, attaches the child to the real terminal, and restores
// the screen afterwards, which is what PRD §15 requires.
// runExternal hands the terminal to an editor or difftool. tea.ExecProcess
// suspends the renderer, gives the child process the real terminal, and resumes
// drawing when it exits — which is what PRD §15 requires. The callback turns the
// child's exit status into a message so the screen can refresh or report.
func (m reviewModel) runExternal(cmd *exec.Cmd, label string) (tea.Model, tea.Cmd) {
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return externalDoneMsg{err: err, label: label}
	})
}

// --- view -------------------------------------------------------------------

var (
	styleSelected = lipgloss.NewStyle().Reverse(true)
	styleDim      = lipgloss.NewStyle().Faint(true)
	styleErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	styleMark     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	styleSpan     = lipgloss.NewStyle().Bold(true)
)

func (m reviewModel) View() string {
	if m.quitting {
		return ""
	}
	var b strings.Builder
	h := m.sess.Header()
	reviewed, total := m.sess.Count()

	b.WriteString(styleSpan.Render(h.Title) + "\n")
	b.WriteString(styleDim.Render("base: "+h.Base) + "  " +
		styleDim.Render("span: "+m.spanName(h.SpanLabel)) + "\n")
	b.WriteString("\n")

	if total == 0 {
		b.WriteString(styleDim.Render("(no changed files in this span)") + "\n")
	}
	files, section := m.window()
	for _, r := range files {
		b.WriteString(m.line(r) + "\n")
	}
	if m.scroll > 0 {
		b.WriteString(styleDim.Render(fmt.Sprintf("  (hidden above: %d)", m.scroll)) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("%d / %d reviewed\n", reviewed, total))
	b.WriteString("\n")
	// The changeset section is below the counter it does not belong to, so the block above
	// reads as "these are the files the diff touched" and the block below as "this is what
	// the review is made of".
	for _, r := range section {
		b.WriteString(m.line(r) + "\n")
	}

	switch m.mode {
	case modePrompt:
		b.WriteString("New thread: " + m.input + "█\n")
	case modeSubmit:
		for _, line := range m.helpLines() {
			b.WriteString(line + "\n")
		}
	default:
		for _, line := range m.helpLines() {
			b.WriteString(styleDim.Render(line) + "\n")
		}
	}
	if m.status != "" {
		if m.statusErr {
			b.WriteString(styleErr.Render(m.status) + "\n")
		} else {
			b.WriteString(m.status + "\n")
		}
	}
	return b.String()
}

func (m reviewModel) spanName(label string) string {
	if m.sess.Unreviewed() {
		return "unreviewed"
	}
	return label
}

// helpText is the shortcut helper for the current mode. Shortcut groups are separated by
// two spaces; wrapping breaks between those groups, never inside one.
func (m reviewModel) helpText() string {
	switch m.mode {
	case modeSubmit:
		return "Submit review: [b]lock  [f]eedback  [a]pprove  [esc] cancel"
	case modePrompt:
		return "" // the thread prompt is the input line, not help
	}
	threadsHint := "T show threads"
	if m.threadsOpen {
		threadsHint = "T hide threads"
	}
	return "j/k move  tab section  enter open  e edit  space reviewed  a about  t new thread  " +
		threadsHint + "  v span  s submit  q quit"
}

// helpLines is helpText fitted to the terminal width. A narrow window gets the overflow on
// the next line instead of leaving it to the terminal, which would break a shortcut in half.
func (m reviewModel) helpLines() []string {
	return wrapGroups(m.helpText(), m.width)
}

// wrapGroups packs groups separated by two or more spaces into lines no wider than width,
// joined by the same two-space gap. A group wider than width gets a line to itself: cutting
// a word is worse than one line that overflows. width <= 0 leaves the text unwrapped.
func wrapGroups(text string, width int) []string {
	if text == "" || width <= 0 {
		return []string{text}
	}
	const gap = "  "
	var lines []string
	current := ""
	for _, group := range strings.Split(text, gap) {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		switch {
		case current == "":
			current = group
			continue
		case utf8.RuneCountInString(current)+len(gap)+utf8.RuneCountInString(group) <= width:
			current += gap + group
			continue
		}
		lines = append(lines, current)
		current = group
	}
	if current != "" || len(lines) == 0 {
		lines = append(lines, current)
	}
	return lines
}

// --- helpers ----------------------------------------------------------------

// refresh rebuilds the navigable list from the session, keeping the cursor on the row it was
// on while that row still exists — a thread created in the editor, a rescan after a difftool
// closed, or a collapse should not move the reviewer somewhere else.
func (m *reviewModel) refresh() {
	var keep *row
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		keep = &m.rows[m.cursor]
	}
	m.buildRows()
	if keep != nil {
		for i, r := range m.rows {
			if r.kind == keep.kind && r.path == keep.path {
				m.cursor = i
				break
			}
		}
	}
	m.clamp()
}

func (m *reviewModel) buildRows() {
	var rows []row
	for i, f := range m.sess.Files() {
		rows = append(rows, row{kind: rowFile, path: f.Path, name: f.Path, file: i})
	}
	rows = append(rows, row{kind: rowAbout, path: m.sess.AboutPath(),
		name: filepath.Base(m.sess.AboutPath()), file: -1})

	// Nothing is filtered out of the file list above: a thread or ABOUT.md that changed in
	// the span is part of what is under review, so it keeps its file row as well as its
	// place in the section below. The section is the shortcut to read them; the list is the
	// record of what the diff did.
	threads, err := m.sess.Threads()
	head := row{kind: rowThreadsHead, name: fmt.Sprintf("Threads (%d)", len(threads)), file: -1}
	if err != nil {
		head.note = err.Error()
	}
	rows = append(rows, head)
	if m.threadsOpen {
		for _, path := range threads {
			rows = append(rows, row{kind: rowThread, path: path, name: filepath.Base(path), file: -1})
		}
		rows = append(rows, row{kind: rowNewThread, name: newThreadLabel, file: -1})
	}
	m.rows = rows
}

// jumpSection is Tab: it toggles between the two things a reviewer works through, the files
// and the changeset artifacts under them, landing on the first row of the other. One key
// both ways, because a key that only goes one direction leaves the reviewer stuck there.
func (m *reviewModel) jumpSection() {
	r, ok := m.selectedRow()
	if !ok {
		return
	}
	toFiles := r.kind != rowFile
	for i, candidate := range m.rows {
		if (candidate.kind == rowFile) == toFiles {
			m.cursor = i
			m.clamp()
			return
		}
	}
}

// rowText renders one row. Only a file row carries a reviewed mark: the artifacts below it
// are read rather than diffed.
func (m reviewModel) rowText(r row) string {
	switch r.kind {
	case rowFile:
		mark := "○ "
		if f := m.sess.Files(); r.file >= 0 && r.file < len(f) && f[r.file].Reviewed {
			mark = styleMark.Render("✓ ")
		}
		return mark + r.name
	case rowAbout:
		return r.name
	case rowThreadsHead:
		arrow := "▸ "
		if m.threadsOpen {
			arrow = "▾ "
		}
		if r.note != "" {
			return styleErr.Render("threads: " + r.note)
		}
		return styleDim.Render(arrow + r.name)
	case rowThread:
		return threadIndent + r.name
	case rowNewThread:
		return styleDim.Render(threadIndent + r.name)
	}
	return r.name
}

func (m *reviewModel) clamp() {
	total := len(m.rows)
	if total == 0 {
		m.cursor, m.scroll = 0, 0
		return
	}
	if m.cursor < 0 {
		// Without this, k at the top drives the cursor negative and View indexes
		// files[-1], which panics the whole program.
		m.cursor = 0
	}
	if m.cursor > total-1 {
		m.cursor = total - 1
	}
	// Keep the cursor inside a window sized to the terminal, leaving room for
	// the header, footer, and status lines.
	window := m.windowRows()
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+window {
		m.scroll = m.cursor - window + 1
	}
}

func (m *reviewModel) move(delta int) {
	total := len(m.rows)
	if total == 0 {
		return
	}
	m.cursor += delta
	m.clamp()
}

func (m reviewModel) selectedRow() (row, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return row{}, false
	}
	return m.rows[m.cursor], true
}

// renderedRow is a row with its position in the list, so the two blocks can be drawn apart
// and still know which one carries the cursor.
type renderedRow struct {
	row
	index int
}

// window splits the rows inside the scroll window into the two blocks View draws: the files,
// then the changeset section below the reviewed counter. One window over one list, because
// the cursor walks straight from one block into the other.
func (m reviewModel) window() (files, section []renderedRow) {
	for _, idx := range m.visibleRows() {
		r := renderedRow{row: m.rows[idx], index: idx}
		if m.rows[idx].kind == rowFile {
			files = append(files, r)
		} else {
			section = append(section, r)
		}
	}
	return files, section
}

// line renders one row, highlighted when the cursor is on it.
func (m reviewModel) line(r renderedRow) string {
	text := m.rowText(r.row)
	if m.cursor == r.index {
		return styleSelected.Render(m.truncate(text))
	}
	return text
}

// windowRows is how many rows fit between the header and the footer.
func (m reviewModel) windowRows() int {
	window := m.height - m.chromeRows()
	if window < 5 {
		return 5
	}
	return window
}

// chromeRows counts the lines View writes outside the row list: the title, the base and span
// line, the blank under them, the blank above the counter, the counter, the blank under it,
// the blank above the helper, and then the helper itself, the "hidden above" note while
// scrolled, and the status line when there is one. The changeset section is part of the row
// list, so it is not here — only the counter that separates the two blocks is.
func (m reviewModel) chromeRows() int {
	chrome := 7 + len(m.helpLines())
	if m.scroll > 0 {
		chrome++
	}
	if m.status != "" {
		chrome++
	}
	return chrome
}

func (m reviewModel) visibleRows() []int {
	total := len(m.rows)
	window := m.windowRows()
	var rows []int
	for i := m.scroll; i < total && i < m.scroll+window; i++ {
		rows = append(rows, i)
	}
	return rows
}

func (m reviewModel) truncate(line string) string {
	if m.width <= 0 || len(line) <= m.width {
		return line
	}
	return line[:m.width-1] + "…"
}

func (m *reviewModel) setStatus(text string, isErr bool) {
	m.status, m.statusErr = text, isErr
}

func firstRune(k tea.KeyMsg) rune {
	if len(k.Runes) == 0 {
		return 0
	}
	return k.Runes[0]
}

func absPath(root, rel string) string {
	if strings.HasPrefix(rel, "/") {
		return rel
	}
	return root + "/" + rel
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
