package jobs

import "github.com/riverqueue/river"

// Named queues. Jobs are assigned a queue at enqueue time (InsertOpts.Queue);
// the shared client works all of these. Separate queues give INDEPENDENT
// concurrency per lane, so a backlog in one (e.g. a slow customer webhook
// endpoint) can never starve another (e.g. outbound sends) — the isolation the
// hand-rolled queues couldn't express cleanly. Keep these names stable: they are
// persisted on river_job rows.
const (
	// QueueOutbound carries outbound send jobs (API → SES).
	QueueOutbound = "outbound"
	// QueueWebhook carries customer webhook-delivery jobs. Isolated from outbound
	// so a slow/failing endpoint's backlog never delays sends.
	QueueWebhook = "webhook"
	// QueueMaintenance carries low-urgency periodic/janitor work (reapers,
	// hold-TTL resolution, auto-disable sweeps).
	QueueMaintenance = "maintenance"
	// QueueDefault is River's built-in default — anything not explicitly routed.
	QueueDefault = river.QueueDefault
)

// defaultQueueConfig is the concurrency map the shared client works. Per-lane
// MaxWorkers is deliberately generous for I/O-bound work (SMTP / HTTP); tune
// against real throughput. Every queue a domain enqueues into MUST appear here or
// its jobs sit unworked.
func defaultQueueConfig(outbound, webhook, maintenance, deflt int) map[string]river.QueueConfig {
	return map[string]river.QueueConfig{
		QueueOutbound:    {MaxWorkers: outbound},
		QueueWebhook:     {MaxWorkers: webhook},
		QueueMaintenance: {MaxWorkers: maintenance},
		QueueDefault:     {MaxWorkers: deflt},
	}
}
