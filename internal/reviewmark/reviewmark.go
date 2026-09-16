// Package reviewmark keeps the reviewed/unreviewed marks a reviewer makes in the TUI
// between sessions.
//
// PRD §16 makes marks non-durable: they are not part of the review artifact, are not
// shared with the author, and do not affect derived state. Persisting them is a local
// convenience, so they live under the repository's git directory, where git status cannot
// see them and git add cannot stage them. Deleting the directory loses nothing but marks.
//
// Each set is keyed by the commit the review was looking at, and each mark carries the
// diff key of the file it belongs to. A mark is restored only when the same path has the
// same diff content, so a new commit, a rebase, or a different span cannot revive a mark
// that no longer describes anything.
package reviewmark

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// Set maps a file path to the diff key it was reviewed at.
type Set map[string]string

// Mark is one entry as written; a sorted slice keeps the file byte-stable.
type Mark struct {
	Path string `json:"path"`
	Key  string `json:"key"`
}

// Keep is how many commits' mark sets a changeset retains. Marks are worthless once their
// commit is gone, and a long-lived branch would otherwise accumulate them forever.
const Keep = 12

var (
	safeName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	safeSHA  = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
)

type file struct {
	Commit   string `json:"commit"`
	Updated  string `json:"updated"`
	Reviewed []Mark `json:"reviewed"`
}

// Store holds the mark sets for one changeset.
type Store struct{ dir string }

// New points a store at <gitdir>/gitpr/marks/<slug>.
func New(gitDir, slug string) (*Store, error) {
	if !safeName.MatchString(slug) {
		return nil, fmt.Errorf("cannot store review marks for %q: unexpected changeset name", slug)
	}
	return &Store{dir: filepath.Join(gitDir, "gitpr", "marks", slug)}, nil
}

// Dir is where this changeset's mark sets live, for reporting and tests.
func (s *Store) Dir() string { return s.dir }

func (s *Store) path(commit string) (string, error) {
	if !safeSHA.MatchString(commit) {
		return "", fmt.Errorf("cannot store review marks against %q: not a commit id", commit)
	}
	return filepath.Join(s.dir, commit+".json"), nil
}

// Load returns the marks recorded against a commit. A missing file, an unreadable one and
// unparseable JSON all mean "no marks" rather than an error: marks are a convenience, and
// losing them must never stop a review from opening.
func (s *Store) Load(commit string) (Set, error) {
	path, err := s.path(commit)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil || f.Commit != commit {
		return nil, nil
	}
	if len(f.Reviewed) == 0 {
		return nil, nil
	}
	set := make(Set, len(f.Reviewed))
	for _, m := range f.Reviewed {
		set[m.Path] = m.Key
	}
	return set, nil
}

// Save writes the marks for one commit, replacing any previous set for it.
func (s *Store) Save(commit string, set Set) error {
	path, err := s.path(commit)
	if err != nil {
		return err
	}
	f := file{Commit: commit, Updated: time.Now().UTC().Format(time.RFC3339)}
	for _, p := range sortedKeys(set) {
		f.Reviewed = append(f.Reviewed, Mark{Path: p, Key: set[p]})
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	// Written via a temporary file so an interrupted save cannot leave half a set
	// that the next session would read as a complete one.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	s.prune()
	return nil
}

// prune drops the oldest mark sets beyond Keep. Failures are ignored: a stale directory
// is not worth failing a review over.
func (s *Store) prune() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	type entry struct {
		name string
		when time.Time
	}
	var files []entry
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, entry{e.Name(), info.ModTime()})
	}
	if len(files) <= Keep {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].when.After(files[j].when) })
	for _, old := range files[Keep:] {
		os.Remove(filepath.Join(s.dir, old.name))
	}
}

func sortedKeys(set Set) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
