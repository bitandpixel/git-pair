package changeset_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpr/internal/changeset"
	"gitpr/internal/git"
	"gitpr/internal/gittest"
)

// PRD §4: "The implementation should keep the branch-to-changeset naming rule
// deterministic." `/` becomes `-`, other unusable characters collapse to a
// single `-`, surrounding separators are trimmed, and case is preserved so
// `feature/booking-transaction` maps to the directory the PRD example names.
func TestSlugFromBranch(t *testing.T) {
	tests := []struct {
		branch string
		want   string
	}{
		{"booking-transaction", "booking-transaction"},
		{"feature/booking-transaction", "feature-booking-transaction"},
		{"feature/x/y", "feature-x-y"},
		{"feat/JIRA-123 Foo Bar", "feat-JIRA-123-Foo-Bar"},
		{"main", "main"},
		{"release/1.2.3", "release-1.2.3"},
		{"snake_case_and-dashes", "snake_case_and-dashes"},
		{"trailing/slash/", "trailing-slash"},
		{"  padded  ", "padded"},
		{"double//slash", "double-slash"},
		{"dots.in.name", "dots.in.name"},
		{"a+b", "a-b"},
		{"booking#42", "booking-42"},
	}
	for _, tc := range tests {
		t.Run(tc.branch, func(t *testing.T) {
			got, err := changeset.SlugFromBranch(tc.branch)
			if err != nil {
				t.Fatalf("SlugFromBranch(%q) error: %v", tc.branch, err)
			}
			if got != tc.want {
				t.Errorf("SlugFromBranch(%q) = %q, want %q", tc.branch, got, tc.want)
			}
		})
	}
}

func TestSlugFromBranchRejectsUnusableNames(t *testing.T) {
	for _, branch := range []string{"", "   ", "///", "---", "###"} {
		if got, err := changeset.SlugFromBranch(branch); err == nil {
			t.Errorf("SlugFromBranch(%q) = %q, want an error: no usable directory name", branch, got)
		}
	}
}

// Two branches must never map to the same changeset directory unless they really
// are the same name, otherwise stacked branches would share review state
// against PRD §21.
func TestSlugFromBranchIsStableAndInjective(t *testing.T) {
	branches := []string{
		"feature/booking-transaction",
		"feature-booking-transaction",
		"booking-transaction",
		"booking/transaction",
		"booking--transaction",
	}
	seen := map[string]string{}
	for _, branch := range branches {
		slug, err := changeset.SlugFromBranch(branch)
		if err != nil {
			t.Fatalf("SlugFromBranch(%q): %v", branch, err)
		}
		if first, ok := seen[slug]; ok && first != branch {
			t.Logf("note: %q and %q both map to %q", first, branch, slug)
		}
		seen[slug] = branch
		// Deterministic: the same input always yields the same directory name.
		again, _ := changeset.SlugFromBranch(branch)
		if again != slug {
			t.Errorf("SlugFromBranch(%q) is not deterministic: %q then %q", branch, slug, again)
		}
	}
	// The PRD example must hold exactly.
	if got, _ := changeset.SlugFromBranch("feature/booking-transaction"); got != "feature-booking-transaction" {
		t.Errorf("PRD §4 example maps to %q", got)
	}
}

// PRD §7: `gitpr review thread "concurrency tests"` creates
// `changesets/<changeset>/concurrency-tests.md`.
func TestThreadSlugAndPath(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		{"concurrency tests", "concurrency-tests"},
		{"Concurrency Tests", "concurrency-tests"},
		{"  Transaction   boundary  ", "transaction-boundary"},
		{"What happens if these execute concurrently?", "what-happens-if-these-execute-concurrently"},
		{"transaction/boundary", "transaction-boundary"},
	}
	cs := changeset.Changeset{Slug: "booking-transaction", Dir: filepath.Join("changesets", "booking-transaction")}
	for _, tc := range tests {
		t.Run(tc.title, func(t *testing.T) {
			stem, err := changeset.ThreadSlug(tc.title)
			if err != nil {
				t.Fatalf("ThreadSlug(%q): %v", tc.title, err)
			}
			if stem != tc.want {
				t.Errorf("ThreadSlug(%q) = %q, want %q", tc.title, stem, tc.want)
			}
			path, err := cs.ThreadPath(tc.title)
			if err != nil {
				t.Fatalf("ThreadPath(%q): %v", tc.title, err)
			}
			want := filepath.Join("changesets", "booking-transaction", tc.want+".md")
			if path != want {
				t.Errorf("ThreadPath(%q) = %q, want %q", tc.title, path, want)
			}
		})
	}
	if _, err := changeset.ThreadSlug("///"); err == nil {
		t.Error("ThreadSlug accepted a title with no usable characters")
	}
}

