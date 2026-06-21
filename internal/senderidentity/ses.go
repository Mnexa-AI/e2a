package senderidentity

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sesv2/types"

	"github.com/Mnexa-AI/e2a/internal/mailfrom"
)

// sesAPI is the slice of the SES v2 client the provider uses. Narrowing to an
// interface lets the status-mapping logic be unit-tested with a stub (the
// AWS-touching calls themselves are exercised only against live SES).
type sesAPI interface {
	CreateEmailIdentity(ctx context.Context, in *sesv2.CreateEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.CreateEmailIdentityOutput, error)
	PutEmailIdentityMailFromAttributes(ctx context.Context, in *sesv2.PutEmailIdentityMailFromAttributesInput, optFns ...func(*sesv2.Options)) (*sesv2.PutEmailIdentityMailFromAttributesOutput, error)
	GetEmailIdentity(ctx context.Context, in *sesv2.GetEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.GetEmailIdentityOutput, error)
	DeleteEmailIdentity(ctx context.Context, in *sesv2.DeleteEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.DeleteEmailIdentityOutput, error)
	ListEmailIdentities(ctx context.Context, in *sesv2.ListEmailIdentitiesInput, optFns ...func(*sesv2.Options)) (*sesv2.ListEmailIdentitiesOutput, error)
}

// SESProvider is the real Provider backed by AWS SES v2. It registers
// domain sending identities with BYODKIM, reusing e2a's per-domain DKIM key
// so the DKIM d= aligns with the From domain (DMARC passes on DKIM
// alignment), and configures a custom MAIL FROM subdomain (bounce.<domain>) so
// the Return-Path aligns too (SPF passes on the From org-domain → no "via e2a").
// NOTE: only exercised against live AWS — CI/tests use FakeProvider; the
// status-mapping helpers below are unit-tested with a stub.
type SESProvider struct {
	api    sesAPI
	region string // for the custom MAIL FROM MX target (feedback-smtp.<region>.amazonses.com)
}

// NewSESProvider wraps a pre-built SES API (or stub). region feeds the MAIL FROM
// MX record target.
func NewSESProvider(api sesAPI, region string) *SESProvider {
	return &SESProvider{api: api, region: region}
}

// NewSESProviderFromConfig builds a provider from ambient AWS config
// (env/instance role) for the given region.
func NewSESProviderFromConfig(ctx context.Context, region string) (*SESProvider, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return &SESProvider{api: sesv2.NewFromConfig(cfg), region: region}, nil
}

func (p *SESProvider) Provision(ctx context.Context, domain, dkimSelector string, dkimPrivateKeyDER []byte) (Result, error) {
	privB64, err := pkcs8Base64(dkimPrivateKeyDER)
	if err != nil {
		// A malformed key is not retryable — fail closed with a reason.
		return Result{Status: StatusFailed, Error: "dkim private key not usable for BYODKIM: " + err.Error()}, nil
	}
	_, err = p.api.CreateEmailIdentity(ctx, &sesv2.CreateEmailIdentityInput{
		EmailIdentity: &domain,
		DkimSigningAttributes: &ststypes.DkimSigningAttributes{
			DomainSigningSelector:         &dkimSelector,
			DomainSigningPrivateKey:       &privB64,
			DomainSigningAttributesOrigin: ststypes.DkimSigningAttributesOriginExternal,
		},
	})
	if err != nil {
		// AlreadyExists is success (idempotent): the identity is registered;
		// fall through to also (re)configure the custom MAIL FROM below.
		var already *ststypes.AlreadyExistsException
		if !errors.As(err, &already) {
			return Result{}, err // transient/permission — retry
		}
	}

	// Configure the custom MAIL FROM subdomain (Return-Path alignment). Idempotent.
	// USE_DEFAULT_VALUE: if the customer's MX later breaks, SES falls back to its
	// own MAIL FROM rather than dropping mail — deliverability-safe (the send path
	// only uses the aligned envelope once status==verified, which requires the MX).
	mfDomain := mailfrom.Domain(domain)
	if _, err := p.api.PutEmailIdentityMailFromAttributes(ctx, &sesv2.PutEmailIdentityMailFromAttributesInput{
		EmailIdentity:       &domain,
		MailFromDomain:      &mfDomain,
		BehaviorOnMxFailure: ststypes.BehaviorOnMxFailureUseDefaultValue,
	}); err != nil {
		return Result{}, err // transient/permission — retry
	}

	return Result{Status: StatusPending, DNSRecords: mailFromRecords(domain, p.region)}, nil
}

