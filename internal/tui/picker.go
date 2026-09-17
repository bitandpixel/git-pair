package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"gitpr/internal/git"
	"gitpr/internal/lifecycle"
	"gitpr/internal/span"
)

// The `V` screen: choose both ends of the span before either takes effect.
//
// Two rules shape all of it. Pending is not applied — `Space` sets one end, `Enter`
// applies the pair — so a reviewer can look at "Review -3 → Review -1" and decide. And
// the columns list review submissions, commits and refs, never `HEAD`: beside "Working
// Tree" it would put two similar-looking current targets on screen when only one of them
// can be edited (§4).
//
// The Commit… and Ref… rows are drills into git itself. They are searchable because a
// list of a reviewer's own history is only useful if they can find their way through it,
// and both take typed text as a checkpoint directly, so history older than the window is
// one keystroke from being reachable.

const (
	// pickerCommitLimit is how far back the commit list reaches. A scrollback rather
	// than a rule: the same list takes a typed id.
	pickerCommitLimit = 200
	// pickerDetailWidth is the right-hand column of a row: a short sha and an age.
	pickerDetailWidth = 16
	// pickerGap separates the two columns, and matches the divider joinColumns draws.
	pickerGap = 3
)

// spanPicker is the picker's whole state. The zero value is only ever held while the
// picker is closed: entering it goes through newSpanPicker.
type spanPicker struct {
	// col is the active column: 0 is BASE, 1 is HEAD.
	col    int
	base   span.Checkpoint
	head   span.Checkpoint
	cursor [2]int
	// list is the Commit…/Ref… drill-in, nil while the columns are showing.
	list *checkpointList
	// err is a refusal the reviewer is being shown: a pair git would not resolve. The
	// picker stays open with it, because they are mid-choice.
	err string
	// nav is the drill-in's own mode: false means what you type goes to the filter, true
	// means the keys navigate the list. Tab toggles it, and each mode's shortcut bar names
	// the keys it reads -- the same bargain the diff overlay makes.
	nav bool
	// gPrefix waits for the second g of `gg`, as the preview overlay's does.
	gPrefix bool
}

type listKind int

const (
	listCommits listKind = iota
	listRefs
)

// checkpointList is one drill-in: what git reported, what has been typed at it, and
// where the cursor is among what survives the filter.
type checkpointList struct {
	kind   listKind
	filter string
	sel    int
	items  []pickerItem
	err    string
}

// pickerItem is one selectable row. Group is set for refs so the renderer can put a
// heading over each family; it is not a row the cursor can rest on.
type pickerItem struct {
	label   string
	detail  string
	group   string
	ckpt    span.Checkpoint
	drill   listKind
	isDrill bool
}

// --- the two columns --------------------------------------------------------

// endpointsFor are the rows one column offers. Both list the submissions by alias and
// both end with the drills; only the base offers the changeset base, and only the head
// offers the working tree.
func (m reviewModel) endpointsFor(base bool) []pickerItem {
	reviews := m.sess.Summary().Reviews
	var items []pickerItem
	if !base {
		// Title-cased like its siblings in this list; the span label renders the same endpoint
		// lower-case inline ("last review..current"). The detail says what the endpoint holds,
		// because "Working Tree" named only the uncommitted half of it.
		items = append(items, pickerItem{label: "Current", detail: "latest + edits", ckpt: span.WorkingTree()})
	}
	for i := len(reviews) - 1; i >= 0; i-- {
		items = append(items, reviewItem(reviews[i], i, len(reviews)))
	}
	if base {
		items = append(items, pickerItem{
			label: "Changeset Base", detail: "base " + m.sess.Header().Base, ckpt: span.ChangesetBase(),
		})
	}
	items = append(items,
		pickerItem{label: "Commit…", detail: "a fixed point", isDrill: true, drill: listCommits},
		pickerItem{label: "Ref…", detail: "branch, tag, ref", isDrill: true, drill: listRefs})
	return items
}

