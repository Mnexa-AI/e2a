package relay

import (
	"fmt"
	"net"
	"net/netip"
	"time"

	proxyproto "github.com/pires/go-proxyproto"
)

// proxyHeaderTimeout bounds how long a trusted peer may take to present (or
// omit) the PROXY header before the connection is dropped. go-smtp's
// ReadTimeout only applies after the session starts, so an unwrapped
// slow-loris on the header would otherwise be unbounded. Variable only so
// tests can shorten the headerless-connection wait.
var proxyHeaderTimeout = 5 * time.Second

// parseTrustedCIDRs compiles configured CIDR strings. config.Validate already
// rejects malformed entries at startup; this is the relay's own fail-closed
// guard in case it is ever wired differently.
func parseTrustedCIDRs(cidrs []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, fmt.Errorf("smtp proxy_trusted_cidrs: malformed CIDR %q: %w", c, err)
		}
		if p.Bits() == 0 {
			return nil, fmt.Errorf("smtp proxy_trusted_cidrs: %q trusts every peer, which lets anyone spoof source IPs; list only the proxy's own address(es)", c)
		}
		out = append(out, p)
	}
	return out, nil
}

// wrapProxyListener lets connections from trusted peers present a PROXY
// protocol v1/v2 header; the parsed source replaces the connection's
// RemoteAddr for SPF, logging, and the durable inbound record. Trusted peers
// without a header are accepted as direct connections (HAProxy health checks
// and diagnostics do not send one). Headers from untrusted peers are never
// parsed: SKIP makes Accept return the raw connection, so the bytes surface
// as garbage SMTP and the real peer IP is used, making source-IP spoofing
// impossible from outside the trusted set. (IGNORE would still parse and
// consume the header bytes before discarding them — only SKIP on a Listener
// passes the stream through untouched.)
func wrapProxyListener(l net.Listener, trusted []netip.Prefix) net.Listener {
	return &proxyproto.Listener{
		Listener:          l,
		ReadHeaderTimeout: proxyHeaderTimeout,
		ConnPolicy: func(o proxyproto.ConnPolicyOptions) (proxyproto.Policy, error) {
			if addr, ok := o.Upstream.(*net.TCPAddr); ok {
				if ip, ok := netip.AddrFromSlice(addr.IP); ok {
					ip = ip.Unmap()
					for _, p := range trusted {
						if p.Contains(ip) {
							return proxyproto.USE, nil
						}
					}
				}
			}
			return proxyproto.SKIP, nil
		},
	}
}
