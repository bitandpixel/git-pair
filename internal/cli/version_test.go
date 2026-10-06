package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/cli"
)

// A build from a commit with no tag has no version to report, and the value it prints in the
// meantime has to be one nobody can mistake for a published number: a dev build printing `0.1.0`
// is indistinguishable from the release everyone installed, which is how a bug gets filed against
// a build that was never shipped. Releases take their version from the tag through
// `-ldflags -X gitpair/internal/cli.Version=…` (`.goreleaser.yaml`), so what is pinned here is
// only ever the fallback.
func TestVersionFallbackNamesNoRelease(t *testing.T) {
	if cli.Version == "" {
		t.Fatal("`git-pair --version` prints nothing that identifies the build")
	}
	if !strings.Contains(cli.Version, "-") {
		t.Errorf("the fallback %q reads like a released number; it has to name a state instead, "+
			"which none of the published tags (v0.1.0, v0.1.1, …) do", cli.Version)
	}
	if strings.HasPrefix(cli.Version, "v") {
		// goreleaser stamps `{{.Version}}`, which is the tag with its v removed, so a fallback
		// wearing a v would be the one case where `--version` prints the tag's own shape.
		t.Errorf("the fallback %q carries the tag's v; `--version` prints it after %q, so the v "+
			"belongs to the tag and not to the value", cli.Version, "git-pair version ")
	}
}

// `mise run build` stamps the version itself, so the fallback is only what a plain `go build` prints.
// That rule breaks two ways — a build task stops calling the script, or the script starts printing
// something that reads like a release — and nothing at runtime would notice either one, so both are
// pinned here. It is the same argument `docs_contract_test.go` makes about the prose: the wrong thing
// is not an error, it is a string someone reads and trusts.
func TestDevBuildStampsItsCommit(t *testing.T) {
	root := filepath.Join("..", "..")

	stamped := runRepo(t, root, "sh", "scripts/dev-version.sh")
	if !strings.HasPrefix(stamped, "0.0.0-dev+") {
		t.Errorf("scripts/dev-version.sh printed %q, want the dev answer 0.0.0-dev+<sha>", stamped)
	}
	if strings.HasPrefix(stamped, "v") {
		t.Errorf("scripts/dev-version.sh printed %q with the tag's v; nothing this script prints "+
			"should look like the shape of a published tag", stamped)
	}
	head := runRepo(t, root, "git", "rev-parse", "--short", "HEAD")
	commit := strings.TrimPrefix(stamped, "0.0.0-dev+")
	// `.dirty` is the tree's own statement: uncommitted work belongs in the version a build of it
	// reports, or a build of someone's edits is indistinguishable from a build of the commit.
	if commit != head && commit != head+".dirty" {
		t.Errorf("the stamped commit %q is neither this checkout's short HEAD %q nor its .dirty form",
			commit, head)
	}

	contents := readRepo(t, root, ".mise.toml")
	for _, task := range []string{"build", `"build:prod"`} {
		body := taskRun(t, contents, task)
		for _, want := range []string{
			"sh scripts/dev-version.sh",                // the one rule, not a second copy of it
			"-X gitpair/internal/cli.Version=$version", // and it reaches the linker
		} {
			if !strings.Contains(body, want) {
				t.Errorf("tasks.%s is missing %q, so a build from it installs a binary reporting the "+
					"fallback rather than the commit it came from", task, want)
			}
		}
	}
}

// runRepo runs one command in dir and returns its trimmed stdout. The repo-level checks need git and
// /bin/sh where the package tests need nothing but a fixture.
func runRepo(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v in %s: %v", args, dir, err)
	}
	return strings.TrimSpace(string(out))
}

func readRepo(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// taskRun returns the run block of one mise task. Deliberately crude: the tasks are a small literal in
// one file, and what is being pinned is that a build still stamps — not that a unit test can parse TOML.
func taskRun(t *testing.T, contents, task string) string {
	t.Helper()
	start := strings.Index(contents, "[tasks."+task+"]")
	if start < 0 {
		t.Fatalf(".mise.toml has no [tasks.%s]", task)
	}
	rest := contents[start:]
	open := strings.Index(rest, `"""`)
	if open < 0 {
		t.Fatalf("tasks.%s has no run block", task)
	}
	after := rest[open+len(`"""`):]
	end := strings.Index(after, `"""`)
	if end < 0 {
		t.Fatalf("tasks.%s has an unterminated run block", task)
	}
	return after[:end]
}
