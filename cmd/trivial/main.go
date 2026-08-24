// Command trivial administers the daily trivia content library and puzzles.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/hjordan6/trivial/internal/cli"
	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/config"
	"github.com/hjordan6/trivial/internal/db"
	"github.com/hjordan6/trivial/internal/httpapi"
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
		AdminAllowedNets: cfg.AdminAllowedNets,
		CooldownDays:     cfg.QuestionCooldownDays,
		TimeLimitSeconds: cfg.TimeLimitSeconds,
	}).Handler()
	server := &http.Server{Addr: cfg.HTTPAddress, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	slog.Info("http server listening", "address", cfg.HTTPAddress)
	return server.ListenAndServe()
}
