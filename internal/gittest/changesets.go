package gittest

import (
	"os"
	"path/filepath"
	"strings"
)

// Changeset layout helpers.
//
// These mirror PRD §4/§5/§7 (the `changesets/<slug>/` directory, its
// CHANGESET.yaml, ABOUT.md and thread files) so engine tests can build a
// scenario without running `gitpr change init`. A broken `change init` must
// therefore only fail the tests that exercise it. Product-written scaffolding is
// asserted by the CLI tests in internal/cli.

// DefaultAboutBody is the ABOUT.md content fixture helpers write. It is
// deliberately not `change init`'s scaffold, because tests need an ABOUT.md that
// counts as described.
const DefaultAboutBody = "# Changeset\n\n## Summary\n\nDescribed by the test fixture.\n"

// ChangesetPath is the repository-relative path of a file inside a changeset
// directory.
func (f *Fixture) ChangesetPath(slug string, parts ...string) string {
	all := append([]string{"changesets", slug}, parts...)
	return filepath.Join(all...)
}

// StageChangeset writes a changeset's CHANGESET.yaml and ABOUT.md into the
// working tree without committing them.
func (f *Fixture) StageChangeset(slug, base string) {
	f.t.Helper()
	f.Write(f.ChangesetPath(slug, "CHANGESET.yaml"), "base: "+base+"\n")
	if !f.HasWorktreeFile(f.ChangesetPath(slug, "ABOUT.md")) {
		f.Write(f.ChangesetPath(slug, "ABOUT.md"), DefaultAboutBody)
	}
}

// CommitChangeset writes and commits a changeset's CHANGESET.yaml and ABOUT.md as
// an ordinary implementation commit.
func (f *Fixture) CommitChangeset(slug, base string, opts ...CommitOpt) string {
	f.t.Helper()
	f.StageChangeset(slug, base)
	return f.Commit("Add changeset "+slug, opts...)
}

// ChangesetFile returns a file from a changeset directory in the working tree.
func (f *Fixture) ChangesetFile(slug, name string) string {
	f.t.Helper()
	return f.Read(f.ChangesetPath(slug, name))
}

// WriteChangesetFile writes a file into a changeset directory in the working tree.
func (f *Fixture) WriteChangesetFile(slug, name, content string) {
	f.t.Helper()
	f.Write(f.ChangesetPath(slug, name), content)
}

// MetadataBase returns the `base:` value recorded in a changeset's
// CHANGESET.yaml in the working tree.
func (f *Fixture) MetadataBase(slug string) string {
	f.t.Helper()
	for _, line := range strings.Split(f.ChangesetFile(slug, "CHANGESET.yaml"), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "base") {
			return strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return ""
}

// Threads lists the thread file names in a changeset directory, ABOUT.md excluded.
func (f *Fixture) Threads(slug string) []string {
	f.t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.dir, "changesets", slug))
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == "ABOUT.md" || !strings.HasSuffix(name, ".md") {
			continue
		}
		out = append(out, name)
	}
	return out
}
