package survival_test

import (
	"context"
	"strings"
	"testing"

	"gitpair/internal/git"
	"gitpair/internal/gittest"
	"gitpair/internal/survival"
)

// Integration tests for the surviving-review-additions diagnostic (PRD §19). Each
// case is a real repository, and the assertions are about which additions the
// check reports for real commits.
//
// The caveats exercised here are the ones the plumbing spike identified as risky:
// blank lines, binary files, deleted paths, renames, duplicate identical lines, a
// review commit that is the repository's root commit, and an empty review diff.

const slug = "booking-transaction"

type fixture struct {
	f    *gittest.Fixture
	repo *git.Repo
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := gittest.New(t)
	f.Commit("seed", gittest.WithFile("main.go", "package main\n\nfunc main() {}\n"))
	f.CreateBranch(slug)
	f.CommitChangeset(slug, "main")
	// The files a review will annotate already exist, so a review commit's only
	// addition is the reviewer's line rather than a whole new file.
	f.Commit("implement", gittest.WithFiles(map[string]string{
		"service.go":      "package main\n\nfunc Lock() {}\n",
		"handler.go":      "package main\n\nfunc Serve() {}\n",
		"service_test.go": "package main\n",
	}))
	return &fixture{f: f, repo: &git.Repo{Dir: f.Dir()}}
}

func (x *fixture) check(t *testing.T, review string) survival.Report {
	t.Helper()
	report, err := survival.Check(context.Background(), x.repo, review)
	if err != nil {
		t.Fatalf("Check(%s): %v", review, err)
	}
	return report
}

// codePaths and artifactPaths make assertion failures readable.
func codePaths(report survival.Report) []string {
	var out []string
	for _, a := range report.Code {
		out = append(out, a.Path)
	}
	return out
}

func artifactPaths(report survival.Report) []string {
	var out []string
	for _, a := range report.Artifacts {
		out = append(out, a.Path)
	}
	return out
}

func findAddition(list []survival.Addition, path, text string) (survival.Addition, bool) {
	for _, a := range list {
		if a.Path == path && a.Text == text {
			return a, true
		}
	}
	return survival.Addition{}, false
}

// lineOf returns the 1-based line a text occupies in a working-tree file, which is
// HEAD's copy while the tree is clean. PRD §19.2 reports `path:line` locations, so
// the reported line has to be the real one.
func lineOf(t *testing.T, x *fixture, path, text string) int {
	t.Helper()
	for i, line := range strings.Split(x.f.FileAt("HEAD", path), "\n") {
		if line == text {
			return i + 1
		}
	}
	t.Fatalf("%q not found in HEAD:%s", text, path)
	return 0
}

// A reviewer-added line the author leaves untouched must be reported; resolving it
// is what unblocks `change ready` (PRD §19.2).
func TestCheckReportsUntouchedReviewAdditions(t *testing.T) {
	x := newFixture(t)

	const untouched = "// What happens if these execute concurrently?"
	const resolved = "// Please use a transaction here"
	review := x.f.CommitReviewMarker(slug, "block", gittest.WithFiles(map[string]string{
		"service.go": "package main\n\n" + resolved + "\nfunc Lock() {}\n",
		"handler.go": "package main\n\n" + untouched + "\nfunc Serve() {}\n",
	}))

	// The author rewrites one comment and leaves the other alone.
	x.f.Commit("address one comment", gittest.WithFile("service.go",
		"package main\n\nfunc Lock() { transaction() }\n"))

	report := x.check(t, review)
	if report.Clean() {
		t.Fatalf("report is Clean, want the untouched addition to block: %+v", report)
	}
	if len(report.Code) != 1 {
		t.Fatalf("Code = %+v, want exactly the untouched addition", report.Code)
	}
	got := report.Code[0]
	if got.Path != "handler.go" || got.Text != untouched {
		t.Errorf("reported %+v, want handler.go %q", got, untouched)
	}
	if want := lineOf(t, x, "handler.go", untouched); got.Line != want {
		t.Errorf("reported line %d, want %d (PRD §19.2 reports the location at HEAD)", got.Line, want)
	}
	if len(report.Artifacts) != 0 {
		t.Errorf("Artifacts = %+v, want none: nothing was added inside changesets/", report.Artifacts)
	}
	if report.ReviewSHA != review {
		t.Errorf("ReviewSHA = %s, want %s", report.ReviewSHA, review)
	}

	// Resolving the surviving line unblocks the check.
	x.f.Commit("resolve the rest", gittest.WithFile("handler.go", "package main\n\nfunc Serve() { ctx() }\n"))
	after := x.check(t, review)
	if !after.Clean() {
		t.Errorf("after resolving, Code = %+v, want empty", after.Code)
	}
	if len(after.Artifacts) != 0 {
		t.Errorf("after resolving, Artifacts = %+v, want empty", after.Artifacts)
	}
}

