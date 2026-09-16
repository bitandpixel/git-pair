package survival

import (
	"reflect"
	"strings"
	"testing"
)

// Unit tests for the `git show -U0` parsing that the surviving-additions
// diagnostic is built on (PRD §19.1). The integration tests in check_test.go run
// the same rules against real commits; these pin the parser's handling of the
// cases the plumbing spike identified.

func addedLines(diff string) []Addition {
	return parseAddedLines(diff, binaryPaths(numstatOf(diff)))
}

// numstatOf synthesises the `--numstat` block a diff would produce, so the
// parser is exercised with the same pairing of inputs the production code uses.
func numstatOf(diff string) string {
	var b strings.Builder
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "Binary files ") {
			path := strings.TrimPrefix(line, "Binary files ")
			if i := strings.Index(path, " and "); i >= 0 {
				path = path[i+len(" and "):]
				path = strings.TrimSuffix(path, " differ")
				path = strings.TrimPrefix(path, "b/")
				b.WriteString("-\t-\t" + path + "\n")
			}
		}
	}
	return b.String()
}

// A single added line: git reports `@@ -2,0 +3 @@` for one insertion after line
// 2, so the added line is post-image line 3.
func TestParseAddedLinesSingleInsertion(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/service.go b/service.go",
		"index 111..222 100644",
		"--- a/service.go",
		"+++ b/service.go",
		"@@ -2,0 +3 @@ func main() {",
		"+// Please use a transaction here",
		"",
	}, "\n")

	got := parseAddedLines(diff, nil)
	want := []Addition{{Path: "service.go", Line: 3, Text: "// Please use a transaction here", Count: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseAddedLines = %#v, want %#v", got, want)
	}
}

// Two insertions in one hunk are reported at their own post-image lines.
func TestParseAddedLinesNumbersEveryInsertion(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/a.go b/a.go",
		"index 1..2 100644",
		"--- a/a.go",
		"+++ b/a.go",
		"@@ -1,0 +2,2 @@",
		"+first added",
		"+second added",
		"@@ -5,0 +8,1 @@",
		"+third added",
		"",
	}, "\n")

	got := parseAddedLines(diff, nil)
	want := []Addition{
		{Path: "a.go", Line: 2, Text: "first added", Count: 1},
		{Path: "a.go", Line: 3, Text: "second added", Count: 1},
		{Path: "a.go", Line: 8, Text: "third added", Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseAddedLines =\n%#v\nwant\n%#v", got, want)
	}
}

// Insertion at the very start of a file: `@@ -0,0 +1,2 @@`.
func TestParseAddedLinesAtStartOfFile(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/new.go b/new.go",
		"new file mode 100644",
		"index 000..111 100644",
		"--- /dev/null",
		"+++ b/new.go",
		"@@ -0,0 +1,2 @@",
		"+package main",
		"+// review comment",
		"",
	}, "\n")

	got := parseAddedLines(diff, nil)
	if len(got) != 2 {
		t.Fatalf("parseAddedLines = %#v, want two additions", got)
	}
	if got[0].Line != 1 || got[0].Text != "package main" {
		t.Errorf("first addition = %+v, want line 1 %q", got[0], "package main")
	}
	if got[1].Line != 2 || got[1].Text != "// review comment" {
		t.Errorf("second addition = %+v, want line 2 %q", got[1], "// review comment")
	}
}

// Blank additions cannot carry feedback and would dominate the report (PRD §19.2
// reports feedback, not whitespace), so they are dropped while non-blank lines
// that only differ in leading whitespace are kept verbatim.
func TestParseAddedLinesSkipsBlankLines(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/a.go b/a.go",
		"--- a/a.go",
		"+++ b/a.go",
		"@@ -1,0 +2,4 @@",
		"+",
		"+   ",
		"+\t\t",
		"+// only this one is real feedback",
		"",
	}, "\n")

	got := parseAddedLines(diff, nil)
	if len(got) != 1 || got[0].Text != "// only this one is real feedback" {
		t.Errorf("parseAddedLines = %#v, want only the non-blank addition", got)
	}
	if got[0].Count != 1 {
		t.Errorf("Count = %d, want 1", got[0].Count)
	}
}

