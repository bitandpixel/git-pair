package cli

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"gitpair/internal/hooks"
)

// The review screen and `review submit` show the same refusal, and only one of them can write to stderr while
// it does: the screen owns the terminal. So the text is produced once (`hookOutputLines`) and each surface
// places it. These are the two halves that a refactor would otherwise separate — the sentence from `hookError`
// and the output from the run — and a reader at the screen has to get both from one message.

func refused(name, stdout, stderr string) hooks.Result {
	return hooks.Result{
		Hook:   hooks.Hook{Point: hooks.PreSubmit, Name: name, Path: "/clone/.git/git-pair/hooks/pre-submit/" + name},
		Status: hooks.StatusFailed,
		Exit:   1,
		Stdout: stdout,
		Stderr: stderr,
	}
}

func TestHookOutputLinesIsTheTextBothSurfacesShare(t *testing.T) {
	t.Run("a refused hook always shows its own text", func(t *testing.T) {
		got := hookOutputLines(refused("01-suite", "building…", "suite red: 2 failures"))

		want := []string{"building…", "suite red: 2 failures"}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("lines = %q, want %q", got, want)
		}
	})

	t.Run("a passing hook is silent unless the output was asked for", func(t *testing.T) {
		passed := refused("01-suite", "400 lines of suite noise", "")
		passed.Status = hooks.StatusPassed
		passed.Exit = 0

		if got := hookOutputLines(passed); got != nil {
			t.Errorf("lines = %q, want none: a green suite must not fill the handoff", got)
		}
		t.Setenv(hooks.EchoOutputEnv, "1")
		if got := hookOutputLines(passed); len(got) != 1 || got[0] != "400 lines of suite noise" {
			t.Errorf("lines = %q, want the output $%s asked for", got, hooks.EchoOutputEnv)
		}
	})

	t.Run("a hook that never ran prints nothing", func(t *testing.T) {
		skipped := refused("01-suite", "stale text from an earlier run", "")
		skipped.Status = hooks.StatusSkippedUntrusted

		if got := hookOutputLines(skipped); got != nil {
			t.Errorf("lines = %q, want none: it is the consent warning's job to say why it did not run", got)
		}
	})
}

func TestScreenRefusalCarriesTheReasonTheHookGave(t *testing.T) {
	results := []hooks.Result{refused("01-suite", "running the suite", "suite red: 2 failures")}

	refusal := hookError(results)
	composed := withHookText(refusal, hookOutputLines(results[0]))

	// Both halves, in one message: which hook stopped the submission, and what it said. The sentence alone
	// leaves the reviewer guessing; the output alone leaves them unable to tell a refusal from a crash.
	for _, want := range []string{`"01-suite"`, "pre-submit", "exit 1", "the hook printed:", "suite red: 2 failures"} {
		if !strings.Contains(composed.Error(), want) {
			t.Errorf("the screen's message does not contain %q:\n%s", want, composed)
		}
	}
	if !errors.Is(composed, refusal) {
		t.Error("the composed message must still unwrap to the refusal, which is what the exit code decides on")
	}
}

func TestLongHookOutputKeepsItsTail(t *testing.T) {
	var many []string
	for i := 1; i <= 60; i++ {
		many = append(many, fmt.Sprintf("line %02d", i))
	}
	many = append(many, "the failure is here, at the end")

	composed := withHookText(errors.New("hook refused"), []string{strings.Join(many, "\n")})
	text := composed.Error()

	if !strings.Contains(text, "the failure is here, at the end") {
		t.Errorf("the tail was cut; a suite's verdict is at the end:\n%s", text)
	}
	if strings.Contains(text, "line 01\n") {
		t.Error("the head survived; a megabyte of setup would replace the review instead of informing it")
	}
	if !strings.Contains(text, "git pair review submit") {
		t.Error("a cut message has to say where the whole of it is readable")
	}
}
