package relay_test

import (
	"context"
	"encoding/binary"
	"net"
	"net/smtp"
	"strings"
	"testing"

	"github.com/tokencanopy/e2a/internal/config"
	"github.com/tokencanopy/e2a/internal/identity"
	"github.com/tokencanopy/e2a/internal/relay"
	"github.com/tokencanopy/e2a/internal/testutil"
	"github.com/tokencanopy/e2a/internal/usage"
	"github.com/tokencanopy/e2a/internal/webhookpub"
	"github.com/tokencanopy/e2a/internal/ws"
)

// proxyV2HeaderE2E builds a v2 header: TCP over IPv4, src 203.0.113.7:4444 ->
// dst 192.0.2.1:25. Duplicated from the package-internal proxy_test.go builder —
// relay_test can't see it and it isn't worth exporting for tests alone.
func proxyV2HeaderE2E() []byte {
	h := []byte{0x0d, 0x0a, 0x0d, 0x0a, 0x00, 0x0d, 0x0a, 0x51, 0x55, 0x49, 0x54, 0x0a, 0x21, 0x11, 0x00, 0x0c}
	h = append(h, 203, 0, 113, 7, 192, 0, 2, 1)
	var p [2]byte
	binary.BigEndian.PutUint16(p[:], 4444)
	h = append(h, p[:]...)
	binary.BigEndian.PutUint16(p[:], 25)
	return append(h, p[:]...)
}

// sendSMTPOnConn runs a full SMTP transaction over an already-open connection
// (unlike sendSMTP, which dials — the PROXY header must precede the greeting).
func sendSMTPOnConn(t *testing.T, conn net.Conn, host, from, to, body string) {
	t.Helper()
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		t.Fatalf("smtp.NewClient: %v", err)
	}
	defer c.Close()
	if err := c.Mail(from); err != nil {
		t.Fatalf("MAIL FROM: %v", err)
	}
	if err := c.Rcpt(to); err != nil {
		t.Fatalf("RCPT TO: %v", err)
	}
	w, err := c.Data()
	if err != nil {
		t.Fatalf("DATA: %v", err)
	}
	if _, err := w.Write([]byte(body)); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close DATA: %v", err)
	}
	_ = c.Quit()
}

// TestE2E_ProxySourceReachesInboundIntake is the over-the-wire proof that the
// PROXY-parsed source IP becomes the durable record: with the listener trusting
// loopback, a forged v2 header source lands in inbound_intake.remote_ip, while a
// headerless direct connection from the same trusted loopback records the real
// 127.* peer (the HAProxy health-check / diagnostic path).
func TestE2E_ProxySourceReachesInboundIntake(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	const domain = "e2e-proxy.example.com"
	const agentEmail = "bot@" + domain
	user, err := store.CreateOrGetUser(ctx, "owner@"+domain, "O", "g-e2e-proxy")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := store.ClaimOrCreateDomain(ctx, domain, user.ID); err != nil {
		t.Fatalf("domain: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE domains SET verified=true WHERE domain=$1`, domain); err != nil {
		t.Fatalf("verify domain: %v", err)
	}
	if _, err := store.CreateAgent(ctx, agentEmail, domain, "", "", "", user.ID); err != nil {
		t.Fatalf("agent: %v", err)
	}

	port, err := freePort()
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			ListenAddr:        "127.0.0.1:" + port,
			Domain:            domain,
			ProxyTrustedCIDRs: []string{"127.0.0.0/8"},
		},
		Inbound: config.InboundConfig{Mode: "async"},
		Env:     "development",
	}
	server := relay.NewServer(cfg, store, usage.NewNoopUsageTracker(), ws.NewHub())
	server.SetOutbox(webhookpub.NewOutbox(pool, webhookpub.StaticFlag(true)))
	server.SetInboundEnqueuer(&fakeInboundEnq{})
	go func() { _ = server.ListenAndServe() }()
	t.Cleanup(func() { _ = server.Close() })
	waitForSMTP(t, cfg.SMTP.ListenAddr)

	readRemoteIP := func(t *testing.T, messageID string) string {
		t.Helper()
		var remoteIP string
		if err := pool.QueryRow(ctx,
			`SELECT remote_ip FROM inbound_intake WHERE recipient=$1 AND message_id=$2`,
			agentEmail, messageID).Scan(&remoteIP); err != nil {
			t.Fatalf("read intake remote_ip: %v", err)
		}
		return remoteIP
	}

	t.Run("forged source via PROXY v2 header", func(t *testing.T) {
		conn, err := net.Dial("tcp", cfg.SMTP.ListenAddr)
		if err != nil {
			t.Fatalf("net.Dial: %v", err)
		}
		if _, err := conn.Write(proxyV2HeaderE2E()); err != nil {
			t.Fatalf("write PROXY header: %v", err)
		}
		const msgID = "<proxy-v2-1@ext.test>"
		body := "From: sender@ext.test\r\nTo: " + agentEmail + "\r\nMessage-ID: " + msgID + "\r\nSubject: proxy\r\n\r\nhello via proxy"
		sendSMTPOnConn(t, conn, domain, "sender@ext.test", agentEmail, body)

		if got := readRemoteIP(t, msgID); got != "203.0.113.7" {
			t.Fatalf("inbound_intake.remote_ip = %q, want the PROXY source 203.0.113.7", got)
		}
	})

	t.Run("direct connection without header", func(t *testing.T) {
		const msgID = "<proxy-direct-1@ext.test>"
		body := "From: sender@ext.test\r\nTo: " + agentEmail + "\r\nMessage-ID: " + msgID + "\r\nSubject: direct\r\n\r\nhello direct"
		sendSMTP(t, cfg.SMTP.ListenAddr, "sender@ext.test", agentEmail, body)

		if got := readRemoteIP(t, msgID); !strings.HasPrefix(got, "127.") {
			t.Fatalf("inbound_intake.remote_ip = %q, want the real loopback peer (127.*)", got)
		}
	})
}
