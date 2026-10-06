package tui

import (
	"context"
	"errors"
	"fmt"
	"gitpair/internal/changeset"
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

	"gitpair/internal/console"
	gitmodel "gitpair/internal/model"
	"gitpair/internal/reviewops"
	"gitpair/internal/span"
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
	// modePreview is the diff over the whole screen: what `p` gives a terminal too narrow for a
	// pane beside the list. It reads, and dismisses with q, esc or enter.
	modePreview
)

// focusTarget is the part of the screen the keys belong to. `Tab` moves them between two of these: the
// list column and the diff. The third value is the other half of the list column -- the changeset box
// above the file tree -- which shares the column's keys and is reached by walking up out of the tree
// rather than by a key of its own. The frame says which of them holds the keys rather than leaving it to
// be inferred: a keystroke means something different in each, and a region that cannot be seen cannot be
// changed by one.
// previewKind is what the pane is a window onto: a file's diff across the span, one changeset document's
// own text, every thread's text as one thing to read, or one file's own text at the span's head.
type previewKind int

const (
	previewDiff previewKind = iota
	previewDocument
	previewThreads
	// previewContent is a file the span created or moved whole. Its patch says nothing the file does
	// not say better, so the pane shows the text and the tree's sign says what happened to it.
	previewContent
)

type focusTarget uint8

const (
	focusFiles focusTarget = iota
	focusPreview
	focusMeta
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
	// rowDir is a directory of the file tree: a prefix of the changed files, foldable, and the
	// thing Space marks when a reviewer wants a whole subtree read at once.
	rowDir
	rowAbout
	rowThreadsHead
	rowThread
	rowNewThread
	// rowSpan is the span line inside the changeset box. It is a row rather than a caption because
	// the span is the one thing in the box a reviewer changes: Enter or Space on it opens the picker.
	rowSpan
)

// Labels the section rows print. A thread is shown by file name: they all live in one
// directory, so the name is the whole distinction.
const (
	newThreadLabel = "+ new thread…"
	threadIndent   = "    "
)

// The title prompt is one field: a label, the ghost of a title while the field is empty, the caret,
// and the keys that finish or abandon it. The ghost says what the field wants, so the line under it
// has no reason to say it again.
const (
	threadPromptLabel      = "New thread: "
	threadTitlePlaceholder = "Thread title"
	threadPromptCursor     = "█"
)

// threadPromptHint names the two keys that end the title prompt. It rides the second row of the band
// rather than the notification slot: it says what the keys mean for as long as the field is up, so it
// is chrome, and chrome cannot be dismissed by the reviewer or by a clock.
const threadPromptHint = "Enter to create, Esc to cancel"

// row is one line of the navigable list: a file in the span, or an entry of the changeset
// section — ABOUT.md, the thread heading, one thread nested under it, or the action that
// creates another. Both sections are in one list because a reviewer works down the screen:
// the code, then what the changeset says about it, with `j` running off the bottom of the
// files and into the artifacts instead of into a separate mode.
type row struct {
	kind rowKind
	// path is repository-relative. A directory row's carries the trailing separator, which makes it
	// both the prefix that marks the subtree and the pathspec git takes for the directory diff.
	path string
	// name is what the row prints: a base name, a folded directory run, or a section label.
	name string
	// file is the index into Session.Files() for a file row, -1 for everything else.
	file int
	// depth is how far a tree row sits under the top of the file tree. The rows under the reviewed
	// counter are all at 0, because that section is not a tree.
	depth int
	// total and marked count the files under a directory row, folded ones included: they are what
	// the row promises and what Space is about to set.
	total, marked int
	note          string // set on the thread heading when the threads could not be listed
	count         int    // how many threads the heading is standing in for
	// change is what the span did to this file, carried on the row rather than looked up again, so the
	// tree's sign and the row's own Enter cannot disagree about what happened to the file. Only a file
	// row has one: every other kind leaves it at the modification, whose sign is nothing at all.
	change Change
	// dirty is the row's own copy of what the session knows about the working tree: an uncommitted
	// change at this path, or -- for a directory -- under it, which the tree only says while the
	// directory is folded. It is carried rather than looked up for the same reason `change` is: the row
	// that colours its name and the row that says it is reviewed are one row.
	dirty bool
}

// inFileBlock is which rows belong to the file tree rather than to the changeset box. Directory rows
// belong to it: they are part of the files, and folding a directory must not count as having left them.
func inFileBlock(k rowKind) bool { return k == rowFile || k == rowDir }

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
	// actionSpan is Enter or Space on the span row: the picker, which changes nothing until its own
	// Enter — so it is reachable over a historical span like any other read.
	actionSpan
)

// activateBy is the Enter table: one row kind, one action. A file opens in the difftool because that
// is the thing under review, with one exception spelled out in fileAction: a file the span added is
// read in the editor, because there is no other side of it to compare against. The changeset documents
// are markdown, so they get openArtifact: what a returning reviewer wants from ABOUT.md or a thread is
// usually the two or three lines the author rewrote after the last review, and a diff is the only way to see
// exactly those — while a document the changeset invented has no comparison worth opening.
// The heading toggles its own group, and the last row of the group creates another thread. Enter on
// a directory does the same to its own part of the tree — fold it, unfold it — which is what the key
// already means for a group of rows; reading what a directory changed is `d`, as it is for a file.
func activateBy(r row) action {
	switch r.kind {
	case rowFile:
		return fileAction(r.change)
	case rowThread, rowAbout:
		return actionArtifact
	case rowDir, rowThreadsHead:
		return actionCollapse
	case rowNewThread:
		return actionNewThread
	case rowSpan:
		return actionSpan
	}
	return actionNone
}

