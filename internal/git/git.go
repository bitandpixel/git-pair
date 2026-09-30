// Package git wraps the git plumbing that git-pair is built on.
//
// Only read operations and one mutating verb live here: `commit`. git-pair writes no ref
// at any point in a lifecycle (§13.4), so it deliberately has no wrapper for push, update-ref,
// symbolic-ref, merge, rebase, reset, or branch deletion — see the source-hygiene test.
package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitpair/internal/factcache"
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

	// mu guards cache, which holds the memo of pure reads. See memo.go.
	mu    sync.Mutex
	cache *state

	// factsMu guards facts, the derived-fact cache. It is apart from mu because naming the git directory
	// is itself a git call, and that call goes through the memo — which holds mu. One mutex would deadlock.
	factsMu sync.Mutex
	facts   *factcache.Store
}

// ResetMemo drops every kept answer.
//
// It is for a caller that is about to observe the repository again after something outside this package may
// have changed it. `change wait` uses it before each poll round, which is the case that matters: the whole
// point of the loop is that a review submitted in another clone ends the wait, and a memo that survived the
// sleep would never notice. A caller that changed the repository through this package needs it less — a
// write clears the memo by itself — but a caller that ran an editor, a gate script, or plain git by hand
// cannot rely on that.
func (r *Repo) ResetMemo() { r.st().memo.clear() }

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
// git-pair parses pathnames out of diff output.
func (r *Repo) Git(ctx context.Context, args ...string) (string, error) {
	return r.run(ctx, "", true, args...)
}

// GitInherit runs git attached to the current terminal, for commands whose
// output belongs to the user (diffs, difftools, editors, hooks).
func (r *Repo) GitInherit(ctx context.Context, args ...string) error {
	_, err := r.run(ctx, "", false, args...)
	return err
}

// run is the one place every git subprocess is spawned, and the one place the memo is consulted.
//
// The memo covers captured invocations only. An invocation attached to the terminal belongs to the reader
// — a diff, a difftool, an editor — and its output never comes back through here to be kept.
func (r *Repo) run(ctx context.Context, stdin string, capture bool, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !capture {
		return r.spawn(ctx, stdin, false, args...)
	}
	class := classify(args)
	key := memoKey(args, stdin)
	if e, served := r.answer(key, class); served {
		if e.failed {
			return e.stdout, &Error{ExitCode: e.code, Stderr: e.stderr, Stdout: e.stdout}
		}
		return e.stdout, nil
	}
	stdout, err := r.spawn(ctx, stdin, true, args...)
	if e, keep := kept(stdout, err); keep && r.Memoizing() {
		r.st().memo.put(key, e)
	}
	return stdout, err
}

