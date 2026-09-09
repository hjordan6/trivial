package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hjordan6/trivial/internal/accounts"
	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/mail"
	"github.com/hjordan6/trivial/internal/testsupport"
)

// These are the only tests in the repo that write to the shared test database
// without rolling back: Server holds a *pgxpool.Pool because it opens its own
// transactions, so testsupport.Tx's rollback pattern is not available here.
//
// Isolation instead comes from each test owning a unique frozen clock and a
// unique email prefix, so cleanup can delete exactly its own rows by equality
// rather than by guessing at a time range.
var testSeq atomic.Int64

const testClockBase = 1_800_000_000

type fakeSender struct {
	mu   sync.Mutex
	sent []mail.Message
	err  error
}

func (f *fakeSender) Send(_ context.Context, m mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeSender) last() mail.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return mail.Message{}
	}
	return f.sent[len(f.sent)-1]
}

type authFixture struct {
	server *Server
	sender *fakeSender
	pool   *pgxpool.Pool
	now    time.Time
	// clocks is every instant this fixture has run at. Player rows are stamped
	// with the server clock, so cleanup deletes by exact equality against these
	// rather than guessing a time range that could catch another test's rows.
	clocks []time.Time
	// prefix makes every address this test uses unique.
	prefix string
	// seq numbers this fixture, so a test can derive dates and slugs that no
	// other test in the shared database will collide with.
	seq int64
}

