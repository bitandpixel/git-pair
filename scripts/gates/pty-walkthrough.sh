#!/usr/bin/env bash
# Walk the review TUI through a real pty: keystrokes reach a running program, and what it paints
# is checked. This is the scripted version of the hand runs behind the span plan's M2/M3/M4 rows
# and behind the difftool handoff, none of which a Go test can reach — `review open` refuses to
# start without a terminal, and a ref moved by another process only moves while something is
# watching.
#
# Usage: bash scripts/gates/pty-walkthrough.sh [/path/to/git-pair]
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
ROOT=$(cd "$HERE/../.." && pwd)
G=${1:-$HOME/.local/bin/$(sh "$ROOT/scripts/install-name.sh" "$ROOT")}
if [ ! -x "$G" ]; then
  printf 'no binary at %s - run `mise run build` in %s first\n' "$G" "$ROOT" >&2
  exit 1
fi
DRIVER="$HERE/pty-tui.py"
PLAIN="$HERE/pty-plain.py"
T=$(mktemp -d /tmp/git-pair-pty.XXXXXX)
# A failed scenario is worth more kept than re-run: the transcripts are the only record of what the
# terminal actually received, and a check that fails once in twenty does not reproduce on demand.
trap 'if [ "${FAILED:-0}" = 1 ]; then echo "PTY: transcripts kept for inspection in $T"; else rm -rf "$T"; fi' EXIT
# The replayed commands get stdin from /dev/null. `git pair init` reads a pipe as piped `--about` content,
# so a harness that leaves stdin open would leave a fixture `init` blocked forever. The pty drivers build
# their own terminal for the program they run, so this does not touch what the TUI reads.
exec < /dev/null
trap 'rm -rf "$T"' EXIT
FAILED=0
COLS=100
ROWS=30

step()  { printf '\n\033[1m### %s\033[0m\n' "$1"; }
ok()    { printf '  ok: %s\n' "$1"; }
fail()  { printf '  FAIL: %s\n' "$1"; FAILED=1; }

# What painted is captured first and matched in bash, with a substring test. Neither half of that is
# stylistic. Piping the replay into `grep -q` under `pipefail` makes an assertion fail when it succeeds:
# `grep -q` exits at the first match, the writer's next call takes SIGPIPE, and the pipeline reports the
# signal instead of the match — which is how one run of this gate came out red on a screen whose own dump
# plainly contained the string. `refuse` is the worse half, because there a SIGPIPE is indistinguishable
# from the string being absent. Matching in bash removes the ordering entirely: nothing else is running
# while the answer is decided, so a pass and a fail cannot disagree about the same bytes.
painted() { python3 "$PLAIN" --after "$1" "$2"; }          # painted <window> <raw file>
paintedall() { python3 "$PLAIN" "$1"; }                    # paintedall <raw file>

