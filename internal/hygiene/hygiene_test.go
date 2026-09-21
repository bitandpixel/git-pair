package hygiene

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// PRD §26 makes merging, squashing, pushing and remote enforcement explicit
// non-goals, and the plan turns that into an invariant: "No destructive verbs. No push,
// merge, rebase, reset, branch -D, or update-ref -d anywhere in the codebase (enforced
// by a test that greps the source)."
//
// A substring grep cannot express that. `git.MergeBase` legitimately passes
// "merge-base", `--json` output legitimately has a "branch" key, and
// `init` tells the user to "run `git switch -c <branch>`" — all three contain
// forbidden words while invoking nothing forbidden. So this test reads the source as
// syntax and only looks at *git invocations*: the argument slices handed to the
// helpers in internal/git, to repo.Git/GitStdin/GitInherit, and to exec.Command.
//
// Three layers, from strongest to narrowest:
//
//   - Layer A, argv: a git-invocation argument whose whitespace-separated field equals
//     a forbidden subcommand. "merge-base" is a different word and never matches;
//     "reset" is caught even when written out of order ("run("--hard", "reset")).
//   - Layer B, bare verb: a string literal *equal* to push/merge/rebase/reset anywhere
//     in the file. This closes the hole Layer A leaves when a call spreads a slice
//     variable (`r.Git(append([]string{"show"}, flags...)...)`) whose contents the call
//     site cannot show. It is deliberately not applied to "branch"/"switch"/"checkout",
//     which appear as `--json` keys and struct tags.
//   - Layer C, shell: arguments of exec.Command/exec.CommandContext scanned as shell
//     text, which catches `exec.Command("/bin/sh", "-c", "git push ...")`. Applied
//     only to real process invocations, so help text may mention git verbs freely.

// gitCallMethods are the functions through which a git command line is built. Every
// git-pair git invocation goes through one of them; internal/git.run is the shared
// implementation the exported wrappers delegate to.
var gitCallMethods = map[string]bool{
	"Git":                     true, // (*git.Repo).Git
	"GitStdin":                true,
	"GitInherit":              true,
	"GitInheritDiscardOutput": true,
	"run":                     true, // (*git.Repo).run
	// The fixture's own wrappers, so scanning internal/gittest sees what scanning the
	// product sees.
	"MustGit": true,
	"GitIn":   true,
}

// productArgvVerbs may never appear as a git subcommand in shipped code.
var productArgvVerbs = map[string]string{
	"push":     "PRD §26: git-pair does not push; review history is preserved locally (PRD §13)",
	"merge":    "PRD §26: git-pair does not merge branches",
	"rebase":   "PRD §26: git-pair does not rewrite history",
	"reset":    "PRD §26: git-pair does not reset the working tree or index",
	"switch":   "git-pair never changes what is checked out; the worktree belongs to the user",
	"checkout": "git-pair never changes what is checked out; the worktree belongs to the user",
	"branch":   "git-pair never creates, renames or deletes a branch: a changeset is the branch already checked out (PRD §9.1)",
}

// destructiveVerbs are forbidden even as bare strings, because a verb hidden behind a
// spread slice would otherwise be invisible to the argv layer.
var destructiveVerbs = map[string]string{
	"push":   "PRD §26: git-pair does not push",
	"merge":  "PRD §26: git-pair does not merge",
	"rebase": "PRD §26: git-pair does not rewrite history",
	"reset":  "PRD §26: git-pair does not reset the working tree or index",
}

// forbiddenBranchFlags delete a branch when passed to `git branch`.
var forbiddenBranchFlags = map[string]bool{"-D": true, "-d": true, "--delete": true}

// shellGitVerb finds a destructive git command line inside text passed to a shell.
var shellGitVerb = regexp.MustCompile(`(?i)\bgit\s+(push|merge|rebase|reset|switch|checkout|branch)\b`)

// branchDeletion finds `git branch -D <name>` written as one string.
var branchDeletion = regexp.MustCompile(`(?i)\bgit\s+branch\s+(-[Dd]|--delete)\b`)

// forbiddenFuncNames are wrapper names that would put a destructive verb behind an API.
var forbiddenFuncNames = map[string]string{
	"Push":              "git-pair must not push",
	"Merge":             "git-pair must not merge",
	"Rebase":            "git-pair must not rebase",
	"Reset":             "git-pair must not reset",
	"Squash":            "git-pair must not squash",
	"DeleteBranch":      "git-pair must not delete branches",
	"RemoveBranch":      "git-pair must not delete branches",
	"CreateBranch":      "git-pair must not create branches",
	"CheckoutBranch":    "git-pair must not switch branches",
	"ForceDeleteBranch": "git-pair must not delete branches",
}

