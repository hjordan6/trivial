package httpapi

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hjordan6/trivial/internal/accounts"
	"github.com/hjordan6/trivial/internal/mail"
)

// failureDelay is the flat pause on a rejected code, matching adminLogin. It is
// the whole rate-limiting story for a single connection: four guesses a second
// against a budget of five.
const failureDelay = 250 * time.Millisecond

// sessionResponse is the shape every auth route answers with.
//
// available is separate from signed_in because the frontend has to distinguish
// "you are not signed in" from "this server has no sign-in", and it cannot read
// the HttpOnly cookie to work either out for itself.
type sessionResponse struct {
	Available bool   `json:"available"`
	SignedIn  bool   `json:"signed_in"`
	Email     string `json:"email,omitempty"`
}

// accountsEnabled reports whether sign-in exists on this server.
//
// Unlike the admin surface, a disabled account system does not hide behind 404
// on every route: admin is an operator tool that benefits from being
// indistinguishable from absent, while accounts are a public feature the
// frontend must be able to ask about so it can hide the prompt.
func (s *Server) accountsEnabled() bool {
	return s.Mailer != nil && s.AppSecret != ""
}

func (s *Server) accountsConfig() accounts.Config {
	cfg := s.Accounts
	cfg.Key = s.appKey()
	return cfg
}

// requestLoginCode emails a six-digit code.
//
// It answers 202 identically whether or not the address is a known account. That
// falls out for free rather than being carefully arranged: no user row exists
// until a code is verified, so this handler genuinely cannot tell.
func (s *Server) requestLoginCode(w http.ResponseWriter, r *http.Request) {
	if !s.accountsEnabled() {
		s.fail(w, http.StatusServiceUnavailable, "accounts_unavailable", "Sign-in is not available on this server.")
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if err := decodeOptional(r, &body); err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	email, err := accounts.NormalizeEmail(body.Email)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_email", "That does not look like an email address.")
		return
	}

	code, err := accounts.RequestCode(r.Context(), s.Pool, s.accountsConfig(), email, s.requestIP(r), s.now())
	switch {
	case errors.Is(err, accounts.ErrTooManyRequests):
		// Per-email limits answer 202 and silently drop the mail. A 429 keyed on
		// an email address is an oracle: it tells the caller that somebody has
		// been requesting codes for a stranger's address. Only a per-IP breach
		// earns a 429, because that is a statement about the caller themselves.
		if errors.Is(err, accounts.ErrTooManyRequestsFromIP) {
			s.fail(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again in a few minutes.")
			return
		}
		s.logger().Info("login code suppressed by a per-address limit", "reason", err)
		s.write(w, http.StatusAccepted, codeRequested{Status: "sent", Message: codeSentMessage})
		return
	case err != nil:
		s.internal(w, err)
		return
	}

	// The code must never be logged here. mail.Logger is the single deliberate
	// exception, and it exists precisely so this layer does not have to be.
	if err := s.Mailer.Send(r.Context(), loginCodeMessage(email, code)); err != nil {
		if errors.Is(err, mail.ErrRejected) {
			s.fail(w, http.StatusBadRequest, "undeliverable_email", "That address was refused. Check it for typos.")
			return
		}
		s.logger().Error("send login code", "error", err)
		s.fail(w, http.StatusBadGateway, "mail_failed", "We could not send that code. Try again.")
		return
	}

	out := codeRequested{Status: "sent", Message: codeSentMessage}
	if s.showCodeTo(email) {
		out.DevCode = code
	}
	s.write(w, http.StatusAccepted, out)
}

// showCodeTo reports whether this server may hand the six-digit code straight
// back to whoever asked for it, instead of only mailing it.
//
// This once existed as a bare DevelopmentMode check and was removed in 61e1816,
// because the flag was on in a deployed environment: anyone could ask for a
// stranger's code, read it out of the JSON, and sign in as them. A code proves
// you can read the address it was sent to, and echoing it destroys that proof.
//
// It is back for the staging deployment, which needs sign-in to work against a
// copy of production data without mailing the real people in it -- but gated on
// a second, independent setting. DEV_CODE_EMAILS names the addresses this may
// happen for, so a server that merely has DEVELOPMENT_MODE left on by accident
// still echoes nothing, and a staging server exposes only the test accounts
// deliberately listed rather than all 16 real ones. Two mistakes are needed
// where one used to do.
//
// "*" restores the old blanket behaviour. It is deliberately spelled as a
// value an operator has to type, not a default, and belongs only on a server
// nobody else can reach.
func (s *Server) showCodeTo(email string) bool {
	if !s.DevelopmentMode {
		return false
	}
	for _, allowed := range s.DevCodeEmails {
		// email is already normalized by NormalizeEmail and the list by
		// config.emailList, so both sides are lowercase and trimmed.
		if allowed == "*" || allowed == email {
			return true
		}
	}
	return false
}

// The spam hint is temporary, and matches the wording in SignIn.vue: the
// sending domain is new, so Gmail still files some codes as spam while its
// reputation builds. Both come out together once delivery settles.
const codeSentMessage = "If that address can receive mail, a code is on its way. " +
	"If it is not in your inbox, check your spam folder."

type codeRequested struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	// DevCode is the sign-in code itself, present only when showCodeTo allows
	// it. omitempty keeps the field out of the JSON entirely otherwise, so a
	// production response is byte-for-byte what it was before this existed.
	DevCode string `json:"dev_code,omitempty"`
}

