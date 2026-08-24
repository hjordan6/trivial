package httpapi

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/hjordan6/trivial/internal/tailnet"
)

// tailscaleRanges are the address ranges Tailscale assigns to tailnet nodes:
// the CGNAT block for IPv4 and Tailscale's ULA prefix for IPv6. They are used
// only to decide whether asking tailscaled about an address is worth a round
// trip -- membership in the range proves nothing on its own, since the range
// is shared by every tailnet in the world. The identity check is what decides.
var tailscaleRanges = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fd7a:115c:a1e0::/48"),
}

// adminReachable reports whether a request's client address is allowed to see
// the admin surface at all. It is checked before the password, and before the
// panel's own HTML, so a caller from anywhere else cannot tell the surface
// exists: everything answers 404, exactly as it does when no ADMIN_PASSWORD is
// configured.
//
// The decision uses r.RemoteAddr and nothing else. X-Forwarded-For and its
// relatives are attacker-controlled on a direct connection, so honouring them
// would hand the allowlist to whoever wanted past it. The cost of that choice
// is that a reverse proxy in front of this server makes every request look
// like it came from the proxy: a deployment behind one has to allow the
// proxy's address and do its own filtering, or keep the admin surface on a
// direct port.
//
// An empty allowlist allows nothing. The zero-valued Server therefore has no
// reachable admin surface, which is the safe way for a misconfiguration to
// fail.
func (s *Server) adminReachable(r *http.Request) bool {
	addr, ok := clientAddr(r.RemoteAddr)
	if !ok {
		return false
	}
	for _, allowed := range s.AdminAllowedNets {
		if allowed.Contains(addr.Addr()) {
			return true
		}
	}
	return s.tailnetPeerAllowed(r, addr)
}

// tailnetPeerAllowed asks the local tailscaled who is behind an address and
// whether they are allowed here.
//
// This is an identity check rather than an address check: tailscaled answers
// only for peers of this tailnet, and it answers with the account that owns
// the device. Being inside Tailscale's address range proves nothing by itself,
// because every tailnet draws from the same range.
//
// Any failure to identify is a refusal. A daemon that is down or slow denies
// access rather than falling back to trusting the address, which is the safe
// direction for this to break.
func (s *Server) tailnetPeerAllowed(r *http.Request, addr netip.AddrPort) bool {
	if s.AdminTailnet == nil {
		return false
	}
	inRange := false
	for _, prefix := range tailscaleRanges {
		if prefix.Contains(addr.Addr()) {
			inRange = true
			break
		}
	}
	if !inRange {
		return false
	}

	identity, err := s.AdminTailnet.WhoIs(r.Context(), addr)
	if err != nil {
		// ErrNoMatch is the ordinary answer for a non-peer and not worth
		// logging; anything else means the check could not be made at all.
		if !errors.Is(err, tailnet.ErrNoMatch) {
			s.logger().Warn("tailnet identity check failed", "addr", addr.Addr().String(), "error", err)
		}
		return false
	}
	if len(s.AdminTailnetUsers) == 0 {
		return true
	}
	for _, allowed := range s.AdminTailnetUsers {
		if strings.EqualFold(allowed, identity.LoginName) {
			return true
		}
	}
	s.logger().Info("admin request from an unlisted tailnet account",
		"login", identity.LoginName, "node", identity.Node)
	return false
}

// clientAddr pulls the address and port out of a RemoteAddr. The port is kept
// because tailscaled identifies a peer by address and port together.
//
// IPv4-mapped IPv6 addresses are unmapped so that ::ffff:127.0.0.1 matches a
// 127.0.0.0/8 rule, which is how loopback arrives on a dual-stack listener.
func clientAddr(remote string) (netip.AddrPort, bool) {
	host, port, err := net.SplitHostPort(remote)
	if err != nil {
		// RemoteAddr is documented as host:port, but a bare address is worth
		// tolerating rather than failing open or closed on a formatting quirk.
		// Port 0 simply means tailscaled will not match it.
		host, port = remote, "0"
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.AddrPort{}, false
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		number = 0
	}
	return netip.AddrPortFrom(addr.Unmap(), uint16(number)), true
}
