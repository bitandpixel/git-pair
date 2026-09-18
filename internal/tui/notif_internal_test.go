package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The bottom of the screen is one band of a height the layout decides, not the message. A note
// appears inside it, over the shortcut bar, and leaves on its own; a refusal waits for the reviewer
// to press something; the drift warning is derived from the session, so it comes back as soon as
// whatever was covering it is gone. None of the three moves a row of the list above them.

func plainView(m reviewModel) string {
	return ansiCodes.ReplaceAllString(m.View(), "")
}

func bandOf(t *testing.T, m reviewModel) []string {
	t.Helper()
	view := ansiCodes.ReplaceAllString(m.View(), "")
	rows := strings.Split(strings.TrimSuffix(view, "\n"), "\n")
	n := m.bandRows()
	if len(rows) < n {
		t.Fatalf("the frame is %d rows, too short for a %d-row band:\n%s", len(rows), n, m.View())
	}
	return rows[len(rows)-n:]
}

func send(t *testing.T, m reviewModel, msg tea.Msg) reviewModel {
	t.Helper()
	updated, _ := m.Update(msg)
	got, ok := updated.(reviewModel)
	if !ok {
		t.Fatalf("update with %T returned %T", msg, updated)
	}
	return got
}

// The row count of the frame is not the invariant, because the frame is padded to the terminal
// whether or not the list fills it. What must not move is the number of rows the list is given.
func TestNotificationTakesTheBandInsteadOfARow(t *testing.T) {
	m := navModel(t)
	chrome, window := m.chromeRows(), m.rowArea()
	if !strings.Contains(plainView(m), "q quit") {
		t.Fatal("the shortcut bar is not on screen to begin with")
	}

	m.setStatus("3 of 4 reviewed", false)

	if got := m.chromeRows(); got != chrome {
		t.Errorf("chrome grew from %d to %d rows when a note appeared", chrome, got)
	}
	if got := m.rowArea(); got != window {
		t.Errorf("the list had %d rows and now has %d with a note on screen", window, got)
	}
	view := plainView(m)
	if strings.Contains(view, "q quit") {
		t.Errorf("the shortcut bar is still on screen under the note:\n%s", view)
	}
	if !strings.Contains(view, "3 of 4 reviewed") {
		t.Errorf("the note is nowhere in the frame:\n%s", view)
	}
	band := strings.Join(bandOf(t, m), "\n")
	if !strings.Contains(band, "3 of 4 reviewed") {
		t.Errorf("the note is not in the bottom band:\n%s", band)
	}
	assertFrameFits(t, m)
}

// The shortcut bar's height is a budget rather than the bar, so that the keys moving between the
// three regions cannot move the layout. A notification has to obey the same rule, or the next
// reviewed mark undoes the fix.
func TestNeitherTheKeysNorANoteMoveTheRowArea(t *testing.T) {
	m := navModel(t)
	m.width, m.height = 100, 30
	targets := []focusTarget{focusFiles, focusMeta, focusPreview}
	same := true
	for _, target := range targets {
		if len(wrapGroups(m.helpTextFor(target), m.width)) != len(wrapGroups(m.helpTextFor(targets[0]), m.width)) {
			same = false
		}
	}
	if same {
		t.Fatalf("the three shortcut bars are the same height at %d columns; this test needs a width "+
			"where they differ", m.width)
	}
	area, band := m.rowArea(), m.bandRows()

	for _, target := range targets {
		m.focus = target
		if got := m.rowArea(); got != area {
			t.Errorf("focus %d: the row area moved from %d to %d", target, area, got)
		}
		m.setStatus("2 reviewed marks set", false)
		if got := m.rowArea(); got != area {
			t.Errorf("focus %d: the row area moved from %d to %d with a note on screen", target, area, got)
		}
		if got := m.bandRows(); got != band {
			t.Errorf("focus %d: the band grew from %d rows to %d", target, band, got)
		}
		m.setStatus("", false)
	}
}

