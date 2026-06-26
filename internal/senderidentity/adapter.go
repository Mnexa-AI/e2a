package senderidentity

import (
	"context"
	"encoding/json"
)

// RawStore is the primitive persistence surface implemented by
// *identity.Store. It deliberately speaks plain strings / JSON bytes so the
// core identity package does NOT import senderidentity (and thus does not pull
// River + the AWS SDK into its dependency graph). NewStoreAdapter wraps it
// into the typed Store the workers consume.
type RawStore interface {
	SendingProvisionInputs(ctx context.Context, domain string) (selector string, privateKeyDER []byte, ok bool, err error)
	SetSendingStatus(ctx context.Context, domain, status, dkimStatus, mailFromStatus, errMsg string, recordsJSON []byte) error
	TouchSendingChecked(ctx context.Context, domain string) error
	GetSendingStatus(ctx context.Context, domain string) (string, error)
	DomainOwner(ctx context.Context, domain string) (string, error)
	DomainExists(ctx context.Context, domain string) (bool, error)
}

// NewStoreAdapter bridges a RawStore (e.g. *identity.Store) to the typed
// Store the workers use, converting Status ↔ string and DNSRecord ↔ JSON.
func NewStoreAdapter(raw RawStore) Store { return &storeAdapter{raw: raw} }

type storeAdapter struct{ raw RawStore }

func (a *storeAdapter) SendingProvisionInputs(ctx context.Context, domain string) (string, []byte, bool, error) {
	return a.raw.SendingProvisionInputs(ctx, domain)
}

func (a *storeAdapter) SetSendingStatus(ctx context.Context, domain string, status, dkimStatus, mailFromStatus Status, errMsg string, records []DNSRecord) error {
	var recordsJSON []byte
	if len(records) > 0 {
		b, err := json.Marshal(records)
		if err != nil {
			return err
		}
		recordsJSON = b
	}
	return a.raw.SetSendingStatus(ctx, domain, string(status), string(dkimStatus), string(mailFromStatus), errMsg, recordsJSON)
}

func (a *storeAdapter) TouchSendingChecked(ctx context.Context, domain string) error {
	return a.raw.TouchSendingChecked(ctx, domain)
}

func (a *storeAdapter) GetSendingStatus(ctx context.Context, domain string) (Status, error) {
	s, err := a.raw.GetSendingStatus(ctx, domain)
	if err != nil {
		return "", err
	}
	return Status(s), nil
}

func (a *storeAdapter) DomainOwner(ctx context.Context, domain string) (string, error) {
	return a.raw.DomainOwner(ctx, domain)
}

func (a *storeAdapter) DomainExists(ctx context.Context, domain string) (bool, error) {
	return a.raw.DomainExists(ctx, domain)
}
