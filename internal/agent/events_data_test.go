package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// B3 (review correctness bug): EventJSON.data is `required` + `type: object` in
// the spec, but a stored envelope whose `data` is JSON null (or absent) parses
// successfully with a nil map, so the response serializes `"data": null` —
// violating the contract and breaking strict SDK deserializers. Drive the real
// read paths (the exported wrappers the /v1 handlers use) against a seeded
// null-data event.
func TestEventData_NeverNull_GetAndList(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	store := identity.NewStore(pool)

	user, err := store.CreateOrGetUser(ctx, "ev-null@example.com", "Owner", "g-ev-null")
	if err != nil {
		t.Fatalf("CreateOrGetUser: %v", err)
	}
	eventID := "evt_b3_null"
	// A valid envelope whose `data` is explicitly null — unmarshals fine, so the
	// "tolerate malformed" branch does NOT fire; env.Data stays nil.
	_, err = pool.Exec(ctx,
		`INSERT INTO webhook_events
		   (id, user_id, type, aud, envelope, schema_version, status)
		 VALUES ($1, $2, 'email.received', 'webhook', $3, 1, 'pending')`,
		eventID, user.ID, []byte(`{"type":"email.received","data":null}`),
	)
	if err != nil {
		t.Fatalf("seed webhook_events: %v", err)
	}

	ev, err := agent.GetEventForUser(ctx, pool, user.ID, eventID)
	if err != nil {
		t.Fatalf("GetEventForUser: %v", err)
	}
	if ev.Data == nil {
		t.Errorf("getEvent: Data is nil → serializes as JSON `null`, violating required `data: object`")
	}

	evs, err := agent.ListEventsForUser(ctx, pool, user.ID, "", "", "", "", nil, nil, time.Time{}, "", 50)
	if err != nil {
		t.Fatalf("ListEventsForUser: %v", err)
	}
	var found bool
	for i := range evs {
		if evs[i].ID == eventID {
			found = true
			if evs[i].Data == nil {
				t.Errorf("listEvents: Data is nil → serializes as JSON `null`, violating required `data: object`")
			}
		}
	}
	if !found {
		t.Fatalf("seeded event %s not returned by listEvents", eventID)
	}
}
