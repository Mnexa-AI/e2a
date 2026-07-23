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
	// Header bytes were NOT consumed: they arrive at the app as garbage SMTP.
	buf := make([]byte, 4)
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	n, _ := c.Read(buf)
	if n == 0 {
		t.Fatal("connection was dropped; IGNORE policy must pass bytes through")
	}
}
