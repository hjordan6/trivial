package friends_test

import (
	"testing"

	"github.com/hjordan6/trivial/internal/friends"
)

func TestDefaultNickname(t *testing.T) {
	tests := []struct {
		name  string
		email string
		want  string
	}{
		{"local part", "jordan@example.com", "jordan"},
		{"dots are kept", "ana.maria@example.com", "ana.maria"},
		{"plus tag is kept", "jordan+trivia@example.com", "jordan+trivia"},
		{"no at sign falls back", "nonsense", "A player"},
		{"empty local part falls back", "@example.com", "A player"},
		{"blank falls back", "   ", "A player"},
		{"whitespace-only local part falls back", "  @example.com", "A player"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := friends.DefaultNickname(tt.email); got != tt.want {
				t.Errorf("DefaultNickname(%q) = %q, want %q", tt.email, got, tt.want)
			}
		})
	}
}

// A long local part must come back inside the length CHECK on
// friend_invites.nickname, and still be valid UTF-8.
func TestDefaultNicknameIsBounded(t *testing.T) {
	long := ""
	for i := 0; i < 60; i++ {
		long += "é"
	}
	got := friends.DefaultNickname(long + "@example.com")
	if n := len([]rune(got)); n > 40 {
		t.Errorf("rune count = %d, want at most 40", n)
	}
}
