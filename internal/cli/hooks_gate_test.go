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
	// A handed-off, approved changeset that carries no hooks. With the hand-off asking for a position as well,
	// the way a clone reaches the gate with nothing recorded is for the repository to ship the scripts while the
	// work is in flight — which is also the case the gate must keep asking about independently, because the
	// hand-off could not have asked about content that did not exist yet.
	reviewedChangeset := func(t *testing.T) *gittest.Fixture {
		t.Helper()
		f, _ := newChangeset(t, "checkout-flow", "main")
		ready(t, f)
		submit(t, f, "approve")
		return f
	}
	// reasonsCarrying finds the hooks reason among whatever else the verdict refuses for. A fixture that commits
	// a hook after the approval earns a content reason too, and the one under test is not necessarily first.
	reasonsCarrying := func(reasons []any, phrase string) bool {
		for _, r := range reasons {
			if s, ok := r.(string); ok && strings.Contains(s, phrase) {
				return true
			}
		}
		return false
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
		f := reviewedChangeset(t)
		dir := scratch(t)
		putHook(t, f, "tracked", "pre-check", "01-suite", touch(dir, "RAN_CHECK"))

		have := checkRefused(t, f)
		mustContain(t, have, "have not been recorded in this clone", "the refusal has to say what is missing")
		mustContain(t, have, "git pair hooks --trust", "and the way that runs the checks")
		mustContain(t, have, "git pair hooks --decline", "and the way that does not, so the gate is not read as demanding approval")
		if _, err := os.Stat(filepath.Join(dir, "RAN_CHECK")); err == nil {
			t.Error("an unrecorded hook ran")
		}
		mustContain(t, have, "did not run", "and the run says the scripts were skipped")

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
		if !reasonsCarrying(reasons, "recorded in this clone") {
			t.Errorf("reasons = %v, want the hooks refusal among them", reasons)
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
		// The position is taken before the hand-off, because the hand-off asks for one too now.
		// The hash is what the note has to carry: "declined" alone does not tell this reader whether the
		// script they refused is the script that is here.
		record := runIn(t, f.Dir(), "hooks", "--decline", "--json").mustSucceed(t, "hooks", "--decline", "--json").json(t)
		declined, _ := record["declined"].(string)
		if declined == "" {
			t.Fatal("--decline recorded no hash")
		}
		ready(t, f)
		submit(t, f, "approve")

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
		// At the gate's own point: the verdict asks about the scripts the verdict runs, and a `pre-ready` suite is
		// the hand-off's business. See `TestThePositionIsAskedOnlyWhereTheCommandRuns`.
		putHook(t, f, "tracked", "pre-check", "01-suite", touch(dir, "RAN_CHECK"))
		trustHooks(t, f)
		// Hand-off and approval while the accepted content is still what is here, so nothing else is outstanding
		// when the repository moves it: the tree this clone accepted is not the tree that is here, and that is the
		// fact the refusal is about.
		ready(t, f)
		submit(t, f, "approve")
		f.Append(filepath.Join(".git-pair", "hooks", "pre-check", "01-suite"), "echo escalated\n")
		f.Commit("escalate the hook")

		have := checkRefused(t, f)
		mustContain(t, have, "changed since this clone recorded them", "the refusal has to say the tree moved")
		mustContain(t, have, "git pair hooks --decline", "and that a position is what is wanted, not a re-approval")
	})

	// The off switch is not a position. `GIT_PAIR_HOOKS=0` says a machine does not want to run scripts right now,
	// which is not an answer about what those scripts do; the answer is `--decline`, written down where the next
	// reader can see it was taken. Without this, the quietest way past the gate would be an environment variable
	// nobody has to read.
	t.Run("the off switch is not a position", func(t *testing.T) {
		f := reviewedChangeset(t)
		dir := scratch(t)
		putHook(t, f, "tracked", "pre-check", "01-suite", touch(dir, "RAN_CHECK"))
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

// The reviewer asked for the position to be obvious as early as it can be, so it is met before the work exists
// rather than only where it is merged: `init` refuses before a changeset directory is written, `change ready`
// before the hand-off, and `review submit` before the reviewer's content is staged. The two commands that take
// work *out* of the queue deliberately do not ask, because the person who cannot run the checks is exactly the
// person who needs to put the work down.
func TestTheCommandsThatStartWorkAskForAPosition(t *testing.T) {
	t.Run("init warns about a tree nobody has read, and the hand-off is where the ask lands", func(t *testing.T) {
		f := newRepo(t)
		putHook(t, f, "tracked", "pre-ready", "01-suite", "#!/bin/sh\nexit 0\n")
		f.CreateBranch("checkout-flow")
		before := f.Head()

		// init runs no hook, so it has no check of its own resting on silence and no business refusing. It says
		// the checks exist — the reviewer's "obvious as early as it can be" — and lets the changeset start.
		res := runIn(t, f.Dir(), "init").mustSucceed(t, "init")
		have := res.stdout + res.stderr
		mustContain(t, have, "ships 1 hook(s) under .git-pair/hooks that this clone has not read",
			"the first command a newcomer runs tells them the repository ships scripts")
		mustContain(t, have, "git pair hooks", "and where to read them")
		mustContain(t, have, "the first command that runs one asks for a position", "and when the refusal comes")
		if _, err := os.Stat(filepath.Join(f.Dir(), "changesets", "checkout-flow")); err != nil {
			t.Error("init would not create the changeset directory it was asked to create")
		}
		if f.Head() == before {
			t.Error("init recorded nothing")
		}

		// The warning is not the refusal. The ask arrives with the first command that would execute the script,
		// and reading is never gated, so the stop has a way forward in both directions.
		refused := runIn(t, f.Dir(), "change", "ready")
		if refused.code != exitRefusal {
			t.Fatalf("`change ready` exited %d, want %d\nstdout: %s\nstderr: %s",
				refused.code, exitRefusal, refused.stdout, refused.stderr)
		}
		mustContain(t, refused.stdout+refused.stderr, "have not been recorded in this clone",
			"the hand-off names the silence the warning only pointed at")
		mustContain(t, refused.stdout+refused.stderr, "git pair hooks --trust", "and names the way that runs them")
		mustContain(t, refused.stdout+refused.stderr, "git pair hooks --decline", "and the way that does not")
		runIn(t, f.Dir(), "hooks").mustSucceed(t, "hooks")

		trustHooks(t, f)
		runIn(t, f.Dir(), "change", "ready").mustSucceed(t, "change", "ready")
	})

	// Not only the unseen content: a script the repository moved after this clone accepted it is content nobody
	// has read since, so the hand-off asks about that too rather than leaving it to the gate.
	t.Run("the hand-off refuses a moved script as well as an unseen one", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-ready", "01-suite", "#!/bin/sh\nexit 0\n")
		trustHooks(t, f)
		f.Append(filepath.Join(".git-pair", "hooks", "pre-ready", "01-suite"), "echo escalated\n")
		f.Commit("escalate the hook")

		res := runIn(t, f.Dir(), "change", "ready")
		if res.code != exitRefusal {
			t.Fatalf("`change ready` exited %d, want %d\nstdout: %s\nstderr: %s",
				res.code, exitRefusal, res.stdout, res.stderr)
		}
		mustContain(t, res.stdout+res.stderr, "changed since this clone recorded them",
			"the hand-off names the move, not only the absence")
	})

	// Asked before the stage, so a reviewer is not left with a dirty index and a verdict they did not record —
	// which is what a refusal discovered by the pre-submit hooks would have done.
	t.Run("a refused submission stages nothing", func(t *testing.T) {
		f, _ := reviewerFixture(t)
		f.Write("handler.go", "package main\n\nfunc ServeHTTP() {}\n")
		putHook(t, f, "tracked", "pre-submit", "01-repo", "#!/bin/sh\nexit 0\n")

		res := runIn(t, f.Dir(), "review", "submit", "--approve")
		if res.code != exitRefusal {
			t.Fatalf("`review submit --approve` exited %d, want %d\nstdout: %s\nstderr: %s",
				res.code, exitRefusal, res.stdout, res.stderr)
		}
		staged, err := f.Git("diff", "--cached", "--name-only")
		if err != nil {
			t.Fatalf("reading the index: %v", err)
		}
		if strings.TrimSpace(staged) != "" {
			t.Errorf("the refusal left these staged: %q", staged)
		}
		if !strings.Contains(f.Read("handler.go"), "ServeHTTP") {
			t.Error("the reviewer's own edit was disturbed by the refusal")
		}
	})

	t.Run("putting the work down stays free", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-ready", "01-suite", "#!/bin/sh\nexit 0\n")

		// Abandon is the escape hatch for work that was never offered; refusing it would trap the person the
		// rule is meant to protect.
		runIn(t, f.Dir(), "change", "abandon").mustSucceed(t, "change", "abandon")
	})

	t.Run("unready works with the record cleared", func(t *testing.T) {
		f, _ := newChangeset(t, "checkout-flow", "main")
		putHook(t, f, "tracked", "pre-ready", "01-suite", "#!/bin/sh\nexit 0\n")
		trustHooks(t, f)
		ready(t, f)
		// A position is this clone's, so clearing the record is what a second clone looks like: nothing
		// recorded, and the work still takeable out of the queue.
		record := filepath.Join(f.Dir(), ".git", "git-pair", "trusted-hooks")
		if err := os.Remove(record); err != nil {
			t.Fatalf("clear the record: %v", err)
		}

		runIn(t, f.Dir(), "change", "unready").mustSucceed(t, "change", "unready")
	})
}
