// Package changeset owns review identity: the branch-to-directory rule,
// CHANGESET.yaml, ABOUT.md, and review threads.
package changeset

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gitpair/internal/git"
)

// Root is the repository directory holding every changeset directory.
const Root = "changesets"

// Standard file names inside a changeset directory.
const (
	MetadataFile = "CHANGESET.yaml"
	AboutFile    = "ABOUT.md"
)

// Errors callers branch on.
var (
	// ErrNoChangeset means the current branch has no changeset directory.
	ErrNoChangeset = errors.New("no changeset for this branch")
	// ErrDetachedHead means git-pair was run where no branch is checked out.
	ErrDetachedHead = errors.New("HEAD is detached; check out a branch first")
	// ErrBaseConflict means CHANGESET.yaml already names a different base.
	ErrBaseConflict = errors.New("base already set to a different value")
	// ErrAboutConflict means ABOUT.md already has content and no override was given.
	ErrAboutConflict = errors.New("ABOUT.md already has content")
	// ErrBaseIsOwnBranch means the base is the very branch the changeset lives on.
	ErrBaseIsOwnBranch = errors.New("self-referential changeset base")
	// ErrParentWithBase means a CHANGESET.yaml names both a `base:` and a `parent:`, which
	// is one fact spelled twice.
	ErrParentWithBase = errors.New("changeset names both base and parent")
	// ErrIDMismatch means a CHANGESET.yaml records an `id` that is not the name of the
	// directory holding it.
	ErrIDMismatch = errors.New("changeset id does not match its directory")
)

// ID is the changeset's canonical identity: the name of its directory, which is what
// `changesets/<id>/` and the durable refs are named after. The field carrying it is
// called Slug for historical reasons; it is the id, and the branch name is only where
// the default came from (PRD §4).

// Changeset is one branch's review state, located in the working tree.
type Changeset struct {
	// Slug is the changeset ID: the filesystem name of its directory. It is the
	// identity refs and JSON output name, and it is not derived from the branch except
	// as the default `init` suggests.
	Slug string
	// Branch is the branch the caller was asking about. Resolution does not determine
	// it — the rule reads trees, not checkouts — so callers fill it in from what they
	// know, and an empty value means no branch is involved.
	Branch string
	// Base is the ref the changeset's diff is measured against. For stacked
	// branches this is another changeset's branch name — whichever key recorded it.
	Base string
	// ParentBranch is the branch named by `parent:`, empty for a changeset measured straight
	// against the integration branch. It stays the branch name even when the measurement base has
	// been redirected to the parent's integration ref, so the stack and the diff base can be
	// reported separately (§21).
	ParentBranch string
	// ParentChangeset is the changeset this one is stacked on, from `parent-changeset:`. It is
	// empty for a changeset measured against the integration branch, and empty for a stack whose
	// parent changeset was not known when the stack was recorded.
	ParentChangeset string
	// Dir is the changeset directory relative to the repository root.
	Dir string
	// Exists is false when the directory has not been created yet. Only Current can
	// return such a value, so `init` can report what it would create.
	Exists bool
}

// ID is the changeset ID, spelled out because callers report and compare it as the
// changeset's name rather than as a directory.
func (c Changeset) ID() string { return c.Slug }

// Path joins parts onto the changeset directory.
func (c Changeset) Path(parts ...string) string {
	return filepath.Join(append([]string{c.Dir}, parts...)...)
}

// AboutPath is the changeset's ABOUT.md, relative to the repository root.
func (c Changeset) AboutPath() string { return c.Path(AboutFile) }

// MetadataPath is the changeset's CHANGESET.yaml, relative to the repository root.
func (c Changeset) MetadataPath() string { return c.Path(MetadataFile) }

// SlugFromBranch maps a branch name to a deterministic, filesystem-safe
// changeset directory name: `/` becomes `-`, every other character outside
// [A-Za-z0-9._-] becomes `-`, runs of `-` collapse, and leading/trailing `-`
// are trimmed. Case is preserved.
//
//	feature/booking-transaction -> feature-booking-transaction
//	feat/JIRA-123 Foo Bar       -> feat-JIRA-123-Foo-Bar
func SlugFromBranch(branch string) (string, error) {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.TrimSpace(branch) {
		if isSlugChar(r) {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteRune('-')
			prevDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "", fmt.Errorf("branch %q does not produce a usable changeset name", branch)
	}
	return slug, nil
}

func isSlugChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '.', r == '_', r == '-':
		return true
	}
	return false
}