// rules is one policy: which verbs are forbidden where.
type rules struct {
	// argv is the Layer A verb set.
	argv map[string]string
	// bare is the Layer B verb set; nil disables the layer.
	bare map[string]string
	// funcNames are wrapper names that must not exist; nil disables the layer.
	funcNames map[string]string
	// dirs are the directories to scan, relative to the module root.
	dirs []string
	// exclude is scanned by no layer. internal/gittest drives real repositories for
	// the tests, so it needs branch creation and deletion to stage scenarios.
	exclude []string
}

func TestSourceNeverInvokesDestructiveGitVerbs(t *testing.T) {
	// scan fails the test if it reads no files, so an empty result here really means
	// "no forbidden invocation" rather than "nothing was examined".
	for _, f := range scan(t, rules{
		argv:      productArgvVerbs,
		bare:      destructiveVerbs,
		funcNames: forbiddenFuncNames,
		dirs:      []string{"cmd", "internal"},
		exclude:   []string{filepath.Join("internal", "gittest")},
	}) {
		t.Error(f)
	}
}

// TestFixturePackageNeverInvokesProductForbiddenVerbs covers the one directory the
// product scan excludes. The fixture may create, switch and force-delete branches to
// stage scenarios the CLI itself refuses to build, but it must still never push, merge,
// rebase or reset: that is what keeps the suite from mutating anything by accident, and
// `git merge` in a fixture would quietly change what the span tests observe.
func TestFixturePackageNeverInvokesProductForbiddenVerbs(t *testing.T) {
	dir := filepath.Join("internal", "gittest")
	findings := scan(t, rules{
		argv: destructiveVerbs,
		bare: destructiveVerbs,
		dirs: []string{dir},
	})
	for _, f := range findings {
		t.Error(f)
	}

	// And the exclusion must be justified: the fixture really does use the
	// branch-management verbs that shipped code is forbidden to use. If this stops
	// reporting, the exclusion has become unnecessary and should be dropped.
	broad := scan(t, rules{argv: productArgvVerbs, dirs: []string{dir}})
	if len(broad) == 0 {
		t.Errorf("internal/gittest no longer uses any branch or switch verb; drop it from the exclusion list")
	}
}

