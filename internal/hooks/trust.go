package hooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
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

// trustRecordPath is where acceptance is recorded, beside the other two git-directory stores.
func trustRecordPath(gitDir string) string {
	return filepath.Join(gitDir, "git-pair", "trusted-hooks")
}

// declinedRecordPath is where a decline is recorded, beside the acceptance.
//
// It is a separate file rather than a marker inside the acceptance record because the two answers are not
// states of one thing: they are two positions a person can hold about two different trees. A clone may have
// accepted last month's suite and decline this month's, and reading the pair is what tells a report which of
// the two the person took *for the content in front of them* — which is the whole difference between a team
// that opted out and a team that never heard.
func declinedRecordPath(gitDir string) string {
	return filepath.Join(gitDir, "git-pair", "declined-hooks")
}

// TreeHash hashes the tracked hooks tree: every regular file's path, mode, and content. The mode is in the
// hash because an executable bit is part of what a hook is — making a script executable, or removing it,
// changes what the repository is asking to run.
//
// The empty tree hashes to "" rather than to the digest of nothing, so an absent directory and an empty one
// are both "nothing to trust" and neither needs accepting.
func TreeHash(workTree string) (string, []string, error) {
	root := TrackedRootDir(workTree)
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := sha256.Sum256(data)
		files = append(files, fmt.Sprintf("%s\t%o\t%s", filepath.ToSlash(rel), info.Mode().Perm(), hex.EncodeToString(content[:])))
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("reading %s: %w", root, err)
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
	return TrustAt(gitDir, repo.Dir)
}

// Trusted reports whether the tracked hooks tree matches what was accepted. A tree that does not exist is
// trusted by vacuity: there is nothing there to run.
func Trusted(ctx context.Context, repo *git.Repo) (bool, error) {
	state, _, _, err := Trust(ctx, repo)
	return state == TrustAccepted, err
}

// TrustAt is Trust for a caller that already knows where the git directory is. The state itself is read from
// the filesystem — the tree hash and the two records beside it — so the directory is the only git question in
// it, and a run that asks it once can answer this for every hook point it walks.
//
// The two records are asked in one order, and the order is the safety property. An acceptance of the current
// content wins, because running the checks the repository asked for is the answer the gate is looking for. A
// decline of the current content is next. A stale acceptance outranks a decline of anything, because "the tree
// moved under a person who approved the old version" is the fact a report must not bury under a choice they
// made about different content. Anything else is unrecorded.
func TrustAt(gitDir, workTree string) (TrustState, string, string, error) {
	current, _, err := TreeHash(workTree)
	if err != nil {
		return TrustUnrecorded, "", "", err
	}
	if current == "" {
		return TrustAccepted, "", "", nil
	}
	accepted, err := AcceptedAt(gitDir)
	if err != nil {
		return TrustUnrecorded, "", current, err
	}
	declined, err := DeclinedAt(gitDir)
	if err != nil {
		return TrustUnrecorded, "", current, err
	}
	switch {
	case accepted == current:
		return TrustAccepted, accepted, current, nil
	case declined == current:
		return TrustDeclined, accepted, current, nil
	case accepted != "":
		return TrustChanged, accepted, current, nil
	default:
		return TrustUnrecorded, "", current, nil
	}
}

// AcceptedAt is Accepted with the directory the record lives under already resolved.
func AcceptedAt(gitDir string) (string, error) {
	data, err := os.ReadFile(trustRecordPath(gitDir))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading the hook trust record: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// Accepted reports the recorded hash, or "" when nobody has accepted anything.
func Accepted(ctx context.Context, repo *git.Repo) (string, error) {
	gitDir, err := repo.GitDir(ctx)
	if err != nil {
		return "", err
	}
	return AcceptedAt(gitDir)
}

// DeclinedAt is Declined with the directory the record lives under already resolved. An absent file and an
// empty one both mean no decline has been recorded, which is the same answer a reader needs.
func DeclinedAt(gitDir string) (string, error) {
	data, err := os.ReadFile(declinedRecordPath(gitDir))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading the hook decline record: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// Declined reports the recorded declined hash, or "" when nobody has declined anything. It is reported even
// when it no longer matches the tree: "declined at 3f1a…, the tree is now 82b0…" is the sentence that tells a
// person the script they refused has a new version sitting unrecorded behind it.
func Declined(ctx context.Context, repo *git.Repo) (string, error) {
	gitDir, err := repo.GitDir(ctx)
	if err != nil {
		return "", err
	}
	return DeclinedAt(gitDir)
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
	path := trustRecordPath(gitDir)
	if err := recordPosition(gitDir, path, declinedRecordPath(gitDir), hash); err != nil {
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
	if err := recordPosition(gitDir, declinedRecordPath(gitDir), trustRecordPath(gitDir), hash); err != nil {
		return "", nil, err
	}
	return hash, names, nil
}

// recordPosition writes one per-clone position and clears the other.
//
// They clear each other because both are answers to "what do you think of the content that is here now".
// Leaving a stale acceptance in place beside a fresh decline would let the older record answer for newer
// content — which is the one reading the state machine in TrustAt refuses to allow, and the reason a decline
// of last month's suite cannot be allowed to cover this month's.
func recordPosition(gitDir, path, other, hash string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(hash+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Remove(other); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clearing %s: %w", other, err)
	}
	return nil
}
