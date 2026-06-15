package senderidentity

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// fakeRawStore implements RawStore, recording the primitive (string/JSON) form
// SetSendingStatus receives so the adapter's conversion can be asserted.
type fakeRawStore struct {
	lastStatus      string
	lastErrMsg      string
	lastRecordsJSON []byte
	getStatusReturn string
}

func (f *fakeRawStore) SendingProvisionInputs(ctx context.Context, domain string) (string, []byte, bool, error) {
	return "sel", []byte("der"), true, nil
}

func (f *fakeRawStore) SetSendingStatus(ctx context.Context, domain, status, errMsg string, recordsJSON []byte) error {
	f.lastStatus = status
	f.lastErrMsg = errMsg
	f.lastRecordsJSON = recordsJSON
	return nil
}

func (f *fakeRawStore) TouchSendingChecked(ctx context.Context, domain string) error { return nil }

func (f *fakeRawStore) GetSendingStatus(ctx context.Context, domain string) (string, error) {
	return f.getStatusReturn, nil
}

func (f *fakeRawStore) DomainOwner(ctx context.Context, domain string) (string, error) {
	return "user_1", nil
}

func (f *fakeRawStore) DomainExists(ctx context.Context, domain string) (bool, error) {
	return true, nil
}

func TestStoreAdapter_SetSendingStatus(t *testing.T) {
	raw := &fakeRawStore{}
	store := NewStoreAdapter(raw)
	records := []DNSRecord{{Type: "TXT", Name: "_dmarc", Value: "v=DMARC1"}}

	if err := store.SetSendingStatus(context.Background(), "example.com", StatusVerified, "ok", records); err != nil {
		t.Fatalf("SetSendingStatus: %v", err)
	}
	if raw.lastStatus != "verified" {
		t.Fatalf("status not converted to string: got %q", raw.lastStatus)
	}
	if raw.lastErrMsg != "ok" {
		t.Fatalf("errMsg = %q", raw.lastErrMsg)
	}
	var gotRecords []DNSRecord
	if err := json.Unmarshal(raw.lastRecordsJSON, &gotRecords); err != nil {
		t.Fatalf("records not valid JSON: %v", err)
	}
	if !reflect.DeepEqual(gotRecords, records) {
		t.Fatalf("records round-trip mismatch: got %+v want %+v", gotRecords, records)
	}
}

func TestStoreAdapter_SetSendingStatus_NoRecords(t *testing.T) {
	raw := &fakeRawStore{}
	store := NewStoreAdapter(raw)
	if err := store.SetSendingStatus(context.Background(), "example.com", StatusPending, "", nil); err != nil {
		t.Fatalf("SetSendingStatus: %v", err)
	}
	if raw.lastRecordsJSON != nil {
		t.Fatalf("expected nil records JSON for empty records, got %q", raw.lastRecordsJSON)
	}
}

func TestStoreAdapter_GetSendingStatus(t *testing.T) {
	raw := &fakeRawStore{getStatusReturn: "pending"}
	store := NewStoreAdapter(raw)
	got, err := store.GetSendingStatus(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("GetSendingStatus: %v", err)
	}
	if got != StatusPending {
		t.Fatalf("string→Status conversion failed: got %q", got)
	}
}
