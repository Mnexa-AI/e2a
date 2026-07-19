// Package telemetry defines the metrics interface for the e2a backend.
// Implementations:
//
//   * NoOp — production default until an operator wires a real backend.
//   * Log  — structured log emitter; cheap; aggregator-friendly.
//   * (future) Prometheus, OTLP, statsd, etc.
//
// The interface is small by design. Call sites should depend on
// telemetry.Metrics, not on a concrete backend. To swap backends,
// change the constructor at the cmd/e2a/main.go wiring; nothing else
// moves.
package telemetry

import (
	"log"
	"sync/atomic"
)

// Metrics is the observability surface for the slice 10 design.
// Counter-style methods record a discrete event; SetPublisherLag is a
// gauge that should be set on each tick.
//
// Stable across implementations. Adding a new metric is additive (add
// a method, default it to a no-op on existing implementations).
type Metrics interface {
	// OutboxEventsPublished is incremented each time PublishTx or
	// PublishBestEffortTx successfully writes a webhook_events row.
	OutboxEventsPublished(eventType string)

	// OutboxEventsFanOut is incremented each time the worker finishes
	// fanning an event out to its matched webhooks. matched is the
	// number of webhook_subscriber_deliveries rows written.
	OutboxEventsFanOut(eventType string, matched int)

	// OutboxEventsNoMatch is incremented each time the worker
	// transitions an event to status='no_match' because zero
	// subscribers matched. Useful for spotting "why didn't my
	// webhook fire?"
	OutboxEventsNoMatch(eventType string)

	// OutboxFailures is incremented on any outbox failure — worker-side
	// (stage in {"lease", "list_webhooks", "insert_delivery", "update_status"})
	// or emit-side when a producer's PublishTx fails and an event is
	// DROPPED (stage "publish"). A non-zero "publish" rate means contract
	// events are silently missing from the log — alert on it.
	OutboxFailures(stage string)

	// RedeliverRequests is incremented on each customer-driven replay.
	// scope in {"single", "since"}.
	RedeliverRequests(scope string)

	// JanitorRowsDeleted is incremented by the cleanup tick.
	// table in {"webhook_events", "webhook_subscriber_deliveries", "webhook_deliveries", "messages", "user_sessions", "oauth"}.
	JanitorRowsDeleted(table string, count int)

	// NotifyMissed is incremented when the 1-second fallback poll
	// finds work that LISTEN/NOTIFY didn't wake us for. A non-zero
	// rate indicates reconnect churn or a dropped notification.
	NotifyMissed()

	// SetPublisherLag is a gauge: the age in seconds of the oldest
	// pending webhook_events row. Should be set on every Tick. Alert
	// if it stays > 30s.
	SetPublisherLag(seconds float64)
}

// NoOp swallows every call. Default for tests that don't care.
type NoOp struct{}

func (NoOp) OutboxEventsPublished(string)         {}
func (NoOp) OutboxEventsFanOut(string, int)       {}
func (NoOp) OutboxEventsNoMatch(string)           {}
func (NoOp) OutboxFailures(string)                {}
func (NoOp) RedeliverRequests(string)             {}
func (NoOp) JanitorRowsDeleted(string, int)       {}
func (NoOp) NotifyMissed()                        {}
func (NoOp) SetPublisherLag(float64)              {}

// Log emits a structured log line for every metric call. Cheap and
// portable; production aggregators (Loki, CloudWatch, Datadog) can
// build counters and gauges from these directly.
//
// All lines share the [metrics] prefix so they're easy to filter.
// Format is key=value space-separated, which both jq and Splunk parse
// natively.
type Log struct {
	// inflightPublisherLag is set by SetPublisherLag and emitted
	// every N calls (currently 60 — once a minute at the worker's
	// 1s poll cadence). Saves log volume.
	calls atomic.Int64
}

func NewLog() *Log { return &Log{} }

func (l *Log) OutboxEventsPublished(eventType string) {
	log.Printf("[metrics] event=outbox.published type=%s", eventType)
}

func (l *Log) OutboxEventsFanOut(eventType string, matched int) {
	log.Printf("[metrics] event=outbox.fanout type=%s matched=%d", eventType, matched)
}

func (l *Log) OutboxEventsNoMatch(eventType string) {
	log.Printf("[metrics] event=outbox.no_match type=%s", eventType)
}

func (l *Log) OutboxFailures(stage string) {
	log.Printf("[metrics] event=outbox.failure stage=%s", stage)
}

func (l *Log) RedeliverRequests(scope string) {
	log.Printf("[metrics] event=redeliver.request scope=%s", scope)
}

func (l *Log) JanitorRowsDeleted(table string, count int) {
	if count == 0 {
		return // skip noise when nothing was cleaned
	}
	log.Printf("[metrics] event=janitor.delete table=%s count=%d", table, count)
}

func (l *Log) NotifyMissed() {
	log.Printf("[metrics] event=notify.missed")
}

func (l *Log) SetPublisherLag(seconds float64) {
	// Rate-limit: emit every 60th call (~once a minute at 1s poll).
	n := l.calls.Add(1)
	if n%60 == 0 || seconds > 30 {
		log.Printf("[metrics] gauge=publisher.lag_seconds value=%.2f", seconds)
	}
}

// Compile guard.
var _ Metrics = NoOp{}
var _ Metrics = (*Log)(nil)