// ValidateID checks an explicit changeset ID against the character rule the derived
// default obeys, so an `--id` is either accepted exactly as typed or refused. Silently
// normalising `booking v2` into `booking-v2` would mean the caller's identity and the
// one on disk are different things, and the refs would be named after neither.
func ValidateID(id string) error {
	if id == "" {
		return errors.New("changeset id must not be empty")
	}
	if id == "." || id == ".." {
		return fmt.Errorf("changeset id %q is not a usable directory name", id)
	}
	if Reserved(id) {
		return fmt.Errorf("changeset id %q is reserved: %s/ holds the directories of changesets that have landed, so no changeset may take that name",
			id, filepath.Join(Root, LandedDir))
	}
	if strings.ContainsAny(id, "/\\") {
		return fmt.Errorf("changeset id %q must not contain a path separator", id)
	}
	if strings.TrimSpace(id) != id {
		return fmt.Errorf("changeset id %q must not have leading or trailing whitespace", id)
	}
	for _, r := range id {
		if !isSlugChar(r) {
			return fmt.Errorf("changeset id %q contains %q: ids use letters, digits, dot, underscore and hyphen", id, string(r))
		}
	}
	if strings.HasPrefix(id, "-") || strings.HasSuffix(id, "-") || strings.Contains(id, "--") {
		return fmt.Errorf("changeset id %q must not start, end or double up on hyphens", id)
	}
	return nil
}

// ForID names the changeset directory an id would use, without consulting the tree.
// `init` needs the value to check for collisions before it writes anything.
func ForID(id string) (Changeset, error) {
	if err := ValidateID(id); err != nil {
		return Changeset{}, err
	}
	return Changeset{Slug: id, Dir: filepath.Join(Root, id)}, nil
}

// DirectoryState says where a changeset directory is present: on disk, in HEAD's tree, or
// both. `init` needs the two halves separately. A directory on disk is this changeset
// being re-initialised, which is idempotent. A directory in HEAD but not on disk is a deletion
// that has not been committed, and a name whose history is still live on this line of
// development is not free — retiring a changeset is a commit, not an `rm`.
//
// The committed half is also what stops a merged changeset's leftover directory on the
// integration branch from being adopted by a new changeset of the same name.
//
// The window neither half closes is two branches that each created the same new id without
// either being readied: nothing is visible from one checkout, and the refs check below closes
// it the moment either is. A full branch scan per `init` prices a rare mistake against
// a common command.
type DirectoryState struct {
	Worktree  bool
	Committed bool
}

// DirectoryAt reports where the directory for id is present.
func DirectoryAt(ctx context.Context, repo *git.Repo, id string) (DirectoryState, error) {
	var st DirectoryState
	if err := ValidateID(id); err != nil {
		return st, err
	}
	if _, err := os.Stat(filepath.Join(repo.Dir, Root, id)); err == nil {
		st.Worktree = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return st, err
	}
	dirs, err := ActiveIDs(ctx, repo, "HEAD")
	if err != nil {
		return st, err
	}
	for _, d := range dirs {
		if d == id {
			st.Committed = true
			break
		}
	}
	return st, nil
}

// worktreeDirs lists the changeset directories present on disk.
func worktreeDirs(repo *git.Repo) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(repo.Dir, Root))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !Reserved(e.Name()) {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

// changesetDir reports the changeset name a tree path belongs to, and whether it is the
// metadata file that carries the claim.
func changesetDir(path string) (string, bool) {
	dir, name := filepath.Split(path)
	if name != MetadataFile {
		return "", false
	}
	rest := strings.TrimSuffix(dir, "/")
	prefix := Root + "/"
	if !strings.HasPrefix(rest, prefix) {
		return "", false
	}
	id := strings.TrimPrefix(rest, prefix)
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	// A `CHANGESET.yaml` written directly under the landed namespace is not a changeset claiming
	// to be called `.landed`, so this reader refuses it rather than naming it. Same trap, third
	// reader; see the comment on LandedDir.
	if Reserved(id) {
		return "", false
	}
	return id, true
}

// localBranches lists local branches in for-each-ref order.
func localBranches(ctx context.Context, repo *git.Repo) ([]string, error) {
	out, err := repo.Git(ctx, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, err
	}
	var branches []string
	for _, b := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if b != "" {
			branches = append(branches, b)
		}
	}
	return branches, nil
}