// reviewItem names a submission the way a reviewer does: the last few by their alias from
// the end, older ones by their index from the start. The index stored is the one shown, so
// "Review -2" keeps meaning the same submission however the other end is set (§5). The newest
// is "Last Review" for the same reason the span label calls it "last review" — the reader is
// not being asked to count backwards from a total they cannot see.
func reviewItem(e lifecycle.Event, i, total int) pickerItem {
	index := i - total // -1 for the newest
	label := fmt.Sprintf("Review %d", index)
	switch {
	case index == -1:
		label = "Last Review"
	case index < -3:
		index = i
		label = fmt.Sprintf("Review %d", i)
	}
	return pickerItem{label: label, detail: e.Short + " " + ago(e.When), ckpt: span.Review(index)}
}

// newSpanPicker seeds the pending pair from the span on screen and puts each column's
// cursor on it, so `V` opens on the present rather than at the top of a list.
func (m reviewModel) newSpanPicker() spanPicker {
	sel := m.sess.Selector()
	p := spanPicker{base: sel.Base, head: sel.Head}
	m.recentrePicker(&p)
	return p
}

// recentrePicker moves each column's cursor onto its pending checkpoint.
func (m reviewModel) recentrePicker(p *spanPicker) {
	for col, want := range [2]span.Checkpoint{p.base, p.head} {
		p.cursor[col] = endpointIndex(m.endpointsFor(col == 0), want)
	}
}

// endpointIndex is where a column's cursor sits for a pending checkpoint: on its row, or on
// the drill that holds it. A commit or ref chosen from history has no row of its own, and the
// asterisk for it goes on the drill row -- so the cursor goes there too, rather than leaving
// the mark on a row the picker is not standing on.
func endpointIndex(items []pickerItem, want span.Checkpoint) int {
	for i, it := range items {
		if it.isDrill {
			continue
		}
		if sameCheckpoint(it.ckpt, want) {
			return i
		}
	}
	for i, it := range items {
		if it.isDrill && drillHolds(it.drill, want) {
			return i
		}
	}
	return 0
}

// drillHolds says whether the end of a column came out of that drill. It is what keeps the
// asterisk on screen: without it, a base chosen from the commit list leaves the BASE column
// with no mark at all, and the reviewer cannot see which end the pick went to.
func drillHolds(kind listKind, ckpt span.Checkpoint) bool {
	switch {
	case ckpt.Kind == span.KindCommit && kind == listCommits:
		return true
	case ckpt.Kind == span.KindRef && kind == listRefs:
		return true
	}
	return false
}

// sameCheckpoint compares what a reviewer chose rather than what git resolved: two refs
// named differently are different choices even when they point at the same commit.
func sameCheckpoint(a, b span.Checkpoint) bool {
	if a.Kind != b.Kind || a.Index != b.Index {
		return false
	}
	return span.ShortRef(a.Name) == span.ShortRef(b.Name)
}

// --- keys -------------------------------------------------------------------

func (m reviewModel) handleSpanKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ctrl-C means leave, here as it does in the list and the overlay. It used to be a dead key in
	// this screen: the columns answered Esc and q and the drill answered Esc, so reaching for the
	// universal exit in the screen you wanted out of got no reply at all.
	if key.Type == tea.KeyCtrlC {
		m.quitting = true
		return m, tea.Quit
	}
	if m.pick.list != nil {
		return m.handleListKey(key)
	}

	p := m.pick
	items := m.endpointsFor(p.col == 0)
	p.cursor[p.col] = clampIndex(p.cursor[p.col], 0, len(items)-1)

	switch {
	case key.Type == tea.KeyEsc, key.Type == tea.KeyRunes && firstRune(key) == 'q':
		// Cancel means the span on screen is the span that stays. The pending pair is
		// simply dropped.
		m.mode = modeFiles
		m.pick = spanPicker{}
		m.setStatus("", false)
		return m, nil

	case key.Type == tea.KeyTab, key.Type == tea.KeyShiftTab:
		p.col = 1 - p.col

	case key.Type == tea.KeyDown, key.Type == tea.KeyRunes && firstRune(key) == 'j':
		p.cursor[p.col]++
	case key.Type == tea.KeyUp, key.Type == tea.KeyRunes && firstRune(key) == 'k':
		p.cursor[p.col]--

	case key.Type == tea.KeySpace:
		return m.chooseEndpoint(items[p.cursor[p.col]])

	case key.Type == tea.KeyEnter:
		return m.applySpan(p.base, p.head)

	case key.Type == tea.KeyRunes && firstRune(key) == 'u':
		if len(m.sess.Summary().Reviews) == 0 {
			p.err = "this changeset has no review submissions, so there is nothing unreviewed to span"
			break
		}
		p.base, p.head = span.Review(-1), span.WorkingTree()
		m.recentrePicker(&p)
	case key.Type == tea.KeyRunes && firstRune(key) == 'f':
		p.base, p.head = span.ChangesetBase(), span.WorkingTree()
		m.recentrePicker(&p)
	}

	m.pick = p
	return m, nil
}

