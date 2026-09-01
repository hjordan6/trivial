// Package mail delivers transactional email.
//
// It is deliberately separate from internal/accounts: accounts stays pure SQL
// and never imports net/http, so the whole sign-in flow is testable without a
// provider.
package mail

import (
	"context"
	"errors"
	"log/slog"
)

// Message is one outbound email.
//
// Text only, on purpose: a six-digit code needs no markup, and a text-only body
// is what reaches an inbox rather than a spam folder.
type Message struct {
	To      string
	Subject string
	Text    string
}

// Sender delivers a message. Implementations must be safe for concurrent use.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// ErrRejected means the provider refused the message permanently, which in
// practice means an undeliverable address. Retrying will not help, so the
// caller can tell the player rather than logging an outage.
var ErrRejected = errors.New("mail rejected")

// Logger is the development sender: it writes the message to the log and
// succeeds, so the sign-in flow is completable locally with no provider and no
// outbound mail.
//
// This is the one place the code is deliberately logged. Every other layer
// treats it as a secret.
type Logger struct{ Log *slog.Logger }

func (l Logger) Send(_ context.Context, m Message) error {
	log := l.Log
	if log == nil {
		log = slog.Default()
	}
	log.Info("login email (development sender; not actually sent)",
		"to", m.To, "subject", m.Subject, "body", m.Text)
	return nil
}
