package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"gitpair/internal/git"
	"gitpair/internal/reviewref"
)

// A record that exists in one clone is a promise that the clone survives. `publish` is the command that
// discharges it: it sends the two durable refs to the shared remote, and it is the only thing in git-pair
// allowed to (PRD §26), because pushing is otherwise a verb this tool has no business owning.
//
// It is a command rather than a flag on `record` for the same reason `record` is a command rather than a
// side effect of the merge: the two actions have different permissions, different failure modes, and
// sometimes different owners. A pipeline may record in a job that can read the repository and publish in
// one that can write it; `record` stays network-free either way, which is what keeps the durable write
// local, fast and testable.
//
// And it is not `record --push`, so that the half of the design that reads — `--fetch`, `status`, `queue`
// — never becomes the half that writes.

// publishFamily names which half of a pair a report line is about.
type publishFamily struct {
	family string
	ref    string
	local  string
}

type publishJSON struct {
	Remote string `json:"remote"`
	// Every array here is present and non-null, published-or-not being an answer to a question.
	Published []publishedRef  `json:"published"`
	Already   []publishedRef  `json:"already_published"`
	Failed    []publishFailed `json:"failed"`
}

type publishedRef struct {
	Changeset string   `json:"changeset"`
	Refs      []string `json:"refs"`
}

type publishFailed struct {
	Changeset string `json:"changeset"`
	// Family is the half that did not arrive: `integration`, `archive`, or both joined by "+".
	Family string `json:"family"`
	// Local and Remote are the two values in the disagreement, short where they exist. Empty means
	// absent: the remote has no such ref, which is a different complaint from a conflicting one.
	Local     string `json:"local,omitempty"`
	Remote    string `json:"remote,omitempty"`
	Reason    string `json:"reason,omitempty"`
	HalfState bool   `json:"half_state,omitempty"`
}