// `\ No newline at end of file` is not content.
func TestParseAddedLinesSkipsNoNewlineMarker(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/a.md b/a.md",
		"--- a/a.md",
		"+++ b/a.md",
		"@@ -1,0 +2,2 @@",
		"+new line",
		"\\ No newline at end of file",
		"",
	}, "\n")

	got := parseAddedLines(diff, nil)
	if len(got) != 1 || got[0].Text != "new line" {
		t.Errorf("parseAddedLines = %#v, want just %q", got, "new line")
	}
}

// A rename under --no-renames is delete + add, so the added lines belong to the
// post-image path. The spike's rule "prefer the +++ header over diff --git"
// matters here because `diff --git a/old b/new` is ambiguous.
func TestParseAddedLinesUsesPostImagePath(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/old.go b/new.go",
		"new file mode 100644",
		"index 000..111",
		"--- /dev/null",
		"+++ b/new.go",
		"@@ -0,0 +1 @@",
		"+package main",
		"",
	}, "\n")

	got := parseAddedLines(diff, nil)
	if len(got) != 1 || got[0].Path != "new.go" {
		t.Errorf("parseAddedLines = %#v, want the post-image path new.go", got)
	}
}

// A deletion has no post-image, so it contributes no additions.
func TestParseAddedLinesDeletedFileContributesNothing(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/gone.go b/gone.go",
		"deleted file mode 100644",
		"index 111..000",
		"--- a/gone.go",
		"+++ /dev/null",
		"@@ -1,2 +0,0 @@",
		"-package main",
		"-// review comment",
		"",
	}, "\n")

	if got := parseAddedLines(diff, nil); len(got) != 0 {
		t.Errorf("parseAddedLines = %#v, want no additions for a deletion", got)
	}
}

// Reviewer-added code that is a binary blob cannot be compared as text, so it is
// skipped via --numstat rather than text scanning (spike finding 5).
func TestParseAddedLinesSkipsBinaryPaths(t *testing.T) {
	numstat := "-\t-\tassets/logo.png\n1\t0\tservice.go\n"
	binary := binaryPaths(numstat)
	if !binary["assets/logo.png"] {
		t.Fatal("binaryPaths did not flag the binary entry")
	}
	if binary["service.go"] {
		t.Error("binaryPaths flagged a text file")
	}

	diff := strings.Join([]string{
		"diff --git a/assets/logo.png b/assets/logo.png",
		"new file mode 100644",
		"index 000..111",
		"Binary files /dev/null and b/assets/logo.png differ",
		"diff --git a/service.go b/service.go",
		"--- a/service.go",
		"+++ b/service.go",
		"@@ -1,0 +2 @@",
		"+// real comment",
		"",
	}, "\n")

	got := parseAddedLines(diff, binary)
	if len(got) != 1 || got[0].Path != "service.go" {
		t.Errorf("parseAddedLines = %#v, want only the text file's addition", got)
	}
}

// Duplicate identical additions are reported once, with an honest count, so the
// report stays readable without hiding how much the reviewer added.
func TestParseAddedLinesDeduplicatesIdenticalLines(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/a_test.go b/a_test.go",
		"--- a/a_test.go",
		"+++ b/a_test.go",
		"@@ -1,0 +2,3 @@",
		"+t.Fatal(\"not implemented\")",
		"+t.Fatal(\"not implemented\")",
		"+t.Fatal(\"not implemented\")",
		"@@ -9,0 +13,1 @@",
		"+t.Fatal(\"not implemented\")",
		"",
	}, "\n")

	got := parseAddedLines(diff, nil)
	if len(got) != 1 {
		t.Fatalf("parseAddedLines = %#v, want one deduplicated addition", got)
	}
	if got[0].Count != 4 {
		t.Errorf("Count = %d, want 4 identical lines", got[0].Count)
	}
	if got[0].Path != "a_test.go" || got[0].Text != "t.Fatal(\"not implemented\")" {
		t.Errorf("addition = %+v", got[0])
	}
}

