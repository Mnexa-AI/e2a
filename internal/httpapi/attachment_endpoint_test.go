package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/ratelimit"
)

// attMultipart: text body + base64 PDF (index 0) + named inline png (index 1).
func attMultipart() []byte {
	pdf := base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 hello"))
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG bytes"))
	return []byte("From: a@x.com\r\nTo: support@acme.com\r\nSubject: hi\r\n" +
		"Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
		"--B\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename=\"report.pdf\"\r\nContent-Transfer-Encoding: base64\r\n\r\n" + pdf + "\r\n" +
		"--B\r\nContent-Type: image/png\r\nContent-Disposition: inline; filename=\"logo.png\"\r\nContent-Transfer-Encoding: base64\r\n\r\n" + png + "\r\n" +
		"--B--\r\n")
}

// attBig: one attachment whose DECODED size exceeds the 256 KB inline cap.
func attBig() []byte {
	big := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("X", 300*1024)))
	return []byte("Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=\"big.bin\"\r\nContent-Transfer-Encoding: base64\r\n\r\n" + big + "\r\n" +
		"--B--\r\n")
}

func attTestServer(t *testing.T, opts ...func(*Deps)) *httptest.Server {
	t.Helper()
	u := &identity.User{ID: "u_1", Email: "owner@acme.com"}
	deps := Deps{
		PrincipalAuthenticator: func(r *http.Request) (*identity.Principal, error) {
			switch r.Header.Get("Authorization") {
			case "Bearer acct":
				return &identity.Principal{User: u, Scope: identity.ScopeAccount}, nil
			case "Bearer agtOther":
				return &identity.Principal{User: u, Scope: identity.ScopeAgent, AgentID: "other@acme.com"}, nil
			}
			return nil, errors.New("unauthorized")
		},
		AuthChallenge: func(r *http.Request) string { return `Bearer realm="e2a"` },
		GetAgent: func(ctx context.Context, address string) (*identity.AgentIdentity, error) {
			switch address {
			case "support@acme.com":
				a := sampleAgent()
				return &a, nil
			case "other@acme.com":
				// A second agent of the same owner — used to prove a download token
				// minted for support's message can't be replayed against other's path.
				a := sampleAgent()
				a.ID = "other@acme.com"
				return &a, nil
			}
			return nil, errors.New("not found")
		},
		GetMessage: func(ctx context.Context, id, agentID string) (*identity.Message, error) {
			if agentID != "support@acme.com" {
				return nil, errors.New("not found")
			}
			switch id {
			case "msg_att":
				return &identity.Message{ID: id, Direction: "inbound", Recipient: "support@acme.com", RawMessage: attMultipart()}, nil
			case "msg_big":
				return &identity.Message{ID: id, Direction: "inbound", Recipient: "support@acme.com", RawMessage: attBig()}, nil
			}
			return nil, errors.New("not found")
		},
		AttachmentStore: NewNativeAttachmentStore("test-secret-test-secret", "http://att.test"),
		Legacy:          http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }),
	}
	for _, o := range opts {
		o(&deps)
	}
	srv := httptest.NewServer(New(deps).Router)
	t.Cleanup(srv.Close)
	return srv
}

