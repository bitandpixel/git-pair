package hooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gitpair/internal/git"
)

// The tracked hooks tree is code that runs on a clone. git keeps its own hooks under `.git/` and refuses to
// clone them for exactly this reason, so a tracked `.git-pair/hooks/` needs the step git chose not to need:
// a person records acceptance before the scripts run.
//
// Acceptance is recorded as the hash of the tree's content, not as a flag. An edit to any hook — including a
// line added after the approval — invalidates it, because the thing that was approved was that content.
//
// An untrusted tree means the tracked hooks do not run and the command says so on stderr; the operation
// itself proceeds. Refusing to work in a clone one has not yet approved would make the safety mechanism the
// thing people route around, which is the same asymmetry `--configure-fetch` uses for consent.

// The two records a clone holds about the tracked hooks tree: the hashes a person accepted, and the hashes the
// same person read and declined.
//
// They are separate files rather than markers inside one record because the two answers are not states of one
// thing: they are two positions a person can hold about two different trees. A clone may have accepted last
// month's suite and decline this month's, and reading the pair is what tells a report which of the two the
// person took *for the content in front of them* — which is the whole difference between a team that opted out
// and a team that never heard.
//
// Each file is a set: one tree hash per line, oldest first. A single slot was enough while the record lived in
// one worktree's git directory, because there was one tree in front of it. The record is clone-wide now, and two
// worktrees of one clone can stand on two different versions of the directory — one on trunk, one on the branch
// that edits the suite — so the record has to hold a position about each. Membership is what decides a state;
// order only decides which hash a report prints.
//
// Nothing shipped a per-worktree record, so there is no migration to make and no second place to read: this is
// the release that first asks the question.
const (
	trustedRecordName  = "trusted-hooks"
	declinedRecordName = "declined-hooks"
)

// maxRecordedPositions bounds what one clone remembers. The state machine asks only whether the tree in front
// of it is in the set, so the entries that matter are the current one and the handful of versions a team has
// actually run against. The bound keeps a repository that edits its hooks every week from accumulating a file
// nobody reads, and keeps `--trust` from becoming slower as history piles up.
const maxRecordedPositions = 32

// recordPath is one record inside a git directory.
func recordPath(dir, name string) string { return filepath.Join(dir, "git-pair", name) }

// TrustedRecordPath and DeclinedRecordPath name where a position gets written, for the surfaces that print the
// path they recorded to. A report that names the wrong path tells a reader to go and delete the wrong file.
func TrustedRecordPath(gitDir string) string { return recordPath(RecordDir(gitDir), trustedRecordName) }

func DeclinedRecordPath(gitDir string) string {
	return recordPath(RecordDir(gitDir), declinedRecordName)
}

// RecordDir is where a position about the tracked hooks belongs: the directory the clone shares with every
// worktree cut from it, or — where there is nothing to share — the git directory this command was asked about.
//
// A position is an answer about content, and the content is the same in every worktree that has it checked out,
// so the answer belongs to the clone. Recording it per worktree instead asked a person who keeps ten worktrees to
// read the same dozen lines ten times, and the tenth reading is not a safety check — it is the tax that teaches
// people to run `--trust` without reading.
//
// There is no reading of a record written beside a single worktree, which is a choice rather than an oversight:
// no release ever wrote one there, so the only people holding such a file are running a checkout of this
// repository. The cost of a second location is that every reader of this code has to work out which one answers.
func RecordDir(gitDir string) string {
	if common, ok := git.CommonDir(gitDir); ok {
		return common
	}
	return gitDir
}

// readPositions reads one record and returns the hashes it holds, oldest first. An absent file and a blank line
// both mean "no position", and a line that is not a tree hash is skipped rather than fatal: the record is a set of
// content a person read, and one corrupt line is not a reason to stop a hand-off.
func readPositions(dir, name string) ([]string, error) {
	data, err := os.ReadFile(recordPath(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the hook %s record: %w", name, err)
	}
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		hash := strings.TrimSpace(line)
		if hash == "" || !isTreeHash(hash) || seen[hash] {
			continue
		}
		seen[hash] = true
		out = append(out, hash)
	}
	return out, nil
}