// Blank and whitespace-only additions are excluded: they cannot carry feedback and
// would otherwise make the check a wallpaper (plan risk table).
func TestCheckSkipsBlankLineAdditions(t *testing.T) {
	x := newFixture(t)

	review := x.f.CommitReviewMarker(slug, "block", gittest.WithFile("service.go",
		"package main\n\n\n\nfunc Lock() {}\n\n\n"))

	report := x.check(t, review)
	if !report.Clean() {
		t.Errorf("Code = %+v, want empty: blank lines are not feedback", report.Code)
	}
	if report.Total() != 0 {
		t.Errorf("Total = %d, want 0", report.Total())
	}
	if report.AddedLines != 0 {
		t.Errorf("AddedLines = %d, want 0: blank additions are not counted", report.AddedLines)
	}

	// A real comment added alongside the blanks is still reported.
	withText := x.f.CommitReviewMarker(slug, "block", gittest.WithFile("handler.go",
		"package main\n\n// Please name this variable\nfunc Serve() {}\n\n\n"))
	report = x.check(t, withText)
	if len(report.Code) != 1 || report.Code[0].Text != "// Please name this variable" {
		t.Errorf("Code = %+v, want only the non-blank addition", report.Code)
	}
}

// Binary files are skipped via `--numstat`, not by text scanning: git emits
// "Binary files ... differ" with no `+` lines, and a `diff.binary` change must not
// be able to corrupt the check (spike finding 5).
func TestCheckSkipsBinaryFiles(t *testing.T) {
	x := newFixture(t)

	const comment = "// is this logo the final one?"
	review := x.f.CommitReviewMarker(slug, "block", gittest.WithFiles(map[string]string{
		"assets/logo.png": "\x89PNG\x00\x01\x02\x00binary\x00payload\n",
		"handler.go":      "package main\n\n" + comment + "\nfunc Serve() {}\n",
	}))

	report := x.check(t, review)
	for _, path := range append(codePaths(report), artifactPaths(report)...) {
		if path == "assets/logo.png" {
			t.Errorf("report contains the binary file: %+v", report.Code)
		}
	}
	if len(report.Code) != 1 || report.Code[0].Path != "handler.go" {
		t.Errorf("Code = %+v, want only handler.go", report.Code)
	}

	// A binary-only review has nothing to block on.
	x.f.Commit("clear the comment", gittest.WithFile("handler.go", "package main\n\nfunc Serve() {}\n"))
	binaryOnly := x.f.CommitReviewMarker(slug, "block", gittest.WithFile("assets/icon.png",
		"\x89PNG\x00\x01other\x00payload\n"))
	report = x.check(t, binaryOnly)
	if !report.Clean() {
		t.Errorf("Code = %+v, want empty for a binary-only review", report.Code)
	}
}

