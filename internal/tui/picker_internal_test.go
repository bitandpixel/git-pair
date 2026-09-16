package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"gitpr/internal/changeset"
	"gitpr/internal/git"
	"gitpr/internal/gittest"
	"gitpr/internal/lifecycle"
	"gitpr/internal/span"
)

// The picker is a two-step commitment: `Space` chooses one end, `Enter` applies both. Most
// of what is worth breaking there is whether a pending choice really stays pending, and
// whether the two drills hand back the checkpoint they claim to.

func pickerFixture(t *testing.T, reviews int) (reviewModel, *gittest.Fixture) {
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
	for i := range reviews {
		f.CommitReviewMarker(readonlySlug, "feedback",
			gittest.WithFile(fmt.Sprintf("notes-%d.md", i), "note\n"))
		f.Commit(fmt.Sprintf("response %d", i),
			gittest.WithFile("service.go", fmt.Sprintf("package main\n\nfunc Lock() { tx%d() }\n", i)))
	}

	repo := &git.Repo{Dir: f.Dir()}
	cs, err := changeset.ForBranch(repo, readonlySlug)
	if err != nil {
		t.Fatalf("ForBranch: %v", err)
	}
	summary, err := lifecycle.SummarizeHEAD(ctx, repo, cs.Slug, cs.Base)
	if err != nil {
		t.Fatalf("SummarizeHEAD: %v", err)
	}
	sess, err := NewSession(ctx, Options{Repo: repo, Changeset: cs, Summary: summary, Span: span.Full()})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	m := reviewModel{ctx: ctx, sess: sess, width: 100, height: 30}
	m.refresh()
	return m, f
}

func open(t *testing.T, m reviewModel) reviewModel {
	t.Helper()
	updated, _ := m.Update(runeKey('V'))
	got := updated.(reviewModel)
	if got.mode != modeSpan {
		t.Fatalf("V did not open the picker (mode %v)", got.mode)
	}
	return got
}

func pressKey(t *testing.T, m reviewModel, key tea.KeyMsg) reviewModel {
	t.Helper()
	updated, _ := m.Update(key)
	return updated.(reviewModel)
}

// indexOf finds a row in a column by its label, so tests navigate by what the reviewer
// reads rather than by an offset that changes when the list does.
func rowIndex(t *testing.T, m reviewModel, base bool, label string) int {
	t.Helper()
	for i, it := range m.endpointsFor(base) {
		if it.label == label {
			return i
		}
	}
	t.Fatalf("the %s column has no row %q", map[bool]string{true: "BASE", false: "HEAD"}[base], label)
	return -1
}

func TestVPickerShowsTheSpanInTheScreen(t *testing.T) {
	m, _ := pickerFixture(t, 1)
	m = open(t, m)
	view := m.View()

	for _, want := range []string{"Span picker", "BASE", "HEAD", "Selected:", "Changeset Base", "Working Tree", "Review -1"} {
		if !strings.Contains(view, want) {
			t.Errorf("the picker never shows %q:\n%s", want, view)
		}
	}
	// It opens where the reviewer is, not at the top of a list.
	if got := m.pick.cursor[0]; m.endpointsFor(true)[got].label != "Changeset Base" {
		t.Errorf("BASE cursor = %q, want it on the span in use", m.endpointsFor(true)[got].label)
	}
	if got := m.pick.cursor[1]; m.endpointsFor(false)[got].label != "Working Tree" {
		t.Errorf("HEAD cursor = %q, want it on Working Tree", m.endpointsFor(false)[got].label)
	}
	if !strings.Contains(view, "LIVE") {
		t.Error("the pending pair says nothing about what the session would become")
	}
}

// §4: HEAD is not a checkpoint a reviewer can pick. Working Tree is the only "current"
// target on offer, because it is the only one that can be edited.
func TestPickerNeverOffersHEAD(t *testing.T) {
	m, _ := pickerFixture(t, 1)
	for _, base := range []bool{true, false} {
		for _, it := range m.endpointsFor(base) {
			if it.label == "HEAD" {
				t.Errorf("the picker offers HEAD as a checkpoint")
			}
		}
	}
	if labels := columnLabels(m.endpointsFor(false)); !strings.Contains(strings.Join(labels, "|"), "Working Tree") {
		t.Error("the head column should offer the working tree, the one target that is editable")
	}
}

func columnLabels(items []pickerItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.label)
	}
	return out
}

