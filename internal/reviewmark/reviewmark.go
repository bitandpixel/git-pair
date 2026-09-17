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
//
// A path can therefore hold several keys at once — one for each span it was marked reviewed
// in — because two spans ending at the same commit usually diff the same file differently.
// That is why saving replaces one (path, key) pair rather than the whole set for a path: the
// span you are standing on does not own the other span's marks.
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

// Set holds the diff keys a reviewer marked reviewed, grouped by path.
type Set map[string][]string

// Has reports whether this path was marked reviewed at exactly this diff key. A nil Set means
// no marks, which is how a missing or unreadable set arrives.
func (s Set) Has(path, key string) bool {
	for _, k := range s[path] {
		if k == key {
			return true
		}
	}
	return false
}

// add records one mark. Adding what is already there changes nothing, so a mark carried
// through two scans does not duplicate itself.
func (s Set) add(path, key string) {
	if s.Has(path, key) {
		return
	}
	s[path] = append(s[path], key)
}

// remove drops one mark and leaves the other keys of the same path alone. The full slice
// expression copies rather than shifting in place, so a Set that shared a backing array with
// something else cannot be edited through the hole.
func (s Set) remove(path, key string) {
	keys := s[path]
	for i, k := range keys {
		if k != key {
			continue
		}
		rest := append(keys[:i:i], keys[i+1:]...)
		if len(rest) == 0 {
			delete(s, path)
		} else {
			s[path] = rest
		}
		return
	}
}

// Mark is one entry as written; a sorted slice keeps the file byte-stable.
type Mark struct {
	Path string `json:"path"`
	Key  string `json:"key"`
}

// Answer is one file the reviewer was shown, and whether they marked it reviewed. Saving
// needs the unreviewed ones too: an answer that says "not reviewed" is what overwrites a mark.
type Answer struct {
	Path     string
	Key      string
	Reviewed bool
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

// New points a store at <gitdir>/git-pair/marks/<slug>.
func New(gitDir, slug string) (*Store, error) {
	if !safeName.MatchString(slug) {
		return nil, fmt.Errorf("cannot store review marks for %q: unexpected changeset name", slug)
	}
	return &Store{dir: filepath.Join(gitDir, "git-pair", "marks", slug)}, nil
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
		if !set.Has(m.Path, m.Key) {
			set[m.Path] = append(set[m.Path], m.Key)
		}
	}
	return set, nil
}

// Save records the reviewer's answers for the files of one span, against the commit that span
// ends at. It replaces only the (path, key) pairs it is given: marks recorded at other keys of
// the same path — the same file as it differs in another span over the same commit — survive
// the write. Clearing every mark is still remembered, because the cleared pairs come back as
// answers that say unreviewed.
func (s *Store) Save(commit string, answers []Answer) error {
	path, err := s.path(commit)
	if err != nil {
		return err
	}
	// Started from what is already on disk rather than from nothing: this write speaks for one
	// span, and a whole-set replacement would erase the marks made in another.
	set, err := s.Load(commit)
	if err != nil {
		return err
	}
	if set == nil {
		set = Set{}
	}
	for _, a := range answers {
		set.remove(a.Path, a.Key)
		if a.Reviewed {
			set.add(a.Path, a.Key)
		}
	}

	f := file{Commit: commit, Updated: time.Now().UTC().Format(time.RFC3339)}
	for _, p := range sortedPaths(set) {
		keys := append([]string(nil), set[p]...)
		sort.Strings(keys)
		for _, k := range keys {
			f.Reviewed = append(f.Reviewed, Mark{Path: p, Key: k})
		}
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

func sortedPaths(set Set) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
