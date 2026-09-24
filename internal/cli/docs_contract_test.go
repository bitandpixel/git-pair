package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The spec, the README, and the agent skill name commands and ref paths. All three are prose, and prose
// is what rotted last time: a document beside the code that describes `refs/reviews/archive/<slug>/<sha>`
// and a `review close` nobody implemented is worse than no document, because it is read as a promise.
//
// The skill is in here because it is the document an agent actually reads, and because the one that
// rotted lived in a sibling repository — where nothing could see that the command it named had been
// removed. A skill shipped with the tool is a surface of the tool, and gets the same check as `--help`.
//
// This is the cheapest thing that keeps the prose and the code from drifting apart. It does not check that
// the prose is *accurate* about what a command does — no test can — only that every name it uses is a name
// this code answers to.

// docFiles returns the documents that speak in command names and ref paths: the spec, the README, and
// every page of the agent skill. The skill is in here because it is the document an agent actually
// reads, and it is the one that rotted last: a `review close` and a `refs/reviews/*` namespace that no
// version of this code wrote, in prose beside a different repository.
func docFiles(t *testing.T) []string {
	return append([]string{"../../PRD.md", "../../README.md"}, skillDocs(t)...)
}

// completeDocs returns the documents that intend to name *every* command. The forward check above runs
// over all of the prose; this one runs only where a document claims to be a catalogue, because the
// requirement it enforces — no command may leave the documents — is a property of a reference, not of a
// short contract an agent reads on the way to a command.
func completeDocs(t *testing.T) []string {
	return []string{"../../PRD.md", "../../README.md", "../../skills/git-pair/references/cli.md"}
}

// skillDocs walks the skill directory, so a new reference page is covered without anyone remembering to
// add it. Finding no markdown there is a failure rather than a silent loss of coverage: a renamed or
// moved skill is exactly how a checked document stops being checked.
func skillDocs(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir("../../skills", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk ../../skills: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no markdown under ../../skills: the agent skill is a checked document and it is gone")
	}
	return files
}

func TestCommandsNamedInTheDocsExist(t *testing.T) {
	paths := commandPaths(newRootCommand(&app{}))
	// `git pair` itself, and the help cobra adds, are named in prose too.
	paths["help"] = true

	command := regexp.MustCompile(`git pair ([a-z][a-z-]*)(?: ([a-z][a-z-]*))?`)
	for _, file := range docFiles(t) {
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

// TestEveryCommandIsNamedInTheDocs is the other direction of the check above. That one stops the
// documents promising a command the code does not have. This one stops a command leaving the documents.
// `review reopen` is the reason: it shipped, it is in README's quickstart and command table, and the
// spec's own command tree never listed it, because nothing looked for a name the code had and the prose
// had dropped.
func TestEveryCommandIsNamedInTheDocs(t *testing.T) {
	paths := commandPaths(newRootCommand(&app{}))
	for _, file := range completeDocs(t) {
		text := readDoc(t, file)
		for path := range paths {
			// A group is documented by the commands under it, so only a leaf has to appear by name.
			if path == "" || hasChild(paths, path) {
				continue
			}
			// README's command table names a command bare and in backticks; the prose and the PRD use
			// the `git pair` form. Either counts as naming it.
			if !strings.Contains(text, "git pair "+path) && !strings.Contains(text, "`"+path+"`") {
				t.Errorf("%s never names `git pair %s`", filepath.Base(file), path)
			}
		}
	}
}

// TestRefPathsInTheDocsAreOnesWeWrite checks the durable namespace, where drift is expensive: a
// document naming a ref family the code never writes teaches a reader to fetch nothing.
func TestRefPathsInTheDocsAreOnesWeWrite(t *testing.T) {
	prefixes := []string{"refs/git-pair/archive/", "refs/git-pair/integrations/",
		// Named in the migration prose, and named truthfully: `List` reports these retired paths so a
		// clone holding only them is not called unfetched, and nothing reads either one as a record.
		"refs/git-pair/changesets/"}
	path := regexp.MustCompile(`refs/git-pair/[A-Za-z0-9_.<>*-]+(?:/[A-Za-z0-9_.<>*-]+)*`)
	for _, file := range docFiles(t) {
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

// TestSkillFrontmatterIsLoadable checks what a harness reads before it reads anything else. A skill
// directory whose SKILL.md has no description, or none that parses, is not loaded and only warns — so the
// skill an agent was supposed to have is quietly not there. The name is checked against the directory
// because the Agent Skills standard requires them to agree, and a harness that matches on the name and
// opens the directory by path would otherwise read two different things.
func TestSkillFrontmatterIsLoadable(t *testing.T) {
	for _, page := range skillDocs(t) {
		if filepath.Base(page) != "SKILL.md" {
			continue
		}
		text := readDoc(t, page)
		fm := frontmatter(t, page, text)
		dir := filepath.Base(filepath.Dir(page))
		name := fm["name"]
		if name != dir {
			t.Errorf("%s: name %q, want %q — the standard requires them to agree", page, name, dir)
		}
		if !skillName.MatchString(name) || len(name) > 64 {
			t.Errorf("%s: name %q is not a valid skill name", page, name)
		}
		desc := fm["description"]
		if desc == "" {
			t.Errorf("%s: no description — a skill without one is not loaded", page)
		}
		if len(desc) > 1024 {
			t.Errorf("%s: description is %d characters, and the standard caps it at 1024", page, len(desc))
		}
	}
}

var skillName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// frontmatter returns the leading YAML block of a skill page as flat key/value pairs. It parses only what
// the spec requires of a skill page — one-line scalars — which is what this check needs.
func frontmatter(t *testing.T, page, text string) map[string]string {
	t.Helper()
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		t.Fatalf("%s: SKILL.md must open with a `---` frontmatter block", page)
	}
	out := map[string]string{}
	for i, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			return out
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("%s: frontmatter line %d is not `key: value`: %q", page, i+2, line)
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	t.Fatalf("%s: frontmatter block is never closed", page)
	return nil
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
