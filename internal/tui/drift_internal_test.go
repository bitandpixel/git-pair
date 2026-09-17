package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"gitpair/internal/gittest"
	"gitpair/internal/span"
)

// Drift is a warning about the ground moving, not about what the reviewer just did, so it gets
// a row of the screen rather than the status line the last keystroke owns. And it is a report:
// the span on screen stays the one that was chosen until `r` says otherwise.

// driftModel returns a model reviewing from a ref-backed base, plus the fixture to move that
// ref under it.
func driftModel(t *testing.T) (reviewModel, *gittest.Fixture) {
	t.Helper()
	m, f := pickerFixture(t, 0)
	f.MustGit("branch", "probe", f.RevParse("HEAD~1"))
	sel := span.Selector{Base: span.Ref("refs/heads/probe"), Head: span.WorkingTree()}
	if err := m.sess.SetSpan(context.Background(), sel); err != nil {
		t.Fatalf("SetSpan: %v", err)
	}
	m.refresh()
	return m, f
}

// runDriftCheck runs the check the way the timer does: the work happens off the event loop and
// comes back as a message, and the update path is what stores the answer.
func runDriftCheck(t *testing.T, m reviewModel) reviewModel {
	t.Helper()
	_, cmd := m.Update(driftCheckMsg{})
	if cmd == nil {
		t.Fatal("the drift check scheduled nothing")
	}
	updated, _ := m.Update(cmd())
	got, ok := updated.(reviewModel)
	if !ok {
		t.Fatalf("the drift answer did not come back to the review model: %T", updated)
	}
	return got
}

func TestDriftBannerWarnsAndNamesR(t *testing.T) {
	m, f := driftModel(t)
	pinned := m.sess.Span().From

	if view := m.View(); strings.Contains(view, "moved") {
		t.Errorf("a banner appeared before anything moved:\n%s", view)
	}

	f.MustGit("update-ref", "refs/heads/probe", f.RevParse("HEAD~2"))
	got := runDriftCheck(t, m)

	view := got.View()
	if !strings.Contains(view, "probe moved") {
		t.Errorf("no drift banner in:\n%s", view)
	}
	if !strings.Contains(view, "[r] refresh") {
		t.Errorf("the banner does not name the key that fixes it:\n%s", view)
	}
	if strings.Contains(view, "refs/heads/probe") {
		t.Errorf("the banner spells the full ref name; a reviewer chose `probe`:\n%s", view)
	}
	if got.sess.Span().From != pinned {
		t.Errorf("the span moved to %s while the banner was up; a warning is not a refresh",
			got.sess.Span().From)
	}
	// The banner is a row of the screen, so it comes out of the list, not out of the terminal.
	if rows := len(strings.Split(view, "\n")); rows != got.height {
		t.Errorf("the frame is %d rows in a %d row window with the banner up", rows, got.height)
	}
}

// In a split screen the list column is narrow, and a banner parked there loses its key to an
// ellipsis — which is the whole point of the banner. It lives in the footer, which is as wide
// as the terminal.
func TestDriftBannerKeepsItsKeyInASplitScreen(t *testing.T) {
	m, f := driftModel(t)
	m.previewOn = true
	m.refresh()
	if m.paneWidth() == 0 {
		t.Skip("window too narrow for the preview pane")
	}

	f.MustGit("update-ref", "refs/heads/probe", f.RevParse("HEAD~2"))
	got := runDriftCheck(t, m)

	view := got.View()
	if !strings.Contains(view, "[r] refresh") {
		t.Errorf("the banner lost its key to the narrow list column:\n%s", view)
	}
	if rows := len(strings.Split(view, "\n")); rows != got.height {
		t.Errorf("the frame is %d rows in a %d row window with the banner and the pane up", rows, got.height)
	}
}

func TestRRefreshesTheDriftedRefAndClearsTheBanner(t *testing.T) {
	m, f := driftModel(t)
	from := m.sess.Span().From
	f.MustGit("update-ref", "refs/heads/probe", f.RevParse("HEAD~2"))
	m = runDriftCheck(t, m)
	if len(m.sess.Drifted()) != 1 {
		t.Fatalf("Drifted() = %v, want probe reported once", m.sess.Drifted())
	}

	updated, _ := m.Update(runeKey('r'))
	got := updated.(reviewModel)

	if got.statusErr {
		t.Fatalf("r reported an error: %s", got.status)
	}
	if !strings.HasPrefix(got.status, "refreshed probe ") {
		t.Errorf("r said %q, want a report naming the ref and where it moved", got.status)
	}
	if strings.Contains(got.View(), "moved") {
		t.Errorf("the banner is still up after the refresh:\n%s", got.View())
	}
	if got.sess.Span().From == from {
		t.Error("the span did not move, so the refresh did nothing")
	}
	if len(got.sess.Drifted()) != 0 {
		t.Errorf("Drifted() = %v after the refresh, want nothing", got.sess.Drifted())
	}
}

// `r` is only advertised while there is something to refresh. Pressing it at any other time
// should say so rather than sit on an unbound key.
func TestRWithoutDriftSaysSo(t *testing.T) {
	m, _ := driftModel(t)
	updated, _ := m.Update(runeKey('r'))
	got := updated.(reviewModel)
	if !got.statusErr {
		t.Errorf("r with nothing drifted set %q without an error flag", got.status)
	}
	if !strings.Contains(got.status, "nothing has moved") {
		t.Errorf("r said %q, want it to say nothing has moved", got.status)
	}
}

