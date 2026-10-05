# fix-installer-pinned-version

## Summary

`GIT_PAIR_VERSION=v0.1.0 sh install.sh` cannot install anything, and neither can
`GIT_PAIR_VERSION=0.1.0`. Pinned installs have been broken since v0.1.0 shipped; the unpinned form works,
which is why it went unnoticed. Reported by David against the published release.

## What changed

- `packaging/install.sh` — the pinned branch no longer builds the asset name. The input is normalized to a
  tag for the URL path (`tag="v${version#v}"`), and the archive is resolved from that release's
  `checksums.txt` — the same resolution `latest` already needed. The two die messages now read
  `checksums.txt for <label> lists no …`, naming the release they looked in.
- `scripts/gates/install-sh.sh` — the fixture serves a `v`-prefixed tag directory over an unprefixed
  archive name, which production does and the old fixture did not. New cases: `--version` and
  `GIT_PAIR_VERSION`, each spelled with and without the `v`; a release whose archive is named for
  something unrelated to its tag; and the missing-release case now asserts the URL asked for the tag.
- `README.md` — the Install paragraph claimed the installer "names the archive from `uname`". It does not,
  and the sentence was the bug's shape: only the platform comes from `uname`. Pinning now says the leading
  `v` is optional.

## Design decisions

**The name is read, never constructed.** goreleaser names an archive from `{{ .Version }}` while GitHub
keys the download directory by tag, and nothing in the installer can derive one from the other without an
assumption about how this project tags. `checksums.txt` is the release's own index of what it published,
so it is asked, and the "exactly one archive for this machine, or stop" rule that `latest` already applied
now covers pinned installs too. One resolution path instead of two.

**The tag is the input with a `v` on the front, whether or not the caller put one there.** This project
tags `v*` — that is what the release workflow triggers on — so the normalization is safe here and the
alternative (accepting only one spelling) makes a person guess a detail that is invisible in the release
page. The assumption is stated at the code, not implied.

**The fixture had to stop agreeing with the code.** The replay served `releases/download/<version>/` with
the asset named `<version>`, so tag and version were one string and any installer that confused them
passed. That is the reason this reached a release: the test reproduced the layout the installer assumed,
not the layout GitHub has. It now reproduces the difference deliberately, and the case with an
unrelated archive name is the one that makes constructing the name fail loudly.

## Validation

- Against the **released** installer, the new checks fail 8 ways (all three pinned spellings and the
  unrelated-name case), so the fixture now detects the bug it was blind to.
- Against this fix: `install-sh gate: 45 checks passed`.
- Against the live v0.1.0 release, all four forms install and report `git-pair version 0.1.0`:
  `GIT_PAIR_VERSION=v0.1.0`, `GIT_PAIR_VERSION=0.1.0`, `--version v0.1.0`, and unpinned.
  `GIT_PAIR_VERSION=v9.9.9` fails with `could not download …/releases/download/v9.9.9/checksums.txt`,
  naming the tag it tried.
- `mise run release:check` green, and the docs contract green over the README edit.

## Known limitations

- The published v0.1.0 asset is still the broken script. `checksums.txt` lists only the four archives, so
  replacing `install.sh` on that release invalidates nothing anyone verifies — but that is a call about a
  public artefact, recorded under Open questions rather than made here.
- Tag normalization assumes `v*` tags. A release tagged `0.1.0` with no prefix would be looked for as
  `v0.1.0`; the failure message names the tag it tried, so the mismatch is legible rather than mysterious.
- Still unsigned, still `latest`-and-pinned only; nothing here changes that.

## Open questions

- Replace `install.sh` on the published v0.1.0 (`gh release upload v0.1.0 packaging/install.sh --clobber`),
  or leave it and let the next release carry the fix? People who already pinned v0.1.0 keep the broken
  script either way unless the asset is replaced.
- Should `checksum.extra_files` list `install.sh` and `THIRD-PARTY-NOTICES.md`, so the index covers
  everything on the release rather than only the archives? It changes what `sha256sum -c` verifies today.
