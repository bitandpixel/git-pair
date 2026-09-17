#!/usr/bin/env bash
# Walk the review TUI through a real pty: keystrokes reach a running program, and what it paints
# is checked. This is the scripted version of the hand runs behind the span plan's M2/M3/M4 rows
# and behind the difftool handoff, none of which a Go test can reach — `review open` refuses to
# start without a terminal, and a ref moved by another process only moves while something is
# watching.
#
# Usage: bash docs/plans/completed/gitpr-mvp/artifacts/pty-walkthrough.sh [/path/to/git-pair]
#        (default: the name `mise run build` installs from this repository — the shared
#        ~/.local/bin/git-pair on trunk, a branch-namespaced one anywhere else)
#
# Prints "PTY: all checks passed" when every scenario painted what it should. It builds its own
# repository in a temp directory, so it never touches the one you are standing in.
set -uo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
# The default comes from this script's own path, not the working directory, and from the same
# rule `mise run build` uses. This walkthrough paints the TUI and checks what came out, so the
# binary under test has to be the one built from *this* repository — the shared name would
# paint whatever another worktree last installed, and the checks would pass or fail on it.
ROOT=$(cd "$HERE/../../../../.." && pwd)
G=${1:-$HOME/.local/bin/$(sh "$ROOT/scripts/install-name.sh" "$ROOT")}
if [ ! -x "$G" ]; then
  printf 'no binary at %s - run `mise run build` in %s first\n' "$G" "$ROOT" >&2
  exit 1
fi
DRIVER="$HERE/pty-tui.py"
PLAIN="$HERE/pty-plain.py"
T=$(mktemp -d /tmp/git-pair-pty.XXXXXX)
trap 'rm -rf "$T"' EXIT
FAILED=0
COLS=100
ROWS=30

step()  { printf '\n\033[1m### %s\033[0m\n' "$1"; }
ok()    { printf '  ok: %s\n' "$1"; }
fail()  { printf '  FAIL: %s\n' "$1"; FAILED=1; }

# expect <description> <window> <raw file> <literal string>
expect() {
  if python3 "$PLAIN" --after "$2" "$3" | grep -qF -- "$4"; then ok "$1"; else
    fail "$1 — no '$4' in what painted after key $2"
    python3 "$PLAIN" --after "$2" "$3" --head 14 | sed 's/^/      | /'
  fi
}
# refuse <description> <window> <raw file> <literal string>
refuse() {
  if python3 "$PLAIN" --after "$2" "$3" | grep -qF -- "$4"; then
    fail "$1 — '$4' appeared after key $2 and should not have"
  else ok "$1"; fi
}
# expectfile <description> <path>
expectfile() {
  if [ -e "$2" ]; then ok "$1"; else fail "$1 — $2 was never created"; fi
}
# expectbytes <description> <raw file> <literal bytes> — for escape sequences, which pty-plain
# removes on purpose. Alt-screen entry and leave are the whole claim of one scenario.
expectbytes() {
  if grep -aqF -- "$3" "$2"; then ok "$1"; else fail "$1 — $(printf '%q' "$3") never reached the terminal"; fi
}

# session <name> <keys> [span flags...] — one `git pair review open` under a pty, captured to
# $T/<name>.raw. The subcommand is part of the helper so a scenario reads as the keys and the span
# it chose, which is the only thing that differs between them.
session() {
  local name=$1 keys=$2; shift 2
  python3 "$DRIVER" --raw "$T/$name.raw" --settle 1 --timeout 30 --term "${PTY_TERM:-xterm-256color}" \
    "$COLS" "$ROWS" "$R" "$keys" "$G" review open "$@" >"$T/$name.exit" 2>"$T/$name.err"
  local code=$?
  case $code in
    0) ok "$name exited on its own" ;;
    124) fail "$name never quit; the harness killed it" ;;
    *) fail "$name exited $code" ;;
  esac
  [ -s "$T/$name.err" ] && sed 's/^/      ! /' "$T/$name.err"
  return 0
}

# --- the repository under review -------------------------------------------
step "fixture: a changeset with a review in it and work after it"
R=$T/repo
mkdir -p "$R/src"
cd "$R" || exit 1
git init -q -b main .
git config user.email reviewer@example.com
git config user.name Reviewer
git config commit.gpgsign false
git config core.pager cat          # a pager in the capture would hide the screen behind it
printf 'func createOffering() {\n\to := load()\n\treturn o\n}\n' > src/service.ts
git add -A && git commit -qm "initial implementation"