// metadataAt reads CHANGESET.yaml for a directory out of a revision.
func metadataAt(ctx context.Context, repo *git.Repo, rev, dir string) (map[string]string, error) {
	data, err := repo.ShowFile(ctx, rev, filepath.ToSlash(filepath.Join(dir, MetadataFile)))
	if err != nil {
		return nil, err
	}
	return parseMetadata(data)
}

// BaseAt reads the base a changeset directory recorded at a revision, for callers
// that have the slug and a commit but no branch to hang them on. A stacked changeset
// answers with its parent branch, which is its base.
func BaseAt(ctx context.Context, repo *git.Repo, rev, slug string) (string, error) {
	stack, err := StackAt(ctx, repo, rev, slug)
	if err != nil {
		return "", err
	}
	return stack.Base, nil
}

// Parent is the branch a changeset is stacked on and where that branch stands right now. A zero
// value means the changeset is not stacked.
type Parent struct {
	// Branch is the parent branch's short name, as `parent:` recorded it.
	Branch string
	// Tip is the parent branch's current commit, empty when the branch is gone or has not been
	// pushed yet. It is what a review submission records as `Review-Parent-Head`.
	Tip string
}

// ParentOf names the branch a changeset is stacked on.
//
// Three things make a changeset unstacked, and all three answer with the zero Parent: measured against
// the integration branch (ordinary drift covers the integration branch moving), measured against a ref
// that is not a branch, and measured against its own branch. A parent whose branch is missing is
// *still* a stack, and answers with the branch and no tip — that is the case a child has to be
// reconciled over, not the case where there is nothing to reconcile.
func ParentOf(ctx context.Context, repo *git.Repo, c Changeset, db DefaultBranchRef) (Parent, error) {
	if c.Base == "" {
		return Parent{}, nil
	}
	// The branch is the stack. `Base` can name the parent's integration ref instead of its branch,
	// because a landed parent is still measured against — see relinkStacks — and the branch name is
	// where the two cases are told apart.
	if c.ParentBranch != "" {
		return parentOfBranch(ctx, repo, c.ParentBranch, c.Branch, db)
	}
	if db.IsBranch(c.Base) {
		return Parent{}, nil
	}
	name := c.Base
	if !strings.HasPrefix(name, "refs/heads/") {
		name = "refs/heads/" + name
	}
	if c.Branch != "" && name == "refs/heads/"+c.Branch {
		return Parent{}, nil
	}
	sha, err := repo.RevParse(ctx, name)
	if errors.Is(err, git.ErrUnknownRevision) {
		return Parent{Branch: strings.TrimPrefix(name, "refs/heads/")}, nil
	}
	if err != nil {
		return Parent{}, err
	}
	return Parent{Branch: strings.TrimPrefix(name, "refs/heads/"), Tip: sha}, nil
}

func parentOfBranch(ctx context.Context, repo *git.Repo, branch, own string, db DefaultBranchRef) (Parent, error) {
	if db.IsBranch(branch) {
		return Parent{}, nil
	}
	if own != "" && branch == own {
		return Parent{}, nil
	}
	sha, err := repo.RevParse(ctx, "refs/heads/"+branch)
	if errors.Is(err, git.ErrUnknownRevision) {
		return Parent{Branch: branch}, nil
	}
	if err != nil {
		return Parent{}, err
	}
	return Parent{Branch: branch, Tip: sha}, nil
}

