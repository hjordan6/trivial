package accounts

import (
	"errors"
	"strings"
	"testing"
)

func TestParseCode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{"six digits", "048221", true},
		{"all zeros", "000000", true},
		{"all nines", "999999", true},
		{"five digits", "04822", false},
		{"seven digits", "0482210", false},
		{"empty", "", false},
		{"letters", "abcdef", false},
		{"mixed", "04822a", false},
		{"padded", " 048221 ", false},
		{"internal space", "048 221", false},
		{"signed", "+12345", false},
		{"hex", "0x1234", false},
		// strconv.Atoi and unicode-aware digit checks both accept these; a
		// byte-wise check does not, which is the point.
		{"arabic-indic digits", "٠١٢٣٤٥", false},
		{"fullwidth digits", "０４８２２１", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCode(tt.in)
			if !tt.ok {
				if !errors.Is(err, ErrInvalidCode) {
					t.Fatalf("parseCode(%q) = (%q, %v), want ErrInvalidCode", tt.in, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCode(%q) error = %v", tt.in, err)
			}
			if got != tt.in {
				t.Errorf("parseCode(%q) = %q, want it unchanged", tt.in, got)
			}
		})
	}
}

func TestNewCodeIsSixDigits(t *testing.T) {
	var sawLeadingZero bool
	for i := 0; i < 2000; i++ {
		code, err := newCode()
		if err != nil {
			t.Fatalf("newCode() error = %v", err)
		}
		if _, err := parseCode(code); err != nil {
			t.Fatalf("newCode() = %q, which parseCode rejects: %v", code, err)
		}
		if strings.HasPrefix(code, "0") {
			sawLeadingZero = true
		}
	}
	// %06d formatting, not decimal printing: without it one code in ten is five
	// digits long and the input is impossible to type into a six-box field.
	if !sawLeadingZero {
		t.Error("2000 draws produced no leading-zero code; formatting is losing them")
	}
}

// TestHashCodeIsKeyedAndBoundToTheAddress covers the two properties that make a
// six-digit code safe to store: the hash is useless without APP_SECRET, and a
// hash captured for one address cannot be replayed against another.
func TestHashCodeIsKeyedAndBoundToTheAddress(t *testing.T) {
	keyA, keyB := []byte("key A"), []byte("key B")
	base := hashCode(keyA, "player@example.com", "048221")

	if len(base) != 32 {
		t.Fatalf("hash length = %d, want 32", len(base))
	}
	same := hashCode(keyA, "player@example.com", "048221")
	if string(base) != string(same) {
		t.Error("hashCode is not deterministic")
	}
	for _, tc := range []struct {
		name string
		got  []byte
	}{
		{"different key", hashCode(keyB, "player@example.com", "048221")},
		{"different email", hashCode(keyA, "other@example.com", "048221")},
		{"different code", hashCode(keyA, "player@example.com", "048222")},
	} {
		if string(tc.got) == string(base) {
			t.Errorf("%s produced the same hash", tc.name)
		}
	}
}
