package tui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	runewidth "github.com/mattn/go-runewidth"
)

// The preview pane is git's output with two things added: the line number git itself put in the
// hunk header, and a break where a line is wider than the column. It does not fold hunks, group
// them, filter them, decide what is worth reading, or choose its own colours, and PRD §3 is the
// reason for those restrictions -- a pane that interprets diffs is a diff renderer, and the way to
// read a diff properly remains opening it in the difftool.

const (
	sgrReset        = "\x1b[0m"
	previewTabWidth = 8 // what a tab advances to, as in a terminal's own default
	// The pane's search marks its matches with these rather than with colour: reverse video for the one
	// `n` landed on, underline for the rest. Colour is git's -- the green and red of a diff are its bytes
	// -- and a highlight that painted over them would hide which kind of line the match is on.
	sgrReverse      = "\x1b[7m"
	sgrReverseOff   = "\x1b[27m"
	sgrUnderline    = "\x1b[4m"
	sgrUnderlineOff = "\x1b[24m"
)

// previewRow is one drawn row of the pane: the cells to write, and the git line they were rendered
// from. The line travels with the row because the search looks for a term in git's line rather than in
// the row it produced: a line wider than the column is drawn as several rows, and a term the break cut
// in half is still a term the reviewer is looking for.
type previewRow struct {
	text string
	line string
}

// rowTexts is the rows as the pane writes them, without the line each came from.
func rowTexts(rows []previewRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.text
	}
	return out
}

// marks is what the pane is highlighting: the term, and which row `n` landed on. The paging and the match
// list count rows with no marks at all, because marking a line cannot change how many rows it is drawn as.
type marks struct {
	term    string
	current int // the row to pick out in reverse video; -1 when there is none
}

// unmarked is what counting asks for.
var unmarked = marks{current: -1}

// previewRows assembles the pane's body: the author's span, and below it -- when there are any --
// the reviewer's own uncommitted edits under a caption naming who they came from. Both sections are
// git's bytes, and git's bytes do not say who typed them: an added line the reviewer wrote and one
// the author wrote are the same green. The caption is what keeps the reviewer's edits from reading
// as the author's. The caption and the blank above it carry no line: the search looks for the diff,
// not for the chrome drawn over it.
func previewRows(span, work Patch, width int, mk marks) []previewRow {
	rows := previewBody(span, width, mk)
	if len(work.Lines) == 0 {
		return rows
	}
	if len(rows) > 0 {
		rows = append(rows, previewRow{})
	}
	rows = append(rows, previewRow{text: styleDim.Render(clip(yourEditsCaption(work), width))})
	// The row the reviewer is standing on, counted from here rather than from the top of the pane.
	return append(rows, previewBody(work, width, marks{term: mk.term, current: mk.current - len(rows)})...)
}

// yourEditsCaption labels the reviewer's own section, with git's counts for it rather than the
// span's, so the number beside the file cannot be read as a number about the author's work.
func yourEditsCaption(work Patch) string {
	if work.Added >= 0 {
		return fmt.Sprintf("\u2500\u2500 you \u00b7 uncommitted  +%d \u2212%d", work.Added, work.Deleted)
	}
	return "\u2500\u2500 you \u00b7 uncommitted"
}

// previewBody renders a patch as rows of at most width cells: a right-aligned line number, a
// space, then git's own text, wrapped when it is too long for the column. Every row opens the
// styles it needs and closes them again, because the renderer skips redrawing a row that has not
// changed -- a colour left open on a skipped row would tint everything written under it.
func previewBody(patch Patch, width int, mk marks) []previewRow {
	numbers, largest := lineNumbers(patch.Lines)
	l, ok := layout(width, largest)
	if !ok {
		return nil
	}
	out := make([]previewRow, 0, len(patch.Lines))
	for i, line := range patch.Lines {
		number := ""
		if numbers[i] > 0 {
			number = strconv.Itoa(numbers[i])
		}
		out = append(out, l.line(line, number, len(out), mk)...)
	}
	return out
}

// laidOut is the column the pane draws in: a gutter wide enough for the largest line number it will print,
// and the rest for the text. A diff and a document are drawn by the same two rules, because the pane is one
// thing the reviewer looks at and the numbers down its left edge should mean the same distance in both.
type laidOut struct {
	gutter int
	body   int
}

func layout(width, largest int) (laidOut, bool) {
	gutter := len(strconv.Itoa(largest))
	if gutter < 3 {
		gutter = 3
	}
	l := laidOut{gutter: gutter, body: width - gutter - 1}
	return l, l.body >= 4 // below this the column shows neither the text nor its indentation
}

