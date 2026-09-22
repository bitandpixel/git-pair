package git

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// AUDITED: PRD §26 — the only place in shipped source that invokes `git push`.
//
// §26 forbids git-pair pushing, and the durable refs (PRD §13) cannot survive that forever: a paper
// trail written in one clone and never sent anywhere is a paper trail that dies with the clone. The
// exception is deliberately narrow, and this file is the audit rather than a wrapper to forget about:
//
//   - only refs under `refs/git-pair/` may be pushed, in either half of a refspec;
//   - never forced — a `+` is refused, so a remote that disagrees rejects the push instead of being
//     overwritten by it;
//   - never with flags: the argument vector is built here from refspecs, nothing caller-supplied is
//     passed through, and anything beginning with `-` is refused before it reaches git;
//   - nothing else may call git's push: `internal/hygiene` fails the build if `push` appears in any
//     other shipped file, and `TestOnlyTheAuditedFilePushes` pins both halves of that — this file is
//     the only invoker, and `internal/cli/publish.go` is the only caller.

// ErrPushScope is the refusal of a push that would leave the durable namespace, or reach git with an
// argument the audited helper does not allow.
//
// It is a distinct error because the caller's answer differs: a transport failure is retried, while a
// scope refusal is a bug in the caller and must never be softened into a warning.
var ErrPushScope = errors.New("git: push refused")

// durablePushRoot is the only namespace this helper will send.
const durablePushRoot = "refs/git-pair/"

// pushForbiddenArgs are named explicitly, even though the leading-dash rule already refuses them, so
// that the arguments the amendment is about are asserted by name rather than by a rule they happen to
// fall under. `--force-with-lease` and friends are the arguments a future caller might reach for to
// "fix" a conflict, and the answer to a conflict is a human deciding, not a flag.
var pushForbiddenArgs = []string{
	"--force", "-f", "--force-with-lease", "--force-if-includes",
	"--delete", "-d", "--prune", "--mirror", "--all", "-a", "--tags", "-t",
	"--receive-pack", "--exec", "--push-option", "-o", "--atomic", "--thin",
	"--verbose", "-v", "--dry-run", "--porcelain", "--no-verify",
}

// PushOutcome is git's verdict on one refspec of a push.
type PushOutcome struct {
	// Ref is the destination ref, which is what a reader cares about.
	Ref string
	// Flag is git's status word — `[new reference]`, `[remote rejected]`, `[up to date]` — kept
	// verbatim because the reader is being told what git said, not what git-pair concluded.
	Flag string
	// Summary is git's own reason, when it gave one.
	Summary string
	// OK is false for a rejected refspec. git exits non-zero for the whole command when any refspec
	// is rejected, and that is the trap this avoids: the accepted refspecs are still applied, so the
	// honest report is per ref, not per command.
	OK bool
}

// PushReport is one push's per-refspec results.
type PushReport struct {
	Outcomes []PushOutcome
}

// Rejected returns the refs git refused, in the order it reported them.
func (p PushReport) Rejected() []PushOutcome {
	var out []PushOutcome
	for _, o := range p.Outcomes {
		if !o.OK {
			out = append(out, o)
		}
	}
	return out
}

// PushDurableRefs pushes refs under `refs/git-pair/` to a remote in one unforced invocation.
//
// The refspecs are `src:dst` strings naming local and remote refs; both sides must live under
// `refs/git-pair/`. Refusing to send a `+` is what makes a disagreement between this clone and the
// remote a rejection rather than an overwrite — and it matters which servers honour that: measured on
// GitHub and on a local bare repository (git 2.43), an unforced non-fast-forward update of
// `refs/git-pair/*` is rejected by both. So the create-only rule (PRD §11.4) survives the trip to the
// forge, and publish never has to ask for permission to break it.
func (r *Repo) PushDurableRefs(ctx context.Context, remote string, refspecs ...string) (PushReport, error) {
	if err := pushScopeError(remote, refspecs...); err != nil {
		return PushReport{}, err
	}
	args := append([]string{"push", "--quiet", "--porcelain", remote}, refspecs...)
	out, err := r.Git(ctx, args...)
	report := parsePushPorcelain(out)
	if err != nil {
		// git failed. Whether that is a transport failure or a rejected refspec is in the report —
		// a rejection exits 1 with per-ref lines, which is the half-state, and the caller reports
		// what arrived rather than guessing from the exit status.
		if len(report.Outcomes) == 0 {
			return PushReport{}, fmt.Errorf("git: push to %s failed: %w", remote, err)
		}
		return report, nil
	}
	return report, nil
}

