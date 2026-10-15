#!/usr/bin/env bash
# Install the pinned git-pair the merge gate runs with. Called by every job in
# .github/workflows/git-pair-integrate.yml, so the tool that answers the gate is one tool and not one per file.
#
# Usage: scripts/ci/install-git-pair.sh          (GIT_PAIR_VERSION selects the release; default below)
#
# Why this is a script and not a step: the two jobs that ask git-pair a question must not be able to disagree
# about which build answered it. A step copied twice drifts, and drift here means a merge decided by a binary
# nobody reviewed.
#
# Why pinned rather than `latest`: the gate that decides whether work lands should not change underneath a run,
# and an unpinned tool makes every merge a build of whatever the release happened to be. Move the pin with a
# repository variable (GIT_PAIR_VERSION) or by changing the default here, so the diff shows the version that ran
# the last merge.
#
# Why v0.5.0 as the floor and the default: it is the release whose `scripts/ci/git-pair-integrate.sh` this
# profile ships byte-for-byte, so the program that runs the merge gate and the script the job runs come from one
# tag — and it carries both `git pair change integrate`, the declaration the job gates on, and the candidate-shape
# driver that verifies the merge commit before it pushes. The floor above it is the release carrying the
# changeset scaffold, so the tool that writes the scaffold at `git pair init` and the tool that runs the gate stay
# one program.
set -euo pipefail

VERSION=${GIT_PAIR_VERSION:-v0.5.0}
INSTALL_DIR=${GIT_PAIR_INSTALL_DIR:-${RUNNER_TEMP:-/tmp}/git-pair-bin}
base="https://github.com/bitandpixel/git-pair/releases/download/$VERSION"

mkdir -p "$INSTALL_DIR"

# The release ships the installer that does this job: it picks the archive for this OS and architecture and
# checks it against the checksums.txt from the same release. Both come from the pinned tag, so the script that
# verifies the binary is pinned by the same reference as the binary it verifies — and the archive's naming rules
# stay the release's own business.
#
# What the checksum proves: a complete archive, and the same bytes the release lists. What it does not prove:
# that the publisher is honest — checksums.txt arrives over TLS from the same origin as the archive, so a
# compromised release carries a matching checksum of its own. Pinning narrows the window to one tag.
curl -fsSL --retry 3 -o "$INSTALL_DIR/install.sh" "$base/install.sh"

# Non-zero here means the archive did not match checksums.txt, or the release does not exist. Either way this
# exits before the gate runs, and the gate never runs on an unverified tool.
#
# The destination has to be said out loud. `install.sh` writes wherever it was told, and told nothing it writes to
# its own default, `~/.local/bin` — which on a runner is a directory nobody added to `GITHUB_PATH`, and a binary
# the step below cannot find. That is how this line shipped broken: the installer exited 0, the fetch and checksum
# were fine, and the very next command exec'd `$INSTALL_DIR/git-pair`, which had never been written there. The
# flag is what makes the two halves of this script agree on one path; the installer has honoured it since the
# release this profile pins.
sh "$INSTALL_DIR/install.sh" --force --quiet --install-dir "$INSTALL_DIR"

# Report the version of the binary this step verified, by path. Resolving `git-pair` by name would report
# whatever the machine already had, which is a different claim from the one being made.
"$INSTALL_DIR/git-pair" --version

# On a runner, later steps get it through GITHUB_PATH and the driver gets the explicit path through GITHUB_ENV.
# Off a runner both are unset, and the caller can still point GIT_PAIR_BIN at the directory.
if [ -n "${GITHUB_PATH:-}" ]; then
  printf '%s\n' "$INSTALL_DIR" >> "$GITHUB_PATH"
fi
if [ -n "${GITHUB_ENV:-}" ]; then
  printf 'GIT_PAIR_BIN=%s/git-pair\n' "$INSTALL_DIR" >> "$GITHUB_ENV"
fi
export GIT_PAIR_BIN="$INSTALL_DIR/git-pair"