// StackAt reads the stack relationship a changeset directory recorded at a revision, under either
// spelling of the directory.
func StackAt(ctx context.Context, repo *git.Repo, rev, slug string) (Stack, error) {
	dir, ok := DirAt(ctx, repo, rev, slug)
	if !ok {
		// Ask for the active spelling anyway, so the error a caller already handles is the one it gets:
		// `git.ErrUnknownPath` naming the path a reader would look for first.
		dir = ActiveDirPath(slug)
	}
	md, err := metadataAt(ctx, repo, rev, dir)
	if err != nil {
		return Stack{}, err
	}
	return stackOf(md)
}

// ActiveIDs lists the changeset directories present in a revision's tree: the immediate children of
// `changesets/`, minus the landed namespace. It reads the tree and never the working tree, so a
// changeset that was merged and whose branch has gone is still visible as the directory it left
// behind.
//
// The exclusion is not cosmetic, and `Reserved` names the trap it avoids: a listing that returned
// `.landed` would hand every caller a changeset id with no `CHANGESET.yaml`, and each of them would
// report the branch as broken instead of reporting the namespace.
//
// Ask for this when the question is "what work does this revision carry". Ask for LandedIDs when the
// question is "has this reached the destination", because a tidied directory is landed and is not
// listed here.
func ActiveIDs(ctx context.Context, repo *git.Repo, rev string) ([]string, error) {
	dirs, err := dirsUnder(ctx, repo, rev, Root)
	if err != nil {
		return nil, err
	}
	return withoutReserved(dirs), nil
}

// LandedIDs lists the changesets a destination branch has: every directory it carries under
// `changesets/`, plus every directory it carries under `changesets/.landed/`.
//
// It is one fact with two spellings. A landed changeset's directory arrives on the destination as
// `changesets/<id>/` and stays there until somebody tidies it to `changesets/.landed/<id>/`. Either
// way the work landed, and a reader that asks only one of the two questions reports a landed
// changeset as work in progress — the directory is simply not where it looked. Both halves come from
// the destination's own tree, so there is nothing to keep in sync and nothing to fetch.
func LandedIDs(ctx context.Context, repo *git.Repo, trunkRef string) ([]string, error) {
	active, err := ActiveIDs(ctx, repo, trunkRef)
	if err != nil {
		return nil, err
	}
	moved, err := dirsUnder(ctx, repo, trunkRef, ActiveDirPath(LandedDir))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(active)+len(moved))
	for _, id := range append(active, moved...) {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// dirsUnder lists the immediate child directories of one path in a revision's tree, sorted, and
// tolerates a revision or a path that is not there by returning nothing.
func dirsUnder(ctx context.Context, repo *git.Repo, rev, dir string) ([]string, error) {
	out, err := repo.Git(ctx, "ls-tree", "-d", "--name-only", rev, "--", dir+"/")
	if err != nil {
		if git.IsUnknownRevision(err) {
			return nil, nil
		}
		return nil, err
	}
	prefix := dir + "/"
	var names []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "/"))
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if name := strings.TrimPrefix(line, prefix); name != "" && !strings.Contains(name, "/") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// Current resolves the changeset for the checked-out branch.
//
// It reports an ambiguity as an error rather than picking, because the callers of this
// function act on the answer: `status`, `review ready` and `change abandon` would each read
// the diff base of whichever candidate happened to sort first.
func Current(ctx context.Context, repo *git.Repo, defaultBranch string) (Changeset, error) {
	db, err := DefaultBranch(ctx, repo, defaultBranch)
	if err != nil {
		return Changeset{}, err
	}
	return currentOn(ctx, repo, db)
}

// currentOn answers for the checked-out revision, given an integration branch somebody already
// resolved. `status` takes that route so the branch it compared against is still in hand afterwards.
func currentOn(ctx context.Context, repo *git.Repo, db DefaultBranchRef) (Changeset, error) {
	res, err := ResolveCurrentOn(ctx, repo, db)
	if err != nil {
		return Changeset{}, err
	}
	branch, err := repo.CurrentBranch(ctx)
	if err != nil {
		return Changeset{}, err
	}
	if branch == "" {
		// `git rev-parse --abbrev-ref HEAD` answers "HEAD" on a detached checkout, which the
		// git helper reports as no branch. The rule itself works detached — trees do not need
		// a branch — but the value callers print does, so this stays a refusal.
		return Changeset{}, ErrDetachedHead
	}
	if res.Ambiguous {
		return Changeset{}, AmbiguityError(res)
	}
	if res.Selected == nil {
		// Nothing is in progress here. The value names the directory `init` would
		// create from this branch's name, which is what `status` reports and what
		// ErrNoChangeset's hint points at.
		fallback, err := SlugFromBranch(branch)
		if err != nil {
			return Changeset{}, err
		}
		return Changeset{Slug: fallback, Branch: branch, Dir: filepath.Join(Root, fallback)}, nil
	}
	cs := res.Selected.Changeset
	cs.Branch = branch
	return cs, nil
}

