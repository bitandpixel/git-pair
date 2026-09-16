// Package marker writes the lifecycle commits gitpr derives state from.
//
// Markers are ordinary commits with GitPR-* trailers, so `git log` alone can
// reconstruct the whole review lifecycle. Subjects are stable strings for
// humans; trailers are the machine-readable contract.
package marker

import (
	"context"
	"fmt"
	"strings"

	"gitpr/internal/git"
	"gitpr/internal/model"
)

// Message is a constructed lifecycle commit message.
type Message struct {
	Subject  string
	Body     string
	Trailers []string
}

// Render assembles the final commit message.
func (m Message) Render(ctx context.Context, repo *git.Repo) (string, error) {
	text := m.Subject
	if strings.TrimSpace(m.Body) != "" {
		text += "\n\n" + strings.TrimSpace(m.Body) + "\n"
	}
	if len(m.Trailers) == 0 {
		return text, nil
	}
	out, err := repo.InterpretTrailers(ctx, text, m.Trailers...)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(out, "\n") + "\n", nil
}

// ReadyMessage describes a ready marker for slug.
func ReadyMessage(slug string) Message {
	return Message{
		Subject: fmt.Sprintf("gitpr: ready %s", slug),
		Trailers: []string{
			"GitPR-State=" + model.StateValueReady,
			"GitPR-Changeset=" + slug,
		},
	}
}

// ReviewMessage describes a review submission with the given outcome.
func ReviewMessage(slug string, outcome model.Outcome, body string) Message {
	return Message{
		Subject: fmt.Sprintf("review: %s %s", outcome, slug),
		Body:    body,
		Trailers: []string{
			"GitPR-Outcome=" + string(outcome),
			"GitPR-Changeset=" + slug,
		},
	}
}

// CloseMessage describes the final lifecycle marker for a changeset.
func CloseMessage(slug, archiveRef string) Message {
	return Message{
		Subject: fmt.Sprintf("gitpr: close %s", slug),
		Body:    "Review archive: " + archiveRef,
		Trailers: []string{
			"GitPR-State=" + model.StateValueClosed,
			"GitPR-Changeset=" + slug,
		},
	}
}

// Commit writes a marker commit and returns its SHA. Review submissions may be
// empty (an approval with no edits is a legitimate review), so empty commits are
// always allowed here.
func Commit(ctx context.Context, repo *git.Repo, msg Message) (string, error) {
	rendered, err := msg.Render(ctx, repo)
	if err != nil {
		return "", err
	}
	if err := repo.Commit(ctx, rendered, true); err != nil {
		return "", err
	}
	return repo.Head(ctx)
}