git switch -qc booking-transaction
"$G" change init --base main >/dev/null || { echo "fixture: change init failed"; exit 1; }
printf '# booking-transaction\n\n## Summary\n\nTransactional locking around offering creation.\n\n## What changed\n\n- business-scoped locking\n\n## Design decisions\n\nAdmin scheduling serializes at business level.\n\n## Validation\n\n- unit tests\n\n## Known limitations\n\n## Open questions\n' > changesets/booking-transaction/ABOUT.md
git add -A && git commit -qm "implement transactional locking"
"$G" change ready >/dev/null

# The reviewer's own additions, then a blocking submission: this is the state a reviewer
# reopens, and it is what makes `--head-review=-1` a span with content in it.
printf '  // What happens if these execute concurrently?\n' >> src/service.ts
"$G" review submit --block -m "concurrency is not covered" >/dev/null || { echo "fixture: submit failed"; exit 1; }
printf '  // serialised per business now\n' >> src/service.ts
git add -A && git commit -qm "author: serialise per business"
# A branch a span can pin by name, which one scenario then moves from under the session.
git branch probe HEAD~1
ok "fixture built at $R"

# --- 1. first paint ---------------------------------------------------------
step "first paint: a real terminal, a real keystroke"
session paint q
expect "the reviewed counter is on screen" -1 "$T/paint.raw" "reviewed"
expect "the shortcut bar offers the span picker" -1 "$T/paint.raw" "V picker"
expect "the shortcut bar offers quit" -1 "$T/paint.raw" "q quit"

# --- 2. a historical span is read-only (span plan M2) ----------------------
step "historical span: read-only, and it says so rather than eating keystrokes"
session hist s,q --head-review=-1
expect "the counter's slot carries the mode" -1 "$T/hist.raw" "HISTORICAL"
expect "the counter's slot says read only" -1 "$T/hist.raw" "READ ONLY"
expect "s refuses and explains the span it ends at" 0 "$T/hist.raw" "read-only: this span ends at"
refuse "no submission was made from a historical span" 0 "$T/hist.raw" "Review submitted"

# --- 3. the span picker (span plan M3) --------------------------------------
step "picker: V opens it, and it offers spans rather than a DAG"
session picker V,esc,q
expect "the picker lists the live end as an endpoint" 0 "$T/picker.raw" "Current"
expect "the picker names the newest review" 0 "$T/picker.raw" "Last Review"
expect "the picker offers the typed-commit drill" 0 "$T/picker.raw" "Commit"
expect "esc returns to the list" 1 "$T/picker.raw" "reviewed"

# --- 3b. typing into a drill (the drill's two modes) -----------------------
step "drill: typing filters, Tab moves, space is a character, backspace cannot leave"
# The picker opens on Changeset Base, so one j lands on Commit… and space opens the drill. The
# filter is then typed one letter at a time -- the space in it is a filter character here and a
# command in the list -- then Tab hands the keys to the list, one backspace deletes a character,
# Tab hands them back, and twenty more backspaces run out of filter. Twenty is the point: the last
# several press against an empty filter, and the drill has to survive them, so the session ends
# there with ctrl-c rather than with a key that would have closed it honestly.
# Each ~0.3 gives the terminal time to paint the frame that key produced: two keys sent inside one
# frame interval arrive as one update, and the frame in between is never written.
BACKS=$(python3 -c "print(','.join(['backspace']*20))")
session drill "V,j,space,~0.3,s,e,r,i,a,l,i,s,e,space,p,e,r,~0.3,tab,~0.3,backspace,~0.3,tab,~0.3,$BACKS,~0.3,x,~0.3,ctrl-c"
expect "the drill opens over the list" 2 "$T/drill.raw" "Pick Commit"
expect "the drill says which end the pick lands on" 2 "$T/drill.raw" "for BASE"
expect "the typing bar names the key that navigates" 2 "$T/drill.raw" "tab navigate"
expect "what is typed becomes the filter, space included" 16 "$T/drill.raw" "filter: serialise per"
expect "Tab hands the keys to the list" 18 "$T/drill.raw" "tab filter"
expect "backspace deletes one character" 20 "$T/drill.raw" "filter: serialise pe"
# An inert key paints nothing, so "still in the drill" has to be shown by a key that changes the
# screen without leaving it: one more letter, which the filter takes.
expect "twenty backspaces leave the drill open" 45 "$T/drill.raw" "Pick Commit"
expect "and the filter still takes what is typed" 45 "$T/drill.raw" "filter: x"

