package identity_test

import (
	"context"
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
	"github.com/jackc/pgx/v5"
)

// TestInboundIntake_InsertLoadDedup covers the accept-side surface: insert reports
// whether a row landed, the dedup key (recipient, message_id, content_hash)
// suppresses a re-send (inserted=false, no error), and load round-trips the raw +
// facts / returns (nil,nil) when gone.
func TestInboundIntake_InsertLoadDedup(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	raw := []byte("From: a@b.test\r\nTo: bot@x.test\r\nSubject: hi\r\n\r\nbody")
	insert := func(iid, recipient, msgID, hash string) bool {
		var inserted bool
		if err := store.WithTx(ctx, func(tx pgx.Tx) error {
			var e error
			inserted, e = store.InsertInboundIntakeTx(ctx, tx, iid, recipient, "a@b.test", "1.2.3.4", msgID, hash, raw)
			return e
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
		return inserted
	}

	first := identity.NewInboundIntakeID()
	if !insert(first, "bot@x.test", "<m1@b.test>", "hash1") {
		t.Fatal("first insert should report inserted=true")
	}
	if insert(identity.NewInboundIntakeID(), "bot@x.test", "<m1@b.test>", "hash1") {
		t.Fatal("duplicate (same recipient+message_id+content_hash) must report inserted=false")
	}
	if !insert(identity.NewInboundIntakeID(), "bot@x.test", "<m1@b.test>", "hash2") {
		t.Fatal("different content_hash must insert")
	}
	if !insert(identity.NewInboundIntakeID(), "other@x.test", "<m1@b.test>", "hash1") {
		t.Fatal("different recipient must insert")
	}

	it, err := store.LoadInboundIntake(ctx, first)
	if err != nil || it == nil {
		t.Fatalf("load: err=%v it=%v", err, it)
	}
	if it.Recipient != "bot@x.test" || it.MessageID != "<m1@b.test>" || it.ContentHash != "hash1" ||
		string(it.Raw) != string(raw) || it.Status != identity.IntakeStatusAccepted || it.RemoteIP != "1.2.3.4" {
		t.Fatalf("loaded intake mismatch: %+v", it)
	}
	miss, err := store.LoadInboundIntake(ctx, "intk_missing")
	if err != nil || miss != nil {
		t.Fatalf("missing load should be (nil,nil), got it=%v err=%v", miss, err)
	}
}

// TestInboundIntake_StampProcessAndFail covers the worker-side terminal transitions:
// stamp the job id in the accept-tx, flip to processed + link the messages row, and
// the failed terminal path with a diagnostic detail.
func TestInboundIntake_StampProcessAndFail(t *testing.T) {
	pool := testutil.TestDB(t)
	store := identity.NewStore(pool)
	ctx := context.Background()

	id := identity.NewInboundIntakeID()
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		_, e := store.InsertInboundIntakeTx(ctx, tx, id, "bot@x.test", "a@b.test", "1.2.3.4", "<m@b.test>", "h", []byte("raw"))
		return e
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		return store.StampInboundIntakeJobIDTx(ctx, tx, id, 4242)
	}); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	var jobID int64
	if err := pool.QueryRow(ctx, `SELECT process_job_id FROM inbound_intake WHERE id=$1`, id).Scan(&jobID); err != nil {
		t.Fatalf("read job id: %v", err)
	}
	if jobID != 4242 {
		t.Fatalf("job id = %d, want 4242", jobID)
	}

	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		return store.MarkInboundIntakeProcessedTx(ctx, tx, id, "msg_result123")
	}); err != nil {
		t.Fatalf("mark processed: %v", err)
	}
	var status, fk string
	if err := pool.QueryRow(ctx, `SELECT status, message_fk FROM inbound_intake WHERE id=$1`, id).Scan(&status, &fk); err != nil {
		t.Fatalf("read processed: %v", err)
	}
	if status != identity.IntakeStatusProcessed || fk != "msg_result123" {
		t.Fatalf("after process: status=%q fk=%q", status, fk)
	}

	id2 := identity.NewInboundIntakeID()
	if err := store.WithTx(ctx, func(tx pgx.Tx) error {
		_, e := store.InsertInboundIntakeTx(ctx, tx, id2, "bot@x.test", "a@b.test", "1.2.3.4", "<m2@b.test>", "h2", []byte("raw2"))
		return e
	}); err != nil {
		t.Fatalf("insert 2: %v", err)
	}
	if err := store.MarkInboundIntakeFailed(ctx, id2, "unparseable MIME"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	var s2, d2 string
	if err := pool.QueryRow(ctx, `SELECT status, detail FROM inbound_intake WHERE id=$1`, id2).Scan(&s2, &d2); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if s2 != identity.IntakeStatusFailed || d2 != "unparseable MIME" {
		t.Fatalf("after fail: status=%q detail=%q", s2, d2)
	}
}
