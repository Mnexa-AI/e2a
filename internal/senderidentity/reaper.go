package senderidentity

import (
	"context"
	"log"

	"github.com/riverqueue/river"
)

// ReapArgs drives the periodic orphan-identity sweep. No fields — it scans
// the whole provider identity list each run.
type ReapArgs struct{}

func (ReapArgs) Kind() string { return "sender_identity_reap" }

// ReapWorker is the teardown BACKSTOP (design decision 4). The primary
// teardown is the transactional DeprovisionWorker enqueued on domain/account
// delete; this sweep catches identities orphaned by edge cases (e.g. a delete
// whose job was somehow lost). It is intentionally ALERT-ONLY: a naïve
// "list then delete" races a concurrent re-registration of the same domain
// (stale snapshot deletes a freshly-created identity → silent breakage), so
// the safe default is to log orphans for an operator to inspect. The
// TOCTOU-safe conditional delete (SELECT … FOR UPDATE liveness re-confirm) is
// deferred — see the design doc.
type ReapWorker struct {
	river.WorkerDefaults[ReapArgs]
	store    Store
	provider Provider
}

func (w *ReapWorker) Work(ctx context.Context, job *river.Job[ReapArgs]) error {
	identities, err := w.provider.List(ctx)
	if err != nil {
		return err
	}
	orphans := 0
	for _, domain := range identities {
		exists, err := w.store.DomainExists(ctx, domain)
		if err != nil {
			return err
		}
		if !exists {
			orphans++
			log.Printf("[senderidentity:reaper] ALERT orphan sending identity with no live domain: %s "+
				"(provider identity exists but no domain row) — manual review required", domain)
		}
	}
	if orphans > 0 {
		log.Printf("[senderidentity:reaper] swept %d provider identities, %d orphan(s) flagged", len(identities), orphans)
	}
	return nil
}
