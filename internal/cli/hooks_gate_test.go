package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitpair/internal/gittest"
)

// The verdict asks for a position on the repository's own scripts, because reporting the skip was not enough. A
// clone nobody had accepted ran none of the checks the team wrote, `check` still answered ready, and a pipeline
// that merged on that answer merged work the suite never saw. Here the four states meet the one point a machine
// reads. Two of them pass it — one because the scripts ran, one because a person read them and chose not to run
// them — and the two that fail it are both silence about content, which is the thing a merge should not be able to
// rest on.
func TestCheckAsksForAPositionOnTrackedHooks(t *testing.T) {
	// reviewedChangeset leaves one thing outstanding: what this clone says about the hook the repository ships.
	// The hand-off and the approval are done, so every refusal below is the hooks and nothing else.
	reviewedChangeset := func(t *testing.T) (*gittest.Fixture, string) {
		t.Helper()
		dir := scratch(t)
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-ready", "01-suite", touch(dir, "RAN_READY"))
		ready(t, f)
		submit(t, f, "approve")
		return f, dir
	}
	checkRefused := func(t *testing.T, f *gittest.Fixture) string {
		t.Helper()
		res := runIn(t, f.Dir(), "check")
		if res.code != exitRefusal {
			t.Fatalf("`check` exited %d, want %d\nstdout: %s\nstderr: %s", res.code, exitRefusal, res.stdout, res.stderr)
		}
		return res.stdout + res.stderr
	}

	t.Run("no position recorded, the verdict refuses and names both ways out", func(t *testing.T) {
		f, dir := reviewedChangeset(t)

		have := checkRefused(t, f)
		mustContain(t, have, "have not been recorded in this clone", "the refusal has to say what is missing")
		mustContain(t, have, "git pair hooks --trust", "and the way that runs the checks")
		mustContain(t, have, "git pair hooks --decline", "and the way that does not, so the gate is not read as demanding approval")
		if _, err := os.Stat(filepath.Join(dir, "RAN_READY")); err == nil {
			t.Error("an unrecorded hook ran")
		}

		// The same answer in both fields a pipeline reads. A `ready: true` with the reason only in prose would
		// let a job that ignores prose do exactly what the previous paragraph describes.
		out := runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check", "--json")
		if out.json(t)["ready"] != false {
			t.Errorf("ready = %v while the tracked hooks are unrecorded, want false", out.json(t)["ready"])
		}
		reasons := out.jsonList(t, "reasons")
		if len(reasons) == 0 {
			t.Fatal("reasons is empty while the verdict refuses")
		}
		if !strings.Contains(reasons[0].(string), "recorded in this clone") {
			t.Errorf("reasons = %v, want the hooks refusal", reasons)
		}
	})

	// The position that costs something: recording acceptance means the scripts run, which is the whole reason a
	// team writes them. Asserted here rather than only in the trust tests because the gate and the runner read the
	// same state, and a gate that passed while the runner skipped would be the old bug wearing a new hat.
	t.Run("accepted, the checks ran and the verdict passes", func(t *testing.T) {
		dir := scratch(t)
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-ready", "01-suite", touch(dir, "RAN_READY"))
		trustHooks(t, f)
		ready(t, f)
		if _, err := os.Stat(filepath.Join(dir, "RAN_READY")); err != nil {
			t.Error("the accepted hook did not run at the hand-off")
		}
		submit(t, f, "approve")

		runIn(t, f.Dir(), "check").mustSucceed(t, "check")
	})

	// The position that costs nothing and is still not silence. The gate passes because somebody read the script
	// and answered; the hook stays skipped because a decline is not a licence to execute. If declining made the
	// hooks run, the escape hatch would be the dangerous path; if it left the verdict refused, the only way to be
	// green would be to approve.
	t.Run("declined, the verdict passes and the hook stays skipped", func(t *testing.T) {
		dir := scratch(t)
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-ready", "01-suite", touch(dir, "RAN_READY"))
		// A hook at the point `check` itself runs, so the run is asked to say what it did with these scripts and
		// has an answer to give that is not "nothing was configured here".
		putHook(t, f, "tracked", "pre-check", "02-suite", touch(dir, "RAN_CHECK"))
		ready(t, f)
		submit(t, f, "approve")
		// The hash is what the note has to carry: "declined" alone does not tell this reader whether the script
		// they refused is the script that is here.
		record := runIn(t, f.Dir(), "hooks", "--decline", "--json").mustSucceed(t, "hooks", "--decline", "--json").json(t)
		declined, _ := record["declined"].(string)
		if declined == "" {
			t.Fatal("--decline recorded no hash")
		}

		res := runIn(t, f.Dir(), "check").mustSucceed(t, "check")
		for _, name := range []string{"RAN_READY", "RAN_CHECK"} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				t.Errorf("a declined hook ran (%s)", name)
			}
		}
		mustContain(t, res.stderr, "declined "+declined, "the run says which position kept the script from running, and which content it was about")

		out := runIn(t, f.Dir(), "check", "--json").mustSucceed(t, "check", "--json").json(t)
		if out["ready"] != true {
			t.Errorf("ready = %v for a declined tree, want true — %v", out["ready"], out["reasons"])
		}
	})

	// `changed` refuses beside `unrecorded`, and this is the case the decline exists to make legible: content that
	// moved under an acceptance is content nobody has looked at since, so the older record cannot answer for it.
	// Approval, hand-off and everything else are in place; the moved script is the only outstanding fact.
	t.Run("content that moved under the acceptance is silence again", func(t *testing.T) {
		dir := scratch(t)
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-ready", "01-suite", touch(dir, "RAN_READY"))
		trustHooks(t, f)
		f.Append(filepath.Join(".git-pair", "hooks", "pre-ready", "01-suite"), "echo escalated\n")
		f.Commit("escalate the hook")
		// Approved after the move, so nothing else is outstanding: the tree this clone accepted is not the tree
		// that is here, and that is the only fact the refusal is about.
		ready(t, f)
		submit(t, f, "approve")

		have := checkRefused(t, f)
		mustContain(t, have, "changed since this clone accepted them", "the refusal has to say the tree moved")
		mustContain(t, have, "git pair hooks --decline", "and that a position is what is wanted, not a re-approval")
	})

	// The off switch is not a position. `GIT_PAIR_HOOKS=0` says a machine does not want to run scripts right now,
	// which is not an answer about what those scripts do; the answer is `--decline`, written down where the next
	// reader can see it was taken. Without this, the quietest way past the gate would be an environment variable
	// nobody has to read.
	t.Run("the off switch is not a position", func(t *testing.T) {
		f, _ := reviewedChangeset(t)
		t.Setenv("GIT_PAIR_HOOKS", "0")

		have := checkRefused(t, f)
		mustContain(t, have, "have not been recorded in this clone", "the refusal stands with hooks switched off")
	})
}