// §5: recent submissions by alias from the end, older ones by index from the start, and
// the stored index is the one shown.
func TestPickerNamesReviewsByAliasThenIndex(t *testing.T) {
	m, _ := pickerFixture(t, 5)
	var got []string
	for _, it := range m.endpointsFor(true) {
		if strings.HasPrefix(it.label, "Review ") {
			got = append(got, it.label)
		}
	}
	want := []string{"Review -1", "Review -2", "Review -3", "Review 1", "Review 0"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("review rows = %v, want %v", got, want)
	}

	// Choosing by alias stores the alias, so the choice means the same submission
	// whichever end of the span it lands on.
	for _, it := range m.endpointsFor(true) {
		if it.label == "Review -2" && it.ckpt.Index != -2 {
			t.Errorf("Review -2 stored index %d, want -2", it.ckpt.Index)
		}
	}
}

func TestSpaceIsPendingAndEnterApplies(t *testing.T) {
	m, _ := pickerFixture(t, 2)
	m = open(t, m)

	// Move to the HEAD column and choose the newest review.
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = pressKey(t, m, runeKey('j')) // Working Tree -> Review -1
	if got := m.endpointsFor(false)[m.pick.cursor[1]].label; got != "Review -1" {
		t.Fatalf("cursor is on %q, want Review -1", got)
	}
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeySpace})

	if m.sess.Span().Historical() {
		t.Error("Space applied the span; only Enter may do that")
	}
	if m.pick.head.Kind != span.KindReview {
		t.Errorf("pending head = %s, want the review chosen", m.pick.head.Kind)
	}
	// The picker shows what applying would cost, before it is applied.
	if view := m.View(); !strings.Contains(view, "HISTORICAL") || !strings.Contains(view, "read-only") {
		t.Errorf("the pending pair does not say it would be read-only:\n%s", view)
	}

	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeFiles {
		t.Errorf("mode = %v after applying, want the review screen", m.mode)
	}
	if !m.sess.Span().Historical() {
		t.Error("Enter did not apply the pending pair")
	}
	if !strings.Contains(m.status, "read-only") {
		t.Errorf("applying a historical span should say so; status = %q", m.status)
	}
	if !strings.Contains(m.View(), "HISTORICAL") {
		t.Error("the review screen is not in read-only mode")
	}
}

func TestPickerCancelDiscardsThePendingPair(t *testing.T) {
	m, _ := pickerFixture(t, 2)
	before := m.sess.Span()
	m = open(t, m)
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	m = pressKey(t, m, runeKey('j'))
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeySpace})
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	if m.mode != modeFiles {
		t.Errorf("mode = %v after cancel", m.mode)
	}
	if m.sess.Span() != before {
		t.Errorf("cancel changed the span from %s to %s", before.Label, m.sess.Span().Label)
	}
	if m.status != "" {
		t.Errorf("status = %q after a clean cancel", m.status)
	}
}

func TestPickerPresets(t *testing.T) {
	m, _ := pickerFixture(t, 2)
	m = open(t, m)

	m = pressKey(t, m, runeKey('u'))
	if m.pick.base.Kind != span.KindReview || m.pick.base.Index != -1 || m.pick.head.Kind != span.KindWorkingTree {
		t.Errorf("u set %s → %s, want review -1 → working tree", m.pick.base.Kind, m.pick.head.Kind)
	}
	if !strings.Contains(m.View(), "unreviewed") && !strings.Contains(m.View(), "after review") {
		t.Error("the u preset's pending pair does not resolve to the unreviewed span")
	}

	m = pressKey(t, m, runeKey('f'))
	if m.pick.base.Kind != span.KindChangesetBase || m.pick.head.Kind != span.KindWorkingTree {
		t.Errorf("f set %s → %s, want changeset base → working tree", m.pick.base.Kind, m.pick.head.Kind)
	}
}

func TestUnreviewedPresetSaysSoWhenThereIsNothingToGoBackTo(t *testing.T) {
	m, _ := pickerFixture(t, 0)
	m = open(t, m)
	m = pressKey(t, m, runeKey('u'))

	if !strings.Contains(m.View(), "no review submissions") {
		t.Errorf("the u preset silently did nothing:\n%s", m.View())
	}
	if m.pick.base.Kind == span.KindReview {
		t.Error("the preset set a review checkpoint that does not exist")
	}
}