// TestDetectorCatchesInjectedViolations proves the scan is not vacuous. It reads a
// source file containing each forbidden form next to each benign lookalike, and requires
// exactly the marked lines to be reported — so a rule that stopped matching, or one that
// started matching legitimate code, fails here first.
func TestDetectorCatchesInjectedViolations(t *testing.T) {
	lines := []string{
		`package bad`,
		``,
		`import (`,
		`	"fmt"`,
		`	"os/exec"`,
		`)`,
		``,
		`// Comments about push, merge, rebase, reset and branch -D must not be flagged.`,
		``,
		`type repo struct{}`,
		``,
		`func (r *repo) Git(args ...string) (string, error)                 { return "", nil }`,
		`func (r *repo) GitInherit(args ...string) error                    { return nil }`,
		`func (r *repo) GitStdin(stdin string, args ...string) (string, error) { return "", nil }`,
		`func (r *repo) run(args ...string) (string, error)                  { return "", nil }`,
		``,
		`// Layer A: forbidden verbs as git arguments.`,
		`func (r *repo) push()   { r.Git("push", "origin") } // BAD`,
		`func (r *repo) merge()  { r.Git("merge", "main") } // BAD`,
		`func (r *repo) rebase() { r.GitInherit("rebase", "main") } // BAD`,
		`func (r *repo) reset()  { r.run("--hard", "reset") } // BAD: argument order does not matter`,
		`func (r *repo) sw()     { r.Git("switch", "-c", "feature") } // BAD`,
		`func (r *repo) co()     { r.Git("checkout", "main") } // BAD`,
		`func (r *repo) mkBr()   { r.Git("branch", "feature") } // BAD`,
		`func (r *repo) delBr()  { r.Git("branch", "-D", "feature") } // BAD: must report once, not twice`,
		`func (r *repo) delRef() { r.Git("update-ref", "-d", "refs/x") } // BAD`,
		`func (r *repo) nulRef() { r.Git("update-ref", "refs/x", "") } // BAD: an empty new value deletes the ref`,
		``,
		`// Layer C: verbs smuggled through a shell or a direct git exec.`,
		`func (r *repo) shell()  { exec.Command("/bin/sh", "-c", "git push origin HEAD") } // BAD`,
		`func (r *repo) shBr()   { exec.Command("sh", "-c", "git branch -D feature") } // BAD`,
		`func (r *repo) direct() { exec.Command("git", "reset", "--hard") } // BAD`,
		`func (r *repo) swExec() { exec.Command("git", "switch", "main") } // BAD`,
		``,
		`// Layer B: a bare verb string, e.g. one spread into a git call elsewhere.`,
		`var flags = []string{"rebase", "main"} // BAD`,
		`func (r *repo) Push() {} // BAD: a wrapper name puts a forbidden verb behind an API`,
		``,
		`// Benign lookalikes: none of these may be reported.`,
		`func (r *repo) base()   { r.Git("merge-base", "main", "HEAD") } // OK: merge-base is not merge`,
		`func (r *repo) ok1()    { r.Git("commit", "--allow-empty", "-m", "restore ideas") } // OK`,
		`func (r *repo) ok2()    { r.Git("for-each-ref", "refs/git-pair/changesets") } // OK`,
		`func (r *repo) ok3()    { r.Git("symbolic-ref", "--short", "HEAD") } // OK`,
		"func (r *repo) prose()  { fmt.Errorf(\"run `git switch -c <branch>` first\") } // OK: help text, not an exec",
		`func (r *repo) keys() map[string]any { return map[string]any{"branch": "main", "checkout": false} } // OK: --json keys`,
		``,
		`type view struct {`,
		"\tBranch string `json:\"branch\"` // OK: struct tag",
		`}`,
		``,
		`const help = "This command never merges, pushes, rebases or resets anything." // OK: prose`,
	}

	var want []int
	for i, line := range lines {
		if strings.Contains(line, "// BAD") {
			want = append(want, i+1)
		}
	}
	if len(want) == 0 {
		t.Fatal("the fixture marks no violations; the self-test proves nothing")
	}

	path := filepath.Join(t.TempDir(), "bad.go")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	findings := dedupe(checkFile(t, path, productArgvVerbs, destructiveVerbs, forbiddenFuncNames))

	got := make([]int, 0, len(findings))
	for _, f := range findings {
		got = append(got, f.Line)
	}
	sort.Ints(got)
	if strings.Join(ints(got), ",") != strings.Join(ints(want), ",") {
		report := make([]string, 0, len(findings))
		for _, f := range findings {
			report = append(report, f.String())
		}
		t.Errorf("reported lines = %v, want %v\nfindings:\n%s", got, want, strings.Join(report, "\n"))
	}
}

// --- scanning ---------------------------------------------------------------

type finding struct {
	File string
	Line int
	Msg  string
}

func (f finding) String() string { return f.File + ":" + strconv.Itoa(f.Line) + ": " + f.Msg }

// scan applies one rule set to every non-test .go file under rules.dirs.
func scan(t *testing.T, r rules) []string {
	t.Helper()
	root := moduleRoot(t)
	var findings []finding
	count := 0
	for _, dir := range r.dirs {
		for _, path := range goSourceFiles(t, filepath.Join(root, dir), r.exclude) {
			count++
			findings = append(findings, checkFile(t, path, r.argv, r.bare, r.funcNames)...)
		}
	}
	if count == 0 {
		t.Fatalf("scanned no .go files under %v; the scan is vacuous", r.dirs)
	}
	out := make([]string, 0, len(findings))
	for _, f := range dedupe(findings) {
		out = append(out, f.String())
	}
	return out
}

// dedupe collapses multiple reasons reported for the same line into one finding, so a
// verb caught by two layers is reported once with both reasons.
func dedupe(in []finding) []finding {
	var out []finding
	index := map[string]int{}
	for _, f := range in {
		key := f.File + ":" + strconv.Itoa(f.Line)
		if at, ok := index[key]; ok {
			out[at].Msg += " (also: " + f.Msg + ")"
			continue
		}
		index[key] = len(out)
		out = append(out, f)
	}
	return out
}

