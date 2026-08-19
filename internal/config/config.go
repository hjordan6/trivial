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
	PuzzleTimezone       *time.Location
	QuestionCooldownDays int
	TimeLimitSeconds     int
}

// Load reads configuration from the environment, applying defaults and
// rejecting values that would fail later in less obvious ways.
func Load() (Config, error) {
	var cfg Config

	cfg.DatabaseURL = os.Getenv("DATABASE_URL")
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
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
	if cfg.TimeLimitSeconds, err = positiveInt("TIME_LIMIT_SECONDS", 135); err != nil {
		return Config{}, err
	}
	return cfg, nil
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
