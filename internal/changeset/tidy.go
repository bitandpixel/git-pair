package changeset

import (
	"context"
	"path/filepath"

	"gitpair/internal/git"
)

// The landed namespace.
//
// `changesets/.landed/<id>/` is where a landed changeset's directory goes when somebody tidies
// clutter out of the way. It stays under `changesets/` rather than beside it so a landing keeps its
// statement in one tree: both spellings are read from the same `ls-tree` of the same branch, and
// moving a directory between them is a `git mv` with no second place to update.
//
// The name is hidden because it is the one child of `changesets/` that is not a changeset. Four
// readers have to know that, and they all ask here:
//
//   - `ActiveIDs` does not list it,
//   - `changesetDir` does not name it,
//   - `worktreeDirs` skips it,
//   - `ValidateID` refuses it, so `git pair init --id .landed` cannot overwrite the namespace.
//
// Anything new that lists the children of `changesets/` has to answer the same question. `Reserved`
// is the way to answer it.
const LandedDir = ".landed"

// Reserved reports a name that lives under `changesets/` without being a changeset.
//
// The reason this is a function and not a comment is the listing `DirsAt` used to do: it returned
// every immediate child directory of `changesets/` with no dotfile exclusion, so the landed
// namespace became a changeset id named `.landed`, and the resolver then failed the whole branch
// over the `CHANGESET.yaml` that namespace does not have. One trap, four readers, one answer.
func Reserved(name string) bool {
	return name == LandedDir
}

// withoutReserved drops the reserved names from a listing, keeping the order. It filters in place
// because every caller passes a listing it just built, and it leaves an empty listing empty rather
// than making it a non-nil empty slice — the distinction the callers already report as "none".
func withoutReserved(names []string) []string {
	out := names[:0]
	for _, name := range names {
		if !Reserved(name) {
			out = append(out, name)
		}
	}
	return out
}

// ActiveDirPath is where a changeset's directory sits while the work is in progress.
func ActiveDirPath(id string) string {
	return filepath.Join(Root, id)
}

// LandedDirPath is where a changeset's directory sits once it has been tidied.
//
// The metadata keeps the same filenames, so a reader that takes a path can take either spelling —
// which is exactly why a walk over history asks for both (DirPathspecs) instead of picking one.
func LandedDirPath(id string) string {
	return filepath.Join(Root, LandedDir, id)
}

// DirAt reports the path one changeset's directory has in a revision's tree, and whether it is there at
// all: `changesets/<id>/`, or `changesets/.landed/<id>/` once it has been tidied. Every reader that takes
// a slug and a revision asks this before it reads a file, because a reader that hard-codes the active
// spelling stops finding a changeset the moment somebody tidies it.
func DirAt(ctx context.Context, repo *git.Repo, rev, id string) (string, bool) {
	if present, moved := CarriesDir(ctx, repo, rev, id); present {
		if moved {
			return LandedDirPath(id), true
		}
		return ActiveDirPath(id), true
	}
	return "", false
}

// DirPathspecs returns both spellings of one changeset's directory, active first, each with the
// trailing slash that makes it mean "everything under here" to `git log` and `git diff`.
//
// Neither spelling alone names the work. A walk that takes only the first misses every changeset
// tidied out of the way; one that takes only the second misses every changeset still in place, which
// is all of them before tidying exists; and the tidying move itself puts the original path in the
// history of a directory that now lives at the other. So a landed-chain walk, a `git log --` over one
// changeset, and the tidying move all need this pair rather than a guess at where the directory is
// now.
func DirPathspecs(id string) []string {
	return []string{ActiveDirPath(id) + "/", LandedDirPath(id) + "/"}
}
