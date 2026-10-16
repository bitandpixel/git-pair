package hooks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/git"
	"gitpair/internal/gittest"
)

// Where a position on the tracked hooks is written, and what it covers.
//
// A position is an answer about content — "I read these scripts, run them" or "I read them, do not" — and the
// content is the same in every worktree that has it checked out. Recording it per worktree asked one person to
// read a repository's checks once per directory `git worktree add` made, and the tenth reading of the same dozen
// lines is not a safety check; it is the pressure that teaches people to run `--trust` without reading.
//
// These tests hold the two halves of the answer. The record belongs to the clone, so one reading covers the
// worktrees cut from it. And it is a set, so covering them does not mean overwriting them: two worktrees standing
// on two versions of the directory keep a position about each.

// hooksRepo builds a repository with one committed hook, and returns the fixture, a repo for its main checkout,
// and the path of the hook inside it. The hook is committed rather than dropped in, because a worktree only sees
// what the commit carries, and these tests are about what two worktrees see.
func hooksRepo(t *testing.T, body string) (*gittest.Fixture, *git.Repo, string) {
	t.Helper()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n"))
	f.Commit("hooks", gittest.WithFile(hookRelPath, body))
	return f, &git.Repo{Dir: f.Dir(), Env: f.Env()}, filepath.Join(f.Dir(), hookRelPath)
}

const hookRelPath = ".git-pair/hooks/pre-ready/01-suite"

// addWorktree cuts a worktree from the clone at its current head. It starts on the same content as the main
// checkout, and the two then diverge by commits made in one of them.
func addWorktree(t *testing.T, f *gittest.Fixture, name, branch string) *git.Repo {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	f.MustGit("worktree", "add", "-q", "-b", branch, dir, "HEAD")
	return &git.Repo{Dir: dir, Env: f.Env()}
}

// moveHookTo commits new hook content in the main checkout, which is how the branch a worktree stands on ends up
// holding a different version of the same directory.
func moveHookTo(t *testing.T, f *gittest.Fixture, body, msg string) {
	t.Helper()
	f.Commit(msg, gittest.WithFile(hookRelPath, body))
}

func recordFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, "git-pair", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func recordLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// hashAt is a distinct well-formed tree hash, for tests that exercise the record itself rather than a real tree.
func hashAt(i int) string { return fmt.Sprintf("%032x", i) }

// The headline the feature exists for: reading the scripts in one worktree is an answer for the whole clone.
func TestTrustRecordCoversEveryWorktree(t *testing.T) {
	ctx := context.Background()
	f, main, _ := hooksRepo(t, "#!/bin/sh\nexit 0\n")
	wt := addWorktree(t, f, "feature", "feature/hooks")

	hash, _, err := Accept(ctx, wt)
	if err != nil {
		t.Fatalf("Accept in the worktree: %v", err)
	}

	// The write went to the directory the clone shares, and the worktree's own git directory holds nothing. A
	// record that lands beside the worktree is the behaviour this change exists to remove, and `--json` alone
	// cannot see the difference: from the worktree that wrote it, both layouts answer `accepted`.
	wtDir, err := wt.GitDir(ctx)
	if err != nil {
		t.Fatalf("worktree git dir: %v", err)
	}
	common, ok := git.CommonDir(wtDir)
	if !ok {
		t.Fatalf("CommonDir(%q) says this is not a linked worktree, so the test's own premise is wrong", wtDir)
	}
	if _, err := os.Stat(filepath.Join(common, "git-pair", trustedRecordName)); err != nil {
		t.Errorf("no acceptance in the directory the clone shares (%s): %v", common, err)
	}
	if _, err := os.Stat(filepath.Join(wtDir, "git-pair", trustedRecordName)); err == nil {
		t.Errorf("acceptance written beside the worktree at %s, want it in the clone's shared directory", wtDir)
	}

	// The checkout that was never asked is the one that used to refuse.
	state, accepted, current, err := Trust(ctx, main)
	if err != nil {
		t.Fatalf("Trust in the main checkout: %v", err)
	}
	if state != TrustAccepted || accepted != hash || current != hash {
		t.Errorf("main checkout: state=%s accepted=%q current=%q, want accepted at %q — one reading should cover the clone",
			state, accepted, current, hash)
	}
}

