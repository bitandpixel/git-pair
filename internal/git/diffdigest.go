package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// DiffRawDigest is the identity of what a diff changes, independent of how the diff is rendered.
//
// It hashes the raw name-status description of the diff — modes, pre- and post-image blob OIDs,
// statuses and paths — so two invocations score equal exactly when they change the same content, and
// no user configuration moves the answer. That independence is the reason to hash `--raw` rather than
// patch text. A digest of the patch is a digest of a rendering: `git patch-id --stable` on one commit
// produced three different values under the default, `-U10` and `-U1`, because the context lines the
// diff happens to print are part of the bytes it hashes, and `diff.context` in a reviewer's
// `~/.gitconfig` would then decide what an approval recorded. `--raw` reads the object database, so
// context width, whitespace options and the diff algorithm are not in its output at all — measured
// identical across `-U3`, `-U10`, `-U1`, `--ignore-all-space` and `--diff-algorithm=patience`.
//
// Two settings do reach the raw form, and both are pinned on the command line where a user's config
// cannot override them:
//
//   - `--no-renames`, because rename detection decides whether a move is one `R` entry or a `D` plus
//     an `A` — a shape difference, not a content one, and so not something a recorded value should
//     inherit from whoever computed it first.
//   - `--no-ext-diff` and `--no-textconv`, because an external diff driver or a textconv filter has no
//     business answering a plumbing question. They write patch text, which `--raw` does not print, so
//     this is insurance rather than a fix.
//
// `core.quotePath=false` is set for every invocation by `spawn`, which is why paths cannot be quoted
// two ways here.
//
// Each `exclude` directory is dropped from the diff before hashing, as a pathspec of its own. The
// caller passes the changeset's own directory: the review record lives in it, so a reply to a review
// thread would otherwise change the digest and invalidate the approval it is written into.
//
// An empty diff hashes the empty output, which is the right answer for a changeset that contributes no
// content at all — a documentation-only changeset has nothing for a reviewer to have missed, and two
// of them are correctly identical.
//
// A revision this clone cannot read is an error, not an empty digest: a caller that recorded a
// fabricated value would be reporting a comparison it never made (PRD §21).
//
// `DiffIdentity` measures the same half from its own richer invocation, so nothing calls this in the
// product any more. It stays, and stays a separate implementation, because the invariant the compound
// value depends on is that the new invocation reproduces this one's bytes: one function comparing with
// itself would pin nothing. `TestDiffIdentityRawHalfIsTheShippedDigest` holds the two against each other.
func (r *Repo) DiffRawDigest(ctx context.Context, from, to string, exclude ...string) (string, error) {
	out, err := r.diff(ctx, false, from, to, exclude...)
	if err != nil {
		return "", err
	}
	return hashHex(out), nil
}

// DiffIdentity is the pair of values a review records about the diff it read. One `git diff` answers both
// halves, so they cannot disagree about the ground, the exclusions or the moment they were taken.
type DiffIdentity struct {
	// Raw is the value `DiffRawDigest` computes: the identity of the content, and the value version 1 of
	// `Review-Diff-Id` carried alone. It is the primary claim, and the one that falls back to when the
	// other half cannot be interpreted.
	Raw string
	// Patch is the identity of the diff *as rendered*: what changed, and where. It is what keeps an
	// approval standing when trunk edits a file the branch also edits on a line nowhere near the branch's
	// own, and what refuses when trunk edits a line inside the three lines of context the reviewer read.
	//
	// Empty means the half was not computed rather than that it differs: an older git without
	// `patch-id --verbatim`, a git that could not be run, or a diff with no patch section at all. An
	// absent half costs the relaxation and nothing else — the gate keeps asking the raw question.
	Patch string
}

// DiffIdentity measures both halves from one invocation.
//
// `git diff --raw … -U3` prints the raw name-status records and then the patch. The two sections are split
// at the first `diff --git` line, which each record is NUL-terminated ahead of, and hashed separately: the
// raw half is hashed over the same bytes `DiffRawDigest` sees, which is what lets a value written by this
// build be compared with one written by the version that recorded only that half.
//
// The patch half goes through `git patch-id --verbatim`, not `--stable`: on the git measured here, `--stable`
// and `--unstable` scored a Makefile recipe line indented with a tab the same as the same line indented with
// spaces, and `make` does not. `--verbatim` keeps the hunk's line numbers out of the value, which is the
// insensitivity this half exists for.
//
// The flags on the diff are the ones `DiffRawDigest` explains, plus `-U3` — pinned rather than inherited,
// because the patch half is the context the reviewer read and a reader's `diff.context` must not decide what
// an approval recorded. Measured: the raw half is the same bytes at `-U1`, `-U3`, `-U10` and `-U99`; only the
// patch half moves.
func (r *Repo) DiffIdentity(ctx context.Context, from, to string, exclude ...string) (DiffIdentity, error) {
	out, err := r.diff(ctx, true, from, to, exclude...)
	if err != nil {
		return DiffIdentity{}, err
	}
	raw, patch := splitRawAndPatch(out)
	id := DiffIdentity{Raw: hashHex(raw)}
	if strings.TrimSpace(patch) == "" {
		return id, nil
	}
	if pid, err := r.patchID(ctx, patch); err == nil {
		id.Patch = pid
	}
	// A failure here is silence, not a refusal: the half is absent, and an absent half cannot be read as a
	// content difference (§21). The raw half still answers the question on its own.
	return id, nil
}

// diff is the one place the two measurements build their invocation, so the pair cannot drift apart.
func (r *Repo) diff(ctx context.Context, withPatch bool, from, to string, exclude ...string) (string, error) {
	args := []string{"diff", "--raw", "-z", "--no-renames", "--no-ext-diff", "--no-textconv"}
	if withPatch {
		// `-U3` is the context the reviewer read, pinned rather than inherited; `--default-prefix` is the
		// `a/` and `b/` on the file headers, which `patch-id` reads and a reader's `diff.noprefix` would
		// otherwise move.
		args = append(args, "-U3", "--default-prefix")
	}
	args = append(args, from, to, "--", ".")
	for _, dir := range exclude {
		args = append(args, ":(exclude)"+filepath.ToSlash(dir)+"/")
	}
	return r.Git(ctx, args...)
}

// splitRawAndPatch cuts git's combined output at the first patch header. Each raw record is NUL-terminated,
// and git puts one further NUL between the last record and the patch, so the raw section is everything
// before that separator: exactly the bytes the same diff prints without `-U3`, which is what lets a value
// written by this build be compared with one written by the version that recorded only that half.
func splitRawAndPatch(out string) (raw, patch string) {
	const marker = "\x00diff --git "
	i := strings.Index(out, marker)
	if i < 0 {
		return out, ""
	}
	return out[:i], out[i+len("\x00"):]
}

// patchID asks git for the identity of a rendered diff. Only the first field of its answer is kept: the
// second is the commit-ish, which git does not know for a piped patch.
func (r *Repo) patchID(ctx context.Context, patch string) (string, error) {
	out, err := r.GitStdin(ctx, patch, "patch-id", "--verbatim")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", &Error{ExitCode: 0, Stderr: "patch-id answered nothing"}
	}
	return fields[0], nil
}

func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