// line is one source line as the rows it is drawn on. first is how many rows the pane has already got, which
// is what says whether the row the reviewer is standing on is among these: the marks go on the line before
// it is broken up, so that a term the column cuts in half is marked on both halves -- wrapLine carries
// styling across a break the way it carries git's own colours. Counting the rows first is safe because
// marking a line cannot move where it breaks.
func (l laidOut) line(text, number string, first int, mk marks) []previewRow {
	marked := highlightRow(text, mk.term, false)
	rows := wrapLine(marked, l.body)
	if mk.term != "" && mk.current >= first && mk.current < first+len(rows) {
		rows = wrapLine(highlightRow(text, mk.term, true), l.body)
	}
	out := make([]previewRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, previewRow{
			text: styleDim.Render(fmt.Sprintf("%*s", l.gutter, number)) + " " + row, line: text})
		number = "" // a wrapped line is still one line, numbered once
	}
	return out
}

// docRows lays a document out the way a diff is laid out, and numbers it with the file's own lines, which is
// how a reviewer points at a paragraph. A section with a name gets the caption the diff's "you" section gets,
// for the same reason: text with nothing above it saying where it came from reads as one document.
func docRows(doc Document, width int, mk marks) []previewRow {
	lines := 0
	for _, sec := range doc.Sections {
		lines += len(sec.Lines)
	}
	l, ok := layout(width, lines)
	if !ok {
		return nil
	}
	var out []previewRow
	for _, sec := range doc.Sections {
		if sec.Name != "" {
			if len(out) > 0 {
				out = append(out, previewRow{})
			}
			out = append(out, previewRow{text: styleDim.Render(clip("\u2500\u2500 "+sec.Name, width))})
		}
		for i, line := range sec.Lines {
			out = append(out, l.line(line, strconv.Itoa(i+1), len(out), mk)...)
		}
	}
	return out
}

// --- searching the pane ------------------------------------------------------

// foldSearch is the smart-case rule `less` and `vim` use: a term written entirely in lower case is
// looked for in any case, and one with a capital in it is looked for exactly. It is what lets `/lock`
// find LockManager while `/Lock` does not find lock.
func foldSearch(term string) bool { return term == strings.ToLower(term) }

// containsTerm is the search's match, asked of a line with its escapes stripped -- the colour git put
// between two letters of a word is not part of the word.
func containsTerm(plain, term string) bool {
	if term == "" {
		return false
	}
	if foldSearch(term) {
		return strings.Contains(strings.ToLower(plain), strings.ToLower(term))
	}
	return strings.Contains(plain, term)
}

// matchRows returns the rows to jump between: one entry for each git line that carries an occurrence of
// term, given as the first row that line is drawn on. Matching a whole git line rather than a drawn row
// is what makes a term the column broke in half findable; the highlight is still per row, so each half
// of that term lights up on the row it is on.
func matchRows(rows []previewRow, term string) []int {
	if term == "" {
		return nil
	}
	var out []int
	for i, r := range rows {
		if !containsTerm(ansi.Strip(r.line), term) {
			continue
		}
		// The continuation rows of one line carry the same line, and the line is one match.
		if i > 0 && rows[i-1].line == r.line {
			continue
		}
		out = append(out, i)
	}
	return out
}

// highlightRow wraps every occurrence of term in the row's own cells: reverse video for the match the
// reviewer is on and underline for the others. It finds the matches in the row's visible cells and
// splices the escapes into the string that carries git's colours, because rebuilding the row from its
// plain text is how a highlight ends up drawing the diff in white.
func highlightRow(row, term string, current bool) string {
	if term == "" {
		return row
	}
	on, off := sgrUnderline, sgrUnderlineOff
	if current {
		on, off = sgrReverse, sgrReverseOff
	}

	var (
		visible []rune
		starts  []int // the byte each visible rune starts at, and where the row's text ends
	)
	for i := 0; i < len(row); {
		if row[i] == 0x1b {
			i += escapeLen(row[i:])
			continue
		}
		r, size := utf8.DecodeRuneInString(row[i:])
		visible, starts = append(visible, r), append(starts, i)
		i += size
	}
	starts = append(starts, len(row))

	fold := foldSearch(term)
	needle := []rune(term)
	var (
		b     strings.Builder
		last  int
		found bool
	)
	for i := 0; i+len(needle) <= len(visible); {
		if !matchesAt(visible[i:], needle, fold) {
			i++
			continue
		}
		// The match's bytes are the span from its first rune to the byte the rune after it starts at,
		// which is why `starts` has one entry past the last rune.
		from, to := starts[i], starts[i+len(needle)]
		b.WriteString(row[last:from])
		b.WriteString(on + row[from:to] + off)
		last, found, i = to, true, i+len(needle)
	}
	if !found {
		return row
	}
	b.WriteString(row[last:])
	return b.String()
}

// matchesAt is the same comparison containsTerm makes, over runes, so a highlight and a jump cannot
// disagree about what a match is.
func matchesAt(hay, needle []rune, fold bool) bool {
	if len(needle) > len(hay) {
		return false
	}
	for i, r := range needle {
		other := hay[i]
		if fold {
			r, other = unicode.ToLower(r), unicode.ToLower(other)
		}
		if r != other {
			return false
		}
	}
	return true
}

