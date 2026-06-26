package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/dkim"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/Mnexa-AI/e2a/internal/mailfrom"
	"github.com/danielgtaylor/huma/v2"
)

// DNSRecord is one row in the unified DomainView.DNSRecords array. It supersedes
// the legacy split of `dns_records` (an mx/txt/dkim object) + `sending_dns_records`
// (an array): every record the customer must publish — inbound and sending — now
// carries its own `purpose` and `status`, mirroring how peers (Resend/SendGrid)
// model DNS setup. MX records carry their priority in the dedicated `priority`
// field (TXT records leave it null) rather than embedding it in `value`.
type DNSRecord struct {
	Type     string `json:"type" doc:"DNS record type. MX or TXT."`
	Name     string `json:"name" doc:"Record name (host). The apex domain for ownership/inbound_mx; an FQDN for dkim/mail_from records."`
	Value    string `json:"value" doc:"Record value. For MX records this is the mail-server host only; the priority is in the priority field."`
	Priority *int   `json:"priority" doc:"MX priority. Null for non-MX records."`
	Purpose  string `json:"purpose" doc:"What the record is for. Open set; tolerate unknown values. Known values: ownership, inbound_mx, dkim, mail_from_mx, mail_from_spf."`
	Status   string `json:"status" doc:"Per-record verification state. Open set; tolerate unknown values. Known values: verified, pending, missing, failed. Inbound records (ownership, inbound_mx) become verified once inbound verification passes, which requires BOTH the ownership TXT and the inbound MX. Sending records (dkim, mail_from_mx, mail_from_spf) share the SES sending_status rollup, which is all-or-nothing: a failed value can mean only one of DKIM or MAIL FROM failed; consult sending_error for the specific reason. A per-record SES breakdown is a planned enhancement."`
}

type DomainView struct {
	Domain            string `json:"domain"`
	Verified          bool   `json:"verified"`
	VerificationToken string `json:"verification_token"`
	// DNSRecords is the unified, purpose-tagged set of records the customer must
	// publish. ALL applicable records are returned at register time (they are
	// deterministic), so onboarding is a single paste — sending records do not
	// wait for the domain to verify.
	DNSRecords    []DNSRecord `json:"dns_records" nullable:"false"`
	CreatedAt     time.Time   `json:"created_at"`
	VerifiedAt    *time.Time  `json:"verified_at,omitempty"`
	LastCheckedAt *time.Time  `json:"last_checked_at,omitempty"`
	AgentCount    int         `json:"agent_count"`
	// Sender identity (decision 4 / Slice 4). Independent of `verified`
	// (inbound ownership): the async SES sending identity that lets agents
	// on this domain send as their own address. This is the rollup over the
	// dkim + mail_from_* records' per-record status; poll GET /domains/{domain}
	// to watch it go pending → verified|failed.
	SendingStatus        string     `json:"sending_status" doc:"Async SES sending-identity state (rollup). Open set; tolerate unknown values. Known values: none, pending, verified, failed."`
	SendingError         string     `json:"sending_error,omitempty"`
	SendingLastCheckedAt *time.Time `json:"sending_last_checked_at,omitempty"`
}

// domainView builds the unified DNS-records array. Status derivation:
//
//   - Inbound records (ownership, inbound_mx) follow `verified`: the domain is
//     either verified (TXT/MX confirmed) or pending.
//   - Sending records (dkim, mail_from_mx, mail_from_spf) follow the domain's
//     `sending_status` rollup via sendingRecordStatus (none/""→pending,
//     pending→pending, verified→verified, failed→failed).
//
// mail_from_mx + mail_from_spf are emitted ONLY when the sending feature is
// enabled (SESRegion set) and are computed deterministically here (no SES call),
// so they appear at register time regardless of provisioning state. The httpapi
// layer must not import senderidentity (AWS SDK/River), so the MAIL FROM record
// shapes are mirrored locally from mailfrom.Domain + the region.
func (s *Server) domainView(d *identity.Domain) DomainView {
	sendingStatus := d.SendingStatus
	if sendingStatus == "" {
		sendingStatus = "none" // pre-migration-030 rows read as none
	}

	inboundStatus := "pending"
	if d.Verified {
		inboundStatus = "verified"
	}
	sendingRec := sendingRecordStatus(sendingStatus)
	mxPriority := 10

	records := []DNSRecord{
		// ownership — prove control of the domain (also drives the SPF check).
		{Type: "TXT", Name: d.Domain, Value: d.VerificationToken, Purpose: "ownership", Status: inboundStatus},
		// inbound_mx — route inbound mail to the e2a relay.
		{Type: "MX", Name: d.Domain, Value: s.deps.SMTPDomain, Priority: &mxPriority, Purpose: "inbound_mx", Status: inboundStatus},
	}

	// dkim — surfaced for rows that have a stored per-domain key (migration 014+).
	if d.DKIMSelector != "" && d.DKIMPublicKey != "" {
		name, value := dkim.DNSRecord(d.DKIMSelector, d.Domain, d.DKIMPublicKey)
		records = append(records, DNSRecord{Type: "TXT", Name: name, Value: value, Purpose: "dkim", Status: sendingRec})
	}

	// mail_from_* — the custom MAIL FROM subdomain's MX + SPF, deterministic and
	// returned regardless of provisioning state, but only when sending is enabled.
	if s.deps.SESRegion != "" {
		mf := mailfrom.Domain(d.Domain)
		records = append(records,
			DNSRecord{Type: "MX", Name: mf, Value: fmt.Sprintf("feedback-smtp.%s.amazonses.com", s.deps.SESRegion), Priority: &mxPriority, Purpose: "mail_from_mx", Status: sendingRec},
			DNSRecord{Type: "TXT", Name: mf, Value: "v=spf1 include:amazonses.com ~all", Purpose: "mail_from_spf", Status: sendingRec},
		)
	}

	return DomainView{
		Domain:               d.Domain,
		Verified:             d.Verified,
		VerificationToken:    d.VerificationToken,
		DNSRecords:           records,
		CreatedAt:            d.CreatedAt,
		VerifiedAt:           d.VerifiedAt,
		LastCheckedAt:        d.LastCheckedAt,
		AgentCount:           d.AgentCount,
		SendingStatus:        sendingStatus,
		SendingError:         d.SendingError,
		SendingLastCheckedAt: d.SendingLastCheckedAt,
	}
}