// An addition in a file that no longer exists at HEAD cannot survive, and must not
// turn into a spurious report or an error from a failed blob read.
func TestCheckFileDeletedAtHead(t *testing.T) {
	x := newFixture(t)

	const deletedComment = "// this whole file should go"
	const keptComment = "// keep this one"
	review := x.f.CommitReviewMarker(slug, "block", gittest.WithFiles(map[string]string{
		"obsolete.go": "package main\n\n" + deletedComment + "\nfunc Old() {}\n",
		"service.go":  "package main\n\n" + keptComment + "\nfunc Lock() {}\n",
	}))

	x.f.Remove("obsolete.go")
	x.f.Commit("delete obsolete file")

	report := x.check(t, review)
	if len(report.Code) != 1 || report.Code[0].Path != "service.go" {
		t.Fatalf("Code = %+v, want only the addition in the file that still exists", report.Code)
	}
	for _, a := range report.Code {
		if a.Path == "obsolete.go" {
			t.Errorf("report claims a surviving addition in a deleted file: %+v", a)
		}
	}

	// Deleting everything the review touched leaves a clean report, not an error.
	x.f.Remove("service.go")
	x.f.Commit("delete service too")
	if report := x.check(t, review); !report.Clean() {
		t.Errorf("Code = %+v, want empty when every reviewed file is deleted", report.Code)
	}
}

// A rename after the review is read as delete + add (spike finding 4, `--no-renames`),
// so the review's additions are keyed to a path that no longer exists. The check
// must not report a surviving addition for a path that is gone at HEAD, and must not
// fail. Following a rename instead would require rename detection, which the
// findings document deliberately rejects; the surviving text is still visible in the
// `--unreviewed` span, where the rename shows up as a deletion plus an addition.
func TestCheckRenamedFileHasNoAdditionsAtTheOldPath(t *testing.T) {
	x := newFixture(t)

	const comment = "// does this need a lock too?"
	review := x.f.CommitReviewMarker(slug, "block", gittest.WithFile("service.go",
		"package main\n\n"+comment+"\nfunc Lock() {}\n"))

	x.f.Rename("service.go", "lock/service.go")
	x.f.Commit("rename into a package")

	if x.f.HasFile("HEAD", "service.go") {
		t.Fatal("scenario did not rename the file: service.go still exists at HEAD")
	}
	report := x.check(t, review)
	for _, a := range append(report.Code, report.Artifacts...) {
		if a.Path == "service.go" {
			t.Errorf("report keys a surviving addition to the deleted path service.go: %+v", a)
		}
		if !x.f.HasFile("HEAD", a.Path) {
			t.Errorf("report keys a surviving addition to %s, which does not exist at HEAD", a.Path)
		}
	}
	// Whatever the check does report must be a real location: a path that exists at
	// HEAD and the line the text actually occupies there.
	for _, a := range append(report.Code, report.Artifacts...) {
		if !x.f.HasFile("HEAD", a.Path) {
			t.Errorf("report keys a surviving addition to %s, which does not exist at HEAD", a.Path)
			continue
		}
		if got := lineOf(t, x, a.Path, a.Text); a.Line != got {
			t.Errorf("%s:%d is not where %q lives at HEAD (%d)", a.Path, a.Line, a.Text, got)
		}
	}
}

// Three identical reviewer lines are one reported addition carrying an honest
// count, and they still block.
func TestCheckDuplicateIdenticalAddedLines(t *testing.T) {
	x := newFixture(t)

	const dup = "t.Fatal(\"not implemented\")"
	review := x.f.CommitReviewMarker(slug, "block", gittest.WithFile("service_test.go",
		"package main\n\n"+dup+"\n"+dup+"\n"+dup+"\n"))

	report := x.check(t, review)
	if len(report.Code) != 1 {
		t.Fatalf("Code = %+v, want one deduplicated entry", report.Code)
	}
	got := report.Code[0]
	if got.Count != 3 {
		t.Errorf("Count = %d, want 3: the report must not hide how many copies the review added", got.Count)
	}
	if report.ReviewShort != review[:7] {
		t.Errorf("ReviewShort = %q, want the 7-character abbreviation %s", report.ReviewShort, review[:7])
	}
	// The block is reported at a real line of the file at HEAD.
	if want := lineOf(t, x, "service_test.go", dup); got.Line != want {
		t.Errorf("Line = %d, want %d", got.Line, want)
	}

	// Removing two of the three copies still leaves the line surviving.
	x.f.Commit("delete two copies", gittest.WithFile("service_test.go",
		"package main\n\n"+dup+"\n"))
	after := x.check(t, review)
	if len(after.Code) != 1 || after.Code[0].Count != 3 {
		t.Errorf("Code = %+v, want the same deduplicated entry (one copy is enough to block)", after.Code)
	}
}