// chooseEndpoint sets one pending end, or opens the drill-in the row stands for.
func (m reviewModel) chooseEndpoint(it pickerItem) (tea.Model, tea.Cmd) {
	p := m.pick
	if it.isDrill {
		p.err = ""
		p.list = m.openList(it.drill)
		p.nav, p.gPrefix = false, false
		m.pick = p
		return m, nil
	}
	if p.col == 0 {
		p.base = it.ckpt
	} else {
		p.head = it.ckpt
	}
	p.err = ""
	m.pick = p
	return m, nil
}

// applySpan is Enter. The span changes only if git agrees with it, and the note that
// follows says what the new span costs: marks that no longer apply, and whether the
// screen is about to refuse keys.
func (m reviewModel) applySpan(base, head span.Checkpoint) (tea.Model, tea.Cmd) {
	before, _ := m.sess.Count()
	if err := m.sess.SetSpan(m.ctx, span.Selector{Base: base, Head: head}); err != nil {
		m.pick.err = err.Error()
		return m, nil
	}
	m.mode = modeFiles
	m.pick = spanPicker{}
	m.forgetPatches()
	m.refresh()

	m.setStatus(spanNote(before, m.sess, StepResult{}), false)
	return m, nil
}

// spanNote is what a span change says: the span now on screen, where it sits among the
// spans this session has been in when there are enough of them to be worth a position, how
// many reviewed marks stopped applying, and whether the screen went read-only with it. The
// position is left out for two-stop sessions, where it would only be noise.
func spanNote(before int, sess *Session, res StepResult) string {
	sp := sess.Span()
	note := "span " + sp.Label
	if res.Total > 2 {
		note += fmt.Sprintf(" (%d of %d)", res.Pos, res.Total)
	}
	if dropped := before - countMarked(sess); dropped > 0 {
		note += fmt.Sprintf(" \u00b7 %d reviewed mark%s no longer applies", dropped, plural(dropped))
	}
	if sp.Historical() {
		note += " \u00b7 read-only"
	}
	return note + skippedNote(res.Skipped)
}

// skippedNote says what a step passed over. The spans are named rather than counted, because the
// reviewer's next question is "which one", and each carries git's reason, because "review 0..probe-tag"
// is not yet a diagnosis. Silence would be worse than either: an unexplained short walk reads as a ring
// that lost a span, and the span is still there.
func skippedNote(skipped []SkippedStop) string {
	if len(skipped) == 0 {
		return ""
	}
	what := make([]string, 0, len(skipped))
	for _, s := range skipped {
		what = append(what, fmt.Sprintf("%s: %s", s.Selector.Display(), s.Reason))
	}
	if len(skipped) == 1 {
		return " \u00b7 skipped " + what[0]
	}
	return fmt.Sprintf(" \u00b7 skipped %d stops that no longer resolve: %s", len(skipped), strings.Join(what, ", "))
}

