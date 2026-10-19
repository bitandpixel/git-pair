# fix-pty-harness-settles-before-keys

## Summary

The pty walkthrough typed its next keystroke on a timer, so a keystroke sent while the program was
still drawing the previous one sat in the terminal's buffer and came back folded into the same read —
two keys arriving as one event, which is the failure the comma in a key list already exists to
prevent, arriving from the other side. The driver now waits for the program to go quiet before it
types again, and the gate carries a probe that can tell the two harnesses apart.

## What changed

`scripts/gates/pty-tui.py` gained `--steady S`: after the fixed `--gap` window for a keystroke
closes, it keeps reading until the output has been quiet for `S` seconds before the next keystroke is
typed, with `--settle` as the ceiling. `scripts/gates/pty-walkthrough.sh` sets the value once
(`PTY_STEADY`, 0.15 by default) and passes it at every driver call, so the number that decides
whether a keystroke arrives alone is visible from the scenario side.

The wait does not weaken any assertion. `--gap` still fixes the window a keystroke opens, which is
what a `refuse` check needs, and the new wait reads into the same capture — it can only lengthen the
window it follows, so a `refuse` check gets more bytes to judge, never fewer.

The pin is a step beside the existing subshell self-test. A stub program reads the terminal the way a
busy TUI does — one read, then a sleep — and reports how many bytes each read brought:

```console
$ pty-tui.py --steady 0.12 … "a,b,c,q" python3 slowtty.py   # the gate's setting
READY
READ 1 a
READ 1 b
READ 1 c
READ 1 q

$ pty-tui.py --steady 0 … "a,b,c,q" python3 slowtty.py      # the wait turned off
READY
READ 4 abcq
```

The step requires the first shape and requires the second, so the check is known to be able to fail.
The folded case is what a harness without the wait does, and it does it deterministically: the stub
answers with the read that consumed a keystroke, and the harness types the next keystroke only after
that answer, so the read for key N is finished before key N+1 is written.

## Design decisions

**Wait for the screen, not for the clock.** The alternative was a larger `--gap`. A bigger gap is a
number that has to be guessed against the slowest repaint on the slowest runner, and it is the wrong
shape: the property is "the program has finished with what it was given", and the program says so by
going quiet. `--gap` keeps its old job of bounding the window. The ceiling is four times the
threshold, measured against the alternatives: with the startup `--settle` as the ceiling the gate cost
139s, and at a 0.3s threshold it costs 161s, while a program that never goes quiet — a spinner — is
waited for to the ceiling either way.

**The last keystroke is exempt.** Nothing follows it to be folded with, and the tail already reads
after it. The exemption keeps the added time proportional to the transitions between keys rather than
to the number of keys.

**The stub clears `ICANON` and `ECHO` rather than calling `tty.setraw`.** A stub that leaves the
terminal in its default state is not reading what the harness typed — the line discipline holds
anything without a newline, which is what the first version of this probe did: it saw the echo of each
keystroke and never one read. Clearing only those two flags keeps output post-processing, so the
lines the stub writes are the lines the capture shows. The stub is a program that owns a terminal,
which is the only kind of program this harness can meaningfully drive.

## Validation

`bash scripts/gates/pty-walkthrough.sh` three times against this repository's binary, which is the
measurement that chose the numbers:

| `PTY_STEADY` | checks | failures | wall clock |
| ------------ | ------ | -------- | ---------- |
| unset (0.12) | 155    | 0        | 85s        |
| `0`          | 153    | 3        | 40s        |
| `0.3`        | 155    | 0        | 161s       |

The middle row is the harness as it was, and it fails: the folded reads land on `key b`, `key c` and
on the fold check itself, which is the same run with the wait removed and the state the probe exists
to see. The last row is why the wait's ceiling is four times the threshold rather than the startup
`--settle 1`, which measured 139s at `PTY_STEADY=0.15`. The cost of the property at the default is
about 45s on this machine. `mise run check` and `mise run gates` cover the rest.

## Known limitations

The probe pins the property against a stub that reads slowly and answers immediately. A real TUI can
also be slow in ways the stub is not — a repaint that arrives in several bursts, or a key that
produces no output at all — and for those the wait falls back to its ceiling, which is extra time
rather than extra certainty. The scenario checks themselves still decide the product's behaviour;
this step decides only that the keystrokes reached the program as the scenarios intended.
