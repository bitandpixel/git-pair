// Package landingconfig reads the repository's declaration of what happens
// after the gate clears.
//
// git-pair's landing advice has always named the step and the destination and
// never the actor, because git-pair asks nothing about who merges (PRD §26).
// The sentence an agent reads still has to pick an exit, and which exit is
// right is a fact about the repository: one where a job merges declared work
// is misadvised by the author's-own-merge sentence, exactly as one where the
// author merges is misadvised by a hand-off. The declaration moves that
// decision from the reader to the repository: `.git-pair/config` states the
// route, and the advice follows it.
//
// The declaration is data, not behavior, and the rules for reading it follow
// the two precedents this repository has already set. Like the tracked hooks
// root, it lives under `.git-pair/` — repository policy, shared by everyone
// who clones, unlike the clone-level hooks root which is one person's own.
// And like `parseMetadata`, it is parsed by hand rather than by a format
// library: the file has one table and two keys, and a parser that small is
// safer written down than imported.
//
// A declaration this tool cannot honor is reported, never silently ignored —
// the same rule `git pair hooks` applies to a hook configured under an unknown
// point, where a typo is otherwise indistinguishable from an absence. The
// fallback is the default route, both exits, with the defect named on stderr:
// the unopinionated view is what a repository with a broken declaration sees
// until the declaration is fixed.
package landingconfig

import (
	"fmt"
	"regexp"
	"strings"
)

// File is the tracked config file, named from the repository root. It sits
// beside the tracked hooks root (`.git-pair/hooks/`), which is the precedent
// for repository-owned git-pair state that travels with the tree.
const File = ".git-pair/config"

// Route is the landing route a repository declares: which exit the landing
// advice offers for the work the gate has cleared.
type Route string

const (
	// RouteDefault is both exits — the author's own merge as the next step and
	// the hand-off beside it. It is what an absent file has always meant, and
	// naming it lets a repository say "we have not picked" explicitly.
	RouteDefault Route = "default"
	// RouteManual is the author's own merge, and nothing beside it. The
	// repository has answered the question: the merge is the author's.
	RouteManual Route = "manual"
	// RouteAutomated is the hand-off, and nothing beside it: the author
	// declares and pushes, and whoever owns the destination merges. The
	// author's-own-merge sentence is removed from every surface, which is the
	// point — a repository whose merge runs behind a CI gate cannot afford a
	// screen that offers the merge as an exit.
	RouteAutomated Route = "automated"
	// RouteCustom is the repository's own sentence, read from Next and
	// substituted. The PR reviewed by a human is the shape that needs it: the
	// step names a forge git-pair does not name, so the repository names it.
	RouteCustom Route = "custom"
)

// Config is the resolved declaration: the route the advice follows, the
// template a custom route substitutes, and what was wrong when the file could
// not be honored.
type Config struct {
	// Route is the resolved route. A file with a defect resolves to
	// RouteDefault — the fallback is the unopinionated view, never a guess at
	// what the repository meant.
	Route Route
	// Next is the custom route's template, with {branch}, {destination} and
	// {changeset} as the placeholders Expand fills. Read only under
	// RouteCustom; a template under any other route is a defect.
	Next string
	// Defect names what made the declaration unusable, empty when the file was
	// absent or valid. The surfaces that read the config print it on stderr
	// and show the default advice; it never fails a command, because a wrong
	// label makes advice wrong, not work unmergeable.
	Defect string
}

// Effective is the route the advice follows, treating the zero value as the
// default. A contract built without a route — a call site that constructs one
// directly rather than through the session — still advises the way an
// undeclared repository always has.
func (r Route) Effective() Route {
	if r == "" {
		return RouteDefault
	}
	return r
}

// OffersMerge reports whether the author's own merge may be offered as an
// exit. It is the primary sentence under the default and manual routes and the
// "or land it yourself" line under them; under automated and custom the
// repository has said the merge is not the author's step, so no surface offers
// it — that sentence is the one a repository with a merge job cannot afford.
func (r Route) OffersMerge() bool {
	switch r.Effective() {
	case RouteDefault, RouteManual:
		return true
	}
	return false
}

// ShowsBothExits reports whether the screen may offer both exits — the
// author's own merge and the hand-off beside it. Only the default route forks:
// under manual the repository named the merge as the whole answer, under
// automated the hand-off is the primary rather than an alternative, and under
// custom the repository wrote the step itself.
func (r Route) ShowsBothExits() bool {
	return r.Effective() == RouteDefault
}

