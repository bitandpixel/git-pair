// Package marker writes the lifecycle commits git-pair derives state from.
//
// Markers are ordinary commits with Review-* trailers, so `git log` alone can
// reconstruct the whole review lifecycle. Subjects are stable strings for
// humans; trailers are the machine-readable contract.
package marker

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gitpair/internal/changeset"
	"gitpair/internal/git"
	"gitpair/internal/model"
)

// Message is a constructed lifecycle commit message.
type Message struct {
	Subject  string
	Body     string
	Trailers []string
}

// Render assembles the final commit message.
//
// The layout is built directly rather than through `git interpret-trailers`,
// because that command only inserts the blank-line separator when its input
// already ends with a newline. A message whose trailers sit on the subject line
// has no trailer block as far as git is concerned, and every derived lifecycle
// state silently collapses to WORKING. Building it here makes the shape
// deterministic and testable.
func (m Message) Render() (string, error) {
	subject := strings.TrimSpace(m.Subject)
	if subject == "" {
		return "", errors.New("marker: commit message needs a subject")
	}
	if strings.ContainsAny(subject, "\n\r") {
		return "", errors.New("marker: commit subject must be a single line")
	}
	var b strings.Builder
	b.WriteString(subject)
	b.WriteString("\n")
	if body := strings.TrimSpace(m.Body); body != "" {
		b.WriteString("\n" + body + "\n")
	}
	if len(m.Trailers) > 0 {
		b.WriteString("\n")
		for _, t := range m.Trailers {
			key, value, ok := strings.Cut(t, "=")
			if !ok || strings.TrimSpace(key) == "" {
				return "", fmt.Errorf("marker: malformed trailer %q", t)
			}
			fmt.Fprintf(&b, "%s: %s\n", strings.TrimSpace(key), strings.TrimSpace(value))
		}
	}
	return b.String(), nil
}

// ReadyMessage describes a ready marker for slug.
func ReadyMessage(slug string) Message {
	return Message{
		Subject: fmt.Sprintf("git-pair: ready %s", slug),
		Trailers: []string{
			"Review-State=" + model.StateValueReady,
			"Review-Changeset=" + slug,
		},
	}
}

// IntegrateMessage is the declaration `git pair change integrate` writes: the approval is
// standing and the work is handed to whoever owns the destination branch.
//
// head is the commit the declaration covers, written for the reason `ReviewMessage` writes
// `Review-Head` for: a rebase rewrites the marker and keeps its message, so the marker still
// names the commit that has gone out of this line — and a declaration about history the branch
// no longer carries must not read as a licence to merge what replaced it.
//
// It names no destination. Where the work goes is derived from the changeset and its parent's
// record when the merge happens, and a trailer written here would be a second, unfalsifiable
// claim about the same fact (PRD §9.9).
func IntegrateMessage(slug, head string) Message {
	trailers := []string{
		"Review-State=" + model.StateValueIntegrating,
		"Review-Changeset=" + slug,
	}
	// The same rule as a review's head: no head is recorded as no trailer, because
	// `Review-Head:` with nothing after it is a malformed trailer block to every other reader,
	// while a missing trailer reads as "this declaration names no commit" — which is a refusal.
	if head != "" {
		trailers = append(trailers, "Review-Head="+head)
	}
	return Message{
		Subject:  fmt.Sprintf("git-pair: integrate %s", slug),
		Trailers: trailers,
	}
}

// UnreadyMessage describes a retraction of a ready marker for slug.
// AbandonedMessage is the terminal marker written by `change abandon` (PRD §9.7).
func AbandonedMessage(slug string) Message {
	return Message{
		Subject: fmt.Sprintf("git-pair: abandon %s", slug),
		Trailers: []string{
			"Review-State=" + model.StateValueAbandoned,
			"Review-Changeset=" + slug,
		},
	}
}

func UnreadyMessage(slug string) Message {
	return Message{
		Subject: fmt.Sprintf("git-pair: unready %s", slug),
		Trailers: []string{
			"Review-State=" + model.StateValueWorking,
			"Review-Changeset=" + slug,
		},
	}
}

