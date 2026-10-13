package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `check` is where a repository's own conditions belong to the verdict, because `check` writes nothing. A
// pre-hook beside a marker refuses an operation; a pre-hook beside the gate contributes a reason to an
// answer, and a post-hook here — unlike a post-hook beside a marker — can join the verdict, since there is
// nothing to reverse. The contract that has to survive is the one CI already depends on:
// `git pair check --json | jq -e '.ready and .integrating'` still answers the whole question, and the reason
// that says who refused names a file rather than a condition of the gate.
func TestPreCheckHooksJoinTheVerdict(t *testing.T) {
	t.Run("a failing hook refuses the gate with itself as the reason", func(t *testing.T) {
		f, slug, _, _ := approvedChangeset(t)
		putHook(t, f, "clone", "pre-check", "01-suite", failHook("4 tests failed"))

		res := runIn(t, f.Dir(), "check", "--json")
		// `check --json` carries its verdict in `ready` and leaves the exit code alone; the human form
		// is the one that refuses with 1. Both halves are asserted below, because a reader who only
		// ever runs the JSON form should not have to remember which of the two carries the answer.
		if res.code != exitOK {
			t.Fatalf("check --json exit = %d, want %d (the verdict travels in `ready`)\nstdout: %s",
				res.code, exitOK, res.stdout)
		}
		// The proof is that stdout parses at all. A hook's 400 lines of build log on stdout would
		// make the verdict unreadable to the job that came to read it, which is the failure mode a
		// hook inside a gate is most likely to cause.
		var out map[string]any
		if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
			t.Fatalf("check --json is not parseable with a hook configured: %v\n%s", err, res.stdout)
		}
		if out["ready"] != false {
			t.Errorf("ready = %v, want false", out["ready"])
		}
		reasons, _ := out["reasons"].([]any)
		if len(reasons) != 1 {
			t.Fatalf("reasons = %v, want the hook alone", reasons)
		}
		// The wording has to say who refused. A reader who takes "01-suite failed" for a git-pair
		// condition goes looking for a marker that is not missing.
		if !strings.Contains(reasons[0].(string), "pre-check") || !strings.Contains(reasons[0].(string), "01-suite") {
			t.Errorf("reason = %q, want it to name the point and the hook", reasons[0])
		}
		if !strings.Contains(reasons[0].(string), "exit 1") {
			t.Errorf("reason = %q, want the exit status the hook returned", reasons[0])
		}
		mustContain(t, res.stderr, "4 tests failed", "the hook's own output has to reach the reader")
		if strings.Contains(res.stdout, "4 tests failed") {
			t.Error("hook output reached stdout, where the verdict lives")
		}
		// The verdict still says which changeset it is about, so the reason is not the only thing
		// a reader has to go on.
		if out["changeset"] != slug {
			t.Errorf("changeset = %v, want %s", out["changeset"], slug)
		}

		// And the form a person runs at the prompt refuses the way every other refusal does.
		human := runIn(t, f.Dir(), "check")
		if human.code != exitRefusal {
			t.Errorf("`check` exit = %d, want %d", human.code, exitRefusal)
		}
		mustContain(t, human.stdout, "NOT READY:", "the human form states the verdict as its output")
		mustContain(t, human.stdout, "01-suite", "and names who refused among the reasons")
	})

	t.Run("a passing hook leaves the verdict alone and is reported", func(t *testing.T) {
		f, _, _, _ := approvedChangeset(t)
		putHook(t, f, "clone", "pre-check", "01-suite", passHook)

		res := runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check", "--json")

		var out map[string]any
		if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
			t.Fatalf("check --json is not parseable: %v\n%s", err, res.stdout)
		}
		if out["ready"] != true {
			t.Errorf("ready = %v, want true with a passing hook: %v", out["ready"], out["reasons"])
		}
		hooks := res.jsonList(t, "hooks")
		if len(hooks) != 1 || hooks[0].(map[string]any)["status"] != "passed" {
			t.Errorf("hooks = %v, want one passed entry", hooks)
		}
	})

	t.Run("the gate's own reasons and the hook's are reported together", func(t *testing.T) {
		f, _, reviewed, _ := approvedChangeset(t)
		// Content moved since the approval, so the gate has a reason of its own — and the hook has
		// one too. The command's principle is that one run names every reason the merge would be
		// wrong; a gate that reports one per run turns a two-minute fix into a round trip each.
		f.Write("service.go", "package service\n")
		f.Commit("work the review never saw")
		putHook(t, f, "clone", "pre-check", "01-suite", failHook("no"))

		res := runIn(t, f.Dir(), "check", "--json")
		if res.code != exitOK {
			t.Fatalf("exit = %d, want %d (the verdict travels in `ready`)", res.code, exitOK)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
			t.Fatalf("check --json is not parseable: %v\n%s", err, res.stdout)
		}
		joined := ""
		for _, r := range out["reasons"].([]any) {
			joined += r.(string) + "\n"
		}
		if !strings.Contains(joined, "01-suite") {
			t.Errorf("reasons = %q, want the hook among them", joined)
		}
		if !strings.Contains(joined, reviewed[:7]) && !strings.Contains(joined, "since") {
			t.Errorf("reasons = %q, want git-pair's own condition kept beside the hook's", joined)
		}
	})

	t.Run("a hook that timed out says so rather than that it failed", func(t *testing.T) {
		f, _, _, _ := approvedChangeset(t)
		putHook(t, f, "clone", "pre-check", "01-slow", "#!/bin/sh\nsleep 30\nexit 0\n")
		t.Setenv("GIT_PAIR_HOOK_TIMEOUT", "1")

		res := runIn(t, f.Dir(), "check", "--json")
		if res.code != exitOK {
			t.Fatalf("exit = %d, want %d", res.code, exitOK)
		}
		mustContain(t, res.stdout, "deadline", "the reason must say the hook ran out of time")
		mustContain(t, res.stdout, `"ready": false`, "and the verdict must say so")
	})

	t.Run("an untrusted tracked hook does not refuse the gate", func(t *testing.T) {
		f, _, _, _ := approvedChangeset(t)
		// Consent is not a merge condition. A clone nobody has accepted yet must still get a verdict
		// from `check`, or the first act of cloning turns into a build failure.
		putHook(t, f, "tracked", "pre-check", "01-suite", failHook("no"))

		runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check", "--json")
	})

	t.Run("there is no flag that runs the merge over a failing hook", func(t *testing.T) {
		f, _, _, _ := approvedChangeset(t)
		putHook(t, f, "clone", "pre-check", "01-suite", failHook("no"))

		// `change ready` and `review submit` carry an override for the author's and reviewer's own
		// judgement. `check` deliberately has none, for the reason §19.3 gives: the command that runs
		// the gate is not the place a policy gets relaxed by whoever is standing at it. An invented
		// flag has to be refused as a usage error rather than quietly ignored, or the next reader
		// assumes it works.
		res := runIn(t, f.Dir(), "check", "--allow-hook-failure")
		if res.code != exitUsage {
			t.Errorf("exit = %d, want %d (an unknown flag is a usage error, not a refusal)\nstderr: %s",
				res.code, exitUsage, res.stderr)
		}
	})
}

