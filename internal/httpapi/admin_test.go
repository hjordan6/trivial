package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/hjordan6/trivial/internal/clock"
)

// adminRoutes is every gated route, as method/path pairs.
var adminRoutes = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/api/admin/session"},
	{http.MethodGet, "/api/admin/puzzles"},
	{http.MethodPut, "/api/admin/puzzles/2026-09-01/topics"},
	{http.MethodDelete, "/api/admin/puzzles/2026-09-01/topics"},
	{http.MethodPost, "/api/admin/puzzles/generate"},
	{http.MethodGet, "/api/admin/topics"},
	{http.MethodPatch, "/api/admin/topics/history"},
	{http.MethodGet, "/api/admin/questions"},
	{http.MethodPost, "/api/admin/questions/import"},
}

func TestAdminRoutesAreInvisibleWithoutAPassword(t *testing.T) {
	// A zero-valued Server has no admin password, which is what a deployment
	// that never set ADMIN_PASSWORD gets.
	handler := (&Server{}).Handler()
	for _, route := range adminRoutes {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(route.method, route.path, nil))
		if res.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", route.method, route.path, res.Code)
		}
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/admin/login",
		strings.NewReader(`{"password":"anything"}`)))
	if res.Code != http.StatusNotFound {
		t.Errorf("login = %d, want 404 when no password is configured", res.Code)
	}
}

func TestAdminRoutesRejectMissingAndForgedCookies(t *testing.T) {
	s := &Server{AdminPassword: "correct horse", Clock: clock.Fake{T: time.Unix(1_800_000_000, 0)}}
	handler := s.Handler()

	valid := s.signAdminSession(s.now().Add(time.Hour))
	cases := []struct {
		name   string
		cookie string
		want   int
	}{
		{"no cookie", "", http.StatusNotFound},
		{"garbage", "not-a-session", http.StatusNotFound},
		{"no signature", "MTgwMDAwMzYwMA", http.StatusNotFound},
		{"tampered signature", valid[:len(valid)-3] + "AAA", http.StatusNotFound},
		{"expired", s.signAdminSession(s.now().Add(-time.Minute)), http.StatusNotFound},
		{"valid", valid, http.StatusNoContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/admin/session", nil)
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: adminCookie, Value: tc.cookie})
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != tc.want {
				t.Fatalf("status = %d, want %d", res.Code, tc.want)
			}
		})
	}
}

func TestAdminLoginIssuesAndRejects(t *testing.T) {
	s := &Server{AdminPassword: "correct horse", Clock: clock.Fake{T: time.Unix(1_800_000_000, 0)}}
	handler := s.Handler()

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/admin/login",
		strings.NewReader(`{"password":"wrong"}`)))
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401", res.Code)
	}
	if len(res.Result().Cookies()) != 0 {
		t.Fatal("a failed login set a cookie")
	}

	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/admin/login",
		strings.NewReader(`{"password":"correct horse"}`)))
	if res.Code != http.StatusNoContent {
		t.Fatalf("correct password = %d, want 204", res.Code)
	}
	var session *http.Cookie
	for _, c := range res.Result().Cookies() {
		if c.Name == adminCookie {
			session = c
		}
	}
	if session == nil {
		t.Fatal("no admin cookie was set")
	}
	if !session.HttpOnly {
		t.Error("admin cookie is not HttpOnly")
	}
	if session.SameSite != http.SameSiteLaxMode {
		t.Error("admin cookie is not SameSite=Lax, which is what blocks cross-site writes")
	}
	if !s.validAdminSession(session.Value, s.now()) {
		t.Error("the issued cookie does not validate")
	}
}

// A session must not survive a password change, since the signing key is
// derived from the password itself.
func TestAdminSessionDiesWithThePassword(t *testing.T) {
	old := &Server{AdminPassword: "first", Clock: clock.Fake{T: time.Unix(1_800_000_000, 0)}}
	value := old.signAdminSession(old.now().Add(time.Hour))

	rotated := &Server{AdminPassword: "second", Clock: old.Clock}
	if rotated.validAdminSession(value, rotated.now()) {
		t.Fatal("a session signed with the old password still validates")
	}
}

func TestSPAFallbackServesTheShellForClientRoutes(t *testing.T) {
	assets := fstest.MapFS{
		"dist/index.html":          {Data: []byte("<!doctype html>shell")},
		"dist/assets/index-abc.js": {Data: []byte("console.log(1)")},
	}
	handler := (&Server{Assets: assets}).Handler()

	cases := []struct {
		path     string
		want     int
		wantBody string
	}{
		{"/", http.StatusOK, "shell"},
		{"/admin", http.StatusOK, "shell"},
		{"/c/some-token", http.StatusOK, "shell"},
		{"/assets/index-abc.js", http.StatusOK, "console.log(1)"},
		// A hashed asset that is genuinely absent must stay a 404, or a broken
		// deploy shows up as a script parse error instead.
		{"/assets/index-missing.js", http.StatusNotFound, ""},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if res.Code != tc.want {
				t.Fatalf("status = %d, want %d", res.Code, tc.want)
			}
			if tc.wantBody != "" && !strings.Contains(res.Body.String(), tc.wantBody) {
				t.Fatalf("body = %q, want it to contain %q", res.Body.String(), tc.wantBody)
			}
		})
	}
}