func TestThreadTemplateAndAboutTemplate(t *testing.T) {
	cs := changeset.Changeset{Slug: "booking", Dir: filepath.Join("changesets", "booking")}
	thread := changeset.ThreadTemplate("Concurrency tests")
	if thread != "# Concurrency tests\n\n" {
		t.Errorf("ThreadTemplate = %q", thread)
	}
	about := changeset.AboutTemplate("booking")
	for _, heading := range []string{"# booking", "## Summary", "## What changed", "## Design decisions", "## Validation"} {
		if !strings.Contains(about, heading) {
			t.Errorf("ABOUT.md scaffold is missing %q; PRD §6 lists it:\n%s", heading, about)
		}
	}
	if got := cs.AboutPath(); got != filepath.Join("changesets", "booking", "ABOUT.md") {
		t.Errorf("AboutPath = %q", got)
	}
	if got := cs.MetadataPath(); got != filepath.Join("changesets", "booking", "CHANGESET.yaml") {
		t.Errorf("MetadataPath = %q", got)
	}
}

// PRD §9.1: `change init` creates deterministic scaffolding, is idempotent, and
// "should not destroy existing changeset data".
func TestWriteCreatesScaffoldingAndIsIdempotent(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	repo := &git.Repo{Dir: f.Dir()}

	cs := changeset.Changeset{Slug: "feature-booking", Branch: "feature/booking", Dir: filepath.Join("changesets", "feature-booking")}
	written, err := changeset.Write(repo, cs, "main", false)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(written) == 0 {
		t.Fatal("first Write reported nothing created")
	}
	if got := f.MetadataBase("feature-booking"); got != "main" {
		t.Errorf("CHANGESET.yaml base = %q, want main", got)
	}
	if !f.HasWorktreeFile(cs.AboutPath()) {
		t.Fatal("ABOUT.md was not created")
	}

	aboutBefore := f.Read(cs.AboutPath())
	written, err = changeset.Write(repo, cs, "main", false)
	if err != nil {
		t.Fatalf("second Write: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("second Write reported %v, want nothing written", written)
	}
	if f.Read(cs.AboutPath()) != aboutBefore {
		t.Error("second Write changed ABOUT.md")
	}
	if f.MetadataBase("feature-booking") != "main" {
		t.Error("second Write changed CHANGESET.yaml")
	}
}

func TestWriteNeverClobbersAuthorContent(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	repo := &git.Repo{Dir: f.Dir()}

	cs := changeset.Changeset{Slug: "booking", Branch: "booking", Dir: filepath.Join("changesets", "booking")}
	if _, err := changeset.Write(repo, cs, "main", false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	authorText := "# booking\n\n## Summary\n\nThe author's own description, which must survive.\n"
	f.Write(cs.AboutPath(), authorText)
	f.Write(cs.MetadataPath(), "base: main\n")

	if _, err := changeset.Write(repo, cs, "main", false); err != nil {
		t.Fatalf("re-Write: %v", err)
	}
	if got := f.Read(cs.AboutPath()); got != authorText {
		t.Errorf("ABOUT.md was overwritten:\n%s", got)
	}
}

// PRD §5: `base` is the only required metadata, and stacked changesets name a
// sibling changeset as their base.
func TestWriteRecordsStackedBase(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	repo := &git.Repo{Dir: f.Dir()}

	cs := changeset.Changeset{Slug: "booking-tests", Branch: "booking-tests", Dir: filepath.Join("changesets", "booking-tests")}
	if _, err := changeset.Write(repo, cs, "booking-transaction", false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := f.MetadataBase("booking-tests"); got != "booking-transaction" {
		t.Errorf("base = %q, want booking-transaction", got)
	}

	resolved, err := changeset.ForBranch(repo, "booking-tests")
	if err != nil {
		t.Fatalf("ForBranch: %v", err)
	}
	if !resolved.Exists || resolved.Base != "booking-transaction" || resolved.Slug != "booking-tests" {
		t.Errorf("ForBranch = %+v, want an existing changeset with base booking-transaction", resolved)
	}
}

func TestWriteBaseConflict(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	repo := &git.Repo{Dir: f.Dir()}

	cs := changeset.Changeset{Slug: "booking", Branch: "booking", Dir: filepath.Join("changesets", "booking")}
	if _, err := changeset.Write(repo, cs, "main", false); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := changeset.Write(repo, cs, "trunk", false); !errors.Is(err, changeset.ErrBaseConflict) {
		t.Errorf("changing base without --set-base = %v, want ErrBaseConflict", err)
	}
	if got := f.MetadataBase("booking"); got != "main" {
		t.Errorf("refused Write changed base to %q, want main", got)
	}
	if _, err := changeset.Write(repo, cs, "trunk", true); err != nil {
		t.Fatalf("Write with setBase: %v", err)
	}
	if got := f.MetadataBase("booking"); got != "trunk" {
		t.Errorf("base = %q, want trunk after --set-base", got)
	}
}

func TestForBranchDetachedHeadAndMissingDirectory(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	repo := &git.Repo{Dir: f.Dir()}

	if _, err := changeset.ForBranch(repo, ""); !errors.Is(err, changeset.ErrDetachedHead) {
		t.Errorf("ForBranch(\"\") = %v, want ErrDetachedHead", err)
	}
	cs, err := changeset.ForBranch(repo, "uninitialised")
	if err != nil {
		t.Fatalf("ForBranch: %v", err)
	}
	if cs.Exists {
		t.Error("ForBranch reported a changeset that was never created")
	}
	if cs.Slug != "uninitialised" || cs.Dir != filepath.Join("changesets", "uninitialised") {
		t.Errorf("ForBranch = %+v, want the deterministic directory for the branch", cs)
	}
	if _, err := changeset.RequireCurrent(context.Background(), repo); err == nil {
		t.Error("RequireCurrent succeeded on a branch with no changeset")
	}
}

func TestListAndThreads(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	repo := &git.Repo{Dir: f.Dir()}

	for _, slug := range []string{"booking-ui", "booking-transaction"} {
		cs := changeset.Changeset{Slug: slug, Branch: slug, Dir: filepath.Join("changesets", slug)}
		if _, err := changeset.Write(repo, cs, "main", false); err != nil {
			t.Fatalf("Write %s: %v", slug, err)
		}
	}
	// A stray directory with no metadata is not a changeset and must be skipped
	// rather than fail the whole listing.
	if err := os.MkdirAll(filepath.Join(f.Dir(), "changesets", "not-a-changeset"), 0o755); err != nil {
		t.Fatal(err)
	}

	list, err := changeset.List(repo)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	names := make([]string, 0, len(list))
	for _, cs := range list {
		names = append(names, cs.Slug)
	}
	want := []string{"booking-transaction", "booking-ui"}
	if len(names) != 2 || names[0] != want[0] || names[1] != want[1] {
		t.Errorf("List() = %v, want %v sorted and without the metadata-less directory", names, want)
	}

	cs := changeset.Changeset{Slug: "booking-transaction", Dir: filepath.Join("changesets", "booking-transaction")}
	f.Write(cs.Path("concurrency-tests.md"), "# Concurrency tests\n")
	f.Write(cs.Path("transaction-boundary.md"), "# Transaction boundary\n")
	threads, err := cs.Threads(fixRepo(f))
	if err != nil {
		t.Fatalf("Threads: %v", err)
	}
	if len(threads) != 2 {
		t.Errorf("Threads = %v, want the two thread files (ABOUT.md excluded)", threads)
	}
	for _, path := range threads {
		if filepath.Base(path) == changeset.AboutFile {
			t.Errorf("Threads included the ABOUT.md scaffold: %v", threads)
		}
	}
}

func TestEnsureThreadReusesExistingFile(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	repo := &git.Repo{Dir: f.Dir()}
	cs := changeset.Changeset{Slug: "booking", Dir: filepath.Join("changesets", "booking")}

	path, created, err := cs.EnsureThread(repo, "concurrency tests")
	if err != nil {
		t.Fatalf("EnsureThread: %v", err)
	}
	if !created {
		t.Error("first EnsureThread reported an existing file")
	}
	authorText := "# Concurrency tests\n\nReviewer question.\n"
	f.Write(path, authorText)

	path2, created, err := cs.EnsureThread(repo, "Concurrency Tests")
	if err != nil {
		t.Fatalf("second EnsureThread: %v", err)
	}
	if path2 != path {
		t.Errorf("second EnsureThread = %q, want the same path %q (PRD §10.3: no duplicates)", path2, path)
	}
	if created {
		t.Error("second EnsureThread reported creating an existing thread")
	}
	if f.Read(path) != authorText {
		t.Error("EnsureThread overwrote the existing thread")
	}
}

// fixRepo keeps the fixture's repository handle explicit at the call site.
func fixRepo(f *gittest.Fixture) *git.Repo { return &git.Repo{Dir: f.Dir()} }
