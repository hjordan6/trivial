// Package tailnet identifies the Tailscale peer behind a connection by asking
// the local tailscaled daemon.
//
// It speaks tailscaled's local HTTP API over its unix socket directly rather
// than importing tailscale.com's client library, which would pull a very large
// module into a project whose only other dependencies are a database driver
// and a migration tool. The one endpoint used here is a stable, documented GET.
package tailnet

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// DefaultSocket is where tailscaled listens on Linux.
const DefaultSocket = "/var/run/tailscale/tailscaled.sock"

// localHost is the Host header tailscaled expects on its local API. The value
// is a fixed name rather than a real one; the unix socket is the address.
const localHost = "local-tailscaled.sock"

// Identity is who tailscaled says a peer is.
type Identity struct {
	// Node is the machine's full tailnet name, e.g.
	// "laptop.tailnet-name.ts.net.".
	Node string
	// LoginName is the tailnet user the node belongs to, e.g. an email
	// address. This is the field that answers "is this one of my devices".
	LoginName string
}

// Client asks a local tailscaled who a peer is.
type Client struct {
	http *http.Client
}

// New returns a Client talking to the tailscaled socket at path.
//
// The timeout is deliberately short. This runs in the path of every admin
// request, and a wedged daemon should deny access promptly rather than hold
// requests open: failing to answer is treated as "not identified", which is
// closed rather than open.
func New(path string) *Client {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	return &Client{
		http: &http.Client{
			Timeout: 3 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return dialer.DialContext(ctx, "unix", path)
				},
			},
		},
	}
}

// ErrNoMatch means tailscaled does not recognise the address as a peer on this
// tailnet. It is the ordinary answer for anything arriving from outside the
// tailnet, not a malfunction.
var ErrNoMatch = fmt.Errorf("no tailnet peer for that address")

// WhoIs identifies the peer behind addr, which must carry a port because that
// is what tailscaled matches on.
func (c *Client) WhoIs(ctx context.Context, addr netip.AddrPort) (Identity, error) {
	url := "http://" + localHost + "/localapi/v0/whois?addr=" + addr.String()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Identity{}, err
	}
	req.Host = localHost

	res, err := c.http.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("ask tailscaled who %s is: %w", addr, err)
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode == http.StatusNotFound {
		return Identity{}, ErrNoMatch
	}
	if res.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("tailscaled whois for %s: unexpected status %d", addr, res.StatusCode)
	}

	var body struct {
		Node struct {
			Name string `json:"Name"`
		} `json:"Node"`
		UserProfile struct {
			LoginName string `json:"LoginName"`
		} `json:"UserProfile"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return Identity{}, fmt.Errorf("decode tailscaled whois for %s: %w", addr, err)
	}
	// A 200 with no login is not an identity. Treating it as one would let a
	// malformed answer stand in for a real device.
	if body.UserProfile.LoginName == "" {
		return Identity{}, ErrNoMatch
	}
	return Identity{Node: body.Node.Name, LoginName: body.UserProfile.LoginName}, nil
}
