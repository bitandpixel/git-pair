package cli

import (
	"context"
	"fmt"
	"strings"

	"gitpair/internal/git"
	"gitpair/internal/reviewref"
)

// A changeset directory in the destination branch with no integration record is work that landed and
// lost its paper trail. Nothing else in git-pair says so. The tree rule (PRD §12) makes a directory the
// destination carries stop being a claim, so the changeset leaves `status` and `queue` at the
// moment it becomes most worth remembering, and the command that would have caught it — `integration
// record` — is the one step the integration contract (PRD §22) allows to be skipped: the merge happens
// outside git-pair, so nothing in this tool sees it.
//
// The detector is the reason that contract is enforceable rather than hoped for, and it is the cheapest
// thing in the design that protects the durable-memory goal: directory present in the destination, no
// integration ref ⇒ landed, unrecorded. It gets its own heading in `queue` rather than a line among the
// skip notes, because the finding is not "nothing to do here" — it is "someone stopped one command
// early, and the history is only in the merge commit until they come back".
//
// The destination branch is the one the queue can name without guessing, which is also where the loss is
// silent: a landing on a release branch still shows its branch, its markers, and its own refusals.

// unrecordedDisplayCap bounds the printed list. A repository with fifty unrecorded landings has a
// workflow problem that a fifty-line queue will not fix, and the queue is also a notification surface —
// so it prints a counted sample and `--json` carries the whole answer.
const unrecordedDisplayCap = 10

// unrecordedLanding is one changeset whose landing nobody recorded, and the way to close the gap. The
// invocation is part of the finding rather than a hint: the state exists because someone did not know
// they were supposed to run a command, and the report that names it is the one that gets followed.
type unrecordedLanding struct {
	Changeset string `json:"changeset"`
	Command   string `json:"command"`
}

// refIndex is one read of the durable namespace, indexed by changeset.
//
// It exists because three questions in `queue` are questions about the same refs — has this changeset
// landed, does this orphan have a chain to read, which landings carry no record — and each of them used
// to ask git separately, once per changeset. One `for-each-ref` answers all three, and the count of git
// invocations a queue makes stops following the number of changesets a repository has ever had.
type refIndex struct {
	// Archive holds the changesets whose unsquashed chain this clone can read.
	Archive map[string]string
	// Integrated holds the changesets whose landing this clone has a record of, and the commit each
	// record names. It is keyed on the fact rather than the layout: a landing written down under the
	// retired `refs/git-pair/changesets/<id>/integration` name is written down (see KindLegacyIntegration).
	Integrated map[string]string
	// IntegratedRef is the ref that holds each of those records, keyed the same way. It is in the index
	// because the layout is not uniform — a retired-layout record lives under a different name than the
	// one its id would suggest — and a surface that names a ref has to name the one that exists.
	IntegratedRef map[string]string
	// NamespaceEmpty says this clone holds no durable git-pair ref of any kind. That is a fact about the
	// fetch, not about any changeset (PRD §13.4), and it belongs in the index because the read that
	// produced the answers already knows it — a caller that printed a fetch hint per changeset would be
	// blaming the work for a clone.
	NamespaceEmpty bool
}

func indexDurableRefs(ctx context.Context, repo *git.Repo) (refIndex, error) {
	idx := refIndex{
		Archive:       map[string]string{},
		Integrated:    map[string]string{},
		IntegratedRef: map[string]string{},
	}
	entries, err := reviewref.List(ctx, repo)
	if err != nil {
		return idx, err
	}
	idx.NamespaceEmpty = len(entries) == 0
	for _, e := range entries {
		switch e.Kind {
		case reviewref.KindArchive:
			idx.Archive[e.ID] = e.SHA
		case reviewref.KindIntegration, reviewref.KindLegacyIntegration:
			idx.Integrated[e.ID] = e.SHA
			idx.IntegratedRef[e.ID] = e.Ref
		}
	}
	return idx, nil
}

