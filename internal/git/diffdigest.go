package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
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
func (r *Repo) DiffRawDigest(ctx context.Context, from, to string, exclude ...string) (string, error) {
	args := []string{"diff", "--raw", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", from, to, "--"}
	args = append(args, ".")
	for _, dir := range exclude {
		args = append(args, ":(exclude)"+filepath.ToSlash(dir)+"/")
	}
	out, err := r.Git(ctx, args...)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(out))
	return hex.EncodeToString(sum[:]), nil
}
