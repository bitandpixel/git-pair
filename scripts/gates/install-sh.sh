#!/usr/bin/env bash
# Replays packaging/install.sh against a built release directory, so the one-liner people are told to
# curl is something a build actually ran.
# Usage: bash scripts/gates/install-sh.sh [/path/to/dist]
#        (default: <repo>/dist — the directory `goreleaser release --snapshot` writes)
#
# The installer is the only part of the release path a user runs, and the only part no Go test can
# reach: it is /bin/sh, it reads the machine with uname, it downloads, and it checks a hash. This replay
# serves a directory shaped like GitHub's release URLs over http on localhost and drives the installer
# through the cases that decide whether an install works:
#
#   the newest release installs and the binary it writes runs; a named version installs; the archive is
#   checked against checksums.txt and a tampered one is refused; an archive missing from checksums.txt is
#   refused; a file this installer did not write is not overwritten without --force; an unwritable
#   directory is reported rather than escalated through sudo; an unsupported machine says so in the words
#   that name the machine; and the PATH note appears when the install directory is not on PATH.
#
# Two things it restates rather than shares, on purpose. The uname→asset-name mapping is re-derived here
# so that a change to it in the installer shows up as a failure in this replay instead of as a second
# copy of the same bug. And the release URL layout (`releases/latest/download/…` and
# `releases/download/<version>/…`) is built here as a directory tree, which is what proves the
# installer's URL construction against GitHub's actual shape rather than against a comment saying so.
#
# It is not in `mise run gates`. That ladder is what every change pays for, and this needs goreleaser,
# a Go cross-compile of four targets, and a listening socket — all of which only matter when a tag is
# cut. The Release workflow runs it between the build and the publish, which is the point where it pays.
set -uo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
DIST=${1:-$ROOT/dist}
INSTALLER=$ROOT/packaging/install.sh
for f in "$INSTALLER" "$DIST/checksums.txt"; do
  if [ ! -f "$f" ]; then
    printf 'no %s - run `mise run release:snapshot` first\n' "$f" >&2
    exit 1
  fi
done
if ! command -v python3 >/dev/null 2>&1; then
  printf 'this replay needs python3 to serve the release directory\n' >&2
  exit 1
fi
if ! command -v curl >/dev/null 2>&1; then
  printf 'this replay needs curl (the installer falls back to wget, the replay does not)\n' >&2
  exit 1
fi

T=$(mktemp -d /tmp/git-pair-install-gate.XXXXXX)
trap 'rm -rf "$T"' EXIT
FAILED=0
PASS=0

step()  { printf '\n\033[1m### %s\033[0m\n' "$1"; }
ok()    { PASS=$((PASS+1)); printf '  ok: %s\n' "$*"; }
fail()  { printf '  FAIL: %s\n' "$*"; FAILED=1; }
check() { # check <description> <expected-exit> <actual-exit>
  if [ "$2" = "$3" ]; then ok "$1"; else fail "$1 (exit $3, want $2)"; fi
}
contains() { # contains <haystack> <needle> <description>
  case $1 in *"$2"*) ok "$3" ;; *) fail "$3 — missing: $2" ;; esac
}

# The machine's own asset, named the way the release names it. Re-derived here rather than read from the
# installer, for the reason in the header.
os=$(uname -s)
case "$os" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) printf 'no published release for OS "%s" - this replay runs on linux or darwin\n' "$os" >&2; exit 1 ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) printf 'no published release for architecture "%s" - this replay runs on amd64 or arm64\n' "$arch" >&2; exit 1 ;;
esac

asset=$(awk -v o="$os" -v a="$arch" '$2 ~ "^git-pair_.+_" o "_" a "\\.tar\\.gz$" { print $2; exit }' \
  "$DIST/checksums.txt")
if [ -z "$asset" ]; then
  printf 'dist/checksums.txt has no %s_%s archive:\n%s\n' "$os" "$arch" \
    "$(cat "$DIST/checksums.txt")" >&2
  exit 1
