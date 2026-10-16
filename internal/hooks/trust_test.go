package hooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
// These tests hold the three halves of the answer. The record belongs to the clone, so one reading covers the
// worktrees cut from it. It is a set, so covering them does not mean overwriting them: two worktrees standing on
// two versions of the directory keep a position about each. And the client's own hooks root is the clone's for the
// same reason — a check attached to a machine is attached once, not once per checkout.

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

// The record is a set. A position about other content survives, the hash being answered for leaves the other
// record, and a line that is not a tree hash is ignored rather than believed.
func TestPositionsCancelOneHashAndIgnoreJunk(t *testing.T) {
	h1, h2, junk := hashAt(1), hashAt(2), "not-a-hash"
	dir := t.TempDir()
	recordFile(t, dir, declinedRecordName, strings.Join([]string{h1, h2, junk, ""}, "\n"))

	if err := recordPosition(dir, trustedRecordName, declinedRecordName, h1); err != nil {
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
	if _, accepted, current, err := TrustAt(dir, dir); err != nil || accepted != "" {
		t.Errorf("accepted = %q (%v) from a file of junk, want nothing accepted beside the tree %q", accepted, err, current)
	}
}

// The bound keeps a repository that edits its hooks weekly from leaving every clone it touches carrying a file
// nobody reads, read on the path to a hand-off.
func TestRecordStaysBounded(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxRecordedPositions+12; i++ {
		if err := recordPosition(dir, trustedRecordName, declinedRecordName, hashAt(i)); err != nil {
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

// The client's own root is the clone's, in the same sense the record is. A reviewer attaches a check to a
// repository they may not edit; making them attach it again for every worktree they cut asks for the same act
// twice, and the second act protects nothing that the first one did not.
func TestCloneRootIsSharedByEveryWorktree(t *testing.T) {
	ctx := context.Background()
	f, main, _ := hooksRepo(t, "#!/bin/sh\nexit 0\n")
	wt := addWorktree(t, f, "feature", "feature/hooks")

	wtDir, err := wt.GitDir(ctx)
	if err != nil {
		t.Fatalf("worktree git dir: %v", err)
	}
	common, ok := git.CommonDir(wtDir)
	if !ok {
		t.Fatalf("CommonDir(%q) says this is not a linked worktree", wtDir)
	}
	if want := CloneRootDir(wtDir); want != filepath.Join(common, "git-pair", "hooks") {
		t.Errorf("CloneRootDir for a worktree = %q, want the directory the clone shares", want)
	}
	if got := CloneRootDir(wtDir); got != CloneRootDir(mustGitDir(t, ctx, main)) {
		t.Errorf("the two checkouts disagree about where the client's hooks live: %q vs %q", got, CloneRootDir(mustGitDir(t, ctx, main)))
	}

	// Attaching a check from one worktree makes it a check the other one runs.
	attached := filepath.Join(CloneRootDir(wtDir), string(PreReady), "01-reviewer-check")
	if err := os.MkdirAll(filepath.Dir(attached), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(attached), err)
	}
	if err := os.WriteFile(attached, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", attached, err)
	}
	plan, err := Discover(ctx, main, PreReady)
	if err != nil {
		t.Fatalf("Discover in the main checkout: %v", err)
	}
	var found []string
	for _, h := range plan.Hooks {
		found = append(found, h.Path)
	}
	if !containsHash(found, attached) {
		t.Errorf("the main checkout did not find the check attached from the worktree: %v", found)
	}
}

func mustGitDir(t *testing.T, ctx context.Context, repo *git.Repo) string {
	t.Helper()
	dir, err := repo.GitDir(ctx)
	if err != nil {
		t.Fatalf("git dir: %v", err)
	}
	return dir
}

// One place, one rule. The record read is the shared directory and nothing else, which is what makes the
// simplification safe to state: a file left beside one worktree's git directory is inert, so there is no second
// answer that a reader has to know about to predict what `check` will say.
func TestRecordBesideAWorktreeIsInert(t *testing.T) {
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
	if state != TrustUnrecorded || accepted != "" || current != hash {
		t.Errorf("a record beside the worktree changed the answer: state=%s accepted=%q current=%q, want unrecorded at %q",
			state, accepted, current, hash)
	}
}

// What cannot run must not invalidate a position, and what can run must not hide from one.
//
// The identity used to be a recursive walk of `.git-pair/hooks/`, while discovery ran the entries a hooks directory
// holds that a reader would call a hook. Two rules, drifting apart in both directions — and the direction that
// reached a person was the wrong one. Opening a hook in vim leaves `.pre-ready.swp` beside it, the walk counted it,
// the clone lost its position, and the refusal named content nobody had edited. Both sides read hookFiles now, so
// the two tests below are one sentence said twice.
func TestTreeHashIgnoresWhatDiscoveryWouldNotRun(t *testing.T) {
	f, _, _ := hooksRepo(t, "#!/bin/sh\nexit 0\n")
	repo := &git.Repo{Dir: f.Dir(), Env: f.Env()}

	before, names, err := TreeHash(repo.Dir)
	if err != nil || before == "" {
		t.Fatalf("TreeHash: %q, %v", before, err)
	}
	if len(names) != 1 || names[0] != "pre-ready/01-suite" {
		t.Fatalf("names = %v, want the one hook the directory holds", names)
	}

	for _, rel := range []string{
		".git-pair/hooks/.pre-ready.swp",          // a vim swap file beside the point directory
		".git-pair/hooks/pre-ready/.01-suite.swp", // and one inside it
		".git-pair/hooks/pre-ready/01-suite~",     // an editor's backup of the hook
		".git-pair/hooks/pre-ready/README.md",     // a note about the suite
		".git-pair/hooks/pre-ready/02-off.sample", // git's own "no hook here" convention
		".git-pair/hooks/pre-ready/sub/deep",      // discovery looks one level down, not through a tree
		".git-pair/hooks/stray",                   // beside the points, where no point ever looks
	} {
		dropFile(t, f, rel, "not a hook\n")
	}

	after, namesAfter, err := TreeHash(repo.Dir)
	if err != nil {
		t.Fatalf("TreeHash: %v", err)
	}
	if after != before {
		t.Errorf("an entry that cannot run moved the identity: %q then %q — the clone would lose a position nobody earned", before, after)
	}
	if len(namesAfter) != 1 || namesAfter[0] != "pre-ready/01-suite" {
		t.Errorf("names = %v, want only the hook that can run", namesAfter)
	}
}

// The other half of the same rule is the half that keeps the mechanism honest: a script discovery would run has to
// sit inside the hash, or a repository could ship a new check that ran with nobody having read it. An identity that
// was merely *narrower* than the run would be worse than the walk it replaces.
func TestTreeHashCoversEveryHookDiscoveryRuns(t *testing.T) {
	f, _, _ := hooksRepo(t, "#!/bin/sh\nexit 0\n")
	repo := &git.Repo{Dir: f.Dir(), Env: f.Env()}
	ctx := context.Background()

	dropFile(t, f, ".git-pair/hooks/pre-ready/02-lint", "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(f.Dir(), ".git-pair/hooks/pre-ready/02-lint"), 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	dropFile(t, f, ".git-pair/hooks/pre-ready/README.md", "# not a hook\n")

	_, hashNames, err := TreeHash(repo.Dir)
	if err != nil {
		t.Fatalf("TreeHash: %v", err)
	}

	plan, err := Discover(ctx, repo, PreReady)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	var runs []string
	for _, h := range plan.Hooks {
		if h.Root == RootTracked {
			runs = append(runs, filepath.Join(PreReady.String(), h.Name))
		}
	}
	if strings.Join(runs, ",") != strings.Join(hashNames, ",") {
		t.Errorf("discovery runs %v while the identity covers %v — a person cannot accept a set they cannot see", runs, hashNames)
	}

	// And the set is live: a new runnable script is new content, whether or not anybody told the hash about it.
	if _, _, err := Accept(ctx, repo); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	dropFile(t, f, ".git-pair/hooks/pre-ready/03-unread", "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(f.Dir(), ".git-pair/hooks/pre-ready/03-unread"), 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	state, _, _, err := Trust(ctx, repo)
	if err != nil {
		t.Fatalf("Trust: %v", err)
	}
	if state != TrustChanged {
		t.Errorf("after a new runnable hook appeared: state=%s, want changed — an unread script must not inherit an old acceptance", state)
	}
}

// Upgrading must not ask a clean clone to read its hooks again. For a tree holding nothing but hooks, the new
// identity is the old one byte for byte, so a position recorded under the previous release still answers for the
// same content. The formula below is the walk's, spelled out rather than called, so the equality says something.
func TestTreeHashUnchangedForATreeWithOnlyHooks(t *testing.T) {
	body := "#!/bin/sh\nexit 0\n"
	f, _, hook := hooksRepo(t, body)
	repo := &git.Repo{Dir: f.Dir(), Env: f.Env()}
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	sum := sha256.Sum256([]byte(body))
	line := fmt.Sprintf("pre-ready/01-suite\t1\t%s", hex.EncodeToString(sum[:]))
	h := sha256.New()
	h.Write([]byte(line))
	h.Write([]byte{0})
	want := hex.EncodeToString(h.Sum(nil)[:16])

	got, _, err := TreeHash(repo.Dir)
	if err != nil {
		t.Fatalf("TreeHash: %v", err)
	}
	if got != want {
		t.Errorf("TreeHash = %q, want the walk's %q — a release that only changes what gets ignored should not ask a clean clone to re-read its hooks", got, want)
	}
}

// dropFile writes one file under the fixture, making whatever directories it needs. It is for the entries that
// appear beside a hooks tree without ever being committed: an editor's droppings, a note, a template.
func dropFile(t *testing.T, f *gittest.Fixture, rel, body string) {
	t.Helper()
	abs := filepath.Join(f.Dir(), rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(abs), err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", abs, err)
	}
}
