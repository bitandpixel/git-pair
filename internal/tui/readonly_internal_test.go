package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/gittest"
	"gitpair/internal/lifecycle"
	"gitpair/internal/span"
)

// A span whose head is a commit is a look at history. Everything here is about the
// screen honouring that: refusing the actions that would change something, saying so
// where the reviewer is already looking, and keeping the reading keys working.

const readonlySlug = "booking"

// readonlyModel builds a changeset with one review submission and work after it, then
// opens a session over sel. The fixture comes back too, for assertions about the
// working tree: the point of most of these refusals is that nothing is written.
func readonlyModel(t *testing.T, sel span.Selector) (reviewModel, *gittest.Fixture) {
	t.Helper()
	ctx := context.Background()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n\nfunc main() {}\n"))
	f.CreateBranch(readonlySlug)
	f.CommitChangeset(readonlySlug, "main")
	f.Commit("implement", gittest.WithFiles(map[string]string{
		"service.go": "package main\n\nfunc Lock() {}\n",
		"handler.go": "package main\n\nfunc Serve() {}\n",
	}))
	f.CommitReviewMarker(readonlySlug, "feedback",
		gittest.WithFile("service.go", "package main\n\n// Please use a transaction here\nfunc Lock() {}\n"))
	// The author edits the prose as well as the code, so a span that starts after the review
	// contains an ABOUT.md change against a version that already existed.
	f.Commit("author response", gittest.WithFiles(map[string]string{
		"handler.go":                  "package main\n\nfunc Serve() { ctx() }\n",
		"changesets/booking/ABOUT.md": "# booking\n\nResponded: the handler takes a context now.\n",
	}))

	repo := &git.Repo{Dir: f.Dir()}
	cs, err := changeset.Current(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	summary, err := lifecycle.SummarizeHEAD(ctx, repo, cs.Slug, cs.Base)
	if err != nil {
		t.Fatalf("SummarizeHEAD: %v", err)
	}
	sess, err := NewSession(ctx, Options{Repo: repo, Changeset: cs, Summary: summary, Span: sel})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	m := reviewModel{ctx: ctx, sess: sess, width: 100, height: 24, threadsOpen: true}
	m.refresh()
	return m, f
}

// historySel is "what did this changeset look like at the review": both ends commits, so
// nothing in it can be marked, edited, or submitted against.
func historySel() span.Selector {
	return span.Selector{Base: span.ChangesetBase(), Head: span.Review(-1)}
}

func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func marksOf(files []File) []bool {
	out := make([]bool, len(files))
	for i, f := range files {
		out[i] = f.Reviewed
	}
	return out
}

func TestHistoricalSpanRefusesEverythingThatChangesSomething(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyMsg
	}{
		{"space marks reviewed", tea.KeyMsg{Type: tea.KeySpace}},
		{"e edits a file", runeKey('e')},
		{"T starts a thread", runeKey('T')},
		{"s submits a review", runeKey('s')},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, f := readonlyModel(t, historySel())
			if !m.sess.Span().Historical() {
				t.Fatal("the fixture is not a historical span, so this test proves nothing")
			}
			before := marksOf(m.sess.Files())
			rows := len(m.rows)

			updated, _ := m.Update(tc.key)
			got := updated.(reviewModel)

			if got.mode != modeFiles {
				t.Errorf("mode = %v, want the reviewer kept out of submit and prompt modes", got.mode)
			}
			if !strings.Contains(got.status, "read-only") {
				t.Errorf("status = %q, want an explanation rather than silence", got.status)
			}
			// "read-only" alone leaves the reviewer hunting for the way out.
			if !strings.Contains(got.status, "V chooses") {
				t.Errorf("status = %q, want it to name the key that gets back to a reviewable span", got.status)
			}
			if !strings.Contains(got.status, "last review") {
				t.Errorf("status = %q, want it to name the head the span is stuck on", got.status)
			}
			if diff := marksOf(got.sess.Files()); len(diff) != len(before) {
				t.Errorf("the file list changed length: %d, want %d", len(diff), len(before))
			} else {
				for i := range before {
					if before[i] != diff[i] {
						t.Errorf("file %d changed its reviewed mark", i)
					}
				}
			}
			if len(got.rows) != rows {
				t.Errorf("the row list changed from %d rows to %d", rows, len(got.rows))
			}
			if !f.Clean() {
				t.Error("the refusal wrote to the working tree")
			}
		})
	}
}