// loginCodeMessage puts the code in the subject as well as the body, so it is
// readable from a notification without opening the mail. That is the whole
// payoff of choosing a code over a link: the player never leaves their tab.
func loginCodeMessage(email, code string) mail.Message {
	return mail.Message{
		To:      email,
		Subject: "Your Trivial sign-in code: " + code,
		Text: code + " is your Trivial sign-in code.\n\n" +
			"It works once, and only in the tab you started in. If you asked for\n" +
			"more than one code, only the newest one works.\n\n" +
			"If you did not ask for this, you can ignore this email.",
	}
}

// createSession verifies a code and signs the player in.
func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	if !s.accountsEnabled() {
		s.fail(w, http.StatusServiceUnavailable, "accounts_unavailable", "Sign-in is not available on this server.")
		return
	}
	var body struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := decodeOptional(r, &body); err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	email, err := accounts.NormalizeEmail(body.Email)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_email", "That does not look like an email address.")
		return
	}

	playerID, ok := s.requirePlayer(w, r)
	if !ok {
		return
	}

	// Refuse to move a browser that already belongs to somebody else. Letting
	// Attach re-point it would silently transfer every run this browser ever
	// played to the new account, because runs are keyed to the player, not the
	// user. One keystroke of convenience is not worth that.
	existing, err := accounts.ForPlayer(r.Context(), s.Pool, playerID)
	if err != nil {
		s.internal(w, err)
		return
	}
	if existing != nil && existing.Email != email {
		s.fail(w, http.StatusConflict, "already_signed_in",
			fmt.Sprintf("This browser is already signed in as %s. Sign out first.", existing.Email))
		return
	}

	now := s.now()
	// Deliberately NOT wrapped in a transaction.
	//
	// VerifyCode records a failed guess by incrementing login_tokens.attempts,
	// and that increment is the entire brute-force defence for a six-digit code.
	// Inside a transaction the failure path rolls it back, so the budget never
	// accumulates and the code becomes guessable at leisure -- the counter reads
	// zero no matter how many wrong guesses arrive.
	//
	// What the transaction bought was atomicity across verify-and-attach. Losing
	// it means a crash in between can consume a code without attaching the
	// browser, and the player asks for another one. That is a far cheaper failure
	// than a rate limiter that silently does nothing.
	user, err := accounts.VerifyCode(r.Context(), s.Pool, s.accountsConfig(), email, body.Code, now)
	if err != nil {
		s.authError(w, err)
		return
	}
	if err := accounts.Attach(r.Context(), s.Pool, playerID, user.ID); err != nil {
		s.internal(w, err)
		return
	}

	s.setSessionCookie(w, user.ID, now)
	s.write(w, http.StatusOK, sessionResponse{Available: true, SignedIn: true, Email: user.Email})
}

// currentSession is the probe the frontend uses, because the session cookie is
// HttpOnly and the browser cannot answer the question itself.
//
// It uses optionalPlayer rather than requirePlayer: asking whether you are
// signed in must not mint a player row, or every crawler hitting the SPA
// creates one.
func (s *Server) currentSession(w http.ResponseWriter, r *http.Request) {
	if !s.accountsEnabled() {
		s.write(w, http.StatusOK, sessionResponse{})
		return
	}
	playerID := s.optionalPlayer(r)
	if playerID == "" {
		s.write(w, http.StatusOK, sessionResponse{Available: true})
		return
	}
	user, err := s.signedInUser(r, playerID)
	if err != nil {
		s.internal(w, err)
		return
	}
	if user == nil {
		s.write(w, http.StatusOK, sessionResponse{Available: true})
		return
	}
	// Slide the expiry forward so an active player's sign-in never lapses.
	s.setSessionCookie(w, user.ID, s.now())
	s.write(w, http.StatusOK, sessionResponse{Available: true, SignedIn: true, Email: user.Email})
}

