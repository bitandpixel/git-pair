#!/bin/sh
# Install a published git-pair binary.
#
# The one-line form:
#
#     curl -fsSL https://github.com/bitandpixel/git-pair/releases/latest/download/install.sh | sh
#
# It downloads the archive for this machine's OS and architecture from this repository's releases,
# checks its SHA-256 against checksums.txt from the same release, and writes the binary into
# ~/.local/bin/git-pair. The name matters: git finds a subcommand by name, so `git pair review` and
# `git-pair review` are the same program only while the file on PATH is called `git-pair`.
#
# What the checksum does and does not prove. It catches a truncated download and an archive that got
# mixed with another build — the failure a piped installer actually hits. It does not authenticate the
# publisher: checksums.txt comes over TLS from the same origin as the archive, so a compromised release
# would carry its own matching checksum. Anyone who needs that guarantee should pin a version
# (--version v0.1.0), read the release notes, and compare the hash by hand.
#
# The three overrides exist for reasons beyond testing, though they are what the gate script under
# scripts/gates/ uses to replay this file against a local build:
#
#   GIT_PAIR_VERSION      release to install, default `latest`
#   GIT_PAIR_INSTALL_DIR  where to write it, default ~/.local/bin
#   GIT_PAIR_BASE_URL     release base, for a mirror or a local rehearsal
#
# Written for /bin/sh: no arrays, no `[[`, no process substitution. It runs under dash and busybox
# ash as well as bash, and it never uses sudo — if the install directory is not writable, that is
# yours to decide about, not something this script escalates through.
set -eu

# The repository the releases live in, as owner/repo. Everything about the URL derives from this, and
# `mise run release:check` greps the same slug out of README.md and .goreleaser.yaml, so the three places
# that name it cannot quietly disagree — a release page pointing at a repository that is not the one the
# installer downloads from is the kind of mistake nobody notices until someone installs the wrong thing.
slug=bitandpixel/git-pair
project=git-pair

version=${GIT_PAIR_VERSION:-latest}
install_dir=${GIT_PAIR_INSTALL_DIR:-"$HOME/.local/bin"}
base_url=${GIT_PAIR_BASE_URL:-"https://github.com/$slug/releases"}
force=0
quiet=0

usage() {
  cat <<'USAGE'
usage: install.sh [--version v0.1.0] [--install-dir DIR] [--force] [--quiet] [--help]

  --version v0.1.0   install a specific release instead of the newest one
  --install-dir DIR  where to write git-pair (default ~/.local/bin)
  --force            replace the existing file at that path whatever it is
  --quiet            no progress output, only errors
  --help             this text

Environment: GIT_PAIR_VERSION, GIT_PAIR_INSTALL_DIR, GIT_PAIR_BASE_URL do the same jobs.
USAGE
}

say() {
  [ "$quiet" -eq 1 ] || printf '%s\n' "$*"
}

die() {
  printf 'git-pair install: %s\n' "$*" >&2
  exit 1
}

while [ $# -gt 0 ]; do
  case "$1" in
    --version) [ $# -ge 2 ] || die "--version needs a value"; version=$2; shift 2 ;;
    --version=*) version=${1#*=}; shift ;;
    --install-dir) [ $# -ge 2 ] || die "--install-dir needs a value"; install_dir=$2; shift 2 ;;
    --install-dir=*) install_dir=${1#*=}; shift ;;
    --base-url) [ $# -ge 2 ] || die "--base-url needs a value"; base_url=$2; shift 2 ;;
    --base-url=*) base_url=${1#*=}; shift ;;
    --force) force=1; shift ;;
    --quiet) quiet=1; shift ;;
    -h | --help) usage; exit 0 ;;
    *) usage >&2; die "unknown argument: $1" ;;
  esac
done

# uname says the machine's name, the release says the archive's name, and the two vocabularies differ
# only for the two names that matter most. Anything else is a platform this project does not ship.
os=$(uname -s)
case "$os" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) die "no git-pair release for OS \"$os\" (linux and darwin are published)" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) die "no git-pair release for architecture \"$arch\" (amd64 and arm64 are published)" ;;
esac

# `latest` is a redirect to the newest release, not a version in a filename: the archive there is named
# with the version it actually is. So for `latest` the checksums file is fetched first and read for the
# name, which both resolves the version and gives the hash to check it against, without asking the GitHub
# API and its rate limits. A pinned version needs no resolution — its name is known, and checksums.txt is
# then only the thing the hash is looked up in.
asset=""
if [ "$version" = latest ]; then
  download_base="$base_url/latest/download"
  label="the newest release"
else
  download_base="$base_url/download/$version"
  label="release $version"
  asset="$project"_"$version"_"$os"_"$arch".tar.gz
fi

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
else
  die "need curl or wget to fetch the release"
