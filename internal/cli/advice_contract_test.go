package cli

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"gitpair/internal/changeset"
	"gitpair/internal/gittest"
	"gitpair/internal/lifecycle"
	"gitpair/internal/model"
)

// Advice is a command line somebody runs. When it names a command that does not exist, or a flag the command
// it names does not declare, the advice is not merely imprecise: the reader types it, gets a usage error from
// their own tool, and loses the step the sentence was there to give them.
//
// Two sentences in the tree did exactly that. `git pair change ready --changeset <id>` appeared in the
// landed-parent advice from `feat-next-action-fixes` — in the step a refused verdict leaves, and in the step
// a conflicting merge leaves — and `change ready` has no `--changeset`: the commands that act on the changeset
// this branch carries (`ready`, `unready`, `integrate`, `abandon`, `wait`) refuse a selector, because the
// branch-shape rule is what makes their subject unambiguous. `--changeset` is real, on `status`, `check`,
// `change feedback` and `review history`, which is how the sentences survived review and their own tests: a
// test that pins an advice string verbatim pins the string, and the flag existed somewhere.
//
// `docs_contract_test.go` checks the documents against the command tree, in both directions. Emitted advice
// is not a document — it is assembled with `fmt.Sprintf` where a verdict is composed — so nothing could see
// it. This is the same check, applied to what the tool says out loud: every command a sentence names has to be
// a command this build has, and every flag has to belong to *that* command. The union of all commands' flags
// would pass the bug that motivated this test, so it is not used.
//
// The corpus is both halves of how advice is made: the builders, called directly so every branch of a sentence
// is covered without a fixture, and the screens, run over a fixture that walks a stacked pair from `WORKING`
// to landed, so strings assembled at run time are covered too.

type adviceLine struct {
	where string
	text  string
}

func TestEmittedAdviceNamesCommandsThisBuildAnswersTo(t *testing.T) {
	root := newRootCommand(&app{})
	paths := commandPaths(root)
	flags := adviceFlagTree(root)

	corpus := adviceCorpus(t)
	if len(corpus) < 25 {
		t.Fatalf("the advice corpus holds %d sentences; it stopped collecting", len(corpus))
	}

	for _, a := range corpus {
		for _, c := range adviceCommands(a.text, paths) {
			if !paths[c.path] {
				t.Errorf("%s advises `git pair %s`, which is not a command of this build", a.where, c.path)
				continue
			}
			if hasChild(paths, c.path) && c.followed != "" && !paths[c.path+" "+c.followed] {
				// A group followed by a word that is not one of its children: either a subcommand
				// that was renamed away, or prose that ran on. Both read as a broken step.
				t.Errorf("%s advises `git pair %s %s`, and %s has no such subcommand", a.where, c.path, c.followed, c.path)
			}
			for _, flag := range c.flags {
				if !flags[c.path][flag] {
					t.Errorf("%s advises `git pair %s --%s`, and that command declares no such flag. It exists on "+
						"another command, which is how the sentence survives a glance at it.", a.where, c.path, flag)
				}
			}
		}
	}
}

// adviceCommand is one `git pair …` reference found in an advice sentence.
type adviceCommand struct {
	path  string
	flags []string
	// followed is the word after the resolved command path, kept so a mistyped subcommand is reportable
	// rather than silently trimmed off a valid group name.
	followed string
}

var adviceWord = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// adviceCommands finds every `git pair …` reference in a sentence and splits it into a command path and the
// flags that follow it.
//
// It stops the command path at the first token the tree does not continue, so `git pair tidy <id>` and
// `git pair change use <x>` do not turn their arguments into subcommands. A reference cut short that way is
// not forgiven by the caller: `adviceCommand.followed` keeps the token so a group named with a bad child is
// still reported.
func adviceCommands(text string, paths map[string]bool) []adviceCommand {
	var out []adviceCommand
	for _, chunk := range strings.Split(text, "git pair ")[1:] {
		var c adviceCommand
		var words []string
		stoppedAt := ""
		for _, raw := range strings.Fields(chunk) {
			token := strings.Trim(raw, "`\"'`,.;:!?)]}")
			if token == "" {
				continue
			}
			if strings.HasPrefix(token, "--") {
				c.flags = append(c.flags, strings.TrimPrefix(token, "--"))
				continue
			}
			if strings.HasPrefix(token, "-") || !adviceWord.MatchString(token) {
				break
			}
			candidate := strings.Join(append(append([]string{}, words...), token), " ")
			if adviceHasPathOrPrefix(paths, candidate) {
				words = append(words, token)
				continue
			}
			stoppedAt = token
			break
		}
		if len(words) == 0 {
			continue
		}
		c.path = strings.Join(words, " ")
		c.followed = stoppedAt
		out = append(out, c)
	}
	return out
}

// adviceHasPathOrPrefix asks whether a token sequence is a command, or leads to one — the question that lets
// `git pair tidy <id>` stop its command path at `tidy` instead of eating the argument.
func adviceHasPathOrPrefix(paths map[string]bool, candidate string) bool {
	if paths[candidate] {
		return true
	}
	for path := range paths {
		if strings.HasPrefix(path, candidate+" ") {
			return true
		}
	}
	return false
}

