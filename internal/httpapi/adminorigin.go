package httpapi

import (
	"net"
	"net/http"
	"net/netip"
)

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
		if allowed.Contains(addr) {
			return true
		}
	}
	return false
}

// clientAddr pulls the bare IP out of a RemoteAddr. IPv4-mapped IPv6 addresses
// are unmapped so that ::ffff:127.0.0.1 matches a 127.0.0.0/8 rule, which is
// how loopback arrives on a dual-stack listener.
func clientAddr(remote string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		// RemoteAddr is documented as host:port, but a bare address is worth
		// tolerating rather than failing open or closed on a formatting quirk.
		host = remote
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}
