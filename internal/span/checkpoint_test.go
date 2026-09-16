package span_test

import (
	"context"
	"strings"
	"testing"

	"gitpr/internal/span"
)

// §23's capability matrix. The head decides everything: a span that ends at a
// commit is a look at history, and history cannot be marked, edited, or submitted
// against.
func TestCommitHeadIsHistorical(t *testing.T) {
	s := newScenario(t, true)

	got, err := s.resolve(t, span.Selector{Base: span.Review(0), Head: span.Commit(s.reviews[1])})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.To != s.reviews[1] {
		t.Errorf("To = %s, want the commit named, %s", got.To, s.reviews[1])
	}
	if got.Live() {
		t.Error("a span ending at a commit is not a live review")
	}
	if !got.Historical() {
		t.Error("Historical() = false for a commit head")
	}
	if got.CanEdit() || got.CanMark() || got.CanSubmit() {
		t.Error("a historical span may not be edited, marked, or submitted")
	}
	if !strings.Contains(got.Label, s.f.Short(s.reviews[1])) {
		t.Errorf("Label = %q, want it to name the commit the span ends at", got.Label)
	}
}

// §29 scenario G: a commit checkpoint is permanently pinned. New work in the
// repository does not move it, because nothing re-resolves it.
func TestCommitCheckpointCannotDrift(t *testing.T) {
	s := newScenario(t, true)

	got, err := s.resolve(t, span.Selector{Base: span.Review(0), Head: span.Commit(s.head)})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	s.f.EmptyCommit("work after the pin")

	drift, err := got.Drift(context.Background(), s.repo)
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if len(drift) != 0 {
		t.Errorf("Drift = %v, want none: a commit cannot move", drift)
	}
	if got.To != s.head {
		t.Errorf("To = %s, want the pinned commit %s", got.To, s.head)
	}
}

// §12 and §29 scenarios C and D: a ref keeps its identity, uses the commit it
// pointed at when chosen, reports movement, and does not act on it.
func TestRefCheckpointIsPinnedAndReportsDrift(t *testing.T) {
	s := newScenario(t, true)
	s.f.MustGit("update-ref", "refs/heads/probe", s.reviews[1])

	got, err := s.resolve(t, span.Selector{Base: span.Review(0), Head: span.Ref("probe")})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.To != s.reviews[1] {
		t.Errorf("To = %s, want where probe pointed, %s", got.To, s.reviews[1])
	}
	if got.Head.Name != "probe" {
		t.Errorf("Head.Name = %q, want the ref to keep its name", got.Head.Name)
	}
	if !strings.Contains(got.Label, "probe") {
		t.Errorf("Label = %q, want the ref name rather than a bare sha", got.Label)
	}

	// The ref moves. This is what a fetch or someone else's push looks like.
	s.f.MustGit("update-ref", "refs/heads/probe", s.reviews[2])

	drift, err := got.Drift(context.Background(), s.repo)
	if err != nil {
		t.Fatalf("Drift: %v", err)
	}
	if len(drift) != 1 {
		t.Fatalf("Drift = %v, want one moved ref", drift)
	}
	if drift[0].Name != "probe" {
		t.Errorf("drift names %q, want probe", drift[0].Name)
	}
	if drift[0].Pinned != s.f.Short(s.reviews[1]) || drift[0].Current != s.f.Short(s.reviews[2]) {
		t.Errorf("drift = %q, want probe pinned at %s now at %s", drift[0],
			s.f.Short(s.reviews[1]), s.f.Short(s.reviews[2]))
	}
	if got.To != s.reviews[1] {
		t.Error("Drift moved the span: it must only report")
	}
}

// The pin has to survive re-resolution, because a session resolves its span again every
// time an editor or difftool closes. If resolution followed the ref, the span would move
// without being asked and the drift would never be visible.
func TestResolveKeepsAPinnedRefWhereItWas(t *testing.T) {
	s := newScenario(t, true)
	s.f.MustGit("update-ref", "refs/heads/probe", s.reviews[1])

	pinned, err := s.resolve(t, span.Selector{Base: span.Review(0), Head: span.Ref("probe")})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	s.f.MustGit("update-ref", "refs/heads/probe", s.reviews[2])

	again, err := s.resolve(t, span.Selector{Base: pinned.Base, Head: pinned.Head})
	if err != nil {
		t.Fatalf("re-Resolve: %v", err)
	}
	if again.To != s.reviews[1] {
		t.Errorf("re-resolving the pinned span ended at %s, want it to stay at %s", again.To, s.reviews[1])
	}

	// Unpinned, the same ref follows where it points now. The pin belongs to the session that
	// chose it, not to the ref (requirements §15).
	fresh, err := s.resolve(t, span.Selector{Base: span.Review(0), Head: span.Ref("probe")})
	if err != nil {
		t.Fatalf("Resolve unpinned: %v", err)
	}
	if fresh.To != s.reviews[2] {
		t.Errorf("a new span on the moved ref ended at %s, want %s", fresh.To, s.reviews[2])
	}
}

