#!/usr/bin/env python3
"""Drive a gitpr TUI session through a real pty and capture what it painted.

Part of the review walkthrough in pty-walkthrough.sh; run that instead of this. It exists because
the TUI's screen is not reachable from a Go test: `review open` refuses to start without a
terminal, and the things worth checking here — that a keystroke repaints, that a tool handoff
leaves the screen where it found it, that a ref moved by another process shows up while a session
is open — only happen against a terminal and a real git.

Only the standard library is used, and no terminal emulator: the harness captures the raw byte
stream, and the checks look for the strings and the escape sequences that must appear in it.
Geometry the screen model would check is already asserted by Go tests (internal/tui).

usage: pty-tui.py [--raw FILE] [--settle S] [--gap S] [--tail S] [--timeout S]
                  COLS ROWS CWD KEYS PROGRAM [ARGS...]

KEYS is a comma-separated list. Each entry is written to the terminal as its own write, because a
burst sent in one write gets coalesced by the input layer and arrives as one event: "jjjj" moves
the cursor once. Entries:

  !<shell command>   run on the host instead of typing it — how a walkthrough moves a ref, or
                     creates a file, while the session is open
  ~<seconds>         wait without typing, reading whatever the program paints: how a walkthrough
                     waits for a timer it does not own, such as the drift check's tick
  enter esc space tab backspace up down left right
  ctrl-c ctrl-d ctrl-f ctrl-b ctrl-r ctrl-s ctrl-q
  anything else      sent literally, one entry per keystroke ("j,j,j", never "jjj")
"""

import os
import pty
import select
import signal
import subprocess
import sys
import time

KEYS = {
    "enter": b"\r",
    "return": b"\r",
    "esc": b"\x1b",
    "space": b" ",
    "tab": b"\t",
    "backspace": b"\x7f",
    "up": b"\x1b[A",
    "down": b"\x1b[B",
    "right": b"\x1b[C",
    "left": b"\x1b[D",
    "ctrl-c": b"\x03",
    "ctrl-d": b"\x04",
    "ctrl-f": b"\x06",
    "ctrl-b": b"\x02",
    "ctrl-r": b"\x12",
    "ctrl-s": b"\x13",
    "ctrl-q": b"\x11",
}


# A program that owns a terminal asks it questions, and a harness that does not answer is not a
# terminal: bubbletea asks for the background colour and the cursor position at startup and holds
# the input queue until they come back, so every keystroke sent in the meantime is eaten as a
# malformed reply and the session looks hung. Each entry is (query, answer, name).
QUERIES = (
    (b"\x1b[6n", b"\x1b[1;1R", "cursor position"),
    (b"\x1b]11;?", b"\x1b]11;rgb:0000/0000/0000\x1b\\", "background colour"),
    (b"\x1b[?u", b"\x1b[?u", "keyboard flags"),
    (b"\x1b[0c", b"\x1b[?6c", "device attributes"),
    (b"\x1b[c", b"\x1b[?6c", "device attributes"),
)


def parse(argv):
    opts = {"settle": 0.8, "gap": 0.06, "tail": 0.8, "timeout": 25.0, "raw": None, "term": "xterm-256color"}
    while argv and argv[0].startswith("--"):
        name = argv[0][2:]
        if name not in ("settle", "gap", "tail", "timeout", "raw", "term"):
            sys.exit(f"pty-tui.py: unknown option --{name}")
        opts[name] = argv[1]
        argv = argv[2:]
    if len(argv) < 5:
        sys.exit(__doc__)
    opts["settle"] = float(opts["settle"])
    opts["gap"] = float(opts["gap"])
    opts["tail"] = float(opts["tail"])
    opts["timeout"] = float(opts["timeout"])
    return opts, int(argv[0]), int(argv[1]), argv[2], argv[3].split(","), argv[4:]


def main():
    opts, cols, rows, cwd, keys, program = parse(sys.argv[1:])
    pid, master = pty.fork()
    if pid == 0:  # child: the terminal is already stdin/stdout/stderr
        os.chdir(cwd)
        os.environ["TERM"] = opts["term"]
        os.environ["COLUMNS"] = str(cols)
        os.environ["LINES"] = str(rows)
        try:
            os.execvp(program[0], program)
        except OSError as exc:  # pragma: no cover - exec failure shows up as a harness error
            sys.stderr.write(f"exec {program[0]}: {exc}\n")
            os._exit(127)

    import fcntl
    import struct
    import termios

    fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
    captured = bytearray()
    answered = set()  # (offset, name) — one reply per query, keyed by where it appeared
    deadline = time.time() + opts["timeout"]
    status = 0
    running = True

    def answer_queries():
        """Reply to whatever the program asked the terminal, once per occurrence."""
        window = bytes(captured[-64:])
        for query, reply, name in QUERIES:
            at = window.rfind(query)
            if at < 0:
                continue
            key = (len(captured) - len(window) + at, name)
            if key in answered:
                continue
            answered.add(key)
            os.write(master, reply)

    def pump(until):
        """Read whatever the program paints until `until`, or until it exits."""
        nonlocal running
        while time.time() < until and running:
            readable, _, _ = select.select([master], [], [], 0.05)
            if master in readable:
                try:
                    chunk = os.read(master, 65536)
                except OSError:
                    running = False
                    break
                if not chunk:
                    running = False
                    break
                captured.extend(chunk)
                answer_queries()
            else:
                done, sigstatus = os.waitpid(pid, os.WNOHANG)
                if done:
                    status = sigstatus
                    running = False

    pump(time.time() + opts["settle"])
    for index, key in enumerate(keys):
        if not running:
            break
        # Mark the capture at every keystroke, so a check can ask what painted after *that* key
        # rather than squinting at a stream of overlapping frames. Without markers there is no
        # reliable frame boundary in the byte stream.
        captured.extend(b"\n\x1b[0m<<<KEY %d:%s>>>\n" % (index, key.encode("utf-8", "replace")))
        if key.startswith("!"):
            subprocess.run(["/bin/sh", "-c", key[1:]], cwd=cwd, check=False)
            pump(time.time() + opts["settle"])
            continue
        if key.startswith("~"):
            # Pump without typing: how a walkthrough waits for a timer it does not own, such as
            # the drift check's tick, while still draining the terminal so the program cannot
            # block on a full pty buffer.
            pump(time.time() + float(key[1:]))
            continue
        if not key:
            continue
        os.write(master, KEYS.get(key, key.encode()))
        pump(time.time() + opts["gap"])

    if running:
        pump(time.time() + opts["tail"])
    if running:  # still alive after everything: it hung, and the walk must say so loudly
        os.kill(pid, signal.SIGKILL)
        pump(time.time() + 1.0)
        status = 124

    os.close(master)
    out = bytes(captured)
    if opts["raw"]:
        with open(opts["raw"], "wb") as handle:
            handle.write(out)
    else:
        sys.stdout.buffer.write(out)
    # Exit 124 means the harness killed a hung program, which is a failed check, not a passing one.
    sys.exit(0 if status == 0 else (124 if status == 124 else 1))


if __name__ == "__main__":
    main()