// fileAction is the file half of the Enter table. A modification, a deletion and a rename all have a
// comparison worth opening: the span did something to a file that was already there, and the patch is
// the thing under review. A file the span *added* has nothing on the span's left side, so its patch is
// the file again with a `+` in front of every line -- which is why the pane already reads it as a file
// rather than as a patch (see Change.ReadsAsFile). Enter opens it in the editor for the same reason
// openArtifact opens a document the changeset invented; artifactAction is this rule for the changeset's
// own files. A rename whose bytes are unchanged keeps the difftool, because the rename is the
// comparison and it is the reason that file is under review at all.
func fileAction(c Change) action {
	if c == ChangeAdded {
		return actionEdit
	}
	return actionDiff
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

// docMsg carries a document back from the disk, alongside previewMsg. Reading a file is not git's work, but
// it is the pane's content and it arrives the same way: off the event loop, with the cursor still moving.
type docMsg struct {
	path string
	doc  Document
}

// contentMsg carries a file's text at the span's head back from git. It is its own message rather than a
// second use of docMsg because the two contents come from different places and must not share a cache.
type contentMsg struct {
	path string
	doc  Document
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
	width  int
	height int

	// cursor and scroll belong to the file list; metaCursor and metaScroll to the changeset box. The two
	// are the halves of one navigable column and each keeps its own place, so walking out of one and
	// back into it returns the reviewer to the row they left rather than to the top of the list.
	cursor     int
	scroll     int
	metaCursor int
	metaScroll int
	// focus is which region holds the keys, and prevFocus the one they were in before the preview
	// took them, so `p` gives them back where they came from rather than always to the files.
	focus     focusTarget
	prevFocus focusTarget
	// zoomReturn is the region the keys were standing in when the diff took the whole screen. `z`
	// gives both the screen and the keys back to it, so un-zooming returns the reviewer to where they
	// were when they asked for the bigger view: the preview column if `z` was pressed inside the pane,
	// the half of the list column if it was pressed out there. prevFocus cannot stand for it, because
	// the pane route leaves prevFocus on the list half that handed the keys over hours of keystrokes
	// ago, and that is the right answer for `esc` and the wrong one for `z`.
	zoomReturn focusTarget
	// gPrefix is a `g` waiting for its partner in the region that holds the keys: the pair jumps to
	// that region's top. It is kept apart from the diff's own previewG so the two jumps cannot read
	// each other's half-press.
	gPrefix bool

	// rows is the navigable list: the file tree, then the changeset box's rows. It is rebuilt by
	// refresh whenever the session or the view could have changed, and metaStart is where the box's
	// rows begin in it — the two regions are two windows over one list.
	rows        []row
	metaStart   int
	inSpan      map[string]bool
	threadsOpen bool
	// folded holds the directories whose contents are hidden, keyed by the directory path with
	// its trailing separator. It is view state and nothing else: the marks a directory row shows
	// are computed from the files under it every time the list is built, so there is no directory
	// state to keep in step with them, or to save.
	folded map[string]bool

	promptKind promptKind
	input      string

	status    string
	statusErr bool
	// statusKind says whether the message fades on its own or waits for the reviewer.
	statusKind notifKind
	// notifGen counts notifications and notifArmed is the generation whose expiry tick has been
	// scheduled. The tick is armed once per message: the drift check answers every few seconds,
	// and a message arriving on a timer must not be able to keep a note alive by re-arming it.
	notifGen, notifArmed uint64
	// patchFor is the seam tests use instead of running git.
	patchFor func(context.Context, string) Patch
	// workingFor is the same seam for the reviewer's own uncommitted edits.
	workingFor func(context.Context, string) Patch
	// The preview, in whichever layout it fits. previewPath is what it shows and previewOffset
	// where in it that pane is; patches and working hold what git already answered, so moving back
	// to a file costs nothing. previewG is the `g` waiting for its partner in the diff, kept
	// apart from the region's gPrefix so the two jumps cannot read each other's half-press.
	previewOn     bool
	previewPath   string
	previewOffset int
	previewG      bool
	patches       map[string]Patch
	working       map[string]Patch
	// previewKind is what the pane is a window onto. The list column has two kinds of row: files, which
	// the span changed and which the pane shows as a diff or as the file's own text, and the changeset's
	// own documents, which are prose the reviewer is asked to read and which the pane shows as their text.
	previewKind previewKind
	// docs is the cache of what has been read, one entry per path -- and one for the changeset directory,
	// holding every thread at once, which is what the Threads heading is standing on.
	docs   map[string]Document
	docFor func(ctx context.Context, path string) Document // nil asks the session; tests say so instead
	// contents is the same cache for the files the pane reads as text. It is apart from `docs` because
	// the two read the same path differently: ABOUT.md the reviewer is editing is the working copy, and
	// ABOUT.md the span created is a revision's bytes. One map would make those two one entry.
	contents   map[string]Document
	contentFor func(ctx context.Context, path string) Document // nil asks the session; tests say so instead
	// The preview's search: the term in force, the field while it is being typed, and the row `n` landed
	// on. Nothing here caches a match position -- the pane's rows are rebuilt from git's bytes on every
	// draw -- it caches which match the reviewer is looking at, and which term that match belongs to, so
	// a row that is no longer a match cannot be left picked out in reverse video.
	previewSearch    string
	searchInput      string
	searching        bool
	previewMatch     int // -1 while there is no match to pick out
	previewMatchTerm string
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
	// mouse is the session's answer to whether the terminal should report the wheel to us,
	// resolved from git-pair.mouse before the program started and carried here so the wheel
	// handler and the handoff can both ask it. See mouse.go.
	mouse MouseSetting
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

	m := reviewModel{
		ctx: ctx, sess: sess, out: out, width: 80, height: 24, threadsOpen: true, previewOn: true,
		mouse: opts.Mouse,
		// There is no match to pick out until `n` or `enter` says there is. Zero would be row zero,
		// which is a row the reviewer never asked to be standing on.
		previewMatch: -1,
	}
	m.refresh()
	if n := sess.Resumed(); n > 0 {
		m.setStatus(fmt.Sprintf("resumed %d reviewed mark%s from an earlier session", n, plural(n)), false)
	}
	// A setting the session could not read is worth more on the screen than silently ignored:
	// the reviewer who typed `git-pair.mouse = diabled` has to be told it did nothing.
	if opts.Mouse.Note != "" {
		m.setStatus(opts.Mouse.Note, false)
	}
	progOpts := []tea.ProgramOption{tea.WithContext(ctx), tea.WithOutput(out)}
	if m.mouse.Report {
		// Cell motion (1002) plus SGR coordinates (1006), which is what bubbletea sends for
		// WithMouseCellMotion. Deliberately not 1003: hover reporting would give up the pointer
		// for nothing, since nothing in this screen is a hover target.
		progOpts = append(progOpts, tea.WithMouseCellMotion())
	}
	p := tea.NewProgram(m, progOpts...)
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

// notifKind is how long a message holds the bottom band. A note reports what the last key did and
// costs nothing to miss, so it fades. A refusal or a failure asks the reviewer to do something, so
// it stays until they press a key or dismiss it with esc.
type notifKind int

const (
	notifNote notifKind = iota
	notifSticky
)

// notifNoteTTL is how long a note stays on screen. Long enough to read after the key that caused
// it, short enough that the shortcut bar comes back on its own while the reviewer is still
// deciding what to press next.
const notifNoteTTL = 4 * time.Second

// notifExpireMsg retires the note that was on screen when it was scheduled.
type notifExpireMsg struct{ seq uint64 }

// Update is handle plus the notification timer. The timer is armed here rather than at the three
// dozen places that set a message, so no message can appear without its dismissal being scheduled.
func (m reviewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	rm, cmd := m.handle(msg)
	model, ok := rm.(reviewModel)
	if !ok {
		return rm, cmd
	}
	if next := model.armNotif(); next != nil {
		if cmd == nil {
			cmd = next
		} else {
			cmd = tea.Batch(cmd, next)
		}
	}
	return model, cmd
}

// armNotif schedules the expiry the current note is owed. Sticky messages are dismissed by the
// reviewer rather than by the clock, so they arm nothing, and a message already armed waits for
// the tick it has.
func (m *reviewModel) armNotif() tea.Cmd {
	if m.status == "" || m.statusKind != notifNote || m.notifArmed == m.notifGen {
		return nil
	}
	m.notifArmed = m.notifGen
	seq := m.notifGen
	return tea.Tick(notifNoteTTL, func(time.Time) tea.Msg { return notifExpireMsg{seq: seq} })
}

func (m reviewModel) handle(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			return m, m.wheelBack(nil)
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
		rm, cmd := m.ensurePreview()
		return rm, rm.wheelBack(cmd)

	case docMsg:
		if m.docs == nil {
			m.docs = map[string]Document{}
		}
		m.docs[msg.path] = msg.doc
		if msg.path == m.previewPath {
			m.previewOffset, m.previewMatch = 0, -1
		}
		return m, nil

	case contentMsg:
		if m.contents == nil {
			m.contents = map[string]Document{}
		}
		m.contents[msg.path] = msg.doc
		if msg.path == m.previewPath {
			m.previewOffset, m.previewMatch = 0, -1
		}
		return m, nil

	case previewMsg:
		m.store(msg.kind, msg.path, msg.patch)
		if msg.path == m.previewPath {
			// The rows have changed underneath the marks: a second section arriving adds a caption and
			// pushes everything below it. The term keeps highlighting, but no row claims to be the one
			// the reviewer was on until `n` finds it again.
			m.previewOffset, m.previewMatch = 0, -1
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

	case notifExpireMsg:
		// Only the message this tick was armed for: a note replaced in the meantime has its own
		// tick, and a stale one must not cut the new one short. A sticky message is dismissed by
		// the reviewer rather than by the clock, so no tick retires it.
		if msg.seq == m.notifGen && m.statusKind == notifNote {
			m.setStatus("", false)
		}
		return m, nil

	case tea.MouseMsg:
		return m.handleWheel(msg)

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

	// A refusal has been acknowledged by the next key you press: it asked for something, and you
	// have moved. Notes are left alone until they fade, so a reviewer mid-motion still has what
	// the last key said. This runs before the modes dispatch so that a key the focused pane reads
	// acknowledges a refusal too -- the pane is where the keys are most of the time.
	if m.status != "" && m.statusKind == notifSticky {
		m.setStatus("", false)
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
	case modePreview:
		// The overlay reads and dismisses. It dispatches before the gate below for the same reason
		// the picker does: the keys it does not read are keys that do not happen, so nothing that
		// changes the review is reachable while the list is off the screen.
		return m.handleDiffKey(key)
	}
	// A focused pane reads its keys the same way, for the same reason, and is dispatched before the
	// gate for the same one. The difference is that its list never left the screen: the reviewer can
	// see the row they are not marking, which is what makes the shortcut bar's silence about `space`
	// an explanation rather than a missing key.
	if m.previewHasFocus() {
		return m.handleDiffKey(key)
	}
	m.refresh()

	// The search field belongs to the diff's keys and does not follow the reviewer into the list. What is
	// highlighted stays highlighted; what was typed and not committed is dropped, because a field that
	// reappears later with half a word in it is a field the reviewer has to inspect before typing again.
	m.searching, m.searchInput = false, ""

	// Every action that changes something goes through one gate. Read-only-ness is a
	// property of the span's head, not of each command, and a screen that grows a new
	// mutating key must not be able to forget the check.
	if doing, mutating := mutatingKey(key, m); mutating {
		if why := m.cannot(doing); why != "" {
			m.setRefusal(why)
			return m, nil
		}
	}

	// `g` waits for its partner, as it does in the diff: the pair means the top of whichever region
	// holds the keys. The guard is what makes the pair possible at all — without it the second `g`
	// would be read as another prefix and the jump would never happen.
	if key.Type == tea.KeyRunes && firstRune(key) == 'g' && !m.gPrefix {
		m.gPrefix = true
		return m, nil
	}
	pressedG := m.gPrefix
	m.gPrefix = false

	switch {
	case key.Type == tea.KeyCtrlC,
		key.Type == tea.KeyRunes && len(key.Runes) == 1 && key.Runes[0] == 'q':
		m.quitting = true
		return m, tea.Quit
	case key.Type == tea.KeyEsc && m.status != "":
		m.setStatus("", false)
		return m, nil
	// The navigation keys belong to the region holding them, and they mean the same thing in both:
	// a row, half a page, a page, the two ends. ctrl-d used to quit, which is why paging was ctrl-f
	// and ctrl-b and nothing else; ctrl-c and q still quit, and the key a pager means it to have gets
	// back to paging.
	case key.Type == tea.KeyDown, key.Type == tea.KeyRunes && firstRune(key) == 'j':
		m.move(1)
	case key.Type == tea.KeyUp, key.Type == tea.KeyRunes && firstRune(key) == 'k':
		m.move(-1)
	case key.Type == tea.KeyCtrlD:
		m.move(max(1, m.activeWindow()/2))
	case key.Type == tea.KeyCtrlU:
		m.move(-max(1, m.activeWindow()/2))
	case key.Type == tea.KeyCtrlF:
		m.move(max(1, m.activeWindow()))
	case key.Type == tea.KeyCtrlB:
		m.move(-max(1, m.activeWindow()))
	case pressedG && key.Type == tea.KeyRunes && firstRune(key) == 'g':
		m.activeTop()
	case key.Type == tea.KeyRunes && firstRune(key) == 'G':
		m.activeBottom()
	case key.Type == tea.KeyTab:
		m.cycleFocus(1)
	case key.Type == tea.KeyShiftTab:
		m.cycleFocus(-1)
	case key.Type == tea.KeyLeft, key.Type == tea.KeyRunes && firstRune(key) == 'h':
		m.foldUp()
	case key.Type == tea.KeyRight, key.Type == tea.KeyRunes && firstRune(key) == 'l':
		m.unfoldUnder()
	case key.Type == tea.KeyRunes && firstRune(key) == 'c':
		m.toggleTree()
	case key.Type == tea.KeySpace:
		// Space on the span row chooses a span rather than marking one, which is what the read-only
		// gate is told about in mutatingKey: the picker commits nothing until its own Enter.
		if r, _, ok := m.activeRow(); ok && r.kind == rowSpan {
			return m.openSpanPicker()
		}
		m.toggleMark()
	case key.Type == tea.KeyEnter:
		return m.activate()
	case key.Type == tea.KeyRunes && firstRune(key) == 'p':
		return m.togglePreview()
	// `z` is the same request from the list column that it is from inside the diff: this, over the
	// whole screen. The difference is only that here the row the cursor is on says which "this", so
	// the keys move into the diff on the way — a reviewer who reads a diff at the size of the terminal
	// should not have to press the key that moves the keys first.
	case key.Type == tea.KeyRunes && firstRune(key) == 'z':
		return m.openFullScreen()
	case key.Type == tea.KeyRunes && firstRune(key) == 'f':
		m.focusOn(focusFiles)
	case key.Type == tea.KeyRunes && firstRune(key) == 'd':
		return m.openDiffOfSelection()
	case key.Type == tea.KeyRunes && firstRune(key) == 'e':
		return m.openEditor()
	case key.Type == tea.KeyRunes && firstRune(key) == 'a':
		// `a` names the row rather than the editor. The box is a keystroke away from anywhere in the
		// column now, `e` on the row it lands on is the key that hands the terminal over, and a jump
		// is something a reviewer can press inside a read-only span.
		if !m.gotoRow(rowAbout) {
			m.setRefusal("no ABOUT.md row in this changeset's box")
		}
	case key.Type == tea.KeyRunes && firstRune(key) == 't':
		if !m.gotoRow(rowThreadsHead) {
			m.setRefusal("no thread heading in this changeset's box")
		}
	case key.Type == tea.KeyRunes && firstRune(key) == 'T':
		m.mode, m.input, m.promptKind = modePrompt, "", promptThread
		m.setStatus("", false)
	case key.Type == tea.KeyRunes && firstRune(key) == 'V':
		return m.openSpanPicker()
	case key.Type == tea.KeyRunes && firstRune(key) == 'v':
		before := countMarked(m.sess)
		res, err := m.sess.StepSpan(m.ctx)
		if err != nil {
			m.setStatus(stepFailureNote(res, err), true)
		} else {
			// The span is what the preview diffs, so every cached patch is about a span
			// that is no longer the one on screen.
			m.forgetPatches()
			m.refresh()
			m.setStatus(spanNote(before, m.sess, res), false)
		}
	case key.Type == tea.KeyRunes && firstRune(key) == 'r':
		// Refreshing a drifted ref is not one of the mutating keys the read-only gate refuses:
		// it changes what the screen compares, like `v` does, and a historical span is exactly
		// where a moved ref is worth catching up with.
		moved, reset, err := m.sess.RefreshDrift(m.ctx)
		if err != nil && !errors.Is(err, ErrNothingMoved) {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		if err == nil {
			// The span now compares different commits, so every cached patch is about the pair it
			// used to be.
			m.forgetPatches()
			m.refresh()
			m.setStatus(refreshNote(moved, reset), false)
			return m, nil
		}
		// Nothing has moved at the refs. They are still only one half of what the screen compares: the
		// other half is the working tree, and a reviewer can write into that from another window without
		// this one being handed a message. `r` is the key that asks the question again, so it asks it of
		// the working tree too and then says the same thing it always said about the refs.
		if rerr := m.sess.Rescan(m.ctx); rerr != nil {
			m.setStatus(rerr.Error(), true)
			return m, nil
		}
		m.forgetPatches()
		m.refresh()
		m.setStatus(err.Error(), true)
	case key.Type == tea.KeyRunes && firstRune(key) == 's':
		if !m.sess.CanToggleSpan() {
			// Still allowed: submitting a first review is legitimate.
			m.setStatus("", false)
		}
		m.mode = modeSubmit
	}
	return m, nil
}

// mutatingKey is the table of keys that change something: a reviewed mark, a file on disk, a thread, a
// review submission. Keeping it in one place is what lets the read-only gate cover commands added
// later. It is asked about the row the keys are on rather than about the keystroke alone, because Space
// means two different things depending on where it lands: on a file it sets a mark, and on the span row
// it opens the picker, which commits nothing.
func mutatingKey(key tea.KeyMsg, m reviewModel) (doing string, ok bool) {
	if key.Type == tea.KeySpace {
		if r, _, found := m.activeRow(); found && r.kind == rowSpan {
			return "", false
		}
		return "mark files reviewed", true
	}
	if key.Type != tea.KeyRunes || len(key.Runes) != 1 {
		return "", false
	}
	switch key.Runes[0] {
	case 'e':
		return "edit files", true
	case 'T':
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
		"\u2014 V chooses a span you can review", sp.Head, doing)
}

// toggleMark marks the file under the cursor reviewed, or explains why the row it is on cannot be:
// ABOUT.md and threads are read rather than diffed, and the group rows are not things at all.
//
// A directory row marks what it stands for — every file under it, including the ones a fold has
// hidden, since the fold is a way of looking and not a claim about what was read. One press marks
// the subtree, and the next clears it, so the key does the same thing to a directory that it does
// to a file: it flips the state of the row under the cursor.
func (m *reviewModel) toggleMark() {
	r, _, ok := m.activeRow()
	if !ok {
		return
	}
	var note string
	switch {
	case r.kind == rowDir:
		all := r.total > 0 && r.marked == r.total
		verb, reviewed := "marked", true
		if all {
			verb, reviewed = "cleared", false
		}
		n := m.sess.SetReviewedUnder(r.path, reviewed)
		note = fmt.Sprintf("%s %d file%s under %s", verb, n, plural(n), r.path)
	case r.kind == rowFile:
		m.sess.Toggle(r.file)
	default:
		m.setRefusal("reviewed marks apply to file rows: " + r.name + " is not one")
		return
	}
	// The list was built before the mark moved, and a directory's mark and its counts are facts
	// about the files under it rather than about the row: rebuilding is what stops the row the
	// cursor is on from showing the state one keystroke behind.
	m.refresh()
	// Marks persist locally so the review can be resumed. A failure is reported
	// and otherwise ignored: the review itself does not depend on them.
	if err := m.sess.SaveMarks(m.ctx); err != nil {
		m.setStatus("marks not saved: "+err.Error(), true)
		return
	}
	// Empty for a file, which is what clearing the line after a mark has always meant.
	m.setStatus(note, false)
}

// toggleThreads collapses or expands the thread list under its heading.
func (m *reviewModel) toggleThreads() {
	m.threadsOpen = !m.threadsOpen
	m.refresh()
}

// --- folding the file tree --------------------------------------------------

// setFolded hides or shows the contents of one directory. Unfolding clears the keys above it as
// well: the row a reviewer is looking at may name a run of single-child directories that `c`
// folded as three keys, and opening only the deepest of them would leave the two above still
// hiding the row that was just opened. Deeper folds are left alone, so unfolding a directory
// reveals the children it has and not the whole world underneath them.
func (m *reviewModel) setFolded(dir string, folded bool) {
	if m.folded == nil {
		m.folded = map[string]bool{}
	}
	if folded {
		m.folded[dir] = true
	} else {
		for _, above := range ancestorsOf(dir) {
			delete(m.folded, above)
		}
	}
	m.refresh()
}

// listOnly is the answer to a key of the file tree while the changeset box holds the keys. Folding is
// a thing the tree does, and doing it to a list the reviewer is not looking at — with the box's bar not
// even offering the key — is the kind of invisible action the focus is there to prevent.
func (m *reviewModel) listOnly() bool {
	if !m.metaHasFocus() {
		return false
	}
	m.setRefusal("that key belongs to the file tree — f goes back to it")
	return true
}

// foldUp is `h` and the left arrow: the way back up a tree. On an open directory it closes it;
// anywhere else in the tree — a file, or a directory already closed — it moves the cursor onto the
// row of the directory containing it, which is how the key leaves the fold decision to the reviewer
// rather than collapsing whatever the cursor happens to be over. It is a key of the file tree: the
// changeset box has paths in it, but no rows that fold.
func (m *reviewModel) foldUp() {
	if m.listOnly() {
		return
	}
	r, ok := m.selectedRow()
	if !ok {
		return
	}
	if r.kind == rowDir && !m.folded[r.path] {
		m.setFolded(r.path, true)
		return
	}
	_, idx := m.containingDirRow(r.path)
	if idx < 0 {
		m.setRefusal(r.name + " sits at the top of the tree")
		return
	}
	m.cursor = idx
	m.clamp()
	m.setStatus("", false)
}

// unfoldUnder is `l` and the right arrow, and it only ever opens: the closing half of a directory
// is `h` or Enter, so that one key cannot be the one that hides what the reviewer is reading.
func (m *reviewModel) unfoldUnder() {
	if m.listOnly() {
		return
	}
	r, ok := m.selectedRow()
	if !ok {
		return
	}
	if r.kind != rowDir {
		m.setRefusal(r.name + " is a file — h/l fold a directory")
		return
	}
	if !m.folded[r.path] {
		m.setRefusal(r.name + " is already open")
		return
	}
	m.setFolded(r.path, false)
}

// toggleTree is `c`: the whole tree at once, and back. A changeset of a hundred files opens as a
// hundred rows, and the reviewer who wants the shape of it before the detail presses this. It is
// one key rather than a pair because the two states are the only two there are, and pressing it
// again has exactly one sensible meaning.
func (m *reviewModel) toggleTree() {
	if m.listOnly() {
		return
	}
	dirs := treeDirs(m.sess.Files())
	if len(dirs) == 0 {
		m.setRefusal("nothing to fold: this span changes files at the top of the tree only")
		return
	}
	if m.treeFolded(dirs) {
		m.folded = nil
		m.refresh()
		return
	}
	m.folded = make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		m.folded[dir] = true
	}
	m.refresh()
}

// treeFolded is whether every directory of the tree is closed, which is what decides which way `c`
// goes. It counts the directories rather than the rows, because a folded run of single-child
// directories hides keys that no row names — and those have to count as folded, or the second press
// of `c` would fold the tree a second time instead of opening it.
func (m reviewModel) treeFolded(dirs []string) bool {
	for _, dir := range dirs {
		if !m.folded[dir] {
			return false
		}
	}
	return true
}

// containingDirRow finds the deepest directory row on screen that path sits under. A file's own
// directory is always among them — nothing hidden is on screen — and so is every directory above
// it, which is what makes this the answer for both "where is the row above this file" and "where
// did the cursor's row go when its subtree was folded away".
func (m reviewModel) containingDirRow(path string) (row, int) {
	best, idx := row{}, -1
	for i, r := range m.rows {
		if r.kind != rowDir || !isUnder(path, r.path) {
			continue
		}
		if idx < 0 || len(r.path) > len(best.path) {
			best, idx = r, i
		}
	}
	return best, idx
}

func (m reviewModel) handleSubmitKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Esc cancels the prompt and stays; ctrl-c leaves. It used to be swallowed here, so the key a
	// reviewer reaches for at a prompt got no reply, and the screen looked like it was thinking.
	if key.Type == tea.KeyCtrlC {
		m.quitting = true
		return m, tea.Quit
	}
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
	// All three ends of the diff are recorded, here and in `review submit`, from one measurement: a review
	// is a claim about a parent tip, about a measurement point, and about the content between them, and the
	// branch that named the first two can be deleted before anyone asks (PRD §21).
	result, err := reviewops.Submit(m.ctx, m.sess.Repo(), m.sess.Changeset(),
		outcome, "", true,
		changeset.MeasureSubmission(m.ctx, m.sess.Repo(), m.sess.Changeset(), m.sess.Trunk(), ""),
		m.sess.Trunk())
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
	// The new file belongs in the box the reviewer was just reading, so the box's cursor goes to it and
	// the keys go with it: the box draws its highlight only on the half that holds the keys, and a thread
	// left selected in a box without the keys is a selection nobody sees. The file tree's cursor is left
	// alone: it is the row the preview is showing, and coming back from the editor to a different diff
	// than the one you left is the one thing a handoff should not do.
	m.refresh()
	for i, r := range m.rows {
		if r.kind == rowThread && r.path == path {
			m.metaCursor, m.focus = i, focusMeta
			break
		}
	}
	m.clamp()
	// The note rides the handoff, so "Created …" is still on screen when the editor closes
	// rather than having been covered by it.
	return m.openPathNoted(path, note)
}

// openSpanPicker is `V`, and Enter or Space on the span row: the screen where both ends are chosen
// before either takes effect. It is reachable from a historical span for the same reason reading is —
// the picker is how a reviewer gets back to a span they can act on, and it changes nothing until its
// own Enter.
func (m reviewModel) openSpanPicker() (tea.Model, tea.Cmd) {
	m.mode = modeSpan
	m.pick = m.newSpanPicker()
	m.setStatus("", false)
	return m, nil
}

// --- external process handoff ----------------------------------------------

// activate does whatever the row under the cursor is for. The mapping from row to action is
// activateBy, kept apart from the process handoff so the table is testable without handing
// the terminal to an editor.
func (m reviewModel) activate() (tea.Model, tea.Cmd) {
	r, _, ok := m.activeRow()
	if !ok {
		return m, nil
	}
	switch activateBy(r) {
	case actionSpan:
		return m.openSpanPicker()
	case actionDiff, actionEdit:
		// One call for both, so the rule and the exception live in one place. Only a file row can ask
		// for either, and it carries the change the rule needs.
		return m.openFile(r.path, r.change)
	case actionArtifact:
		return m.openArtifact(r)
	case actionCollapse:
		if r.kind == rowDir {
			m.setFolded(r.path, !m.folded[r.path])
			return m, nil
		}
		m.toggleThreads()
	case actionNewThread:
		if why := m.cannot("start a thread"); why != "" {
			m.setRefusal(why)
			return m, nil
		}
		m.mode, m.input, m.promptKind = modePrompt, "", promptThread
		m.setStatus("", false)
	}
	return m, nil
}

// openDiffOfSelection is `d`: the difftool for whatever the cursor is on. A file row is always
// diffable — it is in the span by construction — and so is a directory, where git expands the
// pathspec into every file the span changed under it: one key for "show me this package". Anything
// else goes through the same decision Enter makes for it.
func (m reviewModel) openDiffOfSelection() (tea.Model, tea.Cmd) {
	r, _, ok := m.activeRow()
	if !ok {
		return m, nil
	}
	if inFileBlock(r.kind) {
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
		m.setRefusal("nothing to diff: " + r.name + " is a heading, not a file")
		return m, nil
	}
	if why := m.cannot("edit " + r.name); why != "" && m.documentAction(r) != actionDiff {
		// Only the diff is available over history. The file on disk is not the file this
		// span contains, so opening it in an editor would edit something the review is not
		// about, and creating a missing ABOUT.md would create it in the working tree.
		m.setRefusal(why)
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
	return m.openPathNoted(r.path, m.editorNote(r.path, r.name))
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

// editorNote says which of two different reasons sent a file to the editor: it is new,
// or nothing happened to it in this span. Both are worth saying, because both are a row whose `d`
// would open a comparison with nothing on one side of it.
func (m reviewModel) editorNote(path, name string) string {
	if m.inSpan[path] {
		return name + " was added by this changeset, so there is nothing to compare it against — opened in the editor"
	}
	return name + " has not changed in this span — opened in the editor"
}

// openFile is what Enter and the pane's enter do with a file: the difftool, except for a file the span
// added, which the editor reads. See fileAction for why that one file is different, and openArtifact
// for the same reasoning applied to a changeset document -- including the read-only gate, which lives
// here because Enter is not a mutating key and so never passes through the one in handleKey. Over a
// historical span the editor is refused the way `e` refuses it, and a file the span added that the
// working tree no longer has goes to the difftool rather than to an empty buffer, because the patch is
// the one place git still has the file's other side.
func (m reviewModel) openFile(path string, c Change) (tea.Model, tea.Cmd) {
	if c != ChangeAdded {
		return m.openDiff(path)
	}
	name := filepath.Base(path)
	if why := m.cannot("edit " + name); why != "" {
		m.setRefusal(why)
		return m, nil
	}
	if _, err := os.Stat(absPath(m.sess.Repo().Dir, path)); err != nil {
		return m.openDiff(path)
	}
	return m.openPathNoted(path, m.editorNote(path, name))
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
// openEditor edits the row under the cursor: a file, a thread, or ABOUT.md. A directory is not a
// thing an editor has to open — the tool would find its own way in — and the keys that do want the
// whole subtree are already there, so the row says so instead of sending a path to the editor.
func (m reviewModel) openEditor() (tea.Model, tea.Cmd) {
	r, _, ok := m.activeRow()
	if !ok || r.path == "" {
		return m, nil
	}
	if r.kind == rowDir {
		m.setRefusal(r.name + " is a directory — enter folds it, d diffs what is under it")
		return m, nil
	}
	return m.openPath(r.path)
}

// openAbout is gone, and the box's cursor is the reason: `a` used to hand the terminal to the editor from
// wherever the keys were, which left a reviewer who had been reading the box looking at a file they had
// not chosen. `a` now puts the cursor on the ABOUT.md row and `e` opens it, so the row the editor is about
// to show is on screen before the handoff.

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
	// styleActive marks the column that has the keys: the divider becomes a double rule and the pane's
	// file line stops being a caption over the diff and becomes its title. Bold rather than coloured,
	// because a colour is the one thing this terminal is not obliged to render.
	styleActive = lipgloss.NewStyle().Bold(true)
	// styleIdle is the cursor of the region that does not have the keys. That row is still where the
	// reviewer left it — which is the point of a region keeping its own cursor — but two full highlights
	// on the screen would be two things to look at, so this is the same highlight with the intensity
	// turned down.
	styleIdle = lipgloss.NewStyle().Reverse(true).Faint(true)
	styleErr  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	styleMark = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	styleSpan = lipgloss.NewStyle().Bold(true)
	// stylePartial is a directory whose files do not all agree: green is a subtree read, faint is
	// one untouched, and this is the part way through, where the row counts what is left instead of
	// claiming the mark.
	stylePartial = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	// The tree's change signs wear the colours of the diff beside them: green for a file the span
	// created, red for one it deleted, blue for one it moved. Those are git's own colours for the same
	// three facts, so the character in the tree and the body in the pane read as one answer rather
	// than two. Green and red are both spoken for on this screen -- green is a reviewed mark, red is a
	// refusal -- and both collisions are safe because of where they sit: a mark is the first cells of
	// a row and a sign the last, and a refusal is a line of the band rather than a character after a
	// name.
	//
	// The numbers are palette slots rather than hues: the terminal's theme paints them, the way it paints
	// the `32` and `36` git sends for its own diff, so a theme moves the tree and the pane together. git's
	// green is index 2 and this is the bright slot, 10, because 10 and 9 are already this screen's green and
	// red -- the agreement with git is by hue family, and the agreement inside the app is exact.
	styleSignAdded   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	styleSignDeleted = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	styleSignMoved   = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	// The reviewer's own rows in the preview, told apart by colour where the pane shows one file and the
	// header has already named it: blue is a line they added, purple a line the span added that they then
	// deleted, amber a line the span never touched that they deleted. Three colours git's diff has no word
	// for, because git's diff does not know who typed anything.
	styleYouAdd    = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	styleYouUndo   = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))
	styleYouDelete = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	// styleWarn is the drift banner: a warning about the ground moving, not an error about
	// something the reviewer just did.
	styleWarn = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	// The tree's mark on a file or a folded directory the reviewer has written in and not committed.
	// Magenta is already this screen's "written by you" -- it is the colour of a line the reviewer
	// deleted in the pane beside the tree -- and the tree spends no other magenta, so the colour arrives
	// with one meaning and keeps it. Bold goes with it because a colour is the one thing a terminal is
	// not obliged to render: with no colour to give, the weight is still a difference between this name
	// and its neighbours, which is what the gutter's ✱ cannot do for a file that is also reviewed.
	styleDirtyName = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	// styleDirtyMark is the same fact in the gutter, where the glyph carries it on its own and only the
	// colour is added, so the mark reads as part of the column of marks rather than as a loud word.
	styleDirtyMark = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))
)