// RequireCurrent is Current, with ErrNoChangeset when scaffolding is missing.
func RequireCurrent(ctx context.Context, repo *git.Repo, defaultBranch string) (Changeset, error) {
	db, err := DefaultBranch(ctx, repo, defaultBranch)
	if err != nil {
		return Changeset{}, err
	}
	return RequireCurrentOn(ctx, repo, db)
}

// RequireCurrentOn is RequireCurrent for a caller that resolved the integration branch itself, so
// it can report what "landed" was measured against without asking twice.
func RequireCurrentOn(ctx context.Context, repo *git.Repo, db DefaultBranchRef) (Changeset, error) {
	c, err := currentOn(ctx, repo, db)
	if err != nil {
		return c, err
	}
	if !c.Exists {
		return c, noChangesetHere(ctx, repo, db, c)
	}
	return c, nil
}

// noChangesetHere says what a reader standing on a branch with no work in progress can actually do, and
// the answer turns on one fact: whether the integration branch already holds this directory.
//
// `init` is the right hint only when nothing happened to this branch's changeset yet. When the
// destination already carries the directory, the changeset landed and this branch is the copy that has
// not been tidied away. Telling that reader to run `init` starts a second changeset over work with a
// record, and `status` on a merged branch is the ordinary way to arrive here: the branch is where you
// were standing when the merge happened.
func noChangesetHere(ctx context.Context, repo *git.Repo, db DefaultBranchRef, c Changeset) error {
	if db.Ref != "" && repo.PathExistsAt(ctx, db.Ref, c.Dir) {
		id := filepath.Base(c.Dir)
		return fmt.Errorf("%w: %s is not work in progress, and the integration branch already holds it — that "+
			"changeset landed. Read it with `git pair status --changeset %s`", ErrNoChangeset, c.Dir, id)
	}
	return fmt.Errorf("%w: %s (run `git pair init --base <ref>`)", ErrNoChangeset, c.Dir)
}

// The two stack keys. `parent:` names the branch this changeset is stacked on and *is* its base:
// a file that sets both `parent:` and `base:` is refused the way an inconsistent `id:` is, because
// the two spellings are two answers to "what does this diff against?" and they will not stay in
// agreement. `parent-changeset:` records the changeset living on that branch — the durable half of
// the relationship, which is what still means something after the parent branch is deleted and its
// work has become an integration ref.
const (
	ParentKey          = "parent"
	ParentChangesetKey = "parent-changeset"
)

// Stack is what a CHANGESET.yaml says about where a changeset sits: the branch it is measured
// against, and — when it is stacked — the parent branch and the parent's changeset.
type Stack struct {
	// Base is the ref the changeset's diff is measured against. It is `parent:` when there is
	// one, and `base:` otherwise, which is why everything that measures a changeset reads this
	// field rather than either key.
	Base string
	// Parent is the branch named by `parent:`, empty for a changeset measured straight against
	// the integration branch.
	Parent string
	// ParentChangeset is the changeset recorded on the parent branch, empty when nobody could
	// say which one it was at the time the stack was recorded.
	ParentChangeset string
}

// stackOf reads the stack out of already-parsed metadata. It is the one place that decides which
// key wins, so the tree reader, the working-tree reader and the writer cannot disagree about what
// a file means.
func stackOf(md map[string]string) (Stack, error) {
	base, parent := md["base"], md[ParentKey]
	if base != "" && parent != "" {
		return Stack{}, fmt.Errorf("%w: %s: %q and %s: %q; `parent:` is the base, so keep one of them",
			ErrParentWithBase, ParentKey, parent, "base", base)
	}
	if parent != "" {
		return Stack{Base: parent, Parent: parent, ParentChangeset: strings.TrimSpace(md[ParentChangesetKey])}, nil
	}
	return Stack{Base: base}, nil
}