// TestPostCheckHooksJoinTheVerdict is the asymmetry worth stating out loud. A post-hook beside a marker cannot
// reverse the transition and does not try; a post-hook beside `check` has nothing to reverse, so its failure
// belongs to the verdict like any other condition. The shape it must never take is a nonzero exit beside
// `ready: true` — a pipeline would be handed two answers to one question.
func TestPostCheckHooksJoinTheVerdict(t *testing.T) {
	t.Run("a failing post-hook refuses the verdict it follows", func(t *testing.T) {
		f, _, _, _ := approvedChangeset(t)
		putHook(t, f, "clone", "post-check", "01-artifact", failHook("artifact missing"))

		res := runIn(t, f.Dir(), "check", "--json")
		if res.code != exitOK {
			t.Fatalf("check --json exit = %d, want %d", res.code, exitOK)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
			t.Fatalf("check --json is not parseable: %v\n%s", err, res.stdout)
		}
		if out["ready"] != false {
			t.Fatalf("ready = %v with a failing post-check hook: the field and the exit code must answer the same question",
				out["ready"])
		}
		if _, ok := out["next_action"]; ok && out["next_action"] != nil && out["next_action"] != "" {
			t.Errorf("next_action = %v, want it gone: it was written for a verdict that no longer holds", out["next_action"])
		}
		joined := ""
		for _, r := range out["reasons"].([]any) {
			joined += r.(string) + "\n"
		}
		if !strings.Contains(joined, "post-check") || !strings.Contains(joined, "01-artifact") {
			t.Errorf("reasons = %q, want the post-check hook named", joined)
		}
		mustContain(t, res.stderr, "artifact missing", "with the hook's own output")

		human := runIn(t, f.Dir(), "check")
		if human.code != exitRefusal {
			t.Errorf("`check` exit = %d, want %d for a failing post-check hook", human.code, exitRefusal)
		}
	})

	t.Run("both points appear in the array, in the order they ran", func(t *testing.T) {
		f, _, _, _ := approvedChangeset(t)
		putHook(t, f, "clone", "pre-check", "01-suite", passHook)
		putHook(t, f, "clone", "post-check", "01-artifact", passHook)

		res := runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check", "--json")
		var points []string
		for _, e := range res.jsonList(t, "hooks") {
			entry := e.(map[string]any)
			points = append(points, entry["point"].(string))
		}
		if strings.Join(points, ",") != "pre-check,post-check" {
			t.Errorf("points = %v, want [pre-check post-check]", points)
		}
	})
}

// TestIntegrateDoesNotRunTheGateHooks is the reason the points are attached to `check` rather than to the gate
// function. `change integrate` runs the same gate as the same code, and a declaration is one author action:
// firing the gate's hooks there would start the suite twice in one command, and a suite that takes six
// minutes would take twelve and nobody could say why.
func TestIntegrateDoesNotRunTheGateHooks(t *testing.T) {
	f, _, _, _ := approvedChangeset(t)
	dir := scratch(t)
	putHook(t, f, "clone", "pre-check", "01-suite", touch(dir, "RAN_CHECK"))
	putHook(t, f, "clone", "pre-integrate", "01-suite", touch(dir, "RAN_INTEGRATE"))

	runIn(t, f.Dir(), "change", "integrate").mustSucceed(t, "change", "integrate")

	if _, err := os.Stat(filepath.Join(dir, "RAN_INTEGRATE")); err != nil {
		t.Error("change integrate did not run its own point")
	}
	if _, err := os.Stat(filepath.Join(dir, "RAN_CHECK")); err == nil {
		t.Error("change integrate ran the gate's hooks: the author's suite would start twice in one command")
	}

	// And the same hook does run where it is attached, so the assertion above is about the path and
	// not about a hook that never fires anywhere.
	runIn(t, f.Dir(), "check").mustSucceed(t, "check")
	if _, err := os.Stat(filepath.Join(dir, "RAN_CHECK")); err != nil {
		t.Error("`git pair check` did not run the gate's hooks either")
	}
}
