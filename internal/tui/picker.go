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
		items = append(items, pickerItem{label: "Working Tree", detail: "editable", ckpt: span.WorkingTree()})
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
// "Review -2" keeps meaning the same submission however the other end is set (§5).
func reviewItem(e lifecycle.Event, i, total int) pickerItem {
	index := i - total // -1 for the newest
	label := fmt.Sprintf("Review %d", index)
	if index < -3 {
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

func endpointIndex(items []pickerItem, want span.Checkpoint) int {
	for i, it := range items {
		if it.isDrill {
			continue
		}
		if sameCheckpoint(it.ckpt, want) {
			return i
		}
	}
	return 0
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

	m.setStatus(spanNote(before, m.sess, 0, 0), false)
	return m, nil
}

// spanNote is what a span change says: the span now on screen, where it sits among the
// spans this session has been in when there are enough of them to be worth a position, how
// many reviewed marks stopped applying, and whether the screen went read-only with it. The
// position is left out for two-stop sessions, where it would only be noise.
func spanNote(before int, sess *Session, pos, total int) string {
	sp := sess.Span()
	note := "span " + sp.Label
	if total > 2 {
		note += fmt.Sprintf(" (%d of %d)", pos, total)
	}
	if dropped := before - countMarked(sess); dropped > 0 {
		note += fmt.Sprintf(" \u00b7 %d reviewed mark%s no longer applies", dropped, plural(dropped))
	}
	if sp.Historical() {
		note += " \u00b7 read-only"
	}
	return note
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

func (m reviewModel) handleListKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.pick
	list := p.list
	visible := list.visible()

	switch {
	case key.Type == tea.KeyEsc:
		p.list = nil
		m.pick = p
		return m, nil

	case key.Type == tea.KeyDown:
		list.sel = clampIndex(list.sel+1, 0, max(0, len(visible)-1))
	case key.Type == tea.KeyUp:
		list.sel = clampIndex(list.sel-1, 0, max(0, len(visible)-1))

	case key.Type == tea.KeyBackspace:
		if list.filter == "" {
			p.list = nil
			m.pick = p
			return m, nil
		}
		runes := []rune(list.filter)
		list.filter = string(runes[:max(0, len(runes)-1)])
		list.sel = 0

	case key.Type == tea.KeyEnter, key.Type == tea.KeySpace:
		return m.pickFromList(list, visible)

	case key.Type == tea.KeyRunes:
		// Letters belong to the filter here, not to navigation: a list you search is a
		// list where `j` has to type `j`. Arrows move.
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

	var b strings.Builder
	b.WriteString(clip(render(title), width) + "\n")
	for i, it := range m.endpointsFor(base) {
		cursor, chosen := " ", " "
		if m.pick.col == col && m.pick.cursor[col] == i {
			cursor = ">"
		}
		if !it.isDrill && sameCheckpoint(it.ckpt, pending) {
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

func (m reviewModel) selectedBlock() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(styleDim.Render("Selected:") + "\n")
	b.WriteString(clip("  "+m.pick.base.String()+" \u2192 "+m.pick.head.String(), m.width) + "\n")

	if msg := m.pick.err; msg != "" {
		b.WriteString(clip(styleErr.Render("  "+msg), m.width) + "\n")
		return b.String()
	}
	sp, err := m.resolveSelector(span.Selector{Base: m.pick.base, Head: m.pick.head})
	if err != nil {
		b.WriteString(clip(styleErr.Render("  "+err.Error()), m.width) + "\n")
		return b.String()
	}
	mode := styleSpan.Render("LIVE") + styleDim.Render(" \u00b7 editable")
	if sp.Historical() {
		mode = styleSpan.Render("HISTORICAL") + styleDim.Render(" \u00b7 read-only")
	}
	b.WriteString(clip("  "+mode+styleDim.Render("  "+sp.Label), m.width) + "\n")
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
	b.WriteString(clip(styleSpan.Render(title)+"  "+styleDim.Render("filter: "+list.filter+"\u2588"), m.width) + "\n")
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

	rows := m.height - 5
	if rows < 3 {
		rows = 3
	}
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

// helpSpan is the picker's own shortcut bar. Two modes, because the drill-in takes typed
// text and so cannot keep j/k for navigation.
func helpSpan(drilled bool) string {
	if drilled {
		return "type to filter  \u2191/\u2193 move  enter pick  backspace delete  esc back"
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
