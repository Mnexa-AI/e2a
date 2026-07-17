package testutil

import (
	"context"
	"testing"
	"time"
)

func TestTruncateAll_CleansInboundIntakeWithoutExclusiveTableLock(t *testing.T) {
	pool := TestDB(t)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		INSERT INTO inbound_intake (id, recipient, raw_message, content_hash)
		VALUES ('intk_cleanup_lock', 'agent@example.com', 'raw', 'hash')
	`)
	if err != nil {
		t.Fatalf("seed inbound_intake: %v", err)
	}

	reader, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin reader transaction: %v", err)
	}
	defer reader.Rollback(ctx) //nolint:errcheck

	var before int
	if err := reader.QueryRow(ctx, `SELECT count(*) FROM inbound_intake`).Scan(&before); err != nil {
		t.Fatalf("read inbound_intake: %v", err)
	}
	if before != 1 {
		t.Fatalf("inbound_intake rows before cleanup = %d, want 1", before)
	}

	cleanupCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := truncateAll(cleanupCtx, pool); err != nil {
		t.Fatalf("cleanup should not require exclusive access to inbound_intake: %v", err)
	}

	if err := reader.Rollback(ctx); err != nil {
		t.Fatalf("rollback reader transaction: %v", err)
	}

	var after int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbound_intake`).Scan(&after); err != nil {
		t.Fatalf("count inbound_intake after cleanup: %v", err)
	}
	if after != 0 {
		t.Fatalf("inbound_intake rows after cleanup = %d, want 0", after)
	}
}
