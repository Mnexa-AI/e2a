package httpapi

import (
	"context"
	"errors"
	"net/http"
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
	return DomainView{
		Domain:            d.Domain,
		Verified:          d.Verified,
		VerificationToken: d.VerificationToken,
		DNSRecords:        records,
		CreatedAt:         d.CreatedAt,
		VerifiedAt:        d.VerifiedAt,
		IsPrimary:         d.IsPrimary,
		LastCheckedAt:     d.LastCheckedAt,
		AgentCount:        d.AgentCount,
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

type listDomainsOutput struct {
	Body struct {
		Domains []DomainView `json:"domains"`
	}
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
	}, s.handleVerifyDomain)
}

// verifyDomainOutput uses Huma's special Status field to switch between 200
// (verified / already-verified) and 412 (TXT not yet published).
type verifyDomainOutput struct {
	Status int
	Body   VerifyDomainView
}

func (s *Server) handleVerifyDomain(ctx context.Context, in *DomainParam) (*verifyDomainOutput, error) {
	user, err := s.requireUser(ctx)
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
	if d.Verified {
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
	user, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	domains, err := s.deps.ListDomains(ctx, user.ID)
	if err != nil {
		return nil, NewError(http.StatusInternalServerError, "internal_error", "failed to list domains")
	}
	out := &listDomainsOutput{}
	out.Body.Domains = make([]DomainView, 0, len(domains))
	for i := range domains {
		out.Body.Domains = append(out.Body.Domains, s.domainView(&domains[i]))
	}
	return out, nil
}

func (s *Server) handleGetDomain(ctx context.Context, in *DomainParam) (*domainOutput, error) {
	user, err := s.requireUser(ctx)
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

// RegisterDomainRequest mirrors the legacy body.
type RegisterDomainRequest struct {
	Domain string `json:"domain,omitempty"`
}
type registerDomainInput struct {
	Body RegisterDomainRequest
}

func (s *Server) handleRegisterDomain(ctx context.Context, in *registerDomainInput) (*domainCreateOutput, error) {
	user, err := s.requireUser(ctx)
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
	if err != nil || d == nil {
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
	user, err := s.requireUser(ctx)
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

func (s *Server) handleDeleteDomain(ctx context.Context, in *DomainParam) (*deleteDomainOutput, error) {
	user, err := s.requireUser(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.deps.LookupDomain(ctx, in.Domain, user.ID); err != nil {
		return nil, NewError(http.StatusNotFound, "not_found", "domain not found")
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
