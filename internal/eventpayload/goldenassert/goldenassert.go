// Package goldenassert is the shared TEST helper for the golden-payload
// fixtures under internal/eventpayload/testdata — the cross-channel drift
// lock for the stable event `data` payloads.
//
// Each fixture file is a full wire envelope {type,id,schema_version,
// created_at,data}. The canonical envelope-level lock lives in
// internal/eventpayload's own golden test; the per-builder tests (relay,
// agent, delivery, ws) call Data to assert that THEIR builder's marshaled
// `data` is byte-identical to the fixture's `data` subdocument. The TS and
// Python SDK payload tests parse the very same files, so a server-side field
// rename that would break an SDK type fails both sides against one artifact.
package goldenassert

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// Data asserts that json.Marshal(got) is byte-identical to the (compacted)
// `data` subdocument of the fixture at path.
func Data(t *testing.T, path string, got any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden fixture %s: %v (regenerate with `go test ./internal/eventpayload -run TestGoldenFixtures -update`)", path, err)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("parse golden fixture %s: %v", path, err)
	}
	var want bytes.Buffer
	if err := json.Compact(&want, envelope.Data); err != nil {
		t.Fatalf("compact fixture data %s: %v", path, err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if !bytes.Equal(gotJSON, want.Bytes()) {
		t.Errorf("payload drifted from golden fixture %s\n got: %s\nwant: %s", path, gotJSON, want.Bytes())
	}
}
