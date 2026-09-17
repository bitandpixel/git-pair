# Ref namespace reality check

Measured 2026-09-17 on this repo and a scratch fixture, before planning the namespace move. The
question is whether the plumbing the requirements ask for actually exists, and what has to be migrated.

## Existing refs to migrate: none

```console
$ git for-each-ref refs/reviews | wc -l
0
```

`refs/reviews/*` is written by `change ready`, `review submit`, `change abandon` and (until this plan)
`change complete`, but this repository has never completed a reviewed changeset under those commands, so
no ref exists locally. Nothing has been pushed either — git-pair runs no `push` (PRD §26), so
`refs/reviews/*` has never left a machine.

Consequence: the namespace move is a code change with no migration story to tell. It also means the
"re-anchor on the next state command" fallback is untested in practice rather than merely
theoretical, and worth a test that covers a ref written under the old name being ignored.

## Primitives verified

All four against a scratch fixture at git 2.43.0 (the version README develops against).

**Discovery by `--points-at` (§16) works, and returns the ambiguity the spec anticipates (§19).**

```console
$ git update-ref refs/git-pair/changesets/one/archive $A
$ git update-ref refs/git-pair/changesets/two/archive $A
$ git for-each-ref --points-at $A --format='%(refname)' refs/git-pair/changesets/
refs/git-pair/changesets/one/archive
refs/git-pair/changesets/two/archive
```

Two changesets pointing at one commit is *not* reachable by ordinary use, which the first reading of
this spike got wrong. Branches can share a head — a child with no commits of its own sits on its
parent's head — but archive refs cannot, because every command that moves one commits something first:
`marker.Commit` calls `repo.Commit` and returns the new head (`internal/marker/marker.go:108`), and
`review submit` and `change abandon` reach the ref the same way. A ready marker, a review submission and
an abandonment are each new commits, so a ref always names a commit that did not exist a moment earlier.
A child branch created at the parent's head also inherits the parent's `changesets/<id>/`, so it is the
same changeset and the same single ref, not a second one.

So `--changeset` is the recovery path for refs moved outside git-pair — a hand-run `update-ref`, a
restored or copied namespace — rather than something ordinary stacked work produces. It is still
mandatory: §19 asks for it, it costs one flag, and when a namespace has been hand-edited it is the only
way to record the truth without first repairing the ref. The test for it has to build the collision
directly, since no sequence of commands will.

`--points-at` does not take a glob, so the namespace is listed and the `/archive` suffix filtered in
process.

**Create-only ref write (§22) is enforced by git itself.**

```console
$ git update-ref refs/git-pair/changesets/one/integration $B ""      # ok
$ git update-ref refs/git-pair/changesets/one/integration $A ""
fatal: update_ref failed for ref '.../integration': cannot lock ref '.../integration':
reference already exists
```

The third argument being empty means "must not exist", so immutability does not depend on us
serialising correctly. But the failure is a `fatal:` from git with exit 128, not the message §22 asks
for, so the command must check for an existing integration ref first and print §22's text itself. The
create-only write stays as the backstop for a race, not as the user-facing check.

**A ref cannot be both leaf and namespace (§8), enforced by git.**

```console
$ git update-ref refs/git-pair/changesets/one $A
fatal: ... 'refs/git-pair/changesets/one/archive' exists; cannot create 'refs/git-pair/changesets/one'
```

No code is needed to honour §8; the shape is impossible. Worth a comment in `reviewref` so nobody
"fixes" it later by flattening the namespace to `refs/git-pair/changesets-<id>-archive`.

**Ancestry for §21.7 verification is available and permitted.**

```console
$ git merge-base --is-ancestor $B HEAD && echo yes
yes
```

`merge-base` is explicitly outside the hygiene test's forbidden list — `internal/hygiene/hygiene_test.go`
notes that "`merge-base` is a different word and never matches" the `merge` prohibition. So the ancestor
helper deleted in the anchored-lifecycle plan can come back, with a narrower job: verifying a target the
caller named, not deriving whether work landed. That distinction is the reason the deletion was right
and the re-addition is not a reversal.

## Blast radius of the rename

`refs/reviews` appears in 20 files: five shipped Go files (`reviewops`, `reviewref` users in `cli`),
`PRD.md`, and the `gitpr-mvp` `e2e-29.sh` gate, plus completed plans and audits that are historical
records and stay as written. The live gates are the ones that must move with the code.

## Open question left standing

`SlugFromBranch` (`internal/changeset/changeset.go:73`) already matches §2.1's example exactly, so the
default is unchanged by this plan. What is *not* decided by the spec: whether an explicit `--id` must
also be the directory name. The plan takes the position that it must (§4's diagram says
`changesets/<id>/`), which makes `id:` in `CHANGESET.yaml` a cross-check rather than a second source,
and gives §6's mutability rule something concrete to reject.
