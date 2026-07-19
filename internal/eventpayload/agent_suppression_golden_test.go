package eventpayload_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tokencanopy/e2a/internal/eventpayload"
	"github.com/tokencanopy/e2a/internal/webhookpub"
)

func TestAgentSuppressionAddedGoldenFixture(t *testing.T) {
	e := webhookpub.Event{
		ID: "evt_0123456789abcdef0123456789abcdef", Type: webhookpub.EventAgentSuppressionAdded,
		CreatedAt: fixtureCreatedAt,
		Data:      eventpayload.AgentSuppressionAddedData{AgentEmail: "support@agents.example.com", Address: "alice@customer.example.com", Source: "unsubscribe"},
	}
	got, err := json.MarshalIndent(e.AsEnvelope(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	want, err := os.ReadFile(filepath.Join("testdata", "agent.suppression_added.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("fixture drifted\n got: %s\nwant: %s", got, want)
	}
}
