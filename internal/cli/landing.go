package cli

import "strings"

// landing is what git-pair can honestly say about the commit a changeset became in its destination.
//
// It is built from the destination's tree: the branch that carries `changesets/<id>/` holds the work, and
// one walk of its first-parent line names the commit that brought the directory in. Nothing is stored to
// be read back — the tree is the record — which is what makes the answer the same in a fresh clone as in
// one that has fetched everything.
type landing struct {
	// Commit is the full sha of the landing.
	Commit string
	// DefaultBranch is the local name of the branch git-pair calls the integration branch, empty
	// when nothing identifies one.
	DefaultBranch string
	// InDefaultBranch says the landing commit is in that branch's history.
	InDefaultBranch bool
	// BranchKnown separates "not in main" from "cannot tell which branch is main". Collapsing
	// them would report a fetching problem as a fact about the work.
	BranchKnown bool
}

// displayRef trims the prefixes a reader already knows, so a sentence can name a branch without
// printing where git keeps it. A ref outside those namespaces — a tag, a raw revision a pipeline
// passed to --default-branch — is printed as supplied, because shortening it would guess.
func displayRef(ref string) string {
	for _, prefix := range []string{"refs/heads/", "refs/remotes/"} {
		if strings.HasPrefix(ref, prefix) {
			return strings.TrimPrefix(ref, prefix)
		}
	}
	return ref
}

// reach phrases the containment fact for a sentence: "reachable from main", or nothing when the
// branch it would be measured against is unknown.
func (l landing) reach() string {
	if !l.BranchKnown {
		return ""
	}
	if l.InDefaultBranch {
		return ", reachable from " + l.DefaultBranch
	}
	return ", not reachable from " + l.DefaultBranch
}