func getJSONAtt(t *testing.T, url, bearer string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func TestAttachment_MetadataAndSignedURL(t *testing.T) {
	srv := attTestServer(t)
	code, body := getJSONAtt(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_att/attachments/0", "acct")
	if code != 200 {
		t.Fatalf("want 200, got %d: %v", code, body)
	}
	if body["filename"] != "report.pdf" || body["content_type"] != "application/pdf" {
		t.Errorf("metadata wrong: %v", body)
	}
	if body["download_url"] == nil || body["expires_at"] == nil {
		t.Errorf("expected download_url + expires_at, got %v", body)
	}
	if _, hasData := body["data"]; hasData {
		t.Errorf("data must NOT be present without inline=true: %v", body)
	}
}

func TestAttachment_InlineSmall(t *testing.T) {
	srv := attTestServer(t)
	code, body := getJSONAtt(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_att/attachments/0?inline=true", "acct")
	if code != 200 {
		t.Fatalf("want 200, got %d: %v", code, body)
	}
	dec, _ := base64.StdEncoding.DecodeString(body["data"].(string))
	if string(dec) != "%PDF-1.4 hello" {
		t.Errorf("inline data wrong: %q", dec)
	}
}

func TestAttachment_InlineTooLarge(t *testing.T) {
	srv := attTestServer(t)
	code, body := getJSONAtt(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_big/attachments/0?inline=true", "acct")
	if code != http.StatusRequestEntityTooLarge || errCode(body) != "attachment_too_large" {
		t.Fatalf("want 413 attachment_too_large, got %d %v", code, body)
	}
	// Without inline, the same big attachment still yields a download_url (no cap).
	code2, body2 := getJSONAtt(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_big/attachments/0", "acct")
	if code2 != 200 || body2["download_url"] == nil {
		t.Fatalf("big attachment by URL should be 200 with download_url, got %d %v", code2, body2)
	}
}

func TestAttachment_IndexOutOfRange(t *testing.T) {
	srv := attTestServer(t)
	code, body := getJSONAtt(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_att/attachments/9", "acct")
	if code != 404 || errCode(body) != "attachment_not_found" {
		t.Fatalf("want 404 attachment_not_found, got %d %v", code, body)
	}
}

func TestAttachment_AgentScopeCannotReadOtherAgentsMessage(t *testing.T) {
	srv := attTestServer(t)
	// agtOther is pinned to other@acme.com; the path names support@acme.com.
	code, body := getJSONAtt(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_att/attachments/0", "agtOther")
	if code != 403 {
		t.Fatalf("agent-scope cross-agent read must be 403, got %d %v", code, body)
	}
}

// downloadPath extracts the path+query of the minted download_url so we can hit
// the test server (ignoring the configured public host).
func downloadPathFromURL(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Path + "?" + u.RawQuery
}

func assertRawDownloadError(t *testing.T, resp *http.Response, status int, code string) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != status {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want %d: %s", resp.StatusCode, status, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON envelope", ct)
	}
	var env struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.Error.Code != code {
		t.Errorf("error.code = %q, want %q", env.Error.Code, code)
	}
	requestID := resp.Header.Get("X-Request-Id")
	if requestID == "" || env.Error.RequestID != requestID {
		t.Errorf("request id mismatch: header=%q body=%q", requestID, env.Error.RequestID)
	}
}

func TestAttachment_DownloadErrorsUseCanonicalEnvelope(t *testing.T) {
	store := NewNativeAttachmentStore("test-secret-test-secret", "http://att.test")
	mint := func(t *testing.T, messageID string, index int) string {
		t.Helper()
		raw, _, err := store.DownloadURL("support@acme.com", messageID, index, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return downloadPathFromURL(t, raw)
	}

	t.Run("invalid index", func(t *testing.T) {
		srv := attTestServer(t)
		resp, err := http.Get(srv.URL + "/v1/agents/support@acme.com/messages/msg_att/attachments/nope/download?token=x")
		if err != nil {
			t.Fatal(err)
		}
		assertRawDownloadError(t, resp, http.StatusBadRequest, "invalid_request")
	})

	t.Run("missing token", func(t *testing.T) {
		srv := attTestServer(t)
		resp, err := http.Get(srv.URL + "/v1/agents/support@acme.com/messages/msg_att/attachments/0/download")
		if err != nil {
			t.Fatal(err)
		}
		assertRawDownloadError(t, resp, http.StatusUnauthorized, "unauthorized")
	})

	t.Run("unavailable", func(t *testing.T) {
		srv := attTestServer(t, func(d *Deps) { d.GetAgent = nil })
		resp, err := http.Get(srv.URL + "/v1/agents/support@acme.com/messages/msg_att/attachments/0/download?token=x")
		if err != nil {
			t.Fatal(err)
		}
		assertRawDownloadError(t, resp, http.StatusInternalServerError, "internal_error")
	})

	t.Run("invalid token", func(t *testing.T) {
		srv := attTestServer(t)
		resp, err := http.Get(srv.URL + "/v1/agents/support@acme.com/messages/msg_att/attachments/0/download?token=bogus")
		if err != nil {
			t.Fatal(err)
		}
		assertRawDownloadError(t, resp, http.StatusForbidden, "forbidden")
	})

	t.Run("agent not found", func(t *testing.T) {
		srv := attTestServer(t)
		path := strings.Replace(mint(t, "msg_att", 0), "support@acme.com", "missing@acme.com", 1)
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		assertRawDownloadError(t, resp, http.StatusNotFound, "not_found")
	})

	t.Run("message not found", func(t *testing.T) {
		srv := attTestServer(t)
		resp, err := http.Get(srv.URL + mint(t, "msg_missing", 0))
		if err != nil {
			t.Fatal(err)
		}
		assertRawDownloadError(t, resp, http.StatusNotFound, "not_found")
	})

	t.Run("attachment not found", func(t *testing.T) {
		srv := attTestServer(t)
		resp, err := http.Get(srv.URL + mint(t, "msg_att", 9))
		if err != nil {
			t.Fatal(err)
		}
		assertRawDownloadError(t, resp, http.StatusNotFound, "attachment_not_found")
	})
}

func TestAttachment_DownloadRoute(t *testing.T) {
	srv := attTestServer(t)
	_, meta := getJSONAtt(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_att/attachments/0", "acct")
	dlPath := downloadPathFromURL(t, meta["download_url"].(string))

	// Happy path: capability token streams the bytes (no bearer).
	resp, err := http.Get(srv.URL + dlPath)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(b) != "%PDF-1.4 hello" {
		t.Fatalf("download want 200 + pdf bytes, got %d %q", resp.StatusCode, b)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("content-type: got %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "report.pdf") {
		t.Errorf("content-disposition: got %q", cd)
	}
	if got := resp.Header.Get("X-Request-Id"); got == "" {
		t.Error("raw attachment download must carry X-Request-Id")
	}
	for name := range resp.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-ratelimit-") {
			t.Errorf("raw attachment download emitted legacy header %s", name)
		}
	}

	// Bad token → 403.
	bad := strings.Replace(dlPath, "token=", "token=bogus", 1)
	r2, _ := http.Get(srv.URL + bad)
	r2.Body.Close()
	if r2.StatusCode != http.StatusForbidden {
		t.Errorf("bad token want 403, got %d", r2.StatusCode)
	}

	// A valid token for index 0 must NOT work on index 1's path (token binds index).
	wrongIdx := strings.Replace(dlPath, "/attachments/0/download", "/attachments/1/download", 1)
	r3, _ := http.Get(srv.URL + wrongIdx)
	r3.Body.Close()
	if r3.StatusCode != http.StatusForbidden {
		t.Errorf("index-mismatched token want 403, got %d", r3.StatusCode)
	}
}

// TestAttachment_DownloadRateLimited: the raw download route (outside the Huma
// rate-limit middleware) is throttled per-IP. The 3rd request over a cap of 2 is
// a 429 carrying Retry-After + RateLimit-* headers and the e2a error envelope.
func TestAttachment_DownloadRateLimited(t *testing.T) {
	lim := ratelimit.New(time.Minute, 2) // 2 downloads per IP per minute
	srv := attTestServer(t, func(d *Deps) { d.DownloadLimit = lim.AllowSnapshot })

	_, meta := getJSONAtt(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_att/attachments/0", "acct")
	dlPath := downloadPathFromURL(t, meta["download_url"].(string))

	// First two downloads succeed.
	for i := 0; i < 2; i++ {
		r, err := http.Get(srv.URL + dlPath)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("download %d: want 200, got %d", i, r.StatusCode)
		}
		if r.Header.Get("RateLimit-Limit") == "" {
			t.Errorf("download %d: missing RateLimit-Limit header", i)
		}
	}

	// Third is throttled.
	r3, err := http.Get(srv.URL + dlPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r3.Body.Close()
	if r3.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("3rd download: want 429, got %d", r3.StatusCode)
	}
	if r3.Header.Get("Retry-After") == "" {
		t.Error("429 missing Retry-After header")
	}
	if ct := r3.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("429 content-type = %q, want JSON envelope", ct)
	}
	var env map[string]any
	if err := json.NewDecoder(r3.Body).Decode(&env); err != nil {
		t.Fatalf("decode 429 body: %v", err)
	}
	e, _ := env["error"].(map[string]any)
	if e == nil || e["code"] != "rate_limited" {
		t.Errorf("429 body must be the e2a envelope with code=rate_limited, got %v", env)
	}
}

// The download route's claimed defense: a token minted for support's message must
// not stream when replayed against a path naming a DIFFERENT agent — the binding
// rests on GetMessage being keyed by the path agent's id. (Adversarial review #1.)
func TestAttachment_DownloadTokenCannotCrossAgents(t *testing.T) {
	srv := attTestServer(t)
	_, meta := getJSONAtt(t, srv.URL+"/v1/agents/support%40acme.com/messages/msg_att/attachments/0", "acct")
	dlPath := downloadPathFromURL(t, meta["download_url"].(string))
	// The minted URL carries the literal '@' (url.PathEscape leaves it); swap the agent.
	crossed := strings.Replace(dlPath, "support@acme.com", "other@acme.com", 1)
	if crossed == dlPath {
		crossed = strings.Replace(dlPath, "support%40acme.com", "other%40acme.com", 1)
	}
	r, _ := http.Get(srv.URL + crossed)
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode == http.StatusOK {
		t.Fatalf("cross-agent token replay MUST NOT stream bytes, got 200: %q", body)
	}
	if r.StatusCode != http.StatusNotFound {
		t.Errorf("cross-agent replay want 404 (msg not owned by path agent), got %d", r.StatusCode)
	}
}

func TestNativeAttachmentStore_EmptySecretFailsClosed(t *testing.T) {
	s := NewNativeAttachmentStore("", "http://x")
	if _, _, err := s.DownloadURL("a@b.com", "msg", 0, time.Minute); err == nil {
		t.Error("empty-secret DownloadURL must error, not mint a forgeable URL")
	}
	if s.VerifyDownload("anything.anything", "msg", 0) {
		t.Error("empty-secret VerifyDownload must be false")
	}
}

func TestNativeAttachmentStore_ExpiryBoundary(t *testing.T) {
	s := NewNativeAttachmentStore("secret-secret-secret-secret-secret", "http://x").(*nativeAttachmentStore)
	past := s.sign("msg", 0, time.Now().Add(-time.Second).Unix())
	if s.VerifyDownload(past, "msg", 0) {
		t.Error("past-expiry token must be rejected")
	}
	// Exact-expiry second must reject (off-by-one fix).
	boundary := s.sign("msg", 0, time.Now().Unix())
	if s.VerifyDownload(boundary, "msg", 0) {
		t.Error("token expiring this exact second must be rejected")
	}
}