// stepFailureNote is what `v` says when nothing it tried would resolve: which stops had to be passed
// over, then the first reason it met. The skips lead because they are the part the reviewer can act on
// -- a span they expected to reach scrolled past, and this says it was not forgotten.
func stepFailureNote(res StepResult, err error) string {
	if len(res.Skipped) == 0 {
		return err.Error()
	}
	return "nowhere left to step \u2014 " + strings.TrimPrefix(skippedNote(res.Skipped), " \u00b7 ")
}

func countMarked(sess *Session) int {
	marked, _ := sess.Count()
	return marked
}

// --- the drill-ins ----------------------------------------------------------

// openList asks git for what a column can still reach. This is synchronous on purpose:
// it is one `git log` or one `git for-each-ref` behind a keystroke a reviewer chose, and
// the alternative is a spinner in a picker that is meant to be instant.
func (m reviewModel) openList(kind listKind) *checkpointList {
	list := &checkpointList{kind: kind}
	switch kind {
	case listCommits:
		tips, err := m.sess.Repo().RecentCommits(m.ctx, pickerCommitLimit, "HEAD", m.sess.Header().Base)
		if err != nil {
			list.err = err.Error()
			return list
		}
		for _, t := range tips {
			list.items = append(list.items, pickerItem{
				label:  t.Subject,
				detail: t.Short + " " + ago(t.When),
				ckpt:   span.Commit(t.SHA),
			})
		}
	case listRefs:
		tips, err := m.sess.Repo().RefTips(m.ctx, "refs")
		if err != nil {
			list.err = err.Error()
			return list
		}
		list.items = refItems(tips)
	}
	return list
}

// refFamilies is the order the ref list is grouped in, most local first.
var refFamilies = []struct {
	name   string
	prefix string
}{
	{"LOCAL BRANCHES", "refs/heads/"},
	{"REMOTE REFS", "refs/remotes/"},
	{"TAGS", "refs/tags/"},
	{"OTHER REFS", ""},
}

// refItems groups refs under headings and shortens their names for display. The full
// `refs/...` name is what goes into the checkpoint: a branch and a tag called `main` are
// not the same choice, and drift has to be checked against the right one.
func refItems(tips []git.RefTip) []pickerItem {
	var items []pickerItem
	for _, family := range refFamilies {
		var group []git.RefTip
		for _, t := range tips {
			if !refInFamily(t.Name, family.prefix) {
				continue
			}
			group = append(group, t)
		}
		slices.SortFunc(group, func(a, b git.RefTip) int {
			return strings.Compare(span.ShortRef(a.Name), span.ShortRef(b.Name))
		})
		for _, t := range group {
			items = append(items, pickerItem{
				label:  span.ShortRef(t.Name),
				detail: ago(t.When),
				group:  family.name,
				ckpt:   span.Ref(t.Name),
			})
		}
	}
	return items
}

// refInFamily also rejects the refs nobody means to review: git's own bookkeeping, and
// anything that lives under refs/ outside the families listed above.
func refInFamily(name, prefix string) bool {
	for _, skip := range []string{"refs/stash", "refs/bisect/", "refs/replace/", "refs/original/"} {
		if strings.HasPrefix(name, skip) {
			return false
		}
	}
	if prefix == "" {
		for _, family := range refFamilies {
			if family.prefix != "" && strings.HasPrefix(name, family.prefix) {
				return false
			}
		}
		return strings.HasPrefix(name, "refs/")
	}
	return strings.HasPrefix(name, prefix)
}

