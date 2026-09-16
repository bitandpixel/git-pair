// Package marker writes the lifecycle commits gitpr derives state from.
//
// Markers are ordinary commits with GitPR-* trailers, so `git log` alone can
// reconstruct the whole review lifecycle. Subjects are stable strings for
// humans; trailers are the machine-readable contract.
package marker

import (
	"context"
	"errors"
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
	rendered, err := msg.Render()
	if err != nil {
		return "", err
	}
	if err := repo.CommitPaths(ctx, rendered, paths); err != nil {
		return "", err
	}
	return repo.Head(ctx)
}
