package selftest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"nhooyr.io/websocket"
)

// trashMessage soft-deletes one probe message, best-effort on a fresh
// context (the scenario's ctx may already be done when deferred cleanup
// runs). Trash removes the row from lists — including the WS connect-drain's
// unread query — and the janitor purges it after the retention window.
func (p *Probe) trashMessage(id string) {
	if id == "" {
		return
	}
	cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	u := p.HTTPBaseURL + "/v1/agents/" + url.PathEscape(p.AgentEmail) + "/messages/" + url.PathEscape(id)
	req, _ := http.NewRequestWithContext(cctx, http.MethodDelete, u, nil)
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	if resp, err := p.httpClient().Do(req); err == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
	}
}

// scenarioWebSocketRoundTrip exercises the /v1 WebSocket live-tail transport
// end to end: Bearer-authed handshake, then a loopback self-send carrying a
// unique nonce, then await the email.received push frame on the socket. The
// send is a loopback (no egress, no HITL, no metering), so the scenario is
// SmokeSafe. Connect-drain frames from earlier probe messages may arrive
// first — the read loop skips anything without the nonce.
func scenarioWebSocketRoundTrip(ctx context.Context, p *Probe) Result {
	nb := make([]byte, 8)
	if _, err := rand.Read(nb); err != nil {
		return fail("ws: nonce: %v", err)
	}
	nonce := "e2a-selftest-ws-" + hex.EncodeToString(nb)

	// Dial with a client WITHOUT http.Client.Timeout: nhooyr applies a client
	// timeout to the whole connection lifetime, not just the handshake, which
	// would kill the socket mid-await. The dial is bounded by dialCtx instead.
	dialCtx, cancelDial := context.WithTimeout(ctx, 10*time.Second)
	defer cancelDial()
	wsURL := p.HTTPBaseURL + "/v1/agents/" + url.PathEscape(p.AgentEmail) + "/ws"
	conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		HTTPClient: &http.Client{},
		HTTPHeader: http.Header{"Authorization": {"Bearer " + p.APIKey}},
	})
	if err != nil {
		return fail("ws dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "selftest done")
	conn.SetReadLimit(1 << 20)

	// Self-send the nonce AFTER the socket is registered, so the live push —
	// not the next connect's drain — is what must deliver it.
	su := p.HTTPBaseURL + "/v1/agents/" + url.PathEscape(p.AgentEmail) + "/messages"
	payload, _ := json.Marshal(map[string]any{
		"to":      []string{p.AgentEmail},
		"subject": nonce,
		"text":    "e2a selftest websocket round-trip",
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, su, bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return fail("ws self-send: %v", err)
	}
	sendRaw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fail("ws self-send: HTTP %d", resp.StatusCode)
	}
	var sent struct {
		MessageID string `json:"message_id"` // SendResultView: the e2a msg_ id (there is no "id" field)
	}
	_ = json.Unmarshal(sendRaw, &sent)
	// Best-effort residue cleanup (mirrors agent_lifecycle's deferred
	// delete): trash both copies of the probe message. Without this every
	// 30s run leaves one more unread inbound row, and each future connect
	// re-drains the oldest 100 of them forever — the drained-messages
	// metric and the drain query would be ~100% prober noise.
	defer p.trashMessage(sent.MessageID)

	// Await the frame carrying the nonce, skipping drain backlog. Bounded by
	// the shared round-trip timeout so a dead push path fails, never hangs.
	readCtx, cancelRead := context.WithTimeout(ctx, p.roundTripTimeout())
	defer cancelRead()
	start := time.Now()
	for {
		_, data, err := conn.Read(readCtx)
		if err != nil {
			return fail("ws read (no push frame with nonce after %s): %v",
				time.Since(start).Round(time.Millisecond), err)
		}
		if bytes.Contains(data, []byte(nonce)) {
			var env struct {
				Data struct {
					MessageID string `json:"message_id"`
				} `json:"data"`
			}
			_ = json.Unmarshal(data, &env)
			p.trashMessage(env.Data.MessageID) // the unread inbound copy — the one drain would replay
			return pass(fmt.Sprintf("ws connect + live push ok in %s",
				time.Since(start).Round(time.Millisecond)))
		}
	}
}
