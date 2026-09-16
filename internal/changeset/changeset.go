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

	"gitpr/internal/git"
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
	// ErrDetachedHead means gitpr was run where no branch is checked out.
	ErrDetachedHead = errors.New("HEAD is detached; check out a branch first")
	// ErrBaseConflict means CHANGESET.yaml already names a different base.
	ErrBaseConflict = errors.New("base already set to a different value")
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
		return c, fmt.Errorf("%w: %s (run `gitpr change init --base <ref>`)", ErrNoChangeset, c.Dir)
	}
	return c, nil
}

// List returns every changeset directory present in the working tree, sorted.
func List(repo *git.Repo) ([]Changeset, error) {
	entries, err := os.ReadDir(filepath.Join(repo.Dir, Root))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Changeset
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		mdPath := filepath.Join(repo.Dir, Root, e.Name(), MetadataFile)
		md, err := readMetadata(mdPath)
		if err != nil {
			// A directory without readable metadata is not a changeset we
			// can reason about; skip it rather than fail the whole listing.
			continue
		}
		out = append(out, Changeset{
			Slug:   e.Name(),
			Dir:    filepath.Join(Root, e.Name()),
			Base:   md["base"],
			Exists: true,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
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
	md := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
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

// Write creates the changeset directory, CHANGESET.yaml, and ABOUT.md.
//
// It is idempotent: existing files are never overwritten. When CHANGESET.yaml
// names a different base, ErrBaseConflict is returned unless setBase is true.
// Nothing is committed — the author commits scaffolding with their own
// implementation work.
func Write(repo *git.Repo, c Changeset, base string, setBase bool) (written []string, err error) {
	if c.Dir == "" {
		return nil, errors.New("changeset: empty directory")
	}
	if base == "" {
		return nil, errors.New("changeset: base must not be empty")
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
	if existing, ok := md["base"]; ok && existing != "" && existing != base && !setBase {
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
	if _, err := os.Stat(aboutPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(aboutPath, []byte(AboutTemplate(c.Slug)), 0o644); err != nil {
			return written, err
		}
		written = append(written, c.AboutPath())
	}
	return written, nil
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
