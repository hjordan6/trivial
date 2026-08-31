// Package config loads application settings from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds every environment-driven setting the application needs.
type Config struct {
	DatabaseURL          string
	HTTPAddress          string
	CookieSecure         bool
	DevelopmentMode      bool
	PuzzleTimezone       *time.Location
	QuestionCooldownDays int
	TimeLimitSeconds     int
	// AdminPassword gates the admin panel. Empty is legal and means the panel
	// and its API do not exist: every admin route answers 404.
	AdminPassword string
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
	if cfg.TimeLimitSeconds, err = positiveInt("TIME_LIMIT_SECONDS", 240); err != nil {
		return Config{}, err
	}
	cfg.AdminPassword = os.Getenv("ADMIN_PASSWORD")
	return cfg, nil
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