// adviceFlagTree is each command's own flags plus the persistent flags of its ancestors, which is what a reader
// can actually type: `--json` and `--default-branch` are on the root, and advice names them.
//
// It reads `Flags()` and `PersistentFlags()` rather than `LocalFlags()`/`InheritedFlags()`, because cobra
// fills the latter two only when a command executes, and this tree is built, never run. Paths are keyed the
// way `commandPaths` keys them — space-separated below the root, with no `git-pair` segment — so the two maps
// can be read together.
func adviceFlagTree(root *cobra.Command) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	collect := func(c *cobra.Command, from map[string]bool) map[string]bool {
		own := map[string]bool{}
		for k := range from {
			own[k] = true
		}
		c.Flags().VisitAll(func(f *pflag.Flag) { own[f.Name] = true })
		c.PersistentFlags().VisitAll(func(f *pflag.Flag) { own[f.Name] = true })
		return own
	}
	var walk func(path string, c *cobra.Command, inherited map[string]bool)
	walk = func(path string, c *cobra.Command, inherited map[string]bool) {
		own := collect(c, inherited)
		out[path] = own
		for _, sub := range c.Commands() {
			if sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			walk(path+" "+sub.Name(), sub, own)
		}
	}
	rootOwn := collect(root, nil)
	for _, sub := range root.Commands() {
		if sub.Name() == "help" || sub.Name() == "completion" {
			continue
		}
		walk(sub.Name(), sub, rootOwn)
	}
	return out
}

// adviceCorpus collects every sentence the advice surfaces can emit.
func adviceCorpus(t *testing.T) []adviceLine {
	t.Helper()
	var out []adviceLine
	add := func(where, text string) {
		if strings.TrimSpace(text) != "" {
			out = append(out, adviceLine{where: where, text: text})
		}
	}

	cs := changeset.Changeset{Slug: "beta", Branch: "beta", Base: "alpha", BaseChangeset: "alpha"}
	lc := landingContract{Destination: "refs/heads/main"}
	waiting := landingContract{Destination: "main", WaitingOn: "alpha"}

	// The landing sentence, in each shape it takes.
	add("landingNextAction/base", landingNextAction(landingContract{}))
	add("landingNextAction/own", landingNextAction(lc))
	add("landingNextAction/note", landingNextAction(landingContract{Destination: "main", DestinationNote: " (where alpha landed)"}))
	add("landingNextAction/waiting", landingNextAction(waiting))
	// The waiting sentence for the state where the parent's screen cannot answer: this one names the command
	// that resolves the stack instead of pointing at a screen, and the flags it names on `init` are the ones
	// the build has.
	add("waitingNextAction/unreachable-parent",
		waitingNextAction(landingContract{Destination: "main", WaitingOn: "booking", ParentGone: true}))

	// The state sentences, which name the commands a changeset runs next.
	for _, tc := range []struct {
		state    model.State
		stale    bool
		declared declaredPath
		suffix   string
	}{
		{model.StateWorking, false, declaredPath{}, ""},
		{model.StateReady, false, declaredPath{}, ""},
		{model.StateFeedback, false, declaredPath{}, ""},
		{model.StateFeedback, true, declaredPath{}, "/stale"},
		{model.StateApproved, false, declaredPath{}, ""},
		{model.StateApproved, true, declaredPath{}, "/stale"},
		// The declared path in each of its three answers: a declaration to publish, a request already
		// visible, and a declaration that no longer names this head.
		{model.StateIntegrating, false, declaredPath{CoversHead: true}, "/unpublished"},
		{model.StateIntegrating, false, declaredPath{CoversHead: true, Pushed: true}, "/pushed"},
		{model.StateIntegrating, true, declaredPath{}, "/stale"},
	} {
		summary := lifecycle.Summary{State: tc.state, Stale: tc.stale}
		add("nextAction/"+string(tc.state)+tc.suffix, nextAction(summary, lc, "beta", "beta", tc.declared))
		add("waitNextAction/"+string(tc.state), waitNextAction(tc.state, "HEAD", lc))
	}
	abandoned := &lifecycle.Event{Kind: lifecycle.KindAbandoned, SHA: "abcdef1"}
	add("nextAction/abandoned",
		nextAction(lifecycle.Summary{State: model.StateWorking, Abandoned: abandoned}, lc, "beta", "beta", declaredPath{}))
	for _, o := range []model.Outcome{model.OutcomeBlock, model.OutcomeFeedback, model.OutcomeApprove} {
		add("nextActionFor/"+string(o), nextActionFor(o, lc))
		add("nextActionFor/"+string(o)+"/waiting", nextActionFor(o, waiting))
	}

	// The parent sentences: the restack step, the rewrite it stands in for, and the two refusals.
	for name, st := range map[string]parentStatus{
		"rebase":     {Branch: "alpha", Tip: "1234567", Landed: "abcdef1"},
		"stale":      {Branch: "alpha", Tip: "1234567", Landed: "abcdef1", StaleBranch: true},
		"worktree":   {Branch: "alpha", Tip: "1234567", Landed: "abcdef1", StaleBranch: true, ParentWorktree: "/w/alpha"},
		"on-landing": {Branch: "alpha", Landed: "abcdef1", LandingUnderHead: true},
		"stale+head": {Branch: "alpha", Tip: "1234567", Landed: "abcdef1", StaleBranch: true, LandingUnderHead: true},
		"unlanded":   {Branch: "alpha", Tip: "1234567", Changeset: "alpha"},
		"no-record":  {Branch: "alpha", Tip: "1234567"},
	} {
		add("landedParentStep/"+name, landedParentStep(cs, st))
		add("rewriteAdvice/"+name, rewriteAdvice(cs, st))
		for _, r := range unlandedParentReason(cs, st) {
			add("unlandedParentReason/"+name, r)
		}
	}
	// The conflicting-merge step, spelled by its own builder so it is covered without a repository whose
	// merge happens to conflict.
	add("conflictStep", conflictStep("main"))

	// The hand-off line, and its fallbacks: both name commands, and neither is allowed to name one the
	// build does not have.
	add("handoffLine", handoffLine(lc, "beta"))
	add("handoffLine/unknown", handoffLine(landingContract{}, ""))

	// The declared path's own sentences, each named directly so a flag that drifts on one of them is caught
	// even where no screen happens to print it today.
	add("pushNextAction", pushNextAction("beta", "main"))
	add("pushNextAction/unknown", pushNextAction("", ""))
	add("mergeWaitNextAction", mergeWaitNextAction(lc, "beta"))
	add("mergeWaitNextAction/unknown", mergeWaitNextAction(landingContract{}, ""))
	add("selfMergeLine", selfMergeLine(lc))
	add("undeclaredHeadLine", undeclaredHeadLine())

	return append(out, commandAdvice(t)...)
}

