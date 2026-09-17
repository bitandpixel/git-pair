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
// The check is here rather than in each command because a marker commit and the archive move that
// follows it are one operation. A command that committed first and then learned the ref was frozen
// would leave a marker on the branch with nothing pointing at it — a half-write the author can only
// undo by rewriting history. `reviewref.Update` refuses too, so a caller that reaches the ref
// without coming through a marker still meets the rule (PRD §13, requirements §23).
func refuseIfIntegrated(ctx context.Context, repo *git.Repo, msg Message) error {
	id := msg.changesetID()
	if id == "" {
		return nil
	}
	return reviewref.RefuseIntegrated(ctx, repo, id)
}

// changesetID is the changeset a marker speaks about, read from the trailer that exists to answer
// exactly that. A message naming no changeset freezes nothing.
func (m Message) changesetID() string {
	for _, t := range m.Trailers {
		if key, value, ok := strings.Cut(t, "="); ok && strings.TrimSpace(key) == model.TrailerChangeset {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
