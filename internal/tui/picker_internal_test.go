package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/gittest"
	"gitpair/internal/lifecycle"
	"gitpair/internal/span"
)

// The picker is a two-step commitment: `Space` chooses one end, `Enter` applies both. Most
// of what is worth breaking there is whether a pending choice really stays pending, and
// whether the two drills hand back the checkpoint they claim to.

func pickerFixture(t *testing.T, reviews int) (reviewModel, *gittest.Fixture) {
	t.Helper()
	ctx := context.Background()
	// Fixed, increasing dates. The columns merge a changeset's commits with its submissions by when
	// they happened, and a fixture whose neighbours land in the same second cannot say which order
	// that was. An hour apart keeps every age the picker prints inside the "Nd" range it expects.
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	stamp := func() gittest.CommitOpt {
		clock = clock.Add(time.Hour)
		return gittest.WithDate(clock)
	}
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n\nfunc main() {}\n"), stamp())
	f.CreateBranch(readonlySlug)
	f.CommitChangeset(readonlySlug, "main", stamp())
	f.Commit("implement", gittest.WithFiles(map[string]string{
		"service.go": "package main\n\nfunc Lock() {}\n",
		"handler.go": "package main\n\nfunc Serve() {}\n",
	}), stamp())
	for i := range reviews {
		f.CommitReviewMarker(readonlySlug, "feedback",
			gittest.WithFile(fmt.Sprintf("notes-%d.md", i), "note\n"), stamp())
		f.Commit(fmt.Sprintf("response %d", i),
			gittest.WithFile("service.go", fmt.Sprintf("package main\n\nfunc Lock() { tx%d() }\n", i)), stamp())
	}

	repo := &git.Repo{Dir: f.Dir()}
	cs, err := changeset.Current(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("Current: %v", err)
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

	for _, want := range []string{"Span picker", "BASE", "HEAD", "Selected:", "Changeset Base", "Current", "Last Review"} {
		if !strings.Contains(view, want) {
			t.Errorf("the picker never shows %q:\n%s", want, view)
		}
	}
	// It opens where the reviewer is, not at the top of a list.
	if got := m.pick.cursor[0]; m.endpointsFor(true)[got].label != "Changeset Base" {
		t.Errorf("BASE cursor = %q, want it on the span in use", m.endpointsFor(true)[got].label)
	}
	if got := m.pick.cursor[1]; m.endpointsFor(false)[got].label != "Current" {
		t.Errorf("HEAD cursor = %q, want it on Current", m.endpointsFor(false)[got].label)
	}
	if !strings.Contains(view, "LIVE") {
		t.Error("the pending pair says nothing about what the session would become")
	}
}

// §4: HEAD is not a checkpoint a reviewer can pick. "Current" is the only live target on offer,
// because it is the only one that can be edited — and it is not spelled with git's word for a
// commit, which is what made the old name easy to mistake for a historical point.
func TestPickerNeverOffersHEAD(t *testing.T) {
	m, _ := pickerFixture(t, 1)
	for _, base := range []bool{true, false} {
		for _, it := range m.endpointsFor(base) {
			if it.label == "HEAD" {
				t.Errorf("the picker offers HEAD as a checkpoint")
			}
		}
	}
	if labels := columnLabels(m.endpointsFor(false)); !strings.Contains(strings.Join(labels, "|"), "Current") {
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

// moveTo walks the active column's cursor to a row by label with the keys a reviewer uses, so a test
// that cares where the cursor landed does not depend on how many commits the history happens to
// hold.
func moveTo(t *testing.T, m reviewModel, label string) reviewModel {
	t.Helper()
	items := m.endpointsFor(m.pick.col == 0)
	for i := 0; i <= len(items); i++ {
		if m.pick.cursor[m.pick.col] < len(items) && items[m.pick.cursor[m.pick.col]].label == label {
			return m
		}
		m = pressKey(t, m, runeKey('j'))
	}
	t.Fatalf("j never reached %q", label)
	return m
}

// The columns are one timeline, not two lists: the question a reviewer asks is what happened between
// two submissions, and the commits that answer it sit where they happened. The changeset's own
// history is what is merged in -- everything below the base is the base's -- and the marker commits
// are not there twice: they are on the list by alias.
func TestColumnsInterleaveTheChangesetsCommitsWithItsReviews(t *testing.T) {
	m, f := pickerFixture(t, 2)
	// Dates the merge can be pinned by: an early commit belongs at the bottom of the history, and a
	// commit newer than everything is still no row at all if it holds nothing.
	f.Commit("before any review", gittest.WithFile("early.go", "package main\n"),
		gittest.WithDate(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)))
	f.EmptyCommit("git-pair: ready never", gittest.WithDate(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)))
	m = open(t, m)

	got := columnLabels(m.endpointsFor(true))
	want := []string{"response 1", "Last Review", "response 0", "Review -2", "before any review"}
	if !orderedBefore(got, want) {
		t.Errorf("the BASE column reads %v, want the commits among the reviews in the order they "+
			"happened", got)
	}
	for _, absent := range []string{"git-pair: ready never", "seed"} {
		if slices.Contains(got, absent) {
			t.Errorf("the BASE column lists %q: the first is an empty marker and the second is below "+
				"the changeset's base", absent)
		}
	}
}

// A submission is rarely the last word: the author answers it and keeps working, and the span a
// reviewer wants next is that work -- `last review..current` -- which no one can build from a row the
// columns do not offer. So the top of a column is the newest commit, not the submission the timeline
// was built around.
func TestCommitsAfterTheLatestReviewAreRowsOfBothColumns(t *testing.T) {
	m, f := pickerFixture(t, 2)
	// Deliberately undated: what is committed now falls after the fixture's stamped history, which is
	// the case being asked about rather than one pinned into the middle of it.
	f.Commit("answering the block", gittest.WithFile("later.go", "package main\n\nfunc Later() {}\n"))
	f.Commit("and then this", gittest.WithFile("later.go", "package main\n\nfunc Later() { now() }\n"))
	m = open(t, m)

	want := []string{"and then this", "answering the block", "Last Review"}
	for _, col := range []struct {
		name string
		base bool
	}{{"BASE", true}, {"HEAD", false}} {
		got := columnLabels(m.endpointsFor(col.base))
		if !orderedBefore(got, want) {
			t.Errorf("the %s column reads %v, want the commits made after the newest submission, newest "+
				"first, above it", col.name, got)
			continue
		}
		// The row has to be the commit, not an alias for the end of the branch: two commits after the
		// submission are two spans, and the reviewer names the one they mean by its subject.
		row := rowIndex(t, m, col.base, "and then this")
		if it := m.endpointsFor(col.base)[row]; it.ckpt.Kind != span.KindCommit || it.ckpt.Name == "HEAD" {
			t.Errorf("the top commit row is %s, want the commit itself", it.ckpt)
		}
	}
}

// Catching the changeset up with the integration branch puts commits the reviewer never wrote in the
// middle of `last review..current`, and a list with no row for the merge has nothing to set as the
// boundary. The merge is on the changeset's own line, so it is a row -- and the commits that arrived
// with it are not, because the base holds them.
func TestAMergeIntoTheChangesetIsARowOfTheColumn(t *testing.T) {
	m, f := pickerFixture(t, 1)
	f.SwitchTo("main")
	f.Commit("work someone else landed", gittest.WithFile("upstream.go", "package main\n\nfunc Up() {}\n"))
	f.SwitchTo(readonlySlug)
	f.MustGit("merge", "--no-ff", "-m", "Merge main into the changeset", "main")
	m = open(t, m)

	got := columnLabels(m.endpointsFor(true))
	if !orderedBefore(got, []string{"Merge main into the changeset", "Last Review"}) {
		t.Errorf("the BASE column reads %v, want the merge that caught the changeset up with main, above "+
			"the submission it came after", got)
	}
	if slices.Contains(got, "work someone else landed") {
		t.Errorf("the BASE column reads %v: the commits that arrived with the merge belong to the base, "+
			"not to this changeset", got)
	}
	row := rowIndex(t, m, true, "Merge main into the changeset")
	if it := m.endpointsFor(true)[row]; it.ckpt.Kind != span.KindCommit {
		t.Errorf("the merge row is %s, want the merge commit itself, which is the boundary a reviewer "+
			"sets", it.ckpt)
	}
}

// orderedBefore reports whether want appears in got in that order, with anything allowed between.
func orderedBefore(got, want []string) bool {
	at := 0
	for _, g := range got {
		if at < len(want) && g == want[at] {
			at++
		}
	}
	return at == len(want)
}

// Enter means apply, wherever the cursor is resting. It used to mean that only on the rows that were
// not drills: pointed at `Commit…`, Enter applied the pair the reviewer had come to the screen to
// change and closed the picker on it. The drills are keys now, so one keystroke cannot mean two
// things depending on where the cursor happens to be.
func TestEnterAlwaysAppliesThePair(t *testing.T) {
	m, _ := pickerFixture(t, 1)
	m = open(t, m)
	m = moveTo(t, m, "Changeset Base")
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeFiles {
		t.Errorf("mode = %v, want Enter to apply from the last row of the list", m.mode)
	}
	if m.pick.list != nil {
		t.Error("Enter opened a drill")
	}
}

// §5: recent submissions by alias from the end, older ones by index from the start, and
// the stored index is the one shown.
func TestPickerNamesReviewsByAliasThenIndex(t *testing.T) {
	m, _ := pickerFixture(t, 5)
	var got []string
	for _, it := range m.endpointsFor(true) {
		if strings.Contains(it.label, "Review") {
			got = append(got, it.label)
		}
	}
	want := []string{"Last Review", "Review -2", "Review -3", "Review 1", "Review 0"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("review rows = %v, want %v", got, want)
	}

	// Choosing by alias stores the alias, so the choice means the same submission
	// whichever end of the span it lands on.
	for _, it := range m.endpointsFor(true) {
		switch it.label {
		case "Review -2":
			if it.ckpt.Index != -2 {
				t.Errorf("Review -2 stored index %d, want -2", it.ckpt.Index)
			}
		case "Last Review":
			// The friendly row has to name the same submission "Review -1" would have: the word is
			// display, never a different way of addressing a review.
			if it.ckpt.Index != -1 {
				t.Errorf("Last Review stored index %d, want -1", it.ckpt.Index)
			}
		}
	}
}

func TestSpaceIsPendingAndEnterApplies(t *testing.T) {
	m, _ := pickerFixture(t, 2)
	m = open(t, m)

	// Move to the HEAD column and choose the newest review.
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyTab})
	// The rows between Current and Last Review are the changeset's own commits, so the test walks
	// there rather than counting them.
	m = moveTo(t, m, "Last Review")
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

	m = drill(t, m, "commit")
	if len(m.pick.list.items) == 0 {
		t.Fatal("the commit list is empty")
	}
	view := m.View()
	if !strings.Contains(view, "Pick Commit") || !strings.Contains(view, "no filter") {
		t.Errorf("the drill does not look like a searchable list:\n%s", view)
	}
	if !strings.Contains(view, f.Short(f.Head())) {
		t.Error("the commit list does not show the short sha, which is how a reviewer finds a commit")
	}

	// `/` first, then the text: the keys belong to the list until the filter is asked for. The
	// response commits are the only ones with "response" in the subject.
	m = filterTo(t, m, "response")
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
	m = drill(t, m, "commit")
	m = filterTo(t, m, "HEAD^")
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
	m = drill(t, m, "commit")
	m = filterTo(t, m, "nonsense")
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
	m = drill(t, m, "ref")
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

	// Filter to one branch and take it: the pending base is that ref, by its full name. esc leaves
	// the drill with nothing chosen, and `r` brings it back.
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	m = drill(t, m, "ref")
	m = filterTo(t, m, "feature/two")
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
	m = drill(t, m, "ref")
	m = filterTo(t, m, "main")
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

	m = drill(t, m, "commit")
	help := m.helpText()
	if !strings.Contains(help, "j k line") || !strings.Contains(help, "/ filter") {
		t.Errorf("the drill advertises %q, want the keys of the mode it is in", help)
	}
	// The columns' keys are not on this screen, so promising them here would advertise keys the
	// drill ignores.
	for _, absent := range []string{"space choose", "enter apply"} {
		if strings.Contains(help, absent) {
			t.Errorf("the drill advertises %q, which it does not read: %q", absent, help)
		}
	}

	m = pressKey(t, m, runeKey('/'))
	if help := m.helpText(); !strings.Contains(help, "type to filter") {
		t.Errorf("the filter mode advertises %q", help)
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
	m = drill(t, m, "commit")
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

// The changeset box is where a wrong span name is most convincing: it is the row that says what the
// whole screen is measuring. `spanName` substitutes the word "unreviewed" for a label, and it may do
// that only for the span that is unreviewed.
func TestHeaderNamesUnreviewedOnlyForTheUnreviewedSpan(t *testing.T) {
	m, _ := pickerFixture(t, 2)
	ctx := context.Background()

	if err := m.sess.SetSpan(ctx, span.SinceReview(-1)); err != nil {
		t.Fatalf("SetSpan unreviewed: %v", err)
	}
	m.refresh()
	// The word is what is asserted, not the line around it: the label beside it is styled, and the
	// escapes would make an exact match a test of the style rather than of the span's name.
	if !strings.Contains(m.View(), "unreviewed") {
		t.Errorf("the changeset box no longer says unreviewed for the unreviewed span:\n%s", headerOf(m))
	}

	// Three submissions back is not unreviewed: most of what it contains has been through a
	// review already, and the span label names the review it starts after.
	if err := m.sess.SetSpan(ctx, span.SinceReview(-2)); err != nil {
		t.Fatalf("SetSpan older review: %v", err)
	}
	m.refresh()
	if view := m.View(); strings.Contains(view, "unreviewed") {
		t.Errorf("the changeset box calls a span based on an older review unreviewed:\n%s", headerOf(m))
	} else if !strings.Contains(view, "review -2..current") {
		t.Errorf("the changeset box does not name the span in the spelling that was chosen:\n%s", headerOf(m))
	}
}

// headerOf is the changeset box on its own, for failure messages that would otherwise dump a whole
// screen to make a point about one line.
func headerOf(m reviewModel) string {
	return strings.Join(m.boxLines(nil), "\n")
}

// --- the drill-in's two modes -------------------------------------------------

// drill opens the commit or ref drill of whichever column is active. They answer the `c` and `r`
// keys rather than sitting in the list as rows: on a row, `Enter` -- the key a reviewer presses on
// the thing they are pointing at -- applied the pair instead of descending.
func drill(t *testing.T, m reviewModel, kind string) reviewModel {
	t.Helper()
	if m.mode != modeSpan {
		m = open(t, m)
	}
	key := 'c'
	if kind == "ref" {
		key = 'r'
	}
	m = pressKey(t, m, runeKey(key))
	if m.pick.list == nil {
		t.Fatalf("%c did not open the %s drill", key, kind)
	}
	return m
}

// filterTo starts a filter with `/` and types at it: the way a reviewer reaches the filter now that
// the keys belong to the list by default.
func filterTo(t *testing.T, m reviewModel, s string) reviewModel {
	t.Helper()
	m = pressKey(t, m, runeKey('/'))
	return typeText(t, m, s)
}

func typeText(t *testing.T, m reviewModel, s string) reviewModel {
	t.Helper()
	for _, r := range s {
		if r == ' ' {
			m = pressKey(t, m, tea.KeyMsg{Type: tea.KeySpace})
			continue
		}
		m = pressKey(t, m, runeKey(r))
	}
	return m
}

func tabKey() tea.KeyMsg  { return tea.KeyMsg{Type: tea.KeyTab} }
func escKey() tea.KeyMsg  { return tea.KeyMsg{Type: tea.KeyEsc} }
func backKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyBackspace} }

