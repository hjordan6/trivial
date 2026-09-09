// Package config loads application settings from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// minAppSecretLength is the shortest APP_SECRET Load will accept. 32 characters
// is enough that a secret cannot be brute-forced, and short enough that a
// hand-written development value is not annoying.
const minAppSecretLength = 32

// Config holds every environment-driven setting the application needs.
type Config struct {
	DatabaseURL          string
	HTTPAddress          string
	CookieSecure         bool
	DevelopmentMode      bool
	PuzzleTimezone       *time.Location
	QuestionCooldownDays int
	// AnswerCooldownDays bars two questions with the same answer from landing
	// within this many days of each other, even when their prompts, topics,
	// and difficulties differ.
	AnswerCooldownDays int
	TimeLimitSeconds   int
	// AdminPassword gates the admin panel. Empty is legal and means the panel
	// and its API do not exist: every admin route answers 404.
	AdminPassword string
	// AppSecret keys every signed cookie the application issues. It is
	// required: there is no safe default for a signing key, and an empty one
	// would silently sign everything with the hash of the empty string.
	AppSecret string
	// ResendAPIKey enables real email. Empty is legal: in development the
	// sign-in code goes to the log instead, and in production sign-in reports
	// itself unavailable rather than half-working.
	ResendAPIKey string
	// MailFrom is the sender address, e.g. "Trivial <login@example.com>".
	// Required whenever ResendAPIKey is set, because Resend refuses a domain it
	// has not verified.
	MailFrom string
	// LoginCodeTTL is how long a sign-in code stays valid.
	LoginCodeTTL time.Duration
	// TrustProxyIP makes the per-IP rate limiter read the last hop of
	// X-Forwarded-For instead of RemoteAddr. Only turn it on behind a proxy you
	// control: the header is caller-supplied otherwise, which would let anyone
	// mint a fresh rate-limit bucket per request.
	TrustProxyIP bool
}

// Load reads configuration from the environment, applying defaults and
// rejecting values that would fail later in less obvious ways.
func Load() (Config, error) {
	var cfg Config
	var err error

	cfg.DatabaseURL = os.Getenv("DATABASE_URL")
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	cfg.HTTPAddress = envOr("HTTP_ADDRESS", ":8080")
	if cfg.CookieSecure, err = boolValue("COOKIE_SECURE", true); err != nil {
		return Config{}, err
	}
	if cfg.DevelopmentMode, err = boolValue("DEVELOPMENT_MODE", false); err != nil {
		return Config{}, err
	}

	tzName := envOr("PUZZLE_TIMEZONE", "America/Denver")
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return Config{}, fmt.Errorf("PUZZLE_TIMEZONE %q is not a known timezone: %w", tzName, err)
	}
	cfg.PuzzleTimezone = loc

	if cfg.QuestionCooldownDays, err = positiveInt("QUESTION_COOLDOWN_DAYS", 180); err != nil {
		return Config{}, err
	}
	if cfg.AnswerCooldownDays, err = positiveInt("ANSWER_COOLDOWN_DAYS", 14); err != nil {
		return Config{}, err
	}
	if cfg.TimeLimitSeconds, err = positiveInt("TIME_LIMIT_SECONDS", 240); err != nil {
		return Config{}, err
	}
	cfg.AdminPassword = os.Getenv("ADMIN_PASSWORD")

	cfg.AppSecret = os.Getenv("APP_SECRET")

	cfg.ResendAPIKey = os.Getenv("RESEND_API_KEY")
	cfg.MailFrom = os.Getenv("MAIL_FROM")
	if cfg.ResendAPIKey != "" && cfg.MailFrom == "" {
		return Config{}, fmt.Errorf("MAIL_FROM is required when RESEND_API_KEY is set")
	}

	ttlMinutes, err := positiveInt("LOGIN_CODE_TTL_MINUTES", 15)
	if err != nil {
		return Config{}, err
	}
	cfg.LoginCodeTTL = time.Duration(ttlMinutes) * time.Minute

	if cfg.TrustProxyIP, err = boolValue("TRUST_PROXY_IP", false); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ValidateForServe rejects a configuration that cannot run an HTTP server.
//
// It is separate from Load because the signing key is only meaningful to the
// server: `trivial migrate` and `trivial seed` issue no cookies, and requiring
// a secret from them would be asking an operator for something they do not use.
func (c Config) ValidateForServe() error {
	if c.AppSecret == "" {
		return fmt.Errorf("APP_SECRET is required to serve; generate one with `openssl rand -base64 48`")
	}
	// A short secret is the failure mode worth catching: it looks configured,
	// so nothing else complains, but it is guessable.
	if len(c.AppSecret) < minAppSecretLength {
		return fmt.Errorf("APP_SECRET must be at least %d characters, got %d", minAppSecretLength, len(c.AppSecret))
	}
	return nil
}

func boolValue(key string, fallback bool) (bool, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s %q is not a boolean: %w", key, raw, err)
	}
	return v, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func positiveInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a number: %w", key, raw, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s must be positive, got %d", key, n)
	}
	return n, nil
}
