package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"gitpr/internal/console"
	gitmodel "gitpr/internal/model"
	"gitpr/internal/reviewops"
	"gitpr/internal/span"
)

// ErrQuit is returned when the reviewer leaves the session normally.
var ErrQuit = errors.New("review session ended")

// mode is the session's input mode.
type mode int

const (
	modeFiles mode = iota
	modePrompt
	modeSubmit
	// modeSpan is the `V` screen: a pending base and head, neither applied until Enter.
	modeSpan
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
	kind  rowKind
	path  string // repository-relative, for the rows that name a file
	name  string // what the row prints
	file  int    // index into Session.Files() for a file row, -1 otherwise
	note  string // set on the thread heading when the threads could not be listed
	count int    // how many threads the heading is standing in for
}

// action is what Enter does with a row.
type action uint8

const (
	actionNone action = iota
	actionDiff
	actionEdit
	// actionArtifact is Enter on a changeset document: the difftool when the span has a real
	// comparison for it, the editor when it does not. See openArtifact.
	actionArtifact
	actionCollapse
	actionNewThread
)

// activateBy is the Enter table: one row kind, one action. A file opens in the difftool
// because that is the thing under review. The changeset documents are markdown, so they get
// openArtifact: what a returning reviewer wants from ABOUT.md or a thread is usually the two
// or three lines the author rewrote after the last review, and a diff is the only way to see
// exactly those — while a document the changeset invented has no comparison worth opening.
// The heading toggles its own group, and the last row of the group creates another thread.
func activateBy(r row) action {
	switch r.kind {
	case rowFile:
		return actionDiff
	case rowThread, rowAbout:
		return actionArtifact
	case rowThreadsHead:
		return actionCollapse
	case rowNewThread:
		return actionNewThread
	}
	return actionNone
}

// patchKind is which of the two diffs a patch answers: the author's span, or the reviewer's own
// uncommitted edits. Both arrive as git's coloured bytes and look alike, so which one it is has to
// travel with the answer rather than be guessed from it.
type patchKind int

const (
	patchSpan patchKind = iota
	patchWorking
)

// previewMsg carries a patch back from git. The fetch runs off the event loop: reading a large
// diff is git's work, and the interface should not stop moving the cursor while it happens.
type previewMsg struct {
	path  string
	patch Patch
	kind  patchKind
}

// externalDoneMsg reports that a launched editor or difftool has exited. What the caller
// wanted read afterwards is held in reviewModel.pendingNote: a status set before the handoff
// is buried under the child's own screen, so it has to be said on the way back.
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
	inSpan      map[string]bool
	threadsOpen bool

	promptKind promptKind
	input      string

	status    string
	statusErr bool
	// patchFor is the seam tests use instead of running git.
	patchFor func(context.Context, string) Patch
	// workingFor is the same seam for the reviewer's own uncommitted edits.
	workingFor func(context.Context, string) Patch
	// The preview pane. previewPath is what it shows and previewOffset where in it that pane is;
	// patches and working hold what git already answered, so moving back to a file costs nothing.
	previewOn     bool
	previewPath   string
	previewOffset int
	patches       map[string]Patch
	working       map[string]Patch
	// pendingNote is what goes in the status line when the editor or difftool currently
	// holding the terminal exits. Every handoff assigns it, so a note can never outlive the
	// child it was written for.
	pendingNote string
	// pick is the `V` screen's state: pending endpoints, cursors, and any drill-in list.
	// Only meaningful while mode is modeSpan.
	pick spanPicker
	// out is the terminal the session renders on, which is where a cleared screen has to be
	// written when a tool hands it back.
	out      io.Writer
	quitting bool
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
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	// The session takes its own alt screen rather than asking bubbletea for one, because of
	// what happens on a handoff: tea.ExecProcess releases the terminal before starting the
	// child, and for bubbletea releasing means *exiting* the alt screen. The shell's
	// scrollback is then exposed for as long as the editor takes to paint — tens of
	// milliseconds for vim, hundreds for the heavier ones — which is the flash people see,
	// and the tool's own exit messages are left on that screen behind the prompt. With the
	// alt screen ours, the child inherits it and paints over the session, so the screen
	// behind is never shown and never dirtied. bubbletea then renders in its inline mode,
	// which tracks its own frame relative to the cursor: equivalent here, as long as the
	// screen is cleared and homed whenever something else has had it (see externalDoneMsg).
	fmt.Fprint(out, enterAltScreen)
	leftScreen := false
	defer func() {
		if !leftScreen {
			// A panic must not leave the user's terminal looking at a screen that no longer
			// exists, so this is the last line of defence rather than the normal path.
			fmt.Fprint(out, exitAltScreen)
		}
	}()

	m := reviewModel{ctx: ctx, sess: sess, out: out, width: 80, height: 24, threadsOpen: true, previewOn: true}
	m.refresh()
	if n := sess.Resumed(); n > 0 {
		m.setStatus(fmt.Sprintf("resumed %d reviewed mark%s from an earlier session", n, plural(n)), false)
	}
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithOutput(out))
	final, err := p.Run()
	// Back on the shell's screen before anything is printed, so the submitted summary and any
	// error land where the user will still be looking.
	fmt.Fprint(out, exitAltScreen)
	leftScreen = true
	if err != nil {
		return err
	}
	if fm, ok := final.(reviewModel); ok {
		if fm.submitted != "" {
			fmt.Fprintln(out, fm.submitted)
		}
		if fm.quitting {
			return ErrQuit
		}
	}
	return nil
}

