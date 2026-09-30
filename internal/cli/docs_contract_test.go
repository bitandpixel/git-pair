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
func TestNoDocNamesADurableRef(t *testing.T) {
	// git-pair writes no refs. A document that names one teaches a reader to fetch, configure a refspec
	// for, or audit something that does not exist, and every such line in the past was a promise the tool
	// kept by writing a ref nobody needed. The bare namespace is allowed only in the prose that explains
	// what was retired; anything shaped like a ref path under it is not.
	path := regexp.MustCompile(`refs/git-pair/[A-Za-z0-9_.<>*-]+(?:/[A-Za-z0-9_.<>*-]+)*`)
	for _, file := range docFiles(t) {
		for _, m := range path.FindAllString(readDoc(t, file), -1) {
			t.Errorf("%s names %s: git-pair writes no refs, so no document should name one", filepath.Base(file), m)
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

// TestStackedExamplesShowTheWrittenPair keeps an example of a stacked changeset honest about the keys git-pair
// writes. §5 records that pair as `base:` plus `base-changeset:`; `parent:` and `parent-changeset:` are read from
// files written before that pair existed, and nothing writes them any more. Prose is allowed to name the old keys -
// someone reading a file that predates the change has to be told what they are looking at - so the check is scoped
// to fenced blocks, which is where a document *shows* a changeset file rather than talking about one.
//
// The rule is "a block that assigns an old key must also assign the new pair" rather than "no old key may appear",
// because a block that teaches the older spelling is worth keeping and would otherwise be deleted instead of
// annotated. What it stops is the quiet case: an example copied forward from before M4, which a reader would follow
// and produce a file that git-pair reads but never writes.
func TestStackedExamplesShowTheWrittenPair(t *testing.T) {
	stale := regexp.MustCompile(`(?m)^[ \t]*(?:parent|parent-changeset)[ \t]*:`)
	written := regexp.MustCompile(`(?m)^[ \t]*base-changeset[ \t]*:`)
	for _, file := range docFiles(t) {
		for _, block := range fencedBlocks(t, file, readDoc(t, file)) {
			if !stale.MatchString(block) || written.MatchString(block) {
				continue
			}
			t.Errorf("%s shows a stacked changeset with `parent:` or `parent-changeset:` and no `base-changeset:`; "+
				"the pair git-pair writes is `base:` plus `base-changeset:` (§5), so this example cannot be produced "+
				"by any command:\n%s", filepath.Base(file), block)
		}
	}
}

// fencedBlocks returns the content of each ``` fence, opening delimiter included so a failure shows what the example
// claims to be. A fence left open is reported: the blocks after it would go unchecked, and a document that quietly
// stops being checked is the failure this file exists to prevent.
func fencedBlocks(t *testing.T, file, text string) []string {
	t.Helper()
	var blocks []string
	var current []string
	in := false
	for _, line := range strings.Split(text, "\n") {
		fence := strings.HasPrefix(strings.TrimSpace(line), "```")
		switch {
		case fence && !in:
			in = true
			current = []string{line}
		case fence && in:
			current = append(current, line)
			blocks = append(blocks, strings.Join(current, "\n"))
			in = false
		case in:
			current = append(current, line)
		}
	}
	if in {
		t.Errorf("%s has an unclosed ``` fence, which would hide every example after it from this check", filepath.Base(file))
	}
	return blocks
}

// TestTheCommandTreeIsTheCommandSet checks the tree the reference documents draw against the tree the code
// builds. The checks above read prose, where a command arrives spelled `git pair change ready`; §8's tree writes
// bare names behind box-drawing characters, which no prose pattern sees. That is how it carried an `integration`
// group and `change use` long after both were deleted, and never gained `change integrate`, `change tidy`,
// `change stack`, `change combine` or `skill`. Both directions are checked, because a tree missing a command
// misleads exactly as well as one showing a command that is gone - and a tree is the document a person reads to
// learn what the tool can do.
func TestTheCommandTreeIsTheCommandSet(t *testing.T) {
	paths := commandPaths(newRootCommand(&app{}))
	checked := 0
	for _, file := range completeDocs(t) {
		block := commandTreeBlock(t, file, readDoc(t, file))
		if block == "" {
			continue
		}
		checked++
		listed := parseCommandTree(block)
		if len(listed) == 0 {
			t.Errorf("%s has a block that starts like a command tree and draws no entries", filepath.Base(file))
			continue
		}
		for p := range listed {
			if !paths[p] {
				t.Errorf("%s's command tree lists `%s`, which the CLI does not have", filepath.Base(file), p)
			}
		}
		for p := range paths {
			if p == "" || listed[p] {
				continue
			}
			t.Errorf("%s's command tree omits `%s`", filepath.Base(file), p)
		}
	}
	if checked == 0 {
		t.Fatal("no command tree in the reference documents: the tree is a checked surface, and a document set that " +
			"lost it would stop being checked without saying so")
	}
}

// commandTreeBlock returns the fenced block that draws the command tree, identified by its body starting with
// the program's own name, or "" for a document that draws no tree.
func commandTreeBlock(t *testing.T, file, text string) string {
	t.Helper()
	for _, block := range fencedBlocks(t, file, text) {
		lines := strings.Split(block, "\n")
		if len(lines) > 1 && strings.TrimSpace(lines[1]) == "git-pair" {
			return block
		}
	}
	return ""
}

// parseCommandTree turns drawn entries into command paths: "change tidy" for the line "│   ├── tidy". Depth
// comes from the column the branch marker sits at, four characters per level, which is how the tree is written
// and how `git-pair`'s own help groups commands.
func parseCommandTree(block string) map[string]bool {
	out := map[string]bool{}
	var stack []string
	for _, line := range strings.Split(block, "\n") {
		i := strings.Index(line, "├── ")
		if i < 0 {
			i = strings.Index(line, "└── ")
		}
		if i < 0 {
			continue
		}
		name := strings.TrimSpace(line[i+len("├── "):])
		if name == "" {
			continue
		}
		depth := i/4 + 1
		for len(stack) < depth {
			stack = append(stack, "")
		}
		stack[depth-1] = name
		stack = stack[:depth]
		out[strings.Join(stack, " ")] = true
	}
	return out
}