// §14: refresh is the reviewer asking for the new pin, and it is the only way a
// resolved span's endpoints move.
func TestRefreshRefRepinsAndRecomputes(t *testing.T) {
	s := newScenario(t, true)
	s.f.MustGit("update-ref", "refs/heads/probe", s.reviews[1])

	got, err := s.resolve(t, span.Selector{Base: span.Ref("probe"), Head: span.WorkingTree()})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	s.f.MustGit("update-ref", "refs/heads/probe", s.reviews[2])

	refreshed, err := got.RefreshRef(context.Background(), s.repo, "probe")
	if err != nil {
		t.Fatalf("RefreshRef: %v", err)
	}
	if refreshed.From != s.reviews[2] {
		t.Errorf("From = %s, want the ref's new commit %s", refreshed.From, s.reviews[2])
	}
	if refreshed.To != got.To {
		t.Errorf("the working-tree end moved from %s to %s", got.To, refreshed.To)
	}
	if !refreshed.Live() {
		t.Error("refreshing an endpoint must not change the mode")
	}
	drift, err := refreshed.Drift(context.Background(), s.repo)
	if err != nil {
		t.Fatalf("Drift after refresh: %v", err)
	}
	if len(drift) != 0 {
		t.Errorf("Drift after refresh = %v, want none", drift)
	}

	if _, err := got.RefreshRef(context.Background(), s.repo, "elsewhere"); err == nil {
		t.Error("refreshing a ref the span does not use should say so")
	}
}

// §23's other half: a ref as the *base* with the working tree as the head is still
// a review you can act on. Pinning an endpoint is not the same as being historical.
func TestRefBaseWithWorkingTreeHeadIsLive(t *testing.T) {
	s := newScenario(t, true)
	s.f.MustGit("update-ref", "refs/heads/probe", s.reviews[1])

	got, err := s.resolve(t, span.Selector{Base: span.Ref("probe"), Head: span.WorkingTree()})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.From != s.reviews[1] {
		t.Errorf("From = %s, want the pinned commit", got.From)
	}
	if !got.Live() || !got.CanEdit() || !got.CanMark() || !got.CanSubmit() {
		t.Error("a ref base with a working-tree head is a live review")
	}
}

// §5: the aliases are relative to the whole review history, so choosing a head
// must not renumber the base.
func TestReviewIndexesDoNotMoveWithTheHead(t *testing.T) {
	s := newScenario(t, true)

	historical, err := s.resolve(t, span.Selector{Base: span.Review(-3), Head: span.Review(-1)})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	live, err := s.resolve(t, span.SinceReview(-3))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if historical.From != s.reviews[0] || live.From != s.reviews[0] {
		t.Errorf("review -3 resolved to %s and %s, want the first review %s both times",
			historical.From, live.From, s.reviews[0])
	}
	if historical.To != s.reviews[2] {
		t.Errorf("review -1 as a head = %s, want the newest review %s", historical.To, s.reviews[2])
	}
	if historical.Historical() != true || live.Historical() != false {
		t.Error("the head, not the base, decides the mode")
	}
}

func TestSelectorValidation(t *testing.T) {
	tests := []struct {
		name string
		sel  span.Selector
		want string
	}{
		{
			name: "working tree as a base",
			sel:  span.Selector{Base: span.WorkingTree(), Head: span.WorkingTree()},
			want: "working tree cannot be the base",
		},
		{
			name: "changeset base as a head",
			sel:  span.Selector{Base: span.ChangesetBase(), Head: span.ChangesetBase()},
			want: "cannot be the head",
		},
		{
			name: "a commit with no name",
			sel:  span.Selector{Base: span.ChangesetBase(), Head: span.Commit("")},
			want: "needs a name",
		},
		{
			name: "a ref with no name",
			sel:  span.Selector{Base: span.Ref(""), Head: span.WorkingTree()},
			want: "needs a name",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newScenario(t, true)
			_, err := s.resolve(t, tc.sel)
			if err == nil {
				t.Fatalf("Resolve(%s → %s) succeeded, want an error", tc.sel.Base.Kind, tc.sel.Head.Kind)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to say %q", err, tc.want)
			}
		})
	}
}

// A name that resolves to something other than a commit is a mistake worth naming
// out loud: "HEAD:file" resolves, and is not a checkpoint.
func TestCheckpointNamesMustResolveToCommits(t *testing.T) {
	s := newScenario(t, true)

	if _, err := s.resolve(t, span.Selector{Base: span.Review(0), Head: span.Commit("does-not-exist")}); err == nil {
		t.Error("Commit resolved a name that is not in the repository")
	} else if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error = %v, want it to name what failed", err)
	}
	if _, err := s.resolve(t, span.Selector{Base: span.Review(0), Head: span.Ref("does/not/exist")}); err == nil {
		t.Error("Ref resolved a name that is not a ref")
	} else if !strings.Contains(err.Error(), "commit") {
		t.Errorf("error = %v, want it to say the name is not a commit", err)
	}
}

// A checkpoint names itself the way the reviewer met it: an id from a list is long and
// deserves abbreviating, a revision they typed back is worth quoting exactly.
func TestCommitCheckpointNamesItselfTheWayItWasReached(t *testing.T) {
	fromList := span.Commit("4b825dc642cb6eb9a060e54bf8d69288fbee4904")
	if got := fromList.String(); got != "4b825dc" {
		t.Errorf("a commit picked from a list names itself %q, want the short id", got)
	}
	typed := span.Commit("HEAD^")
	if got := typed.String(); got != "HEAD^" {
		t.Errorf("a typed revision names itself %q, want it as typed", got)
	}
}