func TestCommitDrillFiltersAndPicks(t *testing.T) {
	m, f := pickerFixture(t, 1)
	m = open(t, m)

	// The drill belongs to the active column; choose it with Space.
	at := rowIndex(t, m, true, "Commit…")
	m.pick.cursor[0] = at
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if m.pick.list == nil {
		t.Fatal("Commit… did not open a list")
	}
	if len(m.pick.list.items) == 0 {
		t.Fatal("the commit list is empty")
	}
	view := m.View()
	if !strings.Contains(view, "Pick Commit") || !strings.Contains(view, "filter:") {
		t.Errorf("the drill does not look like a searchable list:\n%s", view)
	}
	if !strings.Contains(view, f.Short(f.Head())) {
		t.Error("the commit list does not show the short sha, which is how a reviewer finds a commit")
	}

	// Typing filters. The response commits are the only ones with "response" in the
	// subject, and there are two of them.
	for _, r := range "response" {
		m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	visible := m.pick.list.visible()
	if len(visible) == 0 {
		t.Fatal("the filter matched nothing")
	}
	for _, i := range visible {
		if !strings.Contains(m.pick.list.items[i].label, "response") {
			t.Errorf("the filter kept %q", m.pick.list.items[i].label)
		}
	}

	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.pick.list != nil {
		t.Error("picking left the drill open")
	}
	if m.pick.base.Kind != span.KindCommit {
		t.Fatalf("pending base = %s, want a commit", m.pick.base.Kind)
	}
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.sess.Span().From != f.Head() {
		t.Errorf("applied base = %s, want the highlighted commit %s", m.sess.Span().From, f.Head())
	}
}

// A reviewer who knows the id should not have to scroll to it: typed text becomes the
// checkpoint, and git's answer is what decides.
func TestCommitDrillTakesATypedRevision(t *testing.T) {
	m, f := pickerFixture(t, 0)
	head := f.Head()
	m = open(t, m)
	at := rowIndex(t, m, true, "Commit…")
	m.pick.cursor[0] = at
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeySpace})

	for _, r := range "HEAD^" {
		m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.pick.base.Kind != span.KindCommit || m.pick.base.Name != "HEAD^" {
		t.Fatalf("typed text became %s %q, want the commit as typed", m.pick.base.Kind, m.pick.base.Name)
	}
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	want, err := f.Git("rev-parse", "HEAD^")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	if m.sess.Span().From != strings.TrimSpace(want) {
		t.Errorf("base = %s, want HEAD^ (%s); HEAD is %s", m.sess.Span().From, want, head)
	}
}

func TestCommitDrillReportsAnIdGitDoesNotKnow(t *testing.T) {
	m, _ := pickerFixture(t, 0)
	m = open(t, m)
	at := rowIndex(t, m, true, "Commit…")
	m.pick.cursor[0] = at
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeySpace})

	for _, r := range "nonsense" {
		m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.pick.list == nil {
		t.Fatal("a checkpoint git cannot resolve was accepted")
	}
	if !strings.Contains(m.pick.list.err, "nonsense") {
		t.Errorf("the drill does not say what failed: %q", m.pick.list.err)
	}
	if m.pick.base.Kind == span.KindCommit {
		t.Error("the bad commit became the pending endpoint anyway")
	}
}

// §11: friendly names on screen, the real ref kept underneath, and the pin is the commit
// it pointed at when chosen.
func TestRefDrillGroupsRefsAndKeepsTheirIdentity(t *testing.T) {
	m, f := pickerFixture(t, 0)
	f.MustGit("branch", "feature/two", f.Head())
	f.MustGit("tag", "v0.1.0", f.Head())

	m = open(t, m)
	at := rowIndex(t, m, true, "Ref…")
	m.pick.cursor[0] = at
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if m.pick.list == nil {
		t.Fatal("Ref… did not open a list")
	}
	view := m.View()
	if !strings.Contains(view, "LOCAL BRANCHES") || !strings.Contains(view, "TAGS") {
		t.Errorf("the ref list is not grouped:\n%s", view)
	}
	var labels, names []string
	for _, it := range m.pick.list.items {
		labels = append(labels, it.label)
		names = append(names, it.ckpt.Name)
	}
	joined := strings.Join(labels, "|")
	for _, want := range []string{"main", readonlySlug, "feature/two", "v0.1.0"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the ref list is missing %q; it shows %s", want, joined)
		}
	}
	if strings.Contains(joined, "refs/heads/main") {
		t.Error("the ref list shows the full ref name where a friendly one is enough")
	}
	if !slicesContains(names, "refs/heads/main") {
		t.Errorf("the checkpoint must keep the full ref name for unambiguous resolution; got %v", names)
	}

	// Filter to one branch and take it: the pending base is that ref, by its full name.
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})   // back to the columns, nothing chosen
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeySpace}) // reopen the drill
	for _, r := range "feature/two" {
		m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	visible := m.pick.list.visible()
	if len(visible) != 1 {
		t.Fatalf("filtering to one branch left %d rows", len(visible))
	}
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.pick.base.Kind != span.KindRef || m.pick.base.Name != "refs/heads/feature/two" {
		t.Errorf("pending base = %s %q, want refs/heads/feature/two", m.pick.base.Kind, m.pick.base.Name)
	}
}

