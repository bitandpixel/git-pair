package cli_test

import (
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