// sendingRecordStatus maps the domain-level sending_status rollup onto the
// per-record status carried by the sending records (dkim, mail_from_*). The
// records are deterministic and shown before provisioning, so an unprovisioned
// (none) or in-flight (pending) domain reads as `pending`; a hard failure as
// `failed`; success as `verified`. Unknown rollup values fall through to
// `pending` (open set).
//
// LIMITATION (documented on DNSRecord.Status): this is a ROLLUP — SES verifies
// DKIM + custom MAIL FROM as a unit (mapSESStatus is all-or-nothing), so on
// `failed` all three sending records read `failed` even if only one axis broke;
// sending_error carries the specific reason. Carrying SES's per-axis statuses
// (DkimStatus / MailFromMxStatus) through to each record is a planned follow-up.
func sendingRecordStatus(sendingStatus string) string {
	switch sendingStatus {
	case "verified":
		return "verified"
	case "failed":
		return "failed"
	default:
		return "pending"
	}
}

// enqueueSenderProvision schedules SES sending-identity provisioning for a
// verified domain when the dep is wired (no-op otherwise). Best-effort: a
// missed enqueue is recovered by the next POST /domains/{domain}/verify.
func (s *Server) enqueueSenderProvision(ctx context.Context, domain string) {
	if s.deps.EnqueueSenderProvision != nil {
		s.deps.EnqueueSenderProvision(ctx, domain)
	}
}

// DomainCheckResult is the live-DNS diagnostic surfaced by verify.
type DomainCheckResult struct {
	TXTFound bool
	MX       string
	SPF      string
	DKIM     string
}