// The drill opens on the list: a reviewer scanning commits mostly moves, and the keys for that are
// the ones the rest of the screen already uses. `/` is what asks for the filter -- a key of its own
// so it does not collide with the navigation it replaces.
func TestDrillOpensOnTheListAndSlashStartsTheFilter(t *testing.T) {
	m, _ := pickerFixture(t, 1)
	m = drill(t, m, "commit")
	if m.pick.filtering {
		t.Fatal("the drill opened with the keys on the filter; they belong to the list until /")
	}
	m = pressKey(t, m, runeKey('j'))
	if m.pick.list.sel != 1 {
		t.Errorf("j moved to %d, want one row down", m.pick.list.sel)
	}
	if m.pick.list.filter != "" {
		t.Errorf("j changed the filter to %q, want it left alone", m.pick.list.filter)
	}

	m = pressKey(t, m, runeKey('/'))
	if !m.pick.filtering {
		t.Fatal("/ did not start a filter")
	}
	m = typeText(t, m, "j")
	if m.pick.list.filter != "j" {
		t.Fatalf("filter = %q, want the letter typed at it", m.pick.list.filter)
	}
	if m.pick.list.sel != 0 {
		t.Errorf("typing left the cursor at %d, want it back at the top of what matches", m.pick.list.sel)
	}

	// esc hands the keys back and keeps the text: the rows you filtered down to are the rows you
	// wanted to move through.
	m = pressKey(t, m, escKey())
	if m.pick.filtering {
		t.Error("esc did not hand the keys back to the list")
	}
	if m.pick.list.filter != "j" {
		t.Errorf("esc threw the filter %q away", m.pick.list.filter)
	}
	if m.pick.list == nil {
		t.Fatal("esc left the drill as well as the filter")
	}
}