// checkFile reads one file as syntax and reports every forbidden git invocation.
func checkFile(t *testing.T, path string, argvVerbs, bareVerbs, funcNames map[string]string) []finding {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	root := moduleRoot(t)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	at := func(pos token.Pos, msg string) finding {
		return finding{File: rel, Line: fileSet.Position(pos).Line, Msg: msg}
	}

	var findings []finding

	// Layers A and C: every call expression that builds a command line.
	ast.Inspect(file, func(node ast.Node) bool {
		if decl, ok := node.(*ast.FuncDecl); ok && decl.Name != nil {
			if why, bad := funcNames[decl.Name.Name]; bad {
				findings = append(findings, at(decl.Pos(),
					"git-pair defines a wrapper named "+decl.Name.Name+" ("+why+")"))
			}
			return true
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch classify(call) {
		case callGit:
			findings = append(findings, checkGitArgs(call, argvVerbs, at)...)
		case callExec:
			// Layer C: an exec argument may itself be a shell command line.
			for _, lit := range stringLiterals(call.Args...) {
				if shellGitVerb.MatchString(lit.value) {
					findings = append(findings, at(call.Pos(),
						"git-pair runs a destructive git command through a shell: "+strconv.Quote(lit.value)))
				}
			}
		}
		return true
	})

	// Layer B: a bare destructive verb anywhere, including in a slice built once and
	// spread into a git call somewhere else.
	if bareVerbs != nil {
		ast.Inspect(file, func(node ast.Node) bool {
			basic, ok := node.(*ast.BasicLit)
			if !ok || basic.Kind != token.STRING {
				return true
			}
			value := unquote(basic.Value)
			if why, bad := bareVerbs[value]; bad {
				findings = append(findings, at(basic.Pos(),
					"the string "+strconv.Quote(value)+" is a git subcommand git-pair must never invoke — "+why))
			}
			return true
		})
	}
	return findings
}

type callKind int

const (
	callOther callKind = iota
	callGit
	callExec
)

// classify reports whether a call builds a git command line, either through one of
// git-pair's git helpers or through exec.Command("git", ...).
func classify(call *ast.CallExpr) callKind {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return callOther
	}
	switch sel.Sel.Name {
	case "Command", "CommandContext":
		if len(call.Args) == 0 {
			return callOther
		}
		for _, lit := range stringLiterals(call.Args[0]) {
			if lit.value == "git" {
				return callGit
			}
		}
		return callExec
	default:
		if gitCallMethods[sel.Sel.Name] {
			return callGit
		}
		return callOther
	}
}

// checkGitArgs rejects forbidden subcommands, branch deletion and destructive
// update-ref forms in a git argument list.
func checkGitArgs(call *ast.CallExpr, argvVerbs map[string]string, at func(token.Pos, string) finding) []finding {
	var findings []finding
	args := stringLiterals(call.Args...)

	seen := map[string]bool{}
	for _, arg := range args {
		seen[arg.value] = true
		for _, field := range strings.Fields(arg.value) {
			why, bad := argvVerbs[field]
			if !bad || field == "branch" {
				continue
			}
			findings = append(findings, at(call.Pos(), "git-pair invokes `git "+field+"` — "+why))
		}
	}

	// `git branch` is reported once per call, with the more specific message when a
	// deletion flag is present. It is only a finding in rule sets that forbid the
	// verb at all (shipped code); the fixture is allowed to manage branches.
	if _, forbidden := argvVerbs["branch"]; forbidden && seen["branch"] {
		why := argvVerbs["branch"]
		msg := "git-pair invokes `git branch` — " + why
		for _, arg := range args {
			if forbiddenBranchFlags[arg.value] {
				msg = "git-pair deletes a branch with `git branch " + arg.value + "` — " + why
				break
			}
		}
		findings = append(findings, at(call.Pos(), msg))
	}

	if seen["update-ref"] {
		for _, arg := range args {
			switch arg.value {
			case "-d", "--no-deref":
				findings = append(findings, at(call.Pos(),
					"git-pair deletes a ref with `git update-ref "+arg.value+"` (PRD §13 requires review refs to survive)"))
			case "":
				// `git update-ref <ref> ""` deletes the ref just as -d does.
				findings = append(findings, at(call.Pos(),
					"git-pair passes an empty new value to `git update-ref`, which deletes the ref"))
			}
		}
	}
	return findings
}

type literal struct{ value string }