fi
version=${asset#git-pair_}
version=${version%_"$os"_"$arch".tar.gz}
if [ ! -f "$DIST/$asset" ]; then
  printf 'dist/%s is named in checksums.txt but is not there\n' "$asset" >&2
  exit 1
fi

# One release directory, in each of the shapes the installer has to cope with. Built as separate trees
# instead of mutating one, so a case cannot leak its damage into the next case.
mk_release() { # mk_release <dir> <ok|corrupt|nochecksum>
  local dir=$1 mode=$2
  mkdir -p "$dir/releases/latest/download" "$dir/releases/download/$version"
  cp "$DIST/$asset" "$dir/releases/latest/download/$asset"
  cp "$DIST/$asset" "$dir/releases/download/$version/$asset"
  cp "$DIST/checksums.txt" "$dir/releases/latest/download/checksums.txt"
  cp "$DIST/checksums.txt" "$dir/releases/download/$version/checksums.txt"
  case $mode in
    corrupt)
      python3 -c 'import sys
p = sys.argv[1]
with open(p, "r+b") as fh:
    fh.seek(0)
    fh.write(b"not a tarball any more")' "$dir/releases/latest/download/$asset"
      ;;
    nochecksum)
      awk -v n="$asset" '$2 != n' "$DIST/checksums.txt" \
        >"$dir/releases/latest/download/checksums.txt"
      ;;
  esac
}
mk_release "$T/good" ok
mk_release "$T/bad" corrupt
mk_release "$T/nochecksum" nochecksum

PORT=$(python3 -c 'import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])')
python3 -m http.server "$PORT" --bind 127.0.0.1 --directory "$T" >/dev/null 2>&1 &
SERVER=$!
trap 'kill $SERVER 2>/dev/null; rm -rf "$T"' EXIT
ready=0
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40 41 42 43 44 45 46 47 48 49 50; do
  if curl -sf -o /dev/null "http://127.0.0.1:$PORT/good/releases/latest/download/checksums.txt"; then
    ready=1
    break
  fi
  sleep 0.2
done
if [ "$ready" -ne 1 ]; then
  printf 'could not start the release server on 127.0.0.1:%s\n' "$PORT" >&2
  exit 1
fi
ok "serving $DIST as a release directory on port $PORT"

RC=0
OUT="$T/out"
run() { # run <base> <args...>   — the installer under sh, the shell a piped curl | sh actually gives it
  local base=$1
  shift
  GIT_PAIR_BASE_URL="http://127.0.0.1:$PORT/$base/releases" \
    sh "$INSTALLER" "$@" >"$OUT" 2>&1
  RC=$?
}

step "the newest release installs"
run good --install-dir "$T/bin-latest" --quiet
check "installer exits 0" 0 "$RC"
contains "$(cat "$OUT")" "not on PATH" "says so when the install directory is off PATH"
if [ -x "$T/bin-latest/git-pair" ]; then
  ok "wrote an executable ~/.local/bin/git-pair at the path the release names"
else
  fail "no executable $T/bin-latest/git-pair"
fi
reported=$("$T/bin-latest/git-pair" --version 2>&1)
check "the installed binary answers --version" 0 "$?"
contains "$reported" "git-pair version" "names itself as git-pair"
contains "$reported" "$version" "reports the version it was built as ($version)"

step "a named version installs"
run good --install-dir "$T/bin-pinned" --version "$version" --quiet
check "installer exits 0" 0 "$RC"
contains "$("$T/bin-pinned/git-pair" --version 2>&1)" "git-pair version" "the pinned install runs"

step "a version that was never published is refused"
run good --install-dir "$T/bin-missing" --version v9.9.9-not-published --quiet
check "asking for a release that is not there stops" 1 "$RC"
contains "$(cat "$OUT")" "could not download" "names the thing it could not fetch"
if [ -e "$T/bin-missing/git-pair" ]; then
  fail "something was written for a release that does not exist"
else
  ok "nothing was written for the missing release"
fi

step "the archive is checked against checksums.txt"
run bad --install-dir "$T/bin-corrupt" --quiet
check "a tampered archive is refused" 1 "$RC"
contains "$(cat "$OUT")" "checksum mismatch" "says which check failed"
if [ -e "$T/bin-corrupt/git-pair" ]; then
  fail "the tampered binary was written anyway"