// at moves the fixture's clock, which is how a test reaches an expiry.
func (f *authFixture) at(t time.Time) {
	f.server.Clock = clock.Fake{T: t}
	f.clocks = append(f.clocks, t)
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	pool := testsupport.MustPool(t)
	n := testSeq.Add(1)
	now := time.Unix(testClockBase+n, 0).UTC()
	prefix := fmt.Sprintf("t%d-", n)
	sender := &fakeSender{}

	f := &authFixture{
		server: &Server{
			Pool:         pool,
			Clock:        clock.Fake{T: now},
			AppSecret:    testAppSecret,
			Mailer:       sender,
			CookieSecure: true,
		},
		sender: sender,
		pool:   pool,
		now:    now,
		clocks: []time.Time{now},
		prefix: prefix,
		seq:    n,
	}

	t.Cleanup(func() {
		ctx := context.Background()
		// Ordered so no foreign key is ever violated: players first (they
		// reference users), then the rows keyed on this test's addresses.
		if _, err := pool.Exec(ctx, `DELETE FROM players WHERE created_at = ANY($1)`, f.clocks); err != nil {
			t.Errorf("cleanup players: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM login_tokens WHERE email LIKE $1`, prefix+"%"); err != nil {
			t.Errorf("cleanup login_tokens: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE email LIKE $1`, prefix+"%"); err != nil {
			t.Errorf("cleanup users: %v", err)
		}
	})
	return f
}

func (f *authFixture) email(local string) string { return f.prefix + local + "@example.com" }

// do sends one request through the full handler, carrying cookies forward the
// way a browser would.
func (f *authFixture) do(t *testing.T, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = "203.0.113.7:1234"
	for _, c := range cookies {
		req.AddCookie(c)
	}
	res := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(res, req)
	return res
}

func cookieNamed(res *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range (&http.Response{Header: res.Header()}).Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// requestCode asks for a code and digs it back out of the fake sender's
// subject line, which is where a real player reads it from too.
func (f *authFixture) requestCode(t *testing.T, email string, cookies ...*http.Cookie) string {
	t.Helper()
	res := f.do(t, http.MethodPost, "/api/auth/code", map[string]string{"email": email}, cookies...)
	if res.Code != http.StatusAccepted {
		t.Fatalf("POST /api/auth/code status = %d, want 202: %s", res.Code, res.Body.String())
	}
	m := f.sender.last()
	_, code, found := strings.Cut(m.Subject, "code: ")
	if !found {
		t.Fatalf("no code in subject %q", m.Subject)
	}
	return code
}

// TestAuthIsUnavailableWithoutAMailer covers a zero-valued Server, which is what
// a deployment with no RESEND_API_KEY gets.
func TestAuthIsUnavailableWithoutAMailer(t *testing.T) {
	handler := (&Server{}).Handler()

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/auth/session", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("GET /api/auth/session status = %d, want 200", res.Code)
	}
	var got sessionResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Available || got.SignedIn {
		t.Errorf("session = %+v, want both false", got)
	}

	// Writes say so plainly rather than 404ing: the frontend needs to be able to
	// tell "no sign-in here" from "not signed in", which is why accounts do not
	// hide the way the admin surface does.
	for _, path := range []string{"/api/auth/code", "/api/auth/session"} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
		if res.Code != http.StatusServiceUnavailable {
			t.Errorf("POST %s status = %d, want 503", path, res.Code)
		}
	}
}

// TestRequestCodeAnswersIdenticallyForKnownAndUnknownAddresses is the
// enumeration guard: the two responses must be byte-for-byte the same.
func TestRequestCodeAnswersIdenticallyForKnownAndUnknownAddresses(t *testing.T) {
	f := newAuthFixture(t)
	known, unknown := f.email("known"), f.email("unknown")

	// Make `known` a real account first.
	code := f.requestCode(t, known)
	res := f.do(t, http.MethodPost, "/api/auth/session", map[string]string{"email": known, "code": code})
	if res.Code != http.StatusOK {
		t.Fatalf("sign-in status = %d, want 200: %s", res.Code, res.Body.String())
	}

	knownRes := f.do(t, http.MethodPost, "/api/auth/code", map[string]string{"email": known})
	unknownRes := f.do(t, http.MethodPost, "/api/auth/code", map[string]string{"email": unknown})

	if knownRes.Code != unknownRes.Code {
		t.Errorf("status %d for a known address, %d for an unknown one", knownRes.Code, unknownRes.Code)
	}
	if knownRes.Body.String() != unknownRes.Body.String() {
		t.Errorf("bodies differ:\n known:   %s\n unknown: %s", knownRes.Body, unknownRes.Body)
	}
}

func TestRequestCodeRejectsAMalformedAddressWithoutSending(t *testing.T) {
	f := newAuthFixture(t)

	res := f.do(t, http.MethodPost, "/api/auth/code", map[string]string{"email": "not-an-email"})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.Code)
	}
	if got := apiErrorCode(t, res); got != "invalid_email" {
		t.Errorf("code = %q, want invalid_email", got)
	}
	if f.sender.count() != 0 {
		t.Errorf("sent %d messages, want 0", f.sender.count())
	}
}

