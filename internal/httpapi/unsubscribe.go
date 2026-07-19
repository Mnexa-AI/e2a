package httpapi

import (
	"html/template"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tokencanopy/e2a/internal/unsubscribe"
)

const (
	publicUnsubscribeMaxBody = 1024
	publicUnsubscribeCSP     = "default-src 'none'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
	rfc8058OneClickBody      = "List-Unsubscribe=One-Click"
)

var (
	unsubscribeConfirmPage = template.Must(template.New("unsubscribe-confirm").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Unsubscribe</title></head><body><main><h1>Unsubscribe</h1><p>Stop emails sent by {{.}}?</p><form method="post"><button type="submit" name="confirm" value="unsubscribe">Unsubscribe</button></form></main></body></html>`))
	unsubscribeSuccessPage = template.Must(template.New("unsubscribe-success").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Unsubscribed</title></head><body><main><h1>Unsubscribed</h1><p>You will no longer receive emails from this sender.</p></main></body></html>`))
)

// handlePublicUnsubscribe resolves an opaque bearer capability. It never logs
// the token or its scope and deliberately gives malformed and unknown tokens
// the same response.
func (s *Server) handlePublicUnsubscribe(w http.ResponseWriter, r *http.Request) {
	setPublicUnsubscribeHeaders(w)
	// This bearer-capability route sits outside Huma's authenticated limiter.
	// Reuse the existing raw-capability per-IP budget and apply it before even
	// hashing/resolving the token, matching attachment download semantics.
	if s.deps.DownloadLimit != nil {
		ok, retryAfter, limit, remaining, reset := s.deps.DownloadLimit(clientIP(r))
		w.Header().Set("RateLimit-Limit", strconv.Itoa(limit))
		w.Header().Set("RateLimit-Remaining", strconv.Itoa(remaining))
		w.Header().Set("RateLimit-Reset", strconv.Itoa(reset))
		if !ok {
			secs := int(retryAfter.Round(time.Second).Seconds())
			if secs < 1 {
				secs = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			writePublicUnsubscribeError(w, http.StatusTooManyRequests)
			return
		}
	}
	token := chi.URLParam(r, "token")
	if !validPublicUnsubscribeToken(token) || s.deps.ResolveUnsubscribeToken == nil {
		writePublicUnsubscribeError(w, http.StatusNotFound)
		return
	}

	scope, err := s.deps.ResolveUnsubscribeToken(r.Context(), unsubscribe.Hash(token))
	if err != nil {
		// Lookup errors may contain driver values. Do not risk logging bearer
		// material or recipient scope from this unauthenticated path.
		log.Printf("[unsubscribe] token lookup failed")
		writePublicUnsubscribeError(w, http.StatusInternalServerError)
		return
	}
	if scope == nil {
		writePublicUnsubscribeError(w, http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if err := unsubscribeConfirmPage.Execute(w, scope.AgentID); err != nil {
			log.Printf("[unsubscribe] confirmation render failed: %v", err)
		}
	case http.MethodPost:
		rfc, ok := parsePublicUnsubscribePOST(w, r)
		if !ok {
			writePublicUnsubscribeError(w, http.StatusBadRequest)
			return
		}
		if s.deps.AddAgentSuppressionFromTokenScope == nil {
			writePublicUnsubscribeError(w, http.StatusInternalServerError)
			return
		}
		if _, _, err := s.deps.AddAgentSuppressionFromTokenScope(r.Context(), *scope, s.deps.AgentSuppressionAddedHook); err != nil {
			log.Printf("[unsubscribe] suppression insert failed")
			writePublicUnsubscribeError(w, http.StatusInternalServerError)
			return
		}
		if rfc {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if err := unsubscribeSuccessPage.Execute(w, nil); err != nil {
			log.Printf("[unsubscribe] success render failed: %v", err)
		}
	default:
		writePublicUnsubscribeError(w, http.StatusMethodNotAllowed)
	}
}

func setPublicUnsubscribeHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", publicUnsubscribeCSP)
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func validPublicUnsubscribeToken(token string) bool {
	// u1_ plus an unpadded base64url SHA-256 digest (43 characters).
	if len(token) != 46 || !strings.HasPrefix(token, "u1_") {
		return false
	}
	for _, c := range token[3:] {
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

func parsePublicUnsubscribePOST(w http.ResponseWriter, r *http.Request) (rfc bool, ok bool) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		return false, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, publicUnsubscribeMaxBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return false, false
	}
	if string(body) == rfc8058OneClickBody {
		return true, true
	}
	values, err := url.ParseQuery(string(body))
	if err != nil || len(values) != 1 || len(values["confirm"]) != 1 || values.Get("confirm") != "unsubscribe" {
		return false, false
	}
	return false, true
}

func writePublicUnsubscribeError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, http.StatusText(status)+"\n")
}
