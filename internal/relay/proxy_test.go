package relay

import (
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"
)

// proxyV2Header builds a v2 header: TCP over IPv4, src 203.0.113.7:4444 -> dst 192.0.2.1:25.
func proxyV2Header() []byte {
	h := []byte{0x0d, 0x0a, 0x0d, 0x0a, 0x00, 0x0d, 0x0a, 0x51, 0x55, 0x49, 0x54, 0x0a, 0x21, 0x11, 0x00, 0x0c}
	h = append(h, 203, 0, 113, 7, 192, 0, 2, 1)
	var p [2]byte
	binary.BigEndian.PutUint16(p[:], 4444)
	h = append(h, p[:]...)
	binary.BigEndian.PutUint16(p[:], 25)
	return append(h, p[:]...)
}

func acceptOne(t *testing.T, l net.Listener, dial func() net.Conn) net.Conn {
	t.Helper()
	done := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err == nil {
			done <- c
		}
	}()
	client := dial()
	defer client.Close()
	select {
	case c := <-done:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("Accept timed out")
		return nil
	}
}

func listen(t *testing.T, cidrs ...string) net.Listener {
	t.Helper()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	var prefixes []netip.Prefix
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			t.Fatal(err)
		}
		prefixes = append(prefixes, p)
	}
	return wrapProxyListener(raw, prefixes)
}

func TestProxyListenerTrustedHeaderHonored(t *testing.T) {
	l := listen(t, "127.0.0.0/8")
	c := acceptOne(t, l, func() net.Conn {
		d, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Write(proxyV2Header()); err != nil {
			t.Fatal(err)
		}
		return d
	})
	defer c.Close()
	addr, ok := c.RemoteAddr().(*net.TCPAddr)
	if !ok || !addr.IP.Equal(net.ParseIP("203.0.113.7")) || addr.Port != 4444 {
		t.Fatalf("RemoteAddr() = %v, want 203.0.113.7:4444", c.RemoteAddr())
	}
}

func TestProxyListenerTrustedAbsentHeaderAcceptedDirect(t *testing.T) {
	l := listen(t, "127.0.0.0/8")
	c := acceptOne(t, l, func() net.Conn {
		d, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		// No header: speak SMTP directly (health-check / diagnostic path).
		if _, err := d.Write([]byte("EHLO probe.example\r\n")); err != nil {
			t.Fatal(err)
		}
		return d
	})
	defer c.Close()
	addr, ok := c.RemoteAddr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() {
		t.Fatalf("RemoteAddr() = %v, want the real loopback peer", c.RemoteAddr())
	}
	// The SMTP bytes must arrive untouched (nothing was consumed as a header).
	buf := make([]byte, 20)
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	n, _ := c.Read(buf)
	if string(buf[:n]) != "EHLO probe.example\r\n" {
		t.Fatalf("Read() = %q, want the SMTP bytes verbatim", buf[:n])
	}
}

func TestProxyListenerUntrustedHeaderNeverParsed(t *testing.T) {
	// Trusted list is a DIFFERENT subnet; the loopback peer is untrusted.
	l := listen(t, "172.30.0.0/24")
	c := acceptOne(t, l, func() net.Conn {
		d, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Write(proxyV2Header()); err != nil {
			t.Fatal(err)
		}
		return d
	})
	defer c.Close()
	addr, ok := c.RemoteAddr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() {
		t.Fatalf("RemoteAddr() = %v, want the REAL loopback peer (no spoofing)", c.RemoteAddr())
	}
	// Header bytes were NOT consumed: they arrive at the app byte-for-byte as
	// garbage SMTP (the v2 signature prefix).
	buf := make([]byte, 4)
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	n, _ := c.Read(buf)
	if string(buf[:n]) != "\x0d\x0a\x0d\x0a" {
		t.Fatalf("Read() = %q, want the untouched header prefix \\r\\n\\r\\n", buf[:n])
	}
}

func TestProxyListenerTrustedV1HeaderHonored(t *testing.T) {
	// v1 is a separate parser path in go-proxyproto (text, not binary).
	l := listen(t, "127.0.0.0/8")
	c := acceptOne(t, l, func() net.Conn {
		d, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Write([]byte("PROXY TCP4 203.0.113.9 192.0.2.1 5555 25\r\n")); err != nil {
			t.Fatal(err)
		}
		return d
	})
	defer c.Close()
	addr, ok := c.RemoteAddr().(*net.TCPAddr)
	if !ok || !addr.IP.Equal(net.ParseIP("203.0.113.9")) || addr.Port != 5555 {
		t.Fatalf("RemoteAddr() = %v, want 203.0.113.9:5555", c.RemoteAddr())
	}
}

func TestProxyListenerTrustedMalformedHeaderDropsOnlyThatConnection(t *testing.T) {
	l := listen(t, "127.0.0.0/8")

	// v2 signature followed by an unsupported address-family byte: the header
	// parse fails, which must surface as a sticky per-connection read error
	// (fail closed) without touching the listener's accept loop.
	bad := []byte{0x0d, 0x0a, 0x0d, 0x0a, 0x00, 0x0d, 0x0a, 0x51, 0x55, 0x49, 0x54, 0x0a, 0x21, 0xff, 0x00, 0x00}
	c := acceptOne(t, l, func() net.Conn {
		d, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Write(bad); err != nil {
			t.Fatal(err)
		}
		return d
	})
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("Read() succeeded; a malformed header from a trusted peer must fail the connection")
	}
	c.Close()

	// The listener must still accept and serve the next, well-formed peer.
	c2 := acceptOne(t, l, func() net.Conn {
		d, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Write(proxyV2Header()); err != nil {
			t.Fatal(err)
		}
		return d
	})
	defer c2.Close()
	addr, ok := c2.RemoteAddr().(*net.TCPAddr)
	if !ok || !addr.IP.Equal(net.ParseIP("203.0.113.7")) {
		t.Fatalf("after malformed header, RemoteAddr() = %v, want the next connection served normally", c2.RemoteAddr())
	}
}

func TestProxyListenerTrustedIPv6(t *testing.T) {
	raw, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	t.Cleanup(func() { raw.Close() })
	p, err := netip.ParsePrefix("::1/128")
	if err != nil {
		t.Fatal(err)
	}
	l := wrapProxyListener(raw, []netip.Prefix{p})
	c := acceptOne(t, l, func() net.Conn {
		d, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Write([]byte("PROXY TCP6 2001:db8::7 ::1 4444 25\r\n")); err != nil {
			t.Fatal(err)
		}
		return d
	})
	defer c.Close()
	addr, ok := c.RemoteAddr().(*net.TCPAddr)
	if !ok || !addr.IP.Equal(net.ParseIP("2001:db8::7")) || addr.Port != 4444 {
		t.Fatalf("RemoteAddr() = %v, want [2001:db8::7]:4444", c.RemoteAddr())
	}
}

func TestParseTrustedCIDRsRejectsCatchAll(t *testing.T) {
	for _, cidr := range []string{"0.0.0.0/0", "::/0"} {
		if _, err := parseTrustedCIDRs([]string{cidr}); err == nil {
			t.Errorf("parseTrustedCIDRs(%q) succeeded; a catch-all trust list must be rejected", cidr)
		}
	}
}
