package agent

import (
	"encoding/json"
	"log"
	"net/http"
)

// handleExportUserData returns a complete dump of the authenticated
// user's data as JSON. Used for the right-of-access flow (GDPR Art. 15,
// CCPA equivalent). Output is deterministic-ordered (created_at) so a
// caller diffing exports across time gets stable results.
//
// @Summary      Export your data
// @Description  Returns a JSON dump of every record the authenticated user owns: profile, agents, domains, API key metadata, messages (with bodies), and usage events. API key plaintexts are not included — they were never stored. Internal identifiers (google_subject, session tokens, key hashes) are excluded.
// @Tags         User
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} UserExport
// @Failure      401 {string} string "Missing or invalid API key"
// @Router       /api/v1/users/me/export [get]
func (a *API) handleExportUserData(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}

	dump, err := a.store.ExportUserData(r.Context(), user.ID)
	if err != nil {
		log.Printf("[api] export user data failed: user=%s err=%v", user.ID, err)
		http.Error(w, "failed to export user data", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	// Filename hint for browser/CLI clients that download the response.
	w.Header().Set("Content-Disposition", `attachment; filename="e2a-export-`+user.ID+`.json"`)
	if err := json.NewEncoder(w).Encode(dump); err != nil {
		// Already streaming — no point trying to write a status code.
		log.Printf("[api] export encode failed: user=%s err=%v", user.ID, err)
	}
}

// handleDeleteUserData wipes the authenticated user and every record
// tied to them, in a single Postgres transaction. Used for the
// right-of-deletion flow (GDPR Art. 17, CCPA "Do Not Sell or Share").
//
// Requires `?confirm=DELETE` in the query string as a guardrail
// against accidental clicks. The HTTP method (DELETE) plus this
// confirmation matches the pattern other destructive APIs use
// (Stripe's account close, GitHub's repo delete).
//
// @Summary      Delete your account and all associated data
// @Description  Permanently deletes the authenticated user along with their agents, domains, messages, API keys, sessions, and usage data. **Irreversible.** Requires `confirm=DELETE` query parameter as a guardrail. Returns per-table counts of removed rows so the caller can audit the cascade.
// @Tags         User
// @Produce      json
// @Security     BearerAuth
// @Param        confirm query string true "Must equal 'DELETE' to proceed"
// @Success      200 {object} DeleteUserDataResult
// @Failure      400 {string} string "Missing or invalid confirm parameter"
// @Failure      401 {string} string "Missing or invalid API key"
// @Router       /api/v1/users/me [delete]
func (a *API) handleDeleteUserData(w http.ResponseWriter, r *http.Request) {
	user, err := a.authenticateUser(r)
	if err != nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}

	// Guardrail: misclicks shouldn't be able to wipe a real account.
	// The CLI/SDK should hide this behind a `--yes` flag; the dashboard
	// behind a typed-in confirmation. The server-side check is just a
	// last line of defense.
	if r.URL.Query().Get("confirm") != "DELETE" {
		http.Error(w, `add ?confirm=DELETE to the request to proceed — this is irreversible`, http.StatusBadRequest)
		return
	}

	res, err := a.store.DeleteUserData(r.Context(), user.ID)
	if err != nil {
		log.Printf("[api] delete user data failed: user=%s err=%v", user.ID, err)
		http.Error(w, "failed to delete user data", http.StatusInternalServerError)
		return
	}

	log.Printf("[api] user deleted: id=%s email=%s removed=%+v", user.ID, user.Email, res)

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, res)
}
