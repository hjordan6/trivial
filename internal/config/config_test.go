package config

import (
	"strings"
	"testing"
	"time"
)

// testSecret is a stand-in for APP_SECRET, which Load requires. It only has to
// clear the minimum length.
const testSecret = "0123456789abcdef0123456789abcdef"

// managedEnv is every variable Load reads. Each test clears all of them before
// setting the ones it cares about, so a variable left over from the developer's
// shell cannot change a result.
var managedEnv = []string{
	"DATABASE_URL",
	"APP_SECRET",
	"PUZZLE_TIMEZONE",
	"QUESTION_COOLDOWN_DAYS",
	"TIME_LIMIT_SECONDS",
	"LOGIN_CODE_TTL_MINUTES",
	"RESEND_API_KEY",
	"MAIL_FROM",
	"TRUST_PROXY_IP",
}

func TestLoadDefaults(t *testing.T) {
	for _, k := range managedEnv {
		t.Setenv(k, "")
	}
	t.Setenv("DATABASE_URL", "postgres://x/y")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Load does not require APP_SECRET: the CLI subcommands issue no cookies.
	if cfg.AppSecret != "" {
		t.Errorf("AppSecret = %q, want empty", cfg.AppSecret)
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
	if cfg.LoginCodeTTL != 15*time.Minute {
		t.Errorf("LoginCodeTTL = %v, want 15m", cfg.LoginCodeTTL)
	}
	if cfg.TrustProxyIP {
		t.Error("TrustProxyIP = true, want false: trusting a caller-supplied header must be opt-in")
	}
}

// TestLoadAcceptsAConfiguredMailer covers the one cross-field rule: MAIL_FROM
// is only required once real mail is switched on.
func TestLoadAcceptsAConfiguredMailer(t *testing.T) {
	for _, k := range managedEnv {
		t.Setenv(k, "")
	}
	t.Setenv("DATABASE_URL", "postgres://x/y")
	t.Setenv("APP_SECRET", testSecret)
	t.Setenv("RESEND_API_KEY", "re_test")
	t.Setenv("MAIL_FROM", "Trivial <login@example.test>")
	t.Setenv("LOGIN_CODE_TTL_MINUTES", "5")
	t.Setenv("TRUST_PROXY_IP", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ResendAPIKey != "re_test" {
		t.Errorf("ResendAPIKey = %q, want re_test", cfg.ResendAPIKey)
	}
	if cfg.MailFrom != "Trivial <login@example.test>" {
		t.Errorf("MailFrom = %q", cfg.MailFrom)
	}
	if cfg.LoginCodeTTL != 5*time.Minute {
		t.Errorf("LoginCodeTTL = %v, want 5m", cfg.LoginCodeTTL)
	}
	if !cfg.TrustProxyIP {
		t.Error("TrustProxyIP = false, want true")
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
			env:     map[string]string{"DATABASE_URL": "postgres://x/y", "APP_SECRET": testSecret, "PUZZLE_TIMEZONE": "Mars/Olympus"},
			wantErr: "Mars/Olympus",
		},
		{
			name:    "non numeric cooldown",
			env:     map[string]string{"DATABASE_URL": "postgres://x/y", "APP_SECRET": testSecret, "QUESTION_COOLDOWN_DAYS": "soon"},
			wantErr: "QUESTION_COOLDOWN_DAYS",
		},
		{
			name:    "zero cooldown",
			env:     map[string]string{"DATABASE_URL": "postgres://x/y", "APP_SECRET": testSecret, "QUESTION_COOLDOWN_DAYS": "0"},
			wantErr: "must be positive",
		},
		{
			name:    "negative time limit",
			env:     map[string]string{"DATABASE_URL": "postgres://x/y", "APP_SECRET": testSecret, "TIME_LIMIT_SECONDS": "-5"},
			wantErr: "must be positive",
		},
		{
			name:    "resend key without mail from",
			env:     map[string]string{"DATABASE_URL": "postgres://x/y", "APP_SECRET": testSecret, "RESEND_API_KEY": "re_test"},
			wantErr: "MAIL_FROM is required",
		},
		{
			name:    "zero login code ttl",
			env:     map[string]string{"DATABASE_URL": "postgres://x/y", "APP_SECRET": testSecret, "LOGIN_CODE_TTL_MINUTES": "0"},
			wantErr: "must be positive",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range managedEnv {
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

// TestValidateForServe covers the split: a signing key is required to serve but
// not to migrate or seed.
func TestValidateForServe(t *testing.T) {
	tests := []struct {
		name    string
		secret  string
		wantErr string
	}{
		{"missing", "", "APP_SECRET is required to serve"},
		{"too short", "too-short", "at least 32"},
		{"one short of the minimum", strings.Repeat("x", minAppSecretLength-1), "at least 32"},
		{"exactly the minimum", strings.Repeat("x", minAppSecretLength), ""},
		{"comfortably long", testSecret + testSecret, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Config{AppSecret: tt.secret}.ValidateForServe()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateForServe() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("ValidateForServe() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ValidateForServe() error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
