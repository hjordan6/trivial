// Package friends turns a reusable invite link into a mutual friendship.
//
// A friendship is between two users -- people -- never between two players.
// A player is a browser, so a friendship keyed to one would evaporate the
// moment somebody played on a second device.
//
// Everything here takes a db.DBTX and neither reads the environment nor calls
// time.Now, so every function is testable inside a rolled-back transaction.
// That is the same shape internal/accounts has, and for the same reason.
package friends

import (
	"errors"
	"strings"
)

var (
	ErrNoInvite      = errors.New("no such invite")
	ErrSelfInvite    = errors.New("cannot befriend yourself")
	ErrEmptyNickname = errors.New("nickname is required")
)

// maxNicknameRunes matches the length CHECK on friend_invites.nickname and the
// constant of the same name in internal/httpapi. Counted in runes, never bytes:
// slicing a multi-byte name by bytes yields invalid UTF-8, which Postgres
// refuses on a text column.
const maxNicknameRunes = 40

// fallbackNickname is what an address yields when it has no usable local part.
// The landing page always has something to render, so it never has to fall back
// to showing an email address.
const fallbackNickname = "A player"

// Invite is a sender's reusable link. It deliberately carries no email: the
// token is a bearer credential, and anything reachable with it must not
// disclose the sender's address.
type Invite struct {
	Token    string
	UserID   int64
	Nickname string
}

// Outcome says what accepting an invite actually did.
type Outcome string

const (
	OutcomeAdded          Outcome = "added"
	OutcomeAlreadyFriends Outcome = "already_friends"
)

// DefaultNickname derives a display name from an address: the local part, so
// jordan@example.com becomes "jordan".
//
// It lives here rather than only in the frontend for two reasons: the server
// needs the same fallback when a client sends a blank nickname, and having one
// implementation means one place to test the rule.
func DefaultNickname(email string) string {
	local, _, found := strings.Cut(strings.TrimSpace(email), "@")
	local = strings.TrimSpace(local)
	if !found || local == "" {
		return fallbackNickname
	}
	return truncateRunes(local)
}

// truncateRunes bounds a name at maxNicknameRunes without splitting a
// character. The range loop yields byte offsets at rune boundaries, so slicing
// at one can never produce invalid UTF-8.
func truncateRunes(s string) string {
	count := 0
	for i := range s {
		if count == maxNicknameRunes {
			return s[:i]
		}
		count++
	}
	return s
}