# expect <description> <window> <raw file> <literal string>
expect() {
  out=$(painted "$2" "$3")
  if [[ "$out" == *"$4"* ]]; then ok "$1"; else
    fail "$1 — no '$4' in what painted after key $2"
    printf '%s\n' "$out" | head -14 | sed 's/^/      | /'
  fi
}
# refuse <description> <window> <raw file> <literal string>
refuse() {
  out=$(painted "$2" "$3")
  if [[ "$out" == *"$4"* ]]; then
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
# The character after a name is git's status. The changeset's own two files are new, so both carry `+`.
# src/service.ts was there before the span, so its row carries nothing.
expect "a file the span created carries a + after its name" -1 "$T/paint.raw" "ABOUT.md +"
refuse "a file the span only changed carries no sign" -1 "$T/paint.raw" "service.ts +"
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
# One timeline, not two lists: the changeset's own commit is a row of the same column, written
# between the submissions at the moment it happened -- here, after the submission it answered.
expect "and the changeset's own commit is a row of that column" 0 "$T/picker.raw" "serialise per business"
expect "the columns name the keys that open the drills" 0 "$T/picker.raw" "c commits"
expect "esc returns to the list" 1 "$T/picker.raw" "reviewed"
# The submission is a commit too, and an empty one. It is on the column by alias; a second row for
# it by sha would be one event wearing two names. Captured on its own, because the file list this
# session paints after `esc` has its own reasons to say the word "review".
session markers V,ctrl-c
refuse "the empty marker is no row beside the alias it already has" 0 "$T/markers.raw" "review: block"

# --- 3b. the drill's two modes ---------------------------------------------
step "drill: c opens it, the list has the keys, / asks for the filter, backspace cannot leave"
# `c` opens the commit drill for the active column -- a key rather than a row, so that `enter` means
# apply wherever the picker's cursor is resting. The list keeps the keys first: what a reviewer does
# in a drill is mostly move. `/` then puts what is typed into the filter -- the space in the middle is
# a filter character there and a command in the list -- `esc` hands the keys back with the filter kept,
# and `/` starts a fresh one. Then twenty backspaces run out of filter: the last several press against
# an empty one, and the drill has to survive them, so the session ends there with ctrl-c rather than
# with a key that would have closed it honestly.
# Each ~0.3 gives the terminal time to paint the frame that key produced: two keys sent inside one
# frame interval arrive as one update, and the frame in between is never written.
BACKS=$(python3 -c "print(','.join(['backspace']*20))")
session drill "V,~0.3,c,~0.3,/,s,e,r,i,a,l,i,s,e,space,p,e,r,~0.3,backspace,~0.3,esc,~0.3,/,x,~0.3,$BACKS,~0.3,x,~0.3,ctrl-c"
expect "the drill opens over the list" 2 "$T/drill.raw" "Pick Commit"
expect "the drill says which end the pick lands on" 2 "$T/drill.raw" "for BASE"
expect "the list's bar names the key that asks for the filter" 2 "$T/drill.raw" "/ filter"
expect "and names the paging keys without the modifier too" 2 "$T/drill.raw" "d/u ctrl-d/u half"
# Each window starts at the keystroke whose effect it wants to see, not at the wait after it: the
# frame a key paints belongs to that key, and the frames after it show the *next* state -- the filter
# one character shorter, the bar of the mode the reviewer has already left.
expect "what is typed becomes the filter, space included" 17 "$T/drill.raw" "filter: serialise per"
expect "backspace deletes one character" 19 "$T/drill.raw" "filter: serialise pe"
expect "esc hands the keys back to the list" 21 "$T/drill.raw" "esc/q back"
expect "with the filter it was given still on screen" 21 "$T/drill.raw" "filter: serialise pe"
expect "and / starts a fresh filter over the one kept" 24 "$T/drill.raw" "filter: x"
# An inert key paints nothing, so "still in the drill" has to be shown by a key that changes the
# screen without leaving it: one more letter, which the filter takes.
expect "twenty backspaces leave the drill open" 45 "$T/drill.raw" "Pick Commit"
expect "and the filter still takes what is typed" 47 "$T/drill.raw" "filter: x"

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
# ctrl-c ends this one: `q` closes the overlay now, and a scenario that wants the overlay *on screen*
# has to leave with the key that only ever exits.
( COLS=60 ROWS=14; session overlay p,ctrl-c )
expect "the overlay's shortcut bar is the overlay's own" 0 "$T/overlay.raw" "ctrl-f/b page"
expect "the overlay shows git's diff" 0 "$T/overlay.raw" "@@"
expect "the overlay carries the span the diff is measured against" 0 "$T/overlay.raw" "current"
refuse "the list's counter is off screen while the diff has the screen" 0 "$T/overlay.raw" "reviewed"

step "small terminal: esc closes the overlay and hands the list back"
( COLS=60 ROWS=14; session back p,esc,q )
expect "esc paints the list again" 1 "$T/back.raw" "reviewed"
expect "with the list's own shortcut bar, marks and all" 1 "$T/back.raw" "space reviewed"

step "q gives the list back over the overlay, and leaves from the pane"
# The two layouts are read differently on purpose. The overlay is nothing but the diff, so `q` closes it
# the way `esc` and `enter` do — a `q` that quit would end the session for a reviewer who wanted the list.
# In the pane the list is still drawn beside the diff, so `q` means leave as it means it everywhere else.
( COLS=60 ROWS=14; session qoverlay p,q,ctrl-c )
expect "q in the overlay paints the list again" 1 "$T/qoverlay.raw" "space reviewed"
expectbytes "and the session left only when ctrl-c said so" "$T/qoverlay.raw" $'\033[?1049l'
( COLS=140 ROWS=30; session qpane p,q )
expectbytes "q in the pane gave the terminal back" "$T/qpane.raw" $'\033[?1049l'
refuse "and the list's bar was never repainted under it" 1 "$T/qpane.raw" "space reviewed"

# `z` is the pane's own layout key: the same diff, the same keys and the same place in the file, over
# the whole screen. The two shapes are the two the terminal picks by itself, so the pair of presses is
# one reading gesture rather than two different screens with two different sets of keys.
step "z takes the pane to the whole screen, and gives the column back"
( COLS=140 ROWS=30; session zfull p,z,ctrl-c )
expect "z over the pane paints the overlay's own bar" 1 "$T/zfull.raw" "z pane"
refuse "and the pane's bar is gone with the pane" 1 "$T/zfull.raw" "z full"
( COLS=140 ROWS=30; session zback p,z,z,q )
expect "z again paints the list's counter again" 2 "$T/zback.raw" "reviewed"
expect "and the pane's bar is back with the column" 2 "$T/zback.raw" "z full"

# On the terminal too narrow for a column there is no column to give back, so `z` there gives the list and
# the keys back -- the same way out whether the screen was taken by `p` or by `z` itself. The bar names no
# way back here, because there is no column for a key to go back to.
step "z on a terminal with no column gives the list back"
( COLS=60 ROWS=14; session ztiny z,z,ctrl-c )
expect "z from the narrow list takes the whole screen" 0 "$T/ztiny.raw" "esc enter q back"
expect "and z gives the list and the keys back" 1 "$T/ztiny.raw" "reviewed"
refuse "with no bar offering a column this terminal has never had" 1 "$T/ztiny.raw" "z pane"

# `z` is the same request from the list column, where the row under the cursor says which diff: a
# reviewer who reads every diff at full width never moves into the pane. The second `z` is the way back,
# and it brings the keys back to the row they were pressed on.
step "z from the file list opens that file over the whole screen, and gives the list back"
# The same walk down the tree the difftool step makes: four downs is src/service.ts, past the
# changeset's own directory row and the two documents under it.
( COLS=140 ROWS=30; session zlist j,j,j,j,z,ctrl-c )
expect "z from the list paints the overlay's own bar" 4 "$T/zlist.raw" "z pane"
expect "and the diff of the row the cursor was on" 4 "$T/zlist.raw" "@@"
refuse "with the list's counter off screen" 4 "$T/zlist.raw" "reviewed"
( COLS=140 ROWS=30; session zlistback j,j,j,j,z,z,q )
expect "z again gives the list back, counter and all" 5 "$T/zlistback.raw" "reviewed"
expect "with the column the diff had taken" 5 "$T/zlistback.raw" "z full"

# The pane that reads a file as a file has to say which lines in it are the reviewer's, and it has no diff of
# the author's to draw them into the way a file row's pane has. ABOUT.md is a file this span created, so its
# pane is the file's own text, and the reviewer has changed one line of it without committing. One `j` is the walk from the changeset's own directory row down to ABOUT.md's row -- the walk
# the difftool step makes, stopping one row short of it. One line, not a rewrite: the whole edit has to fit
# in the rows the pane has, or the check below proves only that a row exists somewhere off screen.
step "text pane: the reviewer's uncommitted edit is drawn into the file, and marked"
cp changesets/booking-transaction/ABOUT.md "$T/about-span"
sed -i 's/^- business-scoped locking$/- business-scoped locking and a reviewer note/' changesets/booking-transaction/ABOUT.md
session youmarks j,q
expect "the line they typed is on screen" 0 "$T/youmarks.raw" "+- business-scoped locking and a reviewer note"
expect "the line it replaced is on screen too" 0 "$T/youmarks.raw" $'\u00d7- business-scoped locking'
expect "and each carries the marker that says whose it is" 0 "$T/youmarks.raw" $'\u2190 you'
expect "with the span's own text still the thing being read" 0 "$T/youmarks.raw" "  1 # booking-transaction"
refuse "no patch chrome arrived in the pane that reads a file" 0 "$T/youmarks.raw" "diff --git"
cp "$T/about-span" changesets/booking-transaction/ABOUT.md

# The same pane on a file nobody edited carries no marker. The counted note on the first screen is a Go
# test's at 140 columns: at the walkthrough's 100 the path ahead of it is long enough to clip the header.
session youclean j,j,q
refuse "an untouched file the pane reads as text carries no marker" 1 "$T/youclean.raw" $'\u2190 you'

# The same mark on the pane that reads a file as a patch, where the reviewer's rows are drawn into the author's
# at the numbers both diffs agree on. The marker goes on the reviewer's rows and nowhere else: a row of the
# author's is the author's work however green it looks, and the two are on one screen now rather than in two
# sections. src/service.ts is the file this span modifies, so its pane is git's patch, and the reviewer has
# typed one line into it without committing.
# Four `j` walk the rows the tree stops on -- the changeset's directory, its ABOUT.md and CHANGESET.yaml,
# and src/ -- down to service.ts's row, and `z` paints the pane on its own: the frame the checks below read.
# The keystroke that lands the cursor does not wait for git's answer, and a pane still fetching is a pane
# that paints nothing.
step "diff pane: the reviewer's rows are marked, and what names the file is gone"
printf '  // a note the reviewer typed\n' >> src/service.ts
session youdiff j,j,j,j,z,z,q
# The header, not the tree: the tree names the path too, and the two spaces before the sign are the pane's
# own line. Without it these checks could be a directory's combined patch painted by a cursor that stopped
# one row short.
expect "the pane is on the file's own row" 3 "$T/youdiff.raw" "src/service.ts  +2"
expect "the header counts the reviewer's typing beside the file" 3 "$T/youdiff.raw" "you edited it"
expect "the line they typed is on screen" 3 "$T/youdiff.raw" "+  // a note the reviewer typed"
expect "and the row it typed carries the mark" 3 "$T/youdiff.raw" "+  // a note the reviewer typed  "$'\u2190 you'
# How many times the mark painted is not a check this capture can make -- it holds repaints, so one row on
# screen is the mark twice in the stream. Absence is: a context line of the file carries no mark, because the
# reviewer did not write it.
refuse "a context line is marked as theirs" 3 "$T/youdiff.raw" "  serialised per business now  "$'\u2190 you'
# The two diffs are one body now: the author's line and the reviewer's sit at the numbers the file has them,
# rather than in a section each.
expect "with the author's line the reviewer's sits in the same body" 3 "$T/youdiff.raw" "serialised per business now"
# On a file's pane the rows git printed about which file this is come out, because the header has named it
# twice. That absence is the Go test's to prove rather than this window's: the frames here include the directory
# pane the cursor walked through on the way down, and a directory's patch has every right to say which file it
# is about. What this capture can say is that the hunk header -- the one row naming which lines of the file are
# off screen -- is there.
expect "the hunk header is still there" 3 "$T/youdiff.raw" "@@ "
# Colour is git's here -- `git diff --color=always` -- and the pane puts its own on the reviewer's rows alone.
# The claim is that the reviewer's row reaches this terminal in a colour that is not git's, in a capture where
# git's own colours do appear. Which codes carry them is lipgloss's and git's business: lipgloss writes ANSI 12
# as `94` on a terminal advertising sixteen colours and `38;5;12` on one advertising more, and it drops colour
# entirely when it believes there is no terminal -- which is why this is a pty check and not a Go one.
#
# It is also why the harness pins the environment and not only the window size: `pty-tui.py` removes `CI` and
# `NO_COLOR` for the program it launches, because termenv treats any non-empty `CI` as not-a-terminal before
# it looks at the descriptor, and then there is no colour to compare. That removal is part of the condition
# this assertion needs, so a future failure is worth reading against the driver before this check is relaxed.
python3 - "$T/youdiff.raw" <<'PY' \
  || fail "the reviewer's row does not reach the terminal in a colour of the pane's own"
import re, sys

raw = open(sys.argv[1], "rb").read().decode("utf-8", "replace")
SGR = re.compile(r"\x1b\[([0-9;]*)m")
# git's own diff palette: new, old, and the hunk headers between them.
GIT = {"32", "92", "31", "91", "36", "96"}


def foregrounds(where):
    """The codes setting a foreground colour in `where`.

    For a row, `where` is the bytes just in front of it rather than the line it lands on: the TUI repaints
    differentially, so one stretch of the capture between two newlines can hold the tail of one screen row and
    the head of the next, and a code read from that would belong to a row the needle is not in.
    """
    found = set()
    for code in SGR.findall(where):
        parts = [int(p) for p in code.split(";") if p != ""]
        if any(30 <= p <= 37 or 90 <= p <= 97 for p in parts) or 38 in parts:
            found.add(code)
    return found


yours = set()
at = raw.find("+  // a note the reviewer typed")
while at != -1:
    yours |= foregrounds(raw[max(0, at - 40) : at])
    at = raw.find("+  // a note the reviewer typed", at + 1)
git_here = foregrounds(raw) & GIT
ok = bool(yours) and bool(git_here) and not (yours & GIT)
if not ok:
    # Which of the three conditions failed is the question, and answering it by hand cost a CI round
    # trip and a local bisect: an empty pane set is a profile with no colour in it, an empty git set is
    # git's palette absent from the capture, and an overlap is the 40-byte window reading a neighbouring
    # row's code rather than this one's.
    print("    colour check: pane=%s git=%s overlap=%s" % (sorted(yours), sorted(git_here), sorted(yours & GIT)))
sys.exit(0 if ok else 1)
PY
git checkout -- src/service.ts

# Too small for even that is worth saying out loud, and with the smaller of the two asks -- 12 rows
# would have been enough, so telling the reviewer about the pane's 16 would send them growing the
# wrong window.
step "too small for even the overlay: it names the width it needs"
( COLS=30 ROWS=24; session tiny p,q )   # rows for the status line: the bar wraps to six here
expect "the refusal names the columns the overlay needs" 0 "$T/tiny.raw" "the preview wants 40 columns"
expect "and the terminal it has" 0 "$T/tiny.raw" "this terminal has 30"

# `change tidy` is the command this release added for moving a landing's directory out of the way, and it
# writes a commit. The property PRD §22 cares about is that the CLI is the agent surface, so a command that
# writes must not become a conversation because it found a terminal: sent no keystrokes, with a timeout, the
# run has to finish on its own and paint its answer. Its own repository, because it commits and the fixture
# above has no business holding a landing.
step "a command that writes a commit asks for nothing at a terminal"
CR="$T/tidyrepo"
git init -q -b main "$CR"
git -C "$CR" config user.email p@example.com
git -C "$CR" config user.name Painter
git -C "$CR" config commit.gpgsign false
printf 'one\n' > "$CR/file.txt"
git -C "$CR" add -A && git -C "$CR" commit -qm "start"
git -C "$CR" checkout -qb tidied
mkdir -p "$CR/changesets/tidied"
printf 'base: main\n' > "$CR/changesets/tidied/CHANGESET.yaml"
printf 'Summary: work that lands, then gets out of the way.\n' > "$CR/changesets/tidied/ABOUT.md"
printf 'tidied\n' > "$CR/tidied.md"
git -C "$CR" add -A && git -C "$CR" commit -qm "tidied: the work"
git -C "$CR" switch -q main
git -C "$CR" merge -q --no-ff -m "tidied: land it" tidied

python3 "$DRIVER" --raw "$T/tidy.raw" --settle 1 --timeout 20 --term "${PTY_TERM:-xterm-256color}" \
  "$COLS" "$ROWS" "$CR" "" "$G" change tidy tidied >"$T/tidy.exit" 2>"$T/tidy.err"
case $? in
  0) ok "the tidy finished with nobody typing" ;;
  124) fail "the tidy is waiting at a prompt; no command in git-pair asks" ;;
  *) fail "the tidy exited $?"; [ -s "$T/tidy.err" ] && sed 's/^/      ! /' "$T/tidy.err" ;;