func newIntegrationPublishCommand(a *app) *cobra.Command {
	var remoteFlag string
	cmd := &cobra.Command{
		Use:   "publish [<changeset>…]",
		Short: "Send the durable refs to the shared remote",
		Long: `Send a changeset's two durable refs to the shared remote, unforced.

  refs/git-pair/archive/<id>       refs/git-pair/integrations/<id>

With no arguments, every pair this clone holds is published; named ids publish just those. The remote is
--remote, else the branch's upstream remote, else origin.

Both refs go in one push, without a leading '+'. That is the whole conflict policy: a remote that holds a
different value rejects the push rather than being overwritten by it, because the refs are create-only
(PRD §11.4) and a disagreement between two recorders is a decision, not a race to win. So the output
names both values and never suggests forcing.

The command then re-reads the remote's copies and reports what is actually there, so "published" is a
verified claim rather than the exit status of a command that may have taken one ref of a pair and refused
the other. That half-state is real: git applies the refspecs a server accepts and exits non-zero for the
ones it rejects, on every forge measured.

Publishing is idempotent. A pair the remote already holds is reported as already published and nothing is
written, so CI can run this on every build.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runIntegrationPublish(cmd.Context(), args, remoteFlag)
		},
	}
	cmd.Flags().StringVar(&remoteFlag, "remote", "", "publish to this remote instead of the repository's own")
	return cmd
}

func (a *app) runIntegrationPublish(ctx context.Context, names []string, remoteFlag string) error {
	repo, err := a.loadRepo(ctx)
	if err != nil {
		return err
	}
	remote, err := a.publishRemote(ctx, repo, remoteFlag)
	if err != nil {
		return err
	}

	idx, err := indexDurableRefs(ctx, repo)
	if err != nil {
		return err
	}
	ids, incomplete, err := publishTargets(idx, names)
	if err != nil {
		return err
	}

	view := publishJSON{Remote: remote, Published: []publishedRef{}, Already: []publishedRef{}, Failed: []publishFailed{}}
	if len(ids) == 0 {
		for _, id := range incomplete {
			view.Failed = append(view.Failed, publishFailed{
				Changeset: id, Family: "integration+archive",
				Reason: "this clone holds only one half of the pair, and a half-pair is not a record",
			})
		}
		return a.finishPublish(view, len(incomplete) > 0)
	}

	// The mirrors *before* the push are what distinguishes "we published that" from "it was already
	// there", which is the difference between a build that did something and one that confirmed something.
	before, err := reviewref.RemoteList(ctx, repo, remote)
	if err != nil {
		before = nil
	}
	at := indexMirrorRefs(before, remote)

	specs := make([]string, 0, 2*len(ids))
	var pairs []publishFamily
	for _, id := range ids {
		for _, fam := range []publishFamily{
			{family: familyIntegration, ref: reviewref.Integration(id), local: idx.Integrated[id]},
			{family: familyArchive, ref: reviewref.Archive(id), local: idx.Archive[id]},
		} {
			if fam.local == "" {
				continue
			}
			specs = append(specs, fam.ref+":"+fam.ref)
			pairs = append(pairs, fam)
		}
	}

	pushed, err := repo.PushDurableRefs(ctx, remote, specs...)
	if err != nil {
		return err
	}
	gitReason := map[string]string{}
	for _, o := range pushed.Outcomes {
		if !o.OK {
			gitReason[o.Ref] = strings.TrimSpace(o.Flag + " " + o.Summary)
		}
	}

	// Then verify rather than conclude. The push's exit status is one bit about many refs; what a reader
	// needs is which value the remote holds now.
	if ferr := repo.FetchPruned(ctx, remote, reviewref.MirrorRefspec(remote)); ferr != nil {
		a.warn("warning: published, but %s's copies could not be re-read (%v) — the report below is the last known answer\n", remote, ferr)
	}
	after, err := reviewref.RemoteList(ctx, repo, remote)
	if err != nil {
		return fmt.Errorf("git-pair: published, but the remote's copies cannot be read to confirm it: %w", err)
	}
	now := indexMirrorRefs(after, remote)

	// Classification is per ref, then grouped back into pairs: a pair is what the reader thinks in, and
	// a ref is what git reported.
	publishedFam := map[string][]string{}
	alreadyFam := map[string][]string{}
	failedRefs := map[string][]publishFailed{}
	for _, fam := range pairs {
		id := changesetOf(fam.ref)
		switch {
		case now[fam.ref] == fam.local && at[fam.ref] == fam.local:
			alreadyFam[id] = append(alreadyFam[id], fam.ref)
		case now[fam.ref] == fam.local:
			publishedFam[id] = append(publishedFam[id], fam.ref)
		default:
			f := publishFailed{
				Changeset: id,
				Family:    fam.family,
				Local:     shortish(fam.local),
				Reason:    gitReason[fam.ref],
			}
			if v := now[fam.ref]; v != "" {
				f.Remote = shortish(v)
			}
			failedRefs[id] = append(failedRefs[id], f)
		}
	}
	for id, fams := range publishedFam {
		sort.Strings(fams)
		view.Published = append(view.Published, publishedRef{Changeset: id, Refs: fams})
	}
	for id, fams := range alreadyFam {
		sort.Strings(fams)
		view.Already = append(view.Already, publishedRef{Changeset: id, Refs: fams})
	}
	// A pair with one ref arrived and the other refused is its own state and the worse one: the remote
	// holds a hint of a landing with no way to reconstruct the record from it.
	for id, fs := range failedRefs {
		fams := make([]string, 0, len(fs))
		locals := make([]string, 0, len(fs))
		remotes := make([]string, 0, len(fs))
		reasons := make([]string, 0, len(fs))
		for _, f := range fs {
			fams = append(fams, f.Family)
			if f.Local != "" {
				locals = append(locals, f.Local)
			}
			if f.Remote != "" {
				remotes = append(remotes, f.Remote)
			}
			if f.Reason != "" {
				reasons = append(reasons, f.Reason)
			}
		}
		sort.Strings(fams)
		view.Failed = append(view.Failed, publishFailed{
			Changeset: id,
			Family:    strings.Join(fams, "+"),
			Local:     strings.Join(locals, ", "),
			Remote:    strings.Join(remotes, ", "),
			Reason:    strings.Join(reasons, "; "),
			HalfState: len(fs) == 1,
		})
	}
	sortPublished(view.Published)
	sortPublished(view.Already)
	sort.Slice(view.Failed, func(i, j int) bool { return view.Failed[i].Changeset < view.Failed[j].Changeset })

	return a.finishPublish(view, len(view.Failed) > 0)
}

// finishPublish prints the answer and chooses the exit code: publishing that left a pair split across
// two values is a refusal, not a warning, because the remote is now in a state nobody chose.
func (a *app) finishPublish(view publishJSON, failed bool) error {
	if a.json {
		return a.emitJSON(view)
	}
	for _, p := range view.Published {
		a.printf("%s: published to %s (%s)\n", p.Changeset, view.Remote, refsLabel(p.Refs))
	}
	for _, p := range view.Already {
		a.printf("%s: already published to %s\n", p.Changeset, view.Remote)
	}
	for _, f := range view.Failed {
		a.printf("%s: NOT published to %s — %s\n", f.Changeset, view.Remote, describeFailure(f, view.Remote))
	}
	if len(view.Failed) > 0 {
		a.printf("\n%s of the pairs did not arrive. The remote's value was not overwritten: %s writes each ref\n"+
			"once, so two clones disagreeing about a landing is a decision for a person, not a flag.\n"+
			"`git pair status` lists what is still recorded here and not there.\n",
			plural(len(view.Failed), "pair", "pairs"), "git pair integration record")
		return fmt.Errorf("git-pair: %s not published", plural(len(view.Failed), "pair", "pairs"))
	}
	if len(view.Published) == 0 && len(view.Already) == 0 {
		a.printf("nothing to publish: this clone holds no integration record for the %s asked for\n",
			"changesets")
	}
	return nil
}

func describeFailure(f publishFailed, remote string) string {
	if f.Remote != "" {
		return fmt.Sprintf("%s holds %s and this clone holds %s%s", remote, f.Remote, f.Local,
			suffixReason(f.Reason))
	}
	return fmt.Sprintf("%s has no such ref and the push was refused%s", remote, suffixReason(f.Reason))
}

func suffixReason(reason string) string {
	if reason == "" {
		return ""
	}
	return " (" + reason + ")"
}

// publishRemote resolves the remote to publish to. A named `--remote` must exist; an inferred one may be
// absent, and the refusal says what was looked for, because "no remote" is the answer to a different
// question than "no such remote".
func (a *app) publishRemote(ctx context.Context, repo *git.Repo, remoteFlag string) (string, error) {
	if remoteFlag != "" {
		return namedRemote(ctx, repo, remoteFlag)
	}
	remote, err := a.remoteForDurableRefs(ctx, repo, "")
	if err != nil {
		return "", fmt.Errorf("git-pair: cannot tell which remote to publish to: %w", err)
	}
	if remote == "" {
		return "", fmt.Errorf("git-pair: nothing to publish to: this repository has no remote.\n" +
			"Add one with `git remote add origin <url>`, or name one with --remote")
	}
	return remote, nil
}

// publishTargets decides which pairs to send. Named ids are taken as given and a name with no record in
// this clone is a refusal — a typo that silently publishes nothing would be the worst possible outcome of
// a command whose purpose is that nothing goes unrecorded.
//
// With no names, every pair this clone holds is published rather than only the pairs M2's finding calls
// unpublished. The finding compares against mirrors, which are a memory of the last fetch, and a publish
// that trusts a stale mirror can skip a ref the remote lost; publishing everything is idempotent, costs
// one no-op push, and cannot be wrong about the remote.
func publishTargets(idx refIndex, names []string) (ids, incomplete []string, err error) {
	if len(names) > 0 {
		want := map[string]bool{}
		for _, n := range names {
			want[n] = true
		}
		for _, n := range names {
			if _, ok := idx.Integrated[n]; !ok {
				return nil, nil, fmt.Errorf("git-pair: this clone holds no integration record for %s — publish names a record, not a branch (run `git pair integration record` first)", n)
			}
		}
		for id := range want {
			if idx.Archive[id] == "" {
				incomplete = append(incomplete, id)
				continue
			}
			ids = append(ids, id)
		}
		sort.Strings(ids)
		sort.Strings(incomplete)
		return ids, incomplete, nil
	}
	for id := range idx.Integrated {
		if idx.Archive[id] == "" {
			incomplete = append(incomplete, id)
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	sort.Strings(incomplete)
	return ids, incomplete, nil
}

// changesetOf takes the id out of a durable or mirror ref. Ids contain no slash, so the last segment is
// the whole answer.
// refsLabel turns the refs a pair pushed into what a human reads: the two halves of a record have names
// long enough to blur, and the JSON carries the full refs for anything that needs to check them.
func refsLabel(refs []string) string {
	labels := make([]string, 0, len(refs))
	for _, r := range refs {
		labels = append(labels, familyOfRef(r))
	}
	sort.Strings(labels)
	return strings.Join(labels, " + ")
}

func familyOfRef(ref string) string {
	if strings.Contains(ref, "/integrations/") {
		return familyIntegration
	}
	return familyArchive
}

func changesetOf(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

func sortPublished(in []publishedRef) {
	sort.Slice(in, func(i, j int) bool { return in[i].Changeset < in[j].Changeset })
}

// indexMirrorRefs keys the mirrors by the ref they mirror — `refs/git-pair/integrations/x`, not the
// `refs/remotes/origin/…` name they live under — because the comparison is between a value this clone
// holds and a value the remote holds, which are the same ref spelled two ways. Keying it by the mirror
// name instead makes every published ref look unpublished, which is the failure a test caught.
func indexMirrorRefs(entries []reviewref.Entry, remote string) map[string]string {
	head := "refs/remotes/" + remote + "/"
	at := map[string]string{}
	for _, e := range entries {
		at[strings.TrimPrefix(e.Ref, head)] = e.SHA
	}
	return at
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func shortish(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func orNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}