// The session's own alt screen. These are the sequences bubbletea writes for
// tea.WithAltScreen — the DEC private mode that switches screens and saves the cursor, plus a
// clear and home, plus an explicit cursor-visibility reset because some terminals keep cursor
// state per screen — and the reason to write them by hand is the handoff: bubbletea exits the
// screen it owns before starting a child, and this one is not its own to exit.
const (
	enterAltScreen = "\x1b[?1049h\x1b[2J\x1b[H\x1b[?25h"
	exitAltScreen  = "\x1b[?1049l\x1b[?25h"
)

// clearScreen erases the visible screen and homes the cursor, leaving scrollback alone:
// whatever the child printed is still in history if anyone wants it.
func clearScreen(w io.Writer) {
	fmt.Fprint(w, "\x1b[2J\x1b[H")
}

func (m reviewModel) Init() tea.Cmd { return driftTick() }

// driftCheckEvery is how often the session asks git whether its named-ref endpoints have
// moved since the span pinned them. Three seconds is long enough that nobody waits on it and
// short enough that a branch moved in another window shows up while the reviewer is still in
// the screen; a span with no ref endpoint asks git nothing at all.
const driftCheckEvery = 3 * time.Second

// driftCheckMsg asks for a check; the check itself runs off the event loop and answers with
// a driftMsg, the same way the preview fetch works.
type driftCheckMsg struct{}

// driftMsg carries the answer, with the span it was about. A slow check that comes back
// after the reviewer stepped to another span describes a span nobody is looking at, so it is
// dropped rather than shown.
type driftMsg struct {
	moved    []span.Drift
	from, to string
}

func driftTick() tea.Cmd {
	return tea.Tick(driftCheckEvery, func(time.Time) tea.Msg { return driftCheckMsg{} })
}

func (m reviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.refresh()
		return m.ensurePreview()

	case externalDoneMsg:
		// The child had our screen and left its own marks on it — vimdiff prints
		// "2 files to edit" on the way out. The renderer counts the lines it wrote, and a
		// handoff resets that count, so the next frame would be written onto the debris
		// instead of replacing it. Start it on a clean screen.
		if m.out != nil {
			clearScreen(m.out)
		}
		// An editor or difftool just exited: refresh repository state so the
		// file list and review marks reflect what it changed (PRD §15).
		note := m.pendingNote
		m.pendingNote = ""
		if msg.err != nil {
			m.setStatus(fmt.Sprintf("%s: %v", msg.label, msg.err), true)
			return m, nil
		}
		if err := m.sess.Reload(m.ctx); err != nil {
			m.setStatus(err.Error(), true)
		} else {
			// Empty unless the handoff had something to report, in which case this is
			// the first moment the reviewer can actually read it.
			m.setStatus(note, false)
		}
		// The editor may have written a new thread, so the section below the files
		// has to be listed again, and any patch the preview is holding may be stale.
		// Dropping it and stopping there would leave the pane blank on the file the cursor
		// is already on, so ask for the fresh one in the same breath.
		m.forgetPatches()
		m.refresh()
		return m.ensurePreview()

	case previewMsg:
		m.store(msg.kind, msg.path, msg.patch)
		if msg.path == m.previewPath {
			m.previewOffset = 0
		}
		return m, nil

	case driftCheckMsg:
		sess, ctx := m.sess, m.ctx
		sp := sess.Span()
		from, to := sp.From, sp.To
		return m, func() tea.Msg {
			return driftMsg{moved: sess.CheckDrift(ctx), from: from, to: to}
		}

	case driftMsg:
		if sp := m.sess.Span(); sp.From == msg.from && sp.To == msg.to {
			m.sess.setDrift(msg.moved)
		}
		return m, driftTick()

	case tea.KeyMsg:
		updated, cmd := m.handleKey(msg)
		rm, ok := updated.(reviewModel)
		if !ok {
			return updated, cmd
		}
		rm, fetch := rm.ensurePreview()
		if fetch == nil {
			return rm, cmd
		}
		return rm, tea.Batch(cmd, fetch)
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
	case modeSpan:
		// The picker is how a reviewer gets *out* of a read-only span, so it is not
		// subject to the gate below; it also changes nothing until Enter.
		return m.handleSpanKey(key)
	}
	m.refresh()

	// Every action that changes something goes through one gate. Read-only-ness is a
	// property of the span's head, not of each command, and a screen that grows a new
	// mutating key must not be able to forget the check.
	if doing, mutating := mutatingKey(key); mutating {
		if why := m.cannot(doing); why != "" {
			m.setStatus(why, false)
			return m, nil
		}
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
	case key.Type == tea.KeyTab, key.Type == tea.KeyShiftTab:
		m.jumpSection()
	case key.Type == tea.KeySpace:
		m.toggleMark()
	case key.Type == tea.KeyEnter:
		return m.activate()
	case key.Type == tea.KeyRunes && firstRune(key) == 'p':
		return m.togglePreview()
	case key.Type == tea.KeyCtrlF:
		return m.pagePreview(1)
	case key.Type == tea.KeyCtrlB:
		return m.pagePreview(-1)
	case key.Type == tea.KeyRunes && firstRune(key) == 'd':
		return m.openDiffOfSelection()
	case key.Type == tea.KeyRunes && firstRune(key) == 'e':
		return m.openEditor()
	case key.Type == tea.KeyRunes && firstRune(key) == 'a':
		return m.openAbout()
	case key.Type == tea.KeyRunes && firstRune(key) == 't':
		m.mode, m.input, m.promptKind = modePrompt, "", promptThread
		m.setStatus("New thread title (Enter to create, Esc to cancel)", false)
	case key.Type == tea.KeyRunes && firstRune(key) == 'T':
		m.toggleThreads()
	case key.Type == tea.KeyRunes && firstRune(key) == 'V':
		m.mode = modeSpan
		m.pick = m.newSpanPicker()
		m.setStatus("", false)
	case key.Type == tea.KeyRunes && firstRune(key) == 'v':
		before := countMarked(m.sess)
		pos, total, err := m.sess.StepSpan(m.ctx)
		if err != nil {
			m.setStatus(err.Error(), true)
		} else {
			// The span is what the preview diffs, so every cached patch is about a span
			// that is no longer the one on screen.
			m.forgetPatches()
			m.refresh()
			m.setStatus(spanNote(before, m.sess, pos, total), false)
		}
	case key.Type == tea.KeyRunes && firstRune(key) == 'r':
		// Refreshing a drifted ref is not one of the mutating keys the read-only gate refuses:
		// it changes what the screen compares, like `v` does, and a historical span is exactly
		// where a moved ref is worth catching up with.
		moved, reset, err := m.sess.RefreshDrift(m.ctx)
		if err != nil {
			m.setStatus(err.Error(), true)
			break
		}
		// The span now compares different commits, so every cached patch is about the pair it
		// used to be.
		m.forgetPatches()
		m.refresh()
		m.setStatus(refreshNote(moved, reset), false)
	case key.Type == tea.KeyRunes && firstRune(key) == 's':
		if !m.sess.CanToggleSpan() {
			// Still allowed: submitting a first review is legitimate.
			m.setStatus("", false)
		}
		m.mode = modeSubmit
	}
	return m, nil
}

