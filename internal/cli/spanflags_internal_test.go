package cli

import (
	"errors"
	"strings"
	"testing"

	"gitpr/internal/span"
)

// The span flags are the contract between a script and a screen: the same span has to be nameable
// with `gitpr diff --base-ref=…` and with `gitpr review open --base-ref=…`, and a bad combination
// has to refuse the same way from either. `review open` cannot be run in a test — it asks for a
// terminal and refuses without one — so this checks the two places the contract actually lives:
// the flags a command registers, and the validation a selector runs before anything opens.

// spanFlagNames is the contract: both ends of a span, each nameable three ways, including the two
// review-relative spellings that predated the symmetric set.
var spanFlagNames = []string{
	"unreviewed", "since-review",
	"base-review", "base-commit", "base-ref",
	"head-review", "head-commit", "head-ref",
}

func TestReviewOpenRegistersEverySpanFlagDiffDoes(t *testing.T) {
	diff := newDiffCommand(&app{})
	open := newReviewOpenCommand(&app{})

	for _, name := range spanFlagNames {
		if diff.Flags().Lookup(name) == nil {
			t.Errorf("gitpr diff has no --%s flag", name)
		}
		if open.Flags().Lookup(name) == nil {
			t.Errorf("gitpr review open has no --%s flag: a span a script can name would be a span "+
				"the screen cannot", name)
		}
	}

	// --stat and --tool choose how diff prints, which is a diff question. On review open they
	// would be accepted, ignored, and remembered as broken.
	for _, name := range []string{"stat", "tool"} {
		if open.Flags().Lookup(name) != nil {
			t.Errorf("gitpr review open gained a --%s flag it does not act on", name)
		}
	}
}

// The full changeset is the default, and it is applied in one function so the screen and the
// script cannot disagree about what "no span flag" means.
func TestNoSpanFlagMeansTheFullChangeset(t *testing.T) {
	got, err := (&spanOptions{}).selector()
	if err != nil {
		t.Fatalf("no flags produced an error: %v", err)
	}
	if got != span.Full() {
		t.Errorf("no flags resolved to %+v, want the full changeset %+v", got, span.Full())
	}
}

// "The difftool compares the span against your working tree" was the help for every span, and it
// is true only of a live one: a span ending at a commit passes the tool both pins, so both buffers
// are blobs. A reviewer who chooses --tool expecting live files and gets read-only ones has been
// told the wrong thing by the tool's own help, which is the one piece of documentation here that
// no README correction fixes.
func TestToolFlagHelpNamesBothKindsOfSpan(t *testing.T) {
	usage := newDiffCommand(&app{}).Flags().Lookup("tool").Usage
	if usage == "" {
		t.Fatal("gitpr diff has no --tool flag")
	}
	for _, want := range []string{"working tree", "historical", "pinned"} {
		if !strings.Contains(usage, want) {
			t.Errorf("--tool help = %q, which never says %q", usage, want)
		}
	}
}

// Two flags naming one end of the span disagree, and that is a command to fix rather than a
// precedence to remember. The count is over every spelling of that end, which is why the
// question-shaped `--unreviewed` is refused against an explicit `--base-*` instead of losing.
func TestSpanFlagsRefuseTwoEndpointsOnOneEnd(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts spanOptions
	}{
		{"work-left-to-do spelled over an explicit base", spanOptions{unreviewed: true, baseCommit: "abc1234"}},
		{"since-review plus base-ref", spanOptions{sinceReview: "-1", baseRef: "main"}},
		{"base-review plus base-commit", spanOptions{baseReview: "0", baseCommit: "abc1234"}},
		{"two heads", spanOptions{headReview: "-1", headRef: "main"}},
		{"head-commit plus head-ref", spanOptions{headCommit: "abc1234", headRef: "main"}},
	} {
		_, err := tc.opts.selector()
		if err == nil {
			t.Errorf("%s: selector() accepted it", tc.name)
			continue
		}
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Errorf("%s: error %v is not a usage error", tc.name, err)
			continue
		}
		if !strings.Contains(err.Error(), "pick one") {
			t.Errorf("%s: said %q, want the refusal to name the end and ask for one", tc.name, err)
		}
	}
}