// IgnoresKey is the CHANGESET.yaml key naming the other changesets this one is merely sharing a
// branch with, recorded by `git pair change use`.
const IgnoresKey = "ignores"

// SetIgnores records in a changeset's own CHANGESET.yaml which other changesets it is merely
// sharing a branch with. The record lives in the chosen changeset's file and nowhere else: to
// clear the losing candidates' records instead would write into another changeset's directory,
// which would then be read as part of this changeset's landing.
//
// Comments, unknown keys and their order survive; the file is rewritten only when the value
// actually changes, so running the command twice records one commit rather than two.
func SetIgnores(repo *git.Repo, c Changeset, ids []string) (changed bool, err error) {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	path := filepath.Join(repo.Dir, c.Dir, MetadataFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	want := ""
	if len(sorted) > 0 {
		want = IgnoresKey + ": " + strings.Join(sorted, " ")
	}
	var out []string
	placed := false
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		key, _, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), IgnoresKey) {
			if want != "" && !placed {
				out = append(out, want)
				placed = true
			}
			continue
		}
		out = append(out, line)
	}
	if want != "" && !placed {
		out = append(out, want)
	}

	rewritten := strings.Join(out, "\n") + "\n"
	if rewritten == string(data) {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(rewritten), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// --- metadata ---------------------------------------------------------------

// readMetadata parses the deliberately tiny CHANGESET.yaml format: one
// `key: value` pair per line, `#` comments allowed. A full YAML parser would
// be a dependency this file does not earn.
func readMetadata(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return parseMetadata(string(data))
}

// parseMetadata is the format; readMetadata (the working tree) and the resolver's batched blob
// read (a revision) are the two places it comes from.
func parseMetadata(data string) (map[string]string, error) {
	md := map[string]string{}
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" {
			md[key] = value
		}
	}
	return md, nil
}

// WriteOptions selects what Write should create or replace.
type WriteOptions struct {
	// Base is the ref the changeset diff is measured against. Required unless Parent is set.
	Base string
	// SetBase replaces an existing base value instead of reporting a conflict.
	SetBase bool
	// Parent names the branch this changeset is stacked on. It *is* the base (PRD §21), so it is
	// written as `parent:` and no `base:` is written; passing Base and Parent together is refused.
	Parent string
	// ParentChangeset records which changeset lives on the parent branch. Written only with Parent.
	ParentChangeset string
	// SetParent replaces an existing stack — a different parent, or a parent replacing a plain
	// base — instead of reporting a conflict.
	SetParent bool
	// About is explicit ABOUT.md content. Empty means "scaffold it".
	About string
	// SetAbout replaces existing ABOUT.md content instead of reporting a conflict.
	SetAbout bool
}