// Space is a filter character while you are typing -- "response 0" is a thing to search for -- and
// picks when the keys are on the list.
func TestDrillSpaceTypesWhileTypingAndPicksWhenNavigating(t *testing.T) {
	m, _ := pickerFixture(t, 1)
	m = drill(t, m, "commit")
	m = filterTo(t, m, "response 0")
	if got := m.pick.list.filter; got != "response 0" {
		t.Fatalf("filter = %q, want the space kept: subjects contain them", got)
	}
	for _, i := range m.pick.list.visible() {
		if !strings.Contains(m.pick.list.items[i].label, "response 0") {
			t.Errorf("a space in the filter kept %q", m.pick.list.items[i].label)
		}
	}
	if m.pick.list == nil {
		t.Fatal("typing closed the drill")
	}
	if m.pick.base.Kind == span.KindCommit {
		t.Error("space while typing picked an entry")
	}

	m = pressKey(t, m, escKey())
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if m.pick.list != nil {
		t.Fatalf("space in navigation mode did not pick: %v", m.pick.list)
	}
	if m.pick.base.Kind != span.KindCommit {
		t.Errorf("space picked %s, want a commit", m.pick.base.Kind)
	}
}

// A page moves by what the window shows, so the page keys and the renderer have to count rows
// the same way. The height here is small enough that a page is a fraction of the list.
func TestDrillNavigationMovesWithVimKeys(t *testing.T) {
	m, f := pickerFixture(t, 1)
	for i := range 12 {
		f.Commit(fmt.Sprintf("work %d", i), gittest.WithFile(fmt.Sprintf("w%d.go", i), "package main\n"))
	}
	m.height = 12
	m = drill(t, m, "commit")
	total := len(m.pick.list.visible())
	if total < 10 {
		t.Fatalf("the list has %d rows, want enough to page through", total)
	}
	rows := m.listRows()

	m = pressKey(t, m, runeKey('G'))
	if got := m.pick.list.sel; got != total-1 {
		t.Errorf("G went to %d, want the last row %d", got, total-1)
	}
	m = pressKey(t, m, runeKey('g'))
	m = pressKey(t, m, runeKey('g'))
	if got := m.pick.list.sel; got != 0 {
		t.Errorf("gg went to %d, want the top", got)
	}
	m = pressKey(t, m, runeKey('g'))
	m = pressKey(t, m, runeKey('j'))
	if got := m.pick.list.sel; got != 1 {
		t.Errorf("a lone g then j went to %d, want one row down -- g waits for its partner", got)
	}
	before := m.pick.list.sel
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlD})
	if got := m.pick.list.sel; got != before+rows/2 {
		t.Errorf("ctrl-d moved %d rows from %d, want half a page (%d)", got-before, before, rows/2)
	}
	before = m.pick.list.sel
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlB})
	if got := m.pick.list.sel; got != max(0, before-rows) {
		t.Errorf("ctrl-b went to %d from %d, want a page up", got, before)
	}
	m = pressKey(t, m, runeKey('g'))
	m = pressKey(t, m, runeKey('g'))
	before = m.pick.list.sel
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlF})
	if got := m.pick.list.sel; got != before+rows {
		t.Errorf("ctrl-f moved %d rows from %d, want a page (%d) -- further than the window shows "+
			"would put the cursor off screen", got-before, before, rows)
	}
	before = m.pick.list.sel
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	if got := m.pick.list.sel; got != before-rows/2 {
		t.Errorf("ctrl-u moved %d rows from %d, want half a page back", got-before, before)
	}

	// f and b are the same pages without the modifier, which is what a reviewer reaching for the
	// vim keys presses.
	m = pressKey(t, m, runeKey('g'))
	m = pressKey(t, m, runeKey('g'))
	m = pressKey(t, m, runeKey('f'))
	if got := m.pick.list.sel; got != rows {
		t.Errorf("f moved to %d, want a page (%d) down", got, rows)
	}
	m = pressKey(t, m, runeKey('b'))
	if got := m.pick.list.sel; got != 0 {
		t.Errorf("b moved to %d, want the page back to the top", got)
	}

	// d and u are the half pages without the modifier, which is what the preview overlay takes them
	// for, and a reviewer who has paged a diff all session reaches for them out of habit.
	m = pressKey(t, m, runeKey('d'))
	if got := m.pick.list.sel; got != rows/2 {
		t.Errorf("d moved to %d, want half a page (%d) down", got, rows/2)
	}
	m = pressKey(t, m, runeKey('u'))
	if got := m.pick.list.sel; got != 0 {
		t.Errorf("u moved to %d, want the half page back to the top", got)
	}
}

