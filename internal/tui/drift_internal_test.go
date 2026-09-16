package tui

import (
	"context"
	"strings"
	"testing"

	"gitpr/internal/gittest"
	"gitpr/internal/span"
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