// A review submission may be the repository's root commit: it has no parent, so the
// check must not build its diff from `<review>^`.
func TestCheckReviewCommitIsRepositoryRootCommit(t *testing.T) {
	f := gittest.New(t)
	const comment = "// first commit is already a review"
	review := f.CommitReviewMarker(slug, "block", gittest.WithFiles(map[string]string{
		"changesets/booking-transaction/CHANGESET.yaml": "base: main\n",
		"service.go": "package main\n\n" + comment + "\n",
	}))
	if got := f.ParentCount(review); got != 0 {
		t.Fatalf("review commit has %d parents, want 0 (root commit scenario)", got)
	}

	repo := &git.Repo{Dir: f.Dir()}
	report, err := survival.Check(context.Background(), repo, review)
	if err != nil {
		t.Fatalf("Check on a root-commit review: %v", err)
	}
	// Every non-blank line of a root commit is an addition, so the new file's own
	// `package main` is reported next to the reviewer's comment.
	if _, ok := findAddition(report.Code, "service.go", comment); !ok {
		t.Errorf("Code = %+v, want the review's source comment", report.Code)
	}
	for _, a := range report.Code {
		if a.Path != "service.go" {
			t.Errorf("Code = %+v, want source additions only", report.Code)
		}
	}
	if len(report.Artifacts) != 1 || report.Artifacts[0].Path != "changesets/booking-transaction/CHANGESET.yaml" {
		t.Errorf("Artifacts = %+v, want the changeset metadata line", report.Artifacts)
	}
	if report.AddedLines != 3 {
		t.Errorf("AddedLines = %d, want 3 non-blank additions", report.AddedLines)
	}

	// Resolving the review's lines clears the report even though the review is still
	// the root commit.
	f.Write("service.go", "func Lock() {}\n")
	f.Commit("respond to the review")
	if report := mustCheck(t, repo, review); !report.Clean() {
		t.Errorf("Code = %+v, want empty after the comment was replaced", report.Code)
	}
}

func mustCheck(t *testing.T, repo *git.Repo, review string) survival.Report {
	t.Helper()
	report, err := survival.Check(context.Background(), repo, review)
	if err != nil {
		t.Fatalf("Check(%s): %v", review, err)
	}
	return report
}

// An approval on a clean tree is a legitimate review with no changes at all
// (PRD §10.4), so the diagnostic must produce an empty, clean report rather than
// failing on an empty diff.
func TestCheckEmptyReviewDiff(t *testing.T) {
	x := newFixture(t)

	review := x.f.CommitReviewMarker(slug, "approve")

	report := x.check(t, review)
	if !report.Clean() {
		t.Errorf("Code = %+v, want empty", report.Code)
	}
	if report.Total() != 0 || report.AddedLines != 0 {
		t.Errorf("Total = %d, AddedLines = %d, want 0/0", report.Total(), report.AddedLines)
	}
	if report.ReviewSHA != review || report.ReviewShort != review[:7] {
		t.Errorf("report identifies %s/%s, want %s/%s", report.ReviewSHA, report.ReviewShort, review, review[:7])
	}
	// A deletion-only review also has no additions to survive.
	deletion := x.f.CommitReviewMarker(slug, "block", gittest.WithFile("main.go", "package main\n"))
	report = x.check(t, deletion)
	if !report.Clean() {
		t.Errorf("Code = %+v, want empty for a deletion-only review", report.Code)
	}
}

