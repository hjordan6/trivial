// Command trivial administers the daily trivia content library and puzzles.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/hjordan6/trivial/internal/accounts"
	"github.com/hjordan6/trivial/internal/cli"
	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/config"
	"github.com/hjordan6/trivial/internal/db"
	"github.com/hjordan6/trivial/internal/httpapi"
	"github.com/hjordan6/trivial/internal/mail"
	webassets "github.com/hjordan6/trivial/web"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		if err := serve(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func serve(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.ValidateForServe(); err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool, db.Up); err != nil {
		return err
	}
	h := (&httpapi.Server{
		Pool:             pool,
		Clock:            clock.Real{},
		Timezone:         cfg.PuzzleTimezone,
		Logger:           slog.Default(),
		CookieSecure:     cfg.CookieSecure,
		DevelopmentMode:  cfg.DevelopmentMode,
		Assets:           webassets.Dist,
		AdminPassword:    cfg.AdminPassword,
		CooldownDays:     cfg.QuestionCooldownDays,
		TimeLimitSeconds: cfg.TimeLimitSeconds,
		AppSecret:        cfg.AppSecret,
		Mailer:           loginMailer(cfg),
		Accounts:         accounts.Config{CodeTTL: cfg.LoginCodeTTL},
		TrustProxyIP:     cfg.TrustProxyIP,
	}).Handler()
	server := &http.Server{Addr: cfg.HTTPAddress, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	slog.Info("http server listening", "address", cfg.HTTPAddress)
	return server.ListenAndServe()
}

// loginMailer picks how sign-in codes get delivered, or nil for not at all.
//
// A nil sender makes the whole /api/auth surface report itself unavailable, and
// the sign-in prompt disappears from the UI. That is deliberately the production
// default when no key is configured: a sign-in form that silently swallows codes
// is worse than one that says it is switched off.
func loginMailer(cfg config.Config) mail.Sender {
	switch {
	case cfg.ResendAPIKey != "":
		return mail.Resend{APIKey: cfg.ResendAPIKey, From: cfg.MailFrom}
	case cfg.DevelopmentMode:
		// Log-only, so the entire sign-in flow works locally with no provider
		// and no outbound mail. The code lands in the server log.
		slog.Warn("no RESEND_API_KEY: sign-in codes will be written to this log, not emailed")
		return mail.Logger{Log: slog.Default()}
	default:
		slog.Warn("no RESEND_API_KEY: sign-in is disabled")
		return nil
	}
}
