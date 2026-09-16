package reviewmark_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitpr/internal/reviewmark"
)

const commit = "9f2c1d4b8a7e6f5d4c3b2a1908f7e6d5c4b3a291"

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := reviewmark.New(dir, "booking-transaction")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	set := []reviewmark.Answer{
		{Path: "service.go", Key: "aabbccdd11223344", Reviewed: true},
		{Path: "handler.go", Key: "9988776655443322", Reviewed: true},
		// Answered but not marked: this is what overwrites a mark from an earlier session.
		{Path: "main.go", Key: "1122334455667788", Reviewed: false},
	}
	if err := store.Save(commit, set); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := store.Load(commit)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Load returned %d paths, want 2 (the unreviewed answer stores nothing)", len(got))
	}
	for _, a := range set {
		if has := got.Has(a.Path, a.Key); has != a.Reviewed {
			t.Errorf("Has(%s, %s) = %v, want %v", a.Path, a.Key, has, a.Reviewed)
		}
	}
	// Inside the git directory, so nothing about it can reach the working tree.
	if !strings.HasPrefix(store.Dir(), filepath.Join(dir, "gitpr", "marks")) {
		t.Errorf("store dir = %q, want it under <gitdir>/gitpr/marks", store.Dir())
	}
}

// A mark belongs to one commit's diff; the next commit must not inherit it.
func TestLoadIsScopedToTheCommit(t *testing.T) {
	store, err := reviewmark.New(t.TempDir(), "booking")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := store.Save(commit, []reviewmark.Answer{{Path: "service.go", Key: "aabb", Reviewed: true}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	other := "1111111111111111111111111111111111111111"
	got, err := store.Load(other)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Load for another commit returned %v, want no marks", got)
	}
}

// Saving an answer that says unreviewed has to overwrite the stored mark, or clearing every
// mark would be undone by the next session.
func TestSaveEmptySetClearsTheStoredOne(t *testing.T) {
	store, err := reviewmark.New(t.TempDir(), "booking")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := store.Save(commit, []reviewmark.Answer{{Path: "service.go", Key: "aabb", Reviewed: true}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Save(commit, []reviewmark.Answer{{Path: "service.go", Key: "aabb"}}); err != nil {
		t.Fatalf("Save empty: %v", err)
	}
	got, err := store.Load(commit)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Load after clearing = %v, want no marks", got)
	}
}

// The span you are standing on is not the only span that can end at this commit: the full
// changeset and the unreviewed span both end at HEAD, and the same file has a different diff
// in each. Saving one must not erase the other's marks — this is the write that made marks
// vanish when a reviewer pressed `v` away and back.
func TestSaveKeepsTheMarksAnotherSpanMade(t *testing.T) {
	store, err := reviewmark.New(t.TempDir(), "booking")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	full := []reviewmark.Answer{
		{Path: "service.go", Key: "keyFull", Reviewed: true},
		{Path: "handler.go", Key: "keyFullHandler", Reviewed: false},
	}
	if err := store.Save(commit, full); err != nil {
		t.Fatalf("Save full span: %v", err)
	}
	unreviewed := []reviewmark.Answer{
		{Path: "service.go", Key: "keyUnreviewed", Reviewed: true},
	}
	if err := store.Save(commit, unreviewed); err != nil {
		t.Fatalf("Save unreviewed span: %v", err)
	}

	got, err := store.Load(commit)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.Has("service.go", "keyFull") {
		t.Error("the other span's mark was erased by this span's save")
	}
	if !got.Has("service.go", "keyUnreviewed") {
		t.Error("this span's mark was not recorded")
	}

	// Clearing in one span answers that pair alone.
	if err := store.Save(commit, []reviewmark.Answer{{Path: "service.go", Key: "keyUnreviewed"}}); err != nil {
		t.Fatalf("Save cleared: %v", err)
	}
	got, err = store.Load(commit)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Has("service.go", "keyUnreviewed") {
		t.Error("the cleared mark came back")
	}
	if !got.Has("service.go", "keyFull") {
		t.Error("clearing one span cleared the other's mark too")
	}
}

// Marks are a convenience: a damaged store must never stop a review from opening.
func TestLoadToleratesBrokenData(t *testing.T) {
	dir := t.TempDir()
	store, err := reviewmark.New(dir, "booking")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := os.MkdirAll(store.Dir(), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	bad := map[string]string{
		"not json":         "{",
		"wrong commit key": `{"commit":"other","reviewed":[{"path":"a.go","key":"k"}]}`,
		"wrong type":       `{"commit":"` + commit + `","reviewed":"a.go"}`,
	}
	for _, body := range bad {
		path := filepath.Join(store.Dir(), commit+".json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		got, err := store.Load(commit)
		if err != nil {
			t.Errorf("Load of %q returned %v, want no marks and no error", body, err)
		}
		if len(got) != 0 {
			t.Errorf("Load of %q = %v, want no marks", body, got)
		}
	}
}

func TestLoadRejectsUnusableCommitIDs(t *testing.T) {
	store, err := reviewmark.New(t.TempDir(), "booking")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, bad := range []string{"", "not-a-sha", "../../etc/passwd", "a/b", "DEADBEEF"} {
		if _, err := store.Load(bad); err == nil {
			t.Errorf("Load(%q) succeeded, want an error", bad)
		}
		if err := store.Save(bad, []reviewmark.Answer{{Path: "a.go", Key: "k", Reviewed: true}}); err == nil {
			t.Errorf("Save(%q) succeeded, want an error", bad)
		}
	}
}

func TestNewRejectsUnusableChangeNames(t *testing.T) {
	if _, err := reviewmark.New(t.TempDir(), "../elsewhere"); err == nil {
		t.Error("New accepted a changeset name that escapes the directory")
	}
}

// Old commits' marks are dead weight; the store keeps the newest Keep of them.
func TestSavePrunesOldCommits(t *testing.T) {
	dir := t.TempDir()
	store, err := reviewmark.New(dir, "booking")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := os.MkdirAll(store.Dir(), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Pre-seed with more sets than the store keeps, each clearly older than the next.
	for i := 0; i < reviewmark.Keep+3; i++ {
		name := filepath.Join(store.Dir(), shaFor(i)+".json")
		body := `{"commit":"` + shaFor(i) + `","updated":"2026-01-01T00:00:00Z","reviewed":[{"path":"a.go","key":"k"}]}`
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		when := time.Now().Add(-time.Duration(reviewmark.Keep+3-i) * time.Hour)
		if err := os.Chtimes(name, when, when); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}
	fresh := shaFor(999)
	if err := store.Save(fresh, []reviewmark.Answer{{Path: "a.go", Key: "k", Reviewed: true}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(store.Dir())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != reviewmark.Keep {
		t.Errorf("store holds %d mark sets, want %d", len(entries), reviewmark.Keep)
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), fresh+".json")); err != nil {
		t.Errorf("the set just written was pruned: %v", err)
	}
}

func shaFor(i int) string {
	digits := []byte("0123456789abcdef")
	out := make([]byte, 40)
	for j := range out {
		out[j] = digits[(i*7+j*3)%16]
	}
	out[39] = digits[i%16]
	return string(out)
}