// unrecordedLandings asks which of the directories the destination carries that no integration record
// names. It costs nothing: the answer is the difference between a tree listing the branch scan already
// took and the index above.
//
// The returned slice is never nil. This key is the answer to a question, so `[]` and null would be
// answers to different questions — and a supervisor agent reading `--json` cannot tell a repository with
// nothing unrecorded from a build old enough not to have been asked.
func (idx refIndex) unrecordedLandings(trunkIDs []string) []unrecordedLanding {
	found := []unrecordedLanding{}
	for _, id := range trunkIDs {
		if _, ok := idx.Integrated[id]; ok {
			continue
		}
		found = append(found, unrecordedLanding{
			Changeset: id,
			Command:   "git pair integration record --changeset " + id,
		})
	}
	return found
}

// printUnrecorded writes the queue's own section for the finding, and the one note that keeps both
// readings of it alive: nobody wrote the record, or somebody did and this clone has not been shown it.
// The note is single and shared for the same reason §13.4's is — an unfetched clone is one condition,
// not one per changeset.
func (a *app) printUnrecorded(found []unrecordedLanding, namespaceEmpty bool, dest string, gap bool) {
	if len(found) == 0 {
		return
	}
	// The queue's own spacing puts a blank line *after* each section, so a section that follows one needs
	// none. Its empty answer is a sentence rather than a section, and ends without one.
	if gap {
		a.printf("\n")
	}
	a.printf("LANDED, UNRECORDED\n\n")
	shown := found
	if len(shown) > unrecordedDisplayCap {
		shown = shown[:unrecordedDisplayCap]
	}
	for _, u := range shown {
		a.printf("  %s\n", u.Changeset)
		a.printf("    in %s with no integration record. Record it with:\n", dest)
		a.printf("      %s\n", u.Command)
	}
	if extra := len(found) - len(shown); extra > 0 {
		a.printf("  and %d more (`git pair queue --json` lists every one)\n", extra)
	}
	a.printf("\n")
	if namespaceEmpty {
		a.warn("%s\n", namespaceAbsentWarning)
		return
	}
	a.warn("note: \"no integration record\" means none *in this clone* — a record written where the merge ran arrives with %s\n",
		reviewref.FetchCommand)
}

// unrecordedInStatus is the same finding placed where `status` can put it: on the error that says this
// branch carries no changeset.
//
// That error is the right host. `status` on the destination branch has nothing to report about work in
// progress, and exit 2 is still the honest code — the command asked a question this branch cannot answer.
// What the branch can answer is that the destination holds directories with no record behind them, and a
// reader who is told only "no changeset for this branch" will go looking for a branch they forgot rather
// than for a record they did not write. The original error stays at the front of the message: it is
// still true, and it is the reason the exit code is 2.
func unrecordedInStatus(orig error, found []unrecordedLanding, namespaceEmpty bool, dest string) error {
	if len(found) == 0 {
		return orig
	}
	lines := make([]string, 0, unrecordedDisplayCap)
	for i, u := range found {
		if i == unrecordedDisplayCap {
			lines = append(lines, fmt.Sprintf("  and %d more", len(found)-i))
			break
		}
		lines = append(lines, "  "+u.Command)
	}
	msg := fmt.Sprintf("%s\n\n%s in %s with no integration record: the work landed, and nobody wrote the "+
		"record. Close the gap with\n\n%s\n\n%s",
		orig, plural(len(found), "changeset directory is", "changeset directories are"), dest,
		strings.Join(lines, "\n"), unrecordedHedge(namespaceEmpty))
	return &usageError{fmt.Errorf("%s", msg)}
}

// unrecordedHedge is the one sentence that keeps both readings of the finding available.
func unrecordedHedge(namespaceEmpty bool) string {
	if namespaceEmpty {
		return "this clone holds no refs/git-pair/* refs at all, so \"no record\" here may mean \"not " +
			"fetched yet\": " + reviewref.FetchCommand
	}
	return "\"no record\" here means none in this clone: a record written where the merge ran arrives with " +
		reviewref.FetchCommand
}
