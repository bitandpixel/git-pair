// Prettier configuration for this repository's markdown.
//
// Only markdown is formatted here. `scripts/format.sh` feeds Prettier tracked `.md` files and nothing
// else, so the other parsers this config could steer never see a file: Go is formatted by `gofmt`, which
// `mise run check` enforces, and the YAML under `.github/workflows/` and `scripts/ci/` arrives from the
// development profile, which byte-compares what it installed. A formatter with a wider remit would be a
// second owner of bytes somebody else already owns.
//
// This file is `.mjs` rather than `.prettierrc.json` for one reason: JSON carries no comments, and each
// choice below has a reason a reader needs.

export default {
  // Keep the author's line breaks. The prose here is wrapped by hand near `printWidth`, and a formatter
  // that re-wraps rewrites every paragraph the wrap touches: a one-sentence edit becomes a whole-paragraph
  // diff, and a reviewer reading `changesets/<id>/ABOUT.md` sees lines they never changed. Wrapping is a
  // writing decision here, not a formatting one.
  proseWrap: "preserve",

  // The column the hand-written prose already follows, and the one that governs anything Prettier itself
  // puts on a line. It is a width rather than a limit: a markdown table row is one line no matter how
  // wide its cells, and most of this repository's over-long lines are table rows.
  printWidth: 120,

  // Two spaces for continuation lines under a list item, which is what `- ` plus one space aligns to.
  tabWidth: 2,

  // Leave the contents of a fenced block alone. A fenced block in this repository's documents is evidence:
  // a transcript a gate captured, the JSON `--json` answers with, the command tree §8 draws. Prettier's
  // default formats embedded code it recognises — it reformatted a JSON example in README's transcript —
  // which rewrites a quoted artefact into something the command no longer prints. Code inside a fence is
  // written by the thing it shows, not formatted here.
  embeddedLanguageFormatting: "off",

  // The tree is LF throughout. Left to itself, Prettier keeps whatever a file already uses, so a checkout
  // that writes CRLF would produce a file the check calls formatted and the next checkout rewrites.
  endOfLine: "lf",
};
