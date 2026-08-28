package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

// This file owns cookie signing for all three cookies the application issues:
// the admin session, the player identity, and the account session. They are
// package-level functions rather than methods on Server because they are pure
// and worth testing without one.

// sessionCookie carries the signed-in user. It is separate from playerCookie
// because the two answer different questions: playerCookie says which browser
// this is, sessionCookie says which person is currently using it.
const sessionCookie = "trivial_session"

// sessionTTL is 400 days because that is the longest lifetime Chrome will
// honour for a cookie. The session is also re-issued on every successful read,
// so an active player's sign-in effectively never lapses.
const sessionTTL = 400 * 24 * time.Hour

// signingKey derives a fixed-length HMAC key from a secret, so a short or
// oddly-shaped secret still yields a full-strength key.
func signingKey(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// sign returns "<base64url payload>.<base64url mac>".
//
// The cookie name is mixed into the MAC, so a value minted for one cookie
// cannot be replayed in another even though both are signed with the same key.
// base64url's alphabet excludes '.', which is what makes the split unambiguous.
func sign(key []byte, name, payload string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(mac(key, name, payload))
}

// unsign returns the payload when the signature is intact, and nothing else.
func unsign(key []byte, name, value string) (string, bool) {
	rawPayload, rawMAC, found := strings.Cut(value, ".")
	if !found {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(rawPayload)
	if err != nil {
		return "", false
	}
	got, err := base64.RawURLEncoding.DecodeString(rawMAC)
	if err != nil {
		return "", false
	}
	if !hmac.Equal(got, mac(key, name, string(payload))) {
		return "", false
	}
	return string(payload), true
}

// signExpiring embeds an expiry the client cannot extend, by covering it with
// the same MAC as the payload. The layout is "<unix expiry>|<payload>".
func signExpiring(key []byte, name, payload string, expires time.Time) string {
	return sign(key, name, strconv.FormatInt(expires.Unix(), 10)+"|"+payload)
}

// unsignExpiring verifies the signature and then the expiry.
func unsignExpiring(key []byte, name, value string, now time.Time) (string, bool) {
	signed, ok := unsign(key, name, value)
	if !ok {
		return "", false
	}
	rawExpiry, payload, found := strings.Cut(signed, "|")
	if !found {
		return "", false
	}
	unix, err := strconv.ParseInt(rawExpiry, 10, 64)
	if err != nil {
		return "", false
	}
	if !now.Before(time.Unix(unix, 0)) {
		return "", false
	}
	return payload, true
}

func mac(key []byte, name, payload string) []byte {
	h := hmac.New(sha256.New, key)
	// The name is length-delimited rather than merely concatenated, so no pair
	// of (name, payload) values can produce the same MAC input as another.
	_, _ = h.Write([]byte(strconv.Itoa(len(name))))
	_, _ = h.Write([]byte{':'})
	_, _ = h.Write([]byte(name))
	_, _ = h.Write([]byte(payload))
	return h.Sum(nil)
}

// looksLikeUUID reports whether s has the shape of a canonical UUID.
//
// It exists so the legacy unsigned-player-cookie path can reject junk without
// adding a uuid dependency for one shape check. It deliberately validates shape
// only; whether the id names a real player is settled by the database.
func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch i {
		case 8, 13, 18, 23:
			if s[i] != '-' {
				return false
			}
		default:
			c := s[i]
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}
