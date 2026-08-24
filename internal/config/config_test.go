package config

import (
	"net/netip"
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
	if cfg.TimeLimitSeconds != 135 {
		t.Errorf("TimeLimitSeconds = %d, want 135", cfg.TimeLimitSeconds)
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

func TestAdminAllowedIPs(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x/y")

	t.Run("defaults to loopback", func(t *testing.T) {
		t.Setenv("ADMIN_ALLOWED_IPS", "")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if len(cfg.AdminAllowedNets) != 2 {
			t.Fatalf("AdminAllowedNets = %v, want the two loopback ranges", cfg.AdminAllowedNets)
		}
		for _, remote := range []string{"127.0.0.1", "127.0.0.9", "::1"} {
			if !contains(cfg.AdminAllowedNets, remote) {
				t.Errorf("%s is not allowed by default", remote)
			}
		}
		if contains(cfg.AdminAllowedNets, "10.0.0.4") {
			t.Error("a private address is allowed by default")
		}
	})

	t.Run("accepts addresses and CIDR blocks", func(t *testing.T) {
		t.Setenv("ADMIN_ALLOWED_IPS", "100.80.189.68, 10.0.0.0/24")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		for _, remote := range []string{"100.80.189.68", "10.0.0.7"} {
			if !contains(cfg.AdminAllowedNets, remote) {
				t.Errorf("%s should be allowed", remote)
			}
		}
		// A bare address is one host, not its whole block, and setting the
		// list at all replaces the loopback default rather than adding to it.
		for _, remote := range []string{"100.80.189.69", "10.0.1.7", "127.0.0.1"} {
			if contains(cfg.AdminAllowedNets, remote) {
				t.Errorf("%s should not be allowed", remote)
			}
		}
	})

	t.Run("rejects nonsense rather than silently allowing it", func(t *testing.T) {
		t.Setenv("ADMIN_ALLOWED_IPS", "not-an-address")
		if _, err := Load(); err == nil {
			t.Fatal("Load() accepted an unparseable allowlist")
		}
	})
}

func contains(nets []netip.Prefix, remote string) bool {
	addr, err := netip.ParseAddr(remote)
	if err != nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(addr.Unmap()) {
			return true
		}
	}
	return false
}
