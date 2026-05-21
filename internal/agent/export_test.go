package agent

// Test seams: re-export unexported helpers we want to unit-test from
// the external `agent_test` package without widening the public API.

import "github.com/Mnexa-AI/e2a/internal/outbound"

// IsSelfSendForTest is the agent_test-package handle for isSelfSend.
// Pure function over (request, agent email); see selfsend.go for the
// behavioral contract. Renaming the public name is intentional — the
// "ForTest" suffix marks this as a test-only escape hatch so a future
// reader doesn't reach for it from production code.
func IsSelfSendForTest(req outbound.SendRequest, agentEmail string) bool {
	return isSelfSend(req, agentEmail)
}
