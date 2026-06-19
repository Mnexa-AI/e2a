package webhook

import (
	"fmt"
	"net"
	"syscall"
)

// IsDisallowedWebhookIP reports whether ip is in a range a webhook must never
// reach (loopback, RFC-1918 private, link-local incl. the cloud metadata
// endpoint 169.254.169.254, CGNAT shared space, multicast, unspecified, IPv6
// ULA). Shared by registration-time validation (agent.ValidateWebhookURL) and
// the delivery-time dial guard below so the two can never drift — closing the
// DNS-rebinding window where a host validates as public at registration then
// re-resolves to an internal address before delivery.
func IsDisallowedWebhookIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	// CGNAT shared address space (RFC 6598, 100.64.0.0/10) is not covered by
	// IsPrivate but is just as unroutable/internal.
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
		return true
	}
	return false
}

// guardedDialControl is a net.Dialer.Control hook that rejects a connection
// whose resolved address is a disallowed (private/loopback/link-local/…) IP.
// It runs AFTER DNS resolution with the concrete address the socket will
// connect to, so the IP that is validated is exactly the IP that is dialed —
// no resolve-then-re-resolve TOCTOU. This is the second line of defense the
// registration-time check cannot provide once a hostname's DNS can change.
func guardedDialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("webhook dial: bad address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("webhook dial: unparseable resolved address %q", host)
	}
	if IsDisallowedWebhookIP(ip) {
		return fmt.Errorf("webhook dial blocked: resolved to disallowed IP %s", ip)
	}
	return nil
}
