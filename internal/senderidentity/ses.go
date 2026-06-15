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
)

// sesAPI is the slice of the SES v2 client the provider uses. Narrowing to an
// interface lets the status-mapping logic be unit-tested with a stub (the
// AWS-touching calls themselves are exercised only against live SES).
type sesAPI interface {
	CreateEmailIdentity(ctx context.Context, in *sesv2.CreateEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.CreateEmailIdentityOutput, error)
	GetEmailIdentity(ctx context.Context, in *sesv2.GetEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.GetEmailIdentityOutput, error)
	DeleteEmailIdentity(ctx context.Context, in *sesv2.DeleteEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.DeleteEmailIdentityOutput, error)
	ListEmailIdentities(ctx context.Context, in *sesv2.ListEmailIdentitiesInput, optFns ...func(*sesv2.Options)) (*sesv2.ListEmailIdentitiesOutput, error)
}

// SESProvider is the real Provider backed by AWS SES v2. It registers
// domain sending identities with BYODKIM, reusing e2a's per-domain DKIM key
// so the DKIM d= aligns with the From domain (DMARC passes on DKIM
// alignment). NOTE: only exercised against live AWS — CI/tests use
// FakeProvider; the status-mapping helpers below are unit-tested with a stub.
type SESProvider struct {
	api sesAPI
}

// NewSESProvider wraps a pre-built SES API (or stub).
func NewSESProvider(api sesAPI) *SESProvider { return &SESProvider{api: api} }

// NewSESProviderFromConfig builds a provider from ambient AWS config
// (env/instance role) for the given region.
func NewSESProviderFromConfig(ctx context.Context, region string) (*SESProvider, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return &SESProvider{api: sesv2.NewFromConfig(cfg)}, nil
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
		// fall through to a Status poll shape by returning pending.
		var already *ststypes.AlreadyExistsException
		if errors.As(err, &already) {
			return Result{Status: StatusPending}, nil
		}
		return Result{}, err // transient/permission — retry
	}
	return Result{Status: StatusPending}, nil
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
	return Result{Status: mapSESStatus(out)}, nil
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

// mapSESStatus folds SES's two-axis verification state (sending-verified +
// DKIM status) onto our Status. Verified requires BOTH the identity to be
// verified for sending AND DKIM to have succeeded (so the aligned signature
// actually works). A hard DKIM failure is terminal; anything else is pending.
func mapSESStatus(out *sesv2.GetEmailIdentityOutput) Status {
	dkim := ststypes.DkimStatusNotStarted
	if out.DkimAttributes != nil {
		dkim = out.DkimAttributes.Status
	}
	switch dkim {
	case ststypes.DkimStatusSuccess:
		if out.VerifiedForSendingStatus {
			return StatusVerified
		}
		return StatusPending
	case ststypes.DkimStatusFailed:
		return StatusFailed
	default: // PENDING / TEMPORARY_FAILURE / NOT_STARTED
		return StatusPending
	}
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
