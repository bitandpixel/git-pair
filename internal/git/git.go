// Package git wraps the git plumbing that gitpr is built on.
//
// Only read operations and two mutating verbs live here: `commit` and
// `update-ref`. gitpr deliberately has no wrapper for push, merge, rebase,
// reset, or branch deletion — see the source-hygiene test.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Errors callers branch on.
var (
	// ErrNotRepository means no git repository contains the start directory.
	ErrNotRepository = errors.New("not a git repository")
	// ErrUnknownRevision means `rev-parse --verify` could not resolve a rev.
	ErrUnknownRevision = errors.New("unknown revision")
	// ErrUnknownPath means a `<rev>:<path>` blob does not exist.
	ErrUnknownPath = errors.New("path does not exist at revision")
)

// Record separators for structured `git log --format` output. Both are C0
// controls that cannot appear in a commit subject or trailer.
const (
	FieldSep  = "\x1f"
	RecordSep = "\x1e"
)

// Repo is a handle on one git repository, addressed by its toplevel path.
type Repo struct {
	Dir string
	// Env, when non-empty, fully replaces the environment for git subprocesses.
	// Tests use it to ignore the user's global git config.
	Env []string
}

// Open resolves the repository containing dir.
func Open(dir string) (*Repo, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) == 0 {
			return nil, ErrNotRepository
		}
		return nil, ErrNotRepository
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return nil, ErrNotRepository
	}
	return &Repo{Dir: root}, nil
}

// Git runs git with the given arguments, capturing stdout and stderr.
//
// core.quotePath=false keeps non-ASCII paths literal, which matters because
// gitpr parses pathnames out of diff output.
func (r *Repo) Git(ctx context.Context, args ...string) (string, error) {
	return r.run(ctx, "", true, args...)
}

// GitInherit runs git attached to the current terminal, for commands whose
// output belongs to the user (diffs, difftools, editors, hooks).
func (r *Repo) GitInherit(ctx context.Context, args ...string) error {
	_, err := r.run(ctx, "", false, args...)
	return err
}

