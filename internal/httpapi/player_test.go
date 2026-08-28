package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const testAppSecret = "0123456789abcdef0123456789abcdef"

// TestOptionalPlayerAcceptsSignedAndLegacyCookies covers every shape a player
// cookie can arrive in. Whether the id names a real player is settled later, by
// requirePlayer's TouchPlayer call; this is only about provenance.
func TestOptionalPlayerAcceptsSignedAndLegacyCookies(t *testing.T) {
	s := &Server{AppSecret: testAppSecret}
	const id = "6b7a1c4e-0d2f-4a58-9b31-8f0c5e2d7a44"
	signed := sign(s.appKey(), playerCookie, id)

	tests := []struct {
		name   string
		cookie string
		want   string
	}{
		{"absent", "", ""},
		{"signed", signed, id},
		// The legacy branch: cookies issued before the cookie was signed. A
		// player id was always a UUIDv4, so accepting one gives up no forgery
		// resistance -- signing is here to reject junk, not to make the id
		// unguessable.
		{"legacy bare uuid", id, id},
		{"garbage", "not-a-cookie-value", ""},
		{"tampered mac", signed[:len(signed)-3] + "AAA", ""},
		{"signed for the session cookie", sign(s.appKey(), sessionCookie, id), ""},
		{"signed with another key", sign(signingKey("a completely different secret"), playerCookie, id), ""},
		// A junk cookie used to be interpolated straight into a uuid column and
		// crash the stats query with a 500. It must read as "no cookie".
		{"sql shaped", "'; DROP TABLE players; --", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: playerCookie, Value: tc.cookie})
			}
			if got := s.optionalPlayer(req); got != tc.want {
				t.Errorf("optionalPlayer() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSetPlayerCookieIsSignedAndLocked(t *testing.T) {
	s := &Server{AppSecret: testAppSecret, CookieSecure: true}
	const id = "6b7a1c4e-0d2f-4a58-9b31-8f0c5e2d7a44"

	res := httptest.NewRecorder()
	s.setPlayerCookie(res, id)

	cookies := (&http.Response{Header: res.Header()}).Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != playerCookie {
		t.Errorf("name = %q, want %q", c.Name, playerCookie)
	}
	if got, ok := unsign(s.appKey(), playerCookie, c.Value); !ok || got != id {
		t.Errorf("cookie value did not verify back to the id: got %q, ok %v", got, ok)
	}
	if !c.HttpOnly {
		t.Error("HttpOnly = false, want true")
	}
	if !c.Secure {
		t.Error("Secure = false, want true when CookieSecure is set")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
	if c.MaxAge != playerCookieMaxAge {
		t.Errorf("MaxAge = %d, want %d", c.MaxAge, playerCookieMaxAge)
	}
}
