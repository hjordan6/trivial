package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/hjordan6/trivial/internal/clock"
)

const handoffPlayer = "6b7a1c4e-0d2f-4a58-9b31-8f0c5e2d7a44"

func handoffServer(now time.Time) *Server {
	return &Server{
		AppSecret:     testAppSecret,
		Clock:         clock.Fake{T: now},
		CanonicalHost: "dailytrivial.com",
		MovedFrom:     []string{"trivial.jordanlabs.cc", "trivia.jordanlabs.cc"},
	}
}

func cookiesOf(res *httptest.ResponseRecorder) map[string]*http.Cookie {
	out := map[string]*http.Cookie{}
	for _, c := range (&http.Response{Header: res.Header()}).Cookies() {
		out[c.Name] = c
	}
	return out
}

// TestMovedHostWithoutCookiesRedirectsPlainly: a visitor the old host knows
// nothing about has nothing to carry, so they go straight to the same path on
// the new host and no token is minted.
func TestMovedHostWithoutCookiesRedirectsPlainly(t *testing.T) {
	s := handoffServer(time.Now())
	req := httptest.NewRequest(http.MethodGet, "http://trivial.jordanlabs.cc/f/abc?x=1", nil)
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", res.Code)
	}
	if got := res.Header().Get("Location"); got != "https://dailytrivial.com/f/abc?x=1" {
		t.Errorf("Location = %q", got)
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store: the answer depends on cookies", got)
	}
}

// TestMovedHostMatchesCaseAndPort: Host headers are case-insensitive and may
// carry a port, and neither should let a visitor slip past the redirect.
func TestMovedHostMatchesCaseAndPort(t *testing.T) {
	s := handoffServer(time.Now())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "Trivia.JordanLabs.cc:443"
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", res.Code)
	}
}

