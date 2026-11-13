package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The landing route is the repository's answer to the question the landing sentence used to leave open:
// who lands the work. The declaration is data in `.git-pair/config`, and these tests hold it to the
// contract the workshop settled: each declared route names exactly one exit on every surface, the default
// route keeps both, and a declaration this tool cannot honor shows the default view with the defect named
// — never a guess at what the repository meant.

// withLandingConfig writes the declaration the surfaces are then read under. It runs before any
// git-pair command so every session in the fixture resolves it.
func withLandingConfig(t *testing.T, f *gittest.Fixture, text string) {
	t.Helper()
	dir := filepath.Join(f.Dir(), ".git-pair")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// landingRouteFields reads `landing_route` off the four machine surfaces that carry it.
func landingRouteFields(t *testing.T, f *gittest.Fixture) map[string]string {
	t.Helper()
	return map[string]string{
		"check":  stringField(t, runIn(t, f.Dir(), "check", "--json").json(t), "landing_route"),
		"status": stringField(t, runIn(t, f.Dir(), "status", "--json").json(t), "landing_route"),
		"wait":   stringField(t, runIn(t, f.Dir(), "change", "wait", "--json").json(t), "landing_route"),
		"submit": stringField(t, runIn(t, f.Dir(), "review", "submit", "--approve", "--json").json(t), "landing_route"),
	}
}

// The four next-step reads, human and machine, in one map — the property that matters is that they
// agree, and a test that read one screen would pass on a change that reached only that screen. The
// submit surface prefixes its sentence with "author: " on both the screen and in --json (it is written
// for a reader who just acted as the reviewer), so the prefix is stripped here and the sentence body
// compared like the others.
func routeNextLines(t *testing.T, f *gittest.Fixture) map[string]string {
	t.Helper()
	stripAuthor := func(line string) string {
		return strings.TrimPrefix(line, "author: ")
	}
	return map[string]string{
		"status":  nextStepLine(t, runIn(t, f.Dir(), "status").stdout),
		"check":   nextStepLine(t, runIn(t, f.Dir(), "check").stdout),
		"wait":    nextStepLine(t, runIn(t, f.Dir(), "change", "wait").stdout),
		"submit":  stripAuthor(nextStepLine(t, runIn(t, f.Dir(), "review", "submit", "--approve").stdout)),
		"json":    stringField(t, runIn(t, f.Dir(), "check", "--json").json(t), "next_action"),
		"jsonst":  stringField(t, runIn(t, f.Dir(), "status", "--json").json(t), "next_action"),
		"jsonwt":  stringField(t, runIn(t, f.Dir(), "change", "wait", "--json").json(t), "next_action"),
		"jsonsub": stripAuthor(stringField(t, runIn(t, f.Dir(), "review", "submit", "--approve", "--json").json(t), "next_action")),
	}
}

func TestManualRouteNamesTheMergeAlone(t *testing.T) {
	f := newRepo(t)
	withLandingConfig(t, f, "[landing]\nroute = \"manual\"\n")
	f.CreateBranch("booking-transaction")
	f.CommitChangeset("booking-transaction", "main")
	f.Commit("work", gittest.WithFile("w.go", "package main\n"))
	ready(t, f)
	submit(t, f, "approve")

	const want = "`git pair check`, then merge into main with ordinary git"
	for name, line := range routeNextLines(t, f) {
		if !strings.HasSuffix(line, want) {
			t.Errorf("%s next step is %q, want it to end in %q", name, line, want)
		}
	}
	// The route named the merge as the whole answer, so the hand-off is not offered beside it —
	// that is the difference from the default route, and the whole reason a repository declares.
	for _, name := range []string{"status", "check"} {
		out := runIn(t, f.Dir(), name).mustSucceed(t, name).stdout
		if got := printedHandoff(out); got != "" {
			t.Errorf("%s offers the hand-off under a route that named the merge: %s", name, got)
		}
	}
	for name, got := range landingRouteFields(t, f) {
		if got != "manual" {
			t.Errorf("%s --json landing_route = %q, want \"manual\"", name, got)
		}
	}
}

func TestAutomatedRouteNamesTheHandoffAlone(t *testing.T) {
	f := newRepo(t)
	withLandingConfig(t, f, "[landing]\nroute = \"automated\"\n")
	f.CreateBranch("booking-transaction")
	f.CommitChangeset("booking-transaction", "main")
	f.Commit("work", gittest.WithFile("w.go", "package main\n"))
	ready(t, f)
	submit(t, f, "approve")

	// The hand-off is the whole answer: the gate, then the ask, then the push that makes it visible.
	const want = "`git pair check`, then hand it to main's owner: " +
		"`git pair change integrate`, then `git push origin booking-transaction`"
	for name, line := range routeNextLines(t, f) {
		if line != want {
			t.Errorf("%s next step is %q, want %q", name, line, want)
		}
		if strings.Contains(line, "merge into main with ordinary git") {
			t.Errorf("%s offers the merge under a route that named the hand-off: %s", name, line)
		}
	}
	// The merge sentence is the one a repository with a merge job cannot afford, so it is gone from
	// the human screens too — not demoted to an "or" line, removed.
	for _, name := range []string{"status", "check"} {
		out := runIn(t, f.Dir(), name).mustSucceed(t, name).stdout
		mustNotContain(t, out, "merge into main with ordinary git",
			name+" keeps no merge exit under the automated route")
		mustNotContain(t, out, "or land it yourself", name+" keeps no merge exit under the automated route")
	}
	for name, got := range landingRouteFields(t, f) {
		if got != "automated" {
			t.Errorf("%s --json landing_route = %q, want \"automated\"", name, got)
		}
	}
}

func TestCustomRouteNamesTheRepositorysSentence(t *testing.T) {
	f := newRepo(t)
	withLandingConfig(t, f, "[landing]\nroute = \"custom\"\n"+
		"next = \"push {branch}, then open the pull request into {destination} for {changeset}\"\n")
	f.CreateBranch("booking-transaction")
	f.CommitChangeset("booking-transaction", "main")
	f.Commit("work", gittest.WithFile("w.go", "package main\n"))
	ready(t, f)
	submit(t, f, "approve")

	const want = "`git pair check`, then push booking-transaction, " +
		"then open the pull request into main for booking-transaction"
	for name, line := range routeNextLines(t, f) {
		if line != want {
			t.Errorf("%s next step is %q, want %q", name, line, want)
		}
	}
	// The repository wrote the step; the built-in exits stand down on both sides.
	for _, name := range []string{"status", "check"} {
		out := runIn(t, f.Dir(), name).mustSucceed(t, name).stdout
		mustNotContain(t, out, "merge into main with ordinary git",
			name+" keeps no built-in merge exit under a custom route")
		mustNotContain(t, out, "or hand it to", name+" keeps no built-in hand-off under a custom route")
	}
	for name, got := range landingRouteFields(t, f) {
		if got != "custom" {
			t.Errorf("%s --json landing_route = %q, want \"custom\"", name, got)
		}
	}
}

// The declared path is state machinery, and the route leaves it alone — except that a route which
// removed the author's own merge as an exit removes the "or land it yourself" line with it. Under the
// automated route the wait is the whole answer, which is the point: a repository whose merge runs
// behind a CI gate cannot afford a screen that offers the merge as an exit, even beside a wait.
func TestAutomatedRouteWaitsWithoutTheMergeExit(t *testing.T) {
	f := newRepo(t)
	withLandingConfig(t, f, "[landing]\nroute = \"automated\"\n")
	f.CreateBranch("booking-transaction")
	f.CommitChangeset("booking-transaction", "main")
	f.Commit("work", gittest.WithFile("w.go", "package main\n"))
	ready(t, f)
	submit(t, f, "approve")
	runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")
	declared := f.Head()
	f.MustGit("update-ref", "refs/remotes/origin/booking-transaction", declared)

	for _, name := range []string{"status", "check"} {
		out := runIn(t, f.Dir(), name).mustSucceed(t, name).stdout
		mustContain(t, out, "waiting on main's owner to merge it", name+" names the wait")
		mustNotContain(t, out, "or land it yourself",
			name+" offers no merge beside the wait under the automated route")
		mustNotContain(t, out, "merge into main with ordinary git",
			name+" keeps no merge sentence anywhere under the automated route")
	}
}

// A stack under a declared route waits the same way it merges: the tail of the waiting sentence is the
// route's exit, so a repository with a merge job is never told — at any point of a stack — that its own
// merge is the step. The parent's step still belongs to the parent's screen.
func TestAutomatedRouteKeepsTheWaitingSentenceOffTheMerge(t *testing.T) {
	f := newRepo(t)
	withLandingConfig(t, f, "[landing]\nroute = \"automated\"\n")
	stackedApprovedPair(t, f)

	const want = "waiting on parent alpha: its landing is owed first (`git pair status --changeset alpha` " +
		"names the step), then `git pair check` and hand it to main's owner: " +
		"`git pair change integrate`, then `git push origin beta`"
	for name, line := range routeNextLines(t, f) {
		if !strings.HasSuffix(line, want) {
			t.Errorf("%s next step is %q, want it to end in %q", name, line, want)
		}
		if strings.Contains(line, "merge into main") {
			t.Errorf("%s names a merge the automated route removed: %s", name, line)
		}
	}
}

func TestCustomRouteWaitsOnTheRepositorysSentence(t *testing.T) {
	f := newRepo(t)
	withLandingConfig(t, f, "[landing]\nroute = \"custom\"\n"+
		"next = \"push {branch}, then open the pull request into {destination}\"\n")
	stackedApprovedPair(t, f)

	const want = "waiting on parent alpha: its landing is owed first (`git pair status --changeset alpha` " +
		"names the step), then `git pair check`, then push beta, then open the pull request into main"
	for name, line := range routeNextLines(t, f) {
		if !strings.HasSuffix(line, want) {
			t.Errorf("%s next step is %q, want it to end in %q", name, line, want)
		}
	}
}

// A declaration this tool cannot honor shows the default view — both exits, the sentences an undeclared
// repository has always printed — with the defect named on stderr of every command that read it. The
// fallback is the unopinionated answer, never a guess at what the repository meant.
func TestBrokenDeclarationFallsBackToDefaultAndWarns(t *testing.T) {
	t.Run("unknown route", func(t *testing.T) {
		f := newRepo(t)
		withLandingConfig(t, f, "[landing]\nroute = \"ci\"\n")
		f.CreateBranch("booking-transaction")
		f.CommitChangeset("booking-transaction", "main")
		f.Commit("work", gittest.WithFile("w.go", "package main\n"))
		ready(t, f)
		submit(t, f, "approve")

		const want = "`git pair check`, then merge into main with ordinary git"
		for name, line := range routeNextLines(t, f) {
			if !strings.HasSuffix(line, want) {
				t.Errorf("%s next step is %q, want the default sentence", name, line)
			}
		}
		for name, got := range landingRouteFields(t, f) {
			if got != "default" {
				t.Errorf("%s --json landing_route = %q, want \"default\"", name, got)
			}
		}
		for _, name := range []string{"status", "check"} {
			res := runIn(t, f.Dir(), name).mustSucceed(t, name)
			if !strings.Contains(res.stderr, "git-pair: warning: .git-pair/config:") ||
				!strings.Contains(res.stderr, `unknown landing route "ci"`) {
				t.Errorf("%s stderr = %q, want the defect named", name, res.stderr)
			}
			if !strings.Contains(res.stderr, "the default landing advice is shown") {
				t.Errorf("%s stderr = %q, want the fallback named", name, res.stderr)
			}
		}
		// The fork stays open under the fallback: the default route offers both exits, and the defect
		// is the only thing that says the declaration did not take.
		out := runIn(t, f.Dir(), "check").mustSucceed(t, "check").stdout
		if got := printedHandoff(out); got == "" {
			t.Errorf("check offers no hand-off under a broken declaration, want the default fork")
		}
	})
	t.Run("unknown placeholder", func(t *testing.T) {
		f := newRepo(t)
		withLandingConfig(t, f, "[landing]\nroute = \"custom\"\nnext = \"ask {forge} about {destination}\"\n")
		f.CreateBranch("booking-transaction")
		f.CommitChangeset("booking-transaction", "main")
		f.Commit("work", gittest.WithFile("w.go", "package main\n"))
		ready(t, f)
		submit(t, f, "approve")

		res := runIn(t, f.Dir(), "status").mustSucceed(t, "status")
		if !strings.Contains(res.stderr, `"forge"`) {
			t.Errorf("status stderr = %q, want the unknown placeholder named", res.stderr)
		}
		mustContain(t, res.stdout, "merge into main with ordinary git",
			"the template that cannot be filled falls back to the default sentence")
	})
}

// The default route is what an absent file has always meant, and naming it changes nothing: both
// exits, the merge sentence as the machine answer.
func TestExplicitDefaultRouteIsTheAbsentFile(t *testing.T) {
	f := newRepo(t)
	withLandingConfig(t, f, "[landing]\nroute = \"default\"\n")
	f.CreateBranch("booking-transaction")
	f.CommitChangeset("booking-transaction", "main")
	f.Commit("work", gittest.WithFile("w.go", "package main\n"))
	ready(t, f)
	submit(t, f, "approve")

	const want = "`git pair check`, then merge into main with ordinary git"
	for name, line := range routeNextLines(t, f) {
		if !strings.HasSuffix(line, want) {
			t.Errorf("%s next step is %q, want it to end in %q", name, line, want)
		}
	}
	out := runIn(t, f.Dir(), "check").mustSucceed(t, "check").stdout
	if got := printedHandoff(out); got == "" {
		t.Errorf("check offers no hand-off under the explicit default route, want the fork")
	}
	for name, got := range landingRouteFields(t, f) {
		if got != "default" {
			t.Errorf("%s --json landing_route = %q, want \"default\"", name, got)
		}
	}
}
