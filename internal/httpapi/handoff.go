package httpapi

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// This file moves players from an old hostname to a new one without losing
// anyone.
//
// Cookies belong to the host that set them, so a plain redirect would greet
// every returning player on the new host as a stranger. Players who added an
// email can sign back in; an anonymous player's history would be unreachable
// for good. So instead of redirecting at the edge, the old host stays pointed
// at this server, which reads the cookies it still can, packs them into a
// short-lived signed token, and sends the browser to the new host's
// handoffPath, which sets the same cookies there.
//
// Both hosts are this one process with one APP_SECRET, which is what lets the
// new host trust what the old host signed. Once returning players have come
// through -- a few months -- the old host can be redirected at the edge and
// MOVED_FROM_HOSTS emptied.

// handoffPath is where the new host accepts a handoff token.
const handoffPath = "/handoff"

// handoffCookie is the MAC domain for handoff tokens. It is never set as a
// cookie; it only keeps a token from being replayed as any real cookie, and a
// real cookie from being replayed as a token.
const handoffCookie = "trivial_handoff"

// handoffTTL is how long a token is good for. It only has to survive one
// redirect, and it is a bearer credential for a player while it lives.
const handoffTTL = 2 * time.Minute

// withMovedHosts wraps the router so page loads on a host in MovedFrom go to
// CanonicalHost. Without both settings it is the router unchanged.
func (s *Server) withMovedHosts(next http.Handler) http.Handler {
	if s.CanonicalHost == "" || len(s.MovedFrom) == 0 {
		return next
	}
	moved := make(map[string]bool, len(s.MovedFrom))
	for _, h := range s.MovedFrom {
		moved[normalizeHost(h)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if moved[normalizeHost(r.Host)] && isPageLoad(r) {
			s.redirectToCanonical(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isPageLoad reports whether a request on the old host should move.
//
// API calls and built assets stay where they are: a tab already open on the
// old host has to be able to finish today's game, and neither a fetch nor a
// module script can follow a redirect to another origin.
func isPageLoad(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p := r.URL.Path
	return p != "/healthz" && !strings.HasPrefix(p, "/api/") && !strings.HasPrefix(p, "/assets/")
}

func (s *Server) redirectToCanonical(w http.ResponseWriter, r *http.Request) {
	// Never cached: the answer depends on this browser's cookies.
	w.Header().Set("Cache-Control", "no-store")
	target := url.URL{Scheme: "https", Host: s.CanonicalHost, Path: r.URL.Path, RawQuery: r.URL.RawQuery}

	if playerID := s.optionalPlayer(r); looksLikeUUID(playerID) {
		user := ""
		if id, ok := s.sessionUserID(r); ok {
			user = strconv.FormatInt(id, 10)
		}
		// The destination travels inside the signed payload rather than as its
		// own parameter, so the handoff endpoint cannot be used as an open
		// redirect. It goes last because it is the only part that may contain
		// the separator.
		token := signExpiring(s.appKey(), handoffCookie,
			playerID+"|"+user+"|"+target.RequestURI(), s.now().Add(handoffTTL))
		target = url.URL{Scheme: "https", Host: s.CanonicalHost, Path: handoffPath,
			RawQuery: url.Values{"t": {token}}.Encode()}
	}
	http.Redirect(w, r, target.String(), http.StatusFound)
}

// claimHandoff sets the cookies a handoff token carries and sends the browser
// on to where it was going. Any bad token lands on the home page with nothing
// set: the visitor simply arrives as a new player, which is the worst a failed
// handoff should ever cost.
func (s *Server) claimHandoff(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	playerID, userID, dest, ok := s.readHandoff(r.URL.Query().Get("t"))
	if !ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	// A browser that already has a player here may have played here, and
	// replacing it would strand that run. The session is only meaningful with
	// the player it was issued alongside, so it is skipped with it.
	if s.optionalPlayer(r) == "" {
		s.setPlayerCookie(w, playerID)
		if userID != 0 {
			s.setSessionCookie(w, userID, s.now())
		}
		s.logger().Info("handoff claimed", "player", playerID, "signed_in", userID != 0)
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

func (s *Server) readHandoff(token string) (playerID string, userID int64, dest string, ok bool) {
	payload, ok := unsignExpiring(s.appKey(), handoffCookie, token, s.now())
	if !ok {
		return "", 0, "", false
	}
	parts := strings.SplitN(payload, "|", 3)
	if len(parts) != 3 || !looksLikeUUID(parts[0]) || !isLocalPath(parts[2]) {
		return "", 0, "", false
	}
	if parts[1] != "" {
		id, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return "", 0, "", false
		}
		userID = id
	}
	return parts[0], userID, parts[2], true
}

// isLocalPath accepts a path on this host and nothing a browser would read as
// another one: "//host" and "/\host" are both protocol-relative to browsers.
func isLocalPath(p string) bool {
	return strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") && !strings.HasPrefix(p, `/\`)
}

// normalizeHost lowercases a Host header and drops any port.
func normalizeHost(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.ToLower(strings.TrimSuffix(h, "."))
}