// A note says what the last key did and costs nothing to miss, so it retires on its own and the
// keys come back. The tick is armed once per message: a message that arrives on a timer — the
// drift check answers every three seconds — would otherwise re-arm it and the note would never go.
func TestNoteFadesAndArmsOnlyOneTick(t *testing.T) {
	m := navModel(t)
	m.setStatus("span main...current", false)

	cmd := m.armNotif()
	if cmd == nil {
		t.Fatal("a note armed no expiry")
	}
	if m.notifArmed != m.notifGen {
		t.Errorf("the tick was armed for generation %d, the note is %d", m.notifArmed, m.notifGen)
	}
	if again := m.armNotif(); again != nil {
		t.Error("the same note was armed twice")
	}

	msg := cmd()
	expire, ok := msg.(notifExpireMsg)
	if !ok {
		t.Fatalf("the expiry is a %T, want notifExpireMsg", msg)
	}
	got := send(t, m, expire)
	if got.status != "" {
		t.Errorf("the note survived its own expiry: %q", got.status)
	}
	if !strings.Contains(plainView(got), "q quit") {
		t.Errorf("the shortcut bar did not come back:\n%s", plainView(got))
	}

	// Esc is the early way out, and it works on a note as well as on a refusal.
	m.setStatus("span main...current", false)
	if cleared := press(m, tea.KeyEsc); cleared.status != "" {
		t.Errorf("esc left %q on screen", cleared.status)
	}
}

// A tick armed for an older message must not cut the current one short, or the second note gets
// the first one's clock.
func TestStaleExpiryLeavesTheCurrentNote(t *testing.T) {
	m := navModel(t)
	m.setStatus("first note", false)
	stale := m.notifGen
	m.setStatus("second note", false)

	got := send(t, m, notifExpireMsg{seq: stale})
	if got.status != "second note" {
		t.Errorf("a stale expiry cleared the note it was not armed for: %q", got.status)
	}
	got = send(t, got, notifExpireMsg{seq: got.notifGen})
	if got.status != "" {
		t.Errorf("the current expiry did not clear the note: %q", got.status)
	}
}

// A refusal explains why a key did nothing and names the key that will, so it waits: the reviewer
// has to read it before the next press makes sense. Notes are left alone until they fade, so
// moving through the list does not wipe out what the last key said.
func TestRefusalWaitsForTheNextKey(t *testing.T) {
	base := navModel(t)
	m := focusOnRow(t, base, indexOf(t, base, rowAbout))

	got := press(m, tea.KeySpace)
	note := got.status
	if note == "" || got.statusErr {
		t.Fatalf("space on ABOUT.md said %q (err=%v), want a refusal", note, got.statusErr)
	}

	held := send(t, got, notifExpireMsg{seq: got.notifGen})
	if held.status != note {
		t.Errorf("the clock retired a refusal: %q", held.status)
	}
	if held.statusKind != notifSticky {
		t.Errorf("a refusal is kind %d, want notifSticky", held.statusKind)
	}

	moved := pressRune(held, 'j')
	if moved.status != "" {
		t.Errorf("the next key left %q on screen; a refusal is acknowledged by pressing something", moved.status)
	}

	// Esc dismisses it without pressing a key that does something.
	again := press(held, tea.KeyEsc)
	if again.status != "" {
		t.Errorf("esc left %q on screen", again.status)
	}
}

func TestNoteIsNotDismissedByMoving(t *testing.T) {
	m := navModel(t)
	m.setStatus("refreshed probe bb0f343 → 43915ed", false)

	got := pressRune(m, 'j')
	if got.status != m.status {
		t.Errorf("moving through the list dropped the note: %q", got.status)
	}
	if !strings.Contains(plainView(got), "refreshed probe") {
		t.Errorf("the note is not on screen while the reviewer moves:\n%s", plainView(got))
	}
}

