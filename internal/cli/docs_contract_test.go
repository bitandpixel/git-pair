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

// TestNoDocNamesADurableRef checks the durable namespace, where drift is expensive: a document naming a ref
// family the code never writes teaches a reader to fetch nothing.
func TestNoDocNamesADurableRef(t *testing.T) {
	// git-pair writes no refs. A document that names one teaches a reader to fetch, configure a refspec
	// for, or audit something that does not exist, and every such line in the past was a promise the tool
	// kept by writing a ref nobody needed. The bare namespace is allowed only in the prose that explains
	// what was retired; anything shaped like a ref path under it is not.
	for _, file := range docFiles(t) {
		for _, m := range durableRefPath.FindAllString(readDoc(t, file), -1) {
			t.Errorf("%s names %s: git-pair writes no refs, so no document should name one", filepath.Base(file), m)
		}
	}
}

// durableRefPath is the git-pair ref namespace, and it is a package-level pattern because two surfaces are
// held to it: the documents, and the help text the built tree prints.
var durableRefPath = regexp.MustCompile(`refs/git-pair/[A-Za-z0-9_.<>*-]+(?:/[A-Za-z0-9_.<>*-]+)*`)

// TestHelpTextNamesCommandsThisBuildAnswersTo holds the built help text to the rules the documents obey.
//
// The documents are checked against the command tree in both directions, and the sentences the advice
// builders emit are checked by advice_contract_test.go. Help text is neither of those: it is not a file on
// disk the document checks read, and it is not a sentence the tool prints at run time. That gap is how
// `git pair integration record` kept shipping in `git pair review submit --help` through the milestone that
// deleted the command, the refs it wrote, and the code that wrote them.
//
// Two fields per command are read: `Long`, which is the screen a person reads immediately before running the
// command, and `Example`, which is the line they copy out of it. Short is left alone — cobra writes the
// command's own name into the listing around it, so a name there is cobra's rather than the author's.
//
// The rules are the ones the prose already obeys, read through the same helpers so a reference cannot be
// wrong in one surface and right in another: every `git pair …` reference names a command this build has,
// with flags that belong to the command it is spelled on, and nothing names a ref under the git-pair
// namespace or claims a durable ref. Help states what the tool does today. The history of what it stopped
// doing belongs to the spec, where §13.4 carries the deleted refs and says plainly that there is no
// `integration record`.
func TestHelpTextNamesCommandsThisBuildAnswersTo(t *testing.T) {
	root := newRootCommand(&app{})
	paths := commandPaths(root)
	paths["help"] = true
	flags := adviceFlagTree(root)

	checked := 0
	var walk func(path string, c *cobra.Command)
	walk = func(path string, c *cobra.Command) {
		for _, field := range []struct{ what, text string }{{"Long", c.Long}, {"Example", c.Example}} {
			if strings.TrimSpace(field.text) == "" {
				continue
			}
			checked++
			where := "`git pair` " + field.what
			if path != "" {
				where = "`git pair " + path + "` " + field.what
			}
			for _, c := range helpCommandRefs(field.what, field.text, paths) {
				if c.path == "" {
					if c.followed == "" {
						continue
					}
					t.Errorf("%s names `git pair %s`, which this build has no command for. Help states what the tool does; "+
						"the history of what it stopped doing is the spec's to carry (§13.4), not the screen a person reads "+
						"before running the command", where, c.followed)
					continue
				}
				checkCommandReference(t, where, "names", c, paths, flags)
			}
			for _, m := range durableRefPath.FindAllString(field.text, -1) {
				t.Errorf("%s names %s: git-pair writes no refs, so no surface should name one", where, m)
			}
			if strings.Contains(strings.ToLower(field.text), "durable ref") {
				t.Errorf("%s speaks of a durable ref: git-pair writes none, and the sentence that explains the ones it "+
					"deleted is the spec's", where)
			}
		}
		for _, sub := range c.Commands() {
			if sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			walk(strings.TrimSpace(path+" "+sub.Name()), sub)
		}
	}
	walk("", root)
	if checked < 20 {
		t.Fatalf("only %d help fields were checked: the walk stopped collecting", checked)
	}
}

// helpCommandRefs finds the command references help text cites and parses each into a command path plus the
// flags spelled on it.
//
// Which spans count as a citation depends on the field, because the two fields are written in different
// registers. A `Long` cites a command inside backticks and writes English around it, so the span is the
// reference and the words outside it are prose — the reading that lets "`git pair review` is `git pair review
// open`" mean what it says instead of reporting the second mention as a subcommand of the first. An `Example`
// block is a command line somebody copies, so each of its lines is a reference in full, flags included.
//
// This is deliberately not `adviceCommands`. That scanner walks a sentence and stops where the tree stops
// continuing, which is right for a sentence the tool assembles and wrong for two here: a deleted command is
// exactly a reference whose first word the tree does not continue, and dropping it is how `git pair
// integration record` survived in `--help` through the milestone that deleted it.
func helpCommandRefs(field, text string, paths map[string]bool) []adviceCommand {
	var cited []string
	if field == "Long" {
		for _, span := range backtickSpans(text) {
			if strings.HasPrefix(span, "git pair ") {
				cited = append(cited, span)
			}
		}
	} else {
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if i := strings.Index(line, "  #"); i >= 0 {
				line = strings.TrimSpace(line[:i])
			}
			if strings.HasPrefix(line, "git pair ") {
				cited = append(cited, line)
			}
		}
	}
	var out []adviceCommand
	for _, line := range cited {
		out = append(out, parseCitedCommandLine(strings.TrimPrefix(line, "git pair "), paths))
	}
	return out
}

// backtickSpans returns the contents of each `…` span of a help field, which is how its author marks a command
// citation.
func backtickSpans(text string) []string {
	var out []string
	for len(text) > 0 {
		open := strings.IndexByte(text, '`')
		if open < 0 {
			break
		}
		rest := text[open+1:]
		end := strings.IndexByte(rest, '`')
		if end < 0 {
			break
		}
		if span := strings.TrimSpace(rest[:end]); span != "" {
			out = append(out, span)
		}
		text = rest[end+1:]
	}
	return out
}

// parseCitedCommandLine splits one cited command into the path the tree continues and the flags spelled on it.
// The token where the path stopped is kept as `followed`, so a group named with a word that is not one of its
// children — and a first word that is not a command at all — are reported rather than trimmed away. A flag
// written with its value is checked by name, and a bare `--` names nothing, so neither reaches the caller as a
// flag the command has to declare.
func parseCitedCommandLine(line string, paths map[string]bool) adviceCommand {
	var c adviceCommand
	var words []string
	for _, raw := range strings.Fields(line) {
		token := strings.Trim(raw, "`\"'`,.;:!?) ]}")
		if token == "" || token == "--" {
			continue
		}
		if strings.HasPrefix(token, "--") {
			name, _, _ := strings.Cut(strings.TrimPrefix(token, "--"), "=")
			if name != "" {
				c.flags = append(c.flags, name)
			}
			continue
		}
		if strings.HasPrefix(token, "-") || !adviceWord.MatchString(token) {
			break
		}
		candidate := strings.Join(append(append([]string{}, words...), token), " ")
		if adviceHasPathOrPrefix(paths, candidate) {
			words = append(words, token)
			continue
		}
		c.followed = token
		break
	}
	c.path = strings.Join(words, " ")
	return c
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