esac
# Nothing was typed, so there is no keystroke marker to window on: this is the whole session's output.
expectall() { # expectall <description> <raw file> <literal string>
  out=$(paintedall "$2")
  if [[ "$out" == *"$3"* ]]; then ok "$1"; else
    fail "$1 — no '$3' in what painted"; printf '%s\n' "$out" | head -14 | sed 's/^/      | /'
  fi
}
expectall "it names the move it made" "$T/tidy.raw" "changesets/tidied -> changesets/.landed/tidied"
expectall "and the commit it wrote" "$T/tidy.raw" "committed"
if git -C "$CR" rev-parse --quiet --verify HEAD:changesets/.landed/tidied/CHANGESET.yaml >/dev/null; then
  ok "and the move it painted is the move it committed"
else
  fail "it painted a move the repository does not hold"
fi
if git -C "$CR" diff --name-status HEAD~1 HEAD | grep -qv '^R'; then
  fail "the commit the terminal painted carries more than renames: $(git -C "$CR" diff --name-status HEAD~1 HEAD)"
else
  ok "and it is a move a reviewer reads as one"
fi
# The same invocation through a pipe, on the state the pty run left: with nothing left to move it says so,
# with no terminal under it. Same command, two worlds, one answer — which is what "consent is an argument"
# has to mean for a pipeline.
out=$(cd "$CR" && "$G" change tidy tidied 2>&1)
printf '%s' "$out" | grep -qF -- "already tidied" \
  && ok "through a pipe it reports there is nothing left to move" \
  || fail "the piped run did not report idempotence: $out"

# --- verdict ---------------------------------------------------------------
printf '\n'
if [ "$FAILED" = 0 ]; then echo "PTY: all checks passed"; else echo "PTY: FAILURES PRESENT"; fi
exit "$FAILED"
