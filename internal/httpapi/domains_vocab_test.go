package httpapi

import (
	"sort"
	"strings"
	"testing"

	"github.com/tokencanopy/e2a/internal/identity"
)

// The domain resource deliberately speaks TWO record-state vocabularies, one
// per axis:
//
//   - PERSISTED domain state — DomainView.dns_records[].status: what the
//     platform has recorded about each record. Emitted values today:
//     verified / pending / failed ("missing" is documented for forward
//     compatibility but not currently emitted).
//   - LIVE PROBE outcome — VerifyDomainView.mx/spf/dkim from
//     POST /v1/domains/{domain}/verify: what DNS returned on THIS attempt.
//     Values: found / missing / deferred / mismatch (see
//     internal/agent.checkDomainRecords + classifyDKIM, pinned in
//     internal/agent/dns_probe_vocab_test.go).
//
// A developer polling verify and then GET sees different words for the same
// records ON PURPOSE: a probe answers "what did DNS return just now", the
// persisted status answers "what has the platform recorded". "Harmonizing"
// one vocabulary into the other (e.g. mapping the probe's found→verified, or
// emitting found from dns_records[].status) would silently change the public
// contract — these tests exist to make that a loud failure instead.

// persistedStatusVocab is the exact set dns_records[].status emits today.
var persistedStatusVocab = []string{"failed", "pending", "verified"}

// probeOnlyVocab are the live-probe words that must NEVER appear in the
// persisted axis ("missing" is shared: documented on the persisted axis and
// emitted by the probe axis, so it is not a convergence signal by itself).
var probeOnlyVocab = map[string]bool{"found": true, "deferred": true, "mismatch": true}

// TestDNSRecordStatusVocabularyPinned sweeps domainView — the ONLY constructor
// of DomainView.dns_records — across every persisted input state (verified
// flag × sending_status rollup × per-axis DKIM/MAIL FROM statuses, including
// empty and unknown values) and pins the union of emitted statuses to exactly
// {failed, pending, verified}.
//
// If this fails because you added a genuinely new persisted state: update the
// pin AND the DNSRecord.Status doc tag. If it fails because a probe word
// (found/deferred/mismatch) leaked in: stop — you are collapsing the two axes
// described above; keep the vocabularies distinct.
func TestDNSRecordStatusVocabularyPinned(t *testing.T) {
	s := New(Deps{SMTPDomain: "mx.e2a.test", SESRegion: "us-east-1"})

	axisValues := []string{"", "none", "pending", "verified", "failed", "not_a_known_status"}
	got := map[string]bool{}
	for _, verified := range []bool{false, true} {
		for _, sending := range axisValues {
			for _, dkimAxis := range axisValues {
				for _, mailFromAxis := range axisValues {
					d := &identity.Domain{
						Domain:                "example.com",
						Verified:              verified,
						VerificationToken:     "e2a-verify-token",
						DKIMSelector:          "e2a",
						DKIMPublicKey:         "MIIBIjANBgkqAQAB",
						SendingStatus:         sending,
						SendingDkimStatus:     dkimAxis,
						SendingMailFromStatus: mailFromAxis,
					}
					view := s.domainView(d)
					if len(view.DNSRecords) != 6 {
						t.Fatalf("expected all 6 purpose-tagged records (ownership, inbound_mx, inbound_mx_wildcard, dkim, mail_from_mx, mail_from_spf), got %d", len(view.DNSRecords))
					}
					for _, r := range view.DNSRecords {
						got[r.Status] = true
					}
				}
			}
		}
	}

	var emitted []string
	for v := range got {
		emitted = append(emitted, v)
	}
	sort.Strings(emitted)
	if !enumEqual(emitted, persistedStatusVocab) {
		t.Errorf("dns_records[].status emitted vocabulary drifted:\n  got:  %v\n  want: %v\nPersisted state and live probe outcome are deliberately distinct axes — do not merge the verify-probe vocabulary (found/missing/deferred/mismatch) into the persisted one (or vice versa).", emitted, persistedStatusVocab)
	}
	for v := range got {
		if probeOnlyVocab[v] {
			t.Errorf("dns_records[].status emitted probe-vocabulary value %q — the persisted axis must never speak probe words (found/deferred/mismatch)", v)
		}
	}
}

// TestSendingRecordStatusMappingPinned pins the rollup→per-record mapping for
// the sending records: verified and failed pass through, everything else
// (none, pending, empty, unknown) reads as pending. This is the open-set
// fallback that keeps a new upstream sending status from leaking an
// undocumented word into dns_records[].status.
func TestSendingRecordStatusMappingPinned(t *testing.T) {
	cases := map[string]string{
		"verified": "verified",
		"failed":   "failed",
		"none":     "pending",
		"pending":  "pending",
		"":         "pending",
		"found":    "pending", // probe word in: persisted word out — never passed through
	}
	for in, want := range cases {
		if got := sendingRecordStatus(in); got != want {
			t.Errorf("sendingRecordStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDomainVocabularyDocumentedInSpec asserts the generated OpenAPI document
// keeps the two-axes contract in the field descriptions: the persisted status
// names its axis and points at the probe vocabulary, and each probe field
// (mx, spf, dkim) names its axis, its values, and points back at
// dns_records[].status. This is what tells an SDK consumer that seeing
// "found" from verify and "pending" from GET for the same record is
// intentional, not a bug.
func TestDomainVocabularyDocumentedInSpec(t *testing.T) {
	doc := renderSpec(t)

	desc := func(schema, prop string) string {
		t.Helper()
		schemas, _ := doc["components"].(map[string]any)["schemas"].(map[string]any)
		sc, _ := schemas[schema].(map[string]any)
		if sc == nil {
			t.Fatalf("schema %q not found in spec", schema)
		}
		props, _ := sc["properties"].(map[string]any)
		p, _ := props[prop].(map[string]any)
		if p == nil {
			t.Fatalf("property %s.%s not found in spec", schema, prop)
		}
		d, _ := p["description"].(string)
		return d
	}

	requireContains := func(schema, prop, text, why string) {
		t.Helper()
		if d := desc(schema, prop); !strings.Contains(d, text) {
			t.Errorf("%s.%s description must contain %q (%s); got %q", schema, prop, text, why, d)
		}
	}

	// Persisted axis: names itself and cross-points at the probe vocabulary.
	requireContains("DNSRecord", "status", "Persisted verification state", "axis statement")
	requireContains("DNSRecord", "status", "verified", "known values")
	requireContains("DNSRecord", "status", "found/missing/deferred/mismatch", "pointer to the probe vocabulary")
	requireContains("DNSRecord", "status", "distinct axes", "the two-axes decision")

	// Probe axis: every probe field names itself, its values, and points back.
	for _, prop := range []string{"mx", "spf", "dkim"} {
		requireContains("VerifyDomainView", prop, "Live DNS probe outcome", "axis statement")
		requireContains("VerifyDomainView", prop, "not the persisted domain state", "axis contrast")
		requireContains("VerifyDomainView", prop, "dns_records[].status", "cross-pointer back to the persisted axis")
		requireContains("VerifyDomainView", prop, "found (", "per-value meaning: found")
		requireContains("VerifyDomainView", prop, "missing (", "per-value meaning: missing")
	}
	// Only DKIM has the deferred/mismatch states (see classifyDKIM).
	requireContains("VerifyDomainView", "dkim", "deferred (", "per-value meaning: deferred")
	requireContains("VerifyDomainView", "dkim", "mismatch (", "per-value meaning: mismatch")
}
