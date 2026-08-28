// Package accounts turns an email address into a durable identity for a player.
//
// A player is a browser; a user is a person. Signing in attaches the current
// player row to a user, so one user accumulates player rows over time and
// nothing is ever merged or deleted. That is what makes signing in on a second
// device the same operation as the first.
//
// Everything here takes a db.DBTX and neither reads the environment nor calls
// time.Now, so every function is testable inside a rolled-back transaction.
package accounts

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidEmail        = errors.New("invalid email address")
	ErrInvalidCode         = errors.New("invalid code format")
	ErrCodeIncorrect       = errors.New("code incorrect")
	ErrCodeExpired         = errors.New("code expired")
	ErrTooManyCodeAttempts = errors.New("too many code attempts")
	ErrTooManyRequests     = errors.New("too many code requests")
)

// ErrTooManyRequestsFromIP is the per-IP variant of ErrTooManyRequests, and it
// matches errors.Is against it.
//
// It is separate because it is the only request limit an HTTP layer may
// disclose: it describes the caller. Saying "too many requests" for a per-address
// limit would be an oracle, revealing that somebody has been asking for codes
// for a stranger's address.
var ErrTooManyRequestsFromIP = fmt.Errorf("%w: from this ip", ErrTooManyRequests)

// User is a person, identified only by an address they proved they can read.
type User struct {
	ID        int64     `json:"-"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"-"`
}

// Config carries every knob the package needs, so nothing here reads the
// environment. A zero value is usable: withDefaults fills everything but Key.
type Config struct {
	// Key keys the code HMAC. Derived from APP_SECRET by the caller.
	Key []byte
	// CodeTTL is how long a code stays valid.
	CodeTTL time.Duration
	// MaxAttempts is the guess budget for a single code. Because only the
	// newest unconsumed code for an address is ever checkable, this is the
	// entire brute-force defence.
	MaxAttempts int
	// MaxPerEmailFast and FastWindow stop a burst of requests to one address.
	MaxPerEmailFast int
	FastWindow      time.Duration
	// MaxPerEmailDay bounds a slow, sustained attack on one address.
	MaxPerEmailDay int
	// MaxFailsPerHour stops a targeted address being ground down: once this
	// many guesses have failed within the hour, requesting stops too, so the
	// attacker cannot keep drawing fresh budgets.
	MaxFailsPerHour int
	// MaxPerIP bounds how broad an attack one caller can run.
	MaxPerIP int
	// Retention is how long spent rows are kept. It must exceed the longest
	// rate window, or sweeping would erase a limit.
	Retention time.Duration
}

func (c Config) withDefaults() Config {
	if c.CodeTTL <= 0 {
		c.CodeTTL = 15 * time.Minute
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 5
	}
	if c.MaxPerEmailFast <= 0 {
		c.MaxPerEmailFast = 3
	}
	if c.FastWindow <= 0 {
		c.FastWindow = 15 * time.Minute
	}
	if c.MaxPerEmailDay <= 0 {
		c.MaxPerEmailDay = 10
	}
	if c.MaxFailsPerHour <= 0 {
		c.MaxFailsPerHour = 15
	}
	if c.MaxPerIP <= 0 {
		c.MaxPerIP = 20
	}
	if c.Retention <= 0 {
		c.Retention = 7 * 24 * time.Hour
	}
	return c
}
