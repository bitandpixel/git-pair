# fix-dev-build-version-not-a-release

## Summary

What `git-pair --version` prints now says how the binary was built, and none of the answers is a
released number:

| built by | `--version` prints |
| --- | --- |
| `go build ./cmd/git-pair` | `git-pair version 0.0.0-untagged` |
| `mise run build`, `mise run build:prod` | `git-pair version 0.0.0-dev+498c623`, with `.dirty` when the tree had uncommitted work |
| goreleaser, from a tag | `git-pair version 0.2.0` — the tag, unchanged by this change |

The fallback was `0.1.0`, which is also a published release, so a build off any commit was
indistinguishable from something everyone could have installed. Review then asked for the open question
to be implemented as well: the dev answer now carries the commit.

## What changed

- `internal/cli/root.go`: `Version = "0.0.0-untagged"`, and its comment now names both things that stamp
  it — goreleaser for a release, `mise run build` for a dev build.
- `scripts/dev-version.sh` (new): prints `0.0.0-dev+<sha>`, with `.dirty` added when the tree holds
  uncommitted work. One script for both build tasks, the same reason `scripts/install-name.sh` exists.
- `.mise.toml`: `build` and `build:prod` pass `-ldflags "-X gitpair/internal/cli.Version=$version"`, and
  the line they print says which version they stamped.
- `internal/cli/version_test.go`: two tests — the fallback cannot read like a release, and the stamp rule
  is still wired up.
- `README.md`: the "From source" paragraph states what each build reports rather than one number.

## Design decisions

**`0.0.0-dev+<sha>` rather than goreleaser's `0.1.1-SNAPSHOT-<sha>`.** The snapshot form carries a
released number, which is the confusion this changeset exists to remove. `0.0.0-dev` cannot be a release,
because a release is named by its tag; the commit is the half of the snapshot form worth keeping, and it
is kept.

**The commit goes in semver build metadata.** `+498c623` is legal, is ignored for precedence, and leaves
the string a version to anything that parses it — so a dev build can never outrank a release, and
`--version` stays machine-readable.

**`untagged` for the fallback, `dev` for the stamp.** They answer different questions. The fallback means
nothing stamped this build and there is nothing to say where it came from; the stamp means a build of
this commit. Both are impossible as release names.

**`.dirty` from `git diff --quiet HEAD`,** which covers staged and unstaged work, so a build of someone's
edits does not claim to be a build of the commit. Untracked files do not count, matching
`git describe --dirty`.

**A script rather than the text inline in the task.** `install-name.sh` set that precedent for the binary
name, and the new test pins the part that matters: both tasks still call the script and still hand the
value to the linker. A task silently dropping the flag is the failure mode nobody would notice until a
bug report named `0.0.0-untagged`.

**Not `git describe`.** Its string (`v0.2.0-4-g6708d9e`) starts with a tag's `v` and reads like a tag,
and the tag distance is not what a report needs. The sha is.

## Validation

- On a dirty tree, `sh scripts/dev-version.sh` → `0.0.0-dev+6708d9e.dirty`; on the clean tree it is the
  same string without `.dirty`.
- `mise run build` printed `installed ~/.local/bin/git-pair-dev-build-version-not-a-release at
  0.0.0-dev+6708d9e.dirty`, and that binary reports the same from `--version`.
- The release path is unaffected, checked rather than assumed: building with
  `-ldflags "-s -w -X gitpair/internal/cli.Version=0.2.0"` prints `git-pair version 0.2.0`, and the
  published v0.2.0 archives come from goreleaser's own ldflags in `.goreleaser.yaml`, which this change
  does not touch.
- `gofmt` clean, `go vet` clean, `go test ./...` green, and `mise run gates` green — the scripted
  replays included, which matter here because `gates` starts from `mise run build` and so exercises the
  task that was edited.
- A plain `go build` still prints the fallback: `git-pair version 0.0.0-untagged`.

## Known limitations

- Untracked files do not make a build dirty, so a build whose only local change is a new untracked file
  prints no `.dirty`. That is `git describe --dirty`'s rule and it is adopted deliberately; a build with
  untracked-but-unused files is the same program as one without them.
- `--version` does not name the branch. The install name does (`git-pair-tui-mobile-scroll`), and the
  stamp adds the commit, which is the half that survives being deleted.
- A build from a tagged commit that skips the stamp prints the fallback. Detecting that would mean
  reading `git describe` at build time, which is what the script now does for the stamped path.

## Addressed feedback

- The open question — carry the commit the way goreleaser's snapshots do — was answered in review, and is
  implemented here: `scripts/dev-version.sh` plus the two build tasks, with the commit in build metadata
  and a `.dirty` marker. The one place it departs from the question's wording is the value itself, for
  the reason in Design decisions: goreleaser's `0.1.1-SNAPSHOT-<sha>` shape puts a released number back
  into a dev build's `--version`.

## Open questions

- Should published artefacts carry the commit too (`0.2.0+498c623`)? It is a one-line change to
  `.goreleaser.yaml`, but the published number is what people pin, quote and compare, and a suffix there
  is a different argument from the one in this changeset. Deliberately left alone.