func (r *Repo) run(ctx context.Context, stdin string, capture bool, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	full := append([]string{"-c", "core.quotePath=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = r.Dir
	if len(r.Env) > 0 {
		cmd.Env = r.Env
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	if !capture {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			// Nothing to capture: the streams belonged to the terminal.
			return "", wrapExit(err, "", "")
		}
		return "", nil
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), wrapExit(err, stdout.String(), stderr.String())
	}
	return stdout.String(), nil
}

func wrapExit(err error, stdout, stderr string) error {
	if ee, ok := err.(*exec.ExitError); ok {
		return &Error{
			ExitCode: ee.ExitCode(),
			Stderr:   strings.TrimSpace(stderr),
			Stdout:   strings.TrimSpace(stdout),
		}
	}
	return err
}

// Error is a git subprocess failure.
type Error struct {
	ExitCode int
	Stderr   string
	// Stdout is the captured standard output of the failed command. git writes
	// some of its most useful refusals there rather than to stderr — "nothing to
	// commit" among them — so an error that only carried stderr could not explain
	// itself and could not be recognised by callers.
	Stdout string
}

func (e *Error) Error() string {
	switch {
	case e.Stderr != "":
		return fmt.Sprintf("git exited with status %d: %s", e.ExitCode, e.Stderr)
	case e.Stdout != "":
		return fmt.Sprintf("git exited with status %d: %s", e.ExitCode, e.Stdout)
	default:
		return fmt.Sprintf("git exited with status %d", e.ExitCode)
	}
}

// IsUnknownRevision reports whether err is git failing to resolve a revision
// or path, as opposed to a real git failure.
func IsUnknownRevision(err error) bool {
	var ge *Error
	if !errors.As(err, &ge) {
		return false
	}
	// `rev-parse --verify` and `show rev:path` both fail with 128 and a
	// "unknown revision"/"does not exist" message; anything else is real.
	low := strings.ToLower(ge.Stderr)
	return strings.Contains(low, "unknown revision") ||
		strings.Contains(low, "does not exist") ||
		strings.Contains(low, "needed a single revision") ||
		strings.Contains(low, "does not have any commits yet")
}

// --- repository state -------------------------------------------------------

// Head returns the full SHA of HEAD.
func (r *Repo) Head(ctx context.Context) (string, error) {
	return r.RevParse(ctx, "HEAD")
}

// RevParse resolves a revision to a full SHA.
//
// `rev-parse --verify --quiet` reports an unresolvable revision by exiting 1
// with *no stderr at all*, so the exit code is the only signal. Relying on the
// message alone would turn "ref does not exist" into a generic git failure,
// which callers like CreateRefIfAbsent must be able to distinguish.
// GitDir is the repository's git directory, which is where gitpr keeps data that is
// local to this clone and deliberately outside the working tree.
func (r *Repo) GitDir(ctx context.Context) (string, error) {
	out, err := r.Git(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", fmt.Errorf("cannot locate the git directory: %w", err)
	}
	return strings.TrimSpace(out), nil
}

func (r *Repo) RevParse(ctx context.Context, rev string) (string, error) {
	out, err := r.Git(ctx, "rev-parse", "--verify", "--quiet", rev)
	if err != nil {
		if IsUnknownRevision(err) || ExitCode(err) == 1 {
			return "", fmt.Errorf("%w: %s", ErrUnknownRevision, rev)
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ExitCode returns the git subprocess exit code carried by err, or 0.
func ExitCode(err error) int {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.ExitCode
	}
	return 0
}

// CurrentBranch returns the checked-out branch name, or "" on a detached HEAD.
func (r *Repo) CurrentBranch(ctx context.Context) (string, error) {
	out, err := r.Git(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		if ge, ok := err.(*Error); ok && ge.ExitCode == 1 {
			return "", nil // detached HEAD
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// IsClean reports whether the working tree has no staged, unstaged, or
// untracked (non-ignored) changes.
func (r *Repo) IsClean(ctx context.Context) (bool, error) {
	out, err := r.Git(ctx, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// StatusPorcelain returns the raw `git status --porcelain -uall` output.
func (r *Repo) StatusPorcelain(ctx context.Context) (string, error) {
	return r.Git(ctx, "status", "--porcelain", "--untracked-files=all")
}

// MergeBase resolves the best common ancestor of two revs.
func (r *Repo) MergeBase(ctx context.Context, a, b string) (string, error) {
	out, err := r.Git(ctx, "merge-base", a, b)
	if err != nil {
		if IsUnknownRevision(err) {
			return "", fmt.Errorf("%w: cannot find common ancestor of %s and %s", ErrUnknownRevision, a, b)
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// IsAncestor reports whether ancestor is reachable from descendant.
func (r *Repo) IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	_, err := r.Git(ctx, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	if ge, ok := err.(*Error); ok && ge.ExitCode == 1 {
		return false, nil
	}
	return false, err
}

// --- content and history ----------------------------------------------------

// ShowFile returns the contents of path at rev.
func (r *Repo) ShowFile(ctx context.Context, rev, path string) (string, error) {
	out, err := r.Git(ctx, "show", rev+":"+path)
	if err != nil {
		if IsUnknownRevision(err) {
			return "", fmt.Errorf("%w: %s:%s", ErrUnknownPath, rev, path)
		}
		return "", err
	}
	return out, nil
}

// PathExistsAt reports whether rev contains path.
func (r *Repo) PathExistsAt(ctx context.Context, rev, path string) bool {
	_, err := r.Git(ctx, "rev-parse", "--verify", "--quiet", rev+":"+path)
	return err == nil
}

// LogFields returns one record per commit in revRange, oldest first.
//
// Each field is a git log format placeholder. The final field may contain
// newlines (it normally holds a trailer block); earlier fields must not.
func (r *Repo) LogFields(ctx context.Context, revRange string, fields ...string) ([][]string, error) {
	if len(fields) == 0 {
		return nil, errors.New("git: LogFields requires at least one field")
	}
	format := strings.Join(fields, FieldSep) + RecordSep
	out, err := r.Git(ctx, "log", "--reverse", "--format="+format, revRange)
	if err != nil {
		return nil, err
	}
	return parseRecords(out), nil
}

func parseRecords(out string) [][]string {
	var records [][]string
	for _, rec := range strings.Split(out, RecordSep) {
		rec = strings.TrimPrefix(rec, "\n")
		if rec == "" {
			continue
		}
		records = append(records, strings.Split(rec, FieldSep))
	}
	return records
}

// DiffNames returns the paths changed in `git diff` over the given range.
// EmptyTree is git's empty tree, the diff target for a commit with no parent.
const EmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// PathsChangedOutside lists paths that differ between two revisions, ignoring
// anything under the given directory prefixes.
//
// The exclusion is on the *result*, which is what makes a rename out of an
// excluded directory show up: the deletion is excluded, the new path is not.
func (r *Repo) PathsChangedOutside(ctx context.Context, from, to string, exclude ...string) ([]string, error) {
	args := []string{"diff", "--name-only", from, to, "--", "."}
	for _, dir := range exclude {
		args = append(args, ":(exclude)"+filepath.ToSlash(dir)+"/")
	}
	out, err := r.Git(ctx, args...)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

func (r *Repo) DiffNames(ctx context.Context, from, to string) ([]string, error) {
	out, err := r.Git(ctx, "diff", "--name-only", from, to)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// NumstatEntry is one `git diff --numstat` row. Added/Deleted are -1 for
// binary files, which report literal dashes.
type NumstatEntry struct {
	Path    string
	Added   int
	Deleted int
	Binary  bool
}

// Numstat returns per-file change counts for a range, without rename
// detection so that a rename reads as a delete plus an add.
func (r *Repo) Numstat(ctx context.Context, from, to string) ([]NumstatEntry, error) {
	out, err := r.Git(ctx, "diff", "--numstat", "--no-renames", from, to)
	if err != nil {
		return nil, err
	}
	var entries []NumstatEntry
	for _, line := range splitLines(out) {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		e := NumstatEntry{Path: parts[2]}
		if parts[0] == "-" || parts[1] == "-" {
			e.Binary = true
			e.Added, e.Deleted = -1, -1
		} else {
			fmt.Sscanf(parts[0], "%d", &e.Added)
			fmt.Sscanf(parts[1], "%d", &e.Deleted)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// --- refs -------------------------------------------------------------------

// ResolveRef returns the SHA a ref points at, or ErrUnknownRevision.
func (r *Repo) ResolveRef(ctx context.Context, ref string) (string, error) {
	return r.RevParse(ctx, ref)
}

// UpdateRef points ref at sha.
func (r *Repo) UpdateRef(ctx context.Context, ref, sha string) error {
	_, err := r.Git(ctx, "update-ref", ref, sha)
	return err
}

// CreateRefIfAbsent points ref at sha only when ref does not already exist.
// It reports whether the ref was created by this call.
func (r *Repo) CreateRefIfAbsent(ctx context.Context, ref, sha string) (bool, error) {
	if _, err := r.ResolveRef(ctx, ref); err == nil {
		return false, nil
	} else if !errors.Is(err, ErrUnknownRevision) {
		return false, err
	}
	if err := r.UpdateRef(ctx, ref, sha); err != nil {
		return false, err
	}
	return true, nil
}

// RefEntry is one line of `git for-each-ref` output.
type RefEntry struct {
	Name string
	SHA  string
}

// ForEachRef lists refs matching pattern, ordered by refname.
func (r *Repo) ForEachRef(ctx context.Context, pattern string) ([]RefEntry, error) {
	out, err := r.Git(ctx, "for-each-ref", "--sort=-committerdate", "--format=%(refname)%09%(objectname)", pattern)
	if err != nil {
		return nil, err
	}
	var refs []RefEntry
	for _, line := range splitLines(out) {
		name, sha, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		refs = append(refs, RefEntry{Name: name, SHA: sha})
	}
	return refs, nil
}

// --- mutations --------------------------------------------------------------

// StageAll stages every tracked and untracked (non-ignored) change.
func (r *Repo) StageAll(ctx context.Context) error {
	return r.GitInheritDiscardOutput(ctx, "add", "--all")
}

// GitInheritDiscardOutput runs git with stdout suppressed but stderr attached
// to the terminal, so progress messages from git stay visible.
func (r *Repo) GitInheritDiscardOutput(ctx context.Context, args ...string) error {
	cmd := exec.Command("git", append([]string{"-c", "core.quotePath=false"}, args...)...)
	cmd.Dir = r.Dir
	if len(r.Env) > 0 {
		cmd.Env = r.Env
	}
	cmd.Stdout = nil
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return wrapExit(err, "", "")
	}
	return nil
}

// StagePaths stages the given paths, which may be directories.
func (r *Repo) StagePaths(ctx context.Context, paths ...string) error {
	return r.GitInheritDiscardOutput(ctx, append([]string{"add", "--all", "--"}, paths...)...)
}

// Commit creates a commit. An empty message list is a caller error; empty
// commits are the caller's choice via allowEmpty.
func (r *Repo) Commit(ctx context.Context, message string, allowEmpty bool, extra ...string) error {
	args := []string{"commit"}
	if allowEmpty {
		args = append(args, "--allow-empty")
	}
	args = append(args, "-m", message)
	args = append(args, extra...)
	_, err := r.Git(ctx, args...)
	return err
}

// CommitPaths creates a commit containing exactly the given paths and leaves the
// rest of the index alone.
//
// --only is what makes this safe in a dirty repository: without it, `git commit`
// would also land whatever the author had already staged for an unrelated
// commit. It also accepts freshly `git add`ed files, which `--only <path>` on
// its own would reject as untracked.
// HasStagedChanges reports whether the index differs from HEAD for the given
// paths, i.e. whether committing exactly those paths would do anything.
//
// This is the reliable form of the question. git's "nothing to commit" notice
// goes to stdout rather than stderr and is translated, so matching on that text
// is only ever a fallback.
func (r *Repo) HasStagedChanges(ctx context.Context, paths ...string) (bool, error) {
	args := []string{"diff", "--cached", "--quiet", "HEAD"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	_, err := r.Git(ctx, args...)
	switch {
	case err == nil:
		return false, nil
	case ExitCode(err) == 1: // --quiet reports differences by exit status
		return true, nil
	default:
		return false, err
	}
}

func (r *Repo) CommitPaths(ctx context.Context, message string, paths []string) error {
	args := []string{"commit", "--only", "-m", message}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	_, err := r.Git(ctx, args...)
	return err
}

// --- small helpers ----------------------------------------------------------

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
