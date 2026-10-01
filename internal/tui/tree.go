package tui

import (
	"slices"
	"strings"
)

// A changeset that touches twelve files in four packages is twelve rows of path prefix when it
// is a flat list, and the prefix is the least interesting part of every one of them. Laying the
// same list out as a directory tree is what makes two things possible: folding away the part of
// the changeset you are not reading, and marking a whole package reviewed in one keystroke.
//
// The tree is a *view* over the session's flat file list, not a second copy of review state. A
// directory is a path prefix; marks stay on the files, where internal/reviewmark already keeps
// them. So folding a directory, marking it, quitting, and coming back an hour later all resolve
// to the same per-file answers the store holds — and a directory that no one ever marked needs
// no record of its own.

// dirSep separates path components in the repository-relative paths git reports. It doubles as
// what a directory path ends with: that trailing separator is what makes a prefix test say
// `internal/tui/` covers internal/tui/git.go and not internal/tuition/git.go, what git takes as a
// pathspec for the whole subtree, and what tells a directory from a file at a glance — in the row,
// in the preview header, and in the status line.
const dirSep = "/"

// treeRow is one line of the file tree.
type treeRow struct {
	path string // repository-relative; a directory's ends in dirSep
	name string // what the row prints: the base name, or the folded run for a directory
	// depth is how far the row sits under the top of the tree: 0 for the files and directories at
	// the top, and one deeper than the directory row that holds them for everything else. A
	// directory's children therefore land in the column its own mark gutter starts at.
	depth int
	dir   bool
	// file is the index into Session.Files() for a file row, -1 for a directory.
	file int
	// total and marked count the files under a directory. They count them whether or not they
	// are on screen: a folded directory still has to say what it stands in for, and that is
	// the number `Space` is about to set.
	total, marked int
	// dirty says this row's subject has an uncommitted change in the working tree: the file itself for
	// a file row, and something under the directory for a directory row — but only while the directory
	// is folded. Unfolded, the rows it would stand in for are on screen saying it themselves, and a mark
	// repeated down a subtree is one more thing to look past to find the row that means it.
	dirty bool
}

// branch is a directory while the tree is being built: its subdirectories by name, and the
// files inside it as indexes into the session's list.
type branch struct {
	subs  map[string]*branch
	files []int
}

// flattenTree lays files out as a directory tree, holding back the children of every directory
// named in folded. Files keep the order their directory sorts them into; directories come before
// the files they sit beside, so the foldable rows of a level are together.
func flattenTree(files []File, folded map[string]bool) []treeRow {
	root := newBranch()
	for i, f := range files {
		dir := root
		parts := strings.Split(f.Path, dirSep)
		// Everything but the last component is a directory: the file's own name is what the row
		// prints, not a place a row can be filed under.
		for _, name := range parts[:len(parts)-1] {
			dir = dir.sub(name)
		}
		dir.files = append(dir.files, i)
	}
	out := make([]treeRow, 0, len(files))
	// The repository root is not a row: the top of the tree is the top of the changeset.
	root.walkChildren("", 0, files, folded, &out)
	return out
}

func newBranch() *branch { return &branch{subs: map[string]*branch{}} }

func (b *branch) sub(name string) *branch {
	if s, ok := b.subs[name]; ok {
		return s
	}
	s := newBranch()
	b.subs[name] = s
	return s
}

