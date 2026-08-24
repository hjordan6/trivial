package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/hjordan6/trivial/internal/clock"
)

func originServer(nets []netip.Prefix) (*Server, http.Handler) {
	s := &Server{
		AdminPassword:    "correct horse",
		AdminAllowedNets: nets,
		Clock:            clock.Fake{T: time.Unix(1_800_000_000, 0)},
		Assets: fstest.MapFS{
			"dist/index.html": {Data: []byte("<!doctype html>shell")},
		},
	}
	return s, s.Handler()
}

func fromAddr(s *Server, method, path, remote string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(`{"password":"correct horse"}`))
	req.RemoteAddr = remote
	req.AddCookie(&http.Cookie{Name: adminCookie, Value: s.signAdminSession(s.now().Add(time.Hour))})
	return req
}

// Everything about the admin surface disappears for a caller outside the
// allowlist -- a valid session cookie and the right password included. The
// 404 is the same one an unconfigured server gives, so a caller cannot tell
// the surface exists here.
func TestAdminSurfaceIsInvisibleFromDisallowedAddresses(t *testing.T) {
	s, handler := originServer([]netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")})

	paths := []struct{ method, path string }{
		{http.MethodGet, "/api/admin/session"},
		{http.MethodGet, "/api/admin/puzzles"},
		{http.MethodGet, "/api/admin/questions"},
		{http.MethodPost, "/api/admin/login"},
		{http.MethodGet, "/admin"},
	}
	for _, p := range paths {
		t.Run(p.path, func(t *testing.T) {
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, fromAddr(s, p.method, p.path, "203.0.113.7:5000"))
			if res.Code != http.StatusNotFound {
				t.Fatalf("%s from a stranger = %d, want 404", p.path, res.Code)
			}
		})
	}
}

func TestAdminSurfaceAnswersFromAllowedAddresses(t *testing.T) {
	s, handler := originServer([]netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")})

	cases := []struct {
		name   string
		remote string
	}{
		{"loopback", "127.0.0.1:41000"},
		{"loopback range", "127.0.0.9:41000"},
		// A dual-stack listener reports loopback this way, and it has to match
		// a 127.0.0.0/8 rule or the panel breaks depending on how it was dialled.
		{"ipv4-mapped ipv6 loopback", "[::ffff:127.0.0.1]:41000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, fromAddr(s, http.MethodGet, "/api/admin/session", tc.remote))
			if res.Code != http.StatusNoContent {
				t.Fatalf("session from %s = %d, want 204", tc.remote, res.Code)
			}
			res = httptest.NewRecorder()
			handler.ServeHTTP(res, fromAddr(s, http.MethodGet, "/admin", tc.remote))
			if res.Code != http.StatusOK {
				t.Fatalf("/admin from %s = %d, want 200", tc.remote, res.Code)
			}
		})
	}
}

// The allowlist is decided from the connection, so headers a caller controls
// cannot talk their way past it.
func TestAdminAllowlistIgnoresForwardedHeaders(t *testing.T) {
	s, handler := originServer([]netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")})

	for _, header := range []string{"X-Forwarded-For", "X-Real-IP", "Forwarded"} {
		t.Run(header, func(t *testing.T) {
			req := fromAddr(s, http.MethodGet, "/api/admin/session", "203.0.113.7:5000")
			req.Header.Set(header, "127.0.0.1")
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusNotFound {
				t.Fatalf("%s spoof = %d, want 404", header, res.Code)
			}
		})
	}
}

// An empty allowlist is a misconfiguration, and it fails closed.
func TestAdminSurfaceIsClosedWithNoAllowlist(t *testing.T) {
	s, handler := originServer(nil)

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, fromAddr(s, http.MethodGet, "/api/admin/session", "127.0.0.1:41000"))
	if res.Code != http.StatusNotFound {
		t.Fatalf("session with no allowlist = %d, want 404", res.Code)
	}
}

// A wider rule is honoured, for a deployment that reaches the panel from
// somewhere other than the host itself.
func TestAdminAllowlistHonoursWiderRules(t *testing.T) {
	s, handler := originServer([]netip.Prefix{netip.MustParsePrefix("100.64.0.0/10")})

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, fromAddr(s, http.MethodGet, "/api/admin/session", "100.80.189.68:5000"))
	if res.Code != http.StatusNoContent {
		t.Fatalf("session from an allowed range = %d, want 204", res.Code)
	}
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, fromAddr(s, http.MethodGet, "/api/admin/session", "127.0.0.1:41000"))
	if res.Code != http.StatusNotFound {
		t.Fatalf("loopback = %d, want 404 once the allowlist no longer names it", res.Code)
	}
}

// The game is untouched: only the admin surface is restricted.
func TestGameStaysReachableFromAnywhere(t *testing.T) {
	_, handler := originServer([]netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:5000"
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("game from a stranger = %d, want 200", res.Code)
	}
}