// newest returns the entry a report prints: the last one recorded, or "" when nothing was.
func newest(list []string) string {
	if len(list) == 0 {
		return ""
	}
	return list[len(list)-1]
}

func containsHash(list []string, hash string) bool {
	for _, h := range list {
		if h == hash {
			return true
		}
	}
	return false
}

// isTreeHash is the shape TreeHash produces: 32 lowercase hex characters. Checking it is what lets a hand-edited
// or half-written record be ignored rather than believed.
func isTreeHash(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

// TreeHash hashes the tracked hooks tree: the path of every file the runner would execute, whether it is
// executable, and its content. "The files the runner would execute" is hookFiles — the same list discovery runs,
// so one rule decides and two things read it: what a person is asked to accept is exactly what can run. The
// recursive directory walk this replaces counted things that never run. An editor's `.pre-ready.swp` beside a hook
// moved the hash, the clone lost its position, and the refusal named content nobody had edited; nested directories
// and stray entries under the root counted too, though no point ever runs them. Both halves of that came from
// asking the question twice.
//
// The executable bit is in the hash because it is part of what a hook is — making a script executable, or removing
// it, changes what the repository is asking to run. Nothing else about the permission set is. git checks a file out
// at whatever the machine's umask says and an editor or `tar` writes one at whatever its writer chose, so the
// group and other bits, and even the owner's write bit, are facts about the machine rather than about the content
// a person read. Hashing them made two worktrees of one commit, holding one byte-for-byte script, hash apart — and
// each difference is a position somebody has to re-record.
//
// The empty tree hashes to "" rather than to the digest of nothing, so an absent directory and an empty one
// are both "nothing to trust" and neither needs accepting.
func TreeHash(workTree string) (string, []string, error) {
	root := TrackedRootDir(workTree)
	var files []string
	for _, point := range allPoints {
		paths, err := hookFiles(root, point)
		if err != nil {
			return "", nil, err
		}
		for _, path := range paths {
			info, err := os.Stat(path)
			if errors.Is(err, os.ErrNotExist) {
				// Gone between listing and reading. Nothing can run it, so it belongs to neither the run nor the
				// acceptance, and a transient is not a reason to refuse a hand-off.
				continue
			}
			if err != nil {
				return "", nil, fmt.Errorf("reading %s: %w", path, err)
			}
			data, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return "", nil, fmt.Errorf("reading %s: %w", path, err)
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return "", nil, err
			}
			content := sha256.Sum256(data)
			executable := "0"
			if info.Mode()&0o100 != 0 {
				executable = "1"
			}
			files = append(files, fmt.Sprintf("%s\t%s\t%s", filepath.ToSlash(rel), executable, hex.EncodeToString(content[:])))
		}
	}
	if len(files) == 0 {
		return "", nil, nil
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		h.Write([]byte(f))
		h.Write([]byte{0})
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, strings.SplitN(f, "\t", 2)[0])
	}
	return hex.EncodeToString(h.Sum(nil)[:16]), names, nil
}

// TrustState names a clone's relation to the tracked hooks tree. One boolean could not carry it, because
// `trusted: false` covers two facts a reader acts on differently. A clone that has never accepted anything is
// a person who has not looked yet. A clone whose recorded hash no longer matches is a person the repository
// has moved under: the team shipped a new version of the check, and the choice is now to read it and accept
// it, or to work without it. Those two need different sentences, because the second is the moment a hook
// starts silently not running for everybody who pulled.
//
// `declined` was added for the case those two still conflate: a person who looked, read the scripts, and
// chose not to run them here. Before it, that choice and "nobody has looked" were the same observable fact, so
// a report could not tell a team that opted out from a team that never heard — and a gate could not ask for a
// position without also asking people to accept scripts they do not want.
//
// The state the person reaches for in the moved-under case — keep running the version they read — is available
// today by copying that content into the per-clone root, which needs no acceptance. The tool does not do the
// copying, and does not pretend the option is missing.
type TrustState string

