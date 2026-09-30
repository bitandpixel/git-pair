package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/cli"
	"gitpair/internal/gittest"
)

// The cost bounds in this suite (`TestStatusStackChainCostsABoundedReadPerStep` and its neighbours) exist
// because a formulation that asks one question per changeset behaves identically on every fixture and takes
// minutes in a repository with a year of landed work. Counting git subprocesses is the only way to see that
// difference, and the count comes from outside the code under test.
//
// A cache breaks that instrument in the worst available direction. The read memo answers a duplicated
// invocation inside the run being counted, so a formulation that asks the same question twice reports asking
// it once — the bound passes on precisely the duplicate it exists to catch. And the derived-fact cache
// answers from disk without spawning git at all, which is how `TestStatusStackChainCostsABoundedReadPerStep`
// came to measure 0 invocations for the walk it bounds.
//
// So `SpawnShim` turns both off, through `cli.NoCacheEnv`. The tests here are what make that a fact rather
// than a comment. Each one was checked by deleting the line it protects and confirming it fails: the first
// version of this file used a fixture with nothing landed, and passed either way, because a queue over an
// unlanded repository derives almost no cacheable fact and the memo cannot cross two separate `Execute`
// calls anyway. A test that cannot fail is not a test.

// TestSpawnShimCountsTheAlgorithmNotTheCache is the sensitivity guard. Two identical runs against one
// fixture must cost the same number of git subprocesses. If either cache were on, the second would answer
// from disk and cost materially less, and any bound measured under this shim would be blind to a duplicated
// read.
//
// The fixture has to have something landed. `cacheFixture` puts an unreviewed landing on the destination,
// which is what makes the second run cheaper when caching is allowed: measuring against a repository with
// nothing to derive would pass under either setting.
func TestSpawnShimCountsTheAlgorithmNotTheCache(t *testing.T) {
	f := cacheFixture(t)

	count := f.SpawnShim(t)
	runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue")
	first := count()
	if first == 0 {
		t.Fatal("the shim counted nothing, so the comparison below proves nothing")
	}
	runIn(t, f.Dir(), "queue", "--json").mustSucceed(t, "queue")
	second := count() - first

	// Ten percent of slack for the one or two reads that legitimately differ between runs; a warm cache on
	// this fixture costs roughly a third less, which is far outside it.
	if second*10 < first*9 {
		t.Errorf("the second identical run cost %d invocations against the first's %d: a cache answered it, "+
			"so a cost bound measured under this shim would not see a duplicated read", second, first)
	}
}

// The two sides of the switch are a literal in `internal/gittest` and a constant in `internal/cli`, because
// the import between them would be a cycle. This pins the constant so a rename cannot quietly leave the
// literal behind. The test above catches the drift in behaviour; this one says which side moved.
func TestNoCacheEnvNameIsWhatGittestSets(t *testing.T) {
	if cli.NoCacheEnv != "GIT_PAIR_NO_CACHE" {
		t.Errorf("cli.NoCacheEnv = %q, but internal/gittest/spawn.go sets the literal \"GIT_PAIR_NO_CACHE\"; "+
			"one of the two is wrong and the cost bounds are blind until they agree", cli.NoCacheEnv)
	}
}

// cacheDir is where this repository's derived facts are kept, or the empty string when the git directory
// cannot be named.
func cacheDir(t *testing.T, f *gittest.Fixture) string {
	t.Helper()
	gitDir, err := f.Git("rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatalf("name the git directory: %v", err)
	}
	return filepath.Join(strings.TrimSpace(gitDir), "git-pair", "cache")
}

// TestNoCacheEnvWritesNoCacheFiles is the environment form checked directly rather than by counting: the
// cache directory is created by a run that uses it and is not created by one that does not. Checked this way
// rather than by invocation count because `SpawnShim` now sets the variable itself, which would make a
// counting test pass for the wrong reason.
func TestNoCacheEnvWritesNoCacheFiles(t *testing.T) {
	cases := []struct {
		name  string
		set   func(t *testing.T)
		write bool // whether a cache is expected on disk afterwards
	}{
		{"caching allowed", func(t *testing.T) {}, true},
		{"--no-cache flag", func(t *testing.T) {}, false},
		{cli.NoCacheEnv + "=1", func(t *testing.T) { t.Setenv(cli.NoCacheEnv, "1") }, false},
		{cli.NoCacheEnv + "=empty does not count as set", func(t *testing.T) { t.Setenv(cli.NoCacheEnv, "") }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := cacheFixture(t)
			// The fixture itself ran the CLI — `ready` and the landing both go through it — so the cache
			// directory already holds entries from setup. Start the observation from empty or the assertion
			// below measures the fixture rather than the run.
			if err := os.RemoveAll(cacheDir(t, f)); err != nil {
				t.Fatalf("clear the cache directory: %v", err)
			}
			tc.set(t)
			args := []string{"queue", "--json"}
			if tc.name == "--no-cache flag" {
				args = append(args, "--no-cache")
			}
			runIn(t, f.Dir(), args...).mustSucceed(t, args...)

			entries, err := os.ReadDir(cacheDir(t, f))
			exists := err == nil && len(entries) > 0
			if exists != tc.write {
				got := "no cache files"
				if exists {
					got = "cache files written"
				}
				t.Errorf("%s: %s, want a cache written = %v", tc.name, got, tc.write)
			}
		})
	}
}