// mailFromRecords are the two records the customer must publish for the custom
// MAIL FROM subdomain: an MX to SES's regional feedback handler and an SPF TXT
// so SPF authenticates (and aligns to) the From org-domain. Shared by the SES
// provider and the FakeProvider so tests assert the real shape.
func mailFromRecords(domain, region string) []DNSRecord {
	mf := mailfrom.Domain(domain)
	return []DNSRecord{
		{Type: "MX", Name: mf, Value: fmt.Sprintf("10 feedback-smtp.%s.amazonses.com", region)},
		{Type: "TXT", Name: mf, Value: "v=spf1 include:amazonses.com ~all"},
	}
}

func (p *SESProvider) Status(ctx context.Context, domain string) (Result, error) {
	out, err := p.api.GetEmailIdentity(ctx, &sesv2.GetEmailIdentityInput{EmailIdentity: &domain})
	if err != nil {
		var notFound *ststypes.NotFoundException
		if errors.As(err, &notFound) {
			return Result{}, ErrIdentityNotFound
		}
		return Result{}, err
	}
	// Re-emit the MAIL FROM records on every poll so the verify/failed transition
	// preserves them (ReconcileWorker writes res.DNSRecords) — a verified domain's
	// view keeps showing the MX/SPF the customer must KEEP published.
	return Result{Status: mapSESStatus(out), DNSRecords: mailFromRecords(domain, p.region)}, nil
}

func (p *SESProvider) Deprovision(ctx context.Context, domain string) error {
	_, err := p.api.DeleteEmailIdentity(ctx, &sesv2.DeleteEmailIdentityInput{EmailIdentity: &domain})
	if err != nil {
		var notFound *ststypes.NotFoundException
		if errors.As(err, &notFound) {
			return nil // already gone — idempotent success
		}
		return err
	}
	return nil
}

func (p *SESProvider) List(ctx context.Context) ([]string, error) {
	var out []string
	var token *string
	for {
		resp, err := p.api.ListEmailIdentities(ctx, &sesv2.ListEmailIdentitiesInput{NextToken: token})
		if err != nil {
			return nil, err
		}
		for _, id := range resp.EmailIdentities {
			if id.IdentityType == ststypes.IdentityTypeDomain && id.IdentityName != nil {
				out = append(out, *id.IdentityName)
			}
		}
		if resp.NextToken == nil || *resp.NextToken == "" {
			return out, nil
		}
		token = resp.NextToken
	}
}

// mapSESStatus folds SES's verification axes onto our Status. Verified requires
// ALL of: the identity verified for sending, DKIM succeeded (aligned signature),
// AND the custom MAIL FROM succeeded (aligned Return-Path) — all-or-nothing
// (design Q2), so reaching `verified` means there is genuinely no "via e2a". A
// hard failure on either DKIM or MAIL FROM is terminal; anything else is pending.
func mapSESStatus(out *sesv2.GetEmailIdentityOutput) Status {
	dkim := ststypes.DkimStatusNotStarted
	if out.DkimAttributes != nil {
		dkim = out.DkimAttributes.Status
	}
	mf := ststypes.MailFromDomainStatusPending
	if out.MailFromAttributes != nil {
		mf = out.MailFromAttributes.MailFromDomainStatus
	}
	if dkim == ststypes.DkimStatusFailed || mf == ststypes.MailFromDomainStatusFailed {
		return StatusFailed
	}
	if dkim == ststypes.DkimStatusSuccess && out.VerifiedForSendingStatus && mf == ststypes.MailFromDomainStatusSuccess {
		return StatusVerified
	}
	return StatusPending
}

// pkcs8Base64 converts a stored PKCS#1 DER RSA private key to the single-line
// base64 PKCS#8 form SES BYODKIM expects.
func pkcs8Base64(pkcs1DER []byte) (string, error) {
	key, err := x509.ParsePKCS1PrivateKey(pkcs1DER)
	if err != nil {
		return "", fmt.Errorf("parse pkcs1: %w", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", fmt.Errorf("marshal pkcs8: %w", err)
	}
	return base64.StdEncoding.EncodeToString(pkcs8), nil
}
