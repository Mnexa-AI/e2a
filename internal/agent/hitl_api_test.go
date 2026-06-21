package agent_test

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// enableHITL is a tiny helper that enables HITL on an existing agent using
// default TTL + reject-on-expiry.
func enableHITL(t *testing.T, store *identity.Store, agentID, userID string) {
	t.Helper()
	if err := store.UpdateAgentHITL(
		context.Background(), agentID, userID,
		identity.HITLDefaultTTLSeconds, identity.HITLExpirationReject,
	); err != nil {
		t.Fatalf("UpdateAgentHITL: %v", err)
	}
}
