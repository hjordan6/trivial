package httpapi

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/tailnet"
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

// fakeTailscaled serves the whois endpoint the identity check calls, so these
// tests exercise the real client against a controllable daemon.
func fakeTailscaled(t *testing.T, peers map[string]string) *tailnet.Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "d.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		login, ok := peers[r.URL.Query().Get("addr")]
		if !ok {
			http.Error(w, "no match for IP:port", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"Node":{"Name":"device.tailnet.ts.net."},` +
			`"UserProfile":{"LoginName":"` + login + `"}}`))
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return tailnet.New(path)
}

func tailnetServer(t *testing.T, users []string, peers map[string]string) (*Server, http.Handler) {
	t.Helper()
	s := &Server{
		AdminPassword: "correct horse",
		// No address is trusted on its own: identity is the only way in, which
		// is what "only my Tailscale devices" means.
		AdminAllowedNets:  nil,
		AdminTailnet:      fakeTailscaled(t, peers),
		AdminTailnetUsers: users,
		Clock:             clock.Fake{T: time.Unix(1_800_000_000, 0)},
		Assets:            fstest.MapFS{"dist/index.html": {Data: []byte("<!doctype html>shell")}},
	}
	return s, s.Handler()
}

func TestAdminAllowsAnIdentifiedTailnetDevice(t *testing.T) {
	s, handler := tailnetServer(t, nil, map[string]string{
		"100.80.189.68:5000": "owner@example.com",
	})

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, fromAddr(s, http.MethodGet, "/api/admin/session", "100.80.189.68:5000"))
	if res.Code != http.StatusNoContent {
		t.Fatalf("session from a tailnet device = %d, want 204", res.Code)
	}
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, fromAddr(s, http.MethodGet, "/admin", "100.80.189.68:5000"))
	if res.Code != http.StatusOK {
		t.Fatalf("/admin from a tailnet device = %d, want 200", res.Code)
	}
}

// An address inside Tailscale's range that the daemon does not recognise is
// not a peer. The range is shared by every tailnet, so membership proves
// nothing on its own -- this is the case that would be a hole if the check
// were only an address check.
func TestAdminRejectsAnUnrecognisedAddressInTailscaleRange(t *testing.T) {
	s, handler := tailnetServer(t, nil, map[string]string{
		"100.80.189.68:5000": "owner@example.com",
	})

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, fromAddr(s, http.MethodGet, "/api/admin/session", "100.99.99.99:5000"))
	if res.Code != http.StatusNotFound {
		t.Fatalf("session from an unknown 100.x address = %d, want 404", res.Code)
	}
}

// A device the daemon knows, owned by somebody else, is refused when the
// allowed accounts are named. Tailscale node sharing makes this reachable.
func TestAdminRejectsATailnetDeviceOwnedByAnotherAccount(t *testing.T) {
	s, handler := tailnetServer(t, []string{"owner@example.com"}, map[string]string{
		"100.80.189.68:5000": "owner@example.com",
		"100.80.189.99:5000": "someone-else@example.com",
	})

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, fromAddr(s, http.MethodGet, "/api/admin/session", "100.80.189.99:5000"))
	if res.Code != http.StatusNotFound {
		t.Fatalf("session from another account = %d, want 404", res.Code)
	}
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, fromAddr(s, http.MethodGet, "/api/admin/session", "100.80.189.68:5000"))
	if res.Code != http.StatusNoContent {
		t.Fatalf("session from the owner = %d, want 204", res.Code)
	}
}

// Logins are compared case-insensitively, since an account name is not
// case-sensitive and a capitalised copy in .env should not lock the operator
// out of their own panel.
func TestAdminMatchesTailnetLoginsCaseInsensitively(t *testing.T) {
	s, handler := tailnetServer(t, []string{"Owner@Example.COM"}, map[string]string{
		"100.80.189.68:5000": "owner@example.com",
	})

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, fromAddr(s, http.MethodGet, "/api/admin/session", "100.80.189.68:5000"))
	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", res.Code)
	}
}

// Addresses outside Tailscale's ranges never reach the daemon at all.
func TestAdminRejectsNonTailscaleAddressesWithoutAskingTheDaemon(t *testing.T) {
	s, handler := tailnetServer(t, nil, map[string]string{
		"203.0.113.7:5000": "owner@example.com", // would pass if it were asked
	})

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, fromAddr(s, http.MethodGet, "/api/admin/session", "203.0.113.7:5000"))
	if res.Code != http.StatusNotFound {
		t.Fatalf("session from a public address = %d, want 404", res.Code)
	}
}

// A daemon that cannot answer denies access rather than falling back to
// trusting the address.
func TestAdminClosesWhenTheDaemonIsUnreachable(t *testing.T) {
	s := &Server{
		AdminPassword: "correct horse",
		AdminTailnet:  tailnet.New(filepath.Join(t.TempDir(), "absent.sock")),
		Clock:         clock.Fake{T: time.Unix(1_800_000_000, 0)},
	}
	handler := s.Handler()

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, fromAddr(s, http.MethodGet, "/api/admin/session", "100.80.189.68:5000"))
	if res.Code != http.StatusNotFound {
		t.Fatalf("session with no daemon = %d, want 404", res.Code)
	}
}

// The address allowlist and the identity check are alternatives, so loopback
// keeps working for the machine itself while tailnet devices are identified.
func TestAdminAcceptsLoopbackAlongsideTailnetIdentity(t *testing.T) {
	s, handler := tailnetServer(t, []string{"owner@example.com"}, map[string]string{
		"100.80.189.68:5000": "owner@example.com",
	})
	s.AdminAllowedNets = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	handler = s.Handler()

	for _, remote := range []string{"127.0.0.1:41000", "100.80.189.68:5000"} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, fromAddr(s, http.MethodGet, "/api/admin/session", remote))
		if res.Code != http.StatusNoContent {
			t.Errorf("session from %s = %d, want 204", remote, res.Code)
		}
	}
}