// `--decline` prints what it is recording for the same reason `--trust` does: a position taken about content nobody
// has been shown is not a position. The two flags are opposite answers about the same content, so saying both is a
// usage error rather than a race between two writes to two records.
func TestHooksDeclineCommand(t *testing.T) {
	t.Run("it prints the content and where it is recorded", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-ready", "01-suite", "#!/bin/sh\nexit 0\n")

		res := runIn(t, f.Dir(), "hooks", "--decline").mustSucceed(t, "hooks", "--decline")
		mustContain(t, res.stdout, "01-suite", "a decline has to name the script it declines")
		mustContain(t, res.stdout, "declined-hooks", "and where the record lives, beside the accepted one")

		out := runIn(t, f.Dir(), "hooks", "--json").mustSucceed(t, "hooks", "--json").json(t)
		if out["trusted"] != false {
			t.Errorf("trusted = %v for declined content, want false", out["trusted"])
		}
		if out["trust_state"] != "declined" {
			t.Errorf("trust_state = %v, want declined", out["trust_state"])
		}
		declined, _ := out["declined"].(string)
		if declined == "" {
			t.Error("declined must carry the hash the position was taken against")
		}
		if out["tree_hash"] != declined {
			t.Errorf("tree_hash = %v, want the declined %q so a reader can see the position is current", out["tree_hash"], declined)
		}

		human := runIn(t, f.Dir(), "hooks").mustSucceed(t, "hooks")
		mustContain(t, human.stderr, "DECLINED for this clone", "the human report states the position rather than an absence")
		mustContain(t, human.stderr, declined, "beside its hash")
	})

	t.Run("the two positions are not both", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-ready", "01-suite", "#!/bin/sh\nexit 0\n")

		res := runIn(t, f.Dir(), "hooks", "--trust", "--decline")
		if res.code != exitUsage {
			t.Fatalf("`hooks --trust --decline` exited %d, want %d\nstderr: %s", res.code, exitUsage, res.stderr)
		}
		mustContain(t, res.stdout+res.stderr, "opposite positions", "and says why the pair is a usage error")
	})

	// A decline is a position on content, so the report has to keep showing the old one when a new version arrives
	// unrecorded: "declined 3f1a…, the tree is now 82b0…" is the sentence that tells a person the script they
	// refused has been replaced, which is not the same situation as nobody having looked.
	t.Run("a new version keeps the old decline in the report", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-ready", "01-suite", "#!/bin/sh\nexit 0\n")
		before := runIn(t, f.Dir(), "hooks", "--decline", "--json").mustSucceed(t, "hooks", "--decline", "--json").json(t)
		hash, _ := before["declined"].(string)
		if hash == "" {
			t.Fatal("--decline --json returned no hash to compare the report against")
		}

		f.Append(filepath.Join(".git-pair", "hooks", "pre-ready", "01-suite"), "echo escalated\n")
		f.Commit("escalate the hook")

		out := runIn(t, f.Dir(), "hooks", "--json").mustSucceed(t, "hooks", "--json").json(t)
		if out["trust_state"] != "unrecorded" {
			t.Errorf("trust_state = %v after a new version of declined content, want unrecorded", out["trust_state"])
		}
		if out["declined"] != hash {
			t.Errorf("declined = %v, want the stale %q to stay reportable", out["declined"], hash)
		}
		human := runIn(t, f.Dir(), "hooks").mustSucceed(t, "hooks")
		mustContain(t, human.stderr, "NOT trusted", "and the human report says nothing is running")
	})
}
