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
	Status   string `json:"status" doc:"Per-record verification state. Open set; tolerate unknown values. Known values: verified, pending, missing, failed. Inbound records (ownership, inbound_mx) become verified once inbound verification passes, which requires BOTH the ownership TXT and the inbound MX. Sending records reflect their own SES axis: the dkim record follows the DKIM axis, while mail_from_mx and mail_from_spf follow the custom MAIL FROM axis, so a domain with working DKIM but a broken MAIL FROM (or the reverse) shows exactly which record to fix rather than failing all three. Before SES has reported a per-axis result (pre-provision rows) the sending records fall back to the all-or-nothing sending_status rollup; consult sending_error for the failure reason. The domain-level sending_status field remains the all-or-nothing rollup summary."`
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
//   - Sending records reflect their OWN persisted SES axis: the dkim record
//     follows SendingDkimStatus and the mail_from_* records follow
//     SendingMailFromStatus (via sendingAxisStatus), so a domain with good DKIM
//     but a broken MAIL FROM (or the reverse) shows which record to fix instead
//     of failing all three. When an axis is empty (pre-migration-049 rows, or
//     before the reconciler has recorded one) the record falls back to the
//     all-or-nothing `sending_status` rollup.
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
	// Per-axis: each sending record reflects its own SES axis, falling back to
	// the all-or-nothing sending_status rollup when the axis is unset.
	dkimRec := sendingAxisStatus(d.SendingDkimStatus, sendingStatus)
	mailFromRec := sendingAxisStatus(d.SendingMailFromStatus, sendingStatus)
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
		records = append(records, DNSRecord{Type: "TXT", Name: name, Value: value, Purpose: "dkim", Status: dkimRec})
	}

	// mail_from_* — the custom MAIL FROM subdomain's MX + SPF, deterministic and
	// returned regardless of provisioning state, but only when sending is enabled.
	if s.deps.SESRegion != "" {
		mf := mailfrom.Domain(d.Domain)
		records = append(records,
			DNSRecord{Type: "MX", Name: mf, Value: fmt.Sprintf("feedback-smtp.%s.amazonses.com", s.deps.SESRegion), Priority: &mxPriority, Purpose: "mail_from_mx", Status: mailFromRec},
			DNSRecord{Type: "TXT", Name: mf, Value: "v=spf1 include:amazonses.com ~all", Purpose: "mail_from_spf", Status: mailFromRec},
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

// sendingAxisStatus derives a sending record's per-record status from its OWN
// persisted SES axis (sending_dkim_status for the dkim record;
// sending_mail_from_status for the mail_from_* records). When the axis is empty
// it falls back to the all-or-nothing sending_status rollup, so pre-migration-049
// rows and domains that have not yet been polled behave exactly as before. SES
// reports the DKIM and custom MAIL FROM axes independently, so this is what lets
// a domain with good DKIM but a broken MAIL FROM (or the reverse) surface the
// specific failed record instead of failing all three.
func sendingAxisStatus(axis, sendingStatus string) string {
	if axis == "" {
		return sendingRecordStatus(sendingStatus)
	}
	return sendingRecordStatus(axis)
}

// sendingRecordStatus maps a sending status value (a domain-level rollup OR a
// single SES axis) onto the per-record status carried by the sending records
// (dkim, mail_from_*). The records are deterministic and shown before
// provisioning, so an unprovisioned (none) or in-flight (pending) value reads as
// `pending`; a hard failure as `failed`; success as `verified`. Unknown values
// fall through to `pending` (open set).
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

// listDomainsOutput uses the shared Page[T] envelope (items + next_cursor). The
// list is keyset-paginated on (created_at, domain) — domain is the unique
// tiebreak. See listAgentsOutput.
type listDomainsOutput struct {
	Body Page[DomainView]
}

// listDomainsInput carries the standard cursor/limit (PageParams).
type listDomainsInput struct {
	PageParams
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
		Description: "List the domains owned by the authenticated account, newest first, with cursor pagination.",
		Security:    []map[string][]string{{"bearer": {}}},
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
			"402": s.limitExceededResponse(),
			"409": s.jsonResponse(reflect.TypeOf(ErrorEnvelope{}), "ErrorEnvelope",
				"Conflict — the domain is already claimed by another account (code domain_taken)."),
			"default": s.errorEnvelopeResponse(),
		},
	}, s.handleRegisterDomain)

	huma.Register(s.API, huma.Operation{
		OperationID: "deleteDomain", Method: http.MethodDelete, Path: "/v1/domains/{domain}",
		Summary: "Delete a domain", Tags: []string{"domains"},
		Description: "Deprovisions the domain's sending identity and breaks sending for every agent on it. Requires ?confirm=DELETE (irreversible). Returns 200 with a deletion object ({deleted:true, domain}).",
		Security:    []map[string][]string{{"bearer": {}}},
	}, s.handleDeleteDomain)

	huma.Register(s.API, huma.Operation{
		OperationID: "verifyDomain", Method: http.MethodPost, Path: "/v1/domains/{domain}/verify",
		Summary: "Verify a domain", Tags: []string{"domains"},
		Description: "Probe the domain's published DNS and, when the verification TXT (and inbound MX) are present, mark it verified. Always returns 200 with the per-record diagnostic — branch on the `verified` boolean in the body, not the HTTP status. A not-yet-published record is the normal `verified:false` outcome, not an error.",
		Security:    []map[string][]string{{"bearer": {}}},
		Responses: map[string]*huma.Response{
			"default": s.errorEnvelopeResponse(),
		},
	}, s.handleVerifyDomain)
}

