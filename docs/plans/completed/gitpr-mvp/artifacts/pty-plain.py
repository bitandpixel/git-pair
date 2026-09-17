#!/usr/bin/env python3
"""Print what a gitpr pty session painted, escapes removed.

Part of the review walkthrough in pty-walkthrough.sh; run that instead of this.

usage: pty-plain.py [--after N] [--head N] RAWFILE

--after N   only what painted after keystroke N was sent. -1 means "before any key", which is the
            first frame the reviewer saw. The capture holds every frame the session drew, so a
            claim like "the banner appeared after the ref moved" has to be a claim about a window.
--head N    stop after N lines, for reading a screen rather than grepping it.
"""

import argparse
import re
import sys

CSI = re.compile(r"\x1b\[[0-9;?]*[a-zA-Z]")
OSC = re.compile(r"\x1b\][^\x07]*\x07")
MARKER = re.compile(r"^<<<KEY \d+:.*>>>\s*$")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--after", type=int, default=None)
    parser.add_argument("--head", type=int, default=None)
    parser.add_argument("rawfile")
    args = parser.parse_args()

    with open(args.rawfile, "rb") as handle:
        text = handle.read().decode("utf-8", "replace")

    if args.after is not None and args.after >= 0:
        marker = f"<<<KEY {args.after}:"
        at = text.find(marker)
        if at < 0:
            sys.exit(f"pty-plain.py: no marker for key {args.after} in {args.rawfile}")
        newline = text.find("\n", at)
        text = text[newline + 1:] if newline >= 0 else ""
    # --after -1 means "before any key was sent": the first frame the reviewer saw, which is the
    # whole claim of a first-paint check.

    lines = [CSI.sub("", OSC.sub("", line)) for line in text.splitlines()]
    lines = [line for line in lines if not MARKER.match(line)]
    if args.head is not None:
        lines = lines[: args.head]
    sys.stdout.write("\n".join(lines) + ("\n" if lines else ""))


if __name__ == "__main__":
    main()
