// Command trivial administers the daily trivia content library and puzzles.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hjordan6/trivial/internal/cli"
	"github.com/hjordan6/trivial/internal/clock"
	"github.com/hjordan6/trivial/internal/config"
	"github.com/hjordan6/trivial/internal/db"
	"github.com/hjordan6/trivial/internal/httpapi"
	"github.com/hjordan6/trivial/internal/tailnet"
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
		Pool:              pool,
		Clock:             clock.Real{},
		Timezone:          cfg.PuzzleTimezone,
		Logger:            slog.Default(),
		CookieSecure:      cfg.CookieSecure,
		DevelopmentMode:   cfg.DevelopmentMode,
		Assets:            webassets.Dist,
		AdminPassword:     cfg.AdminPassword,
		AdminAllowedNets:  cfg.AdminAllowedNets,
		AdminTailnet:      adminTailnet(cfg.AdminTailnetSocket),
		AdminTailnetUsers: cfg.AdminTailnetUsers,
		CooldownDays:      cfg.QuestionCooldownDays,
		TimeLimitSeconds:  cfg.TimeLimitSeconds,
	}).Handler()
	addresses := listenAddresses(cfg.HTTPAddress)
	if len(addresses) == 0 {
		return fmt.Errorf("HTTP_ADDRESS names no address to listen on")
	}

	// One handler, one listener per address. Serving loopback and a tailnet
	// address at once is the usual case: the panel stays reachable from the
	// machine itself while Tailscale devices reach it by their own address,
	// without exposing the port to every network the host is attached to.
	server := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	var listeners []net.Listener
	defer func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	}()
	failed := make(chan error, len(addresses))
	for _, address := range addresses {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return fmt.Errorf("listen on %s: %w", address, err)
		}
		listeners = append(listeners, listener)
		slog.Info("http server listening", "address", listener.Addr().String())
		go func(l net.Listener) { failed <- server.Serve(l) }(listener)
	}
	// Any listener dying takes the process with it: a half-served deployment
	// is harder to notice than one that stopped.
	return <-failed
}

// listenAddresses splits HTTP_ADDRESS on commas so one server can be reached
// on several addresses.
func listenAddresses(raw string) []string {
	var out []string
	for _, address := range strings.Split(raw, ",") {
		if address = strings.TrimSpace(address); address != "" {
			out = append(out, address)
		}
	}
	return out
}

// adminTailnet builds the identity checker, or nil when the feature is off.
func adminTailnet(socket string) *tailnet.Client {
	if socket == "" {
		return nil
	}
	return tailnet.New(socket)
}
