package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// README's JSON contracts and PRD §22 state the rule: an array git-pair prints is `[]` for "asked, and
// none", never null. A null says nobody asked the question, which is true of no command here. The rule
// needs a machine behind it, because the shape is invisible in a run that passes: `queue` printed null
// for two of its lists while every reader accepted null as "empty" — `jsonList` in this harness still
// does, which is how it stayed unnoticed.

// permittedNulls names each field allowed to answer null, with the reason it is not an array.
var permittedNulls = map[string]string{
	// An object that does not exist yet: `status` before the first review submission.
	"latest_review": "an absent object, reported as null rather than as an empty object",
	// `status --changeset <other>`: the question belongs to a checkout this command does not stand in.
	"uncommitted": "a field reporting that it cannot answer, which null is the only honest way to say",
}

// nullKeys collects the keys whose value is the JSON literal null, at any depth.
func nullKeys(t *testing.T, out string) []string {
	t.Helper()
	var root any
	if err := json.Unmarshal([]byte(out), &root); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	var keys []string
	var walk func(any)
	walk = func(v any) {
		switch node := v.(type) {
		case map[string]any:
			for k, child := range node {
				if child == nil {
					keys = append(keys, k)
					continue
				}
				walk(child)
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(root)
	return keys
}

// The three arrays that answered null before this rule had a test: two of `queue`'s lists, the empty
// review's file list, and `status`'s stack on a changeset the walk did not run for.
func TestEmptyListsAreEmptyArrays(t *testing.T) {
	t.Run("queue with nothing ready", func(t *testing.T) {
		f := newRepo(t)
		for _, key := range []string{"ready_for_review", "skipped", "landed_unrecorded", "unpublished"} {
			out := runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue", "--json")
			if got := out.json(t)[key]; got == nil {
				t.Errorf("%s = null, want [] — an empty list is the answer %q\n%s", key, "asked, and none", out.stdout)
			} else if _, ok := got.([]any); !ok {
				t.Errorf("%s = %T, want an array", key, got)
			}
		}
	})

	t.Run("a review that changed no files", func(t *testing.T) {
		f, _ := newChangeset(t, "booking", "main")
		ready(t, f)
		out := runIn(t, f.Dir(), "review", "submit", "--json", "--block").mustSucceed(t, "review", "submit")
		files, ok := out.json(t)["files"].([]any)
		if !ok {
			t.Fatalf("files = %v, want an array\n%s", out.json(t)["files"], out.stdout)
		}
		if len(files) != 0 {
			t.Errorf("files = %v, want [] for a review that changed nothing", files)
		}
	})

	t.Run("a changeset that is not stacked", func(t *testing.T) {
		f, _ := newChangeset(t, "booking", "main")
		ready(t, f)
		out := runIn(t, f.Dir(), "status", "--json").mustSucceed(t, "status", "--json")
		if stack := out.json(t)["stack"]; stack == nil {
			t.Errorf("stack = null, want [] — the walk did not run, and that is not the same as the key being absent\n%s", out.stdout)
		} else if _, ok := stack.([]any); !ok {
			t.Errorf("stack = %T, want an array", stack)
		}
	})
}

// Every `--json` surface the suite can reach, in the states a changeset actually passes through. A new
// key that answers null fails here until someone decides it is allowed and says why above.
func TestNoJSONArrayIsEverNull(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T) string
		runs  [][]string
	}{
		{
			name:  "an empty repository",
			setup: func(t *testing.T) string { return newRepo(t).Dir() },
			runs:  [][]string{{"queue", "--json"}, {"skill", "list", "--json"}},
		},
		{
			name:  "an install",
			setup: func(t *testing.T) string { return newRepo(t).Dir() },
			runs:  [][]string{{"skill", "install", "--json"}},
		},
		{
			name: "work in flight",
			setup: func(t *testing.T) string {
				f, _ := newChangeset(t, "booking", "main")
				ready(t, f)
				return f.Dir()
			},
			runs: [][]string{
				{"status", "--json"}, {"queue", "--json"}, {"check", "--json"},
				{"review", "history", "--json"},
			},
		},
		{
			name: "a blocked review",
			setup: func(t *testing.T) string {
				f, _ := newChangeset(t, "booking", "main")
				ready(t, f)
				submit(t, f, "block")
				return f.Dir()
			},
			runs: [][]string{
				{"status", "--json"}, {"queue", "--json"}, {"check", "--json"},
				{"review", "history", "--json"},
				{"change", "wait", "--json", "--interval", "1s"},
				{"review", "submit", "--json", "--feedback"},
			},
		},
		{
			name: "an approval",
			setup: func(t *testing.T) string {
				f, _, _, _ := approvedChangeset(t)
				return f.Dir()
			},
			runs: [][]string{{"status", "--json"}, {"check", "--json"}, {"queue", "--json"}},
		},
		{
			name: "a recorded landing",
			setup: func(t *testing.T) string {
				f, _, source, landing := recordFixture(t)
				runIn(t, f.Dir(), "integration", "record", "--source", source, "--commit", landing,
					"--target", "release/2.x").mustSucceed(t, "integration", "record")
				// The reader's position: standing on the destination branch, asking about a changeset no
				// branch of this checkout is working on any more.
				f.SwitchTo("main")
				return f.Dir()
			},
			runs: [][]string{
				{"status", "--changeset", "booking", "--json"}, {"queue", "--json"},
				{"integration", "record", "--changeset", "booking", "--target", "release/2.x", "--json"},
			},
		},
		{
			name: "a published record",
			setup: func(t *testing.T) string {
				f, _, _ := publishedFixture(t)
				runIn(t, f.Dir(), "integration", "publish").mustSucceed(t, "integration", "publish")
				return f.Dir()
			},
			runs: [][]string{
				{"integration", "publish", "--json"}, {"queue", "--json"}, {"status", "--json"},
			},
		},
	}

	for _, tc := range cases {
		dir := tc.setup(t)
		for _, args := range tc.runs {
			res := runIn(t, dir, args...)
			if res.code != exitOK {
				t.Errorf("%s: git-pair %v exited %d, want 0\nstderr: %s", tc.name, args, res.code, res.stderr)
				continue
			}
			for _, key := range nullKeys(t, res.stdout) {
				if _, ok := permittedNulls[key]; !ok {
					t.Errorf("%s: git-pair %v printed %q as null, and the JSON contract says an array is `[]` for %q\n%s",
						tc.name, args, key, "asked, and none", res.stdout)
				}
			}
		}
	}
}

// The two commands that accept the global `--json` and have no machine-readable form say so. Before the
// note, `change feedback --json` printed its report on stderr and left stdout empty, which a caller
// reading stdout reads as "the review contained nothing".
func TestJSONOnAViewerSaysTheFlagChangedNothing(t *testing.T) {
	f, _ := newChangeset(t, "booking", "main")
	ready(t, f)
	submit(t, f, "block")

	for _, args := range [][]string{
		{"change", "feedback", "--json"}, {"diff", "--json"},
		{"skill", "show", "--json"}, {"skill", "agents-md", "--json"},
	} {
		res := runIn(t, f.Dir(), args...).mustSucceed(t, args...)
		if !strings.Contains(res.stderr, "has no --json output") {
			t.Errorf("git-pair %v said nothing about a flag it cannot honour\nstderr: %s", args, res.stderr)
		}
	}

	res := runIn(t, f.Dir(), "change", "feedback").mustSucceed(t, "change", "feedback")
	if strings.Contains(res.stderr, "has no --json output") {
		t.Errorf("`change feedback` without --json reported a flag nobody passed\nstderr: %s", res.stderr)
	}
}