// mutatingKey is the table of keys that change something: a reviewed mark, a file on
// disk, a thread, a review submission. Keeping it in one place is what lets the
// read-only gate cover commands added later.
func mutatingKey(key tea.KeyMsg) (doing string, ok bool) {
	if key.Type == tea.KeySpace {
		return "mark files reviewed", true
	}
	if key.Type != tea.KeyRunes || len(key.Runes) != 1 {
		return "", false
	}
	switch key.Runes[0] {
	case 'e':
		return "edit files", true
	case 'a':
		return "edit ABOUT.md", true
	case 't':
		return "start a thread", true
	case 's':
		return "submit a review", true
	}
	return "", false
}

// cannot says why an action is unavailable, or "" when the span allows it. A span whose
// head is a commit is a look at history: you can read it, diff it, and leave, but you
// cannot mark, edit, or submit against it. The message names the head it is stuck on and
// the key that gets out, because "read-only" on its own leaves the reviewer guessing.
func (m reviewModel) cannot(doing string) string {
	sp := m.sess.Span()
	if sp.Live() {
		return ""
	}
	return fmt.Sprintf("read-only: this span ends at %s, not your working tree, so you cannot %s "+
		"\u2014 v opens a span you can review", sp.Head, doing)
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
	note := "Opened existing thread " + path
	if created {
		note = "Created " + path
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
	// The note rides the handoff, so "Created …" is still on screen when the editor closes
	// rather than having been covered by it.
	return m.openPathNoted(path, note)
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
	case actionArtifact:
		return m.openArtifact(r)
	case actionCollapse:
		m.toggleThreads()
	case actionNewThread:
		if why := m.cannot("start a thread"); why != "" {
			m.setStatus(why, false)
			return m, nil
		}
		m.mode, m.input, m.promptKind = modePrompt, "", promptThread
		m.setStatus("New thread title (Enter to create, Esc to cancel)", false)
	}
	return m, nil
}

// openDiffOfSelection is `d`: the difftool for whatever the cursor is on. A file row is
// always diffable — it is in the span by construction — and anything else goes through the
// same decision Enter makes for it.
func (m reviewModel) openDiffOfSelection() (tea.Model, tea.Cmd) {
	r, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	if r.kind == rowFile {
		return m.openDiff(r.path)
	}
	return m.openArtifact(r)
}