// spawn runs git once, with no memo in the way.
func (r *Repo) spawn(ctx context.Context, stdin string, capture bool, args ...string) (string, error) {
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

// kept decides whether an invocation's answer belongs in the memo.
//
// A git subprocess that ran and exited non-zero answered the question: `rev-parse --verify --quiet` exits
// 1 to report that a path is absent from a tree, and that absence is what `CarriesDir` came for. An error
// that is not an `*Error` is different — git could not be started, the context was cancelled — and says
// something about the machine rather than the repository, so it is not kept and the next caller tries again.
func kept(stdout string, err error) (memoEntry, bool) {
	if err == nil {
		return memoEntry{stdout: stdout}, true
	}
	if e, ok := err.(*Error); ok {
		return memoEntry{stdout: e.Stdout, stderr: e.Stderr, code: e.ExitCode, failed: true}, true
	}
	return memoEntry{}, false
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
// GitDir is the repository's git directory, which is where git-pair keeps data that is
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

// IsAncestor reports whether rev is an ancestor of head — the question behind "would moving this
// ref lose history it already holds?". Exit 1 is git saying no rather than failing, so it answers
// false with no error; an unresolvable revision stays an error, because an answer built on a name
// that does not exist is worse than no answer.
func (r *Repo) IsAncestor(ctx context.Context, rev, head string) (bool, error) {
	_, err := r.Git(ctx, "merge-base", "--is-ancestor", rev, head)
	if err == nil {
		return true, nil
	}
	if ExitCode(err) == 1 {
		return false, nil
	}
	if IsUnknownRevision(err) {
		return false, fmt.Errorf("%w: cannot compare %s and %s", ErrUnknownRevision, rev, head)
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

// TreeEntry is one file in a revision's tree: where it sits and the object holding it.
type TreeEntry struct {
	Path string
	OID  string
}

// TreeEntries lists the files under dir at rev, with their object ids.
//
// The oid is the point: a caller that then wants many file contents can fetch them in one
// `cat-file --batch` instead of one `git show` per file, and can fetch the same object once
// however many revisions refer to it.
func (r *Repo) TreeEntries(ctx context.Context, rev, dir string) ([]TreeEntry, error) {
	args := []string{"ls-tree", "-r", rev}
	if dir != "" {
		args = append(args, "--", dir)
	}
	out, err := r.Git(ctx, args...)
	if err != nil {
		if IsUnknownRevision(err) {
			return nil, nil
		}
		return nil, err
	}
	var entries []TreeEntry
	for _, line := range splitLines(out) {
		// `<mode> <type> <oid>\t<path>`; a symlink or submodule line parses the same way
		// and is the caller's to reject by path, which is why the type is not filtered here.
		meta, path, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 || fields[1] != "blob" {
			continue
		}
		entries = append(entries, TreeEntry{Path: path, OID: fields[2]})
	}
	return entries, nil
}

// CatFileBlobs reads many blobs in one git process.
//
// A scan that reads a file out of every changeset directory of every branch pays one spawn
// per file through `git show`, which is B×N spawns for a scan that is logically one question.
// Feeding the ids to a single `cat-file --batch` makes it one, and asking for each id once
// means the same landed directory sitting on forty branches is read once.
//
// A missing object is simply absent from the result. A scan across revisions may legitimately
// find a path in one tree and not another, and which absences are worth complaining about is
// the caller's business, not this one's.
func (r *Repo) CatFileBlobs(ctx context.Context, oids []string) (map[string]string, error) {
	blobs := map[string]string{}
	seen := map[string]bool{}
	var want []string
	for _, oid := range oids {
		if oid == "" || seen[oid] {
			continue
		}
		seen[oid] = true
		want = append(want, oid)
	}
	if len(want) == 0 {
		return blobs, nil
	}
	out, err := r.run(ctx, strings.Join(want, "\n")+"\n", true, "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(strings.NewReader(out), 64*1024)
	for {
		header, err := br.ReadString('\n')
		if header == "" {
			if err != nil {
				break
			}
			continue
		}
		header = strings.TrimSuffix(header, "\n")
		fields := strings.Split(header, " ")
		if len(fields) == 2 && fields[1] == "missing" {
			continue
		}
		if len(fields) != 3 {
			return nil, fmt.Errorf("git cat-file --batch: unexpected output line %q", header)
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("git cat-file --batch: unreadable size in %q: %w", header, err)
		}
		content := make([]byte, size)
		if _, err := io.ReadFull(br, content); err != nil {
			return nil, fmt.Errorf("git cat-file --batch: truncated content for %s: %w", fields[0], err)
		}
		if _, err := br.ReadByte(); err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("git cat-file --batch: unreadable separator after %s: %w", fields[0], err)
		}
		blobs[fields[0]] = string(content)
	}
	return blobs, nil
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
	spec := []string{"."}
	for _, dir := range exclude {
		spec = append(spec, ":(exclude)"+filepath.ToSlash(dir)+"/")
	}
	return r.PathsChanged(ctx, from, to, spec...)
}

// PathsChanged lists the paths that differ between two revisions under the given
// pathspecs. It is the one place a diff becomes a list, so the exclusion in
// PathsChangedOutside and the single-directory comparison used to recognise a
// landed changeset cannot drift apart.
func (r *Repo) PathsChanged(ctx context.Context, from, to string, pathspec ...string) ([]string, error) {
	args := []string{"diff", "--name-only", from, to, "--"}
	args = append(args, pathspec...)
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
// Remotes lists the configured remotes, in git's order.
func (r *Repo) Remotes(ctx context.Context) ([]string, error) {
	out, err := r.Git(ctx, "remote")
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// Upstream is the remote-tracking branch a local branch tracks, e.g.
// "origin/booking", or "" when it tracks nothing.
func (r *Repo) Upstream(ctx context.Context, branch string) (string, error) {
	out, err := r.Git(ctx, "rev-parse", "--abbrev-ref", "--symbolic-full-name", branch+"@{upstream}")
	if err != nil {
		if IsUnknownRevision(err) || ExitCode(err) == 128 {
			return "", nil
		}
		return "", err
	}
	up := strings.TrimSpace(out)
	if up == "" || up == "HEAD" {
		return "", nil
	}
	return up, nil
}

// Fetch refreshes remote-tracking refs. It changes nothing in the working tree or index,
// which is what makes it safe for `change wait` to run unattended.
func (r *Repo) Fetch(ctx context.Context, remote string) error {
	args := []string{"fetch", "--quiet", "--no-tags"}
	if remote != "" {
		args = append(args, remote)
	}
	_, err := r.Git(ctx, args...)
	return err
}

func (r *Repo) ResolveRef(ctx context.Context, ref string) (string, error) {
	return r.RevParse(ctx, ref)
}

// short abbreviates a commit sha the way git's own messages do, for text a human reads.
func short(sha string) string {
	if len(sha) < 7 {
		return sha
	}
	return sha[:7]
}

// RefEntry is one line of `git for-each-ref` output.
type RefEntry struct {
	Name string
	SHA  string
}

// ListRefs maps every ref under prefixes to the ref it points at symbolically, and to ""
// when it is an ordinary ref.
//
// It answers several existence questions in one git subprocess. `rev-parse --verify` is one
// subprocess per ref, so a command that asks about a handful of refs on every run pays the
// spawn for each of them.
//
// git leaves a symbolic ref whose target does not resolve out of the listing altogether, so
// an entry here is a ref that resolves. That is the same fact a `rev-parse --verify` of the
// target proved, which is why callers no longer need to follow the pointer and check it.
func (r *Repo) ListRefs(ctx context.Context, prefixes ...string) (map[string]string, error) {
	if len(prefixes) == 0 {
		// Without a pattern git lists every ref in the repository, which is never what a
		// caller that named none meant.
		return nil, errors.New("git: ListRefs needs at least one ref prefix")
	}
	out, err := r.Git(ctx, append([]string{"for-each-ref", "--format=%(refname)%09%(symref)"}, prefixes...)...)
	if err != nil {
		return nil, err
	}
	refs := make(map[string]string)
	for _, line := range splitLines(out) {
		name, symref, ok := strings.Cut(line, "\t")
		if !ok || name == "" {
			continue
		}
		refs[name] = strings.TrimSpace(symref)
	}
	return refs, nil
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

// CommitTip is one commit as a checkpoint chooser sees it: an id to pin, and a subject
// and age to recognise it by.
type CommitTip struct {
	SHA     string
	Short   string
	Subject string
	When    time.Time
}

// RecentCommits lists commits reachable from revs, newest first, at most limit of them.
// The limit is a scrollback rather than a rule: the picker that uses it also takes a
// typed id, so a commit older than the window is still reachable.
func (r *Repo) RecentCommits(ctx context.Context, limit int, revs ...string) ([]CommitTip, error) {
	if limit <= 0 {
		limit = 100
	}
	args := []string{"log", "--no-color", "--max-count=" + strconv.Itoa(limit),
		"--pretty=%H" + FieldSep + "%h" + FieldSep + "%at" + FieldSep + "%s"}
	args = append(args, revs...)
	out, err := r.Git(ctx, args...)
	if err != nil {
		return nil, err
	}
	var tips []CommitTip
	for _, line := range splitLines(out) {
		parts := strings.SplitN(line, FieldSep, 4)
		if len(parts) < 4 {
			continue
		}
		when, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			continue
		}
		tips = append(tips, CommitTip{
			SHA: parts[0], Short: parts[1], Subject: parts[3],
			When: time.Unix(when, 0),
		})
	}
	return tips, nil
}

// FirstParentLine lists the commits on ref's first-parent line, newest first, at most limit of
// them (200 when limit is unset).
//
// The first-parent distinction is the point. A `--no-ff` merge brings a whole branch into the
// destination's ancestry, and the commit that landed the work is on the destination's own line, not among
// the commits that arrived with it. Anything that asks "which commit put this here" over ancestry would
// answer with the branch's own commits.
func (r *Repo) FirstParentLine(ctx context.Context, ref string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 200
	}
	out, err := r.Git(ctx, "rev-list", "--first-parent", "--max-count="+strconv.Itoa(limit), ref)
	if err != nil {
		return nil, err
	}
	return splitLines(out), nil
}

// RefTip is a ref, the commit it points at with tags peeled, and when that commit was
// made.
type RefTip struct {
	Name   string
	Commit string
	When   time.Time
}

// RefTips lists refs under pattern with what they point at, newest first. Unlike
// ForEachRef it peels tags and carries dates, because a reviewer choosing a branch needs
// to tell them apart; the name stays the full `refs/...` form, which is what makes a
// branch and a tag of the same name unambiguous.
func (r *Repo) RefTips(ctx context.Context, pattern string) ([]RefTip, error) {
	// Both `objectname` and `*objectname` are asked for because only the latter is peeled,
	// and it is empty for the refs that need no peeling. An annotated tag's objectname is
	// the tag object; a reviewer choosing v0.4.0 means the commit behind it. The date is
	// asked for twice for the same reason: an annotated tag has a taggerdate and no
	// committerdate, and a ref with no date in this output is still a ref worth listing.
	out, err := r.Git(ctx, "for-each-ref", "--sort=-committerdate",
		"--format=%(refname)"+FieldSep+"%(objectname)"+FieldSep+"%(*objectname)"+FieldSep+
			"%(committerdate:unix)"+FieldSep+"%(taggerdate:unix)", pattern)
	if err != nil {
		return nil, err
	}
	var tips []RefTip
	for _, line := range splitLines(out) {
		parts := strings.SplitN(line, FieldSep, 5)
		if len(parts) < 5 {
			continue
		}
		commit := parts[1]
		if parts[2] != "" {
			commit = parts[2]
		}
		tips = append(tips, RefTip{Name: parts[0], Commit: commit, When: parseUnix(parts[3], parts[4])})
	}
	return tips, nil
}

// parseUnix reads the first field that holds a date, and returns the zero time when none
// does. A missing date is a display gap, not a reason to hide a ref.
func parseUnix(fields ...string) time.Time {
	for _, f := range fields {
		if f == "" {
			continue
		}
		when, err := strconv.ParseInt(f, 10, 64)
		if err != nil {
			continue
		}
		return time.Unix(when, 0)
	}
	return time.Time{}
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