// lineNumbers returns the number each line of the patch carries on the side being reviewed, and
// the largest of them, which is how wide the gutter has to be. The numbers come from git's own
// @@ headers and from counting the +, - and context lines beneath them -- the same arithmetic git
// did -- so a line that no hunk header covers gets nothing rather than a guess.
func lineNumbers(lines []string) ([]int, int) {
	numbers := make([]int, len(lines))
	old, new := -1, -1
	max := 0
	for i, line := range lines {
		// git colours the very characters we are looking for, so the classification reads the
		// line without them while the display keeps them.
		plain := ansi.Strip(line)
		var number int
		switch {
		case strings.HasPrefix(plain, "diff "):
			// A new file's metadata precedes its first hunk; nothing before that header is
			// worth numbering.
			old, new = -1, -1
		case strings.HasPrefix(plain, "@@"):
			o, n, ok := parseHunk(plain)
			if !ok {
				old, new = -1, -1
			} else {
				old, new = o, n
			}
		case new < 0 || strings.HasPrefix(plain, `\`):
			// metadata, or git's "\ No newline at end of file"
		case strings.HasPrefix(plain, "+"):
			number, new = new, new+1
		case strings.HasPrefix(plain, "-"):
			if strings.HasPrefix(plain, "--- ") {
				break
			}
			if old > 0 {
				number, old = old, old+1
			}
		default:
			number, new = new, new+1
			if old > 0 {
				old++
			}
		}
		numbers[i] = number
		if number > max {
			max = number
		}
	}
	return numbers, max
}

// parseHunk reads the two ends of an "@@ -1,3 +1,5 @@ title" header. Each side starts at the line
// number it names, which is what a count of zero -- a hunk that only adds, or only removes --
// relies on.
func parseHunk(header string) (old, new int, ok bool) {
	first, last := strings.IndexByte(header, ' '), strings.LastIndex(header, " @@")
	if first < 0 || last < first {
		return 0, 0, false
	}
	span := strings.Split(header[first+1:last], " ")
	if len(span) != 2 {
		return 0, 0, false
	}
	start := func(side string) (int, bool) {
		if len(side) < 2 || (side[0] != '-' && side[0] != '+') {
			return 0, false
		}
		n, err := strconv.Atoi(strings.SplitN(side[1:], ",", 2)[0])
		if err != nil {
			return 0, false
		}
		return n, true
	}
	o, ok1 := start(span[0])
	n, ok2 := start(span[1])
	return o, n, ok1 && ok2
}

// wrapLine breaks a line at the column limit and changes nothing else about it: escapes travel
// with the text they style, a continuation row re-opens the styles in force at its break, and
// spaces stay exactly where git put them. Tabs are the one thing that cannot be left alone: git
// emits them literally, a terminal advances them to the next stop, and a column measured with the
// tab worth nothing is a column the terminal wraps for you -- which shifts the whole frame. So a
// tab becomes the spaces it would advance to, counted against the row it lands on.
//
// That last part is why this is not ansi.Wrap, whose word wrapping collapses whitespace at the
// break -- indentation in a diff is the content, so the spaces are kept, only spelled out.
func wrapLine(s string, limit int) []string {
	if limit <= 0 {
		return []string{s}
	}
	if !strings.ContainsRune(s, '\t') && ansi.StringWidth(s) <= limit {
		return []string{s}
	}

	var (
		out     []string
		row     strings.Builder
		inUse   strings.Builder // the SGR sequences opened so far and not yet reset
		pending string          // ... as they stood when the current row began
		width   int
	)
	flush := func() {
		if row.Len() == 0 {
			return
		}
		// The prefix is the state at the start of the row, not at its end: a row that carries the
		// reset which closed its own colour still has to say what colour it was drawn in.
		text := pending + row.String()
		if strings.ContainsRune(text, 0x1b) {
			text += sgrReset
		}
		out = append(out, text)
		row.Reset()
		pending = inUse.String()
		width = 0
	}

	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			n := escapeLen(s[i:])
			seq := s[i : i+n]
			switch {
			case !strings.HasSuffix(seq, "m"): // styling we do not track travels untouched
			case seq == sgrReset || seq == "\x1b[m":
				inUse.Reset()
			default:
				inUse.WriteString(seq)
			}
			row.WriteString(seq)
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == '\t' {
			pad := previewTabWidth - width%previewTabWidth
			if width+pad > limit {
				flush()
				i += size // the tab's padding belonged to the row it started on
				continue
			}
			row.WriteString(strings.Repeat(" ", pad))
			width += pad
			i += size
			continue
		}
		w := runewidth.RuneWidth(r)
		if width+w > limit && row.Len() > 0 {
			flush()
		}
		row.WriteString(s[i : i+size])
		width += w
		i += size
	}
	flush()
	return out
}

// escapeLen measures an escape sequence. git's colour output is SGR -- ESC [ params m -- and
// anything else is passed through as a unit rather than parsed into submission.
func escapeLen(s string) int {
	if len(s) < 2 || s[0] != 0x1b {
		return 1
	}
	if s[1] != '[' {
		return 2
	}
	for i := 2; i < len(s); i++ {
		if s[i] >= '@' && s[i] <= '~' {
			return i + 1
		}
	}
	return len(s)
}