// walkChildren emits the rows for the contents of b, which is standing at prefix: its directories
// first, then the files beside them, all at depth. That is one deeper than the row of b itself, and
// 0 at the top of the tree, where the directory holding the rows is the repository and is not a row.
func (b *branch) walkChildren(prefix string, depth int, files []File, folded map[string]bool, out *[]treeRow) {
	names := make([]string, 0, len(b.subs))
	for name := range b.subs {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		sub, subPrefix, label := fold(b.subs[name], prefix+name+dirSep, name+dirSep)
		total, marked, dirty := sub.count(files)
		*out = append(*out, treeRow{
			path: subPrefix, name: label, depth: depth, dir: true, file: -1,
			total: total, marked: marked,
			// Only a folded directory wears this. Open, its own children are the answer.
			dirty: folded[subPrefix] && dirty > 0,
		})
		if folded[subPrefix] {
			continue
		}
		sub.walkChildren(subPrefix, depth+1, files, folded, out)
	}

	sorted := slices.Clone(b.files)
	// The paths in a branch all start with that branch's own prefix, so comparing them is
	// comparing the names the rows print, in the order git's own listing puts them.
	slices.SortFunc(sorted, func(a, b int) int {
		return strings.Compare(files[a].Path, files[b].Path)
	})
	for _, idx := range sorted {
		*out = append(*out, treeRow{
			path: files[idx].Path, name: baseName(files[idx].Path),
			depth: depth, file: idx, dirty: files[idx].Dirty,
		})
	}
}

// fold walks a directory into its only child for as long as it has one child and nothing else,
// so `docs/plans/active/` is one row rather than three rows of single-child directories. It stops
// at the first directory that holds anything besides one directory, because that is where the
// tree actually branches and where a reviewer would want to fold.
//
// The folded row is named for the deepest directory it reaches, which is also the prefix its
// marks and its pathspec are taken from: every directory in the run holds exactly the same files,
// so any of them names the same subtree.
func fold(b *branch, prefix, label string) (*branch, string, string) {
	for len(b.subs) == 1 && len(b.files) == 0 {
		for name, sub := range b.subs {
			b, prefix, label = sub, prefix+name+dirSep, label+name+dirSep
		}
	}
	return b, prefix, label
}

// count is how many files sit under b, how many of them are reviewed, and how many carry an
// uncommitted change — counting every file in the subtree whether or not any of it is on screen. The
// third number is what lets a folded directory say that something inside it has been written to.
func (b *branch) count(files []File) (total, marked, dirty int) {
	for _, idx := range b.files {
		total++
		if files[idx].Reviewed {
			marked++
		}
		if files[idx].Dirty {
			dirty++
		}
	}
	for _, sub := range b.subs {
		t, m, d := sub.count(files)
		total += t
		marked += m
		dirty += d
	}
	return total, marked, dirty
}

// treeDirs is every directory the files sit under, each named with its trailing separator, sorted
// — and a chain of them sorts shallowest first, because a prefix sorts before what it extends.
// Unlike the rows of a folded tree these are every real directory rather than the ones a row was
// printed for, which is what `c` needs: folding the whole tree means folding the directories a
// folded run hid as well, so that unfolding all of it has nothing left to reveal.
func treeDirs(files []File) []string {
	seen := map[string]bool{}
	var dirs []string
	for _, f := range files {
		for i := 0; i < len(f.Path); i++ {
			if f.Path[i] != '/' {
				continue
			}
			prefix := f.Path[:i+1]
			if seen[prefix] {
				break // every shorter prefix of this path is already in the list
			}
			seen[prefix] = true
			dirs = append(dirs, prefix)
		}
	}
	slices.Sort(dirs)
	return dirs
}

// ancestorsOf is dir and every directory above it, shallowest first: `a/`, `a/b/`, `a/b/c/`.
// Unfolding a directory has to unfold these too, because the row a reviewer is looking at may be
// a folded run whose outer directories are named by keys of their own.
func ancestorsOf(dir string) []string {
	var out []string
	for i := 0; i < len(dir); i++ {
		if dir[i] == '/' {
			out = append(out, dir[:i+1])
		}
	}
	return out
}

// isUnder reports whether path sits inside dir, which is given with its trailing separator. A
// directory is not inside itself — the rows above it are what `h` is looking for when the cursor is
// already on one. The separator is the whole comparison: without it `internal/tui/` would also cover
// `internal/tuition/`.
func isUnder(path, dir string) bool {
	return path != dir && strings.HasPrefix(path, dir)
}

// baseName is the last component of a repository-relative path.
func baseName(path string) string {
	if i := strings.LastIndex(path, dirSep); i >= 0 {
		return path[i+len(dirSep):]
	}
	return path
}
