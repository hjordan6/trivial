package mail

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResendSendsTheExpectedRequest(t *testing.T) {
	type payload struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
	}
	var got payload
	var gotAuth, gotType, gotMethod, gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"abc"}`))
	}))
	defer srv.Close()

	sender := Resend{APIKey: "re_test", From: "Trivial <login@example.test>", Endpoint: srv.URL}
	err := sender.Send(context.Background(), Message{
		To:      "player@example.test",
		Subject: "Your Trivial sign-in code: 048221",
		Text:    "048221",
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/" {
		t.Errorf("path = %q, want /", gotPath)
	}
	if gotAuth != "Bearer re_test" {
		t.Errorf("Authorization = %q, want Bearer re_test", gotAuth)
	}
	if gotType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotType)
	}
	if got.From != "Trivial <login@example.test>" {
		t.Errorf("from = %q", got.From)
	}
	if len(got.To) != 1 || got.To[0] != "player@example.test" {
		t.Errorf("to = %v, want [player@example.test]", got.To)
	}
	if got.Subject != "Your Trivial sign-in code: 048221" {
		t.Errorf("subject = %q", got.Subject)
	}
	if got.Text != "048221" {
		t.Errorf("text = %q", got.Text)
	}
}

// TestResendSendsBothBodies covers the multipart/alternative case: a message
// with markup has to arrive with both parts, or a client that prefers HTML and
// one that cannot render it disagree about what was sent.
func TestResendSendsBothBodies(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := Resend{APIKey: "re_test", From: "Trivial <login@example.test>", Endpoint: srv.URL}
	err := sender.Send(context.Background(), Message{
		To:      "player@example.test",
		Subject: "Your Trivial sign-in code: 048221",
		Text:    "048221",
		HTML:    "<b>048221</b>",
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if got["text"] != "048221" {
		t.Errorf("text = %v", got["text"])
	}
	if got["html"] != "<b>048221</b>" {
		t.Errorf("html = %v", got["html"])
	}
}

// A message with no markup must serialise to exactly the request this sent
// before HTML existed, rather than an empty html field the provider would have
// to decide what to do with.
func TestResendOmitsTheHTMLFieldWhenThereIsNone(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := Resend{APIKey: "re_test", From: "Trivial <login@example.test>", Endpoint: srv.URL}
	if err := sender.Send(context.Background(), Message{To: "a@b.test", Subject: "s", Text: "t"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if _, ok := got["html"]; ok {
		t.Errorf("html field present on a text-only message: %v", got)
	}
}

// TestResendClassifiesFailures pins the one distinction the caller acts on: a
// 4xx is the address being refused, which the player should be told about, and
// anything else is our problem and worth retrying.
func TestResendClassifiesFailures(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		wantErr      bool
		wantRejected bool
	}{
		{"ok", http.StatusOK, false, false},
		{"accepted", http.StatusAccepted, false, false},
		{"unauthorized", http.StatusUnauthorized, true, true},
		{"unprocessable", http.StatusUnprocessableEntity, true, true},
		{"too many requests", http.StatusTooManyRequests, true, true},
		{"server error", http.StatusInternalServerError, true, false},
		{"bad gateway", http.StatusBadGateway, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"message":"details from the provider"}`))
			}))
			defer srv.Close()

			err := Resend{APIKey: "re_test", From: "a@b.test", Endpoint: srv.URL}.
				Send(context.Background(), Message{To: "c@d.test"})

			if tt.wantErr != (err != nil) {
				t.Fatalf("Send() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil {
				return
			}
			if got := errors.Is(err, ErrRejected); got != tt.wantRejected {
				t.Errorf("errors.Is(err, ErrRejected) = %v, want %v", got, tt.wantRejected)
			}
			// The provider's own explanation is the only clue an operator gets,
			// so it has to survive into the error.
			if !strings.Contains(err.Error(), "details from the provider") {
				t.Errorf("error = %q, want it to carry the response body", err)
			}
		})
	}
}

func TestResendReportsATransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // nothing is listening now

	err := Resend{APIKey: "re_test", From: "a@b.test", Endpoint: srv.URL}.
		Send(context.Background(), Message{To: "c@d.test"})
	if err == nil {
		t.Fatal("Send() error = nil, want a transport error")
	}
	if errors.Is(err, ErrRejected) {
		t.Error("a transport failure must not be reported as a rejected address")
	}
}

func TestResendRequiresConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		sender Resend
	}{
		{"no api key", Resend{From: "a@b.test"}},
		{"no from", Resend{APIKey: "re_test"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.sender.Send(context.Background(), Message{To: "c@d.test"}); err == nil {
				t.Fatal("Send() error = nil, want a configuration error")
			}
		})
	}
}
