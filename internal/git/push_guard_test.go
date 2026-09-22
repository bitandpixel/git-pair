package git

import (
	"strings"
	"testing"
)

// The audited push helper (PRD §26) is worth its exception only if it refuses everything the exception
// does not grant. These assertions are per argument rather than per rule: the arguments named in the
// amendment — `--force`, `--delete`, `--mirror`, and the rest — are the ones a future caller might reach
// for to "resolve" a conflict, so each is asserted by name, not by membership in a rule that happens to
// cover it.

func TestPublishCannotLeaveTheNamespace(t *testing.T) {
	good := "refs/git-pair/integrations/booking:refs/git-pair/integrations/booking"
	if err := pushScopeError("origin", good); err != nil {
		t.Fatalf("the helper refuses its own happy path: %v", err)
	}

	// Every argument the amendment names, arriving as a refspec — which is the only way a caller can
	// reach git with one, since the argument vector is built here.
	for _, arg := range pushForbiddenArgs {
		err := pushScopeError("origin", good, arg)
		if err == nil {
			t.Errorf("push accepts %q; the helper takes no options", arg)
			continue
		}
		if !strings.Contains(err.Error(), "push refused") {
			t.Errorf("pushScopeError(%q) = %v, want an ErrPushScope refusal", arg, err)
		}
	}

	// Anything option-shaped is refused, named or not: the list above is a courtesy to the reader, and
	// the rule underneath it is what actually holds.
	for _, arg := range []string{"--upload-pack=evil", "-j9", "--", "---"} {
		if err := pushScopeError("origin", arg); err == nil {
			t.Errorf("push accepts %q, which begins like an option", arg)
		}
	}

	for _, tc := range []struct {
		name string
		spec string
		want string
	}{
		{"branch", "refs/heads/booking:refs/heads/booking", "refs/git-pair/"},
		{"tag", "refs/tags/v1:refs/tags/v1", "refs/git-pair/"},
		{"remote side only outside", "refs/git-pair/archive/booking:refs/heads/booking", "refs/git-pair/"},
		{"local side only outside", "refs/heads/booking:refs/git-pair/archive/booking", "refs/git-pair/"},
		{"the root itself", "refs/git-pair:refs/git-pair", "refs/git-pair/"},
		{"a near neighbour", "refs/git-pair-evil/x:refs/git-pair-evil/x", "refs/git-pair/"},
		{"HEAD", "refs/git-pair/archive/booking:HEAD", "refs/git-pair/"},
		{"delete", "refs/git-pair/archive/booking:", "delete or move"},
		{"create from nothing", ":refs/git-pair/archive/booking", "delete or move"},
		{"no colon", "refs/git-pair/archive/booking", "src:dst"},
		{"forced update", "+refs/git-pair/archive/booking:refs/git-pair/archive/booking", "forces an update"},
		{"whitespace smuggling", "refs/git-pair/archive/x refs/heads/y:refs/git-pair/archive/x", "whitespace"},
		{"empty", "", "empty refspec"},
	} {
		err := pushScopeError("origin", tc.spec)
		if err == nil {
			t.Errorf("%s: push accepted %q", tc.name, tc.spec)
			continue
		}
		if !anyContains(err.Error(), strings.Split(tc.want, "|")) {
			t.Errorf("%s: refusal says %q, want one of %q", tc.name, err.Error(), tc.want)
		}
	}

	// The remote is an argument too: a caller cannot smuggle flags through it either, and an empty one is
	// a push to nowhere rather than a push to the default.
	if err := pushScopeError("", good); err == nil {
		t.Error("push accepted an empty remote")
	}
	if err := pushScopeError("--force", good); err == nil {
		t.Error("push accepted an option as the remote name")
	}
	if err := pushScopeError("origin"); err == nil {
		t.Error("push accepted no refspec at all")
	}
}

// The report is what makes the half-state reportable, so its parsing is asserted here rather than left to
// the command's tests to discover. This is the output git 2.43 gives for a push where the server took one
// refspec and refused the other — the case the whole conflict policy is about.
func TestPushReportSeparatesTheHalves(t *testing.T) {
	out := "To ../remote.git\n" +
		"*\trefs/git-pair/archive/y:refs/git-pair/archive/y\t[new reference]\n" +
		"!\trefs/git-pair/integrations/z:refs/git-pair/integrations/z\t[remote rejected] (hook declined)\n" +
		"Done\n"
	report := parsePushPorcelain(out)
	if len(report.Outcomes) != 2 {
		t.Fatalf("parsed %d outcomes from:\n%s", len(report.Outcomes), out)
	}
	if report.Outcomes[0].Ref != "refs/git-pair/archive/y" || !report.Outcomes[0].OK {
		t.Errorf("first outcome = %+v, want the accepted archive ref", report.Outcomes[0])
	}
	rejected := report.Rejected()
	if len(rejected) != 1 || rejected[0].Ref != "refs/git-pair/integrations/z" || rejected[0].Summary != "hook declined" {
		t.Errorf("rejected = %+v, want the integration ref with git's reason", rejected)
	}
	if report.Rejected()[0].Flag != "[remote rejected]" {
		t.Errorf("flag = %q, want git's own word", report.Rejected()[0].Flag)
	}
}

func anyContains(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
