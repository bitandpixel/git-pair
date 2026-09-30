package factcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type payload struct {
	Commit string `json:"commit"`
	N      int    `json:"n"`
}

// openStore returns a store that is turned on, pointed at a directory that belongs to the test.
func openStore(t *testing.T) *Store {
	t.Helper()
	s := New(filepath.Join(t.TempDir(), "gitdir"))
	s.Use(true)
	return s
}

func TestRoundTrip(t *testing.T) {
	s := openStore(t)
	want := payload{Commit: strings.Repeat("a", 40), N: 7}
	s.Put(Key("chain", want.Commit, "alpha"), want)

	var got payload
	if !s.Get(Key("chain", want.Commit, "alpha"), &got) {
		t.Fatal("no answer from a store that was just written")
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// A different commit is a different key. This is the whole invalidation model, so it is worth stating as
// a case rather than leaving it to the reader of Key.
func TestDifferentCommitIsADifferentKey(t *testing.T) {
	s := openStore(t)
	s.Put(Key("chain", "a", "alpha"), payload{Commit: "a", N: 1})

	var got payload
	if s.Get(Key("chain", "b", "alpha"), &got) {
		t.Errorf("an answer for commit a was served for commit b: %+v", got)
	}
}

// The key carries every commit the answer was derived from, in order. Two-ended questions — is this
// ancestor of that — have answers that depend on which end is which, and a key that lost the order would
// let one answer the other.
func TestKeyDistinguishesArgumentOrder(t *testing.T) {
	if Key("ancestor", "a", "b") == Key("ancestor", "b", "a") {
		t.Error("Key(a,b) == Key(b,a): an ordered question would get one answer for both directions")
	}
	if Key("chain", "a", "alpha") == Key("chain", "a", "beta") {
		t.Error("Key does not distinguish the changeset")
	}
	if Key("chain", "a", "alpha") == Key("summary", "a", "alpha") {
		t.Error("Key does not distinguish the question")
	}
}

// Everything below here is the same claim stated for each way an entry can be unusable: the cache is a
// speed change over asking git, so an entry that cannot be trusted is a miss and never an error, and never
// a wrong answer.

func TestMissingKeyIsAMiss(t *testing.T) {
	s := openStore(t)
	var got payload
	if s.Get(Key("chain", "a", "alpha"), &got) {
		t.Errorf("a miss reported as a hit: %+v", got)
	}
}

func TestOffStoreWritesNothingAndMissesEverything(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gitdir")
	s := New(dir)
	s.Put(Key("chain", "a", "alpha"), payload{N: 1})
	var got payload
	if s.Get(Key("chain", "a", "alpha"), &got) {
		t.Error("a store nobody turned on served an answer")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("a store nobody turned on created %s", dir)
	}
}

func TestCorruptEntryIsAMiss(t *testing.T) {
	s := openStore(t)
	key := Key("chain", "a", "alpha")
	s.Put(key, payload{N: 3})
	path := s.path(key)
	if err := os.WriteFile(path, []byte(`{"format":"1","key":`), 0o644); err != nil {
		t.Fatalf("corrupt the entry: %v", err)
	}
	var got payload
	if s.Get(key, &got) {
		t.Errorf("half a JSON document was served as an answer: %+v", got)
	}
}

// The file name carries a truncated hash of the key. A collision there must be a miss, not an answer about
// a different commit, which is why the full key is written inside the file and checked on the way out.
func TestKeyMismatchInsideTheFileIsAMiss(t *testing.T) {
	s := openStore(t)
	key := Key("chain", "a", "alpha")
	s.Put(key, payload{N: 3})

	// Write a second key's answer into the first key's file name, which is what a collision looks like.
	other := Key("chain", "b", "beta")
	s.Put(other, payload{N: 9})
	data, err := os.ReadFile(s.path(other))
	if err != nil {
		t.Fatalf("read the other entry: %v", err)
	}
	if err := os.WriteFile(s.path(key), data, 0o644); err != nil {
		t.Fatalf("simulate the collision: %v", err)
	}
	var got payload
	if s.Get(key, &got) {
		t.Errorf("an answer derived for %s was served for %s: %+v", other, key, got)
	}
}

// A derivation whose answer changes shape is a different thing. An old entry must not be read through the
// new struct.
func TestEntryFromAnotherFormatIsAMiss(t *testing.T) {
	s := openStore(t)
	key := Key("chain", "a", "alpha")
	s.Put(key, payload{N: 3})
	path := s.path(key)
	var e entry
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the entry: %v", err)
	}
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("parse the entry: %v", err)
	}
	e.Format = "0"
	if out, err := json.Marshal(e); err != nil {
		t.Fatalf("re-encode: %v", err)
	} else if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("write the old-format entry: %v", err)
	}
	var got payload
	if s.Get(key, &got) {
		t.Errorf("an entry from another format was served: %+v", got)
	}
}

// A key with a path separator in it must not be able to write outside the cache directory. Keys come from
// Key, so today they cannot contain one; the check is here because a caller that built a key by hand
// should fail closed.
func TestKeyCannotEscapeTheDirectory(t *testing.T) {
	s := openStore(t)
	outside := Key("chain", "../../escape", "alpha")
	s.Put(outside, payload{N: 1})
	var got payload
	if !s.Get(outside, &got) {
		t.Fatal("the entry did not come back")
	}
	filepath.Walk(s.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(path, "..") {
			t.Errorf("an entry was written outside the cache directory: %s", path)
		}
		return nil
	})
	if _, err := os.Stat(filepath.Join(s.dir, "..", "escape")); !os.IsNotExist(err) {
		t.Error("a crafted key wrote above the cache directory")
	}
}

// The directory is bounded. Without this it grows with every commit ever landed, which is the growth this
// package is meant to make cheap.
func TestPruneBoundsTheDirectory(t *testing.T) {
	s := openStore(t)
	for i := 0; i < Keep+25; i++ {
		s.Put(Key("chain", string(rune('a'+i%26))+string(rune('a'+i%26)), "alpha"), payload{N: i})
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		t.Fatalf("read the cache directory: %v", err)
	}
	if len(entries) > Keep {
		t.Errorf("%d entries survived, more than the bound of %d", len(entries), Keep)
	}
	// What pruning removes is the oldest, so the answer the last run wrote is still there.
	var got payload
	if !s.Get(Key("chain", string(rune('a'+(Keep+24)%26))+string(rune('a'+(Keep+24)%26)), "alpha"), &got) {
		t.Error("the most recent entry is gone")
	}
}

// A store pointed at an unusable path must answer misses rather than panic or fail a command. This is the
// shape of a `Repo` aimed at a directory that is not a repository.
func TestUnusableDirectoryIsNotFatal(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "not-there", "deeper"))
	s.Use(true)
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, []byte("x"), 0o644); err != nil {
		t.Fatalf("set up a directory that cannot be made: %v", err)
	}
	blocked := New(parent)
	blocked.Use(true)
	key := Key("chain", "a", "alpha")
	blocked.Put(key, payload{N: 1})
	var got payload
	if blocked.Get(key, &got) {
		t.Error("an answer came from a directory that does not exist")
	}
	if reason, disabled := blocked.Disabled(); !disabled {
		t.Error("a store that could not write did not say so")
	} else if !strings.Contains(reason, "not a directory") && !strings.Contains(reason, "exists") {
		t.Errorf("the failure says %q, which does not name the problem", reason)
	}
}