// destroySession signs out.
//
// It clears the session cookie and nothing else. players.user_id deliberately
// stays set: it records which browsers belong to whom, so clearing it would stop
// this browser's past runs counting toward the account. It is also what keeps
// the already_signed_in guard working after a sign-out, so a second person on a
// shared computer cannot inherit the first person's history.
func (s *Server) destroySession(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, userID int64, now time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    signExpiring(s.appKey(), sessionCookie, strconv.FormatInt(userID, 10), now.Add(sessionTTL)),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL / time.Second),
	})
}

// sessionUserID reads the user out of the session cookie. No database.
func (s *Server) sessionUserID(r *http.Request) (int64, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return 0, false
	}
	payload, ok := unsignExpiring(s.appKey(), sessionCookie, c.Value, s.now())
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(payload, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// signedInUser resolves who the caller is, or nil when nobody.
//
// The session cookie is the bearer and players.user_id is the authority: a
// cookie naming a user who does not own this player reads as signed out. That is
// what stops the two representations of sign-in from ever disagreeing, and it
// fails closed rather than open.
func (s *Server) signedInUser(r *http.Request, playerID string) (*accounts.User, error) {
	userID, ok := s.sessionUserID(r)
	if !ok {
		return nil, nil
	}
	user, err := accounts.ForPlayer(r.Context(), s.Pool, playerID)
	if err != nil || user == nil {
		return nil, err
	}
	if user.ID != userID {
		return nil, nil
	}
	return user, nil
}

// viewerUserID is the user whose whole history the caller may see, or nil for a
// browser-only view.
//
// It returns nil rather than an error when accounts are switched off, so callers
// that merely widen a scope do not have to care whether sign-in exists.
func (s *Server) viewerUserID(r *http.Request, playerID string) (*int64, error) {
	if !s.accountsEnabled() {
		return nil, nil
	}
	user, err := s.signedInUser(r, playerID)
	if err != nil || user == nil {
		return nil, err
	}
	return &user.ID, nil
}

func (s *Server) authError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, accounts.ErrInvalidCode):
		s.fail(w, http.StatusBadRequest, "invalid_code", "A code is six digits.")
	case errors.Is(err, accounts.ErrTooManyCodeAttempts), errors.Is(err, accounts.ErrTooManyRequests):
		// Reachable only by someone who has already spent the budget, so it
		// discloses nothing new -- and the real player needs to be told why
		// retyping is not working.
		time.Sleep(failureDelay)
		s.fail(w, http.StatusTooManyRequests, "rate_limited", "Too many attempts. Ask for a new code in a few minutes.")
	case errors.Is(err, accounts.ErrCodeIncorrect), errors.Is(err, accounts.ErrCodeExpired):
		// Wrong, expired, already used, and never-requested collapse into one
		// answer. Distinguishing them would tell an attacker whether a code is
		// outstanding for an address they do not control. The message has to
		// mention expiry, because asking for a second code kills the first and
		// that is the most common innocent cause.
		time.Sleep(failureDelay)
		s.fail(w, http.StatusUnauthorized, "bad_code", "That code is wrong or has expired. Ask for a new one.")
	default:
		s.internal(w, err)
	}
}

// requestIP is the key for the per-IP request limit.
//
// X-Forwarded-For is only consulted when TrustProxyIP is set, because otherwise
// the header is caller-supplied and anyone could mint a fresh bucket per
// request. The last hop is the one the proxy appended, so it is the only entry
// that is not attacker-controlled.
func (s *Server) requestIP(r *http.Request) string {
	if s.TrustProxyIP {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			hops := strings.Split(forwarded, ",")
			if ip := strings.TrimSpace(hops[len(hops)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if net.ParseIP(host) == nil {
		return ""
	}
	// Behind an untrusted proxy every request shares one address, so a per-IP
	// limit would become a global one and lock the whole site out at twenty
	// codes an hour. Private addresses are therefore not rate-limited by IP; the
	// per-address limits are the ones that actually protect an account, and they
	// are unaffected.
	if !s.TrustProxyIP && isPrivateIP(host) {
		return ""
	}
	return host
}

func isPrivateIP(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}
