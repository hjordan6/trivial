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
// Text is required and HTML is optional, which is the multipart/alternative
// bargain: a client that can render markup gets the designed version, and a
// client that cannot -- plus every spam filter that scores a message with no
// text part -- gets a plain body that says the same thing. Sending HTML alone
// would be the one combination that hurts delivery, so Text is never empty.
type Message struct {
	To      string
	Subject string
	Text    string
	// HTML is the rendered alternative. Empty means send the text on its own,
	// which is what the mail-test command and any future notice do.
	HTML string
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
	// The text part is logged and the HTML one is not. Both carry the same
	// code, and a wall of markup in the terminal would bury the six digits this
	// sender exists to surface.
	log.Info("login email (development sender; not actually sent)",
		"to", m.To, "subject", m.Subject, "body", m.Text)
	return nil
}
