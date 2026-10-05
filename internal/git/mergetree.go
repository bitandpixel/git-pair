package git

import (
	"context"
	"strings"
)

// MergeResult is the answer to "what would merging these two revisions produce".
type MergeResult struct {
	// Tree is the OID of the tree the merge would create. It is meaningless when Clean is false: git
	// writes a tree for a conflicted merge too, with conflict markers in it, so a caller that reports a
	// clean merge from the presence of a tree is reporting the conflict as content.
	Tree string
	// Clean says the merge needed nothing from a human. False is git's exit status 1, which is an answer
	// rather than a failure — the same distinction `rev-parse --verify --quiet` makes by exiting 1.
	Clean bool
	// Conflicts are the paths that could not be merged, in git's order, from the merge's index-stage
	// entries. It is what turns a refusal into advice: "this would conflict with main: internal/cli/x.go".
	Conflicts []string
}

// MergeTree computes what merging `theirs` into `ours` would look like, and touches nothing.
//
// It is the question `git pair check` cannot ask from history alone: a branch can carry exactly the content
// a reviewer approved and still not merge into the destination, because something else landed underneath
// it. Discovering that in the merge job — after the gate said ready and CI said green — is the worst place
// to discover it, because the resolution is then written by whoever happens to be merging, over content no
// reviewer saw.
//
// This is not a merge in the sense PRD §26 forbids. `merge-tree --write-tree` writes objects and nothing
// else: no ref moves, the index is untouched, the working tree is untouched, and no commit exists at the
// end of it. It is a read of what a merge would produce, which is why `classify` treats it as a pure read
// and the memo may keep the answer — two commits and an object database give one answer.
func (r *Repo) MergeTree(ctx context.Context, ours, theirs string) (MergeResult, error) {
	out, err := r.Git(ctx, "merge-tree", "--write-tree", "-z", ours, theirs)
	conflicted := false
	if err != nil {
		if ExitCode(err) != 1 {
			return MergeResult{}, err
		}
		// Exit 1 is git's way of saying the merge needs a human; the stdout it produced alongside the
		// error is the answer, not debris.
		conflicted = true
	}
	fields := strings.Split(out, "\x00")
	if len(fields) == 0 || strings.TrimSpace(fields[0]) == "" {
		return MergeResult{}, err
	}
	res := MergeResult{Tree: strings.TrimSpace(fields[0]), Clean: !conflicted}
	seen := map[string]bool{}
	for _, f := range fields[1:] {
		// The conflicted-file entries are index stages — "<mode> <object> <stage>\t<path>" — and anything
		// else in this section is a message about the conflict, which the caller does not need: the paths
		// are the part an author can act on. A conflicted path appears once per stage (base, ours, theirs),
		// so it is reported once.
		tab := strings.IndexByte(f, '\t')
		if tab <= 0 || strings.Count(f, " ") < 2 {
			continue
		}
		if path := f[tab+1:]; !seen[path] {
			seen[path] = true
			res.Conflicts = append(res.Conflicts, path)
		}
	}
	return res, nil
}
