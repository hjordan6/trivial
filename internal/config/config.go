// Package config loads application settings from the environment.
package config

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hjordan6/trivial/internal/tailnet"
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
	// AdminAllowedNets is the set of client addresses allowed to reach the
	// admin surface at all, checked before the password. It defaults to
	// loopback, which is the machine the server runs on.
	AdminAllowedNets []netip.Prefix
	// AdminTailnetSocket is the tailscaled socket used to identify callers by
	// their Tailscale account. Empty disables identity checking, leaving
	// AdminAllowedNets as the only way in.
	AdminTailnetSocket string
	// AdminTailnetUsers restricts which Tailscale logins may reach the admin
	// surface. Empty means any peer the local tailscaled recognises, which
	// includes nodes other people have shared into this tailnet -- naming the
	// owner is what makes it "my account only".
	AdminTailnetUsers []string
}

// defaultAdminNets is the allowlist when ADMIN_ALLOWED_IPS is unset: the
// loopback ranges, meaning the admin panel is reachable only from the host
// running the server.
var defaultAdminNets = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("::1/128"),
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
	if cfg.TimeLimitSeconds, err = positiveInt("TIME_LIMIT_SECONDS", 135); err != nil {
		return Config{}, err
	}
	cfg.AdminPassword = os.Getenv("ADMIN_PASSWORD")
	if cfg.AdminAllowedNets, err = adminNets(os.Getenv("ADMIN_ALLOWED_IPS")); err != nil {
		return Config{}, err
	}

	tailnetAccess, err := boolValue("ADMIN_TAILNET_ACCESS", false)
	if err != nil {
		return Config{}, err
	}
	if tailnetAccess {
		cfg.AdminTailnetSocket = envOr("ADMIN_TAILSCALE_SOCKET", tailnet.DefaultSocket)
	}
	for _, login := range strings.Split(os.Getenv("ADMIN_TAILNET_USERS"), ",") {
		if login = strings.TrimSpace(login); login != "" {
			cfg.AdminTailnetUsers = append(cfg.AdminTailnetUsers, login)
		}
	}
	// Naming users without turning the check on would read as a restriction
	// while actually being ignored, so it is an error rather than a no-op.
	if len(cfg.AdminTailnetUsers) > 0 && cfg.AdminTailnetSocket == "" {
		return Config{}, fmt.Errorf("ADMIN_TAILNET_USERS is set but ADMIN_TAILNET_ACCESS is not enabled")
	}
	return cfg, nil
}

// adminNets parses the admin allowlist. Entries are addresses or CIDR blocks;
// a bare address is treated as a single-host block. An empty value keeps the
// loopback default rather than meaning "allow everything", because a typo that
// silently opened the admin panel to the network would be the worst possible
// way to get this wrong.
func adminNets(raw string) ([]netip.Prefix, error) {
	if strings.TrimSpace(raw) == "" {
		return defaultAdminNets, nil
	}
	var nets []netip.Prefix
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			nets = append(nets, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("ADMIN_ALLOWED_IPS %q is not an IP address or CIDR block", entry)
		}
		nets = append(nets, netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()))
	}
	if len(nets) == 0 {
		return defaultAdminNets, nil
	}
	return nets, nil
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
