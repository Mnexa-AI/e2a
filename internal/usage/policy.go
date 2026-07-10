package usage

// AccountClass classifies an account for metering/billing/analytics. It is the
// single source of truth read by the metering gate (see RecordAndCheck), kept
// orthogonal to the paid plan/tier. Mirrors the CHECK constraint in
// migrations/037_account_class.sql.
type AccountClass string

const (
	// ClassStandard is a real customer: metered, billed, counted in analytics.
	// The default for every account.
	ClassStandard AccountClass = "standard"
	// ClassInternal is internal dogfooding.
	ClassInternal AccountClass = "internal"
	// ClassSystem is synthetic-monitoring probe traffic.
	ClassSystem AccountClass = "system"
	// ClassDemo is a demo account.
	ClassDemo AccountClass = "demo"
)

// MeteringPolicy is the resolved decision for an account class. Resolved once at
// the metering gate rather than re-derived at each write path.
type MeteringPolicy struct {
	Meter     bool // record usage_events + usage_summaries (and thus count against quota)
	Bill      bool // include in billing
	Analytics bool // include in product analytics
}

// PolicyFor returns the metering policy for an account class. Only standard
// accounts are metered/billed/analyzed; every non-standard class (internal,
// system, demo) is excluded on all three axes. An unknown/empty value
// fail-closed defaults to standard so a misclassified account is never silently
// exempted from billing.
func PolicyFor(c AccountClass) MeteringPolicy {
	switch c {
	case ClassInternal, ClassSystem, ClassDemo:
		return MeteringPolicy{Meter: false, Bill: false, Analytics: false}
	default: // ClassStandard and any unrecognized value
		return MeteringPolicy{Meter: true, Bill: true, Analytics: true}
	}
}

// RateLimited reports whether an account class is subject to the in-memory
// request rate limiters (agent registration, per-user poll, per-agent send).
// Only trusted first-party traffic bypasses them: ClassSystem (synthetic
// monitoring probes) and ClassInternal (internal dogfooding + the conformance
// suite). The limiters exist to bound real-user and anonymous abuse, and those
// two classes are neither. This is deliberately NARROWER than PolicyFor's
// metering exemption: ClassDemo stays limited (it is user-facing, so a demo
// account is still an abuse surface), and any unknown/empty value fail-closes
// to limited — a misclassified account is never silently exempted from the
// limiters. The exemption follows the account (loaded at auth), so it holds
// regardless of source IP or whether the request arrives behind a proxy.
func RateLimited(c AccountClass) bool {
	switch c {
	case ClassSystem, ClassInternal:
		return false
	default: // ClassStandard, ClassDemo, and any unrecognized value
		return true
	}
}