// Asking about sign-in must not mint a player row, or every crawler that loads
// the SPA creates one.
func TestReadOnlyAuthRoutesDoNotMintAPlayer(t *testing.T) {
	f := newAuthFixture(t)

	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/auth/session", nil},
		{http.MethodPost, "/api/auth/code", map[string]string{"email": f.email("nobody")}},
	} {
		res := f.do(t, tc.method, tc.path, tc.body)
		if c := cookieNamed(res, playerCookie); c != nil {
			t.Errorf("%s %s issued a player cookie", tc.method, tc.path)
		}
	}
	var players int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM players WHERE created_at=$1`, f.now).Scan(&players); err != nil {
		t.Fatal(err)
	}
	if players != 0 {
		t.Errorf("players created = %d, want 0", players)
	}
}

func TestSignInSetsCookiesAndAttachesThePlayer(t *testing.T) {
	f := newAuthFixture(t)
	email := f.email("player")

	code := f.requestCode(t, email)
	res := f.do(t, http.MethodPost, "/api/auth/session", map[string]string{"email": email, "code": code})
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", res.Code, res.Body.String())
	}

	var got sessionResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Available || !got.SignedIn || got.Email != email {
		t.Errorf("session = %+v, want signed in as %s", got, email)
	}

	session := cookieNamed(res, sessionCookie)
	if session == nil {
		t.Fatal("no session cookie was issued")
	}
	if !session.HttpOnly {
		t.Error("session cookie HttpOnly = false")
	}
	if !session.Secure {
		t.Error("session cookie Secure = false, want true when CookieSecure is set")
	}
	if session.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie SameSite = %v, want Lax", session.SameSite)
	}
	if session.MaxAge != int(sessionTTL/time.Second) {
		t.Errorf("session cookie MaxAge = %d, want %d", session.MaxAge, int(sessionTTL/time.Second))
	}

	player := cookieNamed(res, playerCookie)
	if player == nil {
		t.Fatal("no player cookie was issued")
	}
	playerID, ok := unsign(f.server.appKey(), playerCookie, player.Value)
	if !ok {
		t.Fatal("the player cookie is not signed")
	}

	var attached string
	err := f.pool.QueryRow(context.Background(),
		`SELECT u.email FROM players p JOIN users u ON u.id=p.user_id WHERE p.id=$1`, playerID).Scan(&attached)
	if err != nil {
		t.Fatalf("the player was not attached to a user: %v", err)
	}
	if attached != email {
		t.Errorf("attached to %q, want %q", attached, email)
	}

	// And the session reads back through the probe.
	probe := f.do(t, http.MethodGet, "/api/auth/session", nil, player, session)
	var back sessionResponse
	if err := json.Unmarshal(probe.Body.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if !back.SignedIn || back.Email != email {
		t.Errorf("probe = %+v, want signed in as %s", back, email)
	}
}

// TestSignInFailuresAreIndistinguishable is the other half of the enumeration
// guard: nothing about a rejected code may reveal whether a live code exists for
// an address, so wrong, expired, already-used and never-requested must all
// produce one byte-identical answer.
func TestSignInFailuresAreIndistinguishable(t *testing.T) {
	f := newAuthFixture(t)
	ttl := 10 * time.Minute
	f.server.Accounts = accounts.Config{CodeTTL: ttl}

	// A live code, to guess wrongly against.
	liveEmail := f.email("live")
	live := f.requestCode(t, liveEmail)
	wrong := "000000"
	if wrong == live {
		wrong = "111111"
	}

	// A code that has been used already.
	usedEmail := f.email("used")
	used := f.requestCode(t, usedEmail)
	if res := f.do(t, http.MethodPost, "/api/auth/session", map[string]string{"email": usedEmail, "code": used}); res.Code != http.StatusOK {
		t.Fatalf("priming a consumed code: status = %d", res.Code)
	}

	// A code that has aged out. Requested now, submitted after the clock moves.
	expiredEmail := f.email("expired")
	expired := f.requestCode(t, expiredEmail)

	cases := []struct {
		name  string
		email string
		code  string
		// at, when set, is the clock the submission happens at.
		at time.Time
	}{
		{name: "wrong digits", email: liveEmail, code: wrong},
		{name: "already consumed", email: usedEmail, code: used},
		{name: "never requested", email: f.email("stranger"), code: "123456"},
		{name: "expired", email: expiredEmail, code: expired, at: f.now.Add(ttl + time.Minute)},
	}

	var want string
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.at.IsZero() {
				f.at(tc.at)
				defer f.at(f.now)
			}
			res := f.do(t, http.MethodPost, "/api/auth/session", map[string]string{"email": tc.email, "code": tc.code})
			if res.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401: %s", res.Code, res.Body.String())
			}
			if i == 0 {
				want = res.Body.String()
				return
			}
			if res.Body.String() != want {
				t.Errorf("body differs from the wrong-digits case, which tells a caller\nwhether a live code exists:\n got:  %s want: %s", res.Body, want)
			}
		})
	}
}

func TestSignInRejectsAMalformedCodeBeforeTheDatabase(t *testing.T) {
	f := newAuthFixture(t)
	email := f.email("player")
	f.requestCode(t, email)

	res := f.do(t, http.MethodPost, "/api/auth/session", map[string]string{"email": email, "code": "12345"})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", res.Code, res.Body.String())
	}
	if got := apiErrorCode(t, res); got != "invalid_code" {
		t.Errorf("code = %q, want invalid_code", got)
	}
}

// A browser already claimed by one account must not be re-pointed at another:
// runs are keyed to the player, so moving it would hand one person's history to
// somebody else.
func TestSignInRefusesToSwitchAccounts(t *testing.T) {
	f := newAuthFixture(t)
	first, second := f.email("first"), f.email("second")

	code := f.requestCode(t, first)
	res := f.do(t, http.MethodPost, "/api/auth/session", map[string]string{"email": first, "code": code})
	if res.Code != http.StatusOK {
		t.Fatalf("first sign-in status = %d", res.Code)
	}
	player := cookieNamed(res, playerCookie)

	code2 := f.requestCode(t, second)
	res = f.do(t, http.MethodPost, "/api/auth/session", map[string]string{"email": second, "code": code2}, player)
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", res.Code, res.Body.String())
	}
	if got := apiErrorCode(t, res); got != "already_signed_in" {
		t.Errorf("code = %q, want already_signed_in", got)
	}
}

// Signing in again as yourself is how a returning player on the same browser
// behaves, and it must simply work.
func TestSignInAgainAsTheSameAccountSucceeds(t *testing.T) {
	f := newAuthFixture(t)
	email := f.email("player")

	code := f.requestCode(t, email)
	res := f.do(t, http.MethodPost, "/api/auth/session", map[string]string{"email": email, "code": code})
	if res.Code != http.StatusOK {
		t.Fatalf("first sign-in status = %d", res.Code)
	}
	player := cookieNamed(res, playerCookie)

	code2 := f.requestCode(t, email, player)
	res = f.do(t, http.MethodPost, "/api/auth/session", map[string]string{"email": email, "code": code2}, player)
	if res.Code != http.StatusOK {
		t.Fatalf("second sign-in status = %d, want 200: %s", res.Code, res.Body.String())
	}
}

func TestSignOutClearsTheSessionButKeepsTheAttachment(t *testing.T) {
	f := newAuthFixture(t)
	email := f.email("player")

	code := f.requestCode(t, email)
	res := f.do(t, http.MethodPost, "/api/auth/session", map[string]string{"email": email, "code": code})
	player, session := cookieNamed(res, playerCookie), cookieNamed(res, sessionCookie)
	playerID, _ := unsign(f.server.appKey(), playerCookie, player.Value)

	out := f.do(t, http.MethodDelete, "/api/auth/session", nil, player, session)
	if out.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", out.Code)
	}
	cleared := cookieNamed(out, sessionCookie)
	if cleared == nil || cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Errorf("session cookie was not cleared: %+v", cleared)
	}

	// players.user_id survives on purpose. Clearing it would stop this browser's
	// past runs counting toward the account, and would let the next person to
	// sign in here inherit them.
	var userID *int64
	if err := f.pool.QueryRow(context.Background(), `SELECT user_id FROM players WHERE id=$1`, playerID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if userID == nil {
		t.Error("signing out detached the browser from the account")
	}

	// And the probe now reports signed out, because the bearer is gone.
	probe := f.do(t, http.MethodGet, "/api/auth/session", nil, player)
	var got sessionResponse
	if err := json.Unmarshal(probe.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.SignedIn {
		t.Error("still signed in after sign-out")
	}
}

// The session cookie is a bearer token, so a forged or expired one must read as
// signed out rather than as its claimed user.
func TestSessionCookieRejectsForgeryAndExpiry(t *testing.T) {
	f := newAuthFixture(t)
	email := f.email("player")

	code := f.requestCode(t, email)
	res := f.do(t, http.MethodPost, "/api/auth/session", map[string]string{"email": email, "code": code})
	player, session := cookieNamed(res, playerCookie), cookieNamed(res, sessionCookie)

	var userID int64
	if err := f.pool.QueryRow(context.Background(), `SELECT id FROM users WHERE email=$1`, email).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	key := f.server.appKey()
	id := fmt.Sprintf("%d", userID)

	tests := []struct {
		name  string
		value string
	}{
		{"garbage", "not-a-session"},
		{"tampered mac", session.Value[:len(session.Value)-3] + "AAA"},
		{"unsigned", id},
		{"expired", signExpiring(key, sessionCookie, id, f.now.Add(-time.Minute))},
		{"signed as the player cookie", sign(key, playerCookie, id)},
		{"signed with another key", signExpiring(signingKey("a totally different secret"), sessionCookie, id, f.now.Add(time.Hour))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			probe := f.do(t, http.MethodGet, "/api/auth/session", nil, player,
				&http.Cookie{Name: sessionCookie, Value: tc.value})
			var got sessionResponse
			if err := json.Unmarshal(probe.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.SignedIn {
				t.Errorf("a %s session cookie was accepted", tc.name)
			}
		})
	}
}

// A session naming a user who does not own this browser must fail closed. This
// is what keeps the cookie (the bearer) and players.user_id (the authority) from
// ever disagreeing.
func TestSessionForAnotherBrowsersUserIsRejected(t *testing.T) {
	f := newAuthFixture(t)
	email := f.email("player")

	code := f.requestCode(t, email)
	res := f.do(t, http.MethodPost, "/api/auth/session", map[string]string{"email": email, "code": code})
	session := cookieNamed(res, sessionCookie)

	// A different, unattached browser presenting a perfectly valid session.
	var otherPlayer string
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO players(created_at,last_seen_at) VALUES($1,$1) RETURNING id`, f.now).Scan(&otherPlayer); err != nil {
		t.Fatal(err)
	}
	probe := f.do(t, http.MethodGet, "/api/auth/session", nil,
		&http.Cookie{Name: playerCookie, Value: sign(f.server.appKey(), playerCookie, otherPlayer)},
		session)

	var got sessionResponse
	if err := json.Unmarshal(probe.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.SignedIn {
		t.Error("a session was honoured on a browser that does not belong to that user")
	}
}