// Backspace deletes a character of the filter and nothing else. On an empty filter it used to close
// the drill, so clearing one mistyped keystroke dumped the reviewer out of the list they were
// choosing from.
func TestDrillBackspaceEditsTheFilterAndNeverLeaves(t *testing.T) {
	m, _ := pickerFixture(t, 1)
	m = drill(t, m, "commit")
	m = pressKey(t, m, runeKey('/'))

	m = pressKey(t, m, backKey())
	if m.pick.list == nil {
		t.Fatal("backspace on an empty filter closed the drill")
	}
	if !m.pick.filtering {
		t.Error("backspace left filter mode as well as editing nothing")
	}
	m = typeText(t, m, "x")
	m = pressKey(t, m, backKey())
	if m.pick.list == nil {
		t.Fatal("backspace closed the drill instead of deleting the character")
	}
	if m.pick.list.filter != "" {
		t.Errorf("filter = %q, want the character deleted", m.pick.list.filter)
	}

	// With the keys on the list it does nothing at all: the filter is edited behind `/`.
	m = pressKey(t, m, escKey())
	m = pressKey(t, m, backKey())
	if m.pick.list == nil || m.pick.filtering {
		t.Errorf("backspace in navigation mode left the drill or its mode: list=%v filtering=%v",
			m.pick.list, m.pick.filtering)
	}

	// esc is the leaving key: first out of the filter, then out of the drill, then out of the picker.
	m = pressKey(t, m, escKey())
	if m.pick.list != nil {
		t.Fatal("esc did not back out of the drill")
	}
	if m.mode != modeSpan {
		t.Errorf("esc left the picker entirely (mode %v), want the columns still open", m.mode)
	}
	m = pressKey(t, m, escKey())
	if m.mode != modeFiles {
		t.Errorf("a second esc did not cancel the picker (mode %v)", m.mode)
	}
}

