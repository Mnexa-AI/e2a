package testutil

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
)

// SMTPAddr holds parsed host and port for a fake SMTP server.
type SMTPAddr struct {
	Host string
	Port int
}

// SMTPMessage represents a message received by the fake SMTP server.
type SMTPMessage struct {
	From       string
	To         string   // first RCPT TO (backward compat)
	Recipients []string // all RCPT TO addresses
	Data       string
}

// FakeSMTPServer starts a minimal SMTP server that accepts messages.
// Returns the address and a function to call to stop the server and get received messages.
func FakeSMTPServer(t *testing.T) (SMTPAddr, func() []SMTPMessage) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("FakeSMTPServer: listen: %v", err)
	}

	addr := listener.Addr().(*net.TCPAddr)
	result := SMTPAddr{Host: addr.IP.String(), Port: addr.Port}

	var mu sync.Mutex
	var messages []SMTPMessage

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return // listener closed
			}
			go handleSMTPConn(conn, &mu, &messages)
		}
	}()

	t.Cleanup(func() { listener.Close() })

	return result, func() []SMTPMessage {
		listener.Close()
		mu.Lock()
		defer mu.Unlock()
		out := make([]SMTPMessage, len(messages))
		copy(out, messages)
		return out
	}
}

func handleSMTPConn(conn net.Conn, mu *sync.Mutex, messages *[]SMTPMessage) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	fmt.Fprintf(conn, "220 fakesmtp ready\r\n")

	var from string
	var recipients []string
	var dataLines []string
	inData := false

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")

		if inData {
			if line == "." {
				inData = false
				firstTo := ""
				if len(recipients) > 0 {
					firstTo = recipients[0]
				}
				mu.Lock()
				*messages = append(*messages, SMTPMessage{
					From:       from,
					To:         firstTo,
					Recipients: recipients,
					Data:       strings.Join(dataLines, "\n"),
				})
				mu.Unlock()
				// Return the id the way production SES SMTP does: BARE in the
				// 250 response — no angle brackets, no @domain (the real
				// Message-ID SES stamps on the wire is <id@<region>.amazonses.com>).
				// A bracketed/qualified fake here would mask the relay's
				// capture path (parse + domain qualification) from every test.
				b := make([]byte, 16)
				rand.Read(b)
				fmt.Fprintf(conn, "250 Ok %x-000000\r\n", b)
				continue
			}
			dataLines = append(dataLines, line)
			continue
		}

		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO"):
			fmt.Fprintf(conn, "250 Hello\r\n")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			from = extractAngle(line[10:])
			fmt.Fprintf(conn, "250 OK\r\n")
		case strings.HasPrefix(upper, "RCPT TO:"):
			recipients = append(recipients, extractAngle(line[8:]))
			fmt.Fprintf(conn, "250 OK\r\n")
		case upper == "DATA":
			dataLines = nil
			inData = true
			fmt.Fprintf(conn, "354 Go ahead\r\n")
		case upper == "QUIT":
			fmt.Fprintf(conn, "221 Bye\r\n")
			return
		default:
			fmt.Fprintf(conn, "250 OK\r\n")
		}
	}
}

func extractAngle(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "<"); i >= 0 {
		if j := strings.Index(s, ">"); j > i {
			return s[i+1 : j]
		}
	}
	return s
}
