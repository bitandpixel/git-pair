# feat-about-template

## Summary

The `ABOUT.md` scaffold was a Go string. Every repository got the same six headings, and a team whose
reviewers read something else had nowhere to say so short of a fork. The scaffold is now resolved per
repository: `.git-pair/about-template.md` when the repository has one, the built-in PRD §6 headings
otherwise, with `{{slug}}` replaced by the changeset id.

`git pair init`, `git pair review about` and the check that reports an author who never described the
change all go through the same resolver, which is the part that makes the feature hold up: a custom
scaffold is recognised as a scaffold, so the stderr nudge at `change ready` still fires for a team that
never wanted the built-in headings.

## What changed

| File | Change |
| --- | --- |
| `internal/changeset/changeset.go` | `AboutTemplateFile` (`.git-pair/about-template.md`) and `AboutSlugToken` (`{{slug}}`) beside the other standard names; the built-in scaffold became the constant `aboutTemplateBuiltIn`, so `AboutTemplate(slug)` is a render of it rather than a separate spelling; `ResolveAboutTemplate(repo, slug)` implements the order (file, blank file → built-in, read error → error); `AboutIsUntouched(about, template)` holds the line rule `change ready` relies on; `Write` and `EnsureAbout` resolve the scaffold before writing anything |
| `internal/cli/change.go` | `aboutIsTemplate` lost its unused `ctx`, reads the resolved scaffold and the built-in, and delegates the line comparison to `AboutIsUntouched` |
| `internal/cli/review.go` | `review about` creates the file through `cs.EnsureAbout` instead of naming the built-in scaffold at a second call site |
| `internal/cli/init.go` | the `described` call site follows the new signature |
| `internal/changeset/about_template_test.go` | new: resolution and token substitution, blank file, unreadable file, `Write`, `EnsureAbout` (including that the second call leaves the file alone), the `AboutIsUntouched` table, and the built-in round-trip |
| `internal/cli/about_template_test.go` | `init` writes the repository's template and leaves the template out of its commit; `change ready` warns against a custom scaffold, and against a built-in scaffold after the repository adopted a template |
| `README.md` | the quickstart sentence, and a paragraph in Configuration |
| `PRD.md` | §6 gains `### The scaffold` |
| `skills/git-pair/SKILL.md`, `skills/git-pair/references/cli.md` | the agent is told to fill in the headings the file actually has, and the `init` row names the template and its `{{slug}}` token |

## Design decisions

- **A committed convention file, not a config key.** The section list is a statement about how a team
  reviews, which is a property of the repository, and a committed file is the shape of a decision that a
  repository makes once and every later checkout inherits. A `git-pair.aboutTemplate` key would have
  allowed two clones of one repository to write different `ABOUT.md` files, and the reviewer reads the
  file, not the setting. This also keeps `init` from needing a second config read: the resolver is one
  `ReadFile` of a repository path, with no precedence ladder to document.
- **`init` never commits the template.** It commits only `changesets/<id>/`, which is what keeps a staged
  index staged; the template is an input to a changeset rather than part of one, and an author can try
  one out without staging it.
- **A blank file counts as no template.** The alternative — an empty `ABOUT.md` — hands the reviewer a
  blank page with no way to tell whose setting failed. Writing the built-in instead means the existing
  "still has the empty `init` template" warning says what happened, in the place the author already reads.
- **A file that exists and cannot be read is an error.** `init` writing a document other than the one the
  repository names is worse than `init` failing, and the author who committed a template expects their
  template. `Write` reads the scaffold before it creates the directory, so the failure leaves no
  half-built changeset — a `CHANGESET.yaml` with no `ABOUT.md` is state every later command then has an
  opinion about. `aboutIsTemplate` is the one place that swallows the same error, because it only gates an
  informational warning and the file it is reading may well be fully described.
- **The warning checks the resolved scaffold and the built-in.** A changeset scaffolded before the
  repository adopted a template still holds the built-in headings, and a reviewer cannot tell the two
  scaffolds apart. Measuring it against only the current template would let an undescribed changeset
  reach the queue silently — the failure the warning exists for.
- **The line rule is unchanged, only moved.** The title line is not compared, and every remaining line
  must match after `TrimSpace` with the trailing newline trimmed. That is what `aboutIsTemplate` did
  before, which means the built-in case behaves identically; moving it into `changeset` keeps the
  template's text and the detection of that text in one package, and makes the rule testable without a
  subprocess.
- **Nothing else is configurable here.** `ThreadTemplate` stays built-in; one scaffold at a time is the
  scope of this change.

## Validation

- `go test ./internal/changeset/` — the new file passes, including the case that the built-in scaffold is
  recognised as untouched (which is what makes the `ready` warning reachable at all), and the case that an
  unreadable template makes `Write` create no directory.
- `go test ./internal/cli/ -run 'About|Scaffold|Template'` — the three new CLI tests, the pre-existing
  scaffold warning, and the `init --about` family.
- `gofmt -l internal cmd` and `go vet ./...` clean.
- `mise run check` — the whole sharded suite, including `docs_contract_test.go`, which checks that the
  command names the new README, PRD and skill prose use are names this code answers to.
- `mise run gates` — the scripted replays CI runs: the PRD §29 loop, the pty TUI walkthrough (which opens
  `ABOUT.md` through the code path `review about` now shares with `init`), and the CI merge job.
- Manual: `git pair init` in this repository wrote the built-in scaffold with no template file present
  (`changesets/feat-about-template/ABOUT.md` is that file), which is the fallback path exercised end to
  end rather than only in a fixture.

## Known limitations

- Discovery is the working tree at the repository root. A template that exists only in `HEAD` and was
  deleted locally is not read, and there is no fallback to committed state; `init` writes a working-tree
  file, so this is the same tree it writes from.
- `{{slug}}` is the only token. There is no branch, base, or date substitution, because a template that
  needs those is asking for a template engine.
- A scaffold check is a text comparison, so it stops recognising a file once its author edits it. That was
  true before and is unchanged; it also means a changeset scaffolded from a template the repository has
  since replaced is unrecognised unless it matches the current template or the built-in.
- No command reports which scaffold it used. `init` prints `created changesets/<id>/ABOUT.md` either way,
  which keeps the output stable and makes the file itself the evidence.

## Open questions

- Should `git pair change stack` and `change combine` say which scaffold they wrote, the way they report
  the base they recorded? Both create a changeset directory through `changeset.Write` and are silent today.
- Should a repository-level template ever be per-directory, for a monorepo whose packages review
  differently? Nothing in the current rule forbids adding that later, and nothing asks for it yet.
