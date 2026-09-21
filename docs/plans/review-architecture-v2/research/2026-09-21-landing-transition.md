# Landing is a visible transition in the destination branch

Measured 2026-09-21 in a scratch repository (`/tmp/trans`), for M3's derivation of the integration tip and
M4's unrecorded-landing detector. Both depend on the same property: because a changeset's directory is
committed on its branch and PRD §4's tree rule keeps landed directories in the destination branch, landing
a changeset changes trunk's tree in a way that identifies the landing commit without inference.

## The rule

> The landing commit for changeset `<id>` is the newest commit on the destination branch's **first-parent**
> line whose tree contains `changesets/<id>/` and whose first parent's tree does not.

First-parent matters: a `--no-ff` merge brings in the whole branch history, and scanning ancestry rather
than the first-parent line would find the branch's own commits, which are not the landing.

## Setup

A destination branch `main`, and `feature/foo` holding one implementation commit, a `changesets/foo/`
directory with `CHANGESET.yaml`, and an empty approving review commit carrying
`Review-Outcome: approve` / `Review-Changeset: foo` / `Review-Head: <impl sha>`.

## Result A — `--no-ff` merge

```
$ git merge --no-ff feature/foo -m "Merge feature/foo"
$ git rev-list -1 --first-parent HEAD
261cec2af2f7c14fd802c57e022e8c8803ce28f9      here=1  first parent here=0
```

The rule names the merge commit, not any of the merged branch's commits, and not `base`.

## Result B — squash

```
$ git merge --squash feature/foo && git commit -m "feat: foo"
$ git rev-list -1 --first-parent HEAD
5cf24a24d3e94754f75b8d48bd8b23b203672721      here=1  first parent here=0
```

Same rule, same shape of answer. Squash destroys the ancestry between the reviewed head and the landing
commit, which is precisely why the landing commit has to be *recorded* rather than derived by ancestry —
and precisely why recording it is cheap: the tree transition identifies it.

## Result C — the chain an archive ref has to keep alive

```
$ git log --oneline main..feature/foo
fd9b0a1 review: approve foo
646132a add impl
```

Two commits after squash, reachable from nothing on `main`. Under the squash case the branch is the only
thing holding them, which is the case `refs/git-pair/archive/<id>` exists for, and the reason it must be
written while the branch is still present.

## What this cost

One `ls-tree` per candidate commit against a `grep -c` of the directory prefix — the same
`DirsAt`-shaped call the resolver already makes once per command. Folding integration refs into the existing
`reviewref.List` pass means the detector for M4 adds no git invocation at all; S2 measures that on the real
resolver rather than on this fixture.

## Not measured — carried into S1

- One merge landing **several changesets** at once: the rule is per-directory, so each should resolve to
  the same commit, but this is the case most likely to make check 4 in M3 refuse a legitimate landing.
- A **child landed before its parent**, where the child's tree also contains the parent's
  `changesets/<parent>/` directory: the rule must not attribute the parent's arrival to the child's landing.
- A **cherry-pick** of a changeset branch, which lands the directory with none of the history behind it.