// Sharing one record only works if sharing it cannot overwrite. Two worktrees on two versions of the directory is
// where a single-slot record goes wrong: whoever recorded last would make the other checkout read as though the
// repository had moved under them, when all that moved was somebody else's branch.
func TestWorktreesOnDifferentVersionsBothKeepTheirPosition(t *testing.T) {
	ctx := context.Background()
	f, main, _ := hooksRepo(t, "#!/bin/sh\nexit 0\n")
	wt := addWorktree(t, f, "feature", "feature/hooks")

	if _, _, err := Accept(ctx, wt); err != nil {
		t.Fatalf("Accept on the branch: %v", err)
	}
	// Trunk moves to a second version and gets its own reading, in the other worktree.
	moveHookTo(t, f, "#!/bin/sh\nexit 1\n", "the suite changed")
	trunkHash, _, err := Accept(ctx, main)
	if err != nil {
		t.Fatalf("Accept on trunk: %v", err)
	}

	for _, tc := range []struct {
		where string
		repo  *git.Repo
	}{
		{"the branch", wt},
		{"trunk", main},
	} {
		state, _, current, err := Trust(ctx, tc.repo)
		if err != nil {
			t.Fatalf("Trust on %s: %v", tc.where, err)
		}
		if state != TrustAccepted {
			t.Errorf("%s: state=%s, want accepted — one worktree's position must not answer for the other's", tc.where, state)
		}
		if current == "" {
			t.Errorf("%s: the tracked tree hashed to nothing, so this test is comparing two absences", tc.where)
		}
	}

	// Trunk moving a third time is content nobody read, and it reads that way even with two accepted versions in
	// the set: sharing the record is not exempting the directory.
	moveHookTo(t, f, "#!/bin/sh\nexit 2\n", "trunk moved again")
	state, accepted, current, err := Trust(ctx, main)
	if err != nil {
		t.Fatalf("Trust after trunk moved: %v", err)
	}
	if state != TrustChanged {
		t.Errorf("trunk after a new version: state=%s, want changed — the set holds two versions and neither is this one", state)
	}
	if accepted != trunkHash || current == trunkHash {
		t.Errorf("trunk: accepted=%q current=%q, want the report to name the newest accepted %q beside the new tree", accepted, current, trunkHash)
	}
	// And the branch, which nobody touched, is still where its reader left it.
	if state, _, _, err := Trust(ctx, wt); err != nil || state != TrustAccepted {
		t.Errorf("the branch after trunk moved twice: state=%s (%v), want its acceptance to survive", state, err)
	}
}

// A clone that recorded its position under the per-worktree path keeps it. Relocating where the record lives is
// not a reason to make somebody read the same scripts a second time.
func TestLegacyPerWorktreeRecordStillAnswers(t *testing.T) {
	ctx := context.Background()
	f, _, _ := hooksRepo(t, "#!/bin/sh\nexit 0\n")
	wt := addWorktree(t, f, "feature", "feature/hooks")

	hash, _, err := TreeHash(wt.Dir)
	if err != nil || hash == "" {
		t.Fatalf("TreeHash = %q (%v), want a hash to record", hash, err)
	}
	wtDir, err := wt.GitDir(ctx)
	if err != nil {
		t.Fatalf("worktree git dir: %v", err)
	}
	recordFile(t, wtDir, trustedRecordName, hash+"\n")

	state, accepted, current, err := Trust(ctx, wt)
	if err != nil {
		t.Fatalf("Trust: %v", err)
	}
	if state != TrustAccepted || accepted != hash || current != hash {
		t.Errorf("legacy record: state=%s accepted=%q current=%q, want accepted at %q", state, accepted, current, hash)
	}
	if _, err := os.Stat(filepath.Join(wtDir, "git-pair", trustedRecordName)); err != nil {
		t.Errorf("the legacy record was moved out of the way: %v", err)
	}
}