// Write creates the changeset directory, CHANGESET.yaml, and ABOUT.md.
//
// It is idempotent and never overwrites content silently: an existing base or
// ABOUT.md that differs from what was asked for is a conflict, not a rewrite.
//
// Nothing here commits. The caller decides, because committing must go through
// `git commit --only` on the changeset directory so an unrelated staged file in
// the author's index is not swept into the scaffolding commit.
func Write(repo *git.Repo, c Changeset, opts WriteOptions) (written []string, err error) {
	if c.Dir == "" {
		return nil, errors.New("changeset: empty directory")
	}
	if opts.Parent != "" && opts.Base != "" {
		return nil, fmt.Errorf("%w: %s and %s; `parent:` is the base, so name one of them",
			ErrParentWithBase, opts.Parent, opts.Base)
	}
	base := opts.Base
	if opts.Parent != "" {
		base = opts.Parent
	}
	if base == "" {
		return nil, errors.New("changeset: base must not be empty")
	}
	id := filepath.Base(filepath.Clean(c.Dir))
	if err := ValidateID(id); err != nil {
		return nil, err
	}
	absDir := filepath.Join(repo.Dir, c.Dir)
	if _, err := os.Stat(absDir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(absDir, 0o755); err != nil {
			return nil, err
		}
		written = append(written, c.Dir+string(filepath.Separator))
	}

	mdPath := filepath.Join(absDir, MetadataFile)
	md, err := readMetadata(mdPath)
	if err != nil {
		return written, err
	}
	if existing := md["id"]; existing != "" && existing != id {
		return written, fmt.Errorf("%w: %s records id %q but sits in %q; the directory name is the id, so rename the directory or correct the file",
			ErrIDMismatch, c.MetadataPath(), existing, id)
	}
	existing, err := stackOf(md)
	if err != nil {
		return written, fmt.Errorf("%s: %w", c.MetadataPath(), err)
	}
	if existing.Base != "" && existing.Base != base && !opts.SetBase && !opts.SetParent {
		flag := "--set-base"
		if opts.Parent != "" {
			flag = "--set-parent"
		}
		return written, fmt.Errorf("%w: %s is measured against %q, not %q (pass %s to change it)",
			ErrBaseConflict, c.MetadataPath(), existing.Base, base, flag)
	}
	want := renderMetadata(md, id, opts)
	_, mdStatErr := os.Stat(mdPath)
	current, _ := os.ReadFile(mdPath)
	switch {
	case errors.Is(mdStatErr, os.ErrNotExist):
		if err := os.WriteFile(mdPath, []byte(want), 0o644); err != nil {
			return written, err
		}
		written = append(written, c.MetadataPath())
	case mdStatErr == nil && string(current) != want:
		if err := os.WriteFile(mdPath, []byte(want), 0o644); err != nil {
			return written, err
		}
		written = append(written, c.MetadataPath()+" (base updated)")
	case mdStatErr != nil:
		return written, mdStatErr
	}

	aboutPath := filepath.Join(absDir, AboutFile)
	_, aboutStatErr := os.Stat(aboutPath)
	aboutExists := aboutStatErr == nil
	switch {
	case opts.About != "" && aboutExists && !opts.SetAbout:
		return written, fmt.Errorf("%w: %s is not empty (pass --set-about to replace it)",
			ErrAboutConflict, c.AboutPath())
	case opts.About != "":
		body := normalizeMarkdown(opts.About)
		if err := os.WriteFile(aboutPath, []byte(body), 0o644); err != nil {
			return written, err
		}
		if aboutExists {
			written = append(written, c.AboutPath()+" (content replaced)")
		} else {
			written = append(written, c.AboutPath())
		}
	case errors.Is(aboutStatErr, os.ErrNotExist):
		if err := os.WriteFile(aboutPath, []byte(AboutTemplate(c.Slug)), 0o644); err != nil {
			return written, err
		}
		written = append(written, c.AboutPath())
	case aboutStatErr != nil:
		return written, aboutStatErr
	}
	return written, nil
}