func (m reviewModel) View() string {
	if m.quitting {
		return ""
	}
	var b strings.Builder
	switch m.mode {
	case modeSpan:
		b.WriteString(m.pickerBlock())
	case modePreview:
		// The whole screen is the diff. The rule goes under it, with the shortcut bar, which is what
		// the diff's own chrome counts.
		b.WriteString(strings.Join(m.previewLines(), "\n") + "\n")
		b.WriteString(m.rule() + "\n")
	default:
		if m.paneWidth() > 0 {
			b.WriteString(joinColumns(m.listBlock(), m.previewLines(), m.listWidth(), m.previewHasFocus()))
		} else {
			b.WriteString(m.listBlock())
		}
		b.WriteString(m.rule() + "\n")
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

// boxLabel is the left column inside the changeset box: "base", "span" and "parent" are the same width, so
// the values under them line up, and the words are the only explanation of which row is which. Seven cells
// is what "parent" costs, and it is the word a reader of a landed child is looking for.
func boxLabel(word string) string {
	return styleDim.Render(word + "      "[:7-len(word)])
}

// baseLine is the box's first row that is not a choice: what the span is measured against. The span
// beside it is a row, because that is the one a reviewer changes. Both are shortened the same way, so the
// pair reads as one answer — `origin/main` and `origin/main...current` — rather than as two bases that
// happen to agree.
func (m reviewModel) baseLine() string {
	return boxLabel("base") + span.ShortRef(m.sess.Header().Base)
}

// parentLine is the base row's footnote, and the reason rule 2 could stop printing a commit: once a landed
// parent moves the measurement onto the destination, the base row names a branch and stops saying why. This
// says the one word the destination cannot — `landed` — beside the parent's changeset id, which is the name
// `status`, the queue and the `Stack:` section all use. A line rather than a row, because it is not a choice,
// and empty for every changeset not in this shape, so the ordinary box loses no height.
func (m reviewModel) parentLine() string {
	p := m.sess.Header().ParentLanded
	if p == "" {
		return ""
	}
	return boxLabel("parent") + p + " " + styleDim.Render("landed")
}

// boxInner is how wide a row inside the box may be: the list column less the border characters and the
// space beside each of them.
func (m reviewModel) boxInner() int {
	if inner := m.listWidth() - 4; inner > 8 {
		return inner
	}
	return 8
}

// boxLines draws the changeset box: the two borders, the base line inside them, and the rows of the
// region — the span, ABOUT.md, the thread heading, the threads, and the row that starts another. The
// borders are the box's focus light, the same job the double rule does for the preview: faint when the
// keys are elsewhere, bold when the box holds them.
func (m reviewModel) boxLines(section []renderedRow) []string {
	f := m.boxFrame()
	frame := styleDim.Render
	if m.metaHasFocus() {
		frame = styleActive.Render
	}
	width := m.listWidth()
	above, below := m.metaHidden()
	up, down := scrollHints(above, below)
	out := []string{frame(boxEdge(f.cornerTopLeft, f.cornerTopRight, f.edge,
		clip(m.sess.Header().Title, m.boxInner()), up, width))}
	out = append(out, m.boxLine(f, m.baseLine(), false))
	if p := m.parentLine(); p != "" {
		out = append(out, m.boxLine(f, p, false))
	}
	for _, r := range section {
		out = append(out, m.boxLine(f, m.rowText(r.row), m.metaCursor == r.index))
	}
	// The box's scroll counts go inside its own borders rather than on a row of their own: a note that
	// arrived and left would change the height of the column, which is what the box is here to stop.
	return append(out, frame(boxEdge(f.cornerBottomLeft, f.cornerBottomRight, f.edge,
		"", down, width)))
}

// boxFrame is the changeset box's border set, which is also the box's focus light: single rules while
// another region holds the keys, double rules while the box holds them. The divider between the list
// and the diff uses the same convention, for the same reason -- bold and faint are the one thing a
// terminal is not obliged to render, and a focus the reviewer cannot see is a focus that eats
// keystrokes.
type boxFrame struct {
	cornerTopLeft, cornerTopRight, cornerBottomLeft, cornerBottomRight string
	vertical, edge                                                     string
}

// The box is closed on all four sides whatever else is on the screen. An earlier version left the right
// side open when the preview column was drawn, on the grounds that the divider was already a rule two
// cells away; a box with no right side is not a box, and the rows inside it read as a column of text
// instead of as the changeset's own block.
var (
	frameIdle = boxFrame{
		cornerTopLeft: "\u256d", cornerTopRight: "\u256e",
		cornerBottomLeft: "\u2570", cornerBottomRight: "\u256f",
		vertical: "\u2502", edge: "\u2500",
	}
	frameActive = boxFrame{
		cornerTopLeft: "\u2554", cornerTopRight: "\u2557",
		cornerBottomLeft: "\u255a", cornerBottomRight: "\u255d",
		vertical: "\u2551", edge: "\u2550",
	}
)

// boxFrame is the frame to draw with: single rules while another region holds the keys, double while
// the box holds them.
func (m reviewModel) boxFrame() boxFrame {
	if m.metaHasFocus() {
		return frameActive
	}
	return frameIdle
}

// boxEdge is one border of the box: a label in it where there is one to say, at the left, and a scroll
// count at its right end where the region has rows off the screen. The rule between the two is what says
// the line is a border rather than a row. The label is dropped before the count -- the count is the thing
// there is no other way to see.
func boxEdge(left, right, rule, label, hint string, width int) string {
	body := ""
	if label != "" {
		body = " " + label + " "
	}
	fill := width - 2 - lipgloss.Width(body) - lipgloss.Width(hint)
	if fill < 0 {
		body, fill = "", width-2-lipgloss.Width(hint)
	}
	if fill < 0 {
		hint, fill = "", width-2
	}
	return left + body + strings.Repeat(rule, fill) + hint + right
}

// boxLine is one row inside the box. The cursor is highlighted only while the box holds the keys: the
// borders already say which region has them, and a row of the box wearing a highlight the keys cannot
// reach is a row that looks chosen and is not. The highlight covers the row's text rather than the
// row's width -- inside a border that spans the column, a full-width bar would read as a selection of
// the whole box.
func (m reviewModel) boxLine(f boxFrame, text string, cursor bool) string {
	inner := m.boxInner()
	if cursor && m.metaHasFocus() {
		text = styleSelected.Render(text)
	}
	return f.vertical + " " + padRight(clip(text, inner), inner) + " " + f.vertical
}

// treeSpine is the rule that belongs to the file tree rather than to the screen: one above the tree,
// one below it. Both are drawn whatever else is on the screen, and both are the tree's focus light --
// single rules while another region holds the keys, double while the tree holds them -- the convention
// the box's borders and the pane's divider already use, chosen over styling alone so it survives a
// terminal that renders no bold. Two horizontal rules rather than a frame, because a frame's two cells
// each side are columns, and the narrow terminal that needs the region marked most is the one with no
// columns to give. The count of what the window hides goes at the end of the rule it belongs to: the
// rule is the region's decoration rather than one of its rows, so the number costs the tree a row of
// nothing and never sits over the row the cursor is on.
func (m reviewModel) treeSpine(hint string) string {
	rule, paint := "\u2500", styleDim.Render
	if m.focus == focusFiles {
		rule, paint = "\u2550", styleActive.Render
	}
	width := m.listWidth()
	if lipgloss.Width(hint) >= width {
		hint = ""
	}
	body := paint(strings.Repeat(rule, width-lipgloss.Width(hint)))
	if hint != "" {
		// The count stays faint while the rule carries the focus light: which region has the keys is the
		// rule's job, and a bold number would say it twice and say it wrong.
		body += styleDim.Render(hint)
	}
	return body
}

// listBlock is the list column on its own: the changeset box, the file tree, and the reviewed counter.
// It is apart from View because the preview is drawn beside exactly this block, row for row, and it is
// padded to the column's height so the two columns end on the same line whatever the window is showing.
func (m reviewModel) listBlock() string {
	reviewed, total := m.sess.Count()
	files, section := m.window()

	lines := m.boxLines(section)
	up, down := scrollHints(m.filesHidden())
	// The row that used to separate the box from the tree is the tree's own top spine, and the tree
	// gets a matching one under its last row: a reviewer on a narrow terminal has rows to spend and
	// no columns to spare, which is the opposite trade to a frame. Each carries the count of what the
	// window hides in its own direction, so nothing here is a row that arrives late.
	lines = append(lines, m.treeSpine(up))
	if total == 0 {
		lines = append(lines, styleDim.Render("(no changed files in this span)"))
	}
	for _, r := range files {
		lines = append(lines, m.line(r))
	}
	lines = append(lines, m.treeSpine(down), m.counterLine(reviewed, total))
	for len(lines) < m.listColumnRows() {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n") + "\n"
}

// footer is the band at the bottom of the screen: one fixed-height block that holds the prompt's
// input line while a title is being typed, a notification while one is on screen, the drift warning
// while a ref has moved, or the shortcut bar. It spans the whole terminal rather than the list
// column, because it belongs to the session rather than to either column.
func (m reviewModel) footer() string {
	var b strings.Builder
	for _, line := range m.band() {
		b.WriteString(line + "\n")
	}
	return b.String()
}

// promptText is the field's contents: the title being typed with the caret after it, or, while the
// field is empty, the caret over the ghost of a title. The caret goes wherever the caret is -- after
// what has been typed, and on the field's first cell when nothing has.
func (m reviewModel) promptText() string {
	if m.input != "" {
		return m.input + threadPromptCursor
	}
	// A ghost too wide for the window is dropped rather than cut: the frame writes one row per
	// line and cuts what overflows, and half a hint reads as a fault rather than as an invitation.
	if lipgloss.Width(threadPromptLabel+threadTitlePlaceholder) > m.width {
		return threadPromptCursor
	}
	return promptGhost()
}

// promptGhost is the caret resting on the first cell of the ghost title. The caret is a block, and a
// block in front of a title nobody has typed reads as a title that is there, so it covers the ghost's
// first letter the way a caret sitting on a cell does -- the letter stays legible inside it, which is
// what says the field is still empty.
func promptGhost() string {
	first, _ := utf8.DecodeRuneInString(threadTitlePlaceholder)
	return styleSelected.Render(string(first)) +
		styleDim.Render(threadTitlePlaceholder[utf8.RuneLen(first):])
}

// band is footer's content, cut or padded to exactly bandRows() rows. Only one thing is shown at a
// time, and the order is what the reviewer needs most: a prompt they are typing into, then what
// their last key did, then the warning about the ground moving under the span.
func (m reviewModel) band() []string {
	var lines []string
	switch {
	case m.mode == modePrompt:
		lines = append(lines, threadPromptLabel+m.promptText())
		lines = append(lines, m.promptHint()...)
	case m.status != "":
		// Each line opens its own colour: the renderer skips rows that have not changed, and a
		// style left open on a skipped row tints whatever is written under it.
		for _, line := range wrapProse(m.status, m.width) {
			if m.statusErr {
				line = styleErr.Render(line)
			}
			lines = append(lines, line)
		}
	case m.driftLine() != "":
		// Full width on purpose: in a split screen the list column is narrow, and a warning that
		// loses "[r] refresh" to an ellipsis warns about nothing. Full width is not unlimited
		// either, so it wraps -- a banner that loses its key to a cut line warns about nothing.
		lines = wrapProse(m.driftLine(), m.width)
	default:
		lines = m.helpBar()
	}
	return fitBand(lines, m.bandRows(), m.width)
}

// bandRows is the height the bottom band always has, and the layout decides it -- never what the band
// happens to be saying, so no notification can change the number of rows the list above it gets. In
// the list that is helpRows, the budget of the tallest bar the session can show, for the reason
// helpRows gives: the keys moving between the three regions must not move the band either. The picker,
// the overlay and the submit prompt each draw one bar, and their band is that bar's height. The prompt
// is the field plus however many rows its own hint takes at this width -- chrome like the bar, so its
// height is settled by the window rather than by a message.
func (m reviewModel) bandRows() int {
	switch m.mode {
	case modePrompt:
		return 1 + max(1, len(m.promptHint()))
	case modeFiles:
		return max(1, m.helpRows())
	}
	return max(1, len(m.helpLines()))
}

// helpBar is the shortcut bar as the band draws it. The submit prompt is drawn at full strength
// because it is a decision rather than a list of keys; everything else here is chrome, and chrome
// is dim.
func (m reviewModel) helpBar() []string {
	lines := m.helpLines()
	if m.mode == modeSubmit {
		return lines
	}
	for i, line := range lines {
		lines[i] = styleDim.Render(line)
	}
	return lines
}

// promptHint is the row under the prompt's field: the two keys that end it, dimmed like the bar whose
// rows they share. It is not a notification -- it says what the keys mean for as long as the field is
// up, so no keystroke and no timer retires it -- and it wraps like the bar, because a hint cut in half
// names neither key.
func (m reviewModel) promptHint() []string {
	if m.promptKind != promptThread {
		return nil
	}
	var lines []string
	for _, line := range wrapProse(threadPromptHint, m.width) {
		lines = append(lines, styleDim.Render(line))
	}
	return lines
}

// fitBand makes a block exactly n rows: a short block is padded with blanks, and a block needing
// more rows than the band has loses its tail to an ellipsis rather than pushing the frame past the
// bottom of the terminal. The shortcut bar is longer than any message this screen sends, so the bar
// is normally the taller of the two and nothing is cut; a message long enough to be cut is the one
// way a notification can say less than it was written to say.
func fitBand(lines []string, n, width int) []string {
	if len(lines) < n {
		for len(lines) < n {
			lines = append(lines, "")
		}
		return lines
	}
	if len(lines) == n {
		return lines
	}
	kept := lines[:n]
	kept[n-1] = clip(kept[n-1]+"\u2026", width)
	return kept
}

// driftLine is the warning that a named ref has moved since this span pinned it. It is derived from
// the session rather than set by a keystroke, which is what keeps it alive: a note may cover the
// band for its few seconds, and then the warning is back until `r` moves the pin. What is on screen
// stays the pinned span -- `r` moves it, and only when asked.
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

// helpText is the shortcut bar for the keys as they mean right now: the bar of the screen the session
// is on, or, in the list, the bar of the region that holds the keys. Shortcut groups are separated by
// two spaces; wrapping breaks between those groups, never inside one.
func (m reviewModel) helpText() string {
	switch m.mode {
	case modeSubmit:
		return "Submit review: [b]lock  [f]eedback  [a]pprove  [esc] cancel"
	case modePrompt:
		return "" // the thread prompt is the input line, not help
	case modeSpan:
		return helpSpan(m.pick.list != nil, m.pick.filtering)
	case modePreview:
		// The overlay's own keys. `p` is not among them: the diff already has the screen and the keys.
		// `esc`, `enter` and `q` are the way back to the list, and the keys that move the keys take the
		// screen down on their way to the region they name.
		return m.overlayHelp()
	}
	return m.helpTextFor(m.focus)
}

// overlayHelp is the whole-screen diff's own bar: the keys the pane reads, with the three exits the
// overlay has -- `esc`, `enter` and `q` all give the screen back -- and the keys that move the keys. It
// is part of the row-area budget wherever the overlay is the only form the diff can take, so that no row
// of it is ever clipped by a bar counted from the list.
//
// `z` is named only where it does something. The overlay is both the wide terminal's full-screen diff
// and the narrow terminal's only diff, and only the first has a column to go back to; a bar that
// offered `z` to the second would name a key that refuses, which is the one thing a bar is not for.
func (m reviewModel) overlayHelp() string {
	back := "esc enter q back"
	if m.previewShortfall() == "" {
		back = "z pane  " + back
	}
	return "j k line  d/u ctrl-d/u half  ctrl-f/b page  gg top  G bottom  / find  n N next  " + back +
		"  f tab list"
}

// helpTextFor is the bar of one region. It names every key that region reads and none that it does not,
// which is what makes a key that does nothing while another region holds them an explained absence
// rather than a dropped keystroke. The keys that mean the same thing from anywhere -- tab, f, p -- are
// named in every bar, because a reviewer should never have to remember which bar they read a key in. The
// two bars of the list column differ only in the keys of the half they name: the box's rows have no folds,
// and the tree's rows are the ones that get marked, diffed and edited.
//
// The file tree's bar is the longest of the three on purpose: it is the bar the preview is cut against
// and the one the layout budgets rows for, so the two shorter bars leave a blank row above the status
// line rather than moving the divider when focus changes.
func (m reviewModel) helpTextFor(target focusTarget) string {
	page := "j/k move  gg/G ends  ctrl-d/u half page  ctrl-f/b page"
	// The keys that leave the column for the diff, and the one that names a half of it. `tab` names the
	// stop it walks to rather than the ring: with two stops there is only one place it can go.
	elsewhere := "p preview  z full  f files  tab preview"
	if target == focusPreview {
		// The diff reads nothing that changes the review, and its three ways out all mean the list
		// column -- `esc` gives the keys back, `f` names the tree, `tab` walks the ring -- so they are
		// one group on the bar rather than three claims on it. `/ find` is named with the two keys that
		// walk what it finds: a search nobody can see the keys for is a feature nobody finds.
		// The two jumps into the box are named here as they are in both bars of the list column: the box
		// is on screen beside the diff, and the row they name is the reason to leave it.
		jumps := "a about  t threads  "
		open := "enter diff"
		if m.previewKind == previewDocument || m.previewKind == previewThreads ||
			fileAction(m.previewFileChange()) == actionEdit {
			// What is on show is a document, or a file the span added, and `enter` opens it in the editor
			// rather than in the difftool -- the bar says "open" because that is the truth of it. Any other
			// file, shown as its patch or as its own text, has a comparison worth opening.
			open = "enter open"
		}
		return "j k line  d/u ctrl-d/u half  ctrl-f/b page  gg top  G bottom  / find  n N next  " +
			jumps + open + "  z full  esc f tab list  q quit"
	}
	// The jumps name a row of the box from either half of the column and take the keys with them. `T`
	// writes, so it is absent from every bar of a span that cannot.
	jumps := "a about  t threads"
	if m.sess.Span().Live() {
		jumps += "  T new thread"
	}
	if target == focusMeta {
		return page + "  enter open  space span  " + jumps + "  " + elsewhere + "  q quit"
	}
	fold := "h/l fold  c fold all"
	if len(treeDirs(m.sess.Files())) == 0 {
		fold = ""
	}
	if !m.sess.Span().Live() {
		// Nothing in this bar may imply the reviewer can act on history.
		return strings.Join([]string{page, fold, "enter open  d diff  " + jumps +
			"  v spans  V picker  " + elsewhere + "  q quit"}, "  ")
	}
	return strings.Join([]string{page, fold, "enter open  d diff  space reviewed  e edit  " + jumps +
		"  v spans  V picker  s submit  " + elsewhere + "  q quit"}, "  ")
}

// helpLines is helpLines fitted to the terminal width. A narrow window gets the overflow on
// the next line instead of leaving it to the terminal, which would break a shortcut in half.
func (m reviewModel) helpLines() []string {
	return wrapGroups(m.helpText(), m.width)
}

// helpRows is how many rows the shortcut bar costs the layout, whichever set of keys is being offered.
//
// This is the budget, not the bar: the chrome that sizes the row area cannot count the focused bar's
// lines, because the bar changes when the keys move and the layout must not. Counting the focused bar
// is what made `p` steal a row from the list — the preview's keys fit on one line and the list's did
// not, so focusing the diff moved the rule and handed the pane a row it had not asked for. The tallest
// bar the session can show is reserved instead, and a shorter one leaves a blank row above the status
// line, which is invisible in a frame that already fills the terminal.
func (m reviewModel) helpRows() int {
	bars := []string{m.helpTextFor(focusFiles), m.helpTextFor(focusMeta)}
	switch {
	case m.paneWidth() > 0:
		// Only count the preview's bar where a preview can actually be focused: the excuse for not
		// having a pane at all is not a reason to spend a row on its bar.
		bars = append(bars, m.helpTextFor(focusPreview))
	case m.previewIsOverlayOnly():
		// Nor where the overlay is the only form the diff has -- except that it is: the ring walks through
		// that screen now, so a row of its bar that the band cannot draw is a key offered nowhere.
		bars = append(bars, m.overlayHelp())
	}
	rows := 0
	for _, bar := range bars {
		if n := len(wrapGroups(bar, m.width)); n > rows {
			rows = n
		}
	}
	return rows
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

// wrapProse lays out a line the footer cannot let the terminal break: first at the group boundaries
// (two or more spaces), then, for a group too wide for the window on its own, at word boundaries. The
// drift banner needs both -- its halves are separated by a gap that ought to survive, and its first
// half is a ref and two shas that have to break somewhere. Cutting a word stays worse than a line that
// overflows, so a single word wider than the window is the one thing that can still run past the edge.
func wrapProse(text string, width int) []string {
	if text == "" {
		return nil
	}
	var out []string
	for _, group := range wrapGroups(text, width) {
		if width <= 0 || lipgloss.Width(group) <= width {
			out = append(out, group)
			continue
		}
		out = append(out, wrapWords(group, width)...)
	}
	return out
}

// wrapWords breaks prose into lines the terminal can show in full. The frame writes one line per row
// and a line wider than the terminal is cut rather than continued, so an explanation longer than the
// window loses its second half: a 30-column reviewer was told "the preview wants 40 columns;" and
// never told what they had. Unlike wrapGroups this breaks on single spaces, because a sentence has no
// group boundaries to break on. A word wider than the width still gets a line to itself -- cutting a
// word is worse than a line that overflows.
func wrapWords(s string, width int) []string {
	if s == "" {
		return nil
	}
	if width <= 0 {
		return []string{s}
	}
	var (
		out   []string
		line  []string
		cells int
	)
	flush := func() {
		if len(line) > 0 {
			out = append(out, strings.Join(line, " "))
			line, cells = nil, 0
		}
	}
	for _, word := range strings.Fields(s) {
		w := lipgloss.Width(word)
		if len(line) > 0 && cells+1+w > width {
			flush()
		}
		if len(line) > 0 {
			cells++
		}
		line = append(line, word)
		cells += w
	}
	flush()
	if len(out) == 0 {
		return []string{s}
	}
	return out
}

// --- helpers ----------------------------------------------------------------

// refresh rebuilds the navigable list from the session, keeping the cursor on the row it was
// on while that row still exists — a thread created in the editor, a rescan after a difftool
// closed, or a collapse should not move the reviewer somewhere else. When the row has gone off
// the screen, the cursor goes to the directory that holds it rather than staying at its old
// index, which after a fold would be some unrelated row further down the list.
// refresh rebuilds the rows and puts each region's cursor back on the row it was on. A row is
// identified by kind and path rather than by index, because the tree re-shapes itself when a directory
// is folded and because a thread the reviewer just wrote appears in the box without being asked; the
// index of a row that is still there is not the number it had.
func (m *reviewModel) refresh() {
	keepFile, keepMeta := m.rowUnder(m.cursor), m.rowUnder(m.metaCursor)
	start := m.metaStart
	m.buildRows()
	// The box's rows sit at whatever index the tree leaves them, so folding a directory moves the whole
	// region up. The box's scroll is a place in the box rather than in the list, so it travels with
	// them: left at the index the region used to begin, it reads as a scroll down the box and hides the
	// region's first rows -- the span among them. The first build has the region start where the list
	// does, which is also where clamp would have put a scroll of its own.
	m.metaScroll += m.metaStart - start
	m.restore(keepFile, &m.cursor)
	m.restore(keepMeta, &m.metaCursor)
	m.clamp()
}

// rowUnder is the row a cursor is on, or nil when it is off the list -- which is how a cursor that was
// never in range stays out of the way of a rebuild that would otherwise look for a row it never had.
func (m *reviewModel) rowUnder(i int) *row {
	if i < 0 || i >= len(m.rows) {
		return nil
	}
	return &m.rows[i]
}

// restore puts a cursor back on the row it was on, or on the directory that contained it when the row
// itself has folded away.
func (m *reviewModel) restore(keep *row, cursor *int) {
	if keep == nil {
		return
	}
	if i := m.rowIndex(keep.kind, keep.path); i >= 0 {
		*cursor = i
	} else if _, i := m.containingDirRow(keep.path); i >= 0 {
		*cursor = i
	}
}

// rowIndex finds the row of a given kind naming a given path, or -1. It is how the cursor knows it
// is still looking at the same thing after the list has been rebuilt.
func (m reviewModel) rowIndex(kind rowKind, path string) int {
	for i, r := range m.rows {
		if r.kind == kind && r.path == path {
			return i
		}
	}
	return -1
}

func (m *reviewModel) buildRows() {
	files := m.sess.Files()
	inSpan := make(map[string]bool, len(files))
	for _, f := range files {
		inSpan[f.Path] = true
	}
	m.inSpan = inSpan
	// The tree is rebuilt from the flat list on every refresh, which is what keeps a directory's
	// counts honest: they are a fact about the files under it, re-read rather than remembered.
	var rows []row
	for _, e := range flattenTree(files, m.folded) {
		if e.dir {
			rows = append(rows, row{kind: rowDir, path: e.path, name: e.name, depth: e.depth,
				file: -1, total: e.total, marked: e.marked, dirty: e.dirty})
			continue
		}
		rows = append(rows, row{kind: rowFile, path: e.path, name: e.name, depth: e.depth, file: e.file,
			change: files[e.file].Change, dirty: e.dirty})
	}
	// The split between the two regions: from here down are the rows the changeset box draws. One index
	// into one list rather than two lists, because a thread the reviewer creates has to appear in the
	// box without anything being told to update it.
	m.metaStart = len(rows)
	// The span is the box's first row and the only one a reviewer changes: what the review is measured
	// against is worth choosing, so it is a row with an action rather than a line to read.
	rows = append(rows, row{kind: rowSpan, name: m.spanName(m.sess.Header().SpanLabel), file: -1})
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

// The list column is one navigable list drawn as two windows: the changeset box, then the file tree under
// it. It used to be two stops on the ring, which made a reviewer carry the keys between two halves of one
// screen with a key whose only other job was the diff. Now `Tab` toggles the column and the diff, and the
// edge the two halves share is passable with `j` and `k`. Each half keeps its own cursor, so crossing and
// coming back lands on the row the reviewer left, and each keeps its own focus light -- the box's double
// border, the tree's double rule -- which is how one column with two halves still says where the keys are.

// What one level of the file tree costs: four cells. A row spends two on its mark gutter before its
// name, and a directory spends two more on its fold arrow -- but the arrow rides inside the indent rather
// than after it (see treeLead), so it costs the row nothing and every row at a depth starts its name in
// one column. Four cells a level then puts a child's name four cells right of the row holding it, and two
// under a top-level directory, which has no indent to spend its arrow on.
const treeIndent = "    "

// treeLead is the cells a row spends before its gutter: the indent, and, for a directory, the two cells
// of its fold arrow taken out of that indent rather than added to it. A nested directory's arrow therefore
// starts two cells left of the gutter of the file beside it, which is what keeps the two names in one
// column. At the top of the tree there is no indent to spend, so a top-level directory's arrow sits where a
// file's gutter would sit and its name starts two cells right of the files beside it -- that row has no row
// above it to line up with.
func treeLead(depth int, dir bool) string {
	if !dir {
		return strings.Repeat(treeIndent, depth)
	}
	// arrowCells is what "▸ " and "▾ " cost: a glyph and a space. The indent gives up exactly that many
	// cells to hold them, and the top of the tree, which has no indent, gives up nothing.
	const arrowCells = 2
	lead := depth*len(treeIndent) - arrowCells
	if lead < 0 {
		lead = 0
	}
	return strings.Repeat(" ", lead)
}

// rowText renders one row. Only the file tree carries a reviewed mark: the artifacts below the
// counter are read rather than diffed.
func (m reviewModel) rowText(r row) string {
	switch r.kind {
	case rowDir:
		arrow := "▸ "
		if !m.folded[r.path] {
			arrow = "▾ "
		}
		text := treeLead(r.depth, true) + styleDim.Render(arrow)
		if m.sess.Span().CanMark() {
			text += m.dirGutter(r)
		}
		name := nameStyle(r).Render(r.name)
		if r.marked > 0 && r.marked < r.total {
			// The count is what a reviewer folds a directory to look for, so it is the one state
			// that says how many are left instead of wearing a mark true of only some of what the
			// row stands for.
			return text + name + styleDim.Render(fmt.Sprintf("  %d/%d", r.marked, r.total))
		}
		return text + name
	case rowFile:
		text := treeLead(r.depth, false)
		if m.sess.Span().CanMark() {
			text += m.fileGutter(r)
		}
		name := nameStyle(r).Render(r.name)
		text += name
		if sign := r.change.Sign(); sign != "" {
			// After the name, the way a directory's count sits after its name: the sign is a fact about
			// the file, not part of its name. Colour carries that where faint used to: a coloured
			// character among plain ones already reads as an annotation, and it is the part a reviewer
			// scans for.
			text += " " + signStyle(r.change).Render(sign)
		}
		return text
	case rowAbout:
		return r.name
	case rowSpan:
		// The control rather than the caption: the arrow says the row goes somewhere, which is the
		// whole difference between a line a reviewer reads and a line a reviewer presses.
		return boxLabel("span") + r.name + " " + styleDim.Render("\u25b8")
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

// nameStyle is which style a file row's name wears, and it is a function for the same reason `signStyle`
// is one: the choice is the thing worth reading and worth testing, and a unit test cannot see a colour a
// terminal has not been given to render.
//
// Magenta is the reviewer's own colour on this screen, so a name in it says that what changed here is
// theirs and is not committed yet. It outranks the faint name of a deleted file: a row git says is gone
// *and* holds an uncommitted change is a row whose deletion is the news, and what the span did to it is
// still on the row, in the sign after the name.
func nameStyle(r row) lipgloss.Style {
	switch {
	case r.dirty:
		return styleDirtyName
	case r.change == ChangeDeleted:
		// A deleted file is the one row whose subject is not there to read, so the name goes faint along
		// with the red sign: the row says the file is gone before the reviewer presses Enter to find out.
		// No other change earns this; a faint name on a file still in the tree would be a claim about the
		// review rather than about the span.
		return styleDim
	}
	return lipgloss.NewStyle()
}

// fileGutter is a file's reviewed mark. Over history there is none: a tick there would mean "this
// reviewer has read it", and marks left on that commit by an earlier review are not this
// reviewer's.
//
// An unreviewed file with an uncommitted change wears ✱ rather than ○. The two are not different kinds
// of state: the gutter answers "has this been read", and ✱ is that same no with "and something has been
// written here" on top of it. A file that is reviewed *and* changed keeps its tick, because the tick is
// the answer the counter is made of, and the magenta name says who changed it.
func (m reviewModel) fileGutter(r row) string {
	f := m.sess.Files()
	if r.file >= 0 && r.file < len(f) && f[r.file].Reviewed {
		return styleMark.Render("✓ ")
	}
	if r.dirty {
		return styleDirtyMark.Render("✱ ")
	}
	return "○ "
}

// signStyle is the colour a file's change sign wears -- git's own colour for that change, so the
// tree and the diff column beside it say the same thing the same way. Who gets a sign at all is
// `Change.Sign`'s answer, and a modification has none, so it has no colour either: this only says
// what an earned sign looks like.
func signStyle(c Change) lipgloss.Style {
	switch c {
	case ChangeAdded:
		return styleSignAdded
	case ChangeDeleted:
		return styleSignDeleted
	case ChangeMoved, ChangeMovedWhole:
		return styleSignMoved
	}
	return lipgloss.NewStyle()
}

// dirGutter is a subtree's mark, and a directory wears one only when every file under it agrees.
// Between the two a tick would be a claim about files nobody has opened, so the mixed row counts
// what is left in rowText and puts the half-mark here.
//
// ✱ stands in for both ○ and ◐ when the directory is folded over a file with an uncommitted change:
// the mark is what a folded row can wear, and the `n/m` count after a partial directory's name is
// unchanged, so nothing about what has been read is lost -- only which of the two empty marks it was.
// A subtree that is reviewed all the way down keeps its tick and says who wrote in it by name.
func (m reviewModel) dirGutter(r row) string {
	switch {
	case r.total > 0 && r.marked == r.total:
		return styleMark.Render("✓ ")
	case r.dirty:
		return styleDirtyMark.Render("✱ ")
	case r.marked == 0:
		return "○ "
	}
	return stylePartial.Render("◐ ")
}

// --- regions ----------------------------------------------------------------

// The row area is split between the two regions. The box gets what it asks for up to a third of the
// area and the list gets the rest, which is what makes a changeset with forty threads a reason to
// scroll the box rather than a reason to hide the diff behind it. The floors are arithmetic rather
// than branches: at the smallest row area the tests allow, the box takes two rows and the list keeps
// six.
const (
	metaShare    = 3
	metaMinRows  = 2
	filesMinRows = 3
	// minRowArea is the floor the split is allowed to go to, and it is the list's floor rather than a
	// comfortable number: the frame is written one line per row, so a row area padded up past what the
	// terminal has left overflows the window and the last line of the shortcut bar scrolls off the top.
	minRowArea = filesMinRows
	// columnChrome is what the list column writes around its two regions: the box's two borders, the
	// base line inside it, the blank under it, the blank over the counter and the counter.
	columnChrome = 6
)

// fileRows and metaRows are the two regions of the one list: the tree, then the rows the box draws.
// metaStart is where the second begins, which is why a region is a slice of rows rather than a second
// list that would have to be kept in step with the first.
func (m reviewModel) fileRows() []row { return m.rows[:m.metaStart] }
func (m reviewModel) metaRows() []row { return m.rows[m.metaStart:] }

// rowArea is what the two regions share: the terminal less everything the frame writes around them.
func (m reviewModel) rowArea() int {
	if area := m.height - m.chromeRows(); area > minRowArea {
		return area
	}
	return minRowArea
}

// regionHeights splits the row area into the rows the box shows and the rows the list shows.
func (m reviewModel) regionHeights() (meta, files int) {
	area := m.rowArea()
	meta = min(len(m.metaRows()), max(metaMinRows, area/metaShare))
	return meta, area - meta
}

func (m reviewModel) metaWindow() int {
	meta, _ := m.regionHeights()
	return meta
}

func (m reviewModel) filesWindow() int {
	_, files := m.regionHeights()
	return files
}

// listColumnRows is how tall the list column is above the rule: both regions and the chrome between
// them. It is the height the preview column is cut to, so the two columns end on the same line. It is
// not picker.go's columnRows, which is how much room the span picker's two candidate lists have.
func (m reviewModel) listColumnRows() int {
	meta, files := m.regionHeights()
	return meta + files + columnChrome
}

// clamp keeps each region's cursor inside its own rows and inside its own window.
func (m *reviewModel) clamp() {
	m.clampRegion(&m.cursor, &m.scroll, 0, m.metaStart, m.filesWindow())
	m.clampRegion(&m.metaCursor, &m.metaScroll, m.metaStart, len(m.rows), m.metaWindow())
}

// clampRegion keeps one region's cursor inside [start, end) and its window scrolled to show it.
// Without it `k` at the top drives an index negative and View indexes rows[-1], which panics the whole
// program — and with two regions it takes a guard of the same size to stop the box's cursor walking
// into the file list.
func (m *reviewModel) clampRegion(cursor, scroll *int, start, end, window int) {
	if start >= end {
		*cursor, *scroll = start, 0
		return
	}
	if *cursor < start {
		*cursor = start
	}
	if *cursor > end-1 {
		*cursor = end - 1
	}
	if *scroll < start {
		*scroll = start
	}
	if *cursor < *scroll {
		*scroll = *cursor
	}
	if *cursor >= *scroll+window {
		*scroll = *cursor - window + 1
	}
	// ... and it never starts so far down that it cannot fill itself. A region that lost rows -- the
	// threads folded away, a rebuild that found fewer files -- leaves a scroll pointing past what is
	// left, and the box would draw two rows under a border built for six.
	if lowest := max(start, end-window); *scroll > lowest {
		*scroll = lowest
	}
}

// activeRow is the row the action keys work on: the focused region's cursor. While the diff holds the
// keys the region they came from is the one that answers, which is why the diff shows a file from the
// list it was opened over.
func (m reviewModel) activeRow() (row, int, bool) {
	if m.metaHasFocus() {
		if m.metaCursor < m.metaStart || m.metaCursor >= len(m.rows) {
			return row{}, 0, false
		}
		return m.rows[m.metaCursor], m.metaCursor, true
	}
	if m.cursor < 0 || m.cursor >= m.metaStart {
		return row{}, 0, false
	}
	return m.rows[m.cursor], m.cursor, true
}

// move steps the list column's cursor. The box and the tree are one navigable list drawn as two windows,
// and the edge they share is passable: `k` off the top of the tree lands on the box's last row, and `j`
// off that row is back on the tree's first. Each half keeps its own cursor, so crossing and coming back
// returns the reviewer to the row they left. A step of more than one row is a page, and a page stays
// inside the window it started in: the two halves have different heights, so a page counted in one means
// nothing in the other.
func (m *reviewModel) move(delta int) {
	if delta == 1 && m.metaHasFocus() && m.metaCursor >= len(m.rows)-1 && m.metaStart > 0 {
		m.focus, m.cursor = focusFiles, 0
		m.clamp()
		return
	}
	if delta == -1 && m.focus == focusFiles && m.cursor <= 0 && m.metaStart < len(m.rows) {
		m.focus, m.metaCursor = focusMeta, len(m.rows)-1
		m.clamp()
		return
	}
	if m.metaHasFocus() {
		if m.metaStart >= len(m.rows) {
			return
		}
		m.metaCursor += delta
	} else {
		if m.metaStart == 0 {
			return
		}
		m.cursor += delta
	}
	m.clamp()
}

// activeTop and activeBottom are what gg and G mean: the two ends of the region holding the keys. Each
// half of the list column is its own window with its own height, so the end that matters is the one in
// the window the reviewer is looking at -- the same reason a page stays inside the window it started in.
// `gg` is the first row of that region and `G` its last, and neither takes the keys to the other half:
// `tab`, `f` and the two jumps into the box do that, and a jump that also changed region would land the
// reviewer at the end of a list they were not reading. Where the region holding the keys has no rows --
// an empty span, a changeset with no box -- the other one is the whole column and gets the jump.
func (m *reviewModel) activeTop() {
	switch {
	case m.metaHasFocus() && m.metaStart < len(m.rows):
		m.metaCursor = m.metaStart
	case m.focus == focusFiles && m.metaStart > 0:
		m.cursor = 0
	case m.metaStart < len(m.rows):
		m.focus, m.metaCursor = focusMeta, m.metaStart
	default:
		m.focus, m.cursor = focusFiles, 0
	}
	m.clamp()
}

func (m *reviewModel) activeBottom() {
	switch {
	case m.metaHasFocus() && m.metaStart < len(m.rows):
		m.metaCursor = len(m.rows) - 1
	case m.focus == focusFiles && m.metaStart > 0:
		m.cursor = m.metaStart - 1
	case m.metaStart > 0:
		m.focus, m.cursor = focusFiles, m.metaStart-1
	default:
		m.focus, m.metaCursor = focusMeta, len(m.rows)-1
	}
	m.clamp()
}

// focusRing is the order Tab walks: the list column -- the changeset box and the file tree under it, which
// share their keys -- and the diff wherever it can be shown. A terminal with room for neither the pane nor
// the overlay has one stop and Tab does nothing there, because a target that cannot be drawn is a target
// whose keys go nowhere.
func (m reviewModel) focusRing() []focusTarget {
	ring := []focusTarget{m.listFocus()}
	if m.paneWidth() > 0 || m.previewIsOverlayOnly() {
		ring = append(ring, focusPreview)
	}
	return ring
}

// listFocus is the half of the list column that holds the keys -- or, while the diff holds them, the half
// that handed them over, so Tab out of the diff returns to the box when the reviewer walked into the diff
// from the box.
func (m reviewModel) listFocus() focusTarget {
	if m.focus == focusPreview {
		if m.prevFocus == focusMeta {
			return focusMeta
		}
		return focusFiles
	}
	return m.focus
}

// previewIsOverlayOnly is whether the diff can only be shown over the whole screen: there is no room for
// a second column beside the list, but the screen itself is big enough for the overlay. It is the narrow
// terminal's case, and the reason the diff is still a stop on the ring there -- the overlay is how that
// region gets drawn when there is no column to focus, so leaving it out would put a hole in the ring
// exactly where the pane cannot be.
func (m reviewModel) previewIsOverlayOnly() bool {
	return m.previewShortfall() != "" && m.overlayShortfall() == ""
}

// focusOn hands the keys to a region. The status line goes with them: the shortcut bar is what names
// the keys of the region that holds them, and a note left over from the last keystroke would read as
// an answer to this one.
func (m *reviewModel) focusOn(to focusTarget) {
	if to == focusPreview && m.paneWidth() == 0 {
		if !m.previewIsOverlayOnly() {
			return
		}
		// The narrow terminal's version of the same move: with no column to focus, the overlay is how the
		// diff is drawn. Where the keys came from is not drawn while it is up, which is what `esc` -- and
		// `f` and `tab` -- take back down on the way there.
		from := m.focus
		m.rememberLeft()
		m.takeScreen(from)
		return
	}
	if to == focusPreview {
		m.rememberLeft()
	} else {
		// The last half of the column the keys were in, so the diff gives them back there rather than
		// always to the tree: reading a diff from the box should land back on the box.
		m.prevFocus = to
	}
	m.previewG = false
	if m.mode == modePreview && to != focusPreview {
		// The region named is only drawn once the diff stops covering the screen.
		m.mode = modeFiles
	}
	m.focus = to
	m.clamp()
	m.setStatus("", false)
}

// rememberLeft notes which half of the list column is giving the keys up, so `esc` and `tab` hand them
// back to the half they came from. It is asked on the way into the diff rather than on the way out of
// walking-up navigation: `k` into the box changes the column's cursor without anyone calling focusOn, so
// the half holding the keys has to be read from the focus itself.
func (m *reviewModel) rememberLeft() {
	if m.focus != focusPreview {
		m.prevFocus = m.focus
	}
}

// cycleFocus walks the ring; dir is -1 for shift-tab, so the ring goes both ways and a reviewer who
// overshoots comes back rather than walking the long way round. With two stops the two directions are the
// same toggle, and the reviewer still lands on the half of the list column they left rather than on the
// tree's top row.
func (m *reviewModel) cycleFocus(dir int) {
	ring := m.focusRing()
	at := 0
	for i, target := range ring {
		if target == m.focus {
			at = i
			break
		}
	}
	m.focusOn(ring[(at+dir+len(ring))%len(ring)])
}

// gotoRow puts the list column's cursor on the first row of a kind and returns whether there was one. The
// box's rows are the ones worth naming from anywhere in the column: ABOUT.md is what a reviewer comes to
// the box to read, and the thread heading is the fold that decides how much of the box they read to get
// to it. The span row above them is the box's one control, and a reviewer changes a span least often.
func (m *reviewModel) gotoRow(kind rowKind) bool {
	for i := m.metaStart; i < len(m.rows); i++ {
		if m.rows[i].kind == kind {
			m.focus = focusMeta
			m.metaCursor = i
			m.clamp()
			m.setStatus("", false)
			return true
		}
	}
	return false
}

// activeWindow is how many rows the region holding the keys draws, which is the distance a page is.
func (m reviewModel) activeWindow() int {
	if m.metaHasFocus() {
		return m.metaWindow()
	}
	return m.filesWindow()
}

// previewTarget is what the pane should have in it: the row the keys are standing on while the list column
// holds them, and what the pane is already showing while it holds them itself. The box's rows are documents,
// and the pane is the place on this screen that shows text to read, so the pane follows the box's cursor the
// way it has always followed the tree's -- `a` puts the cursor on ABOUT.md and ABOUT.md is what appears.
//
// A row with nothing to show -- the span line, "+ new thread…" -- keeps the pane where it was, the same way
// a directory row with no diff in the span does: a pane that blanks out while the cursor passes over a
// heading is a pane that loses the file you were reading.
func (m reviewModel) previewTarget() (previewKind, string, bool) {
	if m.quitting {
		return previewDiff, "", false
	}
	if (m.previewHasFocus() || m.mode == modePreview) && m.previewPath != "" {
		// Reading the diff. The row it is showing is the row it is showing: the list's cursor cannot move
		// from here, and a pane that changed under a reviewer reading it would be the one thing a pane
		// should not do.
		return m.previewKind, m.previewPath, true
	}
	if !m.previewShowing() {
		return previewDiff, "", false
	}
	// With nothing on show yet -- `p` taken the screen before a row had been chosen -- there is nothing to
	// keep, and the row under the cursor is the answer.
	return m.previewRowTarget()
}

// previewRowTarget is the pane's answer for the row the keys are standing on. It is what the pane follows
// while the list column holds the keys, and what `p` asks for when it opens the screen that will show it --
// where a narrow terminal has had no pane to keep a place in, and the row being pointed at is the only
// answer to "show me this".
func (m reviewModel) previewRowTarget() (previewKind, string, bool) {
	r, _, ok := m.activeRow()
	if !ok {
		return previewDiff, "", false
	}
	switch r.kind {
	case rowFile:
		// A file the span created, or moved with its bytes unchanged, is read rather than diffed: the
		// patch for either is the file again, louder. Everything else keeps the patch.
		if i := r.file; i >= 0 && i < len(m.sess.Files()) && m.sess.Files()[i].Change.ReadsAsFile() {
			return previewContent, r.path, true
		}
		return previewDiff, r.path, true
	case rowDir:
		return previewDiff, r.path, true
	case rowAbout, rowThread:
		return previewDocument, r.path, true
	case rowThreadsHead:
		return previewThreads, m.sess.Changeset().Dir, true
	}
	return m.previewKind, m.previewPath, m.previewPath != ""
}

// documentFor reads the pane's text, which is one file for a document row and the whole thread list for the
// heading. The seam is here rather than at the two call sites so that a test says what the changeset contains
// without saying which of the two ways it was read.
func (m reviewModel) documentFor(ctx context.Context, path string) Document {
	if m.docFor != nil {
		return m.docFor(ctx, path)
	}
	if path == m.sess.Changeset().Dir {
		return m.sess.ThreadDocument(ctx)
	}
	return m.sess.Document(ctx, path)
}

// readContent is where a file's own text comes from: the seam when a test has one, the session's answer
// otherwise.
func (m reviewModel) readContent(ctx context.Context, path string) Document {
	if m.contentFor != nil {
		return m.contentFor(ctx, path)
	}
	return m.sess.FileContent(ctx, path)
}

func (m reviewModel) selectedRow() (row, bool) {
	// The file list's cursor, wherever the keys are: the preview shows the file the list is on, and a
	// box row is not something the pane can diff. Keys that act on a row ask activeRow instead.
	if m.cursor < 0 || m.cursor >= m.metaStart {
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

// window splits the rows that fit each region's window into the two blocks View draws: the changeset
// box's rows and the file list's. Two windows over one list, because the list is one list — a thread
// the reviewer creates lands in the box without the file list being told, and each region scrolls
// without moving the other.
func (m reviewModel) window() (files, section []renderedRow) {
	for _, idx := range m.visibleMeta() {
		section = append(section, renderedRow{row: m.rows[idx], index: idx})
	}
	for _, idx := range m.visibleFiles() {
		files = append(files, renderedRow{row: m.rows[idx], index: idx})
	}
	return files, section
}

// visibleFiles and visibleMeta are the rows of one region that fit its own window.
func (m reviewModel) visibleFiles() []int {
	return visibleRows(0, m.metaStart, m.scroll, m.filesWindow())
}

func (m reviewModel) visibleMeta() []int {
	return visibleRows(m.metaStart, len(m.rows), m.metaScroll, m.metaWindow())
}

func visibleRows(start, end, scroll, window int) []int {
	var rows []int
	for i := max(start, scroll); i < end && i < scroll+window; i++ {
		rows = append(rows, i)
	}
	return rows
}

// filesHidden and metaHidden count the rows each region has off its own screen, above and below. They
// are read off the same window the rows are drawn from, so the number a reviewer is told and the rows
// they are not shown cannot disagree.
func (m reviewModel) filesHidden() (above, below int) {
	return hiddenAround(0, m.metaStart, m.scroll, m.filesWindow())
}

func (m reviewModel) metaHidden() (above, below int) {
	return hiddenAround(m.metaStart, len(m.rows), m.metaScroll, m.metaWindow())
}

func hiddenAround(start, end, scroll, window int) (above, below int) {
	if start >= end {
		return 0, 0
	}
	top := max(start, scroll)
	if top > start {
		above = top - start
	}
	if rest := end - (top + window); rest > 0 {
		below = rest
	}
	return above, below
}

// scrollHints is one region's pair of counts, for the two ends of the region's own rule: the rows above
// the window and the rows below it. The two are rendered into the same numeric field -- as wide as the
// longer of the two numbers -- so the two arrows sit in the same column whatever the counts say, and the
// digits line up under it. An arrow that moved a cell because its count grew past 9 would make the pair
// read as two unrelated notes rather than as the two ends of one thing.
func scrollHints(above, below int) (top, bottom string) {
	digits := 1
	for _, n := range []int{above, below} {
		if d := len(fmt.Sprint(max(n, 0))); d > digits {
			digits = d
		}
	}
	return scrollHint(above, true, digits), scrollHint(below, false, digits)
}

// scrollHint is one of those counts: an up arrow with what is above the window, a down arrow with what is
// below it, the number padded to `digits` cells. It is padded with whitespace on both sides so it reads as
// a note pinned to the rule rather than as the last cell of whatever sits beside it, and it is empty when
// there is nothing that way to count.
func scrollHint(hidden int, above bool, digits int) string {
	if hidden <= 0 {
		return ""
	}
	arrow := "\u2193" // down: what is below the window
	if above {
		arrow = "\u2191" // up: what is above it
	}
	return fmt.Sprintf("  %s %*d ", arrow, digits, hidden)
}

// The preview pane is a wide-terminal luxury, so it has an entry condition rather than a squeeze:
// below these dimensions there is no pane at all, and `p` says which way the terminal is short.
// Above them the two columns are content-driven: the list takes what its own text needs, capped,
// and the diff gets the rest.
// The overlay has its own, lower floor, because it has no list to share the terminal with: the
// columns it must spend are the diff's, so the minimum is the narrowest column worth reading and
// the fewest rows that leave one row of diff after the file line, the rule and the shortcut bar.
// TestOverlayFloorHasRoomToRead asserts the arithmetic rather than trusting it.
const (
	previewOverlayMinWidth = 40
	// The bar is what sets this, not the diff: at the floor's 40 columns the overlay's own bar wraps to
	// four rows, and a bar whose rows the band cannot draw is a key offered nowhere. The body gets what is
	// left, so a row added to that bar is a row the floor has to grow by -- TestOverlayFloorHasRoomToRead
	// does that arithmetic rather than trusting this number.
	previewOverlayMinHeight = 13
)

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

// listContentWidth is what the list would take to write everything in full: its longest row, and the
// box above it. It measures every row rather than the visible window, so the divider does not move
// when a longer path scrolls into view, and it is the width the rows are then clipped to -- a column
// that grew because a path scrolled past would be a column that never stops growing.
func (m reviewModel) listContentWidth() int {
	widest := 0
	// The box's rows are inside a border, which costs four cells apiece.
	wide := func(s string, pad int) {
		if w := lipgloss.Width(s) + pad; w > widest {
			widest = w
		}
	}
	wide(m.sess.Header().Title, 4)
	wide(m.baseLine(), 4)
	if p := m.parentLine(); p != "" {
		wide(p, 4)
	}
	for _, r := range m.metaRows() {
		wide(m.rowText(r), 4)
	}
	for _, r := range m.fileRows() {
		wide(m.rowText(r), 0)
	}
	if _, total := m.sess.Count(); total > 0 {
		wide(fmt.Sprintf("%d / %d reviewed", 0, total), 0)
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

// dividerAt is the cell the two columns are separated at: the list column, then the space joinColumns
// leaves before the rule. The gap is three cells -- space, rule, space -- so the rule is the middle one.
func (m reviewModel) dividerAt() int { return m.listWidth() + previewGap/2 }

// paneWidth is how wide the preview column is, or 0 when there is no preview: the reviewer
// switched it off, the session is taking input, or the terminal is too small. One function decides
// it, because layout and key handling have to agree on whether the pane is there.
func (m reviewModel) paneWidth() int {
	if !m.previewOn || !m.paneAllowed() {
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

// previewBodyRows is how many lines of diff fit: in the pane, the window the list gets, less the file
// it belongs to and the note about what is not on show; in the overlay, what the screen has left after
// its own chrome.
func (m reviewModel) previewBodyRows() int {
	if m.mode == modePreview {
		rows := m.height - m.overlayChrome()
		if rows < 1 {
			return 1
		}
		return rows
	}
	rows := m.listColumnRows() - 2
	if rows < 1 {
		return 1
	}
	return rows
}

// previewHasFocus is whether the diff has the keys: a pane is on screen and the reviewer moved into
// it. Nothing decides a keystroke from the focus value alone, because a terminal resized under the
// session takes the pane away while the reviewer is looking at it — and a keyboard held by a column
// that is no longer drawn is a session that reads nothing at all.
func (m reviewModel) previewHasFocus() bool {
	return m.focus == focusPreview && m.paneWidth() > 0
}

// metaHasFocus is whether the changeset box holds the keys, asked the same way: the box is always
// drawn, so the answer is the focus itself, and the one place that has no box to give the keys to is
// the overlay, which has the screen.
func (m reviewModel) metaHasFocus() bool {
	return m.focus == focusMeta && m.mode == modeFiles
}

// previewShowing is whether a diff is on the screen right now, in either layout. Fetching, key
// handling and the frame all ask this rather than paneWidth, because the overlay has no pane to
// measure -- and because a fetch keyed to a column that is not drawn is a git call for nothing.
// paneAllowed is whether the mode leaves the screen shared. The prompts ask their question in a line of
// the footer, so the diff stays beside the list while a thread title is being typed and while a submit
// prompt is open: the reviewer who pressed `t` beside a diff comes back from the editor to the same diff,
// not to a screen that lost it on the way. The picker and the whole-screen preview take the screen, so
// they give the column up.
func (m reviewModel) paneAllowed() bool {
	return m.mode == modeFiles || m.mode == modePrompt || m.mode == modeSubmit
}

func (m reviewModel) previewShowing() bool {
	return m.mode == modePreview || m.paneWidth() > 0
}

// previewWidth is the column the diff is drawn in: the pane's, or the whole terminal when the overlay
// has it.
func (m reviewModel) previewWidth() int {
	if m.mode == modePreview {
		return m.width
	}
	return m.paneWidth()
}

// overlayChrome counts the rows the overlay spends on itself rather than on the diff: the file line
// above it, the note below it, the rule, and the band below that -- counted at the height it always
// takes, whatever it is showing. The list's header, counter and threads are not drawn here, so they
// are not counted: that is what buys the extra rows of diff, and why the overlay is what a small
// terminal gets.
func (m reviewModel) overlayChrome() int {
	return 3 + m.bandRows()
}

// overlayShortfall names why not even the overlay fits, or "" when it does. It is the smaller ask of
// the two, so it is the excuse worth giving: telling a 10-row terminal that the preview wants 16 rows
// hides the fact that 12 would have been enough.
func (m reviewModel) overlayShortfall() string {
	if m.width < previewOverlayMinWidth {
		return fmt.Sprintf("the preview wants %d columns; this terminal has %d", previewOverlayMinWidth, m.width)
	}
	if m.height < previewOverlayMinHeight {
		return fmt.Sprintf("the preview wants %d rows; this terminal has %d", previewOverlayMinHeight, m.height)
	}
	return ""
}

// rule is the separator over the shortcut bar, and it spans the whole terminal rather than the column
// above it. A rule that stopped at the divider read as a property of the list — the end of that column
// — when what it separates is the session's screen from the session's keys, and the divider occluded
// the rest of it in both split layouts.
func (m reviewModel) rule() string {
	width := m.width
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
	kind, path, ok := m.previewTarget()
	if !ok || path == "" {
		// Only "the cursor is on something with no file to show" forgets the pane. When the preview
		// is merely off-screen -- `p` off, or the overlay closed -- the place is kept, so coming back
		// returns to the same lines rather than to the top of the file. Keeping an out-of-date diff is
		// forgetPatches' job, not this one's.
		if m.previewShowing() {
			m.previewPath, m.previewOffset, m.previewMatch, m.previewKind = "", 0, -1, previewDiff
		}
		return m, nil
	}
	if kind != m.previewKind || path != m.previewPath {
		// The term is deliberately not cleared: the point of a term you have typed is that it is the
		// next file's term too, which is how a name you are chasing across a changeset gets chased.
		// What goes is the position in the old file, which has no meaning in this one.
		m.previewKind, m.previewPath = kind, path
		m.previewOffset, m.previewMatch, m.previewMatchTerm = 0, -1, ""
	}
	if kind == previewContent {
		// The file's text, and -- when the span is live -- the reviewer's own edits to the same file, which
		// the title reports instead of letting committed text pass for the working copy.
		var cmds []tea.Cmd
		if _, cached := m.contents[path]; !cached {
			path, ctx := path, m.ctx
			cmds = append(cmds, func() tea.Msg {
				return contentMsg{path: path, doc: m.readContent(ctx, path)}
			})
		}
		if m.sess.Span().Live() {
			if _, cached := m.patch(patchWorking, path); !cached {
				src, fetch, ctx := patchWorking, m.fetcher(patchWorking), m.ctx
				cmds = append(cmds, func() tea.Msg {
					return previewMsg{path: path, patch: fetch(ctx, path), kind: src}
				})
			}
		}
		if len(cmds) == 0 {
			return m, nil
		}
		return m, tea.Batch(cmds...)
	}
	if kind != previewDiff {
		if _, cached := m.docs[path]; cached {
			return m, nil
		}
		path, ctx := path, m.ctx
		return m, func() tea.Msg { return docMsg{path: path, doc: m.documentFor(ctx, path)} }
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
	m.patches, m.working, m.docs, m.contents = nil, nil, nil, nil
	m.previewPath, m.previewOffset, m.previewMatch, m.previewKind = "", 0, -1, previewDiff
	// The field closes with the rows it was searching: a term typed against a diff that is gone is a
	// field with nothing under it. The term itself stays, for the next file to be typed into.
	m.searching, m.searchInput = false, ""
}

// togglePreview is `p`. A terminal with room for two columns gets the pane beside the list. A
// narrower one gets the same diff over the whole screen: the reviewer asked for the diff, and a pane
// that squeezes the list into unreadability is worse than no pane -- so the layout gives up the list
// rather than giving up the preview. Below even that it says so with the number the terminal is short
// by, and with the smaller of the two asks, because that is the one worth growing to.
//
// `p` with the pane already open does not close it: it moves into it, so the diff can be read with the
// keys a diff is read with. Taking a pane off a wide terminal is `q`'s, pressed from inside it.
//
// It decides what is on show and fetches nothing. Update asks the preview what it needs after every
// key, so a handler that fetched as well would ask git twice for the same file -- which is exactly
// what this function did until the overlay's tests noticed two batches arriving for one keystroke.
// togglePreview is `p`: it moves the keys into the diff, and it does nothing else. It used to give
// them back as well, which made one key mean two opposite things depending on where the keys already
// were -- so the key had to be remembered rather than read off the screen. Esc hands the keys back,
// and `p` pressed where the diff already has them is the no-op its name promises. In a terminal with no
// room for a column it gives the diff the whole screen, which is the same request answered as well as
// the window allows, and from that screen it is inert.
func (m reviewModel) togglePreview() (tea.Model, tea.Cmd) {
	if m.mode == modePreview || m.previewHasFocus() {
		return m, nil
	}
	if m.previewOn && m.paneWidth() > 0 {
		m.focusOn(focusPreview)
		return m, nil
	}
	if m.previewShortfall() == "" {
		m.previewOn = true
		m.setStatus("", false)
		return m, nil
	}
	if reason := m.overlayShortfall(); reason != "" {
		m.setRefusal(reason)
		return m, nil
	}
	// What is on show is decided before the keys move: while the terminal has had no room for the pane,
	// nothing has been deriving, and a place kept from the last shape the terminal had is not what the
	// reviewer is pointing at with `p`. When the row is the one already remembered -- `p`, `Esc`, `p` --
	// nothing here changes, and the place in the file is kept.
	if kind, path, ok := m.previewRowTarget(); ok && (kind != m.previewKind || path != m.previewPath) {
		m.previewKind, m.previewPath, m.previewOffset, m.previewMatch = kind, path, 0, -1
	}
	// The overlay is the same region as the pane, so it takes the keys the same way: through focusOn,
	// which is what remembers which half of the list column is handing them over.
	m.focusOn(focusPreview)
	return m, nil
}

// closePreview puts the list back and gives the keys to the region that had them before `p` took them
// away. The scroll position is kept, so `p` twice returns to the same place in the same file.
func (m reviewModel) closePreview() (tea.Model, tea.Cmd) {
	m.mode = modeFiles
	m.focus = m.prevFocus
	m.previewG = false
	m.setStatus("", false)
	return m, nil
}

// leavePreview gives the keys back to the region they came from -- `esc`, and the only key that does:
// `p` moves into the diff and no longer moves out of it. The pane stays where it is, including where it
// is in the file, so coming back returns to the same lines rather than to its top.
func (m reviewModel) leavePreview() (tea.Model, tea.Cmd) {
	if m.mode == modePreview {
		return m.closePreview()
	}
	m.focus = m.prevFocus
	m.previewG = false
	m.clamp()
	m.setStatus("", false)
	return m, nil
}

// takeScreen gives the diff the whole terminal, and records where the keys stood when it did. Every
// route into this one shape comes through here, so `z` pressed again always has a region to give back
// rather than a guess about how the screen was reached.
func (m *reviewModel) takeScreen(from focusTarget) {
	m.zoomReturn = from
	m.previewOn = true
	m.mode = modePreview
	m.focus = focusPreview
	m.previewG = false
	m.setStatus("", false)
}

// openFullScreen is `z` taken from the list column rather than from inside the diff: the row the keys
// are standing on, read over the whole terminal. It decides what is on show the way `p` does — from the
// row, before the keys move — and then takes the screen the way `z` does from the pane, so the three
// entries into this one shape are the same shape and not three screens that happen to look alike.
//
// The keys go with the screen. A whole-screen diff that left them in a list it no longer draws is the
// one thing this screen cannot offer: the list's keys would be read by nothing, and the reviewer would
// be typing at a picture.
func (m reviewModel) openFullScreen() (tea.Model, tea.Cmd) {
	if reason := m.overlayShortfall(); reason != "" {
		m.setRefusal(reason)
		return m, nil
	}
	if kind, path, ok := m.previewRowTarget(); ok && (kind != m.previewKind || path != m.previewPath) {
		m.previewKind, m.previewPath, m.previewOffset, m.previewMatch = kind, path, 0, -1
	}
	if m.previewPath == "" {
		m.setRefusal("nothing to open: the preview has no file on show")
		return m, nil
	}
	from := m.focus
	m.previewOn = true
	// Through focusOn, which is what remembers which half of the list column is handing the keys over,
	// so `esc` from the screen this opens returns to the row the reviewer was on.
	m.focusOn(focusPreview)
	if m.mode == modePreview {
		// The narrow terminal's route: with no column to focus, focusOn drew the overlay directly and
		// recorded `from` as the region to give back.
		return m, nil
	}
	m.takeScreen(from)
	return m, nil
}

// toggleFullScreen is `z`: the diff takes the whole screen, or gives it back. It changes the shape of the
// screen and nothing else -- the keys stay with the diff, so does the file it is showing, and so does the
// place in it, which is what makes the pair of presses one reading gesture rather than two different ones.
//
// The two forms are the same two the terminal picks by itself: a wide terminal puts the diff in a column
// beside the list, a narrow one has no column to spare and draws it over the whole screen. `z` is how a
// reviewer asks for the other one without moving the keys, and it is refused rather than guessed at where
// the shape asked for cannot be drawn -- which leaves one case, giving a column back to a terminal that
// stopped having room for it while the diff was zoomed.
func (m reviewModel) toggleFullScreen() (tea.Model, tea.Cmd) {
	if m.mode == modePreview {
		return m.unzoom()
	}
	if !m.previewHasFocus() {
		return m, nil
	}
	// The overlay is the same region as the pane and takes the keys the same way.
	if reason := m.overlayShortfall(); reason != "" {
		m.setRefusal(reason)
		return m, nil
	}
	m.takeScreen(focusPreview)
	return m, nil
}

// unzoom is `z` over the whole screen: the screen and the keys both go back to the region `z` was
// pressed in. From the pane that is the pane, so the reviewer keeps reading with the list beside them;
// from the list column it is the list column, so the diff does not keep a keyboard whose region the
// screen has stopped drawing. `esc` is unaffected and still returns to whichever half of the list
// column handed the keys over, whatever shape the diff is in.
func (m reviewModel) unzoom() (tea.Model, tea.Cmd) {
	if m.zoomReturn != focusPreview {
		return m.closePreview()
	}
	if reason := m.previewShortfall(); reason != "" {
		m.setRefusal(reason)
		return m, nil
	}
	m.mode = modeFiles
	m.previewOn = true
	m.focus = focusPreview
	m.previewG = false
	m.setStatus("", false)
	return m, nil
}

// handleDiffKey is everything the diff reads, in either layout: it scrolls with the vim primitives,
// `esc` gives the keys back, `tab` and `f` move them to the list column, and `q` quits the session from
// the pane as it does everywhere else. Over the overlay the screen has nothing else on it, so there `esc`,
// `enter` and `q` all close it and hand the list back.
//
// Nothing else reaches through. That is what a mode buys over a flag, and a focus buys over a pane that
// is merely drawn: a reviewer must not be able to mark a file they are not looking at, submit a review
// whose outcome they cannot see, or open an editor over a diff they are reading, and the way to
// guarantee that is keys that do not happen rather than a list of exemptions. The overlay could make
// that claim by hiding the list; the pane makes it by holding the keyboard, which is the half the
// reviewer can still see -- so the shortcut bar names every key this reads and none of the ones it does
// not, and the list's own cursor goes faint until the keys come back.
//
// The exceptions are the keys that move the keys: tab, shift-tab and f. They change no part of the
// review, and a reviewer who arrived at the diff with tab has to be able to go on with it -- a ring you
// can only leave by backing out of is not a ring. Over the overlay they do the same and take the screen
// down with them, because the regions they name are drawn the moment the diff stops covering them. `p`
// is not among them: it names the diff, so pressed here it is still the no-op its name promises, while
// `tab` names the *other stop*, and taking the screen down follows from going somewhere else.
//
// The two jumps into the box -- `a` and `t` -- are not exceptions. They move the cursor as well as the
// keys, and a cursor that moves under a diff the reviewer is reading is the invisible action the focus is
// there to prevent; they belong to the list column, and the bar here does not offer them.
//
// Keys do mean different things here than in the list -- q closes the overlay rather than quitting over
// it, enter opens the file being read rather than the one under the cursor, ctrl-d scrolls rather than
// quits -- and that is the point: the shortcut bar names each of these keys while this screen is up, so
// no meaning travels with a keystroke alone.
// jumpFromPreview is `a` or `t` taken while the diff holds the keys: the keys go to the row the key names,
// which leaves the diff -- the same way `tab` and `f` leave it -- and the pane follows them on to ABOUT.md or
// the threads. A jump that landed on a row the reviewer cannot see would be the invisible action the focus
// exists to prevent; taking the keys is what makes it visible, and reading the document is usually why the
// reviewer left the diff in the first place.
func (m reviewModel) jumpFromPreview(kind rowKind, absent string) (tea.Model, tea.Cmd) {
	if !m.gotoRow(kind) {
		m.setRefusal(absent)
		return m, nil
	}
	m.previewG = false
	return m, nil
}

// openPreview opens what the pane is showing with the key the list would use on it: a diff, or a file shown
// as its own text, opens in the difftool, and a document does whatever its own row does -- the editor, or the
// difftool when the span changed it, and a refusal over history where only the diff is legitimate. Those
// rules belong to the row, so the row is looked up rather than the rules being repeated here.
func (m reviewModel) openPreview() (tea.Model, tea.Cmd) {
	if m.previewPath == "" {
		m.setRefusal("nothing to open: the preview has no file on show")
		return m, nil
	}
	if m.previewKind == previewDiff || m.previewKind == previewContent {
		// The same decision the row makes, so enter in the pane and enter on the list are one rule and
		// not two that can drift. A file shown as its own text keeps its change: the pane reads an added
		// file as text, and enter still opens the file rather than the patch it has no other side of.
		return m.openFile(m.previewPath, m.previewFileChange())
	}
	if r, ok := m.previewRow(); ok {
		return m.openArtifact(r)
	}
	return m, nil
}

// previewFileChange is what the span did to the file the pane is showing, or a modification when the
// pane is showing something that is not a file in the span. It is what lets the pane's enter ask the
// row's question rather than guess at it.
func (m reviewModel) previewFileChange() Change {
	for _, f := range m.sess.Files() {
		if f.Path == m.previewPath {
			return f.Change
		}
	}
	return ChangeChanged
}

// previewRow is the row the pane is a window onto, or false when it is no longer in the list: the rows are
// rebuilt when a thread is created or the span changes, and the pane keeps what it was showing through that.
func (m reviewModel) previewRow() (row, bool) {
	for _, r := range m.rows {
		switch r.kind {
		case rowAbout, rowThread:
			if m.previewKind != previewThreads && r.path == m.previewPath {
				return r, true
			}
		case rowThreadsHead:
			if m.previewKind == previewThreads {
				return r, true
			}
		}
	}
	return row{}, false
}

func (m reviewModel) handleDiffKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Type == tea.KeyCtrlC {
		m.quitting = true
		return m, tea.Quit
	}
	// The search field is read before anything else, because while it is open the pane's keys are not the
	// pane's keys: `d` is a d, `q` is a q, and the diff is not going anywhere until `enter` or `esc` says
	// otherwise.
	if m.searching {
		return m.handleSearchKey(key)
	}
	// The two jumps into the box work from the diff as they do from either half of the list column, keys
	// and all. They used to be keys that did not occur here, on the grounds that a cursor moving under a
	// diff you are reading is an invisible action -- but the answer to that is not to refuse the jump, it
	// is to go where the jump points, which is what taking the keys along does. The overlay is the
	// exception, and the rule is the box: these two work wherever it is drawn, and the overlay is the one
	// screen where it is not, so a cursor moved there would be one you could not see move.
	if m.mode != modePreview {
		switch {
		case key.Type == tea.KeyRunes && firstRune(key) == 'a':
			return m.jumpFromPreview(rowAbout, "no ABOUT.md row in this changeset's box")
		case key.Type == tea.KeyRunes && firstRune(key) == 't':
			return m.jumpFromPreview(rowThreadsHead, "no thread heading in this changeset's box")
		}
	}
	// `g` waits for its partner, as it does in the list. The guard on previewG is what makes the
	// pair possible at all: without it the second `g` would be read as another prefix and the jump
	// would never happen.
	if key.Type == tea.KeyRunes && firstRune(key) == 'g' && !m.previewG {
		m.previewG = true
		return m, nil
	}
	pressedG := m.previewG
	m.previewG = false
	overlay := m.mode == modePreview

	switch {
	// `esc` gives the keys back. `p` does not: it is the key that moves *into* the diff, and a key
	// that meant "the diff" in one region and "not the diff" in this one had to be remembered rather
	// than read. Pressed here it is the no-op its name promises.
	case key.Type == tea.KeyEsc:
		return m.leavePreview()
	// The ring, from inside the diff. Over the pane the other stop is already drawn; over the overlay it
	// is not, so these take the screen down on the way there. On a narrow terminal the overlay is the only
	// form the diff has, and without this the stop it is on the ring would be a hole.
	case key.Type == tea.KeyTab:
		m.cycleFocus(1)
		return m, nil
	case key.Type == tea.KeyShiftTab:
		m.cycleFocus(-1)
		return m, nil
	case key.Type == tea.KeyRunes && firstRune(key) == 'f':
		m.focusOn(focusFiles)
		return m, nil
	// `z` changes the shape of the screen the keys are on and nothing else: the diff keeps them, the
	// place in the file and the file itself. It is also a key of the list column — see openFullScreen —
	// and the two readings end on this same screen, which is why `p` and `z` differ only in the size
	// they ask the diff for.
	case key.Type == tea.KeyRunes && firstRune(key) == 'z':
		return m.toggleFullScreen()
	case key.Type == tea.KeyRunes && firstRune(key) == 'q':
		// Over the overlay `q` gives the list back, the way `esc` and `enter` do. The key is read
		// differently in the two layouts because the two layouts show different things: the overlay is
		// the only thing on the screen, so a `q` that quits ends the session for a reviewer who pressed
		// it to get the list back -- on the narrow terminal where the overlay is all the diff can be.
		// In the pane the list is still drawn, `q` means leave as it means it everywhere else, and the
		// marks are on disk either way.
		if overlay {
			return m.closePreview()
		}
		m.quitting = true
		return m, tea.Quit
	case key.Type == tea.KeyEnter:
		// Over the overlay enter closes: the screen is the diff already, and the difftool was what `p`
		// was asked for. In the pane it is the key the pane's own note points at, and it opens what is
		// being read -- the thing the pane is named after, not whichever row the list's cursor sits on.
		if overlay {
			return m.closePreview()
		}
		return m.openPreview()
	case key.Type == tea.KeyDown, key.Type == tea.KeyRunes && firstRune(key) == 'j':
		return m.scrollPreview(1, 1)
	case key.Type == tea.KeyUp, key.Type == tea.KeyRunes && firstRune(key) == 'k':
		return m.scrollPreview(-1, 1)
	// Half a page, three ways: `less`'s `d` and `u`, and the ctrl pairs the list column already uses for
	// the same distance. The half is what makes it a reading key rather than a paging key -- consecutive
	// presses keep a line of context, so the line you were reading is still on screen.
	case key.Type == tea.KeyCtrlD, key.Type == tea.KeyRunes && firstRune(key) == 'd':
		return m.scrollPreview(1, m.previewBodyRows()/2)
	case key.Type == tea.KeyCtrlU, key.Type == tea.KeyRunes && firstRune(key) == 'u':
		return m.scrollPreview(-1, m.previewBodyRows()/2)
	// `/` searches the file on screen, and the field opens with the last term already in it: `less` and
	// `vim` both keep it, and for the same reason -- the second search is nearly always the first one
	// again, which makes `enter` on an untouched field mean "the next one".
	case key.Type == tea.KeyRunes && firstRune(key) == '/':
		m.searching, m.searchInput = true, m.previewSearch
		return m, nil
	case key.Type == tea.KeyRunes && firstRune(key) == 'n':
		return m.searchStep(1)
	case key.Type == tea.KeyRunes && firstRune(key) == 'N':
		return m.searchStep(-1)
	case key.Type == tea.KeyCtrlF:
		return m.scrollPreview(1, m.previewBodyRows())
	case key.Type == tea.KeyCtrlB:
		return m.scrollPreview(-1, m.previewBodyRows())
	case pressedG && key.Type == tea.KeyRunes && firstRune(key) == 'g':
		m.previewOffset = 0
		return m, nil
	case key.Type == tea.KeyRunes && firstRune(key) == 'G':
		total, _ := m.previewRowsTouched()
		return m.scrollPreview(1, total)
	}
	return m, nil
}

// previewRowsTouched reports how many rendered rows the file on screen has, and how many of them fit.
// Paging, the top and the bottom all count rows rather than source lines, because a line wider than
// the column is drawn as several rows: paging by lines would page an unpredictable distance.
func (m reviewModel) previewRowsTouched() (total, body int) {
	return len(m.previewContent(unmarked)), m.previewBodyRows()
}

// scrollPreview moves the diff by step rows in direction dir, clamped at both ends. A reviewer at
// either end stays there rather than watching the frame stop moving and wondering whether the key was
// dropped.
func (m reviewModel) scrollPreview(dir, step int) (tea.Model, tea.Cmd) {
	total, body := m.previewRowsTouched()
	if total == 0 {
		return m, nil
	}
	if step < 1 {
		step = 1
	}
	offset := m.previewOffset + dir*step
	if max := total - body; offset > max {
		offset = max
	}
	if offset < 0 {
		offset = 0
	}
	m.previewOffset = offset
	return m, nil
}

// pagePreview scrolls the pane by half a page, which keeps a line or two of context on screen at the
// break. It is the pane's own key now: the list pages its region with the same four keys, and which
// region answers is what m.focus says, so there is no longer a pair of keys that means the diff from
// anywhere on the screen.
func (m reviewModel) pagePreview(dir int) (tea.Model, tea.Cmd) {
	if !m.previewShowing() || m.previewPath == "" {
		return m, nil
	}
	return m.scrollPreview(dir, m.previewBodyRows()/2)
}

// previewContent renders what the pane is showing as the rows it draws, with the marks it should carry.
// It sits apart from the pane's chrome so the search, the paging and the note all count the same rows the
// reviewer is looking at. Nil for a file with nothing cached yet.
func (m reviewModel) previewContent(mk marks) []previewRow {
	if m.previewPath == "" {
		return nil
	}
	if m.previewKind == previewDiff {
		patch, ok := m.patches[m.previewPath]
		if !ok {
			return nil
		}
		work, _ := m.patch(patchWorking, m.previewPath)
		return previewRows(patch, work, m.previewWidth(), mk, m.paneView(m.previewPath))
	}
	cache := m.docs
	if m.previewKind == previewContent {
		// A file the span created or moved whole, shown as its own text with the file's own line numbers.
		// The rows are the rows a document gets: what the pane is on show about does not change how it
		// draws text.
		cache = m.contents
	}
	doc, ok := cache[m.previewPath]
	if !ok {
		return nil
	}
	if m.previewKind == previewContent {
		// The file's own rows, with the reviewer's uncommitted typing drawn into them where it lands.
		// The search, the paging, the note and the whole-screen overlay all come through here, so they
		// count those rows too rather than the file alone.
		if work, known := m.patch(patchWorking, m.previewPath); known && len(work.Lines) > 0 {
			// Every line of a file the span created is one the span added, and none of a file it moved
			// unchanged is: that is all the pane needs to tell a deletion of the reviewed work from a deletion
			// of anything else, and it needs no git call to know it.
			return editedDocRows(doc, work, m.previewWidth(), mk, youOf{colour: true, span: m.paneView(m.previewPath).span})
		}
	}
	return docRows(doc, m.previewWidth(), mk)
}

// previewNotice is what the pane says instead of text: why it has none yet, or why it has none at all. Empty
// means there is something to draw.
func (m reviewModel) previewNotice() string {
	switch m.previewKind {
	case previewDocument, previewThreads:
		doc, cached := m.docs[m.previewPath]
		switch {
		case !cached:
			return "(reading…)"
		case doc.Err != "":
			return doc.Err
		case len(doc.Sections) == 0:
			if m.previewKind == previewThreads {
				return "no threads yet"
			}
			return "nothing to read"
		}
	case previewContent:
		doc, cached := m.contents[m.previewPath]
		switch {
		case !cached:
			return "(reading…)"
		case doc.Err != "":
			return doc.Err
		case docLines(doc) == 0:
			// A file the span created can be empty, and a rename can move an empty file. There is no patch
			// behind this view that would have said more.
			return "empty file"
		}
	default:
		patch, cached := m.patches[m.previewPath]
		switch {
		case !cached:
			return "(reading the diff…)"
		case patch.Err != "":
			return patch.Err
		case len(patch.Lines) == 0:
			work, known := m.patch(patchWorking, m.previewPath)
			if m.sess.Span().Live() && !known {
				// Not "no changes" yet: the answer about the reviewer's own edits is still on its way,
				// and saying it now would be wrong for one git call's duration.
				return "(reading your edits…)"
			}
			if len(work.Lines) == 0 {
				return "no changes in this span"
			}
		}
	}
	return ""
}

// previewTooLong is what the pane says when what it is showing does not fit in it, and "" when it does. The
// two answers differ in more than the word: a diff too large is one to open in the difftool, and a document
// too long is one to open in the editor.
func (m reviewModel) previewTooLong() string {
	switch m.previewKind {
	case previewDocument, previewThreads:
		if doc, ok := m.docs[m.previewPath]; ok && doc.Capped {
			return "too long to read here"
		}
	case previewContent:
		if doc, ok := m.contents[m.previewPath]; ok && doc.Capped {
			return "too long to read here"
		}
	default:
		if patch, ok := m.patches[m.previewPath]; ok && patch.Capped {
			return "diff too large to read here"
		}
	}
	return ""
}

// previewTitle is the pane's own line: what is on show, and how much of it. The count sits beside the name
// in every case, in the place a diff's `+N −M` sits, because it is the same kind of answer about the thing
// on screen. The counts are git's for a diff; a document has no additions and deletions to report, only
// lines, so the header counts what the thing on screen actually has rather than leaving the space for a
// count that would mean nothing.
func (m reviewModel) previewTitle(width int) string {
	header := m.previewPath
	if m.mode == modePreview {
		// The overlay hides the list, and the list is where the span is named. Reading a historical
		// diff with nothing on screen saying it is historical is how a reviewer reaches for a mark that
		// cannot be set, so the span travels with the file here -- and it leads the line, so that when
		// the line has to be clipped it loses the counts at the end rather than the answer at the front.
		header = m.spanName(m.sess.Header().SpanLabel) + "  \u00b7  " + m.previewPath
	}
	switch m.previewKind {
	case previewDocument, previewThreads:
		if doc, cached := m.docs[m.previewPath]; cached {
			if m.previewKind == previewThreads {
				header = fmt.Sprintf("%s  %d threads", header, len(doc.Sections))
			} else if lines := docLines(doc); lines > 0 {
				header = fmt.Sprintf("%s  %d lines", header, lines)
			}
			if m.sess.Span().Historical() {
				// The text is the file on disk, and a historical span does not contain that file -- it
				// contains whatever revision of it its head happens to have. Saying which copy is being
				// read is what keeps the two apart in a reviewer's head, and it is why the editor is
				// refused over history while this is not: this cannot change anything.
				header += "  \u00b7  working copy"
			}
		}
	case previewContent:
		if doc, cached := m.contents[m.previewPath]; cached {
			if lines := docLines(doc); lines > 0 {
				header = fmt.Sprintf("%s  %d lines", header, lines)
			}
		}
		if from := m.movedFrom(m.previewPath); from != "" {
			// The tree has room for one character about a move; here is the rest of the answer.
			header = fmt.Sprintf("%s  \u00b7  from %s", header, from)
		}
		header += m.youEditedNote(m.previewPath)
	default:
		if patch, cached := m.patches[m.previewPath]; cached && patch.Added >= 0 {
			header = fmt.Sprintf("%s  +%d \u2212%d", header, patch.Added, patch.Deleted)
		}
		header += m.youEditedNote(m.previewPath)
	}
	title := styleDim.Render
	if m.previewHasFocus() {
		title = styleActive.Render
	}
	return title(clip(header, width))
}

// youEditedNote is the header's answer about the working copy: what this tree holds that the revision under
// review does not, with git's counts for it. The pane's body no longer has a caption of its own to say it --
// the `← you` on each of your rows does that, and the header is where the size of the section goes. The counts
// are git's for that section rather than the span's, so the number beside the file cannot be read as a tally
// that includes the working copy's typing. Over a historical span nothing is fetched, and the note is empty.
func (m reviewModel) youEditedNote(path string) string {
	work, known := m.patch(patchWorking, path)
	if !known || len(work.Lines) == 0 {
		return ""
	}
	note := "  \u00b7  you edited it"
	if work.Added >= 0 {
		note += fmt.Sprintf("  +%d \u2212%d", work.Added, work.Deleted)
	}
	return note
}

// movedFrom is the path a renamed file came from, for the row the pane is holding. It is empty for a file
// the span did not move, which is most of them.
func (m reviewModel) movedFrom(path string) string {
	for _, f := range m.sess.Files() {
		if f.Path == path {
			return f.MovedFrom
		}
	}
	return ""
}

// paneView is what the model knows about a path the pane is showing that git's bytes do not say: whether it is
// one file the tree listed, rather than a directory whose patch happens to hold one file, and whether the span
// wrote every line of it. A file the span created has no line in it the span did not add; a file it moved with
// its bytes unchanged has nothing but lines that predate it. The session's own list of files is what put the
// row in the tree, so it is where both answers are.
func (m reviewModel) paneView(path string) paneView {
	for _, f := range m.sess.Files() {
		if f.Path == path {
			created := f.Change == ChangeAdded
			return paneView{file: true, span: func(int) bool { return created }}
		}
	}
	return paneView{}
}

// searchTerm is what the pane is highlighting: what is being typed while the field is open, and the term
// `enter` committed the rest of the time.
func (m reviewModel) searchTerm() string {
	if m.searching {
		return m.searchInput
	}
	return m.previewSearch
}

// currentMatch is the row `n` last landed on, or -1 when there is no match for the term in force. That is
// also the answer while a new term is being typed: nothing is picked out in reverse video until `enter`
// says which match the reviewer means.
func (m reviewModel) currentMatch() int {
	if m.previewMatch < 0 || m.previewMatchTerm == "" || m.previewMatchTerm != m.previewSearch {
		return -1
	}
	return m.previewMatch
}

// clampPreviewOffset is the one rule about where the pane's top row may sit: it cannot be negative, and it
// cannot show more blank rows than there are. The draw, the paging and a jump to a match all ask for it,
// and they have to agree or the pane scrolls to a row it then refuses to show.
func clampPreviewOffset(off, total, body int) int {
	if max := total - body; off > max {
		off = max
	}
	if off < 0 {
		return 0
	}
	return off
}

// scrollToRow moves the pane the least distance that puts the row on screen. Centreing every jump would
// move lines the reviewer was reading off the top to make room for one they have not read yet.
func (m *reviewModel) scrollToRow(row, total int) {
	body := m.previewBodyRows()
	if row < m.previewOffset {
		m.previewOffset = row
	}
	if row >= m.previewOffset+body {
		m.previewOffset = row - body + 1
	}
	m.previewOffset = clampPreviewOffset(m.previewOffset, total, body)
}

// searchStep walks to the next match (dir > 0) or the previous one, wrapping at both ends. It wraps where
// the pane's own paging does not: a term with one match in the file has to be reachable from anywhere, and
// a reviewer at the bottom of a long diff should find the match at the top rather than a status line.
func (m reviewModel) searchStep(dir int) (tea.Model, tea.Cmd) {
	term := m.previewSearch
	rows := m.previewContent(unmarked)
	matches := matchRows(rows, term)
	if len(matches) == 0 {
		if term == "" {
			return m, nil // `n` before anything has been searched for has nothing to say
		}
		m.setStatus(fmt.Sprintf("no match for %q in this file", term), true)
		return m, nil
	}

	// With no match to work from, the window's own top row is the starting point, so `enter` on a typed
	// term lands on the first match at or below what is already on screen rather than jumping to the top of
	// the file.
	at := m.previewOffset - 1
	if cur := m.currentMatch(); cur >= 0 {
		at = cur
	}
	pick := 0
	if dir > 0 {
		for i, row := range matches {
			if row > at {
				pick = i
				break
			}
		}
	} else {
		pick = len(matches) - 1
		for i := len(matches) - 1; i >= 0; i-- {
			if matches[i] < at {
				pick = i
				break
			}
		}
	}

	m.setStatus("", false)
	m.previewMatch, m.previewMatchTerm = matches[pick], term
	m.scrollToRow(matches[pick], len(rows))
	return m, nil
}

// handleSearchKey is the field: it reads the term and the two keys that end it, and nothing else. The
// scrolling keys are not read here for the same reason they are not read in the thread title, and for a
// better one: the highlights move under the reviewer's fingers as they type, so the pane is already saying
// what the term means without moving. `q` types a q rather than quitting -- the field is where the
// reviewer is, not the pane.
func (m reviewModel) handleSearchKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Type == tea.KeyCtrlC:
		m.quitting = true
		return m, tea.Quit

	case key.Type == tea.KeyEnter:
		// The field closes and what was typed becomes the term, and `enter` then means "this one": the walk
		// `n` does. Typing a term and pressing `enter` twice lands on the same two matches as typing it and
		// pressing `n` twice.
		m.previewSearch, m.searching, m.searchInput = m.searchInput, false, ""
		if m.previewSearch == "" {
			m.previewMatch, m.previewMatchTerm = -1, "" // an empty term is a clear, not a search
			return m, nil
		}
		return m.searchStep(1)

	case key.Type == tea.KeyEsc:
		// The field closes and the term already committed stays where it was: a reviewer who mistypes
		// should not lose the match they were reading.
		m.searching, m.searchInput = false, ""
		return m, nil

	case key.Type == tea.KeyCtrlU:
		// The line is killed rather than the pane scrolled, because the pane's keys are paused here and a
		// term typed wrong is the reason to reach for it.
		m.searchInput = ""
		return m, nil

	case key.Type == tea.KeyBackspace, key.Type == tea.KeyCtrlH:
		// Nothing left to delete means the reviewer is done with the field -- which is what `esc` means,
		// and the same two keys do it in a shell's prompt. Stopping at the empty field instead would mean
		// reaching for a different key once the term is gone than while it is being written.
		if m.searchInput == "" {
			m.searching, m.searchInput = false, ""
			return m, nil
		}
		runes := []rune(m.searchInput)
		m.searchInput = string(runes[:len(runes)-1])
		return m, nil

	case key.Type == tea.KeySpace:
		m.searchInput += " "
		return m, nil

	case key.Type == tea.KeyRunes:
		m.searchInput += string(key.Runes)
		return m, nil
	}
	return m, nil
}

// previewLines renders the pane: the file it belongs to, git's own coloured diff, and a note about the part
// that is not on show. Everything between the first line and the note is git's bytes with nothing added --
// PRD §3 rules out a diff renderer, and this is the alternative to building one: a window onto what git
// printed. The exception is the reviewer's own uncommitted rows, which nothing in git's bytes could tell
// anyone about; previewRows says what the pane puts on them.
func (m reviewModel) previewLines() []string {
	width := m.previewWidth()
	if width <= 0 || m.previewPath == "" {
		return nil
	}
	out := []string{m.previewTitle(width)}
	body := m.previewBodyRows()
	if notice := m.previewNotice(); notice != "" {
		return append(out, styleDim.Render(clip(notice, width)))
	}

	// Rows, not source lines: a line wider than the column is drawn as several rows, so the paging, the
	// note and the search all count what is actually on screen.
	// The marks are asked for as the rows are made rather than written over them afterwards: a term the
	// column breaks in half is one term, and it has to be marked on both of the rows it is drawn on.
	rows := m.previewContent(marks{term: m.searchTerm(), current: m.currentMatch()})
	offset := clampPreviewOffset(m.previewOffset, len(rows), body)
	end := offset + body
	if end > len(rows) {
		end = len(rows)
	}
	out = append(out, rowTexts(rows[offset:end])...)

	switch {
	case m.searching:
		// The field goes on the note's row: it is the pane's own bottom line, the line `less` puts its
		// prompt on, and the layout has already counted a row there, so the field can arrive and leave
		// without moving anything above it. A file shorter than the pane has to be padded up to that row
		// first: a prompt that floats under the last line of a short file is a prompt in the middle of the
		// screen, and the eye looks for it at the bottom. The caret is the block the thread prompt uses,
		// for the same reason it does: a field that looks like prose gets typed into by accident.
		for len(out) < body+1 {
			out = append(out, "")
		}
		out = append(out, clip("/ "+m.searchInput+threadPromptCursor, width))
	case end < len(rows) || offset > 0:
		// The keys the note may point at. In the pane ctrl-b/ctrl-f page it and enter opens the
		// difftool; in the overlay the whole screen is already the diff, enter closes it, and a note
		// that promised "enter opens" would promise the opposite of what the key now does.
		tail, keys := "  enter opens", "  ctrl-b/ctrl-f"
		if m.mode == modePreview {
			tail, keys = "", ""
		}
		note := fmt.Sprintf("… %s%s", rowsMore(len(rows)-end), tail)
		if offset > 0 {
			note = fmt.Sprintf("rows %d\u2013%d of %d%s%s", offset+1, end, len(rows), keys, tail)
		}
		if too := m.previewTooLong(); too != "" {
			note = too + tail
		}
		if term := m.searchTerm(); term != "" {
			// How many places the term has: what tells the reviewer whether `n` has anywhere left to go.
			note = fmt.Sprintf("%s  \u00b7  %d for %q", note, len(matchRows(rows, term)), term)
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
//
// The divider is also what says which column has the keys, because it is the one thing drawn on every
// row of both: single and faint over the list, double and bold over a preview the reviewer moved into.
func joinColumns(left string, right []string, listWidth int, previewFocused bool) string {
	rule := styleDim.Render("│")
	if previewFocused {
		rule = styleActive.Render("║")
	}
	lines := strings.Split(strings.TrimSuffix(left, "\n"), "\n")
	var b strings.Builder
	for i, l := range lines {
		preview := ""
		if i < len(right) {
			preview = right[i]
		}
		b.WriteString(padRight(l, listWidth) + " " + rule + " " + preview + "\n")
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
		// The cursor is the position on the screen only in the region that holds the keys; the other
		// region keeps showing where it was, dimmed, the way the diff's cursor does.
		if m.focus != focusFiles {
			return styleIdle.Render(text)
		}
		return styleSelected.Render(text)
	}
	return text
}

// chromeRows counts the lines View writes outside the row list: the title, the base and span
// line, the blank under them, the blank above the counter, the counter, the blank under it,
// and then the rule over the band and the band itself -- bandRows() tall, whatever the band is
// showing, which is why neither a message nor the keys moving between regions can reflow the list.
// Nothing a region draws is counted here: both of them keep what they have to say inside their own
// borders and rules, so no note can arrive late and take a row with it. The changeset
// section is part of the row list, so it is not here — only the counter that separates the two
// blocks is.
func (m reviewModel) chromeRows() int {
	return 7 + m.bandRows()
}

// setStatus reports what the last keystroke did. A failure waits for the reviewer, since the next
// key depends on what it says; anything else is a note and fades.
func (m *reviewModel) setStatus(text string, isErr bool) {
	kind := notifNote
	if isErr {
		kind = notifSticky
	}
	m.notify(text, isErr, kind)
}

// setRefusal is the message for a key this screen cannot honour. Nothing failed, so it is not red;
// the reviewer still has to read it and act on it, so it waits for a keypress rather than fading.
func (m *reviewModel) setRefusal(text string) { m.notify(text, false, notifSticky) }

func (m *reviewModel) notify(text string, isErr bool, kind notifKind) {
	if text == m.status && isErr == m.statusErr && kind == m.statusKind {
		// Saying the same thing again does not restart its clock.
		return
	}
	m.status, m.statusErr, m.statusKind = text, isErr, kind
	m.notifGen++
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