// handleListKey drives the drill-in, which has two modes because it takes typed text. Typing
// mode is the default, and what you type is the filter -- a space included, since commit subjects
// and ref names both contain them. Tab puts the keys on the list instead: j/k, gg/G, half and full
// page, the navigation the rest of the screen uses. Each mode's shortcut bar names the keys that
// mode reads, so nothing carries over by guesswork -- the bargain the diff overlay makes.
func (m reviewModel) handleListKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.pick
	list := p.list
	visible := list.visible()

	if !p.nav {
		switch {
		case key.Type == tea.KeyEsc:
			return m.closeList(p)

		case key.Type == tea.KeyTab, key.Type == tea.KeyShiftTab:
			p.nav = true

		case key.Type == tea.KeyEnter:
			return m.pickFromList(list, visible)

		// Arrows move in both modes -- they mean the same thing either way, so they cost nothing --
		// while j and k have to be free to type their own letters.
		case key.Type == tea.KeyDown:
			list.sel++
		case key.Type == tea.KeyUp:
			list.sel--

		case key.Type == tea.KeyBackspace:
			// Backspace edits the filter and nothing else. An empty filter makes it inert: this
			// key used to throw the whole drill away, so clearing one mistyped character from an
			// empty filter dumped the reviewer back onto the columns they had just left. `esc`
			// leaves, and it is the only key that does.
			if list.filter != "" {
				runes := []rune(list.filter)
				list.filter = string(runes[:max(0, len(runes)-1)])
				list.sel = 0
			}

		case key.Type == tea.KeySpace:
			// Space is a filter character here, not a command: "fix typo" is a thing to search for.
			list.filter += " "
			list.sel = 0

		case key.Type == tea.KeyRunes:
			// Letters belong to the filter, not to navigation: a list you search is a list where
			// `j` has to type `j`.
			for _, r := range key.Runes {
				if r >= ' ' && r != 127 {
					list.filter += string(r)
				}
			}
			list.sel = 0
		}
		m.pick = p
		return m, nil
	}

	// Navigation mode. `g` waits for its partner, as it does in the list and the overlay.
	if key.Type == tea.KeyRunes && firstRune(key) == 'g' && !p.gPrefix {
		p.gPrefix = true
		m.pick = p
		return m, nil
	}
	pressedG := p.gPrefix
	p.gPrefix = false

	rows := m.listRows()
	step := 0
	switch {
	case key.Type == tea.KeyEsc:
		return m.closeList(p)
	case key.Type == tea.KeyTab, key.Type == tea.KeyShiftTab:
		p.nav = false
	case key.Type == tea.KeyEnter, key.Type == tea.KeySpace:
		return m.pickFromList(list, visible)
	case key.Type == tea.KeyDown, key.Type == tea.KeyRunes && firstRune(key) == 'j':
		step = 1
	case key.Type == tea.KeyUp, key.Type == tea.KeyRunes && firstRune(key) == 'k':
		step = -1
	case key.Type == tea.KeyCtrlD:
		step = rows / 2
	case key.Type == tea.KeyCtrlU:
		step = -rows / 2
	case key.Type == tea.KeyCtrlF:
		step = rows
	case key.Type == tea.KeyCtrlB:
		step = -rows
	case pressedG && key.Type == tea.KeyRunes && firstRune(key) == 'g':
		list.sel = 0
	case key.Type == tea.KeyRunes && firstRune(key) == 'G':
		list.sel = max(0, len(visible)-1)
	case key.Type == tea.KeyBackspace && list.filter != "":
		runes := []rune(list.filter)
		list.filter = string(runes[:max(0, len(runes)-1)])
		list.sel = 0
	}
	list.sel += step
	list.sel = clampIndex(list.sel, 0, max(0, len(visible)-1))
	m.pick = p
	return m, nil
}

// closeList backs out of the drill-in. The columns and the pending pair are as they were -- a
// drill that was opened and left should not have changed anything.
func (m reviewModel) closeList(p spanPicker) (tea.Model, tea.Cmd) {
	p.list, p.nav, p.gPrefix = nil, false, false
	m.pick = p
	return m, nil
}

// listRows is how many rows the drill-in has for rows. The renderer windows to it and the page keys
// move by it, so the two have to agree -- a page that moved further than the window shows would put
// the cursor somewhere the reviewer cannot see.
//
// The budget is the terminal minus everything else that draws: two rows for the heading and the blank
// under it, three for the rows the drill can grow but does not always show (a refusal, and the two
// "more off screen" hints), and the chrome -- the shortcut bar and the status, counted as they will
// actually be drawn, since both wrap. Ignoring the wrapped bar was the bug: at 12 rows the frame came
// out 14 tall, and a frame taller than the terminal repaints by scrolling, which loses the bottom row
// -- the bar that says how to leave.
func (m reviewModel) listRows() int {
	used := 2 + 3 + len(m.helpLines()) + m.footerRows()
	return max(1, m.height-used)
}