// commandAdvice runs the screens over a stacked pair at each point of its life, so a sentence assembled from
// a read at run time is checked as well as the builders that spell most of them.
func commandAdvice(t *testing.T) []adviceLine {
	t.Helper()
	var out []adviceLine
	add := func(where, text string) {
		if strings.TrimSpace(text) != "" {
			out = append(out, adviceLine{where: where, text: text})
		}
	}

	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n\nfunc main() {}\n"))
	f.CreateBranch("alpha")
	f.CommitChangeset("alpha", "main")
	f.Commit("alpha work", gittest.WithFile("a.go", "package main\n"))

	collect := func(where string, args ...string) {
		add(where, runAdviceCommand(t, f.Dir(), args...))
	}
	surfaces := func(where string) {
		// Three screens, human form: the `--json` siblings spell their sentences from the same builders the
		// corpus calls directly, and a fourth screen's worth of in-process runs would only make the rest of
		// the suite slower and the cache tests more likely to wobble.
		for _, args := range [][]string{
			{"status"}, {"check"}, {"queue"}, {"change", "integrate"},
		} {
			collect(where+"/"+strings.Join(args, " "), args...)
		}
		collect(where+"/change stack", "change", "stack")
		collect(where+"/change combine", "change", "combine")
	}

	surfaces("alpha/working")
	runAdviceCommand(t, f.Dir(), "change", "ready")
	surfaces("alpha/ready")
	f.CommitReviewMarker("alpha", "approve")
	surfaces("alpha/approved")

	// The child, stacked, and its parent still unlanded: the waiting sentence and the declaration refusal.
	f.CreateBranch("beta")
	f.Commit("changeset beta", gittest.WithFiles(map[string]string{
		"changesets/beta/CHANGESET.yaml": "id: beta\nbase: alpha\nbase-changeset: alpha\n",
		"changesets/beta/ABOUT.md":       "# beta\n",
	}), gittest.WithFile("b.go", "package main\n"))
	runAdviceCommand(t, f.Dir(), "change", "ready")
	surfaces("beta/ready")
	f.CommitReviewMarker("beta", "approve")
	surfaces("beta/approved")
	collect("beta/approved/review submit", "review", "submit", "--approve")

	// The parent lands, and the same screens answer differently: the restack step, the comparison, and a
	// declaration that is now accepted.
	f.SwitchTo("alpha")
	f.CommitReviewMarker("alpha", "approve")
	f.SwitchTo("main")
	f.MustGit("merge", "--no-ff", "--no-edit", "-m", "land alpha", "alpha")
	f.SwitchTo("beta")
	surfaces("beta/parent-landed")
	runAdviceCommand(t, f.Dir(), "change", "integrate")
	surfaces("beta/integrating")

	return out
}

// runAdviceCommand runs one command in dir and returns everything it printed.
//
// It is the in-process twin of the harness's runner, needed because this check asks the command tree in the
// same package that owns it. A refusal is advice too, so an error from the command is the output, not a
// failure of the test.
func runAdviceCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(old); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	var out, errb bytes.Buffer
	root := newRootCommand(&app{stdout: &out, stderr: &errb})
	root.SetArgs(args)
	root.SetOut(&out)
	root.SetErr(&errb)
	_, _ = root.ExecuteC()
	return out.String() + errb.String()
}
