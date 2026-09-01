package accounts

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math/big"
)

// codeLength is the number of digits in a sign-in code. Six is ~20 bits, which
// is only safe because the attempt budget in login_tokens bounds guessing; see
// the migration comment. The lever for more margin is length, not more limits.
const codeLength = 6

// newCode draws a six-digit code.
//
// crypto/rand.Int is unbiased, unlike a modulo over a random integer, and the
// %06d formatting keeps the leading zeros that make 000123 a legal code -- both
// so the keyspace is really 10^6 and so every code is the same width to type.
func newCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("draw login code: %w", err)
	}
	return fmt.Sprintf("%0*d", codeLength, n), nil
}

// parseCode rejects anything that is not exactly six ASCII digits, before it
// reaches the database.
//
// It checks byte by byte rather than using strconv or a unicode-aware digit
// test, both of which accept forms like "٠١٢٣٤٥" and "+12345" that would never
// match a stored hash but would still cost a round trip and an attempt.
func parseCode(raw string) (string, error) {
	if len(raw) != codeLength {
		return "", fmt.Errorf("%w: want %d digits, got %d characters", ErrInvalidCode, codeLength, len(raw))
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return "", fmt.Errorf("%w: not a digit at position %d", ErrInvalidCode, i)
		}
	}
	return raw, nil
}

// hashCode is what gets stored.
//
// It is keyed on APP_SECRET, so the stored value is useless in a database dump
// -- a plain digest of six digits is a million hash evaluations from plaintext.
// It also covers the email, so a hash captured for one address cannot be
// replayed against another.
func hashCode(key []byte, email, code string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(email))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(code))
	return mac.Sum(nil)
}
