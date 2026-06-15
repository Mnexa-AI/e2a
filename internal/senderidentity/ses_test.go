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
			name: "dkim success + verified for sending → verified",
			out: &sesv2.GetEmailIdentityOutput{
				VerifiedForSendingStatus: true,
				DkimAttributes:           &ststypes.DkimAttributes{Status: ststypes.DkimStatusSuccess},
			},
			want: StatusVerified,
		},
		{
			name: "dkim success but not verified for sending → pending",
			out: &sesv2.GetEmailIdentityOutput{
				VerifiedForSendingStatus: false,
				DkimAttributes:           &ststypes.DkimAttributes{Status: ststypes.DkimStatusSuccess},
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
	getErr error
	delErr error
}

func (s *stubSESAPI) CreateEmailIdentity(ctx context.Context, in *sesv2.CreateEmailIdentityInput, optFns ...func(*sesv2.Options)) (*sesv2.CreateEmailIdentityOutput, error) {
	panic("not used")
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

func TestSESProvider_NotFoundMapping(t *testing.T) {
	t.Run("Status maps NotFoundException to ErrIdentityNotFound", func(t *testing.T) {
		p := NewSESProvider(&stubSESAPI{getErr: &ststypes.NotFoundException{}})
		_, err := p.Status(context.Background(), "example.com")
		if !errors.Is(err, ErrIdentityNotFound) {
			t.Fatalf("expected ErrIdentityNotFound, got %v", err)
		}
	})

	t.Run("Deprovision treats NotFoundException as success", func(t *testing.T) {
		p := NewSESProvider(&stubSESAPI{delErr: &ststypes.NotFoundException{}})
		if err := p.Deprovision(context.Background(), "example.com"); err != nil {
			t.Fatalf("expected nil for missing identity, got %v", err)
		}
	})

	t.Run("Status propagates other errors", func(t *testing.T) {
		boom := errors.New("throttled")
		p := NewSESProvider(&stubSESAPI{getErr: boom})
		if _, err := p.Status(context.Background(), "example.com"); !errors.Is(err, boom) {
			t.Fatalf("expected boom to propagate, got %v", err)
		}
	})
}