func TestRequestCodeReportsARefusedAddress(t *testing.T) {
	f := newAuthFixture(t)
	f.sender.err = fmt.Errorf("provider said no: %w", mail.ErrRejected)

	res := f.do(t, http.MethodPost, "/api/auth/code", map[string]string{"email": f.email("bounces")})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", res.Code, res.Body.String())
	}
	if got := apiErrorCode(t, res); got != "undeliverable_email" {
		t.Errorf("code = %q, want undeliverable_email", got)
	}
}

func TestRequestCodeReportsAProviderOutage(t *testing.T) {
	f := newAuthFixture(t)
	f.sender.err = fmt.Errorf("connection refused")

	res := f.do(t, http.MethodPost, "/api/auth/code", map[string]string{"email": f.email("player")})
	if res.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", res.Code, res.Body.String())
	}
	if got := apiErrorCode(t, res); got != "mail_failed" {
		t.Errorf("code = %q, want mail_failed", got)
	}
}

// A sign-in code proves you can read the address it was sent to, so it must
// leave the server by mail and no other route. This asserts on the raw body
// rather than a named field, because the danger is a future field of any name
// putting it back -- and it holds with DevelopmentMode on, which is exactly the
// misconfiguration that once turned a public server into an open door: anyone
// could ask for a stranger's code and read it out of the response.
// The staging escape hatch, and the three ways it stays shut. DEV_CODE_EMAILS
// is what makes sign-in usable on a server whose database is a copy of
// production: the listed test account can be signed into from the UI, while the
// real people in that copy cannot, even though they are in the same table.
func TestCodeIsEchoedOnlyToListedAddresses(t *testing.T) {
	// codeFor runs one request and returns what the response disclosed and what
	// was actually mailed, so every case below asserts on both.
	codeFor := func(t *testing.T, dev bool, allow []string, local string) (echoed, mailed string) {
		t.Helper()
		f := newAuthFixture(t)
		f.server.DevelopmentMode = dev
		// The list is normalized by config.emailList in production; the fixture
		// builds addresses that are already lowercase, so listing them raw here
		// matches what the loader would produce.
		for i, a := range allow {
			if a != "*" {
				allow[i] = f.email(a)
			}
		}
		f.server.DevCodeEmails = allow

		res := f.do(t, http.MethodPost, "/api/auth/code", map[string]string{"email": f.email(local)})
		if res.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202: %s", res.Code, res.Body.String())
		}
		var got codeRequested
		if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		_, mailed, found := strings.Cut(f.sender.last().Subject, "code: ")
		if !found {
			t.Fatalf("no code in subject %q", f.sender.last().Subject)
		}
		return got.DevCode, mailed
	}

	t.Run("listed address in development", func(t *testing.T) {
		echoed, mailed := codeFor(t, true, []string{"tester"}, "tester")
		if echoed != mailed {
			t.Errorf("dev_code = %q, want the mailed code %q", echoed, mailed)
		}
	})

	// The property the whole gate exists for: being on the staging server is
	// not enough, you have to be one of the addresses it was set up for.
	t.Run("unlisted address in development", func(t *testing.T) {
		echoed, _ := codeFor(t, true, []string{"tester"}, "stranger")
		if echoed != "" {
			t.Errorf("dev_code = %q for an address that is not listed", echoed)
		}
	})

	// The misconfiguration that caused 61e1816: development mode left on where
	// it should not be. On its own it now discloses nothing.
	t.Run("development mode alone echoes nothing", func(t *testing.T) {
		echoed, _ := codeFor(t, true, nil, "tester")
		if echoed != "" {
			t.Errorf("dev_code = %q with an empty DEV_CODE_EMAILS", echoed)
		}
	})

	// The other half: a list left in place on a production server. The flag is
	// off, so the list is inert.
	t.Run("a list without development mode echoes nothing", func(t *testing.T) {
		echoed, _ := codeFor(t, false, []string{"tester", "*"}, "tester")
		if echoed != "" {
			t.Errorf("dev_code = %q with DEVELOPMENT_MODE off", echoed)
		}
	})

	t.Run("wildcard in development", func(t *testing.T) {
		echoed, mailed := codeFor(t, true, []string{"*"}, "anyone")
		if echoed != mailed {
			t.Errorf("dev_code = %q, want the mailed code %q", echoed, mailed)
		}
	})
}

