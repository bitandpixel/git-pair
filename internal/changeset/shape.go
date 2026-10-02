package changeset

import (
	"context"
	"sort"
	"strings"

	"gitpair/internal/git"
)

// shapeWalkCap bounds a chain read from committed files, the way the status chain walk bounds its own: a
// CHANGESET.yaml is authored content, and a file that names its own ancestor would otherwise loop here forever.
const shapeWalkCap = 8

// The branch shape rule: a branch carries one unlanded changeset, plus the directories of the changesets it is
// stacked on. It is stated here, once, because three commands have to refuse the same thing - `init` before it
// writes, `change ready` before it offers, and `check` before the CI job merges - and three phrasings of "is this
// branch carrying more than its own work" is three answers waiting to disagree.
//
// The rule reads the candidate list - `Resolution.Candidates`, which is what survives `ignores:` and the set
// operation - because what survives those two filters is exactly the set of things on the branch that nothing else
// claims to be stacked on. One of those means one stack. More than one means work arrived from elsewhere, and that
// is the whole rule. The ancestors the set operation removed are not missing from the answer: they are recovered by
// reading, in `CheckBranchShape`, because a chain can pass through a changeset that landed.

// StackEdge is one changeset's claim about what sits below it, as recorded on the branch being read. `Parent` is
// empty when the file records nothing usable.
type StackEdge struct {
	ID     string
	Parent string
	// Recorded says the parent came from the file's own `base-changeset:` rather than from evidence
	// read off a `base:`. The two are the same shape to this rule and different strengths of claim,
	// which is why the distinction travels: the set operation will not subtract on the weaker one.
	Recorded bool
	// BaseBranch is the member's `base:` with `refs/heads/` trimmed, kept because a file written before
	// ids were recorded says what it is stacked on only in branch names, and branch names need one read
	// to turn into ids. Empty when the base is a commit, a tag, or the destination.
	BaseBranch string
}

// BranchShape is the answer about one branch.
type BranchShape struct {
	// Members is the set the rule was asked about: the changeset directories on the branch that survive
	// `ignores:` and the set operation, which is the set of tops. A branch carrying a stack of three has one
	// member here and two ancestors below it, recovered by reading if the answer needs them.
	Members []string
	// Top is the member the others are stacked on. Empty when there is nothing to be the top of.
	Top string
	// Strays are the unlanded members no chain from Top reaches: work that arrived from another
	// branch and is not part of this one's stack. This is what the invariant refuses on, and it is
	// empty for every branch whose records say it is one stack.
	Strays []string
}

// Offerable is the invariant's verdict.
func (s BranchShape) Offerable() bool { return len(s.Strays) == 0 }

// EdgesOf turns a resolution's unlanded set into the edges the rule walks. The recorded id is the evidence; a
// `base:` naming a branch is accepted only when that branch name is another changeset's id, which is the older
// way a stack was spelled and is read here because refusing a branch over a missing record would punish the
// author for a file `init` wrote before ids were kept. It is not read by the set operation, which decides who the
// work is and will not guess from a branch name.
func EdgesOf(res Resolution) []StackEdge {
	inSet := map[string]bool{}
	for _, c := range res.Candidates {
		inSet[c.Changeset.Slug] = true
	}
	edges := make([]StackEdge, 0, len(res.Candidates))
	for _, c := range res.Candidates {
		id := c.Changeset.Slug
		edge := StackEdge{ID: id}
		if parent := c.Changeset.BaseChangeset; parent != "" && parent != id {
			edge.Parent, edge.Recorded = parent, true
			edges = append(edges, edge)
			continue
		}
		if !c.Changeset.BaseDerived {
			// applyBases rewrote the base of a stacked changeset to an answer derived from the destination, and
			// that answer is a measurement point rather than a branch of this repository to read a stack from.
			// Only an untouched `base:` can be read as a branch name at all, and a branch name becomes an edge
			// here only when it is also another member's id - the case that needs no read.
			// `CheckEdges` resolves the rest, which is where the id that is not spelled like its branch is
			// turned into one: refusing a stack because `feature/auth` carries `feature-auth` would be
			// refusing the author for a naming convention git-pair never required.
			branch := strings.TrimPrefix(c.Changeset.Base, "refs/heads/")
			edge.BaseBranch = branch
			if branch != "" && branch != id && inSet[branch] {
				edge.Parent = branch
			}
		}
		edges = append(edges, edge)
	}
	return edges
}

// CheckBranchShape answers the invariant for one revision. It costs one read per hop of a chain that passes through a
// changeset the branch no longer carries, and nothing at all when the branch carries one changeset: the ordinary
// case answers from the set the resolver already read.
func CheckBranchShape(ctx context.Context, repo *git.Repo, rev string, res Resolution) (BranchShape, error) {
	return CheckEdges(ctx, repo, rev, EdgesOf(res))
}

