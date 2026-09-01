package httpapi

import (
	"strings"
	"testing"
	"time"
)

func TestSignRoundTrips(t *testing.T) {
	key := signingKey("a secret long enough to be realistic")
	payloads := []struct {
		name    string
		payload string
	}{
		{"empty", ""},
		{"uuid", "6b7a1c4e-0d2f-4a58-9b31-8f0c5e2d7a44"},
		{"contains the expiry separator", "a|b|c"},
		{"contains the mac separator", "a.b.c"},
		{"non ascii", "café ☕"},
		{"long", strings.Repeat("x", 4096)},
	}
	for _, tc := range payloads {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := unsign(key, playerCookie, sign(key, playerCookie, tc.payload))
			if !ok {
				t.Fatal("unsign() ok = false, want true")
			}
			if got != tc.payload {
				t.Errorf("unsign() = %q, want %q", got, tc.payload)
			}
		})
	}
}

func TestUnsignRejects(t *testing.T) {
	key := signingKey("a secret long enough to be realistic")
	valid := sign(key, playerCookie, "player-id")

	tests := []struct {
		name  string
		key   []byte
		value string
	}{
		{"empty", key, ""},
		{"no separator", key, "justonepart"},
		{"missing mac", key, strings.SplitN(valid, ".", 2)[0]},
		{"tampered payload", key, "AAAA" + valid[4:]},
		{"tampered mac", key, valid[:len(valid)-3] + "AAA"},
		{"truncated mac", key, valid[:len(valid)-3]},
		{"wrong key", signingKey("a different secret, equally long here"), valid},
		{"payload not base64", key, "not!base64." + strings.SplitN(valid, ".", 2)[1]},
		{"mac not base64", key, strings.SplitN(valid, ".", 2)[0] + ".not!base64"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := unsign(tc.key, playerCookie, tc.value); ok {
				t.Errorf("unsign() = %q, true; want ok = false", got)
			}
		})
	}
}

// TestUnsignIsBoundToTheCookieName is the assertion that makes swapping one
// cookie's value into another cookie useless, even though both are signed with
// the same key.
func TestUnsignIsBoundToTheCookieName(t *testing.T) {
	key := signingKey("a secret long enough to be realistic")
	value := sign(key, playerCookie, "player-id")

	if _, ok := unsign(key, sessionCookie, value); ok {
		t.Error("a value signed for the player cookie verified as the session cookie")
	}
	if _, ok := unsign(key, playerCookie, value); !ok {
		t.Error("a value signed for the player cookie failed under its own name")
	}
}

func TestSignExpiring(t *testing.T) {
	key := signingKey("a secret long enough to be realistic")
	now := time.Unix(1_800_000_000, 0)
	expires := now.Add(time.Hour)
	value := signExpiring(key, sessionCookie, "42", expires)

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"well before expiry", now, true},
		// The boundary matches validAdminSession's existing now.Before(...)
		// semantics: the last valid instant is one tick before expiry.
		{"one second before expiry", expires.Add(-time.Second), true},
		{"exactly at expiry", expires, false},
		{"after expiry", expires.Add(time.Second), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := unsignExpiring(key, sessionCookie, value, tc.now)
			if ok != tc.want {
				t.Fatalf("unsignExpiring() ok = %v, want %v", ok, tc.want)
			}
			if ok && got != "42" {
				t.Errorf("unsignExpiring() = %q, want 42", got)
			}
		})
	}
}

// TestUnsignExpiringRejectsAMalformedPayload covers the case where the
// signature is genuinely ours but the payload is not the shape we write, which
// is only reachable if a caller mixes sign and signExpiring for one cookie.
func TestUnsignExpiringRejectsAMalformedPayload(t *testing.T) {
	key := signingKey("a secret long enough to be realistic")
	now := time.Unix(1_800_000_000, 0)

	tests := []string{
		sign(key, sessionCookie, "42"),            // no expiry at all
		sign(key, sessionCookie, "notanumber|42"), // expiry is not an integer
		sign(key, sessionCookie, "|42"),           // empty expiry
	}
	for _, value := range tests {
		if _, ok := unsignExpiring(key, sessionCookie, value, now); ok {
			t.Errorf("unsignExpiring(%q) ok = true, want false", value)
		}
	}
}

func TestLooksLikeUUID(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"6b7a1c4e-0d2f-4a58-9b31-8f0c5e2d7a44", true},
		{"6B7A1C4E-0D2F-4A58-9B31-8F0C5E2D7A44", true},
		{"", false},
		{"6b7a1c4e0d2f4a589b318f0c5e2d7a44", false},                  // no hyphens
		{"6b7a1c4e-0d2f-4a58-9b31-8f0c5e2d7a4", false},               // too short
		{"6b7a1c4e-0d2f-4a58-9b31-8f0c5e2d7a444", false},             // too long
		{"6b7a1c4e-0d2f-4a58-9b31-8f0c5e2d7a4g", false},              // non-hex
		{"6b7a1c4e_0d2f_4a58_9b31_8f0c5e2d7a44", false},              // wrong separator
		{"'; DROP TABLE players; --                        ", false}, // right length, not a uuid
	}
	for _, tc := range tests {
		if got := looksLikeUUID(tc.in); got != tc.want {
			t.Errorf("looksLikeUUID(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
