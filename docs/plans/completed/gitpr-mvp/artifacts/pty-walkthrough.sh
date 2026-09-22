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
"$G" init --base main >/dev/null || { echo "fixture: init failed"; exit 1; }
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
expect "the changeset box names the base over the tree" -1 "$T/paint.raw" "base  main"
expect "the span is a row of the box, not a caption" -1 "$T/paint.raw" "span  main...current"
expect "the file tree is below it" -1 "$T/paint.raw" "changesets/booking-transaction/"
expect "the shortcut bar offers the span picker" -1 "$T/paint.raw" "V picker"
expect "the shortcut bar offers quit" -1 "$T/paint.raw" "q quit"
# The box is closed on all four sides in a real terminal, including the side nearest the diff column.
expect "the changeset box closes on its right" -1 "$T/paint.raw" $'\u256e'
expect "and a child sits deeper than the directory over it" -1 "$T/paint.raw" $'    \u25cb'
expect "with the directory's own mark and arrow above it" -1 "$T/paint.raw" $'\u25be \u25cb'
# The tree's own rules, double because the tree holds the keys at first paint. Nothing else on this
# screen draws a run of double rules yet: the box is idle and the divider is single.
expect "the file tree is ruled where it holds the keys" -1 "$T/paint.raw" $'\u2550\u2550\u2550\u2550'
# The box grew a right side without moving the divider: the diff column still starts one space and one
# rule to the right of the list.
expect "and the diff column still starts at its own rule" -1 "$T/paint.raw" $'\u2502 changesets' 

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
step "marks: space marks the first row, v away and back brings the marks to the counter"
# The first row is a directory, and a directory's space is its whole subtree's: the changeset
# directory holds ABOUT.md and CHANGESET.yaml, both of which this span changed, so one press marks two
# of the three files and the counter says so. The claim being checked is that the counter still says it
# after the span has been stepped away from and back.
session marks space,v,v,q
expect "the marks show in the counter" 0 "$T/marks.raw" "2 / 3 reviewed"
expect "the counter is back where it was after v v" 2 "$T/marks.raw" "2 / 3 reviewed"

# --- 5b. the changeset box is a region with keys of its own ----------------
# The screen has two regions where the keys can be, and a real terminal is what shows whether the
# split reads: the box's borders are its focus light, its bar is its own, and the file tree under it
# keeps its rows while the box is being read.
step "the changeset box: a takes the keys, and the borders say so"
# `a` is the jump that names a row of the box from wherever the keys are in the column, and it takes the
# keys with it. `tab` no longer reaches the box from the tree — the two halves of the list column share
# their keys and Tab walks the column and the diff — so the way in is the jump, and the bar is what says so.
session box a,q
expect "the box's borders turn to a double rule" 0 "$T/box.raw" $'\u2554'
expect "the box's bar names the key its own" 0 "$T/box.raw" "space span"
expect "the box's bar names the key that goes back" 0 "$T/box.raw" "f files"
refuse "the tree's fold keys are off the bar while the box has them" 0 "$T/box.raw" "h/l fold"

step "the changeset box: tab out of the diff comes back to the box"
# The ring is the list column and the diff, so from the box one tab lands in the diff and the next returns
# the keys to the half that handed them over — the box, not the tree. That memory is the claim here.
session ring2 a,tab,tab,q
expect "the keys come back to the box" 2 "$T/ring2.raw" $'\u2554'
# The bar of the region that holds the keys, and not the tree's: `space span` belongs to the box alone
# (the tree's row of the same key reads `space reviewed`). `T new thread` is in both bars, and at this
# width it wraps onto the second bar line anyway, so it would prove nothing about which region has them.
expect "with the box's own shortcut bar" 2 "$T/ring2.raw" "space span"

step "the changeset box: space on the span row opens the picker"
# The jump lands on ABOUT.md, so the scenario walks up to the span row before pressing it.
session spanrow a,k,space,esc,q
expect "the span row opens the span picker" 2 "$T/spanrow.raw" "Span picker"
expect "and esc comes back to the list" 3 "$T/spanrow.raw" "reviewed"

step "the changeset box: space there reads a document rather than marking one"
# A repaint-only terminal makes "the tree is still there" an awkward thing to grep, so this checks the
# claim the region boundary is really about: with the box holding the keys, the key that marks a file
# marks nothing, and says which rows it does mark.
session boxspace a,space,q
expect "space in the box says what it marks instead" 1 "$T/boxspace.raw" "file rows"

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
# Four downs is the walk down the tree to src/service.ts: the cursor starts on the changeset's
# directory row, whose Enter folds, and ABOUT.md two rows down is a document the changeset invented,
# which opens in the editor rather than the difftool. A code file in the span is the row whose Enter
# is the reviewer's path to the tool — and the row that has to be reached by counting, since the
# harness types keys rather than pointing at rows.
session tool j,j,j,j,enter,q
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
step "small terminal: p takes the screen with the diff"
# ctrl-c ends this one: q would quit the session (it does that on this screen now, like every other),
# and a scenario that wants the overlay *on screen* has to leave with the key that only ever exits.
( COLS=60 ROWS=14; session overlay p,ctrl-c )
expect "the overlay's shortcut bar is the overlay's own" 0 "$T/overlay.raw" "ctrl-f/b page"
expect "the overlay shows git's diff" 0 "$T/overlay.raw" "@@"
expect "the overlay carries the span the diff is measured against" 0 "$T/overlay.raw" "current"
refuse "the list's counter is off screen while the diff has the screen" 0 "$T/overlay.raw" "reviewed"