// verifyDomainOutput carries the diagnostic body. Both the verified and the
// not-yet-verified outcomes are 200 (Huma's default) — callers branch on
// VerifyDomainView.verified, never on the HTTP status.
type verifyDomainOutput struct {
	Body VerifyDomainView
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
		return &verifyDomainOutput{Body: VerifyDomainView{
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
	// Not-yet-published is the normal `verified:false` outcome — return 200 with
	// the diagnostic so callers see exactly which record is missing. This is NOT
	// an HTTP error (a missing TXT/MX is expected while DNS propagates); clients
	// poll by re-calling and branching on `verified`, never on the status code.
	if !check.TXTFound || check.MX != "found" {
		return &verifyDomainOutput{Body: VerifyDomainView{
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
		return &verifyDomainOutput{Body: VerifyDomainView{
			Domain: in.Domain, Verified: true, MX: check.MX, SPF: check.SPF, DKIM: check.DKIM,
		}}, nil
	}
	return &verifyDomainOutput{Body: VerifyDomainView{
		Domain: updated.Domain, Verified: true, VerifiedAt: updated.VerifiedAt,
		MX: check.MX, SPF: check.SPF, DKIM: check.DKIM,
	}}, nil
}

func (s *Server) handleListDomains(ctx context.Context, in *listDomainsInput) (*listDomainsOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	// The keyset tiebreak for domains is the domain string (its unique key), so
	// the cursor's `id` slot carries the after-domain.
	afterCreatedAt, afterDomain, err := s.decodeKeyset(in.Cursor)
	if err != nil {
		return nil, err
	}
	limit := effectiveLimit(in.Limit)
	// Fetch limit+1 to detect a further page.
	domains, err := s.deps.ListDomains(ctx, user.ID, limit+1, afterCreatedAt, afterDomain)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to list domains")
	}
	hasMore := len(domains) > limit
	if hasMore {
		domains = domains[:limit]
	}
	items := make([]DomainView, 0, len(domains))
	for i := range domains {
		items = append(items, s.domainView(&domains[i]))
	}
	var nextCursor string
	if hasMore {
		last := domains[len(domains)-1]
		if nextCursor, err = s.encodeKeyset(last.CreatedAt, last.Domain); err != nil {
			return nil, err
		}
	}
	return &listDomainsOutput{Body: NewPage(items, nextCursor)}, nil
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
		// Any non-taken failure here is a store/lookup error, not a client
		// error (ClaimOrCreateDomain returns a domain, ErrDomainTaken, or a
		// wrapped DB error — never nil, nil), so it is a 500 — the former
		// 400 "domain_unavailable" misclassified it as the caller's fault.
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to register domain")
	}
	return &domainCreateOutput{Body: s.domainView(d)}, nil
}

type deleteDomainOutput struct{ Body DeleteDomainResult }

// deleteDomainInput adds the confirmation guard (D-5). Deleting a domain
// deprovisions its SES sending identity and breaks sending for every agent on
// it, so it requires ?confirm=DELETE — uniform with deleteAccount/deleteAgent.
type deleteDomainInput struct {
	Domain string `path:"domain"`
	DeleteConfirm
}

func (s *Server) handleDeleteDomain(ctx context.Context, in *deleteDomainInput) (*deleteDomainOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.deps.LookupDomain(ctx, in.Domain, user.ID); err != nil {
		return nil, NewError(http.StatusNotFound, "not_found", "domain not found")
	}
	// Confirm is enforced declaratively by Huma (required + enum:[DELETE] on
	// DeleteConfirm): a missing/wrong ?confirm is a 422 before this handler.
	hasAgents, err := s.deps.HasAgentsOnDomain(ctx, in.Domain, user.ID)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to check domain agents")
	}
	if hasAgents {
		return nil, NewError(http.StatusBadRequest, "domain_has_agents", "cannot delete domain while agents exist — delete its agents first (including any in the trash: they hold the address until restored or permanently deleted)")
	}
	if err := s.deps.DeleteDomain(ctx, in.Domain, user.ID); err != nil {
		switch {
		case errors.Is(err, identity.ErrDomainHasAgents):
			return nil, NewError(http.StatusBadRequest, "domain_has_agents", "cannot delete domain while agents exist — delete its agents first (including any in the trash: they hold the address until restored or permanently deleted)")
		case errors.Is(err, identity.ErrDomainNotFound):
			return nil, NewError(http.StatusNotFound, "not_found", "domain not found")
		default:
			return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to delete domain")
		}
	}
	return &deleteDomainOutput{Body: DeleteDomainResult{Deleted: true, Domain: in.Domain}}, nil
}