func slicesContains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func TestPickerAppliesARefBaseAndPinsIt(t *testing.T) {
	m, f := pickerFixture(t, 0)
	f.MustGit("branch", "probe", f.Head())
	mainTip, err := f.Git("rev-parse", "main")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}

	m = open(t, m)
	at := rowIndex(t, m, true, "Ref…")
	m.pick.cursor[0] = at
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeySpace})
	for _, r := range "main" {
		m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// "main" matches both the branch and the changeset branch's name; take the first
	// match, which the grouping puts in LOCAL BRANCHES.
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.pick.base.Kind != span.KindRef {
		t.Fatalf("pending base = %s, want a ref", m.pick.base.Kind)
	}
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})

	sp := m.sess.Span()
	if sp.Base.Kind != span.KindRef || sp.Base.Name != "refs/heads/main" {
		t.Errorf("applied base = %s %q, want refs/heads/main", sp.Base.Kind, sp.Base.Name)
	}
	if sp.From != strings.TrimSpace(mainTip) {
		t.Errorf("base pinned to %s, want main at %s", sp.From, mainTip)
	}
	if !sp.Live() {
		t.Error("a ref base with a working-tree head is a live review")
	}
	if !strings.Contains(m.View(), "main@") {
		t.Errorf("the screen should name the ref and its pin; view:\n%s", m.View())
	}
}

func TestPickerHelpBarChangesWithTheDrill(t *testing.T) {
	m, _ := pickerFixture(t, 1)
	m = open(t, m)
	if help := m.helpText(); !strings.Contains(help, "space choose") || !strings.Contains(help, "enter apply") {
		t.Errorf("the columns mode advertises %q", help)
	}

	at := rowIndex(t, m, true, "Commit…")
	m.pick.cursor[0] = at
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeySpace})
	help := m.helpText()
	if !strings.Contains(help, "type to filter") {
		t.Errorf("the drill advertises %q", help)
	}
	// Inside a list, j and k type: a searchable list that spends them on navigation is
	// a list you cannot search.
	if strings.Contains(help, "j/k move") {
		t.Errorf("the drill still advertises j/k as navigation: %q", help)
	}
}

// The picker is drawn with the same divider arithmetic as the preview, so the same
// invariant applies: the frame is exactly the terminal.
func TestThePickerFillsTheWindow(t *testing.T) {
	m, _ := pickerFixture(t, 2)
	m.width, m.height = 120, 28
	m = open(t, m)

	rows := strings.Split(strings.TrimSuffix(m.View(), "\n"), "\n")
	if len(rows) != m.height {
		t.Errorf("the picker is %d rows in a %d row window", len(rows), m.height)
	}
	for i, row := range rows {
		if got := lipgloss.Width(row); got != m.width {
			t.Errorf("row %d is %d cells wide, want %d: %q", i, got, m.width, row)
		}
	}

	// And in the drill too, where the content is a list rather than two columns.
	at := rowIndex(t, m, true, "Commit…")
	m.pick.cursor[0] = at
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeySpace})
	rows = strings.Split(strings.TrimSuffix(m.View(), "\n"), "\n")
	if len(rows) != m.height {
		t.Errorf("the drill is %d rows in a %d row window", len(rows), m.height)
	}
	for i, row := range rows {
		if got := lipgloss.Width(row); got != m.width {
			t.Errorf("drill row %d is %d cells wide, want %d: %q", i, got, m.width, row)
		}
	}
}

// `v` is one key for walking the spans this session has been in — the span it opened on,
// the two presets, and anything chosen with V — and the status line has to say where it
// landed, since the header's span label looks the same shape either way.
func TestVStepsThroughTheSessionSpansAndSaysWhere(t *testing.T) {
	m, f := pickerFixture(t, 1)
	custom := span.Selector{Base: span.Commit(f.Parent(f.Head())), Head: span.WorkingTree()}
	if err := m.sess.SetSpan(m.ctx, custom); err != nil {
		t.Fatalf("SetSpan: %v", err)
	}
	m.refresh()

	updated, _ := m.Update(runeKey('v'))
	got := updated.(reviewModel)
	if got.statusErr {
		t.Fatalf("v reported an error: %s", got.status)
	}
	if !strings.Contains(got.status, "(1 of 3)") {
		t.Errorf("v said %q, want the span named with its position, 1 of 3", got.status)
	}
	if got.sess.Selector().Base.Kind != span.KindChangesetBase {
		t.Errorf("v wrapped to a span based on %s, want the span the session opened on",
			got.sess.Selector().Base.Kind)
	}
}

// With nothing but the presets on the ring, the position would be noise: two stops is the
// toggle the manual describes, and it should read like one.
func TestVBetweenTwoStopsDoesNotCountOutLoud(t *testing.T) {
	m, _ := pickerFixture(t, 1)
	updated, _ := m.Update(runeKey('v'))
	got := updated.(reviewModel)
	if strings.Contains(got.status, " of ") {
		t.Errorf("v between two spans said %q; a position is noise when there are two stops", got.status)
	}
	if !strings.Contains(got.status, "span ") {
		t.Errorf("v said %q, want the span it stepped to", got.status)
	}
}
