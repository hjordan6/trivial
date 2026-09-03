package httpapi

import (
	"errors"
	"net/http"

	"github.com/hjordan6/trivial/internal/accounts"
	"github.com/hjordan6/trivial/internal/friends"
)

// inviteResponse is what the sender gets back. url is relative, exactly like
// share's "/c/<token>": the frontend makes it absolute from location.origin, so
// no BASE_URL setting has to exist or be kept correct across environments.
type inviteResponse struct {
	Token    string `json:"token"`
	URL      string `json:"url"`
	Nickname string `json:"nickname"`
}

// publicInviteResponse is what anyone holding the link gets. A nickname and
// nothing else -- see readInvite.
type publicInviteResponse struct {
	Nickname string `json:"nickname"`
}

type acceptResponse struct {
	Nickname string `json:"nickname"`
	Status   string `json:"status"`
}

// friendsAvailable gates all three routes on the same condition sign-in uses,
// because a friendship needs two accounts and there are none without a mailer.
func (s *Server) friendsAvailable(w http.ResponseWriter) bool {
	if s.accountsEnabled() {
		return true
	}
	s.fail(w, http.StatusServiceUnavailable, "accounts_unavailable", "Friends are not available on this server.")
	return false
}

// requireSignedIn resolves the caller, or answers 401 and reports false.
//
// It uses optionalPlayer rather than requirePlayer: these routes must not mint
// a player row for a crawler that wanders onto an invite link. A recipient who
// signs in on the invite page has already been given one by createSession.
func (s *Server) requireSignedIn(w http.ResponseWriter, r *http.Request) (*accounts.User, bool) {
	playerID := s.optionalPlayer(r)
	if playerID != "" {
		user, err := s.signedInUser(r, playerID)
		if err != nil {
			s.internal(w, err)
			return nil, false
		}
		if user != nil {
			return user, true
		}
	}
	s.fail(w, http.StatusUnauthorized, "not_signed_in", "Sign in first.")
	return nil, false
}

// mintInvite returns the caller's reusable friend link, creating it on first use.
//
// The link is stable by design: a second press renames the sender but returns
// the same token, so a link already sent to somebody never goes dead. That does
// make it a bearer credential with no expiry -- anyone holding it can befriend
// the sender -- which is why the token lives in a column that a rotation
// feature could later overwrite in place.
func (s *Server) mintInvite(w http.ResponseWriter, r *http.Request) {
	if !s.friendsAvailable(w) {
		return
	}
	user, ok := s.requireSignedIn(w, r)
	if !ok {
		return
	}
	var body struct {
		Nickname *string `json:"nickname"`
	}
	if err := decodeOptional(r, &body); err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	// A blank name falls back rather than failing. The button's job is to
	// produce a link, and DefaultNickname always yields something printable, so
	// the landing page never has to fall back to showing an address.
	nickname := friends.DefaultNickname(user.Email)
	if cleaned := cleanNickname(body.Nickname); cleaned != nil {
		nickname = *cleaned
	}

	token, err := randomToken()
	if err != nil {
		s.internal(w, err)
		return
	}
	inv, err := friends.UpsertInvite(r.Context(), s.Pool, user.ID, nickname, token, s.now())
	if err != nil {
		s.internal(w, err)
		return
	}
	s.write(w, http.StatusOK, inviteResponse{Token: inv.Token, URL: "/f/" + inv.Token, Nickname: inv.Nickname})
}

// readInvite names the sender for whoever opened the link.
//
// This is the only public friend route, and it exists so the landing page can
// say who is asking before the visitor has signed in. It returns the nickname
// and nothing else -- never the email, the user id, or a count. The token will
// end up forwarded into group chats, and a route that echoed the address would
// turn every such link into a way to read it off the server.
func (s *Server) readInvite(w http.ResponseWriter, r *http.Request) {
	if !s.friendsAvailable(w) {
		return
	}
	inv, err := friends.InviteByToken(r.Context(), s.Pool, r.PathValue("token"))
	if errors.Is(err, friends.ErrNoInvite) {
		s.fail(w, http.StatusNotFound, "no_such_invite", "This link doesn’t work any more.")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	s.write(w, http.StatusOK, publicInviteResponse{Nickname: inv.Nickname})
}
