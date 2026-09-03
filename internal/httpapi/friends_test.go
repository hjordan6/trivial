package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanNicknameTruncatesOnRuneBoundaries(t *testing.T) {
	// Each of these is multi-byte, so a byte-wise s[:40] lands mid-rune and
	// yields invalid UTF-8 -- which Postgres rejects outright on a text column.
	long := strings.Repeat("é", 45)
	got := cleanNickname(&long)
	if got == nil {
		t.Fatal("cleanNickname() = nil, want a value")
	}
	if !utf8.ValidString(*got) {
		t.Errorf("cleanNickname() = %q, which is not valid UTF-8", *got)
	}
	if n := utf8.RuneCountInString(*got); n != 40 {
		t.Errorf("rune count = %d, want 40", n)
	}
}

func TestCleanNicknameKeepsShortNamesWhole(t *testing.T) {
	for _, in := range []string{"jordan", "Ana María", "🎯 quizmaster"} {
		v := in
		got := cleanNickname(&v)
		if got == nil || *got != in {
			t.Errorf("cleanNickname(%q) = %v, want %q", in, got, in)
		}
	}
}

func TestCleanNicknameRejectsBlank(t *testing.T) {
	for _, in := range []string{"", "   ", "\t\n"} {
		v := in
		if got := cleanNickname(&v); got != nil {
			t.Errorf("cleanNickname(%q) = %q, want nil", in, *got)
		}
	}
}

// signIn takes a fixture through the real sign-in flow and returns the two
// cookies a browser would then be holding.
//
// Exactly one code is requested. Asking twice would not merely spend rate
// budget: RequestCode supersedes every live code for an address, so a second
// request silently invalidates the code the first one returned.
//
// No player cookie is sent. createSession calls requirePlayer, which creates a
// player and sets the cookie on the response when the request carries none --
// so both cookies come back from this one call.
func signIn(t *testing.T, f *authFixture, local string) []*http.Cookie {
	t.Helper()
	email := f.email(local)
	code := f.requestCode(t, email)

	res := f.do(t, http.MethodPost, "/api/auth/session",
		map[string]string{"email": email, "code": code})
	if res.Code != http.StatusOK {
		t.Fatalf("sign-in status = %d, want 200: %s", res.Code, res.Body.String())
	}
	player := cookieNamed(res, playerCookie)
	if player == nil {
		t.Fatal("no player cookie after sign-in")
	}
	session := cookieNamed(res, sessionCookie)
	if session == nil {
		t.Fatal("no session cookie after sign-in")
	}
	return []*http.Cookie{player, session}
}

func TestMintInviteReturnsAStableLink(t *testing.T) {
	f := newAuthFixture(t)
	cookies := signIn(t, f, "sender")

	res := f.do(t, http.MethodPost, "/api/friends/invite", map[string]string{"nickname": "Sam"}, cookies...)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	var first struct{ Token, URL, Nickname string }
	if err := json.Unmarshal(res.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Token == "" {
		t.Error("token is empty")
	}
	if first.URL != "/f/"+first.Token {
		t.Errorf("url = %q, want /f/%s", first.URL, first.Token)
	}
	if first.Nickname != "Sam" {
		t.Errorf("nickname = %q, want Sam", first.Nickname)
	}

	// Pressing the button again renames but must not re-mint.
	res = f.do(t, http.MethodPost, "/api/friends/invite", map[string]string{"nickname": "Sammy"}, cookies...)
	var second struct{ Token, URL, Nickname string }
	if err := json.Unmarshal(res.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.Token != first.Token {
		t.Errorf("token = %q, want the original %q", second.Token, first.Token)
	}
	if second.Nickname != "Sammy" {
		t.Errorf("nickname = %q, want Sammy", second.Nickname)
	}
}

// A blank nickname falls back to the address's local part rather than failing:
// the button's job is to produce a link.
func TestMintInviteFallsBackToTheAddressLocalPart(t *testing.T) {
	f := newAuthFixture(t)
	cookies := signIn(t, f, "sender")

	res := f.do(t, http.MethodPost, "/api/friends/invite", map[string]string{"nickname": "  "}, cookies...)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	var got struct{ Nickname string }
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// f.email("sender") is "<prefix>sender@example.com", so the local part is
	// the whole thing before the @.
	want := strings.Split(f.email("sender"), "@")[0]
	if got.Nickname != want {
		t.Errorf("nickname = %q, want %q", got.Nickname, want)
	}
}

func TestMintInviteRequiresSignIn(t *testing.T) {
	f := newAuthFixture(t)

	res := f.do(t, http.MethodPost, "/api/friends/invite", map[string]string{"nickname": "Sam"})
	if res.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401: %s", res.Code, res.Body.String())
	}
}

func TestReadInviteIsPublicAndNamesTheSender(t *testing.T) {
	f := newAuthFixture(t)
	cookies := signIn(t, f, "sender")
	res := f.do(t, http.MethodPost, "/api/friends/invite", map[string]string{"nickname": "Sam"}, cookies...)
	var minted struct{ Token string }
	if err := json.Unmarshal(res.Body.Bytes(), &minted); err != nil {
		t.Fatal(err)
	}

	// No cookies at all: a recipient who has never visited must be able to read it.
	res = f.do(t, http.MethodGet, "/api/friends/invite/"+minted.Token, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}
	var got struct{ Nickname string }
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Nickname != "Sam" {
		t.Errorf("nickname = %q, want Sam", got.Nickname)
	}
}

func TestReadInviteRejectsAnUnknownToken(t *testing.T) {
	f := newAuthFixture(t)

	res := f.do(t, http.MethodGet, "/api/friends/invite/nosuchtoken", nil)
	if res.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404: %s", res.Code, res.Body.String())
	}
}

// Friends are a public feature, so a server with no sign-in says so plainly
// rather than 404ing the way the admin surface does -- the frontend has to be
// able to tell "no accounts here" from "not signed in" in order to hide the
// button.
func TestFriendRoutesAreUnavailableWithoutAMailer(t *testing.T) {
	handler := (&Server{}).Handler()

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/friends/invite"},
		{http.MethodGet, "/api/friends/invite/anything"},
	} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`)))
		if res.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s status = %d, want 503", tc.method, tc.path, res.Code)
		}
	}
}