// openArtifact is Enter on a changeset document and `d` on anything that is not a file row:
// the difftool when the span has a real comparison for the file, the editor when it does not.
//
// Two rounds of review are what makes this worth distinguishing. In an unreviewed span,
// ABOUT.md and the threads already existed at its left end, so the span holds a genuine
// comparison — the lines the author rewrote after the last review, which is exactly what a
// returning reviewer is after, in prose as much as in code. On a first look at the changeset
// those files were invented by it, and the comparison has nothing on its left side: a
// document diffed against /dev/null is the document with extra steps, so it opens plainly.
func (m reviewModel) openArtifact(r row) (tea.Model, tea.Cmd) {
	if r.path == "" {
		m.setStatus("nothing to diff: "+r.name+" is a heading, not a file", false)
		return m, nil
	}
	if why := m.cannot("edit " + r.name); why != "" && m.documentAction(r) != actionDiff {
		// Only the diff is available over history. The file on disk is not the file this
		// span contains, so opening it in an editor would edit something the review is not
		// about, and creating a missing ABOUT.md would create it in the working tree.
		m.setStatus(why, false)
		return m, nil
	}
	if r.kind == rowAbout && m.sess.Span().CanEdit() {
		// A changeset made before ABOUT.md was scaffolded may not have one. Making it and
		// opening it is the whole job there, and no note about the span would be true — but
		// only in a span the reviewer can act on. Over history the gate above lets this row
		// through because the *diff* is legitimate, and the diff is what they should get: the
		// file on disk is not the file this span contains, and creating one here would be a
		// write in the mode whose whole promise is that there are none.
		if _, err := os.Stat(absPath(m.sess.Repo().Dir, r.path)); err != nil {
			if _, err := m.sess.Changeset().EnsureAbout(m.sess.Repo()); err != nil {
				m.setStatus(err.Error(), true)
				return m, nil
			}
			return m.openPath(r.path)
		}
	}
	if artifactAction(m.inSpan[r.path], m.sess.HasVersionAt(m.ctx, m.sess.Span().From, r.path)) == actionDiff {
		return m.openDiff(r.path)
	}
	return m.openPathNoted(r.path, m.artifactNote(r))
}

// documentAction is the artifact decision for a row, apart from acting on it: the
// read-only gate needs to know whether a row still has a legitimate use before anything
// is created or opened.
func (m reviewModel) documentAction(r row) action {
	return artifactAction(m.inSpan[r.path], m.sess.HasVersionAt(m.ctx, m.sess.Span().From, r.path))
}

// artifactAction is the whole rule: a document is worth diffing only when the span changed it
// *and* it already existed at the span's left end. Otherwise the comparison has no left side,
// and the file is simply read.
func artifactAction(inSpan, hasPrior bool) action {
	if inSpan && hasPrior {
		return actionDiff
	}
	return actionEdit
}

// artifactNote says which of two different reasons sent a document to the editor: it is new,
// or nothing happened to it in this span.
func (m reviewModel) artifactNote(r row) string {
	if m.inSpan[r.path] {
		return r.name + " was added by this changeset, so there is nothing to compare it against — opened in the editor"
	}
	return r.name + " has not changed in this span — opened in the editor"
}

func (m reviewModel) openDiff(path string) (tea.Model, tea.Cmd) {
	sp := m.sess.Span()
	return m.runExternal(console.DiffToolCommand(m.sess.Repo(), sp.From, difftoolHead(sp), []string{path}),
		"Difftool exited with an error", "")
}

// difftoolHead is the span's second revision for the external tool. A live span has
// none: the tool compares the start against the working tree, so edits made there survive
// the handoff. A historical span hands over its pinned head, because comparing a
// historical head against today's files would put work in the window that the span does
// not contain.
func difftoolHead(sp span.Span) string {
	if sp.Historical() {
		return sp.To
	}
	return ""
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
	return m.openPathNoted(relPath, "")
}

// openPathNoted opens a file in the editor and reports note on the way back. The handoff
// takes the whole screen, so a status set before it is gone by the time the reviewer looks
// again; this is the only way a note outlives the editor.
func (m reviewModel) openPathNoted(relPath, note string) (tea.Model, tea.Cmd) {
	repo := m.sess.Repo()
	cmd, err := console.EditorCommand(m.ctx, repo, absPath(repo.Dir, relPath))
	if err != nil {
		m.setStatus(err.Error(), true)
		return m, nil
	}
	return m.runExternal(cmd, "Editor exited with an error", note)
}

// runExternal hands the terminal to an editor or difftool. tea.ExecProcess suspends the
// renderer, gives the child process the real terminal, and resumes drawing when it exits —
// which is what PRD §15 requires. The callback turns the child's exit status into a message,
// along with whatever note the caller wanted read afterwards.
func (m reviewModel) runExternal(cmd *exec.Cmd, label, note string) (tea.Model, tea.Cmd) {
	m.pendingNote = note
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
	// styleWarn is the drift banner: a warning about the ground moving, not an error about
	// something the reviewer just did.
	styleWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
)

