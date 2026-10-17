package changeset_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/gittest"
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

func TestAboutTemplateAndTheDiscussionFileDiffer(t *testing.T) {
	cs := changeset.Changeset{Slug: "booking", Dir: filepath.Join("changesets", "booking")}
	about := changeset.AboutTemplate("booking")
	for _, heading := range []string{"# booking", "## Summary", "## What changed", "## Design decisions", "## Validation"} {
		if !strings.Contains(about, heading) {
			t.Errorf("ABOUT.md scaffold is missing %q; PRD §6 lists it:\n%s", heading, about)
		}
	}
	// The description carries the sections a reviewer reads; the discussion file
	// carries none of them, because the shape of a conversation is not the tool's to
	// decide. See discussion_test.go.
	if strings.Contains(changeset.DiscussionTemplate("booking"), "## ") {
		t.Error("the discussion scaffold offers sections")
	}
	if got := cs.AboutPath(); got != filepath.Join("changesets", "booking", "ABOUT.md") {
		t.Errorf("AboutPath = %q", got)
	}
	if got := cs.MetadataPath(); got != filepath.Join("changesets", "booking", "CHANGESET.yaml") {
		t.Errorf("MetadataPath = %q", got)
	}
}

// PRD §9.1: `init` creates deterministic scaffolding, is idempotent, and
// "should not destroy existing changeset data".
func TestWriteCreatesScaffoldingAndIsIdempotent(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	repo := &git.Repo{Dir: f.Dir()}

	cs := changeset.Changeset{Slug: "feature-booking", Branch: "feature/booking", Dir: filepath.Join("changesets", "feature-booking")}
	written, err := changeset.Write(repo, cs, changeset.WriteOptions{Base: "main"})
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
	written, err = changeset.Write(repo, cs, changeset.WriteOptions{Base: "main"})
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
	if _, err := changeset.Write(repo, cs, changeset.WriteOptions{Base: "main"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	authorText := "# booking\n\n## Summary\n\nThe author's own description, which must survive.\n"
	f.Write(cs.AboutPath(), authorText)
	f.Write(cs.MetadataPath(), "base: main\n")

	if _, err := changeset.Write(repo, cs, changeset.WriteOptions{Base: "main"}); err != nil {
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
	if _, err := changeset.Write(repo, cs, changeset.WriteOptions{Base: "booking-transaction"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := f.MetadataBase("booking-tests"); got != "booking-transaction" {
		t.Errorf("base = %q, want booking-transaction", got)
	}

	// The directory is uncommitted, so the checked-out revision is the one that knows about
	// it: resolution reads HEAD and adds what the working tree holds.
	res, err := changeset.ResolveCurrent(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("ResolveCurrent: %v", err)
	}
	if res.Selected == nil || res.Selected.Changeset.Base != "booking-transaction" ||
		res.Selected.Changeset.Slug != "booking-tests" {
		t.Errorf("resolution = %+v, want booking-tests on base booking-transaction", res.Selected)
	}
}

func TestWriteBaseConflict(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	repo := &git.Repo{Dir: f.Dir()}

	cs := changeset.Changeset{Slug: "booking", Branch: "booking", Dir: filepath.Join("changesets", "booking")}
	if _, err := changeset.Write(repo, cs, changeset.WriteOptions{Base: "main"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := changeset.Write(repo, cs, changeset.WriteOptions{Base: "trunk"}); !errors.Is(err, changeset.ErrBaseConflict) {
		t.Errorf("changing base without --set-base = %v, want ErrBaseConflict", err)
	}
	if got := f.MetadataBase("booking"); got != "main" {
		t.Errorf("refused Write changed base to %q, want main", got)
	}
	if _, err := changeset.Write(repo, cs, changeset.WriteOptions{Base: "trunk", SetBase: true}); err != nil {
		t.Fatalf("Write with setBase: %v", err)
	}
	if got := f.MetadataBase("booking"); got != "trunk" {
		t.Errorf("base = %q, want trunk after --set-base", got)
	}
}

// A detached HEAD has no branch to report, and an uninitialised branch resolves to the
// directory `init` would create — the value that makes ErrNoChangeset's hint useful.
func TestCurrentDetachedHeadAndMissingDirectory(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	repo := &git.Repo{Dir: f.Dir()}

	f.MustGit("checkout", "--detach")
	if _, err := changeset.Current(context.Background(), repo, ""); !errors.Is(err, changeset.ErrDetachedHead) {
		t.Errorf("Current on a detached HEAD = %v, want ErrDetachedHead", err)
	}

	f.MustGit("checkout", "-B", "uninitialised")
	cs, err := changeset.Current(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if cs.Exists {
		t.Error("Current reported a changeset that was never created")
	}
	if cs.Slug != "uninitialised" || cs.Dir != filepath.Join("changesets", "uninitialised") {
		t.Errorf("Current = %+v, want the deterministic directory for the branch", cs)
	}
	if _, err := changeset.RequireCurrent(context.Background(), repo, ""); err == nil {
		t.Error("RequireCurrent succeeded on a branch with no changeset")
	}
}

func TestActiveIDs(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	repo := &git.Repo{Dir: f.Dir()}

	for _, slug := range []string{"booking-ui", "booking-transaction"} {
		cs := changeset.Changeset{Slug: slug, Branch: slug, Dir: filepath.Join("changesets", slug)}
		if _, err := changeset.Write(repo, cs, changeset.WriteOptions{Base: "main"}); err != nil {
			t.Fatalf("Write %s: %v", slug, err)
		}
	}
	// A stray directory with no metadata. `List` used to filter these out so the queue
	// could ignore them; the queue classifies them now (see
	// TestReviewQueueIgnoresDirectoriesThatWereNeverOffered), so this primitive is a
	// tree listing and nothing more. It has to hold a file: git does not track empty
	// directories, so an empty one is not in any commit to list.
	if err := os.MkdirAll(filepath.Join(f.Dir(), "changesets", "not-a-changeset"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.Write(filepath.Join("changesets", "not-a-changeset", "NOTES.md"), "scratch\n")
	f.Commit("add the changeset directories")

	names, err := changeset.ActiveIDs(context.Background(), repo, "HEAD")
	if err != nil {
		t.Fatalf("ActiveIDs: %v", err)
	}
	want := []string{"booking-transaction", "booking-ui", "not-a-changeset"}
	if len(names) != len(want) {
		t.Fatalf("ActiveIDs = %v, want %v sorted", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("ActiveIDs = %v, want %v sorted", names, want)
			break
		}
	}

	// Reading the list must not author anything: a changeset gains its discussion file on
	// first use, not because something listed the directory.
	cs := changeset.Changeset{Slug: "booking-transaction", Dir: filepath.Join("changesets", "booking-transaction")}
	if _, err := os.Stat(filepath.Join(f.Dir(), cs.DiscussionPath())); !os.IsNotExist(err) {
		t.Errorf("ActiveIDs wrote %s; it reads, it does not author", cs.DiscussionPath())
	}
}

// fixRepo keeps the fixture's repository handle explicit at the call site.
func fixRepo(f *gittest.Fixture) *git.Repo { return &git.Repo{Dir: f.Dir()} }

// A changeset based on its own branch can never contain a commit, because the
// base moves with every commit the author makes. The test is by ref identity: a
// branch created moments ago shares its tip with its base legitimately.
func TestBaseIsOwnBranch(t *testing.T) {
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("a.txt", "a\n"))
	f.CreateBranch("feature/booking")
	f.Commit("work", gittest.WithFile("b.txt", "b\n"))
	repo := &git.Repo{Dir: f.Dir()}
	ctx := context.Background()

	cases := []struct {
		name   string
		base   string
		branch string
		want   bool
	}{
		{"main on main", "main", "main", true},
		{"main on main by full ref name", "refs/heads/main", "main", true},
		{"main on a branch off main", "main", "feature/booking", false},
		{"HEAD names the branch it is on", "HEAD", "feature/booking", true},
		{"HEAD does not name some other branch", "HEAD", "main", false},
		{"@ is another spelling of HEAD", "@", "feature/booking", true},
		{"own branch named as base", "feature/booking", "feature/booking", true},
		{"a commit sha names no branch", f.RevParse("main"), "main", false},
		{"a base that does not exist", "gone", "main", false},
		{"empty base", "", "main", false},
	}
	for _, tc := range cases {
		got, err := changeset.BaseIsOwnBranch(ctx, repo, tc.base, tc.branch)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: BaseIsOwnBranch(%q, %q) = %v, want %v", tc.name, tc.base, tc.branch, got, tc.want)
		}
	}
}

func TestValidateID(t *testing.T) {
	accepted := []string{"booking-transaction", "booking-transaction-v2", "JIRA-123", "a.b_c-1"}
	for _, id := range accepted {
		if err := changeset.ValidateID(id); err != nil {
			t.Errorf("ValidateID(%q) = %v, want it accepted as typed", id, err)
		}
	}
	rejected := map[string]string{
		"":                "empty",
		"booking v2":      "a space",
		"feature/booking": "a path separator",
		"..":              "a traversal",
		"-booking":        "a leading hyphen",
		"booking-":        "a trailing hyphen",
		"booking--v2":     "a doubled hyphen",
		" booking":        "leading whitespace",
		"booking\t":       "a tab",
		"booking,2":       "a comma",
		"booking\x00":     "a nul",
	}
	for id, why := range rejected {
		if err := changeset.ValidateID(id); err == nil {
			t.Errorf("ValidateID(%q) = nil, want refusal for %s", id, why)
		}
	}
}