fi

# A hash tool per platform: sha256sum on Linux, shasum on macOS, openssl as the fallback that is
# usually present when neither of the first two is.
hash_tool=""
if command -v sha256sum >/dev/null 2>&1; then
  hash_tool=sha256sum
elif command -v shasum >/dev/null 2>&1; then
  hash_tool=shasum
elif command -v openssl >/dev/null 2>&1; then
  hash_tool=openssl
fi
[ -n "$hash_tool" ] || die "need sha256sum, shasum, or openssl to verify the download"

checksum_of() {
  case "$hash_tool" in
    sha256sum) sha256sum "$1" | awk '{print $1}' ;;
    shasum) shasum -a 256 "$1" | awk '{print $1}' ;;
    openssl) openssl dgst -sha256 "$1" | awk '{print $NF}' ;;
  esac
}

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t git-pair) || die "could not make a temporary directory"
# EXIT covers the failure paths; the others cover the Ctrl-C a piped installer actually gets.
trap 'rm -rf "$tmp"' EXIT INT HUP TERM

say "fetching checksums for $label"
fetch "$download_base/checksums.txt" "$tmp/checksums.txt" ||
  die "could not download $download_base/checksums.txt"

if [ -z "${asset:-}" ]; then
  # Exactly one entry for this machine, or the newest release is ambiguous and this installer will not
  # guess. Two entries would mean someone published a second build of the same version for the same
  # platform, and which one `latest` should hand out is not this script's call.
  asset=$(awk -v p="$project" -v suf="_$os"_"$arch".tar.gz '
    substr($2, 1, length(p)) == p &&
    substr($2, length($2) - length(suf) + 1) == suf &&
    length($2) > length(p) + length(suf) { name = $2; n++ }
    END { if (n != 1) exit 1; print name }' "$tmp/checksums.txt") ||
    die "checksums.txt lists no $os/$arch archive for $label"
  version=${asset#"$project"_}
  version=${version%"_$os"_"$arch".tar.gz}
fi

say "downloading $asset"
fetch "$download_base/$asset" "$tmp/$asset" ||
  die "could not download $download_base/$asset"

# Match on the filename field, not the line, so a release that happens to carry git-pair_0.1.0_… and
# git-pair_0.1.0_linux_amd64.tar.gz.sig side by side cannot have one line's hash read against the other.
expected=$(awk -v name="$asset" '$2 == name { print $1; exit }' "$tmp/checksums.txt")
[ -n "$expected" ] || die "checksums.txt lists no $asset for $label"
actual=$(checksum_of "$tmp/$asset")
[ "$expected" = "$actual" ] ||
  die "checksum mismatch for $asset
       expected $expected
       actual   $actual"
say "checksum verified"

tar -xzf "$tmp/$asset" -C "$tmp" || die "could not unpack $asset"
[ -f "$tmp/$project" ] || die "$asset did not contain a $project binary"

target="$install_dir/$project"
if [ -d "$target" ]; then
  # Before the --version probe, which a directory fails for the same reason a broken file does but for no
  # comparable reason: the fix is to move the directory, and --force would not fix anything.
  die "$target is a directory, not a binary - install elsewhere with --install-dir"
fi
if [ -e "$target" ] && [ "$force" -ne 1 ]; then
  # Refuse to overwrite something this installer did not write, which is the same rule
  # `git pair skill install` applies to files it did not create. A binary at that path that will not
  # even answer --version is not a git-pair someone forgot about; it is a name collision.
  if ! "$target" --version >/dev/null 2>&1; then
    die "$target exists and is not a working git-pair binary
       pass --force to replace it"
  fi
fi

[ -d "$install_dir" ] || mkdir -p "$install_dir" ||
  die "could not create $install_dir (not writable? pass --install-dir)"
[ -w "$install_dir" ] ||
  die "$install_dir is not writable (pass --install-dir DIR, or run this as the user who owns it)"

# Install rather than mv: mv across filesystems leaves a half-written file behind on failure, and
# /tmp and $HOME are not the same filesystem on a separate-partition or container host.
if command -v install >/dev/null 2>&1; then
  install -m 0755 "$tmp/$project" "$target"
else
  cp "$tmp/$project" "$target" && chmod 0755 "$target"
fi

# The cheapest end-to-end proof: the bytes that landed execute and name themselves.
installed=$("$target" --version 2>&1) || die "$target was written but will not run"
say "installed $target"
say "  $installed"

case ":$PATH:" in
  *":$install_dir:"*) ;;
  *)
    printf 'git-pair install: %s is not on PATH, so git will not find "git pair".\n' "$install_dir" >&2
    printf 'git-pair install: add it with:  export PATH="%s:$PATH"\n' "$install_dir" >&2
    ;;
esac
