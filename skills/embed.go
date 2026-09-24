// Package skills holds the agent skill that ships inside git-pair.
//
// This directory is the skill and the package that embeds it at once, which is the only arrangement that
// keeps both things the skill depends on true. It has to be a folder an agent or a person finds by
// walking the repository — `skills/git-pair/`, beside `cmd/` and `internal/`, with its `SKILL.md` and
// its `references/` — and it has to be compiled into the binary, so that an installed copy can be told
// apart from the one this `git-pair` ships. `go:embed` reads from a package's own directory tree and
// cannot reach above it with `..`, which is why the embed is here rather than under `internal/`.
//
// The consequence is that `skills/` holds one Go file. Skill scanners look for `SKILL.md`, and ignore
// what is not a skill; the file that makes the copy inside the binary possible never reaches them.
package skills

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed all:git-pair
var skillFS embed.FS

// AgentsMD is the pointer stanza for a harness with no skill discovery — the text an agent reads from
// AGENTS.md or CLAUDE.md when nothing else will tell it that this repository reviews changes with
// git-pair. It lives beside the skill rather than in a Go string so it is reviewed as prose.
//
//go:embed agents-md.md
var agentsMD string

// AgentsMDPointer returns that stanza, with its trailing newline.
func AgentsMDPointer() string { return agentsMD }

// Name is the skill's directory name, and the `name` in its frontmatter. The Agent Skills standard
// requires the two to agree, and a harness that matches on one and opens the other would otherwise read
// two different things. internal/cli/docs_contract_test.go checks the frontmatter half.
const Name = "git-pair"

// Files lists every file in the skill, relative to the skill directory, sorted. This is the install
// manifest: `skill install` writes exactly these paths and owns nothing else in the target directory.
func Files() []string {
	var files []string
	err := fs.WalkDir(skillFS, Name, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, ok := strings.CutPrefix(p, Name+"/")
		if ok {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		// Unreachable for an embedded tree: the walk is over bytes already in the binary. The build
		// would have failed had the directory not been there, so an error here means a file was
		// embedded that the walker could not name, which is a bug rather than a state.
		panic(fmt.Sprintf("git-pair: cannot list the embedded skill: %v", err))
	}
	sort.Strings(files)
	return files
}

// Read returns one file of the skill, named relative to the skill directory — `SKILL.md`, or
// `references/cli.md`.
func Read(file string) ([]byte, error) {
	if !fs.ValidPath(file) {
		return nil, fmt.Errorf("%q is not a file of the %s skill", file, Name)
	}
	data, err := fs.ReadFile(skillFS, path.Join(Name, file))
	if err != nil {
		return nil, fmt.Errorf("%q is not a file of the %s skill", file, Name)
	}
	return data, nil
}