// A slow check answers about the span it asked about. By the time it comes back the reviewer may
// have stepped elsewhere, and a banner about a span nobody is looking at is a lie about the
// screen they are on.
func TestDriftAnswerAboutAnotherSpanIsDropped(t *testing.T) {
	m, f := driftModel(t)
	f.MustGit("update-ref", "refs/heads/probe", f.RevParse("HEAD~2"))
	m = runDriftCheck(t, m)
	held := m.sess.Drifted()
	if len(held) != 1 {
		t.Fatalf("Drifted() = %v, want the move reported", held)
	}

	updated, _ := m.Update(driftMsg{moved: []span.Drift{{Name: "refs/heads/elsewhere", Pinned: "aaa1111", Current: "bbb2222"}},
		from: "0000000000000000000000000000000000000000", to: "1111111111111111111111111111111111111111"})
	got := updated.(reviewModel)

	if after := got.sess.Drifted(); len(after) != 1 || after[0].Name != held[0].Name {
		t.Errorf("a drift answer about another span left %v, want the one about this span", after)
	}
}

// Two refs can move while you read. A span picked between two branches pins both, and the
// banner has to say both moved rather than naming one and implying the other is still where you
// left it. The `(+N more)` branch, and a refresh that has to re-pin two endpoints, are the parts
// every single-ref test walks past.
func TestDriftBannerCountsEveryMovedRef(t *testing.T) {
	m, f := pickerFixture(t, 0)
	f.MustGit("branch", "probe-a", f.RevParse("HEAD~2"))
	f.MustGit("branch", "probe-b", f.RevParse("HEAD~1"))
	if err := m.sess.SetSpan(context.Background(), span.Selector{
		Base: span.Ref("refs/heads/probe-a"),
		Head: span.Ref("refs/heads/probe-b"),
	}); err != nil {
		t.Fatalf("SetSpan: %v", err)
	}
	m.refresh()
	before := m.sess.Span()
	if before.CanMark() {
		t.Fatal("a span between two refs ends at a commit, so it must open read-only")
	}

	// Both branches move, and they keep their order: a refresh that re-pinned only the base
	// would leave a span starting after it ends.
	f.Commit("drift 1", gittest.WithFile("drift1.go", "package main\n"))
	f.Commit("drift 2", gittest.WithFile("drift2.go", "package main\n"))
	f.MustGit("update-ref", "refs/heads/probe-a", f.RevParse("HEAD~1"))
	f.MustGit("update-ref", "refs/heads/probe-b", f.RevParse("HEAD"))

	got := runDriftCheck(t, m)
	if n := len(got.sess.Drifted()); n != 2 {
		t.Fatalf("Drifted() = %v, want both refs reported", got.sess.Drifted())
	}

	view := got.View()
	if !strings.Contains(view, "probe-a moved") {
		t.Errorf("the banner does not name the first moved ref:\n%s", view)
	}
	if !strings.Contains(view, "(+1 more)") {
		t.Errorf("the banner names one ref and stays silent about the second:\n%s", view)
	}
	// One row however many refs moved: a second row would come out of the file list.
	if rows := len(strings.Split(view, "\n")); rows != got.height {
		t.Errorf("the frame is %d rows in a %d row window with a two-ref banner up", rows, got.height)
	}

	updated, _ := got.Update(runeKey('r'))
	after := updated.(reviewModel)
	if after.statusErr {
		t.Fatalf("r reported an error: %s", after.status)
	}
	for _, name := range []string{"probe-a", "probe-b"} {
		if !strings.Contains(after.status, name) {
			t.Errorf("r said %q, which does not mention %s", after.status, name)
		}
	}
	if n := len(after.sess.Drifted()); n != 0 {
		t.Errorf("Drifted() = %v after the refresh, want nothing", n)
	}
	sp := after.sess.Span()
	if sp.From == before.From || sp.To == before.To {
		t.Errorf("refresh left the span at %.8s..%.8s; both endpoints should have re-pinned",
			sp.From, sp.To)
	}
	if sp.CanMark() {
		t.Error("re-pinning the endpoints turned a look at history into a screen you can submit from")
	}
}

// A moved ref can drop a file out of the span altogether — `main` absorbed the change, so the
// diff you marked reviewed is no longer part of what you are reviewing. The mark stops applying,
// and the note has to count it: a reviewed counter that quietly falls from 1 to 0 between two
// keystrokes is how a review tool loses trust, and `r` is the keystroke that did it.
func TestRReportsTheMarksAMovedRefInvalidated(t *testing.T) {
	m, f := driftModel(t)
	m = cursorOn(t, m, "service.go")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(reviewModel)
	if reviewed, _ := m.sess.Count(); reviewed != 1 {
		t.Fatalf("reviewed = %d after Space, want 1", reviewed)
	}

	// The ref moves to the tip: everything the span compared is now inside the base.
	f.MustGit("update-ref", "refs/heads/probe", f.RevParse("HEAD"))
	m = runDriftCheck(t, m)

	updated, _ = m.Update(runeKey('r'))
	got := updated.(reviewModel)
	if !strings.Contains(got.status, "1 reviewed mark no longer applies") {
		t.Errorf("r said %q, want the note to count the mark it invalidated", got.status)
	}
	reviewed, total := got.sess.Count()
	if reviewed != 0 || total != 0 {
		t.Errorf("after the refresh %d of %d files are marked, want an empty span with nothing marked",
			reviewed, total)
	}
}

// A span with no named-ref endpoint has nothing that can move under it, and must not ask.
func TestSpanWithNoRefEndpointReportsNoDrift(t *testing.T) {
	m, _ := pickerFixture(t, 0)
	if moved := m.sess.CheckDrift(context.Background()); moved != nil {
		t.Errorf("CheckDrift on a changeset-base span = %v, want nothing to check", moved)
	}
	if line := m.driftLine(); line != "" {
		t.Errorf("driftLine() = %q with nothing to warn about", line)
	}
}