// The record is a set. A position about other content survives, the hash being answered for leaves the other
// record, and a line that is not a tree hash is ignored rather than believed.
func TestPositionsCancelOneHashAndIgnoreJunk(t *testing.T) {
	h1, h2, junk := hashAt(1), hashAt(2), "not-a-hash"
	dir := t.TempDir()
	recordFile(t, dir, declinedRecordName, strings.Join([]string{h1, h2, junk, ""}, "\n"))

	if err := recordPosition([]string{dir}, dir, trustedRecordName, declinedRecordName, h1); err != nil {
		t.Fatalf("recordPosition: %v", err)
	}
	if got := recordLines(t, recordPath(dir, trustedRecordName)); len(got) != 1 || got[0] != h1 {
		t.Errorf("accepted = %v, want only %s", got, h1)
	}
	kept := recordLines(t, recordPath(dir, declinedRecordName))
	if len(kept) != 1 || kept[0] != h2 {
		t.Errorf("declined = %v, want %s kept and %s cleared — a decline about content nobody is standing on is still a position somebody took", kept, h2, h1)
	}

	// Junk never decides anything. The record is content a person read, and a half-written line is not content,
	// so an unrecorded tree has to stay unrecorded rather than become an acceptance nobody holds.
	recordFile(t, dir, trustedRecordName, junk+"\n\n")
	if _, accepted, current, err := TrustAt([]string{dir}, dir); err != nil || accepted != "" {
		t.Errorf("accepted = %q (%v) from a file of junk, want nothing accepted beside the tree %q", accepted, err, current)
	}
}

// The bound keeps a repository that edits its hooks weekly from leaving every clone it touches carrying a file
// nobody reads, read on the path to a hand-off.
func TestRecordStaysBounded(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxRecordedPositions+12; i++ {
		if err := recordPosition([]string{dir}, dir, trustedRecordName, declinedRecordName, hashAt(i)); err != nil {
			t.Fatalf("recordPosition %d: %v", i, err)
		}
	}
	got := recordLines(t, recordPath(dir, trustedRecordName))
	if len(got) > maxRecordedPositions {
		t.Errorf("the accepted record holds %d hashes, want at most %d", len(got), maxRecordedPositions)
	}
	// Trimming the oldest is the only acceptable bound: the newest entry is the one a report prints.
	if got[len(got)-1] != hashAt(maxRecordedPositions+11) {
		t.Errorf("newest accepted = %q, want %q", got[len(got)-1], hashAt(maxRecordedPositions+11))
	}
}

// What the hash of the tracked tree is allowed to depend on.
//
// The bytes, the path, and whether the file is executable — because those are what a person read and what the
// repository is asking to run. Not the rest of the permission set: git checks a file out at the machine's umask,
// and a file written by an editor, `tar`, or a fixture arrives at whatever its writer chose. When those bits were
// in the hash, two worktrees of one commit holding one byte-for-byte script hashed apart, and every clone that
// added a worktree had a new position to record about content nobody had changed.
func TestTreeHashCoversContentAndExecutabilityOnly(t *testing.T) {
	f, _, hook := hooksRepo(t, "#!/bin/sh\nexit 0\n")
	repo := &git.Repo{Dir: f.Dir(), Env: f.Env()}

	hashAtMode := func(mode os.FileMode) string {
		t.Helper()
		if err := os.Chmod(hook, mode); err != nil {
			t.Fatalf("chmod %s: %v", hook, err)
		}
		hash, _, err := TreeHash(repo.Dir)
		if err != nil || hash == "" {
			t.Fatalf("TreeHash at %o: %q, %v", mode, hash, err)
		}
		return hash
	}

	writable := hashAtMode(0o644)
	if got := hashAtMode(0o600); got != writable {
		t.Errorf("hash at 0600 = %q, want it to equal 0644's %q — who may write a file is not what it runs", got, writable)
	}
	if got := hashAtMode(0o664); got != writable {
		t.Errorf("hash at 0664 = %q, want it to equal 0644's %q — the umask a worktree was checked out with is not content", got, writable)
	}
	executable := hashAtMode(0o755)
	if got := hashAtMode(0o775); got != executable {
		t.Errorf("hash at 0775 = %q, want it to equal 0755's %q", got, executable)
	}
	if executable == writable {
		t.Error("an executable hook hashes the same as a non-executable one; making a script runnable changes what the repository asks to run")
	}

	// And the bytes still decide: a position is keyed to content, so an edit after the approval is new content.
	if _, _, err := Accept(context.Background(), repo); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	moveHookTo(t, f, "#!/bin/sh\nexit 9\n", "someone edited the suite")
	state, _, _, err := Trust(context.Background(), repo)
	if err != nil {
		t.Fatalf("Trust: %v", err)
	}
	if state != TrustChanged {
		t.Errorf("after an edit to the bytes: state=%s, want changed", state)
	}
}
