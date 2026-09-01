package accounts

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // empty means "expect ErrInvalidEmail"
	}{
		{"already normal", "player@example.com", "player@example.com"},
		{"uppercased", "Player@Example.COM", "player@example.com"},
		{"padded", "  player@example.com\t\n", "player@example.com"},
		{"internal whitespace is not padding", "pla yer@example.com", ""},
		// Plus-addressing must survive. Collapsing a+b@c to a@c would merge two
		// people who deliberately used different addresses into one account.
		{"plus addressing preserved", "player+trivia@example.com", "player+trivia@example.com"},
		{"dots preserved", "first.last@example.co.uk", "first.last@example.co.uk"},
		{"subdomain", "player@mail.example.com", "player@mail.example.com"},

		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"no at sign", "player.example.com", ""},
		{"two at signs", "player@one@example.com", ""},
		{"empty local part", "@example.com", ""},
		{"empty domain", "player@", ""},
		{"domain without a dot", "player@example", ""},
		{"domain starting with a dot", "player@.example.com", ""},
		{"domain ending with a dot", "player@example.com.", ""},
		{"too short", "a@b.c", ""},
		{"newline injection", "player@example.com\nBcc: victim@example.com", ""},
		{"carriage return injection", "player@example.com\rSubject: x", ""},
		{"comma separated", "a@example.com,b@example.com", ""},
		{"too long", strings.Repeat("a", 250) + "@example.com", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeEmail(tt.in)
			if tt.want == "" {
				if !errors.Is(err, ErrInvalidEmail) {
					t.Fatalf("NormalizeEmail(%q) = (%q, %v), want ErrInvalidEmail", tt.in, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeEmail(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeEmail(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestNormalizeEmailMatchesTheDatabaseConstraint guards the invariant the
// users_email_normalized CHECK relies on: whatever this returns must already be
// lowercased and trimmed, or inserts fail at the database instead of here.
func TestNormalizeEmailIsIdempotent(t *testing.T) {
	for _, in := range []string{"Player@Example.COM", " a+b@sub.example.co.uk "} {
		once, err := NormalizeEmail(in)
		if err != nil {
			t.Fatalf("NormalizeEmail(%q) error = %v", in, err)
		}
		twice, err := NormalizeEmail(once)
		if err != nil {
			t.Fatalf("NormalizeEmail(%q) error = %v", once, err)
		}
		if once != twice {
			t.Errorf("not idempotent: %q -> %q -> %q", in, once, twice)
		}
		if once != strings.ToLower(strings.TrimSpace(once)) {
			t.Errorf("%q is not lowercased and trimmed; the database CHECK will reject it", once)
		}
	}
}
