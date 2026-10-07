// Package gittest builds throwaway git repositories for git-pair's tests.
//
// The fixture deliberately does NOT import gitpair/internal/git. It shells out to
// git through its own subprocess calls, so a bug in the package under test
// cannot hide inside the code that creates the scenario: if `git log` parsing is
// broken, the fixtures still report what git actually stored.
//
// Every repository is created under t.TempDir() and is isolated from the
// developer's environment:
//
//   - GIT_CONFIG_GLOBAL points at an empty file and GIT_CONFIG_SYSTEM at
//     /dev/null, so no global or system gitconfig (including aliases, hooks,
//     diff.external, credential helpers) can influence a test;
//   - HOME points at an empty directory;
//   - user.name, user.email and commit.gpgsign are set locally in the repo;
//   - VISUAL/EDITOR are replaced with a no-op so no test can launch a real
//     editor, and stdin is /dev/null so nothing can block on a terminal.
//
// Repositories start on an unborn branch "main" with no commits, so a test can
// decide what the root commit is (survival's root-commit case needs that).
package gittest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Identities used for fixture commits.
const (
	AuthorName    = "Test Author"
	AuthorEmail   = "author@example.invalid"
	ReviewerName  = "Test Reviewer"
	ReviewerEmail = "reviewer@example.invalid"
)

// Fixture is one throwaway repository.
type Fixture struct {
	t   *testing.T
	dir string
	env []string
}

// New creates an empty repository on branch "main" with no commits.
func New(t *testing.T) *Fixture {
	t.Helper()

	root := t.TempDir()
	dir, err := filepath.EvalSymlinks(mustMkdir(t, filepath.Join(root, "repo")))
	if err != nil {
		t.Fatalf("gittest: create repository directory: %v", err)
	}
	home := mustMkdir(t, filepath.Join(root, "home"))
	globalConfig := filepath.Join(root, "gitconfig")
	if err := os.WriteFile(globalConfig, nil, 0o644); err != nil {
		t.Fatalf("gittest: create empty gitconfig: %v", err)
	}

	// Isolate the whole test process, not just this fixture's own subprocesses:
	// the code under test shells out to git with the process environment.
	for k, v := range map[string]string{
		"HOME":                home,
		"GIT_CONFIG_GLOBAL":   globalConfig,
		"GIT_CONFIG_SYSTEM":   "/dev/null",
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_PAGER":           "cat",
		"PAGER":               "cat",
		"LC_ALL":              "C",
		"LANG":                "C",
		"VISUAL":              "",
		"EDITOR":              "/bin/true",
	} {
		t.Setenv(k, v)
	}

	f := &Fixture{t: t, dir: dir, env: sanitizedEnv()}
	if out, errB, err := f.run(dir, nil, "init", "--quiet", "--initial-branch=main", "."); err != nil {
		t.Fatalf("gittest: git init: %v\n%s\n%s", err, out, errB)
	}
	f.writeLocalConfig()
	return f
}

// fixtureLocalConfig is the repository config every fixture needs. It is one file
// write rather than seven `git config --local` calls, which is seven git subprocesses
// per fixture: the suite builds hundreds, and the same keys are written every time.
// `Config` remains for the tests that change one key; this is the fixed starting point.
//
// Every value here is a constant this package owns, so the text needs no escaping.
const fixtureLocalConfig = "[user]\n" +
	"\tname = \"" + AuthorName + "\"\n" +
	"\temail = \"" + AuthorEmail + "\"\n" +
	"[commit]\n\tgpgsign = false\n" +
	"[tag]\n\tgpgsign = false\n" +
	"[core]\n\tautocrlf = false\n\tfsmonitor = false\n" +
	"[gc]\n\tauto = 0\n"

// writeLocalConfig appends fixtureLocalConfig to the config `git init` just wrote.
// git reads a config file top to bottom and a later value of a non-multi key wins,
// so appending is how these keys override anything `init` put there.
func (f *Fixture) writeLocalConfig() {
	f.t.Helper()
	path := filepath.Join(f.dir, ".git", "config")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		f.t.Fatalf("gittest: open %s: %v", path, err)
	}
	defer file.Close()
	if _, err := file.WriteString("\n" + fixtureLocalConfig); err != nil {
		f.t.Fatalf("gittest: write %s: %v", path, err)
	}
}

