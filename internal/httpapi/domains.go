package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/Mnexa-AI/e2a/internal/agent"
	"github.com/Mnexa-AI/e2a/internal/dkim"
	"github.com/Mnexa-AI/e2a/internal/identity"
	"github.com/danielgtaylor/huma/v2"
)

// DNSRecordView / DNSRecordsView / DomainView mirror the legacy DomainInfo
// wire shape verbatim (Slice 1 = path move + conventions only).
type DNSRecordView struct {
	Host     string `json:"host"`
	Value    string `json:"value"`
	Priority *int   `json:"priority,omitempty"`
}

type DNSRecordsView struct {
	MX   DNSRecordView `json:"mx"`
	TXT  DNSRecordView `json:"txt"`
	DKIM DNSRecordView `json:"dkim,omitempty"`
}

type DomainView struct {
	Domain            string         `json:"domain"`
	Verified          bool           `json:"verified"`
	VerificationToken string         `json:"verification_token"`
	DNSRecords        DNSRecordsView `json:"dns_records"`
	CreatedAt         time.Time      `json:"created_at"`
	VerifiedAt        *time.Time     `json:"verified_at,omitempty"`
	IsPrimary         bool           `json:"is_primary"`
	LastCheckedAt     *time.Time     `json:"last_checked_at,omitempty"`
	AgentCount        int            `json:"agent_count"`
	// Sender identity (decision 4 / Slice 4). Independent of `verified`
	// (inbound ownership): the async SES sending identity that lets agents
	// on this domain send as their own address. Poll GET /domains/{domain}
	// to watch SendingStatus go pending → verified|failed.
	SendingStatus        string                 `json:"sending_status" enum:"none,pending,verified,failed"`
	SendingError         string                 `json:"sending_error,omitempty"`
	SendingDNSRecords    []SendingDNSRecordView `json:"sending_dns_records,omitempty" nullable:"false"`
	SendingLastCheckedAt *time.Time             `json:"sending_last_checked_at,omitempty"`
}

// SendingDNSRecordView mirrors senderidentity.DNSRecord on the wire. Defined
// locally so the API layer doesn't import senderidentity (and its River +
// AWS SDK deps); the stored JSONB is unmarshaled into this shape.
type SendingDNSRecordView struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// domainView replicates the legacy domainInfoFromRecord: the MX points at
// the relay host, the TXT carries the verification token, and the DKIM
// record is surfaced only for rows that have a per-domain key (migration 014+).
func (s *Server) domainView(d *identity.Domain) DomainView {
	mxPriority := 10
	records := DNSRecordsView{
		MX:  DNSRecordView{Host: "@", Value: s.deps.SMTPDomain, Priority: &mxPriority},
		TXT: DNSRecordView{Host: "@", Value: d.VerificationToken},
	}
	if d.DKIMSelector != "" && d.DKIMPublicKey != "" {
		name, value := dkim.DNSRecord(d.DKIMSelector, d.Domain, d.DKIMPublicKey)
		records.DKIM = DNSRecordView{Host: name, Value: value}
	}
	sendingStatus := d.SendingStatus
	if sendingStatus == "" {
		sendingStatus = "none" // pre-migration-030 rows read as none
	}
	var sendingRecords []SendingDNSRecordView
	if len(d.SendingDNSRecordsJSON) > 0 {
		_ = json.Unmarshal(d.SendingDNSRecordsJSON, &sendingRecords)
	}
	return DomainView{
		Domain:               d.Domain,
		Verified:             d.Verified,
		VerificationToken:    d.VerificationToken,
		DNSRecords:           records,
		CreatedAt:            d.CreatedAt,
		VerifiedAt:           d.VerifiedAt,
		IsPrimary:            d.IsPrimary,
		LastCheckedAt:        d.LastCheckedAt,
		AgentCount:           d.AgentCount,
		SendingStatus:        sendingStatus,
		SendingError:         d.SendingError,
		SendingDNSRecords:    sendingRecords,
		SendingLastCheckedAt: d.SendingLastCheckedAt,
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
	DKIM       string     `json:"dkim,omitempty"`
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
		OperationID: "updateDomain", Method: http.MethodPatch, Path: "/v1/domains/{domain}",
		Summary: "Update a domain (set primary)", Tags: []string{"domains"},
		Security: []map[string][]string{{"bearer": {}}},
	}, s.handleUpdateDomain)

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
	// Missing TXT: 412 with the diagnostic so callers see what's missing.
	if !check.TXTFound {
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

// UpdateDomainRequest mirrors the legacy body: only `is_primary` is updatable,
// and only promotion (true) — demotion is a no-op (switch by promoting another).
type UpdateDomainRequest struct {
	IsPrimary *bool `json:"is_primary,omitempty"`
}
type updateDomainInput struct {
	Domain string `path:"domain"`
	Body   UpdateDomainRequest
}

func (s *Server) handleUpdateDomain(ctx context.Context, in *updateDomainInput) (*domainOutput, error) {
	user, err := s.requireAccountUser(ctx)
	if err != nil {
		return nil, err
	}
	if in.Body.IsPrimary == nil {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "no updatable fields provided")
	}
	if !*in.Body.IsPrimary {
		return nil, NewError(http.StatusBadRequest, "invalid_request", "to switch primary, PATCH the new primary domain instead")
	}
	if err := s.deps.SetDomainPrimary(ctx, in.Domain, user.ID); err != nil {
		if errors.Is(err, identity.ErrDomainNotFound) {
			return nil, NewError(http.StatusNotFound, "not_found", "domain not found")
		}
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to update domain")
	}
	d, err := s.deps.LookupDomain(ctx, in.Domain, user.ID)
	if err != nil || d == nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to read back domain")
	}
	return &domainOutput{Body: s.domainView(d)}, nil
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
