package senderidentity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

func TestMapSESStatus(t *testing.T) {
	tests := []struct {
		name string
		out  *sesv2.GetEmailIdentityOutput
		want Status
	}{
		{
			name: "all three (sending + dkim + mailfrom) success → verified",
			out: &sesv2.GetEmailIdentityOutput{
				VerifiedForSendingStatus: true,
				DkimAttributes:           &ststypes.DkimAttributes{Status: ststypes.DkimStatusSuccess},
				MailFromAttributes:       &ststypes.MailFromAttributes{MailFromDomainStatus: ststypes.MailFromDomainStatusSuccess},
			},
			want: StatusVerified,
		},
		{
			name: "dkim success + verified for sending but mailfrom pending → pending (all-or-nothing)",
			out: &sesv2.GetEmailIdentityOutput{
				VerifiedForSendingStatus: true,
				DkimAttributes:           &ststypes.DkimAttributes{Status: ststypes.DkimStatusSuccess},
				MailFromAttributes:       &ststypes.MailFromAttributes{MailFromDomainStatus: ststypes.MailFromDomainStatusPending},
			},
			want: StatusPending,
		},
		{
			name: "dkim+mailfrom success but not verified for sending → pending",
			out: &sesv2.GetEmailIdentityOutput{
				VerifiedForSendingStatus: false,
				DkimAttributes:           &ststypes.DkimAttributes{Status: ststypes.DkimStatusSuccess},
				MailFromAttributes:       &ststypes.MailFromAttributes{MailFromDomainStatus: ststypes.MailFromDomainStatusSuccess},
			},
			want: StatusPending,
		},
		{
			name: "mailfrom failed → failed (even with dkim ok)",
			out: &sesv2.GetEmailIdentityOutput{
				VerifiedForSendingStatus: true,
				DkimAttributes:           &ststypes.DkimAttributes{Status: ststypes.DkimStatusSuccess},
				MailFromAttributes:       &ststypes.MailFromAttributes{MailFromDomainStatus: ststypes.MailFromDomainStatusFailed},
			},
			want: StatusFailed,
		},
		{
			name: "dkim temporary_failure → pending (transient, not stranded as failed)",
			out: &sesv2.GetEmailIdentityOutput{
				VerifiedForSendingStatus: true,
				DkimAttributes:           &ststypes.DkimAttributes{Status: ststypes.DkimStatusTemporaryFailure},
				MailFromAttributes:       &ststypes.MailFromAttributes{MailFromDomainStatus: ststypes.MailFromDomainStatusSuccess},
			},
			want: StatusPending,
		},
		{
			name: "mailfrom temporary_failure → pending (transient, not stranded)",
			out: &sesv2.GetEmailIdentityOutput{
				VerifiedForSendingStatus: true,
				DkimAttributes:           &ststypes.DkimAttributes{Status: ststypes.DkimStatusSuccess},
				MailFromAttributes:       &ststypes.MailFromAttributes{MailFromDomainStatus: ststypes.MailFromDomainStatusTemporaryFailure},
			},
			want: StatusPending,
		},
		{
			name: "dkim failed → failed",
			out: &sesv2.GetEmailIdentityOutput{
				VerifiedForSendingStatus: true,
				DkimAttributes:           &ststypes.DkimAttributes{Status: ststypes.DkimStatusFailed},
			},
			want: StatusFailed,
		},
		{
			name: "dkim pending → pending",
			out: &sesv2.GetEmailIdentityOutput{
				DkimAttributes: &ststypes.DkimAttributes{Status: ststypes.DkimStatusPending},
			},
			want: StatusPending,
		},
		{
			name: "nil dkim attributes → pending",
			out:  &sesv2.GetEmailIdentityOutput{},
			want: StatusPending,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mapSESStatus(tt.out); got != tt.want {
				t.Fatalf("mapSESStatus = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPKCS8Base64(t *testing.T) {
	t.Run("valid pkcs1 der round-trips to parseable pkcs8", func(t *testing.T) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}
		pkcs1 := x509.MarshalPKCS1PrivateKey(key)

		got, err := pkcs8Base64(pkcs1)
		if err != nil {
			t.Fatalf("pkcs8Base64 returned error: %v", err)
		}
		if got == "" {
			t.Fatalf("expected non-empty base64 output")
		}
		der, err := base64.StdEncoding.DecodeString(got)
		if err != nil {
			t.Fatalf("output not valid base64: %v", err)
		}
		if _, err := x509.ParsePKCS8PrivateKey(der); err != nil {
			t.Fatalf("decoded bytes are not valid PKCS#8: %v", err)
		}
	})

	t.Run("garbage bytes return an error", func(t *testing.T) {
		if _, err := pkcs8Base64([]byte("not a real der key")); err == nil {
			t.Fatalf("expected error for garbage input, got nil")
		}
	})
}

// stubSESAPI implements sesAPI; only the methods under test return real
// behavior, the rest panic if unexpectedly called.
type stubSESAPI struct {
	getErr    error
	delErr    error
	createErr error
	putErr    error

	// recorders for the Provision path.
	createInput   *sesv2.CreateEmailIdentityInput
	mailFromInput *sesv2.PutEmailIdentityMailFromAttributesInput
}

func (s *stubSESAPI) CreateEmailIdentity(ctx context.Context, in *sesv2.CreateEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.CreateEmailIdentityOutput, error) {
	s.createInput = in
	if s.createErr != nil {
		return nil, s.createErr
	}
	return &sesv2.CreateEmailIdentityOutput{}, nil
}

func (s *stubSESAPI) PutEmailIdentityMailFromAttributes(ctx context.Context, in *sesv2.PutEmailIdentityMailFromAttributesInput, optFns ...func(*sesv2.Options)) (*sesv2.PutEmailIdentityMailFromAttributesOutput, error) {
	s.mailFromInput = in
	if s.putErr != nil {
		return nil, s.putErr
	}
	return &sesv2.PutEmailIdentityMailFromAttributesOutput{}, nil
}

func (s *stubSESAPI) GetEmailIdentity(ctx context.Context, in *sesv2.GetEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.GetEmailIdentityOutput, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return &sesv2.GetEmailIdentityOutput{}, nil
}

func (s *stubSESAPI) DeleteEmailIdentity(ctx context.Context, in *sesv2.DeleteEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.DeleteEmailIdentityOutput, error) {
	if s.delErr != nil {
		return nil, s.delErr
	}
	return &sesv2.DeleteEmailIdentityOutput{}, nil
}

func (s *stubSESAPI) ListEmailIdentities(ctx context.Context, in *sesv2.ListEmailIdentitiesInput, optFns ...func(*sesv2.Options)) (*sesv2.ListEmailIdentitiesOutput, error) {
	panic("not used")
}

func TestSESProvider_ProvisionConfiguresMailFrom(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs1 := x509.MarshalPKCS1PrivateKey(key)
	stub := &stubSESAPI{}
	p := NewSESProvider(stub, "eu-west-1")

	res, err := p.Provision(context.Background(), "acme.com", "e2a202606", pkcs1)
	if err != nil {
		t.Fatalf("Provision error: %v", err)
	}
	if res.Status != StatusPending {
		t.Fatalf("want pending after provision, got %q", res.Status)
	}
	// Configured the custom MAIL FROM on the identity.
	if stub.mailFromInput == nil || stub.mailFromInput.MailFromDomain == nil ||
		*stub.mailFromInput.MailFromDomain != "bounce.acme.com" {
		t.Fatalf("want MAIL FROM bounce.acme.com, got %+v", stub.mailFromInput)
	}
	if stub.mailFromInput.BehaviorOnMxFailure != ststypes.BehaviorOnMxFailureUseDefaultValue {
		t.Errorf("want USE_DEFAULT_VALUE behavior, got %v", stub.mailFromInput.BehaviorOnMxFailure)
	}
	// Returned the MX + SPF records (region-targeted) for the customer to publish.
	if len(res.DNSRecords) != 2 {
		t.Fatalf("want 2 DNS records, got %d: %+v", len(res.DNSRecords), res.DNSRecords)
	}
	var mx, txt *DNSRecord
	for i := range res.DNSRecords {
		switch res.DNSRecords[i].Type {
		case "MX":
			mx = &res.DNSRecords[i]
		case "TXT":
			txt = &res.DNSRecords[i]
		}
	}
	if mx == nil || mx.Name != "bounce.acme.com" || mx.Value != "10 feedback-smtp.eu-west-1.amazonses.com" {
		t.Errorf("MX record wrong: %+v", mx)
	}
	if txt == nil || txt.Name != "bounce.acme.com" || txt.Value != "v=spf1 include:amazonses.com ~all" {
		t.Errorf("SPF TXT record wrong: %+v", txt)
	}
}

func TestSESProvider_ProvisionAlreadyExistsStillSetsMailFrom(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs1 := x509.MarshalPKCS1PrivateKey(key)
	// CreateEmailIdentity returns AlreadyExists (idempotent re-provision); MAIL
	// FROM must still be (re)configured.
	stub := &stubSESAPI{createErr: &ststypes.AlreadyExistsException{}}
	p := NewSESProvider(stub, "us-east-1")
	res, err := p.Provision(context.Background(), "acme.com", "sel", pkcs1)
	if err != nil {
		t.Fatalf("Provision error: %v", err)
	}
	if res.Status != StatusPending || stub.mailFromInput == nil {
		t.Fatalf("AlreadyExists must still set MAIL FROM; status=%q mailFrom=%+v", res.Status, stub.mailFromInput)
	}
}

func TestSESProvider_ProvisionPropagatesMailFromError(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs1 := x509.MarshalPKCS1PrivateKey(key)
	// CreateEmailIdentity ok, but the MAIL FROM call fails (transient) → Provision
	// must surface the error so River retries (not silently return pending).
	stub := &stubSESAPI{putErr: errors.New("throttled")}
	p := NewSESProvider(stub, "us-east-1")
	if _, err := p.Provision(context.Background(), "acme.com", "sel", pkcs1); err == nil {
		t.Fatal("expected PutEmailIdentityMailFromAttributes error to propagate")
	}
}

func TestSESProvider_StatusReturnsMailFromRecords(t *testing.T) {
	// Status re-emits the MAIL FROM records so the verify/failed transition
	// preserves them (records aren't wiped when a domain goes verified).
	p := NewSESProvider(&stubSESAPI{}, "eu-west-1")
	res, err := p.Status(context.Background(), "acme.com")
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	if len(res.DNSRecords) != 2 {
		t.Fatalf("want 2 records from Status, got %d", len(res.DNSRecords))
	}
	for _, r := range res.DNSRecords {
		if r.Name != "bounce.acme.com" {
			t.Errorf("record name = %q, want bounce.acme.com", r.Name)
		}
	}
}

func TestSESProvider_NotFoundMapping(t *testing.T) {
	t.Run("Status maps NotFoundException to ErrIdentityNotFound", func(t *testing.T) {
		p := NewSESProvider(&stubSESAPI{getErr: &ststypes.NotFoundException{}}, "us-east-1")
		_, err := p.Status(context.Background(), "example.com")
		if !errors.Is(err, ErrIdentityNotFound) {
			t.Fatalf("expected ErrIdentityNotFound, got %v", err)
		}
	})

	t.Run("Deprovision treats NotFoundException as success", func(t *testing.T) {
		p := NewSESProvider(&stubSESAPI{delErr: &ststypes.NotFoundException{}}, "us-east-1")
		if err := p.Deprovision(context.Background(), "example.com"); err != nil {
			t.Fatalf("expected nil for missing identity, got %v", err)
		}
	})

	t.Run("Status propagates other errors", func(t *testing.T) {
		boom := errors.New("throttled")
		p := NewSESProvider(&stubSESAPI{getErr: boom}, "us-east-1")
		if _, err := p.Status(context.Background(), "example.com"); !errors.Is(err, boom) {
			t.Fatalf("expected boom to propagate, got %v", err)
		}
	})
}
