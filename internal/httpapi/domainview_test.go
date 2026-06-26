package httpapi

import (
	"testing"

	"github.com/Mnexa-AI/e2a/internal/identity"
)

// byPurpose indexes a domainView's unified DNS-records array by purpose so the
// assertions can address records by what they're for rather than by position.
func byPurpose(recs []DNSRecord) map[string]DNSRecord {
	m := make(map[string]DNSRecord, len(recs))
	for _, r := range recs {
		m[r.Purpose] = r
	}
	return m
}

// TestDomainViewInboundStatus: inbound records (ownership, inbound_mx) follow
// `verified`; with the sending feature off there are no mail_from records and
// the dkim record only appears when a key is stored.
func TestDomainViewInboundStatus(t *testing.T) {
	s := New(Deps{SMTPDomain: "mx.e2a.dev"}) // SESRegion empty ⇒ sending off

	t.Run("verified, no key", func(t *testing.T) {
		v := s.domainView(&identity.Domain{Domain: "acme.com", Verified: true, VerificationToken: "tok"})
		m := byPurpose(v.DNSRecords)
		if len(v.DNSRecords) != 2 {
			t.Fatalf("want exactly ownership+inbound_mx, got %d: %+v", len(v.DNSRecords), v.DNSRecords)
		}
		own := m["ownership"]
		if own.Type != "TXT" || own.Value != "tok" || own.Status != "verified" {
			t.Fatalf("ownership record wrong: %+v", own)
		}
		mx := m["inbound_mx"]
		if mx.Type != "MX" || mx.Value != "mx.e2a.dev" || mx.Priority == nil || *mx.Priority != 10 || mx.Status != "verified" {
			t.Fatalf("inbound_mx record wrong: %+v", mx)
		}
		if _, ok := m["dkim"]; ok {
			t.Fatalf("no stored key ⇒ no dkim record: %+v", v.DNSRecords)
		}
		if _, ok := m["mail_from_mx"]; ok {
			t.Fatalf("feature off ⇒ no mail_from records: %+v", v.DNSRecords)
		}
	})

	t.Run("unverified ⇒ inbound pending", func(t *testing.T) {
		v := s.domainView(&identity.Domain{Domain: "pending.com", Verified: false, VerificationToken: "tok"})
		m := byPurpose(v.DNSRecords)
		if m["ownership"].Status != "pending" || m["inbound_mx"].Status != "pending" {
			t.Fatalf("unverified domain inbound records must be pending: %+v", v.DNSRecords)
		}
	})

	t.Run("dkim present but feature off ⇒ dkim pending", func(t *testing.T) {
		v := s.domainView(&identity.Domain{
			Domain: "acme.com", Verified: true, VerificationToken: "tok",
			DKIMSelector: "e2a202606", DKIMPublicKey: "PUBKEY",
		})
		m := byPurpose(v.DNSRecords)
		dk, ok := m["dkim"]
		if !ok {
			t.Fatalf("stored key ⇒ dkim record expected: %+v", v.DNSRecords)
		}
		if dk.Type != "TXT" || dk.Status != "pending" {
			t.Fatalf("dkim status with feature off must be pending: %+v", dk)
		}
		if _, ok := m["mail_from_mx"]; ok {
			t.Fatalf("feature off ⇒ still no mail_from records: %+v", v.DNSRecords)
		}
	})
}

// TestDomainViewSendingRecords: with the sending feature on (SESRegion set) the
// mail_from_mx + mail_from_spf records are returned deterministically — even
// before SES provisioning (sending_status none) — and the dkim + mail_from
// statuses track the sending_status rollup.
func TestDomainViewSendingRecords(t *testing.T) {
	s := New(Deps{SMTPDomain: "mx.e2a.dev", SESRegion: "us-east-1"})

	base := func(sendingStatus string) *identity.Domain {
		return &identity.Domain{
			Domain: "acme.com", Verified: true, VerificationToken: "tok",
			DKIMSelector: "e2a202606", DKIMPublicKey: "PUBKEY",
			SendingStatus: sendingStatus,
		}
	}

	// Pre-provision (none): records present + deterministic, status pending.
	t.Run("pre-provision none ⇒ records present, pending", func(t *testing.T) {
		v := s.domainView(base("none"))
		m := byPurpose(v.DNSRecords)
		mfmx, ok := m["mail_from_mx"]
		if !ok {
			t.Fatalf("feature on ⇒ mail_from_mx must be present even pre-provision: %+v", v.DNSRecords)
		}
		if mfmx.Type != "MX" || mfmx.Name != "bounce.acme.com" ||
			mfmx.Value != "feedback-smtp.us-east-1.amazonses.com" ||
			mfmx.Priority == nil || *mfmx.Priority != 10 || mfmx.Status != "pending" {
			t.Fatalf("mail_from_mx shape/status wrong: %+v", mfmx)
		}
		mfspf, ok := m["mail_from_spf"]
		if !ok || mfspf.Type != "TXT" || mfspf.Name != "bounce.acme.com" ||
			mfspf.Value != "v=spf1 include:amazonses.com ~all" || mfspf.Status != "pending" {
			t.Fatalf("mail_from_spf shape/status wrong: %+v", mfspf)
		}
		if mfspf.Priority != nil {
			t.Fatalf("TXT mail_from_spf must have null priority: %+v", mfspf)
		}
		if m["dkim"].Status != "pending" {
			t.Fatalf("dkim follows sending_status (none⇒pending): %+v", m["dkim"])
		}
		// Inbound still follows `verified`, independent of sending.
		if m["ownership"].Status != "verified" {
			t.Fatalf("inbound status must remain independent of sending: %+v", m["ownership"])
		}
	})

	t.Run("sending verified ⇒ sending records verified", func(t *testing.T) {
		m := byPurpose(s.domainView(base("verified")).DNSRecords)
		for _, p := range []string{"dkim", "mail_from_mx", "mail_from_spf"} {
			if m[p].Status != "verified" {
				t.Fatalf("%s should be verified: %+v", p, m[p])
			}
		}
	})

	t.Run("sending failed ⇒ sending records failed", func(t *testing.T) {
		m := byPurpose(s.domainView(base("failed")).DNSRecords)
		for _, p := range []string{"dkim", "mail_from_mx", "mail_from_spf"} {
			if m[p].Status != "failed" {
				t.Fatalf("%s should be failed: %+v", p, m[p])
			}
		}
	})

	t.Run("sending pending ⇒ sending records pending", func(t *testing.T) {
		m := byPurpose(s.domainView(base("pending")).DNSRecords)
		for _, p := range []string{"dkim", "mail_from_mx", "mail_from_spf"} {
			if m[p].Status != "pending" {
				t.Fatalf("%s should be pending: %+v", p, m[p])
			}
		}
	})
}