const (
	// TrustAccepted is the recorded content and the current content agreeing, including the case where
	// there is nothing to accept.
	TrustAccepted TrustState = "accepted"
	// TrustDeclined is content this clone's owner read and chose not to run. It is a position on this
	// hash, so it says nothing about the next version the repository ships.
	TrustDeclined TrustState = "declined"
	// TrustChanged is a recorded acceptance the tracked tree no longer matches.
	TrustChanged TrustState = "changed"
	// TrustUnrecorded is a clone where nobody has accepted or declined anything about the current
	// content. A decline that no longer matches the tree lands here, because a position on last month's
	// script is not a position on this one.
	TrustUnrecorded TrustState = "unrecorded"
)

// Trust reports the clone's state beside both hashes, so a report can name what changed rather than only that
// something did. The tree's hash comes from the same computation that decides acceptance, so the two values an
// answer prints are comparable to the one `git pair hooks --trust` printed when the person approved it.
func Trust(ctx context.Context, repo *git.Repo) (TrustState, string, string, error) {
	gitDir, err := repo.GitDir(ctx)
	if err != nil {
		return TrustUnrecorded, "", "", err
	}
	return TrustAt(RecordDir(gitDir), repo.Dir)
}

// Trusted reports whether the tracked hooks tree matches what was accepted. A tree that does not exist is
// trusted by vacuity: there is nothing there to run.
func Trusted(ctx context.Context, repo *git.Repo) (bool, error) {
	state, _, _, err := Trust(ctx, repo)
	return state == TrustAccepted, err
}

// TrustAt is Trust for a caller that already knows where the records live. The state itself is read from
// the filesystem — the tree hash and the two records beside it — so the directory is the only git question in
// it, and a run that asks it once can answer this for every hook point it walks.
//
// The two records are asked in one order, and the order is the safety property. An acceptance of the current
// content wins, because running the checks the repository asked for is the answer the gate is looking for. A
// decline of the current content is next. A stale acceptance outranks a decline of anything, because "the tree
// moved under a person who approved the old version" is the fact a report must not bury under a choice they
// made about different content. Anything else is unrecorded.
//
// Membership decides all four of those, not recency: a record that holds the version of the suite a worktree is
// standing on answers for it, and the versions beside it answer for nothing. Recency only decides which hash
// gets printed, so the sentence beside `changed` can say what the clone last recorded.
func TrustAt(recordDir, workTree string) (TrustState, string, string, error) {
	current, _, err := TreeHash(workTree)
	if err != nil {
		return TrustUnrecorded, "", "", err
	}
	if current == "" {
		return TrustAccepted, "", "", nil
	}
	accepted, err := readPositions(recordDir, trustedRecordName)
	if err != nil {
		return TrustUnrecorded, "", current, err
	}
	declined, err := readPositions(recordDir, declinedRecordName)
	if err != nil {
		return TrustUnrecorded, "", current, err
	}
	switch {
	case containsHash(accepted, current):
		return TrustAccepted, newest(accepted), current, nil
	case containsHash(declined, current):
		return TrustDeclined, newest(accepted), current, nil
	case len(accepted) > 0:
		return TrustChanged, newest(accepted), current, nil
	default:
		return TrustUnrecorded, "", current, nil
	}
}

// AcceptedAt is Accepted with the record directory already resolved. It reports the newest accepted hash, or ""
// when nobody has accepted anything: the set is what the state machine consults, and by the time a caller wants a
// value to print, membership has already been answered. Printing the newest rather than an arbitrary entry is what
// makes "accepted 3f1a…, the tree is now 82b0…" the same number the person saw when they approved it.
func AcceptedAt(recordDir string) (string, error) {
	list, err := readPositions(recordDir, trustedRecordName)
	if err != nil {
		return "", err
	}
	return newest(list), nil
}

// Accepted reports the newest hash this clone has accepted, or "" when nobody has accepted anything.
func Accepted(ctx context.Context, repo *git.Repo) (string, error) {
	gitDir, err := repo.GitDir(ctx)
	if err != nil {
		return "", err
	}
	return AcceptedAt(RecordDir(gitDir))
}

// DeclinedAt is Declined with the record directory already resolved. An absent file and an empty one both mean no
// decline has been recorded, which is the same answer a reader needs.
func DeclinedAt(recordDir string) (string, error) {
	list, err := readPositions(recordDir, declinedRecordName)
	if err != nil {
		return "", err
	}
	return newest(list), nil
}