func TestCodeIsNeverInTheResponse(t *testing.T) {
	for _, dev := range []bool{false, true} {
		t.Run(fmt.Sprintf("development=%v", dev), func(t *testing.T) {
			f := newAuthFixture(t)
			f.server.DevelopmentMode = dev
			email := f.email("player")

			res := f.do(t, http.MethodPost, "/api/auth/code", map[string]string{"email": email})
			if res.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202: %s", res.Code, res.Body.String())
			}

			// What the player actually received, dug out of the mail the same
			// way requestCode does.
			_, code, found := strings.Cut(f.sender.last().Subject, "code: ")
			if !found {
				t.Fatalf("no code in subject %q", f.sender.last().Subject)
			}
			if body := res.Body.String(); strings.Contains(body, code) {
				t.Errorf("code %q leaked in the response body: %s", code, body)
			}
		})
	}
}

func TestRequestIP(t *testing.T) {
	tests := []struct {
		name       string
		trustProxy bool
		remote     string
		forwarded  string
		want       string
	}{
		{"public remote addr", false, "203.0.113.7:1234", "", "203.0.113.7"},
		// Without a trusted proxy, a private RemoteAddr means every request
		// shares one bucket, which would turn a per-IP limit into a global one.
		{"loopback is not limited", false, "127.0.0.1:1234", "", ""},
		{"private is not limited", false, "10.1.2.3:1234", "", ""},
		// The header is caller-supplied unless a proxy is trusted.
		{"forwarded header ignored by default", false, "10.1.2.3:1234", "198.51.100.9", ""},
		// The last hop is the one the proxy appended, so it is the only entry a
		// client cannot control.
		{"last forwarded hop wins", true, "10.1.2.3:1234", "198.51.100.9, 203.0.113.7", "203.0.113.7"},
		{"forwarded falls back to remote", true, "203.0.113.7:1234", "", "203.0.113.7"},
		{"unparseable remote addr", false, "not-an-address", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{TrustProxyIP: tt.trustProxy}
			req := httptest.NewRequest(http.MethodPost, "/api/auth/code", nil)
			req.RemoteAddr = tt.remote
			if tt.forwarded != "" {
				req.Header.Set("X-Forwarded-For", tt.forwarded)
			}
			if got := s.requestIP(req); got != tt.want {
				t.Errorf("requestIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A per-address limit must answer 202 like a success, because a 429 keyed on
// somebody else's address reveals that codes are being requested for it.
func TestPerAddressLimitLooksLikeSuccess(t *testing.T) {
	f := newAuthFixture(t)
	f.server.Accounts = accounts.Config{MaxPerEmailFast: 1, FastWindow: time.Hour}
	email := f.email("player")

	first := f.do(t, http.MethodPost, "/api/auth/code", map[string]string{"email": email})
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", first.Code)
	}
	second := f.do(t, http.MethodPost, "/api/auth/code", map[string]string{"email": email})
	if second.Code != http.StatusAccepted {
		t.Fatalf("second status = %d, want 202 so the limit is invisible", second.Code)
	}
	if second.Body.String() != first.Body.String() {
		t.Errorf("a suppressed request is distinguishable:\n got:  %s\n want: %s", second.Body, first.Body)
	}
	if f.sender.count() != 1 {
		t.Errorf("sent %d messages, want 1: the suppressed one must not go out", f.sender.count())
	}
}

func apiErrorCode(t *testing.T, res *httptest.ResponseRecorder) string {
	t.Helper()
	var e apiError
	if err := json.Unmarshal(res.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error body %q: %v", res.Body, err)
	}
	return e.Code
}

// TestFailedGuessesAccumulateAcrossRequests is a regression test for a real bug:
// the attempt counter was being written inside a transaction that the failure
// path rolled back, so the budget reset on every request and a six-digit code
// could be guessed indefinitely.
//
// It has to go through the handler. A store-level test cannot catch this,
// because it reads its own writes through a transaction that is never rolled
// back mid-test, which is exactly what hid the bug.
func TestFailedGuessesAccumulateAcrossRequests(t *testing.T) {
	f := newAuthFixture(t)
	f.server.Accounts = accounts.Config{MaxAttempts: 3}
	email := f.email("player")

	real := f.requestCode(t, email)
	wrong := "000000"
	if wrong == real {
		wrong = "111111"
	}

	for i := 1; i <= 3; i++ {
		res := f.do(t, http.MethodPost, "/api/auth/session", map[string]string{"email": email, "code": wrong})
		var count int
		if err := f.pool.QueryRow(context.Background(),
			`SELECT attempts FROM login_tokens WHERE email=$1 AND consumed_at IS NULL`, email).Scan(&count); err != nil {
			t.Fatalf("guess %d: reading the counter: %v", i, err)
		}
		if count != i {
			t.Fatalf("after %d wrong guesses attempts = %d, want %d: the budget is not accumulating", i, count, i)
		}
		if i < 3 && res.Code != http.StatusUnauthorized {
			t.Fatalf("guess %d status = %d, want 401", i, res.Code)
		}
	}

	// The budget is spent, so even the right code must now be refused.
	res := f.do(t, http.MethodPost, "/api/auth/session", map[string]string{"email": email, "code": real})
	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("the correct code after the budget was spent: status = %d, want 429: %s", res.Code, res.Body.String())
	}
	if got := apiErrorCode(t, res); got != "rate_limited" {
		t.Errorf("code = %q, want rate_limited", got)
	}
}