// Enter on a file the span added hands the terminal to the editor, so over history it is refused for
// the same reason `e` is: the file on disk is not the file this span contains. `d` is the key that
// only reads, and it is the way a reviewer of history gets to the added file's patch.
func TestHistoricalSpanRefusesEnterOnAnAddedFile(t *testing.T) {
	m, _ := readonlyModel(t, historySel())
	at, _ := cursorOnPath(t, m, "handler.go")
	if got := at.rows[at.cursor].change; got != ChangeAdded {
		t.Fatalf("the row for handler.go carries %v, want an added file", got)
	}

	updated, cmd := at.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(reviewModel)
	if cmd != nil {
		t.Error("enter handed the terminal to the editor over a historical span")
	}
	for _, want := range []string{"read-only", "V chooses", "handler.go"} {
		if !strings.Contains(got.status, want) {
			t.Errorf("status = %q, want it to say %q", got.status, want)
		}
	}

	if _, cmd := got.Update(runeKey('d')); cmd == nil {
		t.Error("d on the same row would not open the difftool, which reading history does allow")
	}
}

// The two jumps into the box name rows rather than actions, so they are the keys that came out of the
// read-only gate: a reviewer reading history still comes to the box to read what the author said, and
// `e` on the row they land on is the key that stays refused.
func TestHistoricalSpanStillJumpsAroundTheBox(t *testing.T) {
	m, _ := readonlyModel(t, historySel())

	for _, tc := range []struct {
		name string
		key  tea.KeyMsg
		want rowKind
	}{
		{"a goes to ABOUT.md", runeKey('a'), rowAbout},
		{"t goes to the threads", runeKey('t'), rowThreadsHead},
	} {
		updated, cmd := m.Update(tc.key)
		got := updated.(reviewModel)
		if cmd != nil {
			t.Errorf("%s handed the terminal over from a historical span", tc.name)
		}
		if strings.Contains(got.status, "read-only") {
			t.Errorf("%s was refused: %q", tc.name, got.status)
		}
		if !got.metaHasFocus() || got.rows[got.metaCursor].kind != tc.want {
			t.Errorf("%s left %v on a %v row, want the box on a %v row",
				tc.name, got.focus, got.rows[got.metaCursor].kind, tc.want)
		}
	}
}

// The other half of a read-only screen: the keys that only read must still work, and `v`
// must be a way out rather than a dead end.
func TestHistoricalSpanStillReadsAndEscapes(t *testing.T) {
	m, _ := readonlyModel(t, historySel())

	updated, _ := m.Update(runeKey('j'))
	m = updated.(reviewModel)
	if m.cursor != 1 {
		t.Errorf("cursor = %d, want j to still move", m.cursor)
	}

	updated, _ = m.Update(runeKey('v'))
	m = updated.(reviewModel)
	if !m.sess.Span().Live() {
		t.Fatalf("v left the session on a historical span: %s", m.sess.Span().Label)
	}
	// Now the same key is legitimate again, which is what makes the refusal a mode
	// rather than a broken screen.
	updated, _ = m.Update(runeKey('s'))
	if got := updated.(reviewModel); got.mode != modeSubmit {
		t.Errorf("mode = %v after escaping to a live span, want the submit prompt", got.mode)
	}
}

func TestHistoricalScreenSaysWhatItIs(t *testing.T) {
	m, _ := readonlyModel(t, historySel())
	view := m.View()

	for _, want := range []string{"HISTORICAL", "READ ONLY"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen never says %q:\n%s", want, view)
		}
	}
	// A number of files reviewed over a span nobody is reviewing reads as progress
	// that was not made, so the counter and the gutter are simply absent.
	for _, absent := range []string{"reviewed", "○ ", "+ new thread"} {
		if strings.Contains(view, absent) {
			t.Errorf("the historical screen still offers %q:\n%s", absent, view)
		}
	}

	live, _ := readonlyModel(t, span.Full())
	if !live.sess.Span().Live() {
		t.Fatal("the comparison model is not live")
	}
	liveView := live.View()
	for _, want := range []string{"reviewed", "○ ", "+ new thread"} {
		if !strings.Contains(liveView, want) {
			t.Errorf("the live screen lost %q", want)
		}
	}
}

func TestHistoricalHelpBarOffersOnlyWhatItCanDo(t *testing.T) {
	m, _ := readonlyModel(t, historySel())
	help := m.helpText()
	for _, absent := range []string{"space reviewed", "e edit", "T new thread", "s submit"} {
		if strings.Contains(help, absent) {
			t.Errorf("the historical shortcut bar still advertises %q:\n%s", absent, help)
		}
	}
	for _, want := range []string{"enter open", "d diff", "p preview", "v spans"} {
		if !strings.Contains(help, want) {
			t.Errorf("the historical shortcut bar lost %q", want)
		}
	}
}

