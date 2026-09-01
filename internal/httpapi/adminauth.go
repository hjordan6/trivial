package httpapi

import (
	"crypto/subtle"
	"net/http"
	"time"
)

const (
	adminCookie     = "trivial_admin"
	adminSessionTTL = 12 * time.Hour
)

// adminKey derives the signing key from the password itself, so there is no
// second secret to manage and changing the password invalidates every session
// that was already handed out. Deliberately not APP_SECRET: keying on the
// password is what makes rotating it a way to sign every operator out.
func (s *Server) adminKey() []byte {
	return signingKey(s.AdminPassword)
}

// signAdminSession returns a cookie value carrying its own expiry, signed so a
// client cannot extend it. The payload is empty because the expiry is the whole
// claim: there is only ever one admin.
func (s *Server) signAdminSession(expires time.Time) string {
	return signExpiring(s.adminKey(), adminCookie, "", expires)
}

// validAdminSession reports whether a cookie value is intact and unexpired.
func (s *Server) validAdminSession(value string, now time.Time) bool {
	_, ok := unsignExpiring(s.adminKey(), adminCookie, value, now)
	return ok
}

// requireAdmin gates every admin route.
//
// Every failure - panel disabled, no cookie, forged cookie, expired cookie -
// answers 404 rather than 401 or 403, so the admin surface is indistinguishable
// from absent to anyone without the password. This follows resetCurrentRun,
// which hides itself the same way when DEVELOPMENT_MODE is off.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.AdminPassword == "" {
		s.fail(w, http.StatusNotFound, "not_found", "Not found.")
		return false
	}
	c, err := r.Cookie(adminCookie)
	if err != nil || !s.validAdminSession(c.Value, s.now()) {
		s.fail(w, http.StatusNotFound, "not_found", "Not found.")
		return false
	}
	return true
}

func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) {
	if s.AdminPassword == "" {
		s.fail(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := decodeOptional(r, &body); err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.Password), []byte(s.AdminPassword)) != 1 {
		// A fixed delay is the whole rate-limiting story. For a single-operator
		// tool behind TLS with a strong password that is proportionate; it is
		// not a defence against a determined attacker with time.
		time.Sleep(250 * time.Millisecond)
		s.fail(w, http.StatusUnauthorized, "bad_password", "That password is not right.")
		return
	}
	expires := s.now().Add(adminSessionTTL)
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookie,
		Value:    s.signAdminSession(expires),
		Path:     "/",
		HttpOnly: true,
		Secure:   s.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(adminSessionTTL / time.Second),
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminLogout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

// adminSession is the probe the panel uses to decide between showing the login
// form and showing itself. The admin cookie is HttpOnly, so the browser cannot
// answer that question on its own.
func (s *Server) adminSession(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