// TestDomainViewSendingPerAxis is the key regression for the per-axis fix: when
// SES reports the DKIM and custom MAIL FROM axes separately, each sending record
// must reflect its OWN axis instead of the all-or-nothing sending_status rollup,
// so the user can tell which record to fix. The rollup field itself is unchanged.
func TestDomainViewSendingPerAxis(t *testing.T) {
	s := New(Deps{SMTPDomain: "mx.e2a.dev", SESRegion: "us-east-1"})

	base := func() *identity.Domain {
		return &identity.Domain{
			Domain: "acme.com", Verified: true, VerificationToken: "tok",
			DKIMSelector: "e2a202606", DKIMPublicKey: "PUBKEY",
		}
	}

	// The headline case: good DKIM, broken MAIL FROM. The rollup is failed
	// (all-or-nothing), but the dkim record must read verified while the
	// mail_from_* records read failed.
	t.Run("dkim verified + mail_from failed ⇒ records disagree", func(t *testing.T) {
		d := base()
		d.SendingStatus = "failed" // rollup stays all-or-nothing
		d.SendingDkimStatus = "verified"
		d.SendingMailFromStatus = "failed"
		v := s.domainView(d)
		m := byPurpose(v.DNSRecords)
		if m["dkim"].Status != "verified" {
			t.Fatalf("dkim should follow its OWN axis (verified): %+v", m["dkim"])
		}
		if m["mail_from_mx"].Status != "failed" || m["mail_from_spf"].Status != "failed" {
			t.Fatalf("mail_from records should follow their OWN axis (failed): mx=%+v spf=%+v", m["mail_from_mx"], m["mail_from_spf"])
		}
		// The rollup field is the summary and must be untouched.
		if v.SendingStatus != "failed" {
			t.Fatalf("domain-level sending_status rollup must stay failed: %q", v.SendingStatus)
		}
	})

	// Reverse mixed case: broken DKIM, good MAIL FROM.
	t.Run("dkim failed + mail_from verified ⇒ records disagree", func(t *testing.T) {
		d := base()
		d.SendingStatus = "failed"
		d.SendingDkimStatus = "failed"
		d.SendingMailFromStatus = "verified"
		m := byPurpose(s.domainView(d).DNSRecords)
		if m["dkim"].Status != "failed" {
			t.Fatalf("dkim should be failed: %+v", m["dkim"])
		}
		if m["mail_from_mx"].Status != "verified" || m["mail_from_spf"].Status != "verified" {
			t.Fatalf("mail_from records should be verified: mx=%+v spf=%+v", m["mail_from_mx"], m["mail_from_spf"])
		}
	})

	// Fallback: when the per-axis columns are empty (pre-migration-049 rows or
	// pre-provision), every sending record falls back to the rollup — preserving
	// the old behavior gracefully.
	t.Run("empty axes ⇒ fall back to rollup", func(t *testing.T) {
		d := base()
		d.SendingStatus = "verified"
		// SendingDkimStatus / SendingMailFromStatus left empty.
		m := byPurpose(s.domainView(d).DNSRecords)
		for _, p := range []string{"dkim", "mail_from_mx", "mail_from_spf"} {
			if m[p].Status != "verified" {
				t.Fatalf("%s should fall back to rollup (verified): %+v", p, m[p])
			}
		}
	})

	// Partial fallback: dkim axis recorded, mail_from axis still empty ⇒ dkim
	// follows its axis, mail_from falls back to the rollup.
	t.Run("one axis set, one empty ⇒ per-axis + fallback mix", func(t *testing.T) {
		d := base()
		d.SendingStatus = "pending"
		d.SendingDkimStatus = "verified"
		// SendingMailFromStatus empty ⇒ falls back to rollup (pending).
		m := byPurpose(s.domainView(d).DNSRecords)
		if m["dkim"].Status != "verified" {
			t.Fatalf("dkim should follow its axis (verified): %+v", m["dkim"])
		}
		if m["mail_from_mx"].Status != "pending" || m["mail_from_spf"].Status != "pending" {
			t.Fatalf("mail_from should fall back to rollup (pending): mx=%+v spf=%+v", m["mail_from_mx"], m["mail_from_spf"])
		}
	})
}
