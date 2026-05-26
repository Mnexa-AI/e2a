package agent

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
)

// LimitsInfo is the response body for GET /api/v1/users/me/limits. It
// bundles the user's resolved caps with their current usage so the
// dashboard renders the "you're using X of Y" surface in one round
// trip. plan_code and upgrade_url come straight from the limits row
// (opaque to OSS — written by whatever provisions the row); when no
// row exists, plan_code is the operator default and upgrade_url is
// empty.
type LimitsInfo struct {
	PlanCode   string      `json:"plan_code"`
	Limits     LimitsCaps  `json:"limits"`
	Usage      LimitsUsage `json:"usage"`
	UpgradeURL string      `json:"upgrade_url"`
} // @name LimitsInfo

type LimitsCaps struct {
	MaxAgents        int   `json:"max_agents"`
	MaxDomains       int   `json:"max_domains"`
	MaxMessagesMonth int   `json:"max_messages_month"`
	MaxStorageBytes  int64 `json:"max_storage_bytes"`
} // @name LimitsCaps

type LimitsUsage struct {
	Agents        int   `json:"agents"`
	Domains       int   `json:"domains"`
	MessagesMonth int   `json:"messages_month"`
	StorageBytes  int64 `json:"storage_bytes"`
} // @name LimitsUsage

// handleGetMyLimits returns the authenticated user's caps + current
// usage. Read-only; safe to call from the dashboard on every page load.
//
// @Summary      Get the current user's limits and usage
// @Description  Returns the resolved per-user caps (from account_limits or operator defaults) plus the user's current usage. Dashboard uses this to render the upgrade affordance and the "you've used X of Y" surface.
// @Tags         Users
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} LimitsInfo
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      503 {string} string "Limits subsystem not configured"
// @Router       /api/v1/users/me/limits [get]
func (a *API) handleGetMyLimits(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		a.writeAuthError(w, r, err)
		return
	}
	if a.enforcer == nil {
		// Self-host without limits wired. The dashboard should never
		// show its limits surface in this case (NEXT_PUBLIC_BILLING_API
		// is empty), so this is an "operator misconfig" path rather
		// than a normal one. 503 makes it loud.
		http.Error(w, "limits subsystem not configured", http.StatusServiceUnavailable)
		return
	}

	caps, err := a.enforcer.Get(r.Context(), user.ID)
	if err != nil {
		log.Printf("[api] limits.Get error: %v", err)
		http.Error(w, "limits lookup failed", http.StatusInternalServerError)
		return
	}

	// Read usage directly from the usage store. Done outside the
	// enforcer because the enforcer never caches counts (would lie
	// after a just-created agent or just-sent message). Errors here
	// are non-fatal — surface 0 with a log so the dashboard still
	// renders something instead of erroring the whole panel.
	ctx := r.Context()
	usageReport := LimitsUsage{}
	if a.usageStore != nil {
		if n, err := a.usageStore.CountAgentsByUser(ctx, user.ID); err == nil {
			usageReport.Agents = n
		} else {
			log.Printf("[api] CountAgentsByUser: %v", err)
		}
		if n, err := a.usageStore.CountDomainsByUser(ctx, user.ID); err == nil {
			usageReport.Domains = n
		} else {
			log.Printf("[api] CountDomainsByUser: %v", err)
		}
		if n, err := a.usageStore.MessagesThisMonth(ctx, user.ID); err == nil {
			usageReport.MessagesMonth = n
		} else {
			log.Printf("[api] MessagesThisMonth: %v", err)
		}
		if n, err := a.usageStore.GetStorageBytes(ctx, user.ID); err == nil {
			usageReport.StorageBytes = n
		} else {
			log.Printf("[api] GetStorageBytes: %v", err)
		}
	}

	resp := LimitsInfo{
		PlanCode: caps.PlanCode,
		Limits: LimitsCaps{
			MaxAgents:        caps.MaxAgents,
			MaxDomains:       caps.MaxDomains,
			MaxMessagesMonth: caps.MaxMessagesMonth,
			MaxStorageBytes:  caps.MaxStorageBytes,
		},
		Usage:      usageReport,
		UpgradeURL: caps.UpgradeURL,
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

// invalidateLimitsRequest is the body of POST /api/internal/limits/invalidate.
type invalidateLimitsRequest struct {
	UserID string `json:"user_id"`
}

// handleInvalidateLimits busts the in-process limits cache for the
// given user. Called by the external provisioner (billing sidecar)
// immediately after it writes account_limits, so the next request from
// that user sees the new caps without waiting ~60s for natural TTL
// expiry.
//
// Authentication is a shared HMAC over the request body. The sidecar
// signs with the same secret the OSS server is configured with, sends
// the hex digest in X-E2A-Internal-Signature, and the OSS server
// verifies with a constant-time compare. Anything else (bearer tokens,
// API keys) would either tangle this machine-to-machine endpoint with
// user-scoped auth or require a separate credential store.
//
// The endpoint is intentionally not advertised in the OpenAPI spec —
// it's an internal seam between the OSS server and its operator's
// provisioner. Self-hosters who don't run a provisioner simply leave
// InternalAPISecret empty and the endpoint 503s.
func (a *API) handleInvalidateLimits(w http.ResponseWriter, r *http.Request) {
	if a.internalAPISecret == "" {
		http.Error(w, "internal api not configured", http.StatusServiceUnavailable)
		return
	}
	if a.enforcer == nil {
		http.Error(w, "limits subsystem not configured", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	sig := r.Header.Get("X-E2A-Internal-Signature")
	if sig == "" {
		http.Error(w, "missing signature", http.StatusUnauthorized)
		return
	}
	expected := hmacHexSHA256([]byte(a.internalAPISecret), body)
	if subtle.ConstantTimeCompare([]byte(sig), []byte(expected)) != 1 {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	var req invalidateLimitsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.UserID == "" {
		http.Error(w, "user_id is required", http.StatusBadRequest)
		return
	}

	a.enforcer.Invalidate(req.UserID)
	w.WriteHeader(http.StatusNoContent)
}

func hmacHexSHA256(key, body []byte) string {
	h := hmac.New(sha256.New, key)
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

