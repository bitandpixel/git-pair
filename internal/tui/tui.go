package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

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
	modeThreads
	modeSubmit
)

type promptKind int

const (
	promptNone promptKind = iota
	promptThread
)

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

	promptKind promptKind
	input      string

	threads      []string
	threadCursor int
	status       string
	statusErr    bool
	quitting     bool
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
	m := reviewModel{ctx: ctx, sess: sess, width: 80, height: 24}
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
		m.clamp()
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
	case modeThreads:
		return m.handleThreadKey(key)
	}

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
	case key.Type == tea.KeySpace:
		m.sess.Toggle(m.cursor)
	case key.Type == tea.KeyEnter:
		return m.openDiff()
	case key.Type == tea.KeyRunes && firstRune(key) == 'e':
		return m.openEditor()
	case key.Type == tea.KeyRunes && firstRune(key) == 'a':
		return m.openAbout()
	case key.Type == tea.KeyRunes && firstRune(key) == 't':
		m.mode, m.input, m.promptKind = modePrompt, "", promptThread
		m.setStatus("New thread title (Enter to create, Esc to cancel)", false)
	case key.Type == tea.KeyRunes && firstRune(key) == 'T':
		return m.enterThreads()
	case key.Type == tea.KeyRunes && firstRune(key) == 'v':
		if err := m.sess.ToggleSpan(m.ctx); err != nil {
			m.setStatus(err.Error(), true)
		} else {
			m.clamp()
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
	case tea.KeyRunes:
		m.input += string(key.Runes)
		return m, nil
	}
	return m, nil
}

func (m reviewModel) enterThreads() (tea.Model, tea.Cmd) {
	threads, err := m.sess.Threads()
	if err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	m.mode = modeThreads
	m.threads = threads
	if m.threadCursor >= len(threads) {
		m.threadCursor = 0
	}
	if len(threads) == 0 {
		m.setStatus("No threads yet — press t to create one", false)
	} else {
		m.setStatus("", false)
	}
	return m, nil
}

func (m reviewModel) handleThreadKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Type == tea.KeyEsc, key.Type == tea.KeyRunes && firstRune(key) == 'q',
		key.Type == tea.KeyRunes && firstRune(key) == 'T':
		m.mode = modeFiles
		m.setStatus("", false)
		return m, nil
	case key.Type == tea.KeyDown, key.Type == tea.KeyRunes && firstRune(key) == 'j':
		if m.threadCursor < len(m.threads)-1 {
			m.threadCursor++
		}
	case key.Type == tea.KeyUp, key.Type == tea.KeyRunes && firstRune(key) == 'k':
		if m.threadCursor > 0 {
			m.threadCursor--
		}
	case key.Type == tea.KeyEnter:
		if m.threadCursor < len(m.threads) {
			return m.openPath(m.threads[m.threadCursor])
		}
	case key.Type == tea.KeyRunes && firstRune(key) == 't':
		m.mode, m.input, m.promptKind = modePrompt, "", promptThread
		m.setStatus("New thread title (Enter to create, Esc to cancel)", false)
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
	return m.openPath(path)
}

// --- external process handoff ----------------------------------------------

func (m reviewModel) openDiff() (tea.Model, tea.Cmd) {
	f, ok := m.selected()
	if !ok {
		return m, nil
	}
	sp := m.sess.Span()
	return m.runExternal(console.DiffToolCommand(m.sess.Repo(), sp.From, []string{f.Path}),
		"Difftool exited with an error")
}

func (m reviewModel) openEditor() (tea.Model, tea.Cmd) {
	f, ok := m.selected()
	if !ok {
		return m, nil
	}
	return m.openPath(f.Path)
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
	visible := m.visibleRows()
	for _, idx := range visible {
		f := m.sess.Files()[idx]
		mark := "○ "
		if f.Reviewed {
			mark = styleMark.Render("✓ ")
		}
		line := mark + f.Path
		if m.cursor == idx && m.mode == modeFiles {
			line = styleSelected.Render(m.truncate(line))
		}
		b.WriteString(line + "\n")
	}
	if m.scroll > 0 {
		b.WriteString(styleDim.Render(fmt.Sprintf("  (hidden above: %d)", m.scroll)) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("%d / %d reviewed\n", reviewed, total))
	b.WriteString("\n")
	b.WriteString("ABOUT.md\n")
	threads, err := m.sess.Threads()
	if err != nil {
		b.WriteString(styleErr.Render("threads: "+err.Error()) + "\n")
	} else {
		b.WriteString(fmt.Sprintf("Threads (%d)\n", len(threads)))
	}
	if m.mode == modeThreads {
		b.WriteString("\n")
		if len(threads) == 0 {
			b.WriteString(styleDim.Render("  (none yet)") + "\n")
		}
		for i, t := range threads {
			prefix := "  "
			if i == m.threadCursor {
				prefix = "> "
			}
			b.WriteString(styleDim.Render(prefix+strings.TrimPrefix(t, "changesets/")) + "\n")
		}
	}

	b.WriteString("\n")
	switch m.mode {
	case modePrompt:
		b.WriteString("New thread: " + m.input + "█\n")
	case modeSubmit:
		b.WriteString("Submit review: [b]lock  [f]eedback  [a]pprove  [esc] cancel\n")
	default:
		b.WriteString(styleDim.Render(
			"j/k move  enter difftool  e edit  space reviewed  a about  t thread  T browse  v span  s submit  q quit") + "\n")
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

// --- helpers ----------------------------------------------------------------

func (m *reviewModel) clamp() {
	total := len(m.sess.Files())
	if total == 0 {
		m.cursor, m.scroll = 0, 0
		return
	}
	if m.cursor > total-1 {
		m.cursor = total - 1
	}
	// Keep the cursor inside a window sized to the terminal, leaving room for
	// the header, footer, and status lines.
	window := m.height - 12
	if window < 5 {
		window = 5
	}
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+window {
		m.scroll = m.cursor - window + 1
	}
}

func (m *reviewModel) move(delta int) {
	total := len(m.sess.Files())
	if total == 0 {
		return
	}
	m.cursor += delta
	m.clamp()
}

func (m reviewModel) selected() (File, bool) {
	files := m.sess.Files()
	if m.cursor < 0 || m.cursor >= len(files) {
		return File{}, false
	}
	return files[m.cursor], true
}

func (m reviewModel) visibleRows() []int {
	total := len(m.sess.Files())
	window := m.height - 12
	if window < 5 {
		window = 5
	}
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