// Dir is the repository's absolute path (its git toplevel).
func (f *Fixture) Dir() string { return f.dir }

// Env is the environment fixture subprocesses run with. Tests that construct a
// git.Repo directly may pass it as Repo.Env for the same isolation.
func (f *Fixture) Env() []string { return f.env }

// --- raw git ----------------------------------------------------------------

// Git runs git in the repository and returns stdout. The returned error carries
// stderr, so a test can assert on a failure without t.Fatal.
func (f *Fixture) Git(args ...string) (string, error) {
	f.t.Helper()
	out, errOut, err := f.run(f.dir, nil, args...)
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errOut))
	}
	return out, nil
}

// MustGit runs git and fails the test if it exits non-zero.
func (f *Fixture) MustGit(args ...string) string {
	f.t.Helper()
	out, err := f.Git(args...)
	if err != nil {
		f.t.Fatalf("gittest: %v", err)
	}
	return out
}

// GitIn runs git with the repository as cwd but a different argument root, for
// commands that need a working directory inside the tree.
func (f *Fixture) GitIn(relDir string, args ...string) (string, error) {
	f.t.Helper()
	out, errOut, err := f.run(filepath.Join(f.dir, relDir), nil, args...)
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errOut))
	}
	return out, nil
}

func (f *Fixture) run(dir string, extraEnv []string, args ...string) (string, string, error) {
	full := append([]string{"-c", "core.quotePath=false"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = mergeEnv(f.env, extraEnv)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// Config sets one local repository config key.
func (f *Fixture) Config(key, value string) {
	f.t.Helper()
	f.MustGit("config", "--local", key, value)
}

// InstallHook writes an executable hook into .git/hooks and returns its path, so
// a test can make git itself fail on purpose.
func (f *Fixture) InstallHook(name, script string) string {
	f.t.Helper()
	path := filepath.Join(f.dir, ".git", "hooks", name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		f.t.Fatalf("gittest: install hook %s: %v", name, err)
	}
	return path
}

// RemoveHook deletes a hook installed by InstallHook.
func (f *Fixture) RemoveHook(path string) {
	f.t.Helper()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		f.t.Fatalf("gittest: remove hook %s: %v", path, err)
	}
}

// --- working tree -----------------------------------------------------------

// Write creates or replaces a working-tree file, making parent directories.
func (f *Fixture) Write(relPath, content string) {
	f.t.Helper()
	abs := filepath.Join(f.dir, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		f.t.Fatalf("gittest: mkdir for %s: %v", relPath, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		f.t.Fatalf("gittest: write %s: %v", relPath, err)
	}
}

// Append adds content to a working-tree file, creating it when absent.
func (f *Fixture) Append(relPath, content string) {
	f.t.Helper()
	abs := filepath.Join(f.dir, relPath)
	data, err := os.ReadFile(abs)
	if err != nil && !os.IsNotExist(err) {
		f.t.Fatalf("gittest: read %s: %v", relPath, err)
	}
	f.Write(relPath, string(data)+content)
}

// Read returns a working-tree file's contents.
func (f *Fixture) Read(relPath string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, relPath))
	if err != nil {
		f.t.Fatalf("gittest: read %s: %v", relPath, err)
	}
	return string(data)
}

// HasWorktreeFile reports whether a working-tree path exists.
func (f *Fixture) HasWorktreeFile(relPath string) bool {
	f.t.Helper()
	_, err := os.Stat(filepath.Join(f.dir, relPath))
	return err == nil
}

// Remove deletes a working-tree path.
func (f *Fixture) Remove(relPath string) {
	f.t.Helper()
	if err := os.RemoveAll(filepath.Join(f.dir, relPath)); err != nil {
		f.t.Fatalf("gittest: remove %s: %v", relPath, err)
	}
}

// Chmod sets the mode of a working-tree file. The mode is part of what a diff changes, so a test that
// wants one has to change the file on disk: staging a mode with `update-index --chmod` alone does not
// survive the next `add -A`, which re-reads it from the working tree.
func (f *Fixture) Chmod(relPath string, mode os.FileMode) {
	f.t.Helper()
	if err := os.Chmod(filepath.Join(f.dir, relPath), mode); err != nil {
		f.t.Fatalf("gittest: chmod %s: %v", relPath, err)
	}
}

// Rename moves a working-tree file. The move is recorded by the next commit.
func (f *Fixture) Rename(from, to string) {
	f.t.Helper()
	abs := filepath.Join(f.dir, to)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		f.t.Fatalf("gittest: mkdir for %s: %v", to, err)
	}
	if err := os.Rename(filepath.Join(f.dir, from), abs); err != nil {
		f.t.Fatalf("gittest: rename %s -> %s: %v", from, to, err)
	}
}

