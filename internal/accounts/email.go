package accounts

import (
	"fmt"
	"strings"
)

// Email length bounds. The lower bound matches the users_email_length CHECK;
// 254 is the longest address SMTP will carry.
const (
	minEmailLength = 6
	maxEmailLength = 254
)

// NormalizeEmail lowercases, trims, and validates an address.
//
// It is the only place an address is canonicalised, which is what lets the
// users_email_normalized CHECK be a safety net rather than a second, divergent
// copy of these rules. Anything this returns is already lowercased and trimmed.
//
// The validation is deliberately shape-only. Deciding whether an address can
// actually receive mail is the provider's job, and guessing at it here would
// reject valid exotic addresses for no gain.
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))

	if len(email) < minEmailLength || len(email) > maxEmailLength {
		return "", fmt.Errorf("%w: length %d is outside %d-%d", ErrInvalidEmail, len(email), minEmailLength, maxEmailLength)
	}
	// Any remaining whitespace or control character is either a typo or a
	// header-injection attempt; a newline in a To: line is how a mail body gets
	// extra recipients.
	if strings.ContainsFunc(email, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return "", fmt.Errorf("%w: contains whitespace or control characters", ErrInvalidEmail)
	}
	if strings.ContainsAny(email, ",;") {
		return "", fmt.Errorf("%w: contains a separator, so it may be a list", ErrInvalidEmail)
	}

	local, domain, found := strings.Cut(email, "@")
	if !found {
		return "", fmt.Errorf("%w: no @", ErrInvalidEmail)
	}
	if local == "" {
		return "", fmt.Errorf("%w: empty local part", ErrInvalidEmail)
	}
	if strings.Contains(domain, "@") {
		return "", fmt.Errorf("%w: more than one @", ErrInvalidEmail)
	}
	// A dot with something either side of it is the whole domain rule: it
	// separates a bare hostname, which cannot receive internet mail, from a
	// real domain, without pretending to know the public suffix list.
	dot := strings.LastIndex(domain, ".")
	if dot <= 0 || dot == len(domain)-1 {
		return "", fmt.Errorf("%w: domain %q is not a dotted name", ErrInvalidEmail, domain)
	}
	if strings.HasPrefix(domain, ".") || strings.Contains(domain, "..") {
		return "", fmt.Errorf("%w: domain %q is malformed", ErrInvalidEmail, domain)
	}
	return email, nil
}