func (m reviewModel) View() string {
	if m.quitting {
		return ""
	}
	var b strings.Builder
	switch m.mode {
	case modeSpan:
		b.WriteString(m.pickerBlock())
	default:
		if m.paneWidth() > 0 {
			b.WriteString(joinColumns(m.listBlock(), m.previewLines(), m.listWidth()))
		} else {
			b.WriteString(m.listBlock())
		}
	}
	b.WriteString(m.footer())
	// Joined rather than newline-terminated: the renderer writes a line for each line of the
	// frame and moves down after it, so a frame that fills the terminal has no row left for the
	// cursor to land on and the terminal scrolls a line off the top.
	rows := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	return strings.Join(padRows(rows, m.height, m.width), "\n")
}

// padRows squares the frame up to the window: blank rows at the bottom, and every row padded to
// the full width so the frame is the block the terminal is, rather than a set of ragged lines
// with the divider hanging over nothing.
func padRows(rows []string, height, width int) []string {
	for len(rows) < height {
		rows = append(rows, "")
	}
	for i, row := range rows {
		rows[i] = padRight(row, width)
	}
	return rows
}

// headerLines are the two lines over the list: the changeset, and the base and span it is
// measured against. They are also what the list column has to be wide enough for, so they are
// built in one place rather than measured in one and drawn in another.
func (m reviewModel) headerLines(h Header) []string {
	return []string{
		styleSpan.Render(h.Title),
		styleDim.Render("base: "+h.Base) + "  " + styleDim.Render("span: "+m.spanName(h.SpanLabel)),
	}
}

// listBlock is the list column on its own: header, the rows in the window, the reviewed
// counter, the changeset section, and the rule under it. It is apart from View because the
// preview is drawn beside exactly this block, row for row.
func (m reviewModel) listBlock() string {
	var b strings.Builder
	h := m.sess.Header()
	reviewed, total := m.sess.Count()

	headers := m.headerLines(h)
	b.WriteString(clip(headers[0], m.listWidth()) + "\n")
	b.WriteString(clip(headers[1], m.listWidth()) + "\n")
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
	b.WriteString(m.counterLine(reviewed, total) + "\n")
	b.WriteString("\n")
	// The changeset section is below the counter it does not belong to, so the block above
	// reads as "these are the files the diff touched" and the block below as "this is what
	// the review is made of".
	for _, r := range section {
		b.WriteString(m.line(r) + "\n")
	}
	// The row area holds the height the window was given, so the rule and the shortcut bar sit at
	// the bottom of the terminal instead of floating under a short list.
	for i := len(files) + len(section); i < m.windowRows(); i++ {
		b.WriteString("\n")
	}
	// A rule rather than a blank line, so the shortcut bar reads as chrome and not as
	// another row of the list it sits under.
	b.WriteString(m.rule() + "\n")
	return b.String()
}

