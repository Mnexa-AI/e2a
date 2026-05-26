package limits

import (
	"encoding/json"
	"net/http"
)

// LimitErrorBody is the JSON payload returned with HTTP 402 when a
// Check* call surfaces a *LimitExceededError. Handlers MUST use this
// shape so the dashboard and SDK clients can render a uniform "you hit
// a cap, here's how to upgrade" affordance regardless of which limit
// fired. plan_code is the opaque label written by the external limits
// provisioner; upgrade_url is empty when no provisioner has supplied
// one.
type LimitErrorBody struct {
	Error      string `json:"error"`      // human-readable message
	Resource   string `json:"resource"`   // "agents" | "domains" | "messages" | "storage"
	Limit      int    `json:"limit"`      // cap that was hit
	Current    int    `json:"current"`    // the user's current count for that resource
	PlanCode   string `json:"plan_code"`  // opaque label from account_limits
	UpgradeURL string `json:"upgrade_url"`// optional URL to surface in the dashboard
}

// WriteLimitError writes a 402 Payment Required response with the
// LimitErrorBody payload. Centralized here so every Check* call site in
// HTTP handlers can convert the typed error uniformly:
//
//	if err := enforcer.CheckAgentCreate(ctx, userID); err != nil {
//	    if limits.WriteLimitError(w, err) { return }
//	    // some other DB error — propagate as 5xx
//	}
//
// Returns true when err was a *LimitExceededError and a 402 was
// written; false when the error was something else (caller must handle
// it themselves, typically as 5xx).
func WriteLimitError(w http.ResponseWriter, err error) bool {
	le, ok := IsLimitExceeded(err)
	if !ok {
		return false
	}
	body := LimitErrorBody{
		Error:      le.Error(),
		Resource:   le.Resource,
		Limit:      le.Limit,
		Current:    le.Current,
		PlanCode:   le.Limits.PlanCode,
		UpgradeURL: le.Limits.UpgradeURL,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)
	_ = json.NewEncoder(w).Encode(body)
	return true
}