// pickFromList takes the highlighted entry, or, when the filter has emptied the list,
// takes what was typed as the checkpoint itself. Both go through a resolve first, so a
// mistyped id is reported while the reviewer is still looking at the list.
func (m reviewModel) pickFromList(list *checkpointList, visible []int) (tea.Model, tea.Cmd) {
	p := m.pick
	if len(visible) > 0 {
		return m.setPending(p, list.items[visible[list.sel]].ckpt)
	}
	if list.filter == "" {
		return m, nil
	}
	if list.kind == listRefs {
		return m.setPending(p, span.Ref(list.filter))
	}
	return m.setPending(p, span.Commit(list.filter))
}

func (m reviewModel) setPending(p spanPicker, ckpt span.Checkpoint) (tea.Model, tea.Cmd) {
	base, head := p.base, ckpt
	if p.col == 0 {
		base, head = ckpt, p.head
	}
	if _, err := m.resolveSelector(span.Selector{Base: base, Head: head}); err != nil {
		p.list.err = err.Error()
		m.pick = p
		return m, nil
	}
	if p.col == 0 {
		p.base = ckpt
	} else {
		p.head = ckpt
	}
	p.list = nil
	p.err = ""
	m.recentrePicker(&p)
	m.pick = p
	return m, nil
}

// visible is the filter's answer: the indices into items that still match, in order. The
// match covers the label, the detail and the full ref name, because a reviewer searching
// for `origin/main` may type either half of it.
func (l *checkpointList) visible() []int {
	needle := strings.ToLower(l.filter)
	var out []int
	for i, it := range l.items {
		if needle == "" ||
			strings.Contains(strings.ToLower(it.label), needle) ||
			strings.Contains(strings.ToLower(it.detail), needle) ||
			strings.Contains(strings.ToLower(it.ckpt.Name), needle) {
			out = append(out, i)
		}
	}
	return out
}

// --- rendering --------------------------------------------------------------

func (m reviewModel) pickerBlock() string {
	if m.pick.list != nil {
		return m.pickerListBlock()
	}

	left, right := m.columnWidths()
	leftText := m.columnText(true, left)
	rightText := m.columnText(false, right)

	var b strings.Builder
	b.WriteString(styleSpan.Render("Span picker") + "\n")
	b.WriteString("\n")
	b.WriteString(joinColumns(leftText, strings.Split(strings.TrimSuffix(rightText, "\n"), "\n"), left))
	b.WriteString(m.selectedBlock())
	return b.String()
}

// columnWidths splits the terminal between the two columns. The gap is part of the left
// column's arithmetic so the divider lands in the same cell on every row.
func (m reviewModel) columnWidths() (int, int) {
	room := m.width - pickerGap
	if room < 20 {
		room = 20
	}
	left := room / 2
	return left, room - left
}

func (m reviewModel) columnText(base bool, width int) string {
	col := 1
	if base {
		col = 0
	}
	title, pending := "HEAD", m.pick.head
	if base {
		title, pending = "BASE", m.pick.base
	}
	render := styleDim.Render
	if m.pick.col == col {
		render = styleSpan.Render
	}

	items := m.endpointsFor(base)
	// A short terminal windows the candidates rather than overflowing: the frame has to fit, and
	// the rows that can go are candidates, not the bar that says how to leave. Each column follows
	// its own cursor, and both are drawn to the same number of rows so the divider lands on every
	// row of the frame.
	rows := m.columnRows()
	start := windowStart(m.pick.cursor[col], len(items), rows)

	var b strings.Builder
	b.WriteString(clip(render(title), width) + "\n")
	for drawn := 0; drawn < rows; drawn++ {
		i := start + drawn
		if i >= len(items) {
			b.WriteString(clip("", width) + "\n")
			continue
		}
		it := items[i]
		cursor, chosen := " ", " "
		if m.pick.col == col && m.pick.cursor[col] == i {
			cursor = ">"
		}
		if !it.isDrill && sameCheckpoint(it.ckpt, pending) {
			chosen = "*"
		}
		if it.isDrill && drillHolds(it.drill, pending) {
			// No row of its own, so the drill it came from wears the mark.
			chosen = "*"
		}
		label := it.label
		if it.isDrill {
			label = styleDim.Render(label)
		}
		b.WriteString(clip(m.rowWithDetail(cursor+chosen+" "+label, it.detail, width), width) + "\n")
	}
	return b.String()
}

