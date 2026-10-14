package cli

import (
	"strings"
	"testing"
)

// The consolidation this changeset asked for is only real while the documents quote the code. Two sentences
// are printed by four surfaces each, they were spelled in a fifth place, they disagreed about whether to call
// the merge `git pair check` or `git pair merge`, and the child's waiting sentence restated the parent's
// landing — so a change to one builder left three documents behind, and one of them quoted a command this
// build does not answer to. This test reads the documents and compares each quote with what the builders
// produce, so the drift is caught by `mise run check` rather than by a reader.
func TestDocumentsQuoteTheLandingSentencesTheCodeBuilds(t *testing.T) {
	// The fixture the prose uses: a changeset `booking-transaction` stacked on `booking`, whose destination
	// is `main`. The builders, not the documents, decide the wording.
	const (
		parent      = "booking"
		branch      = "booking-transaction"
		destination = "main"
	)
	waiting := waitingNextAction(landingContract{Destination: destination, WaitingOn: parent})
	landing := landingNextAction(landingContract{Destination: destination})
	handoff := handoffLine(landingContract{Destination: destination}, branch)
	// The same line with the branch left as a placeholder, which is how prose quotes a command it cannot
	// name: the words around it still come from the builder.
	handoffCommands := handoffLine(landingContract{Destination: destination}, "<branch>")
	start := strings.Index(handoffCommands, "`git pair change integrate`")
	if start < 0 {
		t.Fatalf("the hand-off line does not name the declaration command: %q", handoffCommands)
	}
	handoffCommands = handoffCommands[start:]

	quotes := map[string][]string{
		// The canonical statement quotes all three sentences in full.
		"../../PRD.md": {waiting, landing, handoff},
		// The human guide and the agent reference quote the waiting sentence and the hand-off's commands,
		// in their own surrounding prose.
		"../../README.md":                         {waiting, handoffCommands},
		"../../skills/git-pair/references/cli.md": {waiting, handoffCommands},
	}
	for path, want := range quotes {
		text := quoteText(readDoc(t, path))
		for _, sentence := range want {
			if !strings.Contains(text, quoteText(sentence)) {
				t.Errorf("%s does not quote what the code builds.\n  built: %s\n  looked for that wording anywhere in the document", path, sentence)
			}
		}
	}

	// The wording the consolidation removed, kept out on purpose. "land it first" was the child's sentence
	// spelling the parent's step, and it is what a screen printed while `change integrate` refused the
	// changeset it described. The sweep runs over every checked document rather than a list written here, so
	// a new reference page is covered the day it is added — the same list the docs contract uses.
	for _, path := range docFiles(t) {
		text := quoteText(readDoc(t, path))
		if strings.Contains(text, "land it first (git pair check") {
			t.Errorf("%s restates the parent's landing inside the child's next step; the sentence refers the reader to `git pair status --changeset <parent>` instead", path)
		}
	}
}

// quoteText normalises a document or a built sentence for comparison: markdown spans and emphasis are how a
// document presents a command, and line wrapping is how it fits one on a page, and neither is allowed to be
// the reason a quote test fails. What is compared is the wording.
func quoteText(s string) string {
	s = strings.ReplaceAll(s, "`", "")
	s = strings.ReplaceAll(s, "*", "")
	return strings.Join(strings.Fields(s), " ")
}
