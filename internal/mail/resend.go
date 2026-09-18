package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// resendEndpoint is the provider's send API.
const resendEndpoint = "https://api.resend.com/emails"

// maxErrorBody caps how much of a failure response is carried into an error, so
// an HTML error page from a proxy cannot flood the log.
const maxErrorBody = 4 << 10

// Resend posts to the Resend API.
//
// No SDK and no new module dependency: the whole integration is one JSON POST
// with a bearer token, which is the same reason the rest of this codebase has
// three requires in go.mod.
type Resend struct {
	APIKey string
	// From must be an address on a domain verified in Resend, or every send is
	// refused with a 4xx.
	From string
	// Client is optional; nil means a client with a 10s timeout.
	Client *http.Client
	// Endpoint is optional; empty means the real API. Tests point it at an
	// httptest server.
	Endpoint string
}

func (r Resend) Send(ctx context.Context, m Message) error {
	if r.APIKey == "" {
		return fmt.Errorf("resend: no API key configured")
	}
	if r.From == "" {
		return fmt.Errorf("resend: no from address configured")
	}
	// html is omitempty so a text-only message serialises to exactly the request
	// this sent before HTML existed, rather than an empty html field the
	// provider would have to decide what to do with.
	body, err := json.Marshal(struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
		HTML    string   `json:"html,omitempty"`
	}{From: r.From, To: []string{m.To}, Subject: m.Subject, Text: m.Text, HTML: m.HTML})
	if err != nil {
		return fmt.Errorf("resend: encode request: %w", err)
	}

	endpoint := r.Endpoint
	if endpoint == "" {
		endpoint = resendEndpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("resend: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	req.Header.Set("Content-Type", "application/json")

	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		// Deliberately not wrapped as ErrRejected: the address may be perfectly
		// good and the network merely down, so the caller should retry rather
		// than tell the player their address was refused.
		return fmt.Errorf("resend: send: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	// The provider's explanation is the only diagnostic an operator gets, so it
	// is carried into the error. The message body is not, because it holds the
	// code.
	detail, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
	if res.StatusCode >= 400 && res.StatusCode < 500 {
		return fmt.Errorf("resend: %w: status %d: %s", ErrRejected, res.StatusCode, detail)
	}
	return fmt.Errorf("resend: status %d: %s", res.StatusCode, detail)
}
