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

	"gitpair/internal/git"
	"gitpair/internal/model"
	"gitpair/internal/reviewref"
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
func ReviewMessage(slug string, outcome model.Outcome, body string) Message {
	return Message{
		Subject: fmt.Sprintf("review: %s %s", outcome, slug),
		Body:    body,
		Trailers: []string{
			"Review-Outcome=" + string(outcome),
			"Review-Changeset=" + slug,
		},
	}
}

// Commit writes a marker commit and returns its SHA. Review submissions may be
// empty (an approval with no edits is a legitimate review), so empty commits are
// always allowed here.
func Commit(ctx context.Context, repo *git.Repo, msg Message) (string, error) {
	if err := refuseIfIntegrated(ctx, repo, msg); err != nil {
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
func CommitPaths(ctx context.Context, repo *git.Repo, msg Message, paths []string) (string, error) {
	if err := refuseIfIntegrated(ctx, repo, msg); err != nil {
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

// refuseIfIntegrated is the write gate: once a changeset's integration record exists, git-pair
// writes no marker for it.
//
// The record closes the paper trail, so a marker after it would be a claim about a review that
// cannot happen. The gate also keeps a stale checkout honest: an author who forgot the branch was
// left behind cannot put a landed changeset back in the queue, and a reviewer working from an old
// clone cannot approve work that has already become something else.
func refuseIfIntegrated(ctx context.Context, repo *git.Repo, msg Message) error {
	return RefuseIntegrated(ctx, repo, msg.changesetID())
}

// RefuseIntegrated is the same gate for a caller that is about to write nothing. `marker.Commit`
// cannot produce a marker for a recorded changeset, but a command can decide on its own that there is
// nothing to record and report success — and `change unready` on a changeset its author has already
// merged is not a no-op, it is a mistake. The answer to both is the same sentence.
func RefuseIntegrated(ctx context.Context, repo *git.Repo, id string) error {
	if id == "" {
		return nil
	}
	at, err := reviewref.ResolveIntegration(ctx, repo, id)
	if errors.Is(err, reviewref.ErrNotIntegrated) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("changeset %s is recorded as integrated at %s, so git-pair records nothing further for it",
		id, short(at))
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
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
