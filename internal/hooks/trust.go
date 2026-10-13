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
// The state the person reaches for in that second case — keep running the version they read — is available
// today by copying that content into the per-clone root, which needs no acceptance. The tool does not do the
// copying, and does not pretend the option is missing.
type TrustState string

const (
	// TrustAccepted is the recorded content and the current content agreeing, including the case where
	// there is nothing to accept.
	TrustAccepted TrustState = "accepted"
	// TrustChanged is a recorded acceptance the tracked tree no longer matches.
	TrustChanged TrustState = "changed"
	// TrustUnrecorded is a clone where nobody has accepted anything yet.
	TrustUnrecorded TrustState = "unrecorded"
)

// Trust reports the clone's state beside both hashes, so a report can name what changed rather than only that
// something did. The tree's hash comes from the same computation that decides acceptance, so the two values an
// answer prints are comparable to the one `git pair hooks --trust` printed when the person approved it.
func Trust(ctx context.Context, repo *git.Repo) (TrustState, string, string, error) {
	current, _, err := TreeHash(repo.Dir)
	if err != nil {
		return TrustUnrecorded, "", "", err
	}
	if current == "" {
		return TrustAccepted, "", "", nil
	}
	accepted, err := Accepted(ctx, repo)
	if err != nil {
		return TrustUnrecorded, "", current, err
	}
	switch {
	case accepted == "":
		return TrustUnrecorded, "", current, nil
	case accepted == current:
		return TrustAccepted, accepted, current, nil
	default:
		return TrustChanged, accepted, current, nil
	}
}

// Trusted reports whether the tracked hooks tree matches what was accepted. A tree that does not exist is
// trusted by vacuity: there is nothing there to run.
func Trusted(ctx context.Context, repo *git.Repo) (bool, error) {
	state, _, _, err := Trust(ctx, repo)
	return state == TrustAccepted, err
}

// Accepted reports the recorded hash, or "" when nobody has accepted anything.
func Accepted(ctx context.Context, repo *git.Repo) (string, error) {
	gitDir, err := repo.GitDir(ctx)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(trustRecordPath(gitDir))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading the hook trust record: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", nil, fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(hash+"\n"), 0o644); err != nil {
		return "", nil, fmt.Errorf("writing %s: %w", path, err)
	}
	return hash, names, nil
}