// --- commits ----------------------------------------------------------------

type commitConfig struct {
	files      map[string]string
	allowEmpty bool
	noStage    bool
	env        []string
}

// CommitOpt tunes Fixture.Commit and Fixture.CommitMessage.
type CommitOpt func(*commitConfig)

// WithFile writes path with content before committing. Every WithFile path, and
// any other working-tree change, is staged by the commit.
func WithFile(path, content string) CommitOpt {
	return func(c *commitConfig) {
		if c.files == nil {
			c.files = map[string]string{}
		}
		c.files[path] = content
	}
}

// WithFiles writes several paths before committing.
func WithFiles(files map[string]string) CommitOpt {
	return func(c *commitConfig) {
		if c.files == nil {
			c.files = map[string]string{}
		}
		for path, content := range files {
			c.files[path] = content
		}
	}
}

// WithEmpty passes --allow-empty, which is how an approval with no edits is made.
func WithEmpty() CommitOpt {
	return func(c *commitConfig) { c.allowEmpty = true }
}

// WithNoStage commits only what is already in the index, leaving working-tree
// changes alone. Fixture.EmptyCommit uses it so an "empty" commit stays empty.
func WithNoStage() CommitOpt {
	return func(c *commitConfig) { c.noStage = true }
}

// WithAuthor commits as a different author, so reviewer commits are distinguishable.
func WithAuthor(name, email string) CommitOpt {
	return func(c *commitConfig) {
		c.env = append(c.env,
			"GIT_AUTHOR_NAME="+name,
			"GIT_AUTHOR_EMAIL="+email,
			"GIT_COMMITTER_NAME="+name,
			"GIT_COMMITTER_EMAIL="+email,
		)
	}
}

// WithDate fixes both commit dates, which makes age reporting deterministic. A history whose two
// dates disagree about the order is a history this option cannot build; use WithAuthorDate and
// WithCommitterDate for that.
func WithDate(when time.Time) CommitOpt {
	return func(c *commitConfig) {
		WithAuthorDate(when)(c)
		WithCommitterDate(when)(c)
	}
}

// WithAuthorDate fixes the author date alone, leaving the committer date to the clock.
//
// The two dates are separate options because they answer different questions, and a rebase pulls
// them apart: `git rebase` replays each commit as a new commit, so the author date keeps the day the
// work was written while the committer date becomes the minute of the rebase — every commit the
// replay touched then carries the same committer date, and the order they were made in is gone from
// it. Anything that puts history in order has to say which date it means, and a fixture that can only
// set both at once cannot ask the question at all.
func WithAuthorDate(when time.Time) CommitOpt {
	stamp := when.UTC().Format(time.RFC3339)
	return func(c *commitConfig) { c.env = append(c.env, "GIT_AUTHOR_DATE="+stamp) }
}

// WithCommitterDate fixes the committer date alone, leaving the author date to the clock. See
// WithAuthorDate for why the two dates are set apart.
func WithCommitterDate(when time.Time) CommitOpt {
	stamp := when.UTC().Format(time.RFC3339)
	return func(c *commitConfig) { c.env = append(c.env, "GIT_COMMITTER_DATE="+stamp) }
}

