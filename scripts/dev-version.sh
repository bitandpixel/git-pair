#!/bin/sh
# The version a dev build reports, printed so the build tasks and anything checking them agree on one
# rule instead of each restating it — the same reason scripts/install-name.sh exists.
#
# A release takes its version from its tag: goreleaser stamps `-X gitpair/internal/cli.Version={{.Version}}`
# (.goreleaser.yaml), and nothing here is involved in that. What is left is every other build, and a
# build from a commit with no tag has no version to report. `0.0.0-dev+<sha>` says both halves of that.
# The `0.0.0-dev` is a value no release can ever be named, because a release is named by its tag, so a
# dev build cannot be mistaken for one that was shipped; and the commit is the thing a bug report
# actually needs — the sha, with `.dirty` when the tree holds uncommitted work, which is the difference
# between "someone ran a build of a commit" and "someone ran a build of their own edits".
#
# The commit goes in semver build metadata rather than the version proper, so the string a dev build
# prints is still a version to anything reading it, and still cannot outrank a real release.
#
# Usage: sh scripts/dev-version.sh [repo]     (default: the current directory)
set -eu
repo=${1:-.}
sha=$(git -C "$repo" rev-parse --short HEAD 2>/dev/null) || sha=""
if [ -z "$sha" ]; then
  # No commits, or not a repository: name that rather than print a number that means nothing.
  printf '0.0.0-dev+unknown\n'
  exit 0
fi
if ! git -C "$repo" diff --quiet HEAD -- 2>/dev/null; then
  sha="$sha.dirty"
fi
printf '0.0.0-dev+%s\n' "$sha"
