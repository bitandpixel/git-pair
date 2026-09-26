package cli

import (
	"context"
	"fmt"
	"sort"

	"gitpair/internal/git"
	"gitpair/internal/reviewref"
)

// A record can exist in two places at once and mean two different things. In this clone it is the fact
// that the landing happened; on the shared remote it is the fact that *everyone* can still learn that.
// The refs carry the memory for twelve months (§13), and a year of memory in one laptop is a year of
// memory that ends when the laptop does.
//
// This finding is the difference between the two. `LANDED, UNRECORDED` asks whether anyone wrote the
// record; `RECORDED, NOT PUBLISHED` asks whether the record got anywhere. It belongs beside the first,
// not inside it, because the person who stopped early and the person who never pushed are different
// people running different commands, and because a record that exists only locally is the one state
// where the paper trail is complete and still worthless.
//
// The comparison is against this clone's mirrors of the remote — `refs/remotes/<remote>/refs/git-pair/*`,
// which `--fetch` brings — and never against the remote itself. "As far as I know, as of the last time
// I asked" is the honest claim, and asking is `--fetch`'s job rather than this one's (§13).

// unpublishedPair is one changeset whose record this clone holds and whose remote — as this clone last
// saw it — does not hold, or does not hold the same.
//
// `Missing` and `Diverged` name the two halves of the pair rather than collapsing them into a count,
// because the two shapes mean different things: a missing family means the remote cannot reconstruct the
// record at all, and a diverged one means the remote and this clone disagree about what the landing was,
// which no amount of pushing the other half fixes.
type unpublishedPair struct {
	Changeset string   `json:"changeset"`
	Missing   []string `json:"missing"`
	Diverged  []string `json:"diverged"`
}

// publicationReport is the whole answer to "has the record travelled", findings and all, including the
// case where the question could not be asked.
type publicationReport struct {
	// Findings is never nil: an empty list is the answer "nothing is unpubished", and a missing key is
	// the answer "this build was not asked".
	Findings []unpublishedPair `json:"unpublished"`
	// Note says why Findings is empty when the reason is that nothing could be compared — no remote, or
	// a mirror namespace this clone has never fetched. It is one sentence about the clone, never one per
	// changeset, because an unfetched clone is one condition and blaming it on forty changesets would be
	// both wrong and unreadable.
	Note string `json:"unpublished_note,omitempty"`

	// remote is the remote the comparison was made against, for the wording. It stays unexported so the
	// JSON says what was found rather than how the lookup was wired.
	remote string
}

// publicationReport reads this clone's mirrors and compares. It costs one `for-each-ref` plus the remote
// lookup, and no network: the mirrors are already here or they are not, and finding out is `--fetch`.
// `asked` says this run fetched. It is what separates the two ways the mirror namespace can be empty,
// which nothing else can tell apart: the remote may hold no durable refs at all — which is the finding,
// in its loudest form — or this clone may never have asked. Mirrors that exist are evidence of asking,
// so the flag only matters when there are none, and the rule is: with mirrors, compare; having just
// fetched, compare; otherwise say nothing can be claimed.
func (a *app) publicationReport(ctx context.Context, repo *git.Repo, branch string, idx refIndex, remedy string, asked bool) publicationReport {
	rep := publicationReport{Findings: []unpublishedPair{}}
	remote, err := a.remoteForDurableRefs(ctx, repo, branch)
	if err != nil || remote == "" {
		rep.Note = "nothing here can say whether a record was published: this repository has no remote to compare against"
		return rep
	}
	rep.remote = remote
	present, err := reviewref.MirrorPresent(ctx, repo, remote)
	if err != nil {
		rep.Note = fmt.Sprintf("nothing here can say whether a record was published: %s could not be read (%s)",
			reviewref.MirrorRoot(remote), err)
		return rep
	}
	if !present && !asked {
		rep.Note = fmt.Sprintf("nothing here can say whether a record reached %s: this clone has never fetched %s — %s, and %s",
			remote, reviewref.MirrorRoot(remote), remedy, configureFetchNudge)
		return rep
	}
	mirrors, err := reviewref.RemoteList(ctx, repo, remote)
	if err != nil {
		rep.Note = fmt.Sprintf("nothing here can say whether a record was published: %s could not be read (%s)",
			reviewref.MirrorRoot(remote), err)
		return rep
	}
	rep.Findings = unpushedPairs(idx, mirrors)
	return rep
}

