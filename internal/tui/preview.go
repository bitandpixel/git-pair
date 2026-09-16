package tui

import (
	"fmt"
	"strconv"
	"strings"
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
)

// previewBody renders a patch as rows of at most width cells: a right-aligned line number, a
// space, then git's own text, wrapped when it is too long for the column. Every row opens the
// styles it needs and closes them again, because the renderer skips redrawing a row that has not
// changed -- a colour left open on a skipped row would tint everything written under it.
func previewBody(patch Patch, width int) []string {
	numbers, max := lineNumbers(patch.Lines)
	gutter := len(strconv.Itoa(max))
	if gutter < 3 {
		gutter = 3
	}
	body := width - gutter - 1
	if body < 4 {
		return nil
	}

	out := make([]string, 0, len(patch.Lines))
	for i, line := range patch.Lines {
		number := ""
		if numbers[i] > 0 {
			number = strconv.Itoa(numbers[i])
		}
		for _, row := range wrapLine(line, body) {
			out = append(out, styleDim.Render(fmt.Sprintf("%*s", gutter, number))+" "+row)
			number = "" // a wrapped line is still one line, numbered once
		}
	}
	return out
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