// VerifyDomainView mirrors the legacy VerifyDomainResponse.
type VerifyDomainView struct {
	Domain     string     `json:"domain"`
	Verified   bool       `json:"verified"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	MX         string     `json:"mx,omitempty"`
	SPF        string     `json:"spf,omitempty"`
	DKIM       string     `json:"dkim,omitempty" doc:"Live DKIM probe state. Known values: found, missing, deferred, mismatch. 'mismatch' means a DKIM record IS published at the selector but its key doesn't match the issued one — almost always a truncated/clipped TXT (the value is ~400 chars and must be published in full, ending in 'AQAB'). On 'mismatch', re-publish the complete DKIM record; do not just wait."`
}

// listDomainsOutput uses the shared Page[T] envelope (items + next_cursor);
// next_cursor is null at launch. See listAgentsOutput. (GA blocker #3.)
type listDomainsOutput struct {
	Body Page[DomainView]
}
type domainOutput struct{ Body DomainView }
type domainCreateOutput struct{ Body DomainView }

// DomainParam is the path input. The domain segment is matched raw; chi/Huma
// URL-decode it.
type DomainParam struct {
	Domain string `path:"domain"`
}

func (s *Server) registerDomains() {
	huma.Register(s.API, huma.Operation{
		OperationID: "listDomains", Method: http.MethodGet, Path: "/v1/domains",
		Summary: "List domains", Tags: []string{"domains"},
		Security: []map[string][]string{{"bearer": {}}},
	}, s.handleListDomains)

	huma.Register(s.API, huma.Operation{
		OperationID: "getDomain", Method: http.MethodGet, Path: "/v1/domains/{domain}",
		Summary: "Get a domain", Tags: []string{"domains"},
		Security: []map[string][]string{{"bearer": {}}},
	}, s.handleGetDomain)

	huma.Register(s.API, huma.Operation{
		OperationID: "registerDomain", Method: http.MethodPost, Path: "/v1/domains",
		Summary: "Register a domain", Tags: []string{"domains"},
		Security: []map[string][]string{{"bearer": {}}}, DefaultStatus: http.StatusCreated,
		Responses: map[string]*huma.Response{
			"409": s.jsonResponse(reflect.TypeOf(ErrorEnvelope{}), "ErrorEnvelope",
				"Conflict — the domain is already claimed by another account (code domain_taken)."),
			"default": s.errorEnvelopeResponse(),
		},
	}, s.handleRegisterDomain)

	huma.Register(s.API, huma.Operation{
		OperationID: "deleteDomain", Method: http.MethodDelete, Path: "/v1/domains/{domain}",
		Summary: "Delete a domain", Tags: []string{"domains"},
		Security: []map[string][]string{{"bearer": {}}}, DefaultStatus: http.StatusNoContent,
	}, s.handleDeleteDomain)

	huma.Register(s.API, huma.Operation{
		OperationID: "verifyDomain", Method: http.MethodPost, Path: "/v1/domains/{domain}/verify",
		Summary: "Verify a domain", Tags: []string{"domains"},
		Description: "Probe the domain's published DNS and, when the verification TXT is present, mark it verified. Returns the per-record diagnostic; a missing TXT yields 412.",
		Security:    []map[string][]string{{"bearer": {}}},
		Responses: map[string]*huma.Response{
			"412": s.jsonResponse(reflect.TypeOf(VerifyDomainView{}), "VerifyDomainView",
				"Precondition Failed — the verification TXT record is not yet published."),
			"default": s.errorEnvelopeResponse(),
		},
	}, s.handleVerifyDomain)
}

// verifyDomainOutput uses Huma's special Status field to switch between 200
// (verified / already-verified) and 412 (TXT not yet published).
type verifyDomainOutput struct {
	Status int
	Body   VerifyDomainView
}

func (s *Server) handleVerifyDomain(ctx context.Context, in *DomainParam) (*verifyDomainOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	d, err := s.deps.LookupDomain(ctx, in.Domain, user.ID)
	if err != nil || d == nil {
		return nil, NewError(http.StatusNotFound, "not_found", "domain not found")
	}
	// Best-effort touch of last_checked_at — the probe runs regardless.
	if s.deps.TouchDomainChecked != nil {
		_ = s.deps.TouchDomainChecked(ctx, in.Domain, user.ID)
	}
	check := s.deps.VerifyProbe(in.Domain, d.VerificationToken, d.DKIMSelector, d.DKIMPublicKey)

	// Already verified: short-circuit but surface the latest diagnostic.
	// Still (re-)enqueue sender provisioning so this endpoint doubles as the
	// forced sending re-check for a domain whose sending_status is pending/failed.
	if d.Verified {
		s.enqueueSenderProvision(ctx, d.Domain)
		return &verifyDomainOutput{Status: http.StatusOK, Body: VerifyDomainView{
			Domain: d.Domain, Verified: true, VerifiedAt: d.VerifiedAt,
			MX: check.MX, SPF: check.SPF, DKIM: check.DKIM,
		}}, nil
	}
	// Verification requires BOTH the ownership TXT and the inbound MX. The MX
	// gate (added with the per-record status array) is what makes
	// `inbound_mx.status: "verified"` honest: status is derived from the
	// domain's `verified` flag, so `verified` must actually imply the MX is
	// published — otherwise a TXT-only verify would claim a "verified" MX while
	// inbound mail silently bounces. A domain can't receive mail without the MX,
	// so requiring it for `verified` is also the correct inbound semantics.
	// 412 with the diagnostic so callers see exactly which record is missing.
	if !check.TXTFound || check.MX != "found" {
		return &verifyDomainOutput{Status: http.StatusPreconditionFailed, Body: VerifyDomainView{
			Domain: d.Domain, Verified: false, MX: check.MX, SPF: check.SPF, DKIM: check.DKIM,
		}}, nil
	}
	if err := s.deps.VerifyDomain(ctx, in.Domain, user.ID); err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to verify domain")
	}
	// Newly verified (inbound ownership): kick off SES sending-identity
	// provisioning so the domain can graduate to own-address From.
	s.enqueueSenderProvision(ctx, in.Domain)
	// Re-read for verified_at; fall back to the bare success shape.
	updated, err := s.deps.LookupDomain(ctx, in.Domain, user.ID)
	if err != nil || updated == nil {
		return &verifyDomainOutput{Status: http.StatusOK, Body: VerifyDomainView{
			Domain: in.Domain, Verified: true, MX: check.MX, SPF: check.SPF, DKIM: check.DKIM,
		}}, nil
	}
	return &verifyDomainOutput{Status: http.StatusOK, Body: VerifyDomainView{
		Domain: updated.Domain, Verified: true, VerifiedAt: updated.VerifiedAt,
		MX: check.MX, SPF: check.SPF, DKIM: check.DKIM,
	}}, nil
}

func (s *Server) handleListDomains(ctx context.Context, _ *struct{}) (*listDomainsOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	domains, err := s.deps.ListDomains(ctx, user.ID)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to list domains")
	}
	items := make([]DomainView, 0, len(domains))
	for i := range domains {
		items = append(items, s.domainView(&domains[i]))
	}
	return &listDomainsOutput{Body: NewPage(items, "")}, nil
}

func (s *Server) handleGetDomain(ctx context.Context, in *DomainParam) (*domainOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	if in.Domain == "" {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "domain is required")
	}
	d, err := s.deps.LookupDomain(ctx, in.Domain, user.ID)
	if err != nil || d == nil {
		return nil, NewError(http.StatusNotFound, "not_found", "domain not found")
	}
	return &domainOutput{Body: s.domainView(d)}, nil
}

// RegisterDomainRequest is the create-domain body. `domain` is required (D-4):
// it is the only field, so leaving it optional generated an SDK signature that
// compiled with no domain and failed at runtime.
type RegisterDomainRequest struct {
	Domain string `json:"domain"`
}
type registerDomainInput struct {
	Body RegisterDomainRequest
}

func (s *Server) handleRegisterDomain(ctx context.Context, in *registerDomainInput) (*domainCreateOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	normalized, err := agent.ValidateDomain(in.Body.Domain)
	if err != nil {
		return nil, NewError(http.StatusBadRequest, "invalid_domain", err.Error())
	}
	if s.deps.SharedDomain != "" && strings.EqualFold(normalized, s.deps.SharedDomain) {
		return nil, NewError(http.StatusBadRequest, "reserved_domain", "reserved domain")
	}
	if s.deps.EnforceDomainCreate != nil {
		if err := s.deps.EnforceDomainCreate(ctx, user.ID); err != nil {
			if env, ok := limitEnvelope(err); ok {
				return nil, env
			}
			return nil, NewError(http.StatusInternalServerError, "internal_error", "limits check failed")
		}
	}
	d, err := s.deps.ClaimDomain(ctx, normalized, user.ID)
	if err != nil {
		if errors.Is(err, identity.ErrDomainTaken) {
			return nil, NewError(http.StatusConflict, "domain_taken", "domain is already claimed by another account")
		}
		return nil, NewError(http.StatusBadRequest, "domain_unavailable", "failed to register domain")
	}
	if d == nil {
		return nil, NewError(http.StatusBadRequest, "domain_unavailable", "failed to register domain")
	}
	return &domainCreateOutput{Body: s.domainView(d)}, nil
}

type deleteDomainOutput struct{}

// deleteDomainInput adds the confirmation guard (D-5). Deleting a domain
// deprovisions its SES sending identity and breaks sending for every agent on
// it, so it requires ?confirm=DELETE — uniform with deleteAccount/deleteAgent.
type deleteDomainInput struct {
	Domain  string `path:"domain"`
	Confirm string `query:"confirm" doc:"Must be DELETE — this is irreversible (deprovisions the domain's sending identity)."`
}

func (s *Server) handleDeleteDomain(ctx context.Context, in *deleteDomainInput) (*deleteDomainOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.deps.LookupDomain(ctx, in.Domain, user.ID); err != nil {
		return nil, NewError(http.StatusNotFound, "not_found", "domain not found")
	}
	// Confirm after ownership: a not-owned/missing domain is 404 first.
	if in.Confirm != "DELETE" {
		return nil, NewError(http.StatusBadRequest, "confirmation_required", "add ?confirm=DELETE to the request to proceed — this is irreversible")
	}
	hasAgents, err := s.deps.HasAgentsOnDomain(ctx, in.Domain, user.ID)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to check domain agents")
	}
	if hasAgents {
		return nil, NewError(http.StatusBadRequest, "domain_has_agents", "cannot delete domain while agents exist — delete agents first")
	}
	if err := s.deps.DeleteDomain(ctx, in.Domain, user.ID); err != nil {
		switch {
		case errors.Is(err, identity.ErrDomainHasAgents):
			return nil, NewError(http.StatusBadRequest, "domain_has_agents", "cannot delete domain while agents exist — delete agents first")
		case errors.Is(err, identity.ErrDomainNotFound):
			return nil, NewError(http.StatusNotFound, "not_found", "domain not found")
		default:
			return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to delete domain")
		}
	}
	return &deleteDomainOutput{}, nil
}