// columnRows is what the candidate lists have to draw in. The heading is three rows -- "Span
// picker", the blank under it, and the BASE/HEAD titles -- and the Selected block is three more
// (its label, the pair, and what the pair means). Same arithmetic and same reason as listRows.
// One row is the floor: below that the terminal is too small to choose in at all, and a frame
// that overflows loses the shortcut bar.
func (m reviewModel) columnRows() int {
	used := 3 + 3 + len(m.helpLines()) + m.footerRows()
	return max(1, m.height-used)
}

// windowStart is the first row of a list that has more entries than fit, keeping the cursor
// roughly centred so the rows above and below it are both visible where there is room.
func windowStart(cursor, total, rows int) int {
	if total <= rows {
		return 0
	}
	return clampIndex(cursor-rows/2, 0, total-rows)
}

// rowWithDetail puts the detail against the right edge of the column. A column too narrow
// for both loses the detail rather than the label: which checkpoint this is matters more
// than when it was made.
func (m reviewModel) rowWithDetail(label, detail string, width int) string {
	if detail == "" {
		return label
	}
	room := lipgloss.Width(label)
	if room+2+len(detail) > width {
		return label
	}
	return padRight(label, width-len(detail)) + detail
}

// selectedBlock is the picker's preview of the pending pair. It names the pair with the one span
// string the rest of the app uses -- the header, the status line after `Enter`, `gitpr diff`'s stderr,
// `status --json` -- because two vocabularies for one thing read as two things: "changeset base →
// working tree" and "main...current" side by side invite the reviewer to wonder whether they are
// looking at two spans. The endpoint spellings survive for the case where they are the most specific
// thing available, which is when git cannot resolve the pair at all and there is no span to name.
// The choices themselves are not lost: the columns mark them, and a drill names the end it is choosing
// for.
func (m reviewModel) selectedBlock() string {
	var b strings.Builder
	b.WriteString(styleDim.Render("Selected:") + "\n")
	endpoint := "  " + m.pick.base.String() + " \u2192 " + m.pick.head.String()

	if msg := m.pick.err; msg != "" {
		b.WriteString(clip(endpoint, m.width) + "\n")
		b.WriteString(clip(styleErr.Render("  "+msg), m.width) + "\n")
		return b.String()
	}
	sp, err := m.resolveSelector(span.Selector{Base: m.pick.base, Head: m.pick.head})
	if err != nil {
		b.WriteString(clip(endpoint, m.width) + "\n")
		b.WriteString(clip(styleErr.Render("  "+err.Error()), m.width) + "\n")
		return b.String()
	}
	// The span leads and the mode follows. Everywhere else the span is what a line starts with, and
	// if the row is clipped it is the mode that should be lost, not the answer to "which span".
	mode := styleSpan.Render("LIVE") + styleDim.Render(" \u00b7 editable")
	if sp.Historical() {
		mode = styleSpan.Render("HISTORICAL") + styleDim.Render(" \u00b7 read-only")
	}
	b.WriteString(clip("  "+styleSpan.Render(sp.Label)+"  "+mode, m.width) + "\n")
	return b.String()
}