// ReviewMessage describes a review submission with the given outcome.
//
// head is the commit the reviewer was looking at — HEAD at the moment of the submission, which is the
// new commit's first parent. It is recorded rather than left implicit because a rebase rewrites the
// review commit while preserving its message: the rewritten marker still names the head that is gone
// from this line, which is what lets `check` refuse to read an approval as approval of rewritten history
// (PRD §10.4, §11.3).
func ReviewMessage(slug string, outcome model.Outcome, head, parentHead, body string) Message {
	trailers := []string{
		"Review-Outcome=" + string(outcome),
		"Review-Changeset=" + slug,
	}
	// An unknown head is recorded as no trailer rather than as an empty one: a marker
	// that names nothing is readable as "this review does not say what it reviewed",
	// which is what the gate then reports, while `Review-Head:` with nothing after it
	// is a malformed trailer block to every other reader.
	if head != "" {
		trailers = append(trailers, "Review-Head="+head)
	}
	// The same for a stacked changeset's parent: an unstacked changeset writes nothing, because
	// `Review-Parent-Head=` with no value would claim a parent with no name.
	if parentHead != "" {
		trailers = append(trailers, "Review-Parent-Head="+parentHead)
	}
	return Message{
		Subject:  fmt.Sprintf("review: %s %s", outcome, slug),
		Body:     body,
		Trailers: trailers,
	}
}

// Commit writes a marker commit and returns its SHA. Review submissions may be
// empty (an approval with no edits is a legitimate review), so empty commits are
// always allowed here.
func Commit(ctx context.Context, repo *git.Repo, msg Message, db changeset.DefaultBranchRef) (string, error) {
	if err := refuseIfIntegrated(ctx, repo, msg, db); err != nil {
		return "", err
	}
	rendered, err := msg.Render()
	if err != nil {
		return "", err
	}
	if err := repo.Commit(ctx, rendered, true); err != nil {
		return "", err
	}
	return repo.Head(ctx)
}

// CommitPaths writes a marker commit covering exactly the given paths, so
// scaffolding commits cannot sweep unrelated staged work off the author's index.
func CommitPaths(ctx context.Context, repo *git.Repo, msg Message, paths []string, db changeset.DefaultBranchRef) (string, error) {
	if err := refuseIfIntegrated(ctx, repo, msg, db); err != nil {
		return "", err
	}
	rendered, err := msg.Render()
	if err != nil {
		return "", err
	}
	if err := repo.CommitPaths(ctx, rendered, paths); err != nil {
		return "", err
	}
	return repo.Head(ctx)
}

// refuseIfIntegrated is the write gate: once a changeset's directory is in the destination's tree,
// git-pair writes no marker for it.
//
// The destination carrying the directory closes the paper trail, so a marker after it would be a claim
// about a review that cannot happen. The gate also keeps a stale checkout honest: an author who forgot the
// branch was left behind cannot put a landed changeset back in the queue, and a reviewer working from an old
// clone cannot approve work that has already become something else.
func refuseIfIntegrated(ctx context.Context, repo *git.Repo, msg Message, db changeset.DefaultBranchRef) error {
	return RefuseIntegrated(ctx, repo, msg.changesetID(), db)
}

// RefuseIntegrated is the same gate for a caller that is about to write nothing. `marker.Commit`
// cannot produce a marker for a landed changeset, but a command can decide on its own that there is
// nothing to record and report success — and `change unready` on a changeset its author has already
// merged is not a no-op, it is a mistake. The answer to both is the same sentence.
//
// It asks the destination's tree, not a ref: `changesets/<id>/` in the destination is the fact, and a
// clone that has never fetched a namespace answers the same question as one that has. Where no destination
// can be named at all, the gate stays open — refusing because git-pair could not work out which branch is
// main would blame the work for a clone, and the caller's own checks still apply.
func RefuseIntegrated(ctx context.Context, repo *git.Repo, id string, db changeset.DefaultBranchRef) error {
	if id == "" || db.Ref == "" {
		return nil
	}
	present, moved := changeset.CarriesDir(ctx, repo, db.Ref, id)
	if !present {
		return nil
	}
	path := changeset.ActiveDirPath(id)
	if moved {
		path = changeset.LandedDirPath(id)
	}
	return fmt.Errorf("changeset %s is landed on %s at %s, so git-pair records nothing further for it",
		id, displayBranch(db), path)
}

// displayBranch names the destination the way a person reads it rather than the way git stores it.
func displayBranch(db changeset.DefaultBranchRef) string {
	name := db.LocalName()
	for _, prefix := range []string{"refs/heads/", "refs/remotes/"} {
		if strings.HasPrefix(name, prefix) {
			return strings.TrimPrefix(name, prefix)
		}
	}
	return name
}

// changesetID is the changeset a marker speaks about, read from the trailer that exists to answer
// exactly that. A message naming no changeset belongs to no record.
func (m Message) changesetID() string {
	for _, t := range m.Trailers {
		if key, value, ok := strings.Cut(t, "="); ok && strings.TrimSpace(key) == model.TrailerChangeset {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
