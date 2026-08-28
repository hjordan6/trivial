package mail

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TestLoggerSurfacesTheCode is the whole point of the development sender: the
// sign-in flow has to be completable locally with no provider, which means the
// code must reach the log.
func TestLoggerSurfacesTheCode(t *testing.T) {
	var buf bytes.Buffer
	sender := Logger{Log: slog.New(slog.NewTextHandler(&buf, nil))}

	err := sender.Send(context.Background(), Message{
		To:      "player@example.test",
		Subject: "Your Trivial sign-in code: 048221",
		Text:    "048221 is your Trivial sign-in code.",
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	out := buf.String()
	for _, want := range []string{"player@example.test", "048221"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output %q, want it to contain %q", out, want)
		}
	}
}

// A zero-valued Logger must not panic: main.go builds senders from config, and
// a nil Log is an easy thing to end up with.
func TestLoggerToleratesAZeroValue(t *testing.T) {
	if err := (Logger{}).Send(context.Background(), Message{To: "a@b.test"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
}