# --- 4. v walks the spans this session has been in (span plan M3b) ---------
step "span ring: v steps to the next span the session knows"
# A ref-pinned base makes three stops: the span opened on, the full changeset, the unreviewed
# span. Two stops would print the label without a position, which is not what is being checked.
session ring v,q --base-ref=probe
expect "v reports the span it stepped to" 0 "$T/ring.raw" "span "
expect "v reports the position in the ring" 0 "$T/ring.raw" "(2 of"

# --- 5. a mark survives stepping away and back (the mark round trip) -------
step "marks: space marks a file, v away and back brings the mark to the counter"
session marks space,v,v,q
expect "the mark shows in the counter" 0 "$T/marks.raw" "1 /"
expect "the counter is back where it was after v v" 2 "$T/marks.raw" "1 /"

# --- 6. a ref moves while the session is open (span plan M4) ---------------
step "drift: another process moves the ref, the screen warns and r re-pins"
session drift "!git update-ref refs/heads/probe HEAD,~3.5,r,q" --base-ref=probe
expect "the banner names the ref that moved" 1 "$T/drift.raw" $'\u26a0 probe moved'
expect "the banner names the key that fixes it" 1 "$T/drift.raw" "[r] refresh"
expect "r re-pins and reports it" 2 "$T/drift.raw" "refreshed probe"

# --- 7. the difftool handoff keeps the screen ------------------------------
step "handoff: enter runs the configured difftool, and the alt screen comes back"
git config diff.tool marker
git config difftool.marker.cmd "touch $T/tool-ran"
session tool enter,q
expectfile "the difftool actually ran" "$T/tool-ran"
expectbytes "the session took an alternate screen" "$T/tool.raw" $'\033[?1049h'
expectbytes "the session left the alternate screen on quit" "$T/tool.raw" $'\033[?1049l'

# --- 8. a terminal the renderer does not know ------------------------------
# What this asserts is hygiene, not usability: with TERM=dumb nothing paints (bubbletea has no
# colour capability to build a frame from), so the session is harmless and useless. It quits
# cleanly and gives the terminal back, which is the half the MVP plan claimed and the only half
# worth locking in. A refusal naming the terminal would beat a blank screen; that is a product
# decision, not a regression, so it is not asserted here.
step "TERM=dumb: nothing renders, and nothing leaks"
PTY_TERM=dumb session dumb q
unset PTY_TERM
expectbytes "the session took the alternate screen" "$T/dumb.raw" $'\033[?1049h'
expectbytes "it gave the terminal back on quit" "$T/dumb.raw" $'\033[?1049l'

# --- 9. a terminal too narrow for the pane: the overlay --------------------
# The pane is a wide-terminal thing. Below it `p` gives the diff the whole screen, because the
# alternative is squeezing the list into a column nobody can read. A 60x14 terminal is the shape of
# a half-width window on a laptop, which is where this layout earns its keep.
step "small terminal: p takes the screen with the diff, and gives it back"
# ctrl-c ends this one rather than q: q is the overlay's close key here, so a scenario that wants to
# see the overlay *on screen* has to leave with the one key that always means the exit.
( COLS=60 ROWS=14; session overlay p,ctrl-c )
expect "the overlay's shortcut bar is the overlay's own" 0 "$T/overlay.raw" "ctrl-f/b page"
expect "the overlay shows git's diff" 0 "$T/overlay.raw" "@@"
expect "the overlay carries the span the diff is measured against" 0 "$T/overlay.raw" "current"
refuse "the list's counter is off screen while the diff has the screen" 0 "$T/overlay.raw" "reviewed"

step "small terminal: q closes the overlay and hands the list back"
( COLS=60 ROWS=14; session back p,q,q )
expect "q paints the list again" 1 "$T/back.raw" "reviewed"
expect "with the list's own shortcut bar, marks and all" 1 "$T/back.raw" "space reviewed"

# Too small for even that is worth saying out loud, and with the smaller of the two asks -- 12 rows
# would have been enough, so telling the reviewer about the pane's 16 would send them growing the
# wrong window.
step "too small for even the overlay: it names the width it needs"
( COLS=30 ROWS=24; session tiny p,q )   # rows for the status line: the bar wraps to six here
expect "the refusal names the columns the overlay needs" 0 "$T/tiny.raw" "the preview wants 40 columns"
expect "and the terminal it has" 0 "$T/tiny.raw" "this terminal has 30"

# --- verdict ---------------------------------------------------------------
printf '\n'
if [ "$FAILED" = 0 ]; then echo "PTY: all checks passed"; else echo "PTY: FAILURES PRESENT"; fi
exit "$FAILED"
