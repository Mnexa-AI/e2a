package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/riverqueue/river"

	"github.com/Mnexa-AI/e2a/internal/jobs"
	"github.com/Mnexa-AI/e2a/internal/testutil"
)

// Compile-time proof the shared client is an Enqueuer (so business code can
// depend on the narrow interface and River satisfies it with no adapter).
var _ jobs.Enqueuer = (*jobs.Client)(nil)

// pingArgs is a trivial job used to prove the composition root assembles a
// working client end-to-end.
type pingArgs struct {
	ID string `json:"id"`
}

func (pingArgs) Kind() string { return "jobs_test_ping" }

type pingWorker struct {
	river.WorkerDefaults[pingArgs]
	got chan string
}

func (w *pingWorker) Work(_ context.Context, job *river.Job[pingArgs]) error {
	w.got <- job.Args.ID
	return nil
}

// pingRegistrar contributes the ping worker to the shared client.
type pingRegistrar struct{ w *pingWorker }

func (r pingRegistrar) RegisterJobs(workers *river.Workers) []*river.PeriodicJob {
	river.AddWorker(workers, r.w)
	return nil
}

// TestClient_EndToEnd proves New assembles a client from a Registrar, that the
// Enqueuer inserts a job, and that a registered worker runs it on its queue.
func TestClient_EndToEnd(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	if err := jobs.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	got := make(chan string, 1)
	client, err := jobs.New(pool, jobs.Config{}, pingRegistrar{w: &pingWorker{got: got}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Stop(ctx)

	// Enqueue through the narrow Enqueuer surface, onto the outbound queue.
	if _, err := client.Insert(ctx, pingArgs{ID: "hello"}, &river.InsertOpts{Queue: jobs.QueueOutbound}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	select {
	case id := <-got:
		if id != "hello" {
			t.Errorf("worked job id = %q, want hello", id)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the job to be worked")
	}
}

// TestConfig_Defaults confirms the per-queue worker pools fall back to sane
// non-zero sizes (a zero MaxWorkers would silently never work a queue).
func TestConfig_Defaults(t *testing.T) {
	pool := testutil.TestDB(t)
	ctx := context.Background()
	if err := jobs.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// New with a zero Config must build without error (defaults applied).
	if _, err := jobs.New(pool, jobs.Config{}); err != nil {
		t.Fatalf("New with zero config: %v", err)
	}
}