// footer is the shortcut bar and the status line. They span the whole terminal rather than the
// list column, because they belong to the session rather than to either column.
func (m reviewModel) footer() string {
	var b strings.Builder
	// The drift banner is chrome, and it is full width on purpose: in a split screen the list
	// column is narrow, and a warning that loses its key to an ellipsis warns about nothing.
	if line := m.driftLine(); line != "" {
		b.WriteString(line + "\n")
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

// driftLine is the warning that a named ref has moved since this span pinned it. It is a row
// of its own rather than a status line because a status line is where the last keystroke went:
// a reviewer who marked a file would lose the warning before they ever read it. What is on
// screen stays the pinned span — `r` moves it, and only when asked.
func (m reviewModel) driftLine() string {
	moved := m.sess.Drifted()
	if len(moved) == 0 {
		return ""
	}
	warn := fmt.Sprintf("\u26a0 %s moved %s \u2192 %s", span.ShortRef(moved[0].Name), moved[0].Pinned, moved[0].Current)
	if len(moved) > 1 {
		warn += fmt.Sprintf(" (+%d more)", len(moved)-1)
	}
	return styleWarn.Render(warn) + styleDim.Render("  [r] refresh")
}

// refreshNote reports what `r` did, in the shape requirements §14 asks for: which ref moved
// from where to where, and how many reviewed marks that reset.
func refreshNote(moved []span.Drift, reset int) string {
	words := make([]string, 0, len(moved))
	for _, d := range moved {
		words = append(words, d.Display())
	}
	note := "refreshed " + strings.Join(words, ", ")
	if reset > 0 {
		note += fmt.Sprintf(" \u00b7 %d reviewed mark%s no longer applies", reset, plural(reset))
	}
	return note
}

// counterLine is the line under the files. Over history there is no review in progress to
// count, and any marks on that commit belong to whoever reviewed it, so the slot carries
// the mode instead of a number that would read as progress.
func (m reviewModel) counterLine(reviewed, total int) string {
	if !m.sess.Span().CanMark() {
		return styleSpan.Render("HISTORICAL") + styleDim.Render(" \u00b7 READ ONLY")
	}
	return fmt.Sprintf("%d / %d reviewed", reviewed, total)
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
	case modeSpan:
		return helpSpan(m.pick.list != nil)
	}
	threadsHint := "T show threads"
	if m.threadsOpen {
		threadsHint = "T hide threads"
	}
	if !m.sess.Span().Live() {
		// Nothing in this bar may imply the reviewer can act on history.
		return "j/k move  tab section  enter open  d diff  p preview  " + threadsHint +
			"  v spans  V picker  q quit"
	}
	return "j/k move  tab section  enter open  d diff  p preview  e edit  space reviewed  a about  " +
		"t new thread  " + threadsHint + "  v spans  V picker  s submit  q quit"
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
	inSpan := make(map[string]bool, len(m.sess.Files()))
	for i, f := range m.sess.Files() {
		rows = append(rows, row{kind: rowFile, path: f.Path, name: f.Path, file: i})
		inSpan[f.Path] = true
	}
	m.inSpan = inSpan
	rows = append(rows, row{kind: rowAbout, path: m.sess.AboutPath(),
		name: filepath.Base(m.sess.AboutPath()), file: -1})

	// Nothing is filtered out of the file list above: a thread or ABOUT.md that changed in
	// the span is part of what is under review, so it keeps its file row as well as its
	// place in the section below. The section is the shortcut to read them; the list is the
	// record of what the diff did.
	threads, err := m.sess.Threads()
	head := row{kind: rowThreadsHead, name: "Threads", count: len(threads), file: -1}
	if err != nil {
		head.note = err.Error()
	}
	rows = append(rows, head)
	if m.threadsOpen {
		for _, path := range threads {
			rows = append(rows, row{kind: rowThread, path: path, name: filepath.Base(path), file: -1})
		}
		// The offer to start a thread is an offer to change the working tree, and a
		// historical span has no working tree in it.
		if m.sess.Span().CanEdit() {
			rows = append(rows, row{kind: rowNewThread, name: newThreadLabel, file: -1})
		}
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
		if !m.sess.Span().CanMark() {
			// No gutter over history: a tick there means "this reviewer has read it", and
			// marks left on that commit by an earlier review are not this reviewer's.
			return r.name
		}
		mark := "○ "
		if f := m.sess.Files(); r.file >= 0 && r.file < len(f) && f[r.file].Reviewed {
			mark = styleMark.Render("✓ ")
		}
		return mark + r.name
	case rowAbout:
		return r.name
	case rowThreadsHead:
		arrow := "▸ "
		label := r.name
		if m.threadsOpen {
			arrow = "▾ "
			// The count is the promise of what is hidden. Expanded, the threads are
			// the count, and repeating it next to them is noise.
		} else {
			label = fmt.Sprintf("%s (%d)", r.name, r.count)
		}
		if r.note != "" {
			return styleErr.Render("threads: " + r.note)
		}
		return styleDim.Render(arrow + label)
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

// The preview pane is a wide-terminal luxury, so it has an entry condition rather than a squeeze:
// below these dimensions there is no pane at all, and `p` says which way the terminal is short.
// Above them the two columns are content-driven: the list takes what its own text needs, capped,
// and the diff gets the rest.
const (
	previewMinWidth  = 100
	previewMinHeight = 16
	previewGap       = 3  // a space, the divider, a space -- joinColumns renders exactly this
	previewListMin   = 24 // a mark, a path, and room to tell one from another
	previewListMax   = 48 // one vendored path should not cost the reviewer the diff
	previewMinPane   = 40 // a narrower column of diff is a slit; asserted, not branched on
)

// previewLayout returns the width of each column, or a zero pane when there is no pane.
//
// A fixed 60/40 split made the diff share the screen with columns of whitespace, because most
// changesets are named after a package and a feature, not a tarball. So the list sizes itself to
// its content up to previewListMax, and the remainder is the pane's. The minimums are arithmetic
// rather than branches here: at previewMinWidth the pane still gets 49 columns even with the list
// at its cap, which TestPreviewHasRoomAtTheThreshold pins.
func (m reviewModel) previewLayout() (list, pane int) {
	if m.previewShortfall() != "" {
		return m.width, 0
	}
	list = min(max(m.listContentWidth(), previewListMin), previewListMax)
	return list, m.width - list - previewGap
}

// listContentWidth is what the list would take to write everything in full: its longest row, and
// the lines over it. It measures every row rather than the visible window, so the divider does not
// move when a longer path scrolls into view, and it is the width the rows are then clipped to --
// a column that grew because a path scrolled past would be a column that never stops growing.
func (m reviewModel) listContentWidth() int {
	h := m.sess.Header()
	widest := 0
	for _, line := range m.headerLines(h) {
		if w := lipgloss.Width(line); w > widest {
			widest = w
		}
	}
	if _, total := m.sess.Count(); total > 0 {
		if w := len(fmt.Sprintf("%d / %d reviewed", 0, total)); w > widest {
			widest = w
		}
	}
	for _, r := range m.rows {
		if w := lipgloss.Width(m.rowText(r)); w > widest {
			widest = w
		}
	}
	return widest
}

// previewShortfall names why the pane cannot fit, or "" when it can. Width and height are
// separate excuses -- a terminal can be one without the other -- and a reviewer who pressed `p`
// and saw nothing deserves to be told which way to grow the window.
func (m reviewModel) previewShortfall() string {
	if m.width < previewMinWidth {
		return fmt.Sprintf("the preview wants %d columns; this terminal has %d", previewMinWidth, m.width)
	}
	if m.height < previewMinHeight {
		return fmt.Sprintf("the preview wants %d rows; this terminal has %d", previewMinHeight, m.height)
	}
	return ""
}

// paneWidth is how wide the preview column is, or 0 when there is no preview: the reviewer
// switched it off, the session is taking input, or the terminal is too small. One function decides
// it, because layout and key handling have to agree on whether the pane is there.
func (m reviewModel) paneWidth() int {
	if !m.previewOn || m.mode != modeFiles {
		return 0
	}
	_, pane := m.previewLayout()
	return pane
}

// listWidth is the column the list gets: the whole terminal when there is no preview to make room
// for, and its own content width when there is.
func (m reviewModel) listWidth() int {
	if m.paneWidth() == 0 {
		return m.width
	}
	list, _ := m.previewLayout()
	return list
}

// previewBodyRows is how many lines of diff fit in the pane: the window the list gets, less the
// file it belongs to and the note about what is not on show.
func (m reviewModel) previewBodyRows() int {
	rows := m.windowRows() - 2
	if rows < 1 {
		return 1
	}
	return rows
}

// rule is the separator above the shortcut bar, as wide as the column it separates.
func (m reviewModel) rule() string {
	width := m.listWidth()
	if width <= 0 {
		width = 80
	}
	return styleDim.Render(strings.Repeat("─", width))
}

// ensurePreview puts the pane on the file under the cursor, returning the command that fetches
// whatever git has not answered yet -- the span's diff and the reviewer's own edits to that file.
// Fetches are cached per session state, so walking a list asks git once per file per source, and a
// span toggle or a tool handoff drops the cache rather than showing a stale diff.
func (m reviewModel) ensurePreview() (reviewModel, tea.Cmd) {
	path := ""
	if m.paneWidth() > 0 && !m.quitting {
		if r, ok := m.selectedRow(); ok {
			path = r.path
		}
	}
	if path == "" {
		m.previewPath, m.previewOffset = "", 0
		return m, nil
	}
	if path != m.previewPath {
		m.previewPath, m.previewOffset = path, 0
	}
	var cmds []tea.Cmd
	kinds := []patchKind{patchSpan, patchWorking}
	if m.sess.Span().Historical() {
		// There are no reviewer edits inside a historical span: the working tree is not one
		// of its endpoints, so asking git about it would produce a section that belongs to a
		// different review.
		kinds = []patchKind{patchSpan}
	}
	for _, kind := range kinds {
		if _, cached := m.patch(kind, path); cached {
			continue
		}
		kind, fetch, ctx := kind, m.fetcher(kind), m.ctx
		cmds = append(cmds, func() tea.Msg {
			return previewMsg{path: path, patch: fetch(ctx, path), kind: kind}
		})
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

// fetcher is where one kind's patch comes from, with the test seam in front of the real thing.
func (m reviewModel) fetcher(kind patchKind) func(context.Context, string) Patch {
	switch kind {
	case patchWorking:
		if m.workingFor != nil {
			return m.workingFor
		}
		return m.sess.WorkingPatch
	default:
		if m.patchFor != nil {
			return m.patchFor
		}
		return m.sess.Patch
	}
}

// patch answers "have I already asked git about this file" per source, which a single map cannot:
// the same path carries two different patches on screen at once.
func (m reviewModel) patch(kind patchKind, path string) (Patch, bool) {
	cache := m.patches
	if kind == patchWorking {
		cache = m.working
	}
	p, ok := cache[path]
	return p, ok
}

func (m *reviewModel) store(kind patchKind, path string, p Patch) {
	if kind == patchWorking {
		if m.working == nil {
			m.working = map[string]Patch{}
		}
		m.working[path] = p
		return
	}
	if m.patches == nil {
		m.patches = map[string]Patch{}
	}
	m.patches[path] = p
}

// forgetPatches drops what git answered, because the span changed or the working tree did, and a
// preview of the wrong diff is worse than no preview -- the reviewer's own edits are the ones most
// likely to have just changed, so both sources go.
func (m *reviewModel) forgetPatches() {
	m.patches, m.working = nil, nil
	m.previewPath, m.previewOffset = "", 0
}

// togglePreview is `p`. When it cannot fit it says so with the number the terminal is short by,
// rather than appearing to do nothing.
func (m reviewModel) togglePreview() (tea.Model, tea.Cmd) {
	m.previewOn = !m.previewOn
	if reason := m.previewShortfall(); m.previewOn && reason != "" {
		m.setStatus(reason, false)
	} else {
		m.setStatus("", false)
	}
	return m.ensurePreview()
}

// pagePreview scrolls the pane by half a page, which keeps a line or two of context on screen
// at the break. It counts rendered rows, because a line wider than the column takes several of
// them. ctrl-d is quit, so paging is ctrl-f and ctrl-b as in a pager.
func (m reviewModel) pagePreview(dir int) (tea.Model, tea.Cmd) {
	if m.paneWidth() == 0 || m.previewPath == "" {
		return m, nil
	}
	patch, ok := m.patches[m.previewPath]
	if !ok {
		return m, nil
	}
	work, _ := m.patch(patchWorking, m.previewPath)
	total := len(previewRows(patch, work, m.paneWidth()))
	if total == 0 {
		return m, nil
	}
	step := m.previewBodyRows() / 2
	if step < 1 {
		step = 1
	}
	offset := m.previewOffset + dir*step
	if max := total - m.previewBodyRows(); offset > max {
		offset = max
	}
	if offset < 0 {
		offset = 0
	}
	m.previewOffset = offset
	return m, nil
}

// previewLines renders the pane: the file it belongs to, git's own coloured diff, and a note
// about the part that is not on show. Everything between the first line and the note is git's
// bytes with nothing added — PRD §3 rules out a diff renderer, and this is the alternative to
// building one: a window onto what git printed.
func (m reviewModel) previewLines() []string {
	width := m.paneWidth()
	if width <= 0 || m.previewPath == "" {
		return nil
	}
	header := m.previewPath
	body := m.previewBodyRows()
	patch, cached := m.patches[m.previewPath]
	if cached && patch.Added >= 0 {
		header = fmt.Sprintf("%s  +%d \u2212%d", m.previewPath, patch.Added, patch.Deleted)
	}
	out := []string{styleDim.Render(clip(header, width))}

	switch {
	case !cached:
		return append(out, styleDim.Render("(reading the diff…)"))
	case patch.Err != "":
		return append(out, styleDim.Render(patch.Err))
	case len(patch.Lines) == 0:
		work, known := m.patch(patchWorking, m.previewPath)
		if m.sess.Span().Live() && !known {
			// Not "no changes" yet: the answer about the reviewer's own edits is still on its way,
			// and saying it now would be wrong for one git call's duration.
			return append(out, styleDim.Render("(reading your edits…)"))
		}
		if len(work.Lines) == 0 {
			return append(out, styleDim.Render("no changes in this span"))
		}
	}

	// Rows, not source lines: a line wider than the column is drawn as several rows, so paging
	// and the note have to count what is actually on screen.
	work, _ := m.patch(patchWorking, m.previewPath)
	lines := previewRows(patch, work, width)
	offset := m.previewOffset
	if max := len(lines) - body; offset > max {
		offset = max
	}
	if offset < 0 {
		offset = 0
	}
	end := offset + body
	if end > len(lines) {
		end = len(lines)
	}
	out = append(out, lines[offset:end]...)
	if end < len(lines) || offset > 0 {
		note := fmt.Sprintf("… %s  enter opens", rowsMore(len(lines)-end))
		if offset > 0 {
			note = fmt.Sprintf("rows %d\u2013%d of %d  ctrl-b/ctrl-f  enter opens", offset+1, end, len(lines))
		}
		if patch.Capped {
			note = "diff too large to read here  enter opens"
		}
		out = append(out, styleDim.Render(clip(note, width)))
	}
	return out
}

// rowsMore words the count of rows the pane has left to show.
func rowsMore(n int) string {
	if n == 1 {
		return "1 more row"
	}
	return fmt.Sprintf("%d more rows", n)
}

// joinColumns places the preview beside the list. Each list row is padded to the list's column
// so the divider falls in the same place on every row; the list is clipped to that width, so
// nothing here can wrap and shift it.
func joinColumns(left string, right []string, listWidth int) string {
	lines := strings.Split(strings.TrimSuffix(left, "\n"), "\n")
	var b strings.Builder
	for i, l := range lines {
		preview := ""
		if i < len(right) {
			preview = right[i]
		}
		b.WriteString(padRight(l, listWidth) + " " + styleDim.Render("│") + " " + preview + "\n")
	}
	return b.String()
}

func padRight(s string, width int) string {
	if pad := width - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// clip shortens a line to what the terminal will show, not to bytes: it counts the width of
// wide characters and ignores the escape sequences styling them, which is the only way a row
// beside a divider can be trusted to stay on one line.
func clip(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "…")
}

// line renders one row, highlighted when the cursor is on it. Every row is clipped to the list
// column, not just the selected one: with the preview beside it, a row that wrapped would move
// the divider out from under the column above it.
func (m reviewModel) line(r renderedRow) string {
	text := clip(m.rowText(r.row), m.listWidth())
	if m.cursor == r.index {
		return styleSelected.Render(text)
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
// and then the rule over the helper, the drift banner while one is pending, the helper
// itself, the "hidden above" note while scrolled, and the status line when there is one. The changeset section is part of the row
// list, so it is not here — only the counter that separates the two blocks is.
func (m reviewModel) chromeRows() int {
	chrome := 7 + len(m.helpLines())
	if len(m.sess.Drifted()) > 0 {
		// The drift banner takes a row above the shortcut bar, and stays there until the
		// reviewer refreshes it or moves to another span.
		chrome++
	}
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
