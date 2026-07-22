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
	"sort"
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

// DataIgnoringLifecycle asserts the non-ledger portion of a producer payload.
// Pure builder tests use it because they do not own persistence and therefore
// must never be fed fixture-derived transitions. DB-backed producer tests lock
// the exact persisted rows separately.
func DataIgnoringLifecycle(t *testing.T, path string, got any, extraFields ...string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden fixture %s: %v", path, err)
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("parse golden fixture %s: %v", path, err)
	}
	delete(envelope.Data, "lifecycle_transitions")
	for _, field := range extraFields {
		delete(envelope.Data, field)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var gotData map[string]any
	if err := json.Unmarshal(gotJSON, &gotData); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	delete(gotData, "lifecycle_transitions")
	for _, field := range extraFields {
		delete(gotData, field)
	}
	wantJSON, _ := json.Marshal(envelope.Data)
	gotJSON, _ = json.Marshal(gotData)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Errorf("payload drifted from golden fixture %s (excluding lifecycle_transitions)\n got: %s\nwant: %s", path, gotJSON, wantJSON)
	}
}

// Lifecycle compares lifecycle rows emitted by a real producer to the static
// fixture, normalizing only database-generated identity and observation-time
// fields. Evidence, correlations, recipient, taxonomy, retryability, and
// presence remain byte-for-byte locked to the fixture. Rows are matched by
// reason code because equal occurred_at values use generated ids as the
// deterministic production tie-breaker; DB ordering tests lock that rule.
func Lifecycle(t *testing.T, path string, got any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden fixture %s: %v", path, err)
	}
	var envelope struct {
		Data struct {
			Lifecycle json.RawMessage `json:"lifecycle_transitions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("parse golden fixture %s: %v", path, err)
	}
	var want, actual []map[string]any
	if err := json.Unmarshal(envelope.Data.Lifecycle, &want); err != nil {
		t.Fatalf("decode fixture lifecycle %s: %v", path, err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal actual lifecycle: %v", err)
	}
	if err := json.Unmarshal(gotJSON, &actual); err != nil {
		t.Fatalf("decode actual lifecycle: %v", err)
	}
	if len(actual) != len(want) {
		t.Fatalf("lifecycle count = %d, fixture %s has %d", len(actual), path, len(want))
	}
	byReason := func(rows []map[string]any) {
		sort.Slice(rows, func(i, j int) bool {
			return rows[i]["reason_code"].(string) < rows[j]["reason_code"].(string)
		})
	}
	byReason(want)
	byReason(actual)
	for i := range actual {
		for _, field := range []string{"id", "message_id", "occurred_at"} {
			actual[i][field] = want[i][field]
		}
	}
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ = json.Marshal(actual)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Errorf("real producer lifecycle drifted from golden fixture %s\n got: %s\nwant: %s", path, gotJSON, wantJSON)
	}
}