// Plan decision D3: surviving additions inside `changesets/` are reported as review
// artifacts and never block, while surviving additions anywhere else block.
func TestCheckChangesetArtifactsAreNonBlockingAndCodeBlocks(t *testing.T) {
	x := newFixture(t)

	const threadLine = "I'm not convinced the test makes these overlap."
	const codeLine = "// Please use a transaction here"
	about := x.f.ChangesetFile(slug, "ABOUT.md")
	review := x.f.CommitReviewMarker(slug, "block", gittest.WithFiles(map[string]string{
		"changesets/" + slug + "/concurrency-tests.md": "# Concurrency tests\n\n" + threadLine + "\n",
		"changesets/" + slug + "/ABOUT.md":             about + "\n## Review note\n\nPlease document the lock scope.\n",
		"service.go":                                   "package main\n\n" + codeLine + "\nfunc Lock() {}\n",
	}))

	report := x.check(t, review)
	if len(report.Artifacts) < 2 {
		t.Fatalf("Artifacts = %+v, want the thread and ABOUT.md additions", report.Artifacts)
	}
	if _, ok := findAddition(report.Artifacts, "changesets/"+slug+"/concurrency-tests.md", threadLine); !ok {
		t.Errorf("Artifacts = %+v, want the reviewer's thread line", report.Artifacts)
	}
	if len(report.Code) != 1 || report.Code[0].Path != "service.go" {
		t.Fatalf("Code = %+v, want only the source comment", report.Code)
	}
	if report.Clean() {
		t.Error("Clean = true, want false while a source comment survives")
	}

	// Resolving the code line unblocks even though every artifact line still
	// survives: a thread answered by appending keeps the reviewer's text (this is
	// exactly the false-positive the findings document measured).
	x.f.Commit("respond in the thread and fix the code", gittest.WithFiles(map[string]string{
		"service.go": "package main\n\nfunc Lock() { transaction() }\n",
		"changesets/" + slug + "/concurrency-tests.md": "# Concurrency tests\n\n" + threadLine +
			"\n\n## Response\n\nI synchronise both transactions before the read.\n",
	}))

	after := x.check(t, review)
	if !after.Clean() {
		t.Errorf("Clean = false after resolving the code: Code = %+v", after.Code)
	}
	if len(after.Artifacts) == 0 {
		t.Error("Artifacts is empty: surviving reviewer text must still be reported, not silenced")
	}
	if _, ok := findAddition(after.Artifacts, "changesets/"+slug+"/concurrency-tests.md", threadLine); !ok {
		t.Errorf("Artifacts = %+v, want the answered thread's original line reported", after.Artifacts)
	}
}

// PRD §19.1: "The diagnostic should operate on additions from the latest review
// commit only."
func TestCheckOnlyReportsTheReviewItWasGiven(t *testing.T) {
	x := newFixture(t)

	const first = "// first review comment"
	const second = "// second review comment"
	firstReview := x.f.CommitReviewMarker(slug, "block", gittest.WithFile("service.go",
		"package main\n\n"+first+"\nfunc Lock() {}\n"))
	x.f.Commit("fix the first comment", gittest.WithFile("service.go", "package main\n\nfunc Lock() {}\n"))
	secondReview := x.f.CommitReviewMarker(slug, "block", gittest.WithFile("handler.go",
		"package main\n\n"+second+"\nfunc Serve() {}\n"))

	report := x.check(t, secondReview)
	if len(report.Code) != 1 || report.Code[0].Text != second {
		t.Fatalf("Code = %+v, want only the latest review's addition", report.Code)
	}
	if report.ReviewSHA != secondReview {
		t.Errorf("ReviewSHA = %s, want %s", report.ReviewSHA, secondReview)
	}

	// The first review's additions were resolved, so checking it reports nothing;
	// it is never merged into the latest review's report.
	if older := x.check(t, firstReview); !older.Clean() {
		t.Errorf("older review report Code = %+v, want empty", older.Code)
	}
}

// Reviewer additions to ABOUT.md alone must not block `change ready`: PRD §6 makes
// ABOUT.md the shared description the reviewer and author both edit.
func TestCheckAboutOnlySurvivalDoesNotBlock(t *testing.T) {
	x := newFixture(t)

	about := x.f.ChangesetFile(slug, "ABOUT.md")
	review := x.f.CommitReviewMarker(slug, "feedback", gittest.WithFile(
		"changesets/"+slug+"/ABOUT.md", about+"\n## Review note\n\nRename Lock for clarity.\n"))

	report := x.check(t, review)
	if !report.Clean() {
		t.Errorf("Clean = false for an ABOUT.md-only review: Code = %+v", report.Code)
	}
	if len(report.Artifacts) == 0 {
		t.Error("the surviving ABOUT.md additions were not reported at all")
	}
}
