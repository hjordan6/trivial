package config

import (
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y")
	t.Setenv("PUZZLE_TIMEZONE", "")
	t.Setenv("QUESTION_COOLDOWN_DAYS", "")
	t.Setenv("TIME_LIMIT_SECONDS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.PuzzleTimezone.String(); got != "America/Denver" {
		t.Errorf("PuzzleTimezone = %q, want America/Denver", got)
	}
	if cfg.QuestionCooldownDays != 180 {
		t.Errorf("QuestionCooldownDays = %d, want 180", cfg.QuestionCooldownDays)
	}
	if cfg.TimeLimitSeconds != 240 {
		t.Errorf("TimeLimitSeconds = %d, want 240", cfg.TimeLimitSeconds)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{
			name:    "missing database url",
			env:     map[string]string{"DATABASE_URL": ""},
			wantErr: "DATABASE_URL",
		},
		{
			name:    "unknown timezone",
			env:     map[string]string{"DATABASE_URL": "postgres://x/y", "PUZZLE_TIMEZONE": "Mars/Olympus"},
			wantErr: "Mars/Olympus",
		},
		{
			name:    "non numeric cooldown",
			env:     map[string]string{"DATABASE_URL": "postgres://x/y", "QUESTION_COOLDOWN_DAYS": "soon"},
			wantErr: "QUESTION_COOLDOWN_DAYS",
		},
		{
			name:    "zero cooldown",
			env:     map[string]string{"DATABASE_URL": "postgres://x/y", "QUESTION_COOLDOWN_DAYS": "0"},
			wantErr: "must be positive",
		},
		{
			name:    "negative time limit",
			env:     map[string]string{"DATABASE_URL": "postgres://x/y", "TIME_LIMIT_SECONDS": "-5"},
			wantErr: "must be positive",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"DATABASE_URL", "PUZZLE_TIMEZONE", "QUESTION_COOLDOWN_DAYS", "TIME_LIMIT_SECONDS"} {
				t.Setenv(k, tt.env[k])
			}
			_, err := Load()
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Load() error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
