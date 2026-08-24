package tailnet_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/hjordan6/trivial/internal/tailnet"
)

// fakeDaemon serves the one endpoint the client uses over a unix socket, the
// way tailscaled does.
func fakeDaemon(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	// Sockets live under a short path: a unix socket address has a low length
	// limit, and t.TempDir() under some CI roots is long enough to trip it.
	path := filepath.Join(t.TempDir(), "d.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on %s: %v", path, err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return path
}

func addrPort(t *testing.T, s string) netip.AddrPort {
	t.Helper()
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		t.Fatalf("ParseAddrPort(%s): %v", s, err)
	}
	return ap
}

func TestWhoIsReadsTheNodeAndLogin(t *testing.T) {
	var gotQuery, gotHost string
	path := fakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("addr")
		gotHost = r.Host
		_, _ = w.Write([]byte(`{
			"Node": {"Name": "laptop.tailnet.ts.net.", "ID": 1},
			"UserProfile": {"ID": 2, "LoginName": "someone@example.com", "DisplayName": "Someone"}
		}`))
	})

	got, err := tailnet.New(path).WhoIs(context.Background(), addrPort(t, "100.80.189.68:5000"))
	if err != nil {
		t.Fatalf("WhoIs() error = %v", err)
	}
	if got.LoginName != "someone@example.com" {
		t.Errorf("LoginName = %q", got.LoginName)
	}
	if got.Node != "laptop.tailnet.ts.net." {
		t.Errorf("Node = %q", got.Node)
	}
	// The port matters: tailscaled matches on address and port together.
	if gotQuery != "100.80.189.68:5000" {
		t.Errorf("addr query = %q, want the full address and port", gotQuery)
	}
	if gotHost != "local-tailscaled.sock" {
		t.Errorf("Host = %q, want the local API's fixed host", gotHost)
	}
}

// 404 is tailscaled's ordinary answer for an address outside the tailnet, so
// it has to be distinguishable from a malfunction.
func TestWhoIsReportsNoMatchForAStranger(t *testing.T) {
	path := fakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no match for IP:port", http.StatusNotFound)
	})

	_, err := tailnet.New(path).WhoIs(context.Background(), addrPort(t, "203.0.113.7:5000"))
	if !errors.Is(err, tailnet.ErrNoMatch) {
		t.Fatalf("error = %v, want ErrNoMatch", err)
	}
}

// A 200 carrying no login is not an identity; treating it as one would let a
// malformed answer stand in for a real device.
func TestWhoIsRejectsAnAnswerWithNoLogin(t *testing.T) {
	path := fakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Node": {"Name": "x."}, "UserProfile": {}}`))
	})

	_, err := tailnet.New(path).WhoIs(context.Background(), addrPort(t, "100.80.189.68:5000"))
	if !errors.Is(err, tailnet.ErrNoMatch) {
		t.Fatalf("error = %v, want ErrNoMatch", err)
	}
}

func TestWhoIsFailsWhenTheDaemonIsUnreachable(t *testing.T) {
	_, err := tailnet.New(filepath.Join(t.TempDir(), "absent.sock")).
		WhoIs(context.Background(), addrPort(t, "100.80.189.68:5000"))
	if err == nil {
		t.Fatal("WhoIs() succeeded with no daemon listening")
	}
	// Not ErrNoMatch: an unreachable daemon is a failure to identify, which
	// callers must not read as "definitely not a peer" and cache.
	if errors.Is(err, tailnet.ErrNoMatch) {
		t.Error("an unreachable daemon reported ErrNoMatch")
	}
}

func TestWhoIsRejectsAnUnexpectedStatus(t *testing.T) {
	path := fakeDaemon(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	_, err := tailnet.New(path).WhoIs(context.Background(), addrPort(t, "100.80.189.68:5000"))
	if err == nil || errors.Is(err, tailnet.ErrNoMatch) {
		t.Fatalf("error = %v, want a plain failure", err)
	}
}