// Commit writes a commit whose message is subject, and returns its full SHA.
// Like a human running `git add -A && git commit`, it stages the whole working
// tree first, so files written with Write/StageChangeset land in the commit.
func (f *Fixture) Commit(subject string, opts ...CommitOpt) string {
	f.t.Helper()
	return f.CommitMessage(subject, opts...)
}

// EmptyCommit writes a commit with no changes at all. It does not stage, so
// working-tree changes stay uncommitted and the commit really is empty.
func (f *Fixture) EmptyCommit(subject string, opts ...CommitOpt) string {
	f.t.Helper()
	return f.CommitMessage(subject, append([]CommitOpt{WithEmpty(), WithNoStage()}, opts...)...)
}

// CommitMessage writes a commit with a raw (possibly multi-line) message. Use it
// to construct histories whose trailers the product never produced.
func (f *Fixture) CommitMessage(message string, opts ...CommitOpt) string {
	f.t.Helper()
	cfg := &commitConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	for path, content := range cfg.files {
		f.Write(path, content)
	}
	if !cfg.noStage {
		f.MustGit("add", "--all")
	}
	args := []string{"commit", "--quiet"}
	if cfg.allowEmpty {
		args = append(args, "--allow-empty")
	}
	args = append(args, "-m", message)
	out, errOut, err := f.run(f.dir, cfg.env, args...)
	if err != nil {
		f.t.Fatalf("gittest: commit %q: %v\n%s\n%s", firstLine(message), err, out, errOut)
	}
	return f.Head()
}

// --- lifecycle markers ------------------------------------------------------
//
// These build the commit messages PRD §9.2 and §10.4 specify. CLI tests assert
// that `git-pair` writes exactly these subjects and trailers; engine tests use
// them to build histories without depending on the product.

// ReadyMessage is a ready marker commit message (PRD §9.2).
func ReadyMessage(slug string) string {
	return "git-pair: ready " + slug + "\n\nReview-State: ready\nReview-Changeset: " + slug + "\n"
}

// IntegrateMessage is a `git pair change integrate` declaration (PRD §9.9): the state marker that
// hands an approved changeset to whoever owns the destination branch. head is the commit the
// declaration covers — pass "" for one that names none, which is the shape `check` refuses.
func IntegrateMessage(slug, head string) string {
	message := "git-pair: integrate " + slug + "\n\nReview-State: integrating\nReview-Changeset: " + slug
	if head != "" {
		message += "\nReview-Head: " + head
	}
	return message + "\n"
}

// ReviewMessage is a review submission commit message (PRD §10.4). outcome is
// "block", "feedback" or "approve". head is the commit the review speaks about — pass ""
// for a marker that names none, which is the shape a submission written before `Review-Head`
// existed has. parentHead is the tip of the branch this changeset is stacked on, or "" for an
// unstacked one.
func ReviewMessage(slug, outcome, head, parentHead string) string {
	return ReviewMessageRecorded(slug, outcome, head, parentHead, "", "")
}

// ReviewMessageRecorded is a review submission that records each of the ends of the diff it reviewed, as
// `review submit` does: the parent's tip, the commit the diff was measured from, and that diff's identity
// in the `<version>:<raw>[+<patch>]` form `model.FormatDiffID` renders.
//
// Each trailer is written only when its value is non-empty, which is how a fixture produces the shapes the
// product produces: an unstacked changeset records no parent, a submission that could not measure a base
// records neither base nor digest, and a marker from before a trailer existed records none of them. Pass
// a digest with an unknown version to produce the reading those markers get — "no digest recorded", not a
// content difference.
//
// The identity is taken as the rendered string rather than as a `model.DiffID` on purpose: half the shapes
// this suite has to ask about are values the product would never write — a `1:` marker, a truncated half,
// a trailer with no `+` — and a fixture that could only build valid ones could not test the readers that
// exist for the others.
func ReviewMessageRecorded(slug, outcome, head, parentHead, baseHead, diffID string) string {
	message := "review: " + outcome + " " + slug + "\n\nReview-Outcome: " + outcome +
		"\nReview-Changeset: " + slug
	if head != "" {
		message += "\nReview-Head: " + head
	}
	if parentHead != "" {
		message += "\nReview-Parent-Head: " + parentHead
	}
	if baseHead != "" {
		message += "\nReview-Base-Head: " + baseHead
	}
	if diffID != "" {
		message += "\nReview-Diff-Id: " + diffID
	}
	return message + "\n"
}