// The drift warning is not a message someone typed, so nothing they type can clear it: a note may
// borrow the band for its few seconds, and then the warning is back until `r` moves the pin.
func TestDriftReturnsWhenTheNoteThatCoveredItFades(t *testing.T) {
	m, f := driftModel(t)
	f.MustGit("update-ref", "refs/heads/probe", f.RevParse("HEAD~2"))
	m = runDriftCheck(t, m)

	if !strings.Contains(plainView(m), "probe moved") {
		t.Fatalf("no banner with nothing else to say:\n%s", plainView(m))
	}
	chrome, window := m.chromeRows(), m.rowArea()

	m.setStatus("2 reviewed marks set", false)
	view := plainView(m)
	if strings.Contains(view, "probe moved") {
		t.Errorf("the banner and the note are both on screen:\n%s", view)
	}

	got := send(t, m, notifExpireMsg{seq: m.notifGen})
	if !strings.Contains(plainView(got), "[r] refresh") {
		t.Errorf("the banner did not come back when the note faded:\n%s", plainView(got))
	}
	if got.chromeRows() != chrome || got.rowArea() != window {
		t.Errorf("the layout moved as the banner and the note traded places: chrome %d→%d, window %d→%d",
			chrome, got.chromeRows(), window, got.rowArea())
	}
}

// The band is the height the shortcut bar needs, which is more than any message this screen sends
// at a width a reviewer would use. When a message is nonetheless longer — a wide terminal and a
// long error — the band is cut rather than grown, because a frame taller than the terminal repaints
// by scrolling and loses the bar that says how to leave.
func TestNoteTooLongForTheBandIsCutNotStacked(t *testing.T) {
	m := navModel(t)
	m.width, m.height = 200, 24
	band, area := m.bandRows(), m.rowArea()

	m.setStatus(strings.Repeat("long word ", 200), false)

	if got := m.rowArea(); got != area {
		t.Errorf("a long note took rows from the list: %d → %d", area, got)
	}
	rows := bandOf(t, m)
	if len(rows) != band {
		t.Fatalf("the band grew to %d rows from %d: %q", len(rows), band, rows)
	}
	if !strings.HasSuffix(strings.TrimRight(rows[len(rows)-1], " "), "\u2026") {
		t.Errorf("the cut note does not say it was cut: %q", rows[len(rows)-1])
	}
	assertFrameFits(t, m)
}

// The thread prompt is not a notification: the line under the input says what the keys mean for as
// long as the prompt is up, so it is chrome and no clock retires it.
func TestPromptKeepsItsHintWhileTyping(t *testing.T) {
	m := navModel(t)
	m = pressRune(m, 't')
	if m.mode != modePrompt {
		t.Fatalf("t did not open the prompt (mode %d)", m.mode)
	}
	chrome := m.chromeRows()

	for _, r := range []rune("lock") {
		m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = send(t, m, notifExpireMsg{seq: m.notifGen})
	}
	m = press(m, tea.KeySpace)

	if m.status != "" {
		t.Errorf("the prompt hint is stored as a notification: %q", m.status)
	}
	if got := m.chromeRows(); got != chrome {
		t.Errorf("typing into the prompt changed the chrome from %d to %d rows", chrome, got)
	}
	band := strings.Join(bandOf(t, m), "\n")
	for _, want := range []string{"New thread: lock ", "Enter to create, Esc to cancel"} {
		if !strings.Contains(band, want) {
			t.Errorf("the prompt band lost %q:\n%s", want, band)
		}
	}
}

// Every mode draws the band at its own height, and every one of them has to be counted at that
// height by whatever windows the list: the preview overlay is the layout with the least slack.
func TestBandHeightIsWhatTheChromeCounts(t *testing.T) {
	for _, mode := range []mode{modeFiles, modeSubmit, modePrompt, modeSpan, modePreview} {
		m := navModel(t)
		m.mode = mode
		m.promptKind = promptThread
		if m.scroll != 0 {
			t.Fatalf("mode %d: the fixture scrolls, so chrome counts a hint row too", mode)
		}
		if got := m.chromeRows() - m.bandRows(); got != 7 {
			t.Errorf("mode %d: chrome counts %d rows outside the band, want 7", mode, got)
		}
		if got := m.overlayChrome() - m.bandRows(); got != 3 {
			t.Errorf("mode %d: the overlay counts %d rows outside the band, want 3", mode, got)
		}
	}
}