// CheckEdges answers the rule from edges, reading what the edges cannot say on their own. `init` calls it with the
// edge of the changeset it is about to write added, so the record this run is about to make is part of the set
// being judged.
func CheckEdges(ctx context.Context, repo *git.Repo, rev string, edges []StackEdge) (BranchShape, error) {
	shape := ShapeOf(edges)
	if shape.Offerable() || len(shape.Members) < 2 {
		return shape, nil
	}

	// Nothing was lost by the cheap pass: a member whose `base:` names a branch says what it is stacked on, and
	// turning that branch into an id costs one read of that branch's tree. It happens only on this path, so the
	// branch that records its parents answers without touching git.
	edges, err := resolveBranchEdges(ctx, repo, edges)
	if err != nil {
		return BranchShape{}, err
	}
	shape = ShapeOf(edges)
	if shape.Offerable() {
		return shape, nil
	}

	// A chain can pass through a changeset this branch no longer carries, because it landed and left the
	// candidate list. That link is still recorded, in the file the landed changeset keeps on the destination, so
	// reading it is what tells a stacked branch from two changesets that arrived together. The read is one
	// CHANGESET.yaml per hop, only on the path that is about to refuse, and capped the way every other walk over
	// committed metadata is: a file is authored content and can name anything.
	parentOf := func(id string) (string, error) {
		for _, e := range edges {
			if e.ID == id {
				return e.Parent, nil
			}
		}
		st, err := StackAt(ctx, repo, rev, id)
		if err != nil {
			return "", err
		}
		return st.BaseChangeset, nil
	}
	if shape.Top == "" {
		return shape, nil
	}

	reached := map[string]bool{shape.Top: true}
	at, seen := shape.Top, map[string]bool{shape.Top: true}
	for depth := 0; depth < shapeWalkCap; depth++ {
		parent, err := parentOf(at)
		if err != nil || parent == "" || seen[parent] {
			break
		}
		seen[parent] = true
		reached[parent] = true
		at = parent
	}
	shape.Strays = nil
	for _, id := range shape.Members {
		if !reached[id] {
			shape.Strays = append(shape.Strays, id)
		}
	}
	return shape, nil
}

// ShapeOf is the rule with no reads: the members nobody else names are the tops, one top means one stack, and more
// than one top - or none, which is what mutual records leave - is the shape the invariant refuses. When two members
// are both unnamed it keeps the one that reaches further, so the refusal names the fewest ids it can honestly.
func ShapeOf(edges []StackEdge) BranchShape {
	shape := BranchShape{}
	named := map[string]bool{}
	for _, e := range edges {
		if e.Parent != "" && e.Parent != e.ID {
			named[e.Parent] = true
		}
	}
	var tops []string
	for _, e := range edges {
		shape.Members = append(shape.Members, e.ID)
		if !named[e.ID] {
			tops = append(tops, e.ID)
		}
	}
	sort.Strings(shape.Members)
	switch {
	case len(tops) == 1:
		shape.Top = tops[0]
	case len(tops) == 0:
		// Every member is named by another, which is a cycle: mutual records, and no member is the top of
		// anything. All of them are strays, and the message says the records contradict each other rather
		// than that somebody merged the wrong branch.
		shape.Strays = shape.Members
	default:
		// Several tops. The one that reaches furthest down is the stack this branch is working on; the
		// others are the arrivals. Reachability here follows only the edges on this branch, so a top whose
		// chain runs through a landed changeset still counts as reaching nothing - BranchShape above reads
		// the files and narrows the list before anything is refused.
		shape.Top = tops[0]
		for _, t := range tops[1:] {
			if len(reachable(edges, t)) > len(reachable(edges, shape.Top)) {
				shape.Top = t
			}
		}
		reached := reachable(edges, shape.Top)
		for _, t := range tops {
			if t != shape.Top && !reached[t] {
				shape.Strays = append(shape.Strays, t)
			}
		}
	}
	return shape
}

// resolveBranchEdges turns each `base:` that names a branch into the id of the changeset that branch carries. It
// is called only when the recorded edges left more than one top, and it reads at most one tree per candidate
// parent - the shape is rare, and refusing it wrongly is more expensive than reading.
func resolveBranchEdges(ctx context.Context, repo *git.Repo, edges []StackEdge) ([]StackEdge, error) {
	out := make([]StackEdge, len(edges))
	copy(out, edges)
	for i := range out {
		if out[i].Parent != "" || out[i].BaseBranch == "" {
			continue
		}
		ref := "refs/heads/" + out[i].BaseBranch
		for _, other := range edges {
			if other.ID == out[i].ID {
				continue
			}
			present, _ := CarriesDir(ctx, repo, ref, other.ID)
			if present {
				out[i].Parent = other.ID
				break
			}
		}
	}
	return out, nil
}

func reachable(edges []StackEdge, from string) map[string]bool {
	parent := map[string]string{}
	for _, e := range edges {
		parent[e.ID] = e.Parent
	}
	out := map[string]bool{from: true}
	at, seen := from, map[string]bool{from: true}
	for depth := 0; depth < shapeWalkCap; depth++ {
		p := parent[at]
		if p == "" || seen[p] {
			break
		}
		seen[p] = true
		out[p] = true
		at = p
	}
	return out
}
