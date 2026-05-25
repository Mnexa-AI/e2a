package agent

// Test seams: re-export unexported helpers we want to unit-test from
// the external `agent_test` package without widening the public API.

import (
	"net/http"

	"github.com/Mnexa-AI/e2a/internal/outbound"
)

// IsSelfSendForTest is the agent_test-package handle for isSelfSend.
// Pure function over (request, agent email); see selfsend.go for the
// behavioral contract. Renaming the public name is intentional — the
// "ForTest" suffix marks this as a test-only escape hatch so a future
// reader doesn't reach for it from production code.
func IsSelfSendForTest(req outbound.SendRequest, agentEmail string) bool {
	return isSelfSend(req, agentEmail)
}

// AuthenticateUserForTest exposes API.authenticateUser so external
// test helpers can construct synthetic guard-protected handlers
// without re-implementing the bearer-token plumbing.
func (a *API) AuthenticateUserForTest(r *http.Request) (userID string, err error) {
	u, err := a.authenticateUser(r)
	if err != nil {
		return "", err
	}
	return u.ID, nil
}

// IdempotencyGuardForTest exposes API.idempotencyGuard so tests in the
// external agent_test package can verify the side-effect-committed
// caching policy end-to-end (the standard handlers don't have a
// natural way to reach the "5xx after side effect" branch in a unit
// test).
func (a *API) IdempotencyGuardForTest(w http.ResponseWriter, r *http.Request, userID string, bodyBytes []byte) (replayed bool, out http.ResponseWriter, finalize func()) {
	return a.idempotencyGuard(w, r, userID, bodyBytes)
}

// MarkSideEffectCommittedForTest exposes markSideEffectCommitted so
// the external test harness can flip the cache-on-error flag from
// inside a synthetic handler.
func MarkSideEffectCommittedForTest(w http.ResponseWriter) {
	markSideEffectCommitted(w)
}
