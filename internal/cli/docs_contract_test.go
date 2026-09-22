package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The spec and the README name commands and ref paths. Both are prose, and prose is what rotted
// last time: a document beside the code that describes `refs/reviews/archive/<slug>/<sha>` and a
// `review close` nobody implemented is worse than no document, because it is read as a promise.
//
// This is the cheapest thing that keeps the two from drifting apart. It does not check that the
// prose is *accurate* about what a command does — no test can — only that every name it uses is a
// name this code answers to.

// docFiles are the two documents that speak in command names and ref paths.
var docFiles = []string{"../../PRD.md", "../../README.md"}

func TestCommandsNamedInTheDocsExist(t *testing.T) {
	paths := commandPaths(newRootCommand(&app{}))
	// `git pair` itself, and the help cobra adds, are named in prose too.
	paths["help"] = true

	command := regexp.MustCompile(`git pair ([a-z][a-z-]*)(?: ([a-z][a-z-]*))?`)
	for _, file := range docFiles {
		text := readDoc(t, file)
		for _, m := range command.FindAllStringSubmatch(text, -1) {
			name := m[1]
			if m[2] != "" {
				name += " " + m[2]
			}
			// A token that is not a command word is prose running on ("git pair check` failing"),
			// so only a two-word match whose second word is unknown is a possible drift — and only
			// when the single word is a real command with subcommands of its own.
			if !paths[name] {
				if strings.Contains(name, " ") && paths[m[1]] && hasChild(paths, m[1]) {
					t.Errorf("%s names `git pair %s`, which is not a command", filepath.Base(file), name)
				}
				continue
			}
		}
	}
}

// TestRefPathsInTheDocsAreOnesWeWrite checks the durable namespace, where drift is expensive: a
// document naming a ref family the code never writes teaches a reader to fetch nothing.
func TestRefPathsInTheDocsAreOnesWeWrite(t *testing.T) {
	prefixes := []string{"refs/git-pair/archive/", "refs/git-pair/integrations/",
		// Named in the migration prose, and named truthfully: `List` still reports these retired
		// paths, and a legacy integration ref still counts as the record it is.
		"refs/git-pair/changesets/"}
	path := regexp.MustCompile(`refs/git-pair/[A-Za-z0-9_.<>*-]+(?:/[A-Za-z0-9_.<>*-]+)*`)
	for _, file := range docFiles {
	text:
		for _, m := range path.FindAllString(readDoc(t, file), -1) {
			switch m {
			case "refs/git-pair", "refs/git-pair/*", "refs/git-pair/":
				continue
			}
			for _, p := range prefixes {
				if strings.HasPrefix(m, p) {
					continue text
				}
			}
			t.Errorf("%s names %s, which is not a ref family git-pair writes", filepath.Base(file), m)
		}
	}
}

// hasChild reports whether a command has subcommands, which is what makes a second word worth
// checking: "git pair diff for a reviewer" is prose running on, and `diff` has no children for it
// to be wrong about.
func hasChild(paths map[string]bool, parent string) bool {
	for p := range paths {
		if strings.HasPrefix(p, parent+" ") {
			return true
		}
	}
	return false
}

// commandPaths indexes every command path the root can reach, "integration record" style.
func commandPaths(root *cobra.Command) map[string]bool {
	out := map[string]bool{"": true}
	var walk func(prefix string, c *cobra.Command)
	walk = func(prefix string, c *cobra.Command) {
		for _, sub := range c.Commands() {
			name := sub.Name()
			if name == "help" || name == "completion" {
				continue
			}
			path := name
			if prefix != "" {
				path = prefix + " " + name
			}
			out[path] = true
			walk(path, sub)
		}
	}
	walk("", root)
	return out
}

func readDoc(t *testing.T, rel string) string {
	t.Helper()
	text, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(text)
}
