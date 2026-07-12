package httpapi

// DeleteConfirm is the uniform destructive-delete guard embedded in every
// DELETE input across the /v1 surface (agents, domains, account, account
// suppressions, api-keys, templates, webhooks).
//
// Modeling it as a required query param with a single-value enum makes Huma
// validate it declaratively: a request missing `confirm` — or carrying any
// value other than the literal "DELETE" — is rejected with 422 by the
// framework before the handler runs, and the constraint surfaces in the
// generated OpenAPI (required + enum: [DELETE]) so SDK/CLI codegen and the API
// reference show it. The guard exists to stop accidental raw-HTTP deletes; the
// hand-written SDK/CLI `delete(...)` layers auto-send `confirm=DELETE`, so
// explicit `.delete()` calls are never burdened with it.
//
// This replaces the previous per-endpoint optional-string `confirm` that was
// checked in the handler and returned 400 confirmation_required — that
// enforcement is now owned by Huma (422) so the gate is declared once and is
// identical on every delete op.
type DeleteConfirm struct {
	Confirm string `query:"confirm" enum:"DELETE" required:"true" doc:"Must be the literal DELETE — this action is irreversible."`
}