// The tool has to see the span on screen. A live span compares its start against the
// working tree so edits survive; history compares two pins, because today's files are
// not what the span contains.
func TestDifftoolHeadIsThePinOnlyOverHistory(t *testing.T) {
	history, _ := readonlyModel(t, historySel())
	if got := difftoolHead(history.sess.Span()); got != history.sess.Span().To {
		t.Errorf("historical tool head = %q, want the pinned head %q", got, history.sess.Span().To)
	}

	live, _ := readonlyModel(t, span.Full())
	if got := difftoolHead(live.sess.Span()); got != "" {
		t.Errorf("live tool head = %q, want the working tree (no second revision)", got)
	}
}

// The `you` section is the reviewer's uncommitted edits against what they are reviewing.
// Over history that question has no answer, so the pane must not ask it — and must not
// sit there saying it is still waiting for an answer that will never come.
func TestHistoricalPreviewAsksGitAboutOneThingOnly(t *testing.T) {
	m, _ := readonlyModel(t, historySel())
	m.width, m.height = 140, 24
	m.previewOn = true
	m.refresh()

	var asked []string
	m.patchFor = func(_ context.Context, path string) Patch {
		asked = append(asked, "span "+path)
		return Patch{}
	}
	m.workingFor = func(_ context.Context, path string) Patch {
		asked = append(asked, "working "+path)
		return Patch{}
	}

	m = askPreview(t, m)

	for _, a := range asked {
		if strings.HasPrefix(a, "working ") {
			t.Errorf("the pane asked git about the working tree over a historical span (%s)", a)
		}
	}
	if len(asked) == 0 {
		t.Fatal("the pane asked git about nothing at all")
	}
	// The empty-span note has to be the honest one, not a wait for a fetch that is
	// never coming.
	if !strings.Contains(m.View(), "no changes in this span") {
		t.Errorf("the pane is not reporting the empty span:\n%s", strings.Join(m.previewLines(), "\n"))
	}
	if strings.Contains(m.View(), "reading your edits") {
		t.Error("the pane is waiting for an answer about edits it never asked about")
	}
}

// afterReviewAboutSel is a historical span that starts at the review and ends at the author's
// response. ABOUT.md existed at its left end and was rewritten inside it, which is the shape
// that makes that row a diff rather than an edit.
func afterReviewAboutSel() span.Selector {
	return span.Selector{Base: span.Review(-1), Head: span.Commit("HEAD")}
}

