# fix-dev-build-version-not-a-release

## Summary

`git-pair --version` from a source build no longer prints a released number. The fallback at
`internal/cli/root.go:Version` was `0.1.0`, which is also a published release, so a dev build was
indistinguishable from something everyone could have installed. It is now `0.0.0-untagged`.

## What changed

- `internal/cli/root.go`: `Version = "0.0.0-untagged"`, and the comment above it now says why the
  fallback has to name a state rather than a release. The rest of that comment — why it is a `var` and
  what stamps it — is unchanged and still correct.
- `README.md`: the build-from-source sentence in "Building from source" quoted `git-pair version 0.1.0`,
  which is the thing this fixes, so it quotes the new value.
- `internal/cli/version_test.go` (new): pins the shape of the fallback so it cannot drift back to a
  plausible release number.

## Design decisions

**Semver-shaped, with a prerelease suffix.** `0.0.0-untagged` keeps `--version` machine-readable and
sorts below every real release, while reading as "not a version" to a human. A bare `dev` would print
`git-pair version dev`, which is neither parseable nor a version at all, and a plain `0.0.0` reads like
a release that forgot its number.

**Why `untagged` rather than `dev`.** It names the condition that produced the value: goreleaser stamps
`-X gitpair/internal/cli.Version={{.Version}}` for a release (`.goreleaser.yaml`), so anything still
carrying the fallback was built from a commit with no tag. `dev` names an intention that a build from
trunk does not necessarily have.

**No leading `v`.** The tag owns the `v`; goreleaser stamps the de-prefixed value, and cobra prints
`git-pair version <Version>`. The test pins that too, so the fallback cannot become the one case that
prints the tag's shape.

**The test constrains the shape, not the literal.** It requires a `-` and forbids a leading `v`, rather
than asserting the exact string, so the value can be reworded without a test change while a regression
to `0.1.0` cannot pass.

## Validation

- `TestVersionFallbackNamesNoRelease` passes.
- Before: `git-pair --version` → `git-pair version 0.1.0`. After: `git-pair version 0.0.0-untagged`.
- The release path is unaffected, checked rather than assumed: `go build
  -ldflags "-s -w -X gitpair/internal/cli.Version=0.2.0"` printed `git-pair version 0.2.0`, so the
  stamped value still wins over the fallback.
- `gofmt` clean, `go vet ./internal/cli/` clean, `go test ./...` green.
- Nothing else depended on the old default. `internal/cli/contract_test.go` only asserts `--version`
  exits 0; `scripts/gates/install-sh.sh:175-178` asserts the *installed release artefact* reports the
  version it was built as, which is the stamped value, not this one.

## Known limitations

- A build from a tagged commit that skips the ldflags still prints the fallback. Detecting that would
  mean reading `git describe` at build time, which is a different change.
- `--version` still cannot name the commit a dev build came from. Two dev builds print the same string.
  That was true of `0.1.0` as well, and it is now at least honest about being neither.

## Open questions

- Should the dev answer carry the commit, as goreleaser's snapshots do (`0.1.1-SNAPSHOT-498c623`)? That
  needs the stamp at build time, so `mise run build` would have to inject it — a change to the build
  task rather than to this line.
