package agent

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	"github.com/Mnexa-AI/e2a/internal/identity"
)


// CreateSigningSecretRequest is the body of POST /api/v1/users/me/signing-secrets.
type CreateSigningSecretRequest struct {
	// Optional human-readable label so users can tell secrets apart in
	// the dashboard (e.g. "prod", "staging", "rollover-2026-04").
	Name string `json:"name"`
}

// SigningSecretSummary is the safe-to-list shape: prefix only, no
// plaintext. Returned by GET (list) and as a field of the create
// response so callers can confirm what they just made.
type SigningSecretSummary struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	SecretPrefix string  `json:"secret_prefix"`
	CreatedAt    string  `json:"created_at"`
	LastSignedAt *string `json:"last_signed_at,omitempty"`
} // @name SigningSecretSummary

// CreateSigningSecretResponse is returned exactly once at creation.
// The plaintext Secret is the only chance the caller has to capture
// the value — subsequent reads only see SecretPrefix.
type CreateSigningSecretResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Secret       string `json:"secret"`
	SecretPrefix string `json:"secret_prefix"`
	CreatedAt    string `json:"created_at"`
} // @name CreateSigningSecretResponse

// ListSigningSecretsResponse is the GET shape.
type ListSigningSecretsResponse struct {
	Secrets []SigningSecretSummary `json:"secrets"`
} // @name ListSigningSecretsResponse

// handleListSigningSecrets returns the authenticated user's webhook
// signing secrets — id, name, prefix, created_at, last_signed_at —
// sorted most-recent-first. The plaintext secret values are NOT in
// this response; they're only shown at creation.
//
// @Summary      List your webhook signing secrets
// @Description  Returns the authenticated user's webhook signing secrets (metadata + 12-char prefix preview only — full secrets are only shown once at creation). Sorted most-recent-first; the most-recent secret is what the e2a relay uses for new signatures.
// @Tags         User
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} ListSigningSecretsResponse
// @Failure      401 {string} string "Missing or invalid API key"
// @Router       /api/v1/users/me/signing-secrets [get]
func (a *API) handleListSigningSecrets(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	secrets, err := a.store.ListSigningSecrets(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "failed to list signing secrets", http.StatusInternalServerError)
		return
	}
	out := ListSigningSecretsResponse{Secrets: make([]SigningSecretSummary, 0, len(secrets))}
	for _, s := range secrets {
		out.Secrets = append(out.Secrets, toSummary(s))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleCreateSigningSecret mints a new secret, returning the plaintext
// value exactly once. The user must save it on receipt — subsequent
// reads only return the prefix.
//
// @Summary      Create a new webhook signing secret
// @Description  Mints a new HMAC signing secret for the authenticated user. The full plaintext `secret` is returned only in this response — save it now, you cannot retrieve it later. Hard cap is 5 active secrets per user; delete one before creating another.
// @Tags         User
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body body CreateSigningSecretRequest false "Optional name/label"
// @Success      201 {object} CreateSigningSecretResponse
// @Failure      400 {string} string "Bad request (e.g. cap reached)"
// @Failure      401 {string} string "Missing or invalid API key"
// @Router       /api/v1/users/me/signing-secrets [post]
func (a *API) handleCreateSigningSecret(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	var req CreateSigningSecretRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
	}
	req.Name = strings.TrimSpace(req.Name)
	secret, err := a.store.CreateSigningSecret(r.Context(), user.ID, req.Name)
	if err != nil {
		if errors.Is(err, identity.ErrSigningSecretCapReached) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "failed to create signing secret", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(CreateSigningSecretResponse{
		ID:           secret.ID,
		Name:         secret.Name,
		Secret:       secret.Secret,
		SecretPrefix: secret.SecretPrefix,
		CreatedAt:    secret.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

// handleDeleteSigningSecret removes a secret by id. Refuses to delete
// the user's last secret — every user must keep at least one or
// webhooks become unverifiable.
//
// @Summary      Delete a webhook signing secret
// @Description  Deletes the named secret. Refuses if it would leave the user with zero secrets — create a replacement first. After delete, any in-flight HITL approval magic-link tokens signed under the deleted secret stop verifying.
// @Tags         User
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Signing secret ID (e.g. wsec_abc123...)"
// @Success      204 {string} string ""
// @Failure      400 {string} string "Cannot delete last secret"
// @Failure      401 {string} string "Missing or invalid API key"
// @Failure      404 {string} string "Secret not found"
// @Router       /api/v1/users/me/signing-secrets/{id} [delete]
func (a *API) handleDeleteSigningSecret(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	id := mux.Vars(r)["id"]
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if err := a.store.DeleteSigningSecret(r.Context(), id, user.ID); err != nil {
		switch {
		case errors.Is(err, identity.ErrCannotDeleteLastSigningSecret):
			http.Error(w, err.Error(), http.StatusBadRequest)
		case errors.Is(err, identity.ErrSigningSecretNotFound):
			http.Error(w, err.Error(), http.StatusNotFound)
		default:
			http.Error(w, "failed to delete signing secret", http.StatusInternalServerError)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func toSummary(s identity.SigningSecret) SigningSecretSummary {
	out := SigningSecretSummary{
		ID:           s.ID,
		Name:         s.Name,
		SecretPrefix: s.SecretPrefix,
		CreatedAt:    s.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if s.LastSignedAt != nil {
		ts := s.LastSignedAt.UTC().Format("2006-01-02T15:04:05Z")
		out.LastSignedAt = &ts
	}
	return out
}