// stringLiterals returns every string literal inside the given expressions, including
// inside composite literals and slices spread with `...`.
func stringLiterals(exprs ...ast.Expr) []literal {
	var out []literal
	for _, expr := range exprs {
		ast.Inspect(expr, func(node ast.Node) bool {
			basic, ok := node.(*ast.BasicLit)
			if !ok || basic.Kind != token.STRING {
				return true
			}
			out = append(out, literal{value: unquote(basic.Value)})
			return true
		})
	}
	return out
}

func unquote(raw string) string {
	value, err := strconv.Unquote(raw)
	if err != nil {
		// A raw string with a stray escape: fall back to the raw text so a forbidden
		// verb cannot hide behind an unquoting error.
		return strings.Trim(raw, "`\"")
	}
	return value
}

func ints(ns []int) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, strconv.Itoa(n))
	}
	return out
}

// --- filesystem -------------------------------------------------------------

// moduleRoot walks up from the test's working directory to the module root.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's working directory")
		}
		dir = parent
	}
}

// goSourceFiles returns the non-test .go files under dir, skipping excludedDirs.
func goSourceFiles(t *testing.T, dir string, excludeDirs []string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			for _, excluded := range excludeDirs {
				if rel, relErr := filepath.Rel(moduleRoot(t), path); relErr == nil {
					if rel == excluded || strings.HasPrefix(rel, excluded+string(filepath.Separator)) {
						return filepath.SkipDir
					}
				}
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	sort.Strings(out)
	return out
}

// TestDurableRefsAreOnlyEverCreated is the mechanical half of "no command can move or delete a
// durable ref". The verb list cannot express it: `git update-ref <ref> <sha> <old>` is an ordinary git
// command, and it is precisely the operation git-pair must never perform, because the two families
// under refs/git-pair are records rather than pointers — the answer to "what was reviewed, and what did
// it become", written once, at landing.
//
// So this asks a narrower question of every non-test source: where does git-pair invoke `update-ref`
// at all, and with what? The answer has to be one call, inside internal/git, passing git's all-zeros
// old value — the form that asks git to fail if the name is already taken. There is no second write,
// and so no way for a command written next year to move a record while believing it is creating one.
func TestDurableRefsAreOnlyEverCreated(t *testing.T) {
	root := moduleRoot(t)
	const createOnly = "zeroOID"

	var sites []string
	var zeroValue string
	moved := 0
	for _, path := range goSourceFiles(t, filepath.Join(root, "internal"), nil) {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.ValueSpec:
				// The old value the one allowed write passes. Its value is the whole
				// difference between creating a ref and moving one, so it is checked rather
				// than trusted.
				for _, name := range n.Names {
					if name.Name != createOnly {
						continue
					}
					for _, value := range n.Values {
						if lit, ok := value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
							zeroValue = unquote(lit.Value)
						}
					}
				}
			case *ast.CallExpr:
				if classify(n) != callGit {
					return true
				}
				namesRef := false
				for _, lit := range stringLiterals(n.Args...) {
					if lit.value == "update-ref" {
						namesRef = true
					}
				}
				if !namesRef {
					return true
				}
				sites = append(sites, rel+":"+strconv.Itoa(fileSet.Position(n.Pos()).Line))
				passesZeroOld := false
				for _, arg := range n.Args {
					if ident, ok := arg.(*ast.Ident); ok && ident.Name == createOnly {
						passesZeroOld = true
					}
				}
				if !passesZeroOld {
					moved++
				}
			}
			return true
		})
	}

	if len(sites) != 1 {
		t.Fatalf("shipped code invokes `git update-ref` at %v; want exactly one call site, the create-only write in internal/git",
			sites)
	}
	if want := filepath.Join("internal", "git") + string(filepath.Separator); !strings.HasPrefix(sites[0], want) {
		t.Errorf("the only `update-ref` call is at %s, want it in %s, which owns every ref write", sites[0], want)
	}
	if moved > 0 {
		t.Errorf("%d of the %d `update-ref` calls pass no all-zeros old value, so they can move a durable ref; neither family may be moved",
			moved, len(sites))
	}
	if zeroValue == "" {
		t.Fatal("no all-zeros old-value constant was found; the one allowed write cannot be create-only without it")
	}
	if len(zeroValue) != 40 || strings.Trim(zeroValue, "0") != "" {
		t.Errorf("the old value passed to `update-ref` is %q, want git's 40-zero all-zeros object id", zeroValue)
	}
}