// The gate has to cover the write as well as the edit. Over history the ABOUT.md row is a
// legitimate diff, and when the working tree has no ABOUT.md at all, the row's other job is to
// create one and open it in an editor — a write and a handoff in the mode whose promise is that
// there is neither.
func TestHistoricalSpanDoesNotCreateTheAboutItDiffersFrom(t *testing.T) {
	m, f := readonlyModel(t, afterReviewAboutSel())
	about := m.sess.AboutPath()
	if !m.sess.Span().Historical() {
		t.Fatalf("span %s is not historical, so this test proves nothing", m.sess.Span().Label)
	}
	if !m.inSpan[about] {
		t.Fatalf("%s is not in span %s, so this test proves nothing", about, m.sess.Span().Label)
	}
	if !m.sess.HasVersionAt(context.Background(), m.sess.Span().From, about) {
		t.Fatalf("%s did not exist at the span's left end, so the row is an edit, not a diff", about)
	}

	f.Remove(about)
	before := f.MustGit("status", "--porcelain")

	m = focusOnRow(t, m, boxIndexOf(t, m, rowAbout))
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(reviewModel)

	if f.HasWorktreeFile(about) {
		t.Errorf("the read-only screen created %s in the working tree", about)
	}
	if after := f.MustGit("status", "--porcelain"); after != before {
		t.Errorf("the read-only screen changed the working tree:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if strings.Contains(got.status, "read-only") {
		t.Errorf("the reviewer was refused a document they may read: %q", got.status)
	}
	if cmd == nil {
		t.Error("Enter on the ABOUT.md row over history opened nothing, want the difftool for the span's two pins")
	}
}

// The live half, so the guard above is known to be a mode boundary and not a dead branch: a
// reviewer on a span they can act on still gets a missing ABOUT.md created and opened.
func TestLiveSpanStillCreatesAWorkingTreeAbout(t *testing.T) {
	m, f := readonlyModel(t, span.Full())
	about := m.sess.AboutPath()
	f.Remove(about)

	m = focusOnRow(t, m, boxIndexOf(t, m, rowAbout))
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(reviewModel)

	if cmd == nil {
		t.Fatalf("Enter on a missing ABOUT.md opened nothing (status %q)", got.status)
	}
	if !f.HasWorktreeFile(about) {
		t.Errorf("%s was not created for a span the reviewer can edit", about)
	}
}

// The model's half of `v`: a refusal is what turns the next press into the way out. Without one the
// press is an ordinary step around the ring, because a reviewer comparing two pieces of history is
// not stuck -- and stealing their next stop is the bug these two tests exist to keep apart.
//
// The fixture is built so the two answers differ: the ring is [history, full, unreviewed, custom],
// the reviewer's own reviewable span is the last of them, and the next stop around the ring is not.
func vRing(t *testing.T) reviewModel {
	t.Helper()
	ctx := context.Background()
	m, f := readonlyModel(t, historySel())
	if err := m.sess.SetSpan(ctx, span.Full()); err != nil {
		t.Fatalf("SetSpan(full): %v", err)
	}
	custom := span.Selector{Base: span.Commit(f.Parent(f.Head())), Head: span.WorkingTree()}
	if err := m.sess.SetSpan(ctx, custom); err != nil {
		t.Fatalf("SetSpan(custom): %v", err)
	}
	if err := m.sess.SetSpan(ctx, historySel()); err != nil {
		t.Fatalf("SetSpan(history): %v", err)
	}
	if _, total := m.sess.SpanPosition(); total != 4 {
		t.Fatalf("the ring holds %d stops, want history, full, unreviewed and the custom span", total)
	}
	return m
}

// What `v` used to do after a refusal was land somewhere different from where it would otherwise
// have gone, which is the same unpredictability the cycling bug had, with extra steps. The walk is
// the same walk whatever the screen refused; `V` is what gets you to a reviewable span.
func TestARefusalDoesNotChangeWhereVGoes(t *testing.T) {
	ctx := context.Background()

	walked := pressKey(t, vRing(t), runeKey('v'))
	want, _ := walked.sess.SpanPosition()

	m := vRing(t)
	m = pressKey(t, m, runeKey('s'))
	if !strings.Contains(m.status, "read-only") {
		t.Fatalf("s was not refused on a historical span; status was %q", m.status)
	}
	if !strings.Contains(m.status, "V chooses") {
		t.Errorf("the refusal does not name the key that does get out: %q", m.status)
	}
	m = pressKey(t, m, runeKey('v'))

	pos, _ := m.sess.SpanPosition()
	if pos != want {
		t.Errorf("v after a refusal went to stop %d, want the walk to the next stop %d", pos, want)
	}
	// And history is still not a dead end: keep walking and a reviewable span turns up.
	for presses := 0; !m.sess.Span().Live() && presses < 4; presses++ {
		if _, err := m.sess.StepSpan(ctx); err != nil {
			t.Fatalf("StepSpan: %v", err)
		}
	}
	if !m.sess.Span().Live() {
		t.Errorf("walking the ring from history never reached a span that can be reviewed")
	}
}

// Skipping only helps if the reviewer can read it. A walk that passes over a span in silence looks
// exactly like a ring that forgot a span -- so the status names what was skipped and why, on the key
// press that skipped it.
func TestVNamesTheStopItSkipped(t *testing.T) {
	m, f := readonlyModel(t, span.Full())
	ctx := context.Background()
	f.MustGit("tag", "gone-tag", f.Head())
	if err := m.sess.SetSpan(ctx, span.Selector{Base: span.ChangesetBase(), Head: span.Commit("gone-tag")}); err != nil {
		t.Fatalf("SetSpan(tagged): %v", err)
	}
	// Stand on the stop before the tagged one, so the next `v` is the press that meets it.
	if err := m.sess.SetSpan(ctx, span.SinceReview(-1)); err != nil {
		t.Fatalf("SetSpan(unreviewed): %v", err)
	}
	m.refresh()
	f.MustGit("update-ref", "-d", "refs/tags/gone-tag")

	m = pressKey(t, m, runeKey('v'))
	for _, want := range []string{"span ", "skipped", "gone-tag"} {
		if !strings.Contains(m.status, want) {
			t.Errorf("after skipping a span whose tag is gone the status says %q, want it to name %q", m.status, want)
		}
	}
	// The reason travels with the name: "skipped X" alone reads as a span that was deleted.
	if strings.Count(m.status, "skipped") != 1 {
		t.Errorf("the status repeats the skip rather than naming one span: %q", m.status)
	}
}