// pushScopeError refuses every argument that would make this something other than an unforced push of
// the durable namespace.
func pushScopeError(remote string, refspecs ...string) error {
	if strings.TrimSpace(remote) == "" {
		return fmt.Errorf("%w: a remote must be named", ErrPushScope)
	}
	if strings.HasPrefix(remote, "-") {
		return fmt.Errorf("%w: %q is not a remote name", ErrPushScope, remote)
	}
	if len(refspecs) == 0 {
		return fmt.Errorf("%w: no refspec given", ErrPushScope)
	}
	for _, spec := range refspecs {
		if err := pushSpecError(spec); err != nil {
			return err
		}
	}
	return nil
}

func pushSpecError(spec string) error {
	if spec == "" {
		return fmt.Errorf("%w: empty refspec", ErrPushScope)
	}
	// An argument that begins with `-` is an option to git, not a refspec. Refusing it is what keeps
	// "no caller-supplied flags" true even if a future caller builds its list from user input.
	if strings.HasPrefix(spec, "-") {
		for _, bad := range pushForbiddenArgs {
			if spec == bad {
				return fmt.Errorf("%w: %s is not allowed; git-pair pushes the durable refs unforced and with no flags", ErrPushScope, bad)
			}
		}
		return fmt.Errorf("%w: %q looks like an option, not a refspec", ErrPushScope, spec)
	}
	for _, bad := range pushForbiddenArgs {
		if spec == bad {
			return fmt.Errorf("%w: %s is not allowed", ErrPushScope, bad)
		}
	}
	if strings.ContainsAny(spec, " \t\n") {
		return fmt.Errorf("%w: %q contains whitespace", ErrPushScope, spec)
	}
	// A leading `+` is git's forced update: it turns "the remote disagrees" into "the remote is
	// overwritten", which is the one outcome §11.4's create-only rule exists to prevent. Refusing it
	// outright, rather than trimming it and checking the rest, is what keeps the conflict policy in this
	// file rather than in a caller's judgement.
	if strings.HasPrefix(spec, "+") {
		return fmt.Errorf("%w: %s forces an update; the durable refs are create-only, and a disagreement with %s is a decision, not a race", ErrPushScope, spec, "the remote")
	}
	src, dst, found := strings.Cut(spec, ":")
	if !found {
		return fmt.Errorf("%w: %q is not a src:dst refspec", ErrPushScope, spec)
	}
	if src == "" || dst == "" {
		return fmt.Errorf("%w: %q would delete or move a ref", ErrPushScope, spec)
	}
	for _, ref := range []string{src, dst} {
		if !strings.HasPrefix(ref, durablePushRoot) || ref == durablePushRoot {
			return fmt.Errorf("%w: %q is outside %s", ErrPushScope, ref, durablePushRoot)
		}
	}
	return nil
}

// parsePushPorcelain reads `git push --porcelain` output. The shape is a `To <url>` line, one line per
// refspec of `<status>\t<src>:<dst>\t<flag> (<summary>)`, then `Done`. Status `!` is a rejection;
// `*`, `=` and a space are not.
//
// Porcelain exists for exactly this: git's human output names the refs but not the outcome per refspec
// in anything stable enough to branch on, and the half-state — one ref of a pair accepted, the other
// rejected — is the case that has to be reported correctly.
func parsePushPorcelain(out string) PushReport {
	report := PushReport{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || line == "Done" || strings.HasPrefix(line, "To ") || strings.HasPrefix(line, "remote:") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		status, spec, verdict := fields[0], fields[1], fields[2]
		_, dst, _ := strings.Cut(spec, ":")
		// The verdict is `[flag] (reason)`, and the flag itself contains a space
		// (`[remote rejected]`), so the bracket is the boundary rather than the first space.
		verdict = strings.TrimSpace(verdict)
		flag, summary := verdict, ""
		if i := strings.Index(verdict, "]"); i >= 0 {
			flag = verdict[:i+1]
			summary = strings.Trim(strings.TrimSpace(verdict[i+1:]), "()")
		}
		report.Outcomes = append(report.Outcomes, PushOutcome{
			Ref:     strings.TrimSpace(dst),
			Flag:    strings.TrimSpace(flag),
			Summary: strings.TrimSpace(strings.Trim(summary, "()")),
			OK:      status != "!",
		})
	}
	return report
}
