package git_test

import (
	"context"
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// `for-each-ref` does not take a shell glob. It matches its pattern against the ref name path-name aware,
// so `*` never crosses a `/`. That is invisible while every ref sits one level down and fatal one level
// above that: a repository whose branches are `feat/…` and `fix/…` asked for `refs/heads/*` is told it has
// no branches but the integration branch, and a caller that reads that list as "the branches here" acts on
// the miss. The pattern that reaches RefTips is git's, so the two forms are pinned here, at the layer that
// hands it over — a caller cannot be blamed for writing the glob it has seen in every shell.
func TestRefTipsPatternIsAGitPatternNotAShellGlob(t *testing.T) {
	f, repo := openFixture(t)
	tip := f.Commit("work on both", gittest.WithFile("both.txt", "1\n"))
	f.MustGit("branch", "booking", tip)
	f.MustGit("branch", "feat/booked", tip)

	prefix, err := repo.RefTips(context.Background(), "refs/heads/")
	if err != nil {
		t.Fatalf("RefTips(refs/heads/): %v", err)
	}
	got := map[string]bool{}
	for _, tip := range prefix {
		got[tip.Name] = true
	}
	for _, want := range []string{"refs/heads/booking", "refs/heads/feat/booked"} {
		if !got[want] {
			t.Errorf("RefTips(refs/heads/) did not list %s; it listed %s", want, strings.Join(keys(got), " "))
		}
	}

	glob, err := repo.RefTips(context.Background(), "refs/heads/*")
	if err != nil {
		t.Fatalf("RefTips(refs/heads/*): %v", err)
	}
	var names []string
	deep := false
	for _, tip := range glob {
		names = append(names, tip.Name)
		if tip.Name == "refs/heads/feat/booked" {
			deep = true
		}
	}
	if !deep {
		return // the trap, pinned: the glob form answers only the one-component name
	}
	t.Errorf("RefTips(refs/heads/*) listed refs/heads/feat/booked; if git has started matching `*` across `/`, "+
		"say so here and keep the prefix form at the call sites anyway — every clone on an older git still sees "+
		"the narrow match (%s)", strings.Join(names, " "))
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
