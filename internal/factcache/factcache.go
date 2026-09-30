// Package factcache keeps answers git-pair derived from commits, so the next run of the same command does
// not derive them again.
//
// One test decides what may be cached, and it is worth stating precisely because the whole safety of this
// package rests on it: an entry may be kept only when its value follows from the commit ids it was derived
// from, and those ids appear in its key. Git objects are immutable, so an entry derived from a commit is
// true for as long as the object is in the database and cannot be invalidated by anything that happens
// afterwards — a new commit on a branch, a fetch, a rebase, another machine's push. There is no expiry
// policy because there is nothing to expire: a branch that moved produces different commit ids, which is a
// different key, which is a miss and a fresh derivation.
//
// That test also says what must never come through here. Anything read from a ref name rather than a commit
// is mutable by definition, so a caller resolves the ref first and derives from the commit it names. The
// working tree and the index are mutable and outside the object database, so `git status` and an uncommitted
// directory are nowhere near this package. And an answer that involves the wall clock — how long ago a
// marker was written — is not a fact about a commit: cache the timestamp and let the caller format the age.
//
// Contrast the in-process memo in internal/git, which keys on a git invocation's arguments. Arguments name
// refs, and refs move, so that memo is only sound for the length of one command run. This one keys on
// commits and is sound for as long as the files sit on disk, which is why a review session open for an hour
// keeps this and drops that.
//
// Where the files live, and why there: under the repository's git directory, the same place review marks
// live. `git status` cannot see them, `git add` cannot stage them, and deleting the directory loses nothing
// but time.
package factcache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// format identifies the shape of a cached entry. A derivation whose answer changes shape bumps it, so an
// old build's entries are misses rather than values read through the wrong struct.
const format = "1"

// Keep is how many entries the directory holds before the oldest go. Entries are small and one landed
// changeset contributes a handful per destination commit, so this bounds a directory that would otherwise
// grow with every commit ever landed.
const Keep = 4000

// Store is the cache directory for one repository.
type Store struct {
	dir string

	mu  sync.Mutex
	on  bool
	err error // the first filesystem failure, reported once by Disabled
}

// New points a store at <gitdir>/git-pair/cache.
func New(gitDir string) *Store {
	return &Store{dir: filepath.Join(gitDir, "git-pair", "cache")}
}

// Dir is where the entries live, for reporting and tests.
func (s *Store) Dir() string { return s.dir }

// Use turns the store on. It is off until something asks, because a store that is never consulted should
// not create a directory, and because a handle built without a real git directory — a test fixture, a
// repository that turned out not to exist — should not write anywhere.
func (s *Store) Use(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.on = on
}

// On reports whether this store is being used.
func (s *Store) On() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.on
}

// Key names one derived fact: the question asked, and every commit it was derived from.
//
// The order of commits matters only in that the caller must be consistent; `Key("x", a, b)` and
// `Key("x", b, a)` are different keys, which is correct for a question whose answer depends on which end is
// which and merely wasteful otherwise. Callers pass them in a fixed order.
func Key(kind string, commits ...string) string {
	h := sha256.New()
	h.Write([]byte(kind))
	for _, c := range commits {
		h.Write([]byte{0})
		h.Write([]byte(c))
	}
	return fmt.Sprintf("%s-%s", kind, hex.EncodeToString(h.Sum(nil)[:16]))
}

// entry is what one file holds. The full key is stored alongside the value and checked on read: the file
// name carries a truncated hash, and a collision there must be a miss rather than an answer about the
// wrong commit.
type entry struct {
	Format string          `json:"format"`
	Key    string          `json:"key"`
	Value  json.RawMessage `json:"value"`
}

// Get reads the entry for key into v. False means "no usable answer", which covers a missing file, an
// unreadable one, an entry from an older format, a key that collides with another's file name, and
// unparseable JSON.
//
// All of those mean the same thing on purpose. The cache is a speed change over deriving the answer from
// git, and a cache that can make a command fail is a worse trade than a cache that occasionally misses.
func (s *Store) Get(key string, v any) bool {
	if !s.On() {
		return false
	}
	data, err := os.ReadFile(s.path(key))
	if err != nil {
		return false
	}
	var e entry
	if err := json.Unmarshal(data, &e); err != nil {
		return false
	}
	if e.Format != format || e.Key != key {
		return false
	}
	return json.Unmarshal(e.Value, v) == nil
}

// Put records one derived fact. Failures are not returned: an answer that could not be written is simply
// not cached, and the next run derives it again.
func (s *Store) Put(key string, v any) {
	if !s.On() {
		return
	}
	value, err := json.Marshal(v)
	if err != nil {
		return
	}
	data, err := json.Marshal(entry{Format: format, Key: key, Value: value})
	if err != nil {
		return
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		s.note(err)
		return
	}
	path := s.path(key)
	// Written through a temporary file and renamed, the same as a review mark set: an interrupted write
	// must not leave half an entry that the next run reads as a whole one. A truncated entry does fail
	// closed — the JSON will not parse, which is a miss — but a rename makes even that unlikely.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		s.note(err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		s.note(err)
		return
	}
	s.prune()
}

// Disabled reports the first filesystem failure the store hit, and whether the store should be given up on
// entirely. A git directory that is read-only, or a cache directory another user owns, is a reason to stop
// trying for the rest of the run rather than to fail on every entry.
func (s *Store) Disabled() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		return "", false
	}
	return s.err.Error(), true
}

func (s *Store) note(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
	}
}

// path is the file one key belongs in. The key is produced by Key and is already a safe name; the check is
// here so a caller that built a key by hand cannot write outside the directory.
func (s *Store) path(key string) string {
	safe := strings.NewReplacer("/", "-", "\\", "-", "..", "-").Replace(key)
	if safe == "" || strings.ContainsAny(safe, "\x00\n") {
		safe = "entry"
	}
	return filepath.Join(s.dir, safe+".json")
}

// prune drops the oldest entries once the directory is over Keep. It is best-effort and silent, the same
// posture as reviewmark's: cache housekeeping is never a reason to fail a command.
func (s *Store) prune() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	type file struct {
		name string
		when time.Time
	}
	var files []file
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, file{e.Name(), info.ModTime()})
	}
	if len(files) <= Keep {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].when.After(files[j].when) })
	for _, old := range files[Keep:] {
		os.Remove(filepath.Join(s.dir, old.name))
	}
}
