package unsubscribe

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type issuerStore struct {
	err                  error
	hash                 []byte
	user, agent, address string
	calls                int
}

func (s *issuerStore) PutUnsubscribeToken(_ context.Context, hash []byte, user, agent, address string) error {
	s.calls++
	s.hash, s.user, s.agent, s.address = hash, user, agent, address
	return s.err
}

func TestIssuerIssuesAbsoluteStoredToken(t *testing.T) {
	store := &issuerStore{}
	issuer, err := NewIssuer("secret", "https://api.example.com/base/", true, store)
	if err != nil {
		t.Fatal(err)
	}
	link, err := issuer.Issue(context.Background(), "u1", "BOT@Example.com", "User@Example.net")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, "https://api.example.com/base/u/u1_") {
		t.Fatalf("link=%q", link)
	}
	if store.calls != 1 || store.user != "u1" || store.agent != "bot@example.com" || store.address != "user@example.net" || len(store.hash) != 32 {
		t.Fatalf("store=%+v", store)
	}
}

func TestIssuerFailsClosedOnConfigAndStoreErrors(t *testing.T) {
	for _, tc := range []struct {
		name, apiURL string
		prod         bool
	}{
		{"missing", "", false}, {"invalid", "://", false}, {"production_http", "http://api.example", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewIssuer("secret", tc.apiURL, tc.prod, &issuerStore{}); err == nil {
				t.Fatal("want error")
			}
		})
	}
	store := &issuerStore{err: errors.New("db down")}
	issuer, err := NewIssuer("secret", "http://127.0.0.1:8080", false, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.Issue(context.Background(), "u", "a@example.com", "r@example.net"); err == nil {
		t.Fatal("want store error")
	}
}