// Parse resolves a declaration from the file's text. An empty text is a
// missing file: the default route, no defect.
//
// The grammar is the one table and two keys the feature needs, and every
// deviation from it is a defect rather than a tolerated extra: an unknown
// section, an unknown key, an unknown route value, a template under a route
// that does not read one, a custom route without a template, a key outside a
// table. Each is reported; any one of them resolves the whole file to the
// default route, because a file this tool only partly understands is a file
// whose other settings may not mean what the repository believes.
func Parse(text string) Config {
	if strings.TrimSpace(text) == "" {
		return Config{Route: RouteDefault}
	}
	var defects []string
	route := RouteDefault
	next := ""
	inLanding := false
	for i, raw := range strings.Split(text, "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				defects = append(defects, fmt.Sprintf("line %d: %q does not close its section header", lineNo, line))
				inLanding = false
				continue
			}
			name := strings.TrimSpace(line[1 : len(line)-1])
			if name != "landing" {
				defects = append(defects, fmt.Sprintf("line %d: unknown section [%s]", lineNo, name))
			}
			inLanding = name == "landing"
			continue
		}
		key, rest, ok := strings.Cut(line, "=")
		if !ok {
			defects = append(defects, fmt.Sprintf("line %d: expected key = \"value\", got %q", lineNo, line))
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			defects = append(defects, fmt.Sprintf("line %d: empty key", lineNo))
			continue
		}
		if !inLanding {
			defects = append(defects, fmt.Sprintf("line %d: %q is outside [landing]", lineNo, key))
			continue
		}
		value, vdefect := configValue(rest)
		if vdefect != "" {
			defects = append(defects, fmt.Sprintf("line %d: %s", lineNo, vdefect))
			continue
		}
		switch key {
		case "route":
			switch r := Route(value); r {
			case RouteDefault, RouteManual, RouteAutomated, RouteCustom:
				route = r
			default:
				defects = append(defects, fmt.Sprintf("line %d: unknown landing route %q", lineNo, value))
			}
		case "next":
			next = value
		default:
			defects = append(defects, fmt.Sprintf("line %d: unknown landing key %q", lineNo, key))
		}
	}
	if route == RouteCustom && next == "" {
		defects = append(defects, `route is "custom" but next is empty`)
	}
	if route != RouteCustom && next != "" {
		defects = append(defects, `next is read only when route is "custom"`)
	}
	if route == RouteCustom {
		if unknown := unknownPlaceholders(next); len(unknown) > 0 {
			defects = append(defects, fmt.Sprintf("next names %s, which %s not a placeholder git-pair fills",
				strings.Join(quoted(unknown), ", "), plural(unknown, "is", "are")))
		}
	}
	if len(defects) > 0 {
		return Config{Route: RouteDefault, Defect: strings.Join(defects, "; ")}
	}
	return Config{Route: route, Next: next}
}

// Expand fills the template's placeholders with the facts the sentence layer
// holds: the branch the work sits on, the destination the walk resolved, and
// the changeset's id. Parse has already refused a template naming anything
// else, so the result is the whole substitution; the caller passes the
// display-ready forms ("<branch>" included) and Expand fills them verbatim.
func Expand(template, branch, destination, changeset string) string {
	out := strings.ReplaceAll(template, "{branch}", branch)
	out = strings.ReplaceAll(out, "{destination}", destination)
	out = strings.ReplaceAll(out, "{changeset}", changeset)
	return out
}

// configValue reads one `key =` value's right-hand side. A quoted value ends
// at its closing quote, and what follows must be empty or a comment — a
// trailing word is a typo sitting next to a value, not decoration. An unquoted
// value runs to an inline comment, so `route = manual # the job merges` reads
// as "manual".
func configValue(rest string) (string, string) {
	value := strings.TrimSpace(rest)
	if strings.HasPrefix(value, `"`) {
		end := strings.Index(value[1:], `"`)
		if end < 0 {
			return "", "value opens a quote it never closes"
		}
		trailing := strings.TrimSpace(value[end+2:])
		if trailing != "" && !strings.HasPrefix(trailing, "#") {
			return "", fmt.Sprintf("unexpected %q after the closing quote", trailing)
		}
		return value[1 : end+1], ""
	}
	if i := strings.Index(value, " #"); i >= 0 {
		value = strings.TrimSpace(value[:i])
	}
	return value, ""
}

// placeholderNames are the words a template may put in braces. A template is
// the repository's sentence about its own forge, so the vocabulary stays at
// the three facts git-pair holds for every changeset; anything else would be
// git-pair guessing at a fourth.
var placeholderNames = map[string]bool{
	"branch":      true,
	"destination": true,
	"changeset":   true,
}

// placeholderRE matches the braced words Parse validates. A brace that is not
// a placeholder — JSON in a template, shell braces — is left alone by Expand
// and refused here only when it looks like one: a name this package does not
// fill.
var placeholderRE = regexp.MustCompile(`\{([a-zA-Z][a-zA-Z0-9_-]*)\}`)

// unknownPlaceholders lists the braced names in template that no Expand call
// fills, sorted and without duplicates so one defect names each unknown once.
func unknownPlaceholders(template string) []string {
	seen := map[string]bool{}
	var unknown []string
	for _, m := range placeholderRE.FindAllStringSubmatch(template, -1) {
		name := m[1]
		if !placeholderNames[name] && !seen[name] {
			seen[name] = true
			unknown = append(unknown, name)
		}
	}
	return unknown
}

// quoted wraps each name in double quotes for the defect message.
func quoted(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = fmt.Sprintf("%q", n)
	}
	return out
}

// plural picks the verb one or many names take.
func plural(names []string, one, many string) string {
	if len(names) == 1 {
		return one
	}
	return many
}