// The bar is the only place a mode documents its keys, so it names the keys that mode reads --
// and does not advertise the other mode's.
func TestDrillBarNamesTheKeysOfTheModeItIsIn(t *testing.T) {
	nav, filtering := helpSpan(true, false), helpSpan(true, true)
	for _, want := range []string{"j k", "gg", "G", "d/u", "ctrl-d/u", "f/b", "/ filter", "esc"} {
		if !strings.Contains(nav, want) {
			t.Errorf("the navigation bar omits %q: %q", want, nav)
		}
	}
	// The filter's keys are not what navigation mode reads, and promising them would be a lie.
	for _, absent := range []string{"type to filter", "backspace"} {
		if strings.Contains(nav, absent) {
			t.Errorf("the navigation bar advertises %q, which it does not read: %q", absent, nav)
		}
	}
	for _, want := range []string{"type to filter", "backspace delete", "enter pick", "esc navigate"} {
		if !strings.Contains(filtering, want) {
			t.Errorf("the filter bar omits %q: %q", want, filtering)
		}
	}
	// j and k type letters while filtering, so promising them as movement there would be a lie.
	for _, absent := range []string{"j k", "gg", "ctrl-d"} {
		if strings.Contains(filtering, absent) {
			t.Errorf("the filter bar advertises %q, which it does not read: %q", absent, filtering)
		}
	}
}