// unpushedPairs compares the pairs this clone holds against the mirrors. Only changesets with an
// integration ref are considered: a landing nobody recorded is `LANDED, UNRECORDED`'s finding, and
// listing it twice would report one missing action as two problems.
func unpushedPairs(idx refIndex, mirrors []reviewref.Entry) []unpublishedPair {
	at := map[string]map[string]string{
		familyIntegration: {},
		familyArchive:     {},
	}
	for _, m := range mirrors {
		switch m.Kind {
		case reviewref.KindIntegration:
			at[familyIntegration][m.ID] = m.SHA
		case reviewref.KindArchive:
			at[familyArchive][m.ID] = m.SHA
		}
	}
	ids := make([]string, 0, len(idx.Integrated))
	for id := range idx.Integrated {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	found := []unpublishedPair{}
	for _, id := range ids {
		p := unpublishedPair{Changeset: id, Missing: []string{}, Diverged: []string{}}
		for _, family := range []string{familyIntegration, familyArchive} {
			local, ok := localRef(idx, family, id)
			if !ok {
				continue
			}
			remoteSHA, onRemote := at[family][id]
			switch {
			case !onRemote:
				p.Missing = append(p.Missing, family)
			case remoteSHA != local:
				p.Diverged = append(p.Diverged, family)
			}
		}
		if len(p.Missing) > 0 || len(p.Diverged) > 0 {
			found = append(found, p)
		}
	}
	return found
}

func localRef(idx refIndex, family, id string) (string, bool) {
	switch family {
	case familyIntegration:
		sha, ok := idx.Integrated[id]
		return sha, ok
	case familyArchive:
		sha, ok := idx.Archive[id]
		return sha, ok
	}
	return "", false
}

const (
	familyIntegration = "integration"
	familyArchive     = "archive"
)

func familyLabel(family string) string {
	if family == familyIntegration {
		return "integration record"
	}
	return "archive"
}

// printUnpublished writes the finding as its own section. The heading is deliberately parallel to
// `LANDED, UNRECORDED`: the two read alike so a reader who knows one knows the other, and they differ in
// exactly the word that matters.
func (a *app) printUnpublished(rep publicationReport, gap bool) {
	if rep.Note != "" {
		a.printf("%s\n", rep.Note)
		return
	}
	if len(rep.Findings) == 0 {
		return
	}
	if gap {
		a.printf("\n")
	}
	a.printf("RECORDED, NOT PUBLISHED\n\n")
	shown := rep.Findings
	if len(shown) > unrecordedDisplayCap {
		shown = shown[:unrecordedDisplayCap]
	}
	for _, p := range shown {
		a.printf("  %s\n", p.Changeset)
		for _, family := range p.Missing {
			a.printf("    %s: not on %s\n", familyLabel(family), rep.remote)
		}
		for _, family := range p.Diverged {
			a.printf("    %s: %s holds a different commit\n", familyLabel(family), rep.remote)
		}
		// Half a pair is its own case, and it is the worse one. With one family on the remote the
		// reader has a hint that something happened and a record that cannot be reconstructed from it —
		// `archive` alone proves a chain existed, `integration` alone names a commit nobody can tie to
		// reviewed work. It gets said out loud rather than left to be inferred from one line being
		// missing.
		if len(p.Missing) == 1 {
			a.printf("    half published: %s holds %s and not %s, so the record cannot be reconstructed there\n",
				rep.remote, familyLabel(oppositeFamily(p.Missing[0])), familyLabel(p.Missing[0]))
		}
	}
	if extra := len(rep.Findings) - len(shown); extra > 0 {
		a.printf("  and %d more (`--json` lists every one)\n", extra)
	}
	a.printf("\n")
}

func oppositeFamily(family string) string {
	if family == familyIntegration {
		return familyArchive
	}
	return familyIntegration
}