// TestMovedHostLeavesAPIAndAssetsAlone: a tab already open on the old host
// must be able to finish today's game. Its fetches and lazily loaded chunks
// cannot follow a cross-origin redirect, so they are served where they are.
func TestMovedHostLeavesAPIAndAssetsAlone(t *testing.T) {
	s := handoffServer(time.Now())
	h := s.Handler()
	for _, path := range []string{"/healthz", "/api/not-a-route", "/assets/index-abc.js"} {
		req := httptest.NewRequest(http.MethodGet, "http://trivial.jordanlabs.cc"+path, nil)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code == http.StatusFound {
			t.Errorf("%s was redirected; it must be served on the old host", path)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "http://trivial.jordanlabs.cc/api/not-a-route", nil)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code == http.StatusFound {
		t.Error("POST was redirected; only page loads should be")
	}
}

// TestOtherHostsAreNotRedirected: loopback, the LAN address and the new host
// itself are all served normally. Only hosts named in MovedFrom move.
func TestOtherHostsAreNotRedirected(t *testing.T) {
	s := handoffServer(time.Now())
	h := s.Handler()
	for _, host := range []string{"dailytrivial.com", "127.0.0.1:8092", "test-trivial.jordanlabs.cc"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code == http.StatusFound {
			t.Errorf("host %s was redirected", host)
		}
	}
}

func TestNoCanonicalHostMeansNoRedirect(t *testing.T) {
	s := &Server{AppSecret: testAppSecret, MovedFrom: []string{"trivial.jordanlabs.cc"}}
	req := httptest.NewRequest(http.MethodGet, "http://trivial.jordanlabs.cc/", nil)
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code == http.StatusFound {
		t.Error("redirected with no CanonicalHost configured")
	}
}

// handoffRoundTrip runs a visit to the old host with the given cookies, follows
// the redirect to the new host carrying newHostCookies, and returns the claim
// response.
func handoffRoundTrip(t *testing.T, s *Server, oldPath string, oldCookies, newHostCookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	h := s.Handler()
	req := httptest.NewRequest(http.MethodGet, "http://trivial.jordanlabs.cc"+oldPath, nil)
	for _, c := range oldCookies {
		req.AddCookie(c)
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusFound {
		t.Fatalf("old host status = %d, want 302", res.Code)
	}
	loc, err := url.Parse(res.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Scheme != "https" || loc.Host != "dailytrivial.com" || loc.Path != handoffPath {
		t.Fatalf("Location = %s, want https://dailytrivial.com%s?t=...", loc, handoffPath)
	}
	if len(cookiesOf(res)) != 0 {
		t.Error("the old host set cookies; it should leave its own alone")
	}

	claim := httptest.NewRequest(http.MethodGet, loc.String(), nil)
	for _, c := range newHostCookies {
		claim.AddCookie(c)
	}
	claimRes := httptest.NewRecorder()
	h.ServeHTTP(claimRes, claim)
	if claimRes.Code != http.StatusFound {
		t.Fatalf("claim status = %d, want 302", claimRes.Code)
	}
	return claimRes
}

// TestHandoffCarriesAnonymousPlayer is the case the handoff exists for: a
// player with no email, whose history would otherwise be stranded on the old
// host.
func TestHandoffCarriesAnonymousPlayer(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	s := handoffServer(now)
	player := &http.Cookie{Name: playerCookie, Value: sign(s.appKey(), playerCookie, handoffPlayer)}

	res := handoffRoundTrip(t, s, "/f/abc?ref=share", []*http.Cookie{player}, nil)

	if got := res.Header().Get("Location"); got != "/f/abc?ref=share" {
		t.Errorf("claim Location = %q, want the original path and query", got)
	}
	got := cookiesOf(res)
	c, ok := got[playerCookie]
	if !ok {
		t.Fatal("no player cookie set on the new host")
	}
	if id, ok := unsign(s.appKey(), playerCookie, c.Value); !ok || id != handoffPlayer {
		t.Errorf("player cookie = %q (ok %v), want %s", id, ok, handoffPlayer)
	}
	if c.MaxAge != playerCookieMaxAge || !c.HttpOnly {
		t.Errorf("player cookie attributes differ from setPlayerCookie: %+v", c)
	}
	if _, ok := got[sessionCookie]; ok {
		t.Error("a session cookie was set for a player who was not signed in")
	}
}

// TestHandoffCarriesLegacyUnsignedPlayer: optionalPlayer still accepts the
// bare-UUID cookies issued before signing, so the handoff must too, or the
// oldest players are the ones left behind.
func TestHandoffCarriesLegacyUnsignedPlayer(t *testing.T) {
	s := handoffServer(time.Now())
	player := &http.Cookie{Name: playerCookie, Value: handoffPlayer}
	res := handoffRoundTrip(t, s, "/", []*http.Cookie{player}, nil)
	if id, ok := unsign(s.appKey(), playerCookie, cookiesOf(res)[playerCookie].Value); !ok || id != handoffPlayer {
		t.Errorf("player = %q, want %s", id, handoffPlayer)
	}
}

func TestHandoffCarriesSession(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	s := handoffServer(now)
	player := &http.Cookie{Name: playerCookie, Value: sign(s.appKey(), playerCookie, handoffPlayer)}
	session := &http.Cookie{Name: sessionCookie, Value: signExpiring(s.appKey(), sessionCookie, "42", now.Add(time.Hour))}

	res := handoffRoundTrip(t, s, "/", []*http.Cookie{player, session}, nil)

	c, ok := cookiesOf(res)[sessionCookie]
	if !ok {
		t.Fatal("no session cookie set on the new host")
	}
	if id, ok := unsignExpiring(s.appKey(), sessionCookie, c.Value, now); !ok || id != "42" {
		t.Errorf("session = %q (ok %v), want 42", id, ok)
	}
}

// TestHandoffDropsAnExpiredSession: the old host vouches only for what it
// would itself have honoured.
func TestHandoffDropsAnExpiredSession(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	s := handoffServer(now)
	player := &http.Cookie{Name: playerCookie, Value: sign(s.appKey(), playerCookie, handoffPlayer)}
	session := &http.Cookie{Name: sessionCookie, Value: signExpiring(s.appKey(), sessionCookie, "42", now.Add(-time.Hour))}

	res := handoffRoundTrip(t, s, "/", []*http.Cookie{player, session}, nil)
	if _, ok := cookiesOf(res)[sessionCookie]; ok {
		t.Error("an expired session was carried over")
	}
	if _, ok := cookiesOf(res)[playerCookie]; !ok {
		t.Error("the player was not carried over alongside the dropped session")
	}
}

// TestHandoffNeverOverwritesTheNewHost: a browser that already has a player on
// the new host may have played there. Replacing it would strand that run, so
// the handoff only fills an empty browser.
func TestHandoffNeverOverwritesTheNewHost(t *testing.T) {
	s := handoffServer(time.Now())
	old := &http.Cookie{Name: playerCookie, Value: sign(s.appKey(), playerCookie, handoffPlayer)}
	existing := &http.Cookie{Name: playerCookie, Value: sign(s.appKey(), playerCookie, "11111111-2222-4333-8444-555555555555")}

	res := handoffRoundTrip(t, s, "/", []*http.Cookie{old}, []*http.Cookie{existing})
	if len(cookiesOf(res)) != 0 {
		t.Errorf("cookies were set over an existing player: %v", cookiesOf(res))
	}
}

func TestHandoffTokenExpires(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	s := handoffServer(now)
	token := signExpiring(s.appKey(), handoffCookie, handoffPlayer+"||/", now.Add(-time.Second))

	req := httptest.NewRequest(http.MethodGet, "https://dailytrivial.com"+handoffPath+"?t="+url.QueryEscape(token), nil)
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Header().Get("Location") != "/" || len(cookiesOf(res)) != 0 {
		t.Errorf("expired token: Location %q, cookies %v; want / and none", res.Header().Get("Location"), cookiesOf(res))
	}
}

// TestHandoffRejectsForgedTokens: the token is a bearer credential for a
// player, so anything not minted by this server -- including a value signed
// for one of the real cookies -- must be refused.
func TestHandoffRejectsForgedTokens(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	s := handoffServer(now)
	for name, token := range map[string]string{
		"garbage":          "nope",
		"other key":        signExpiring(signingKey("another secret entirely"), handoffCookie, handoffPlayer+"||/", now.Add(time.Minute)),
		"session-cookie":   signExpiring(s.appKey(), sessionCookie, handoffPlayer+"||/", now.Add(time.Minute)),
		"not a player id":  signExpiring(s.appKey(), handoffCookie, "junk||/", now.Add(time.Minute)),
		"offsite next":     signExpiring(s.appKey(), handoffCookie, handoffPlayer+"||//evil.example/", now.Add(time.Minute)),
		"backslash next":   signExpiring(s.appKey(), handoffCookie, handoffPlayer+`||/\evil.example/`, now.Add(time.Minute)),
		"absolute next":    signExpiring(s.appKey(), handoffCookie, handoffPlayer+"||https://evil.example/", now.Add(time.Minute)),
		"non-numeric user": signExpiring(s.appKey(), handoffCookie, handoffPlayer+"|x|/", now.Add(time.Minute)),
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "https://dailytrivial.com"+handoffPath+"?t="+url.QueryEscape(token), nil)
			res := httptest.NewRecorder()
			s.Handler().ServeHTTP(res, req)
			if res.Header().Get("Location") != "/" {
				t.Errorf("Location = %q, want /", res.Header().Get("Location"))
			}
			if len(cookiesOf(res)) != 0 {
				t.Errorf("cookies set from a bad token: %v", cookiesOf(res))
			}
		})
	}
}