// pickerListBlock is one drill-in: the filter, then the rows that survive it, grouped, in
// a window that follows the cursor.
func (m reviewModel) pickerListBlock() string {
	list := m.pick.list
	title := "Pick Commit"
	kind := "commit"
	if list.kind == listRefs {
		title, kind = "Pick Ref", "ref"
	}

	var b strings.Builder
	// The end being chosen is named here because the columns -- the only other place that says it
	// -- are not on screen while the drill is.
	named := "HEAD"
	if m.pick.col == 0 {
		named = "BASE"
	}
	caret := ""
	if !m.pick.nav {
		// The block is the caret: it sits where your typing goes, so navigation mode drops it.
		caret = "\u2588"
	}
	b.WriteString(clip(styleSpan.Render(title)+"  "+styleDim.Render("for "+named)+
		"  "+styleDim.Render("filter: "+list.filter+caret), m.width) + "\n")
	b.WriteString("\n")
	if list.err != "" {
		b.WriteString(clip(styleErr.Render(list.err), m.width) + "\n")
	}

	visible := list.visible()
	lines, lineOf := list.lines(visible)
	if len(lines) == 0 {
		note := "no matching " + kind
		if list.filter != "" {
			note += "; enter uses \"" + list.filter + "\" as the " + kind
		}
		b.WriteString(clip(styleDim.Render(note), m.width) + "\n")
		return b.String()
	}

	rows := m.listRows()
	target := 0
	for i, idx := range lineOf {
		if idx == list.sel {
			target = i
		}
	}
	start := 0
	if target >= start+rows {
		start = target - rows + 1
	}
	if start > 0 {
		b.WriteString(clip(styleDim.Render(fmt.Sprintf("  (above: %d)", start)), m.width) + "\n")
	}
	end := min(start+rows, len(lines))
	for _, line := range lines[start:end] {
		b.WriteString(clip(line, m.width) + "\n")
	}
	if end < len(lines) {
		b.WriteString(clip(styleDim.Render(fmt.Sprintf("  (below: %d)", len(lines)-end)), m.width) + "\n")
	}
	return b.String()
}

// lines renders the filtered rows, returning them beside the index into `visible` each
// one stands for, so the caller can find the cursor's line without redrawing to look.
// Group headings are not selectable and so map to -1.
func (l *checkpointList) lines(visible []int) ([]string, []int) {
	width := 80 // clipped by the caller to the terminal
	var lines []string
	var lineOf []int
	lastGroup := ""
	for vi, idx := range visible {
		it := l.items[idx]
		if it.group != "" && it.group != lastGroup {
			lastGroup = it.group
			lines = append(lines, styleDim.Render(it.group))
			lineOf = append(lineOf, -1)
		}
		cursor := " "
		if vi == l.sel {
			cursor = ">"
		}
		lines = append(lines, cursor+" "+rowText2(it, width))
		lineOf = append(lineOf, vi)
	}
	return lines, lineOf
}

func rowText2(it pickerItem, width int) string {
	if it.detail == "" {
		return it.label
	}
	if lipgloss.Width(it.label)+2+len(it.detail) > width {
		return it.label
	}
	return padRight(it.label, width-len(it.detail)) + it.detail
}

// resolveSelector answers what a pending pair would mean, for the picker's preview and
// for refusing a pair git cannot resolve.
func (m reviewModel) resolveSelector(sel span.Selector) (span.Span, error) {
	return span.Resolve(m.ctx, m.sess.Repo(), m.sess.Header().Base, m.sess.Summary(), sel)
}

// helpSpan is the picker's own shortcut bar, and it says what the keys mean where they are being
// pressed. The drill-in has two modes because it takes typed text, and a mode whose bar does not
// name its keys is a mode with undocumented keys.
func helpSpan(drilled, nav bool) string {
	if drilled && nav {
		return "j k line  gg top  G bottom  ctrl-d/u half  ctrl-f/b page  space/enter pick  tab filter  esc back"
	}
	if drilled {
		return "type to filter  \u2191/\u2193 move  backspace delete  enter pick  tab navigate  esc back"
	}
	return "tab column  j/k move  space choose  enter apply  u unreviewed  f full  esc cancel"
}

// ago is how long ago a commit was, short enough for a detail column.
func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
	return t.Format("2006-01-02")
}

func clampIndex(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