// Identical text in two different files stays two additions: the location is part
// of the identity.
func TestParseAddedLinesKeepsSameTextInDifferentFiles(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/a.go b/a.go",
		"--- a/a.go",
		"+++ b/a.go",
		"@@ -1,0 +2 @@",
		"+// same comment",
		"diff --git a/b.go b/b.go",
		"--- a/b.go",
		"+++ b/b.go",
		"@@ -1,0 +2 @@",
		"+// same comment",
		"",
	}, "\n")

	got := parseAddedLines(diff, nil)
	if len(got) != 2 {
		t.Fatalf("parseAddedLines = %#v, want one addition per file", got)
	}
	if got[0].Path == got[1].Path {
		t.Errorf("both additions have path %q", got[0].Path)
	}
}

func TestBinaryPaths(t *testing.T) {
	numstat := strings.Join([]string{
		"1\t0\tservice.go",
		"-\t-\tassets/logo.png",
		"0\t3\tgone.go",
		"",
	}, "\n")

	got := binaryPaths(numstat)
	if len(got) != 1 || !got["assets/logo.png"] {
		t.Errorf("binaryPaths = %v, want only assets/logo.png", got)
	}
}

func TestFindLineIsExactAndUntrimmed(t *testing.T) {
	blob := strings.Split("first\n  indented\nsecond\n", "\n")
	if got := findLine(blob, "indented"); got != 0 {
		t.Errorf("findLine matched a trimmed line at %d", got)
	}
	if got := findLine(blob, "  indented"); got != 2 {
		t.Errorf("findLine = %d, want 2 (1-based, no trailing newline element)", got)
	}
	if got := findLine(blob, "absent"); got != 0 {
		t.Errorf("findLine = %d, want 0 for an absent line", got)
	}
	// Duplicated lines report the first occurrence; the caller reports the
	// review's own occurrence count so it never claims uniqueness.
	dup := strings.Split("a\nb\nb\n", "\n")
	if got := findLine(dup, "b"); got != 2 {
		t.Errorf("findLine on duplicates = %d, want 2", got)
	}
}

// PRD §19 and plan decision D3: additions inside the changeset directory are
// conversation surface, everything else is code that must be resolved.
func TestIsArtifactPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"changesets/booking/ABOUT.md", true},
		{"changesets/booking/concurrency-tests.md", true},
		{"changesets/booking/CHANGESET.yaml", true},
		{"changesets", true},
		{"changesets.go", false},
		{"src/changesets/booking.md", false},
		{"service.go", false},
		{"README.md", false},
	}
	for _, tc := range tests {
		if got := isArtifactPath(tc.path); got != tc.want {
			t.Errorf("isArtifactPath(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestReportCleanAndTotal(t *testing.T) {
	report := Report{}
	if !report.Clean() || report.Total() != 0 {
		t.Errorf("empty report: Clean = %v, Total = %d", report.Clean(), report.Total())
	}
	report.Artifacts = []Addition{{Path: "changesets/booking/ABOUT.md"}}
	if !report.Clean() {
		t.Error("a report with only artifact survivals must still be Clean: artifacts do not block (plan D3)")
	}
	if report.Total() != 1 {
		t.Errorf("Total = %d, want 1", report.Total())
	}
	report.Code = []Addition{{Path: "service.go"}}
	if report.Clean() {
		t.Error("a report with a code survival must not be Clean")
	}
}

func TestShort(t *testing.T) {
	if got := short("0123456789abcdef"); got != "0123456" {
		t.Errorf("short = %q, want the 7-character abbreviation", got)
	}
	if got := short("abc"); got != "abc" {
		t.Errorf("short = %q, want it unchanged", got)
	}
}