// CommitReadyMarker commits a ready marker for slug. Markers may be empty, so
// these helpers always allow an empty commit, as the product's do.
func (f *Fixture) CommitReadyMarker(slug string, opts ...CommitOpt) string {
	f.t.Helper()
	return f.CommitMessage(ReadyMessage(slug), append([]CommitOpt{WithEmpty()}, opts...)...)
}

// CommitReviewMarker commits a review submission with the given outcome. Like the product's
// `review submit`, the marker names the commit it was made against — except in a repository with no
// commits yet, where there is no head to name and the marker carries no `Review-Head`.
func (f *Fixture) CommitReviewMarker(slug, outcome string, opts ...CommitOpt) string {
	f.t.Helper()
	return f.CommitMessage(ReviewMessage(slug, outcome, f.reviewedHead(), ""), append([]CommitOpt{WithEmpty()}, opts...)...)
}

// CommitReviewMarkerOnParent commits a review submission that also records the tip of the branch
// this changeset is stacked on, as `review submit` does for a stacked changeset.
func (f *Fixture) CommitReviewMarkerOnParent(slug, outcome, parentHead string, opts ...CommitOpt) string {
	f.t.Helper()
	return f.CommitMessage(ReviewMessage(slug, outcome, f.reviewedHead(), parentHead), append([]CommitOpt{WithEmpty()}, opts...)...)
}

// CommitReviewMarkerRecorded commits a review submission that records all three ends of the diff it
// reviewed — the parent's tip, the measured base, and the diff's identity — as `review submit` does.
// Pass "" for the ends this fixture means to leave unrecorded.
func (f *Fixture) CommitReviewMarkerRecorded(slug, outcome, parentHead, baseHead, diffID string, opts ...CommitOpt) string {
	f.t.Helper()
	return f.CommitMessage(ReviewMessageRecorded(slug, outcome, f.reviewedHead(), parentHead, baseHead, diffID),
		append([]CommitOpt{WithEmpty()}, opts...)...)
}

// CommitIntegrateMarker commits a declaration for slug naming the current head, as `change
// integrate` does.
func (f *Fixture) CommitIntegrateMarker(slug string, opts ...CommitOpt) string {
	f.t.Helper()
	return f.CommitMessage(IntegrateMessage(slug, f.reviewedHead()), append([]CommitOpt{WithEmpty()}, opts...)...)
}

// CommitIntegrateMarkerOn commits a declaration naming `head` explicitly, for the cases the
// command itself cannot produce: a marker naming a commit this history no longer carries, or one
// naming nothing.
func (f *Fixture) CommitIntegrateMarkerOn(slug, head string, opts ...CommitOpt) string {
	f.t.Helper()
	return f.CommitMessage(IntegrateMessage(slug, head), append([]CommitOpt{WithEmpty()}, opts...)...)
}

