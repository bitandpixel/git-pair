# feat-publish-binary

## Summary

Publishes git-pair as binaries a person can install with one command. Until now the only route was
`go build`, which asks every prospective user for a Go 1.27 toolchain before they can find out whether
the tool is worth a minute of their attention.

The change is the pipeline and the checks around it, not just the artefacts: a release that publishes
before its installer has run is a release people install and then complain about. Every step short of the
tag has a task, and the tag's own workflow proves the installer before it publishes.

## What changed

- `.goreleaser.yaml` — darwin/linux × amd64/arm64, `CGO_ENABLED=0`, `-trimpath`,
  `-X gitpair/internal/cli.Version={{.Version}}`, `checksum: name_template: checksums.txt`, archives
  carrying README.md and LICENSE, release assets carrying `install.sh` and `THIRD-PARTY-NOTICES.md`,
  `changelog` disabled with a written `release.header` in its place, `preflight.fail_on_error`.
- `.github/workflows/release.yml` — triggers on `v*` and on `workflow_dispatch`. Builds, replays the
  installer, publishes only if the replay passed. `permissions: contents: write` is the whole grant;
  no repository secret.
- `packaging/install.sh` — the one-liner. POSIX sh, no sudo, no array, no `[[`.
- `scripts/gates/install-sh.sh` — replays the installer against a `dist/` directory served over http.
- `scripts/gen-third-party-notices.sh`, and the `packaging/THIRD-PARTY-NOTICES.md` it writes.
- `internal/cli/root.go` — `const Version` becomes `var Version`.
- `.mise.toml` — goreleaser pinned beside Go, and `release:check`, `release:snapshot`, `release:gate`.
- `LICENSE` (MIT). README gains the published-binary half of Install, the fourth gate row, the release
  task table, and a License section.

## Design decisions

**One repository for source and releases.** The alternative was a private source plus a public
artefact repository, which needs a fine-grained personal access token as a repository secret: a runner's
`GITHUB_TOKEN` is scoped to the repository that runs the workflow and cannot write anywhere else.
Publishing into `origin` needs `contents: write` and nothing else, so the credential and its rotation are
gone. The config is the second shape of this file; the first named `release.github.owner`, `name` and
`token`.

**The build runs twice, on purpose.** Snapshot first so `scripts/gates/install-sh.sh` can run against
real artefacts, then publish. The minute it costs buys the difference between a broken installer failing a
build and a broken installer shipping.

**`latest` reads its version from `checksums.txt`, not from the URL.** `releases/latest/download/…` is a
redirect to the newest release, whose archive is named with the version it actually is, so the first
version of the installer — which built the asset name from the literal word `latest` — asked for
`git-pair_latest_linux_amd64.tar.gz` and got a 404. The gate caught it on its first run. Reading the name
out of the checksums file also resolves the version without the GitHub API and its rate limits.

**The checksum claim is stated at its real size.** It catches a truncated download and the wrong archive.
It does not authenticate the publisher, because `checksums.txt` arrives over TLS from the same origin as
the archive. Both the script and README say so, and README gives the by-hand procedure for anyone who
needs more.

**No sudo, and do not overwrite what we did not write.** An unwritable install directory is reported with
the option that fixes it, not escalated. An existing file at the target that will not answer `--version`
stops the install without `--force` — the same rule `skill install` applies to files it did not create.

**Changelog disabled.** goreleaser would compose release notes from commit subjects that are lifecycle
markers (`git-pair: integrate <slug>`, `review: approve <slug>`), while what a change is made of is in
`changesets/<id>/ABOUT.md`. `release.header` carries the install block instead, and `mode: keep-existing`
leaves whatever the releaser writes afterwards alone.

**Notices from what is linked, not what is mentioned.** `go list -deps ./cmd/git-pair`, not
`go list -m all`, so test-only dependencies are not credited into the binary; licence texts deduplicated
by content hash; a linked module with no licence file in the cache is written into the output as an entry
and exits non-zero.

**The release URL is checked for agreement across three files.** `slug=` in the installer decides where
people download from; README and the release header tell them that URL. A release page pointing somewhere
the installer does not go is invisible until someone installs the wrong thing, and it is one grep to rule
out. goreleaser's own `--snapshot` ignores publish errors, so the `extra_files` globs get checked here too
rather than at publish time.

**goreleaser is pinned in `.mise.toml` the way Go is.** A workflow that installed its own version would be
a second definition of the release, which is the rule `ci.yml` already states for the gate. The config says
`version: 2`, and v2 rejects two names v1 wanted (`archives.format`, `checksums`) — both hit during
development and both now prevented by `release:check`.

## Validation

- `mise run release:check` green. Negative-tested both ways: pointing README's install URL at another
  repository fails with `README.md never names github.com/bitandpixel/git-pair, …`, and naming an
  `extra_files` glob with no file behind it fails with the file named.
- `mise run release:snapshot` produces four archives and `checksums.txt`. The linux/amd64 binary prints
  `git-pair version 0.0.0-SNAPSHOT-e4af7af` — the ldflags path working end to end, which a `const` would
  have refused at link time.
- `bash scripts/gates/install-sh.sh`: 37 checks pass — newest release, pinned version, a release that does
  not exist, a tampered archive, an archive missing from `checksums.txt`, a file in the way, a directory in
  the way, an unwritable directory, an unsupported OS, an unsupported architecture, and the four-target
  count. `dash -n packaging/install.sh` is clean, which is what "POSIX sh" is worth as a claim.
- `mise run gates` green in 126s: gofmt, vet, the sharded suite, the PRD §29 replay, the pty walkthrough,
  and the CI merge replay. The docs contract is green over the new README sections.

## Known limitations

- Windows is not published. It cross-compiles, but the pty and TUI gates are unix-only, so a Windows
  archive would be an untested promise.
- Nothing is signed. No cosign, no minisign, no notarisation — see the checksum claim above.
- The installer runs the binary it wrote to prove it, which proves the host's own architecture only. The
  other three archives are built by the same toolchain invocation and never executed.
- `contents: write` is held for the whole job, not only the publish step. Narrowing it to a job-level
  permission on the publish job would cost the build/replay split.
- The publish path has never run: there is no tag yet. `workflow_dispatch` rehearses everything except the
  upload itself.

## Open questions

- Cut `v0.1.0` now, or first settle what `internal/cli/root.go:Version` should say, since that number is
  what a source build reports and it is the number README quotes?
- A Homebrew tap when someone installs this twice: a `Formula/git-pair.rb` in this repository gives
  `brew tap bitandpixel/git-pair && brew install git-pair`, and goreleaser's `brews` pipe can keep it
  current. Deliberately not built for a first user.
- Should the release body name the changesets that made the release, rather than leaving readers to
  `git log` between tags?
- Does `mise run gates` grow the installer replay once the publish path has proven itself? Not yet: it
  costs a cross-compile and a listening socket on every branch push.
