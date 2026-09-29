package changeset

import (
	"context"

	"gitpair/internal/git"
)

// Reads is the memo for one command that asks several questions about one repository: which changeset
// directories a revision carries, and what each of their `CHANGESET.yaml` files says.
//
// It exists because the surfaces that describe a stack walk the same ancestors for different reasons. The
// chain in `status` wants each ancestor's landing; the destination answer wants each ancestor's recorded base
// and whether the destination carries it. Without a memo, an author five ancestors deep pays for the chain
// twice, which is the doubling `status_stack_chain_test.go` refuses — its bound is the marginal read per
// ancestor, and a second walk of the same tree would not be flat.
//
// The memo travels as an argument rather than living inside `git.Repo` on purpose: commands write markers and
// move refs while they run, and a repository-wide cache of `show` output would serve a record that has just
// been rewritten. It caches successful reads of immutable content — a tree at a revision does not change
// under the process — and nothing else. A failed read is not remembered, because a fetch between two attempts
// can change the answer, and one extra failing read per hop is cheaper than a wrong one.
//
// A nil *Reads is a caller asking for no memo, and every method answers it by reading.
type Reads struct {
	ids    map[string][]string // revision -> the directory names it carries
	stacks map[string]Stack    // revision + "\x00" + changeset id -> its record
}

// NewReads returns an empty memo. It is worth building when a command will ask more than one question of one
// repository; a command that asks one question can pass nil and read.
func NewReads() *Reads {
	return &Reads{ids: map[string][]string{}, stacks: map[string]Stack{}}
}

// LandedIDs answers which changesets `rev` carries, listing it at most once.
func (r *Reads) LandedIDs(ctx context.Context, repo *git.Repo, rev string) ([]string, error) {
	if r == nil {
		return LandedIDs(ctx, repo, rev)
	}
	if got, ok := r.ids[rev]; ok {
		return got, nil
	}
	ids, err := LandedIDs(ctx, repo, rev)
	if err != nil {
		return nil, err
	}
	r.ids[rev] = ids
	return ids, nil
}

// StackAt reads one changeset's record from one revision, once. The chain surface and the destination answer
// both read the same file at the same revision for the same ancestor; this is where the second read goes away.
func (r *Reads) StackAt(ctx context.Context, repo *git.Repo, rev, id string) (Stack, error) {
	if r == nil {
		return StackAt(ctx, repo, rev, id)
	}
	key := rev + "\x00" + id
	if got, ok := r.stacks[key]; ok {
		return got, nil
	}
	stack, err := StackAt(ctx, repo, rev, id)
	if err != nil {
		return Stack{}, err
	}
	r.stacks[key] = stack
	return stack, nil
}