else
  ok "nothing was written for the tampered archive"
fi
run nochecksum --install-dir "$T/bin-nochecksum" --quiet
check "an archive missing from checksums.txt is refused" 1 "$RC"
contains "$(cat "$OUT")" "checksums.txt lists no" "says the checksums file is where it looked"
contains "$(cat "$OUT")" "$os/$arch" "names the platform it could not find"

step "a file this installer did not write is left alone"
mkdir -p "$T/bin-existing"
printf '#!/bin/sh\nexit 3\n' >"$T/bin-existing/git-pair"
chmod 0755 "$T/bin-existing/git-pair"
run good --install-dir "$T/bin-existing" --quiet
check "an existing non-git-pair file stops the install" 1 "$RC"
contains "$(cat "$OUT")" "not a working git-pair binary" "says what is in the way"
contains "$(cat "$OUT")" "--force" "names the way through"
run good --install-dir "$T/bin-existing" --force --quiet
check "--force replaces it" 0 "$RC"
contains "$("$T/bin-existing/git-pair" --version 2>&1)" "git-pair version" "the replacement runs"

step "a directory in the way is named as one"
mkdir -p "$T/bin-dir/git-pair"
run good --install-dir "$T/bin-dir" --quiet
check "a directory at the target stops the install" 1 "$RC"
contains "$(cat "$OUT")" "is a directory" "says what is there rather than blaming --version"
contains "$(cat "$OUT")" "--install-dir" "offers the way round it"

step "an unwritable directory is reported, not escalated"
mkdir -p "$T/readonly"
chmod 0555 "$T/readonly"
run good --install-dir "$T/readonly/nested" --quiet
check "a directory that cannot be created stops the install" 1 "$RC"
contains "$(cat "$OUT")" "--install-dir" "offers the option that fixes it"
chmod 0755 "$T/readonly"

step "a machine nothing is published for is named"
mkdir -p "$T/shim"
printf '#!/bin/sh\ncase "$1" in -s) echo FreeBSD ;; -m) echo %s ;; esac\n' "$arch" >"$T/shim/uname"
chmod 0755 "$T/shim/uname"
PATH="$T/shim:$PATH" GIT_PAIR_BASE_URL="http://127.0.0.1:$PORT/good/releases" \
  sh "$INSTALLER" --install-dir "$T/bin-freebsd" --quiet >"$OUT" 2>&1
RC=$?
check "an unsupported OS stops the install" 1 "$RC"
contains "$(cat "$OUT")" "no git-pair release for OS" "names the OS rather than failing to download"
printf '#!/bin/sh\ncase "$1" in -s) echo %s ;; -m) echo sparc64 ;; esac\n' "$(uname -s)" \
  >"$T/shim/uname"
chmod 0755 "$T/shim/uname"
PATH="$T/shim:$PATH" GIT_PAIR_BASE_URL="http://127.0.0.1:$PORT/good/releases" \
  sh "$INSTALLER" --install-dir "$T/bin-sparc" --quiet >"$OUT" 2>&1
RC=$?
check "an unsupported architecture stops the install" 1 "$RC"
contains "$(cat "$OUT")" "no git-pair release for architecture" "names the architecture"

step "the release carries every published target"
published=$(awk '$2 ~ /^git-pair_.+\.tar\.gz$/ { print $2 }' "$DIST/checksums.txt" | sort -u)
count=$(printf '%s\n' "$published" | grep -c . || true)
if [ "$count" -eq 4 ]; then
  ok "checksums.txt covers four archives"
else
  fail "expected four archives, checksums.txt names $count:
$(printf '%s\n' "$published" | sed 's/^/      /')"
fi
for name in $published; do
  if [ -f "$DIST/$name" ]; then
    ok "  $name"
  else
    fail "  $name is named but not built"
  fi
done

printf '\n'
if [ "$FAILED" -eq 0 ]; then
  printf 'install-sh gate: %d checks passed\n' "$PASS"
else
  printf 'install-sh gate: FAILED (%d checks passed)\n' "$PASS"
fi
exit "$FAILED"