// renderMetadata writes CHANGESET.yaml: the keys git-pair owns, in the order that reads best, then
// any key it does not recognise.
//
// The pass-through is not decoration. `change use` records `ignores:` in this same file, and an
// author's hand-edited key is a decision; a command that rewrites the base must not quietly delete
// either one. Keys are sorted because a rewrite that reordered them would show up in a diff as a
// change nobody made.
func renderMetadata(md map[string]string, id string, opts WriteOptions) string {
	out := "id: " + id + "\n"
	if opts.Parent != "" {
		out += ParentKey + ": " + opts.Parent + "\n"
		if opts.ParentChangeset != "" {
			out += ParentChangesetKey + ": " + opts.ParentChangeset + "\n"
		}
	} else {
		out += "base: " + opts.Base + "\n"
	}
	rest := make([]string, 0, len(md))
	for k := range md {
		switch k {
		case "id", "base", ParentKey, ParentChangesetKey:
		default:
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		out += k + ": " + md[k] + "\n"
	}
	return out
}

// normalizeMarkdown makes piped or flag-supplied content end in exactly one
// newline, so a here-string and a here-document produce identical files.
func normalizeMarkdown(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if s == "" {
		return ""
	}
	return s + "\n"
}

// BaseIsOwnBranch reports whether a base ref names the branch it is stacked on.
//
// This is the one base configuration that cannot work: the base moves with every
// commit the author makes, so `base...HEAD` is empty forever, every lifecycle
// marker lands below the range that reads it, and `change ready` reports success
// for a changeset that can never be observed as ready.
//
// The check is by ref identity, not by commit. A branch created moments ago
// shares its tip with its base legitimately, so "same SHA" would refuse the
// normal first run of `init` on a new branch.
func BaseIsOwnBranch(ctx context.Context, repo *git.Repo, base, branch string) (bool, error) {
	if base == "" || branch == "" {
		return false, nil
	}
	// --symbolic-full-name answers "which ref is this?", which is exactly the
	// question: "main" and "HEAD" on main both answer refs/heads/main, while a
	// raw SHA answers nothing and a dead ref answers with an error.
	name, err := repo.Git(ctx, "rev-parse", "--symbolic-full-name", base)
	if err != nil {
		return false, nil
	}
	name = strings.TrimSpace(name)
	return name != "" && name == "refs/heads/"+branch, nil
}

// AboutExists reports whether ABOUT.md is present in the working tree.
func (c Changeset) AboutExists(repo *git.Repo) bool {
	info, err := os.Stat(filepath.Join(repo.Dir, c.AboutPath()))
	return err == nil && !info.IsDir()
}

// AboutTemplate is the starting ABOUT.md written by `init`. Headings
// follow PRD §6 so an agent has somewhere to put each kind of context.
func AboutTemplate(slug string) string {
	return "# " + slug + "\n\n" +
		"## Summary\n\n" +
		"## What changed\n\n" +
		"## Design decisions\n\n" +
		"## Validation\n\n" +
		"## Known limitations\n\n" +
		"## Open questions\n"
}

// WriteIfAbsent creates path with content when it does not already exist, and
// reports whether it was created. Used for ABOUT.md and thread files so opening
// a document never damages an existing one.
func WriteIfAbsent(repo *git.Repo, relPath, content string) (bool, error) {
	abs := filepath.Join(repo.Dir, relPath)
	if _, err := os.Stat(abs); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// EnsureAbout creates ABOUT.md from the scaffold if it is missing and reports
// whether it was created.
func (c Changeset) EnsureAbout(repo *git.Repo) (bool, error) {
	return WriteIfAbsent(repo, c.AboutPath(), AboutTemplate(c.Slug))
}

// EnsureThread resolves a thread title to a path, creating the file when needed.
// It reports whether the file was created, so callers can say "created" versus
// "opened existing" instead of silently duplicating a discussion.
func (c Changeset) EnsureThread(repo *git.Repo, title string) (path string, created bool, err error) {
	path, err = c.ThreadPath(title)
	if err != nil {
		return "", false, err
	}
	created, err = WriteIfAbsent(repo, path, ThreadTemplate(title))
	if err != nil {
		return path, false, err
	}
	return path, created, nil
}

// --- threads ----------------------------------------------------------------

// ThreadSlug maps a thread title to a filename stem, lowercased for the same
// readability reason filenames are normally lowercase.
func ThreadSlug(title string) (string, error) {
	slug, err := SlugFromBranch(title)
	if err != nil {
		return "", fmt.Errorf("thread title %q does not produce a usable file name", title)
	}
	return strings.ToLower(slug), nil
}

// ThreadPath returns the changeset-relative path for a thread title. It does
// not create anything.
func (c Changeset) ThreadPath(title string) (string, error) {
	stem, err := ThreadSlug(title)
	if err != nil {
		return "", err
	}
	return c.Path(stem + ".md"), nil
}

// Threads lists existing thread files in the changeset directory: every `.md`
// file except ABOUT.md.
func (c Changeset) Threads(repo *git.Repo) ([]string, error) {
	abs := filepath.Join(repo.Dir, c.Dir)
	entries, err := os.ReadDir(abs)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" || e.Name() == AboutFile {
			continue
		}
		out = append(out, c.Path(e.Name()))
	}
	sort.Strings(out)
	return out, nil
}

// ThreadTemplate seeds a new thread file.
func ThreadTemplate(title string) string {
	return "# " + strings.TrimSpace(title) + "\n\n"
}