// reviewedHead is HEAD, or "" where the repository has no commits yet.
func (f *Fixture) reviewedHead() string {
	f.t.Helper()
	out, err := f.Git("rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// --- branches ---------------------------------------------------------------

// CreateBranch creates a branch from HEAD (or from start point `from`, when
// given) and switches to it.
func (f *Fixture) CreateBranch(name string, from ...string) {
	f.t.Helper()
	args := append([]string{"switch", "--quiet", "-c", name}, from...)
	f.MustGit(args...)
}

// SwitchTo checks out an existing branch.
func (f *Fixture) SwitchTo(name string) {
	f.t.Helper()
	f.MustGit("switch", "--quiet", name)
}

// Detach checks out HEAD without a branch.
func (f *Fixture) Detach() {
	f.t.Helper()
	f.MustGit("switch", "--quiet", "--detach")
}

// ForceDeleteBranch runs `git branch -D`, which is how a test proves that review
// history survives the loss of its branch (PRD §13).
func (f *Fixture) ForceDeleteBranch(name string) {
	f.t.Helper()
	f.MustGit("branch", "-D", name)
}

// CurrentBranch is the checked-out branch, or "" when HEAD is detached.
func (f *Fixture) CurrentBranch() string {
	f.t.Helper()
	out, err := f.Git("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Branches lists local branch names.
func (f *Fixture) Branches() []string {
	f.t.Helper()
	out := f.MustGit("for-each-ref", "--format=%(refname:short)", "refs/heads")
	return lines(out)
}

// --- history and refs -------------------------------------------------------

// Head is the full SHA of HEAD.
func (f *Fixture) Head() string {
	f.t.Helper()
	return strings.TrimSpace(f.MustGit("rev-parse", "HEAD"))
}

// RevParse resolves any revision to a full SHA.
func (f *Fixture) RevParse(rev string) string {
	f.t.Helper()
	return strings.TrimSpace(f.MustGit("rev-parse", "--verify", "--quiet", rev))
}

// Short is git's default abbreviated SHA for a revision.
func (f *Fixture) Short(rev string) string {
	f.t.Helper()
	return strings.TrimSpace(f.MustGit("rev-parse", "--short", rev))
}

// Parent is the first parent of a revision.
func (f *Fixture) Parent(rev string) string {
	f.t.Helper()
	return strings.TrimSpace(f.MustGit("rev-parse", "--verify", rev+"^"))
}

// Subject is a commit's subject line.
func (f *Fixture) Subject(rev string) string {
	f.t.Helper()
	return strings.TrimSpace(f.MustGit("log", "-1", "--format=%s", rev))
}

// Message is a commit's full message.
func (f *Fixture) Message(rev string) string {
	f.t.Helper()
	return f.MustGit("log", "-1", "--format=%B", rev)
}

// Trailers returns a commit's trailer block as key/value pairs.
func (f *Fixture) Trailers(rev string) map[string]string {
	f.t.Helper()
	block := f.MustGit("log", "-1", "--format=%(trailers:only,unfold)", rev)
	out := map[string]string{}
	for _, line := range strings.Split(block, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, seen := out[key]; !seen {
			out[key] = strings.TrimSpace(value)
		}
	}
	return out
}

// ParentCount is how many parents a commit has: 0 for a root commit, 2 for a merge.
func (f *Fixture) ParentCount(rev string) int {
	f.t.Helper()
	fields := strings.Fields(f.MustGit("rev-list", "--parents", "-n", "1", rev))
	if len(fields) == 0 {
		f.t.Fatalf("gittest: %s resolved to no commit", rev)
	}
	return len(fields) - 1
}

// ChangedFiles lists paths differing between two revisions.
func (f *Fixture) ChangedFiles(from, to string) []string {
	f.t.Helper()
	return lines(f.MustGit("diff", "--name-only", from, to))
}

// Ref is one `for-each-ref` row.
type Ref struct {
	Name string
	SHA  string
}

// Refs lists refs matching a pattern, ordered by refname.
func (f *Fixture) Refs(pattern string) []Ref {
	f.t.Helper()
	out := f.MustGit("for-each-ref", "--sort=refname",
		"--format=%(refname)%09%(objectname)", pattern)
	var refs []Ref
	for _, line := range lines(out) {
		name, sha, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		refs = append(refs, Ref{Name: name, SHA: sha})
	}
	return refs
}

// RefNames lists the names of refs matching a pattern.
func (f *Fixture) RefNames(pattern string) []string {
	f.t.Helper()
	out := f.MustGit("for-each-ref", "--sort=refname", "--format=%(refname)", pattern)
	return lines(out)
}

// HasRef reports whether a ref exists.
func (f *Fixture) HasRef(ref string) bool {
	f.t.Helper()
	_, err := f.Git("rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// RefSHA resolves a ref, failing the test when it is missing.
func (f *Fixture) RefSHA(ref string) string {
	f.t.Helper()
	sha, err := f.Git("rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		f.t.Fatalf("gittest: ref %s does not exist\n(for-each-ref refs: %v)", ref, f.RefNames("refs/"))
	}
	return strings.TrimSpace(sha)
}

// RevList is the commit list reachable from the given revisions, newest first.
func (f *Fixture) RevList(args ...string) []string {
	f.t.Helper()
	return lines(f.MustGit(append([]string{"rev-list"}, args...)...))
}

// RevListCount counts commits reachable from the given revisions.
func (f *Fixture) RevListCount(args ...string) int {
	f.t.Helper()
	out := strings.TrimSpace(f.MustGit(append([]string{"rev-list", "--count"}, args...)...))
	var n int
	if _, err := fmt.Sscanf(out, "%d", &n); err != nil {
		f.t.Fatalf("gittest: rev-list --count %v returned %q", args, out)
	}
	return n
}

// ReachableFrom reports whether rev is an ancestor of (or equal to) head.
func (f *Fixture) ReachableFrom(rev, head string) bool {
	f.t.Helper()
	_, err := f.Git("merge-base", "--is-ancestor", rev, head)
	return err == nil
}

// MergeBase resolves the best common ancestor of two revisions.
func (f *Fixture) MergeBase(a, b string) string {
	f.t.Helper()
	return strings.TrimSpace(f.MustGit("merge-base", a, b))
}

// CommitsAbove lists commits in `base..head`, oldest first.
func (f *Fixture) CommitsAbove(base, head string) []string {
	f.t.Helper()
	return lines(f.MustGit("log", "--reverse", "--format=%H", base+".."+head))
}

// SubjectsAbove lists commit subjects in `base..head`, oldest first.
func (f *Fixture) SubjectsAbove(base, head string) []string {
	f.t.Helper()
	return lines(f.MustGit("log", "--reverse", "--format=%s", base+".."+head))
}

// FileAt returns a path's contents at a revision.
func (f *Fixture) FileAt(rev, path string) string {
	f.t.Helper()
	out, err := f.Git("show", rev+":"+path)
	if err != nil {
		f.t.Fatalf("gittest: %s:%s: %v", rev, path, err)
	}
	return out
}

// HasFile reports whether a path exists at a revision.
func (f *Fixture) HasFile(rev, path string) bool {
	f.t.Helper()
	_, err := f.Git("rev-parse", "--verify", "--quiet", rev+":"+path)
	return err == nil
}

// Clean reports whether the working tree has no staged, unstaged or untracked
// changes, ignoring gitignored paths.
func (f *Fixture) Clean() bool {
	f.t.Helper()
	return strings.TrimSpace(f.MustGit("status", "--porcelain", "--untracked-files=all")) == ""
}

// --- helpers ----------------------------------------------------------------

func lines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func mustMkdir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("gittest: mkdir %s: %v", dir, err)
	}
	return dir
}

// gitEnvVars are removed from the inherited environment so a developer's shell
// cannot steer fixture commits.
var gitEnvVars = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_CONFIG",
	"GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "GIT_AUTHOR_NAME",
	"GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE", "GIT_COMMITTER_NAME",
	"GIT_COMMITTER_EMAIL", "GIT_COMMITTER_DATE", "GIT_EDITOR",
	"GIT_EXTERNAL_DIFF", "GIT_SSH_COMMAND", "GIT_PROXY_COMMAND",
}

func sanitizedEnv() []string {
	skip := map[string]bool{}
	for _, key := range gitEnvVars {
		skip[key] = true
	}
	var out []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if skip[key] {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func mergeEnv(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	override := map[string]string{}
	var order []string
	for _, entry := range append(append([]string{}, base...), extra...) {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, seen := override[key]; !seen {
			order = append(order, key)
		}
		override[key] = value
	}
	out := make([]string, 0, len(order))
	for _, key := range order {
		out = append(out, key+"="+override[key])
	}
	return out
}