step "small terminal: esc closes the overlay and hands the list back"
( COLS=60 ROWS=14; session back p,esc,q )
expect "esc paints the list again" 1 "$T/back.raw" "reviewed"
expect "with the list's own shortcut bar, marks and all" 1 "$T/back.raw" "space reviewed"

step "q quits from the diff, in either layout"
# `q` used to mean "close the preview" where the preview had the keys, which put a second meaning on the
# one key a reviewer never has to think about. It leaves the program now, from the overlay and from the pane.
( COLS=60 ROWS=14; session qoverlay p,q )
expectbytes "q in the overlay gave the terminal back" "$T/qoverlay.raw" $'\033[?1049l'
refuse "and the list was never repainted under it" 1 "$T/qoverlay.raw" "space reviewed"
( COLS=140 ROWS=30; session qpane p,q )
expectbytes "q in the pane gave the terminal back" "$T/qpane.raw" $'\033[?1049l'
refuse "and the list's bar was never repainted under it" 1 "$T/qpane.raw" "space reviewed"

# Too small for even that is worth saying out loud, and with the smaller of the two asks -- 12 rows
# would have been enough, so telling the reviewer about the pane's 16 would send them growing the
# wrong window.
step "too small for even the overlay: it names the width it needs"
( COLS=30 ROWS=24; session tiny p,q )   # rows for the status line: the bar wraps to six here
expect "the refusal names the columns the overlay needs" 0 "$T/tiny.raw" "the preview wants 40 columns"
expect "and the terminal it has" 0 "$T/tiny.raw" "this terminal has 30"

# `--configure-fetch` is consent as an argument, and the property worth painting is that it stays one:
# PRD §22 makes the CLI the agent surface, so a command that writes configuration must not become a
# conversation because it found a terminal. Sent no keystrokes, with a timeout, the run has to finish on
# its own and paint its answer. Its own repository, because the record needs a verdict the TUI fixture
# above has no reason to have left in place.
step "a command that writes config asks for nothing at a terminal"
CR="$T/configrepo"
git init -q -b main "$CR"
git -C "$CR" config user.email p@example.com
git -C "$CR" config user.name Painter
git -C "$CR" config commit.gpgsign false
printf 'one\n' > "$CR/file.txt"
git -C "$CR" add -A && git -C "$CR" commit -qm "start"
git -C "$CR" switch -qc cfg-branch
(cd "$CR" && "$G" init --base main >/dev/null) && ok "the record fixture has a changeset" || fail "fixture: init"
printf 'two\n' > "$CR/file.txt"
git -C "$CR" add -A && git -C "$CR" commit -qm "implement"
(cd "$CR" && "$G" change ready >/dev/null) && ok "and is offered" || fail "fixture: change ready"
(cd "$CR" && "$G" review submit --approve >/dev/null) && ok "and approved" || fail "fixture: review submit --approve"
(cd "$CR" && git switch -q main && git merge -q --no-ff --no-edit cfg-branch)
SRC=$(git -C "$CR" rev-parse cfg-branch)
LAND=$(git -C "$CR" rev-parse HEAD)
git init -q --bare -b main "$T/config-remote.git"
(cd "$CR" && git remote add origin "$T/config-remote.git" && git push -q origin --all)

python3 "$DRIVER" --raw "$T/configure.raw" --settle 1 --timeout 20 --term "${PTY_TERM:-xterm-256color}" \
  "$COLS" "$ROWS" "$CR" "" "$G" integration record --source "$SRC" --commit "$LAND" \
  --target main --configure-fetch >"$T/configure.exit" 2>"$T/configure.err"
case $? in
  0) ok "the record finished with nobody typing" ;;
  124) fail "the record is waiting at a prompt; no command in git-pair asks" ;;
  *) fail "the record exited $?" ;;
esac
[ -s "$T/configure.err" ] && sed 's/^/      ! /' "$T/configure.err"
# Nothing was typed, so there is no keystroke marker to window on: this is the whole session's output.
expectall() { # expectall <description> <raw file> <literal string>
  if python3 "$PLAIN" "$2" | grep -qF -- "$3"; then ok "$1"; else
    fail "$1 — no '$3' in what painted"; python3 "$PLAIN" --head 14 "$2" | sed 's/^/      | /'
  fi
}
expectall "it names the key it wrote" "$T/configure.raw" "remote.origin.fetch"
expectall "and the refspec, not a paraphrase of it" "$T/configure.raw" "+refs/git-pair/*"
expectall "and says what comes next" "$T/configure.raw" "git pair integration publish"
if git -C "$CR" config --local --get-all remote.origin.fetch | grep -qF -- \
     '+refs/git-pair/*:refs/remotes/origin/refs/git-pair/*'; then
  ok "and the config file really holds it"
else
  fail "it painted a config line it did not write"
fi
# The same invocation through a pipe, on the state the pty run left: it must reach the same conclusion
# without a terminal under it, and write the line once rather than again. Same command, two worlds, one
# answer — which is what "consent is an argument" has to mean for a pipeline.
out=$(cd "$CR" && "$G" integration record --source "$SRC" --commit "$LAND" --target main --configure-fetch 2>&1)
printf '%s' "$out" | grep -qF -- "already fetches the durable mirrors" \
  && ok "through a pipe it reports that it wrote nothing" \
  || { fail "the piped run did not report idempotence: $out"; }
count=$(git -C "$CR" config --local --get-all remote.origin.fetch | grep -cF -- '+refs/git-pair/*')
[ "$count" = 1 ] && ok "and the refspec is in the config exactly once" \
  || fail "the refspec appears $count times; the write is meant to be idempotent"

# --- verdict ---------------------------------------------------------------
printf '\n'
if [ "$FAILED" = 0 ]; then echo "PTY: all checks passed"; else echo "PTY: FAILURES PRESENT"; fi
exit "$FAILED"
