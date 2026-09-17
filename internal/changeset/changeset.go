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
)

// Changeset is one branch's review state, located in the working tree.
type Changeset struct {
	Slug   string // filesystem name of the changeset directory
	Branch string // branch it belongs to
	// Base is the ref the changeset's diff is measured against. For stacked
	// branches this is another changeset's branch name.
	Base string
	// Dir is the changeset directory relative to the repository root.
	Dir string
	// Exists is false when the directory has not been created yet. Only
	// ForBranch can return such a value, so `change init` can report what it
	// would create.
	Exists bool
}

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

// ForBranch resolves the changeset belonging to branch. Exists is false when
// the directory is missing; Base is then empty.
func ForBranch(repo *git.Repo, branch string) (Changeset, error) {
	if branch == "" {
		return Changeset{}, ErrDetachedHead
	}
	slug, err := SlugFromBranch(branch)
	if err != nil {
		return Changeset{}, err
	}
	c := Changeset{Slug: slug, Branch: branch, Dir: filepath.Join(Root, slug)}
	abs := filepath.Join(repo.Dir, c.Dir)
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return c, nil
	}
	c.Exists = true
	md, err := readMetadata(filepath.Join(abs, MetadataFile))
	if err != nil {
		return c, err
	}
	c.Base = md["base"]
	return c, nil
}

// AtCommit resolves the changeset that branch carries at its own tip, reading
// CHANGESET.yaml out of that commit instead of the working tree.
//
// `review queue` looks at every branch in the repository from one checkout, and the
// tree that happens to be checked out says nothing about the others: the changeset
// directory of the branch being inspected is usually not on disk at all.
func AtCommit(ctx context.Context, repo *git.Repo, branch string) (Changeset, error) {
	if branch == "" {
		return Changeset{}, ErrDetachedHead
	}
	slug, err := SlugFromBranch(branch)
	if err != nil {
		return Changeset{}, err
	}
	c := Changeset{Slug: slug, Branch: branch, Dir: filepath.Join(Root, slug)}
	md, err := metadataAt(ctx, repo, branch, c.Dir)
	if err != nil {
		if errors.Is(err, git.ErrUnknownPath) {
			// No CHANGESET.yaml at this commit: this branch carries no changeset,
			// which is the common case, not a failure.
			return c, nil
		}
		return c, err
	}
	c.Exists = true
	c.Base = md["base"]
	return c, nil
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
// that have the slug and a commit but no branch to hang them on.
func BaseAt(ctx context.Context, repo *git.Repo, rev, slug string) (string, error) {
	md, err := metadataAt(ctx, repo, rev, filepath.Join(Root, slug))
	if err != nil {
		return "", err
	}
	return md["base"], nil
}

// DirsAt lists the changeset directories present in a revision's tree. Like
// AtCommit it never consults the working tree, so a changeset that was merged and
// whose branch has gone is still visible as the directory it left behind.
func DirsAt(ctx context.Context, repo *git.Repo, rev string) ([]string, error) {
	out, err := repo.Git(ctx, "ls-tree", "-d", "--name-only", rev, "--", Root+"/")
	if err != nil {
		if git.IsUnknownRevision(err) {
			return nil, nil
		}
		return nil, err
	}
	prefix := Root + "/"
	var out2 []string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "/"))
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if name := strings.TrimPrefix(line, prefix); name != "" && !strings.Contains(name, "/") {
			out2 = append(out2, name)
		}
	}
	sort.Strings(out2)
	return out2, nil
}

// Current resolves the changeset for the checked-out branch.
func Current(ctx context.Context, repo *git.Repo) (Changeset, error) {
	branch, err := repo.CurrentBranch(ctx)
	if err != nil {
		return Changeset{}, err
	}
	return ForBranch(repo, branch)
}

// RequireCurrent is Current, with ErrNoChangeset when scaffolding is missing.
func RequireCurrent(ctx context.Context, repo *git.Repo) (Changeset, error) {
	c, err := Current(ctx, repo)
	if err != nil {
		return c, err
	}
	if !c.Exists {
		return c, fmt.Errorf("%w: %s (run `git pair change init --base <ref>`)", ErrNoChangeset, c.Dir)
	}
	return c, nil
}

// BranchesForSlug returns local branches whose slug matches the changeset name.
func BranchesForSlug(ctx context.Context, repo *git.Repo, slug string) ([]string, error) {
	out, err := repo.Git(ctx, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, err
	}
	var matches []string
	for _, b := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if b == "" {
			continue
		}
		if s, err := SlugFromBranch(b); err == nil && s == slug {
			matches = append(matches, b)
		}
	}
	return matches, nil
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

// parseMetadata is the format, readMetadata and AtCommit are the places it comes
// from: the working tree and a commit respectively.
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
	// Base is the ref the changeset diff is measured against. Required.
	Base string
	// SetBase replaces an existing base value instead of reporting a conflict.
	SetBase bool
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
	if opts.Base == "" {
		return nil, errors.New("changeset: base must not be empty")
	}
	base := opts.Base
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
	if existing, ok := md["base"]; ok && existing != "" && existing != base && !opts.SetBase {
		return written, fmt.Errorf("%w: %s names base %q, not %q (pass --set-base to change it)",
			ErrBaseConflict, c.MetadataPath(), existing, base)
	}
	_, mdStatErr := os.Stat(mdPath)
	switch {
	case errors.Is(mdStatErr, os.ErrNotExist):
		if err := os.WriteFile(mdPath, []byte("base: "+base+"\n"), 0o644); err != nil {
			return written, err
		}
		written = append(written, c.MetadataPath())
	case mdStatErr == nil && md["base"] != base:
		if err := os.WriteFile(mdPath, []byte("base: "+base+"\n"), 0o644); err != nil {
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
// normal first run of `change init` on a new branch.
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

// AboutTemplate is the starting ABOUT.md written by `change init`. Headings
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