// While the columns are off screen, the drill's own heading is the only place that says which
// end the pick will land on.
func TestDrillSaysWhichEndItIsChoosing(t *testing.T) {
	m, _ := pickerFixture(t, 1)
	m = drill(t, m, "commit")
	if view := m.View(); !strings.Contains(view, "for BASE") {
		t.Errorf("the commit drill does not say it is choosing the base:\n%s", view)
	}
	m = pressKey(t, m, escKey())
	m = pressKey(t, m, tabKey())
	m = drill(t, m, "ref")
	if view := m.View(); !strings.Contains(view, "for HEAD") {
		t.Errorf("the ref drill does not say it is choosing the head:\n%s", view)
	}
}

// markedLine is the row of a column wearing the asterisk, or "" when none is.
func markedLine(col string) string {
	for _, l := range strings.Split(col, "\n") {
		if strings.Contains(l, "*") {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

// A commit inside the changeset is a row of the timeline, and that row takes the asterisk. A ref --
// or a commit older than the window the columns read -- has no row among the changeset's own
// history, so it earns one of its own: without it the column carries no mark, and the reviewer
// cannot see which end the pick went to now that the drills are keys rather than rows.
func TestChosenCheckpointKeepsItsMark(t *testing.T) {
	m, _ := pickerFixture(t, 1)
	m = drill(t, m, "commit")
	m = filterTo(t, m, "response 0")
	m = pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.pick.base.Kind != span.KindCommit {
		t.Fatalf("pending base = %s, want the commit picked", m.pick.base.Kind)
	}
	line := markedLine(m.columnText(true, 60))
	if !strings.Contains(line, "response 0") {
		t.Errorf("the BASE column marks %q, want the mark on the commit's own row", line)
	}
	if other := markedLine(m.columnText(false, 60)); !strings.Contains(other, "Current") {
		t.Errorf("the HEAD column marks %q, want its own pending end (Current) and nothing from BASE", other)
	}

	m2, f := pickerFixture(t, 1)
	f.MustGit("branch", "feature/marked", f.Head())
	m2 = open(t, m2)
	m2 = pressKey(t, m2, tabKey())
	m2 = drill(t, m2, "ref")
	m2 = filterTo(t, m2, "feature/marked")
	m2 = pressKey(t, m2, tea.KeyMsg{Type: tea.KeyEnter})
	if m2.pick.head.Kind != span.KindRef {
		t.Fatalf("pending head = %s, want the ref picked", m2.pick.head.Kind)
	}
	line = markedLine(m2.columnText(false, 60))
	if !strings.Contains(line, "feature/marked") {
		t.Errorf("the HEAD column marks %q, want a row naming the ref it holds", line)
	}
	if base := m2.columnText(true, 60); strings.Contains(base, "* feature/marked") {
		t.Errorf("a head choice marked the BASE column:\n%s", base)
	}
	if want := rowIndex(t, m2, false, "feature/marked"); m2.pick.cursor[1] != want {
		t.Errorf("the HEAD cursor sits on %d, want the marked row %d", m2.pick.cursor[1], want)
	}
}

// The drill's chrome grows: the bar wraps at narrower widths, a refusal adds a row, and the
// (above:)/(below:) hints arrive when the list is longer than the window. Every one of them is a
// row the list no longer has, and a frame taller than the terminal repaints by scrolling -- which
// loses the shortcut bar, the one thing telling you how to leave.
func TestDrillFramesFitTheTerminal(t *testing.T) {
	for _, width := range []int{100, 72, 60} {
		for _, height := range []int{30, 16, 12} {
			m, _ := pickerFixture(t, 2)
			m.width, m.height = width, height
			// The columns first: their content is fixed, so only chrome can push them over.
			assertFrameFits(t, open(t, m))
			m = drill(t, m, "commit")

			assertFrameFits(t, m)
			for _, filtering := range []bool{false, true} {
				m.pick.filtering = filtering
				assertFrameFits(t, m)
			}
			// With a refusal on screen, and with a status line long enough to wrap.
			m.pick.list.err = "no commit \"nonsense\" in this repository"
			m.status = "span main...current (2 of 4) · 3 reviewed marks no longer apply · read-only"
			assertFrameFits(t, m)
		}
	}
}

// A short terminal windows the candidate columns instead of overflowing, follows the cursor, and
// keeps the divider on every row -- an uneven column count would hang the divider over nothing.
func TestShortTerminalWindowsTheCandidateColumns(t *testing.T) {
	m, _ := pickerFixture(t, 4)
	m.width, m.height = 90, 12
	m = open(t, m)
	total, rows := len(m.endpointsFor(true)), m.columnRows()
	if total <= rows {
		t.Fatalf("the fixture fits without windowing: %d candidates in %d rows", total, rows)
	}
	assertFrameFits(t, m)

	m.pick.cursor[0] = total - 1
	last := m.endpointsFor(true)[total-1].label
	block := strings.Split(strings.TrimSuffix(m.pickerBlock(), "\n"), "\n")
	// Three heading rows -- "Span picker", the blank, the BASE/HEAD titles -- then the window.
	window := block[3 : 3+rows]
	if !strings.Contains(strings.Join(window, "\n"), last) {
		t.Errorf("the window did not follow the cursor to %q:\n%s", last, strings.Join(window, "\n"))
	}
	for _, line := range window {
		if !strings.Contains(line, "\u2502") {
			t.Errorf("a candidate row has no divider: %q", line)
		}
	}
}

// Ctrl-C is the universal leave, and in this screen it used to be a dead key: the columns answered
// Esc and q, the drill answered Esc, and neither answered the key a reviewer reaches for when they
// want out of a screen.
func TestCtrlCLeavesThePickerFromAnywhere(t *testing.T) {
	m := pressKey(t, open(t, mustPicker(t)), tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.quitting {
		t.Error("ctrl-c from the picker's columns did not leave")
	}
	drilled := pressKey(t, drill(t, mustPicker(t), "commit"), tea.KeyMsg{Type: tea.KeyCtrlC})
	if !drilled.quitting {
		t.Error("ctrl-c from inside a drill did not leave")
	}
}

func mustPicker(t *testing.T) reviewModel {
	t.Helper()
	m, _ := pickerFixture(t, 1)
	return m
}

// One span, one string. The picker used to name the pending pair twice -- "changeset base → working
// tree" above "main...current" -- which reads as two spans described slightly differently rather than
// one span described once. The preview now says what the header, the status line and `status --json`
// say, and keeps the endpoint words only where there is no span to name.
func TestSelectedLineNamesTheSpanTheRestOfTheAppNames(t *testing.T) {
	m, _ := pickerFixture(t, 1)
	view := open(t, m).selectedBlock()
	if !strings.Contains(view, "main...current") {
		t.Fatalf("the preview does not use the span string the rest of the app uses:\n%s", view)
	}
	for _, absent := range []string{"working tree", "changeset base", "\u2192"} {
		if strings.Contains(view, absent) {
			t.Errorf("the preview names the endpoints beside the span, so the screen shows one span twice:\n%s", view)
		}
	}
	if !strings.Contains(view, "LIVE") || !strings.Contains(view, "editable") {
		t.Errorf("the preview lost what the pair would let the screen do:\n%s", view)
	}

	// The span leads the row, so a narrow terminal loses the mode rather than the answer.
	narrow := open(t, mustPicker(t))
	narrow.width = 24
	if got := narrow.selectedBlock(); !strings.Contains(got, "main...current") {
		t.Errorf("a clipped preview lost the span rather than the mode:\n%s", got)
	}

	// The unreviewed span is live -- it ends at the working tree -- and is named as the header names
	// it: last review..current, not review -1 → working tree.
	if err := m.sess.SetSpan(context.Background(), span.SinceReview(-1)); err != nil {
		t.Fatalf("SetSpan: %v", err)
	}
	pending := m
	pending.pick = pending.newSpanPicker() // what `V` does: the pending pair starts from the span on screen
	view = pending.selectedBlock()
	if !strings.Contains(view, "last review..current") {
		t.Errorf("the preview of a live pair is not the header's span string:\n%s", view)
	}
	if strings.Contains(view, "\u2192") {
		t.Errorf("the preview names the endpoints beside the span:\n%s", view)
	}

	// A historical pair likewise, with the mode that belongs to it.
	hist := span.Selector{Base: span.ChangesetBase(), Head: span.Review(-1)}
	if err := m.sess.SetSpan(context.Background(), hist); err != nil {
		t.Fatalf("SetSpan(history): %v", err)
	}
	fromHistory := m
	fromHistory.pick = fromHistory.newSpanPicker()
	view = fromHistory.selectedBlock()
	if !strings.Contains(view, "main...last review") {
		t.Errorf("the preview of a historical pair is not the header's span string:\n%s", view)
	}
	if !strings.Contains(view, "HISTORICAL") || !strings.Contains(view, "read-only") {
		t.Errorf("a historical pair does not say it is read-only:\n%s", view)
	}

	// Where git cannot resolve the pair there is no span to name, and the words the reviewer chose
	// are the most specific thing on screen.
	m2, _ := pickerFixture(t, 1)
	m2 = open(t, m2)
	m2.pick.head = span.Commit("nonsense-not-a-revision")
	view = m2.selectedBlock()
	if !strings.Contains(view, "nonsense-not-a-revision") {
		t.Errorf("an unresolvable pair does not say what was chosen:\n%s", view)
	}
	for _, absent := range []string{"LIVE", "HISTORICAL"} {
		if strings.Contains(view, absent) {
			t.Errorf("an unresolvable pair was described as if it resolved: %q\n%s", absent, view)
		}
	}
}
