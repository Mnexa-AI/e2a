package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSubscriberDeliverer_SignsRequest(t *testing.T) {
	var gotSignatureHeader string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSignatureHeader = r.Header.Get("X-E2A-Signature")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewSubscriberDeliverer(false, "")
	body := []byte(`{"event":"email.received","id":"evt_x"}`)
	out := d.Deliver(context.Background(), srv.URL, body, "whsec_secret", "")
	if !out.Success {
		t.Fatalf("Deliver returned failure: %+v", out)
	}
	if out.StatusCode != 200 {
		t.Errorf("status = %d, want 200", out.StatusCode)
	}

	if !strings.HasPrefix(gotSignatureHeader, "t=") {
		t.Errorf("signature header missing t= prefix: %q", gotSignatureHeader)
	}
	if !strings.Contains(gotSignatureHeader, ",v1=") {
		t.Errorf("signature header missing v1= clause: %q", gotSignatureHeader)
	}

	// Verify the signature is correct
	parts := strings.Split(gotSignatureHeader, ",")
	if len(parts) != 2 {
		t.Fatalf("signature header expected 2 parts (t=, v1=), got %d: %q", len(parts), gotSignatureHeader)
	}
	tStr := strings.TrimPrefix(parts[0], "t=")
	v1Hex := strings.TrimPrefix(parts[1], "v1=")
	timestamp, err := strconv.ParseInt(tStr, 10, 64)
	if err != nil {
		t.Fatalf("parse t: %v", err)
	}
	mac := hmac.New(sha256.New, []byte("whsec_secret"))
	fmt.Fprintf(mac, "%d.", timestamp)
	mac.Write(gotBody)
	wantHex := hex.EncodeToString(mac.Sum(nil))
	if v1Hex != wantHex {
		t.Errorf("signature mismatch:\n  got:  %s\n  want: %s", v1Hex, wantHex)
	}
}

func TestSubscriberDeliverer_DualSigDuringRotationGrace(t *testing.T) {
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-E2A-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewSubscriberDeliverer(false, "")
	body := []byte(`{"event":"email.received"}`)
	out := d.Deliver(context.Background(), srv.URL, body, "whsec_new", "whsec_old")
	if !out.Success {
		t.Fatalf("Deliver returned failure: %+v", out)
	}

	// Header should carry two v1= clauses during the rotation window.
	count := strings.Count(gotSig, "v1=")
	if count != 2 {
		t.Errorf("expected 2 v1= clauses during rotation window, got %d: %q", count, gotSig)
	}
}

func TestSubscriberDeliverer_4xxIsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	d := NewSubscriberDeliverer(false, "")
	out := d.Deliver(context.Background(), srv.URL, []byte(`{}`), "whsec_x", "")
	if out.Success {
		t.Error("4xx response reported as success")
	}
	if out.StatusCode != 400 {
		t.Errorf("status_code = %d, want 400", out.StatusCode)
	}
}

func TestSubscriberDeliverer_5xxIsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	d := NewSubscriberDeliverer(false, "")
	out := d.Deliver(context.Background(), srv.URL, []byte(`{}`), "whsec_x", "")
	if out.Success {
		t.Error("5xx response reported as success")
	}
	if out.StatusCode != 503 {
		t.Errorf("status_code = %d, want 503", out.StatusCode)
	}
}

func TestSubscriberDeliverer_ConnectionErrorReportsZeroStatus(t *testing.T) {
	// 192.0.2.0/24 is reserved for documentation per RFC 5737;
	// connection attempts to it should fail without ever reaching a
	// real service.
	d := NewSubscriberDeliverer(false, "")
	d.client.Timeout = 100 * time.Millisecond // speed up the test
	out := d.Deliver(context.Background(), "http://192.0.2.1:8080/", []byte(`{}`), "whsec_x", "")
	if out.Success {
		t.Error("connection failure reported as success")
	}
	if out.StatusCode != 0 {
		t.Errorf("status_code on connection error = %d, want 0", out.StatusCode)
	}
}

func TestSubscriberDeliverer_RefusesPlaintextInProd(t *testing.T) {
	d := NewSubscriberDeliverer(true, "") // requireHTTPS=true
	out := d.Deliver(context.Background(), "http://example.com/hook", []byte(`{}`), "whsec_x", "")
	if out.Success {
		t.Error("plaintext URL allowed in HTTPS-required mode")
	}
	if !strings.Contains(out.Error, "HTTPS") {
		t.Errorf("error message doesn't mention HTTPS: %q", out.Error)
	}
}

func TestSubscriberDeliverer_ExemptsConfiguredInternalSink(t *testing.T) {
	// A plaintext, loopback sink — exactly what the e2a-prober exposes. Under
	// requireHTTPS=true this would normally be refused (http://) and, if it got
	// that far, dial-blocked (127.0.0.1). Configured as the internal sink, an
	// exact-URL-match delivery must bypass both guards.
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewSubscriberDeliverer(true /* requireHTTPS */, srv.URL /* internalSinkURL */)

	// Exact match → exempt → delivered over plain HTTP to loopback.
	out := d.Deliver(context.Background(), srv.URL, []byte(`{"event":"email.received"}`), "whsec_x", "")
	if !out.Success {
		t.Fatalf("delivery to the configured internal sink failed: %+v", out)
	}
	if !hit {
		t.Error("sink handler was never called")
	}

	// A DIFFERENT plaintext URL is NOT exempt — exact-match only, so the HTTPS
	// guard still applies. This is the security property that keeps the
	// exemption from widening into an arbitrary-internal-host SSRF.
	out = d.Deliver(context.Background(), "http://example.com/other", []byte(`{}`), "whsec_x", "")
	if out.Success {
		t.Error("a non-sink plaintext URL was allowed — exemption must be exact-match only")
	}
	if !strings.Contains(out.Error, "HTTPS") {
		t.Errorf("non-sink plaintext error should mention HTTPS, got %q", out.Error)
	}
}

func TestSubscriberDeliverer_RefusesRedirects(t *testing.T) {
	// The deliverer's CheckRedirect: http.ErrUseLastResponse means
	// it captures the 302 status rather than following it. Treat
	// 3xx as a failure (not success and not a silent redirect).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/", http.StatusFound)
	}))
	defer srv.Close()

	d := NewSubscriberDeliverer(false, "")
	out := d.Deliver(context.Background(), srv.URL, []byte(`{}`), "whsec_x", "")
	if out.Success {
		t.Error("3xx response reported as success — redirects must be refused")
	}
}