// Declined reports the recorded declined hash, or "" when nobody has declined anything. It is reported even
// when it no longer matches the tree: "declined at 3f1a…, the tree is now 82b0…" is the sentence that tells a
// person the script they refused has a new version sitting unrecorded behind it.
func Declined(ctx context.Context, repo *git.Repo) (string, error) {
	gitDir, err := repo.GitDir(ctx)
	if err != nil {
		return "", err
	}
	return DeclinedAt(RecordDir(gitDir))
}

// Accept records the current tracked hooks tree as approved and returns the hash and the file names that
// were approved. The names come back so the caller can print what it is asking the person to consent to: a
// consent step nobody can read is a formality, and a formality is how people end up running a stranger's
// script.
func Accept(ctx context.Context, repo *git.Repo) (string, []string, error) {
	hash, names, err := TreeHash(repo.Dir)
	if err != nil {
		return "", nil, err
	}
	if hash == "" {
		return "", nil, fmt.Errorf("%s holds no hooks to accept", TrackedRootDir(repo.Dir))
	}
	gitDir, err := repo.GitDir(ctx)
	if err != nil {
		return "", nil, err
	}
	if err := recordPosition(RecordDir(gitDir), trustedRecordName, declinedRecordName, hash); err != nil {
		return "", nil, err
	}
	return hash, names, nil
}

// Decline records the current tracked hooks tree as content this clone's owner read and chose not to run, and
// returns the hash and the names it covers — the same list `Accept` prints, for the same reason: a position
// taken about content nobody can see is not a position, it is a reflex.
//
// Declining runs nothing, exactly as an unrecorded tree does. What it buys is a difference a report can state:
// the gate can ask for a position without pushing a person into accepting scripts they do not want, and the
// next version of a declined script comes back as unrecorded rather than quietly inheriting the decline.
func Decline(ctx context.Context, repo *git.Repo) (string, []string, error) {
	hash, names, err := TreeHash(repo.Dir)
	if err != nil {
		return "", nil, err
	}
	if hash == "" {
		return "", nil, fmt.Errorf("%s holds no hooks to decline", TrackedRootDir(repo.Dir))
	}
	gitDir, err := repo.GitDir(ctx)
	if err != nil {
		return "", nil, err
	}
	if err := recordPosition(RecordDir(gitDir), declinedRecordName, trustedRecordName, hash); err != nil {
		return "", nil, err
	}
	return hash, names, nil
}

// recordPosition adds one hash to a position record and removes that hash from the other, both in the directory
// the clone shares.
//
// The two positions still cancel each other — accepting content you had declined, or declining content you had
// accepted, is one person changing their mind about the same bytes — but they cancel *that hash*, not the whole
// file. Under a single-slot record the difference was invisible, because writing the current hash necessarily
// cleared whatever older answer sat beside it. Under a set, clearing the file would erase positions about content
// nobody is standing on, which is the difference between "this clone read four versions of the suite" and "this
// clone read one". The safety property the state machine needs is untouched either way: an entry answers for the
// content it names and for no other, so an acceptance cannot stretch over a version nobody read and a decline
// cannot inherit across versions — and clearing the file would let one worktree's decline switch off, clone-wide,
// a check somebody else had accepted.
func recordPosition(recordDir, name, other, hash string) error {
	list, err := readPositions(recordDir, name)
	if err != nil {
		return err
	}
	if !containsHash(list, hash) {
		list = append(list, hash)
	}
	if len(list) > maxRecordedPositions {
		list = list[len(list)-maxRecordedPositions:]
	}
	path := recordPath(recordDir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(list, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	others, err := readPositions(recordDir, other)
	if err != nil {
		return err
	}
	var kept []string
	for _, h := range others {
		if h != hash {
			kept = append(kept, h)
		}
	}
	if len(kept) == len(others) {
		return nil
	}
	otherPath := recordPath(recordDir, other)
	if len(kept) == 0 {
		if err := os.Remove(otherPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("clearing %s: %w", otherPath, err)
		}
		return nil
	}
	if err := os.WriteFile(otherPath, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", otherPath, err)
	}
	return nil
}
