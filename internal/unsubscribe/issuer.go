package unsubscribe

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type TokenStore interface {
	PutUnsubscribeToken(ctx context.Context, tokenHash []byte, userID, agentID, address string) error
}

// Issuer derives and stores a recipient-scoped bearer token before returning
// the absolute public unsubscribe URL. It is safe to call repeatedly.
type Issuer struct {
	secret string
	base   *url.URL
	store  TokenStore
}

// Ready validates the issuer's immutable wiring without deriving or storing a
// recipient token. Callers use it before persisting a review hold, while Issue
// remains deferred until the final recipient set is approved.
func (i *Issuer) Ready() error {
	if i == nil || i.secret == "" || i.base == nil || i.base.Scheme == "" || i.base.Host == "" || i.store == nil {
		return errors.New("managed unsubscribe issuer is not configured")
	}
	return nil
}

func NewIssuer(secret, apiURL string, production bool, store TokenStore) (*Issuer, error) {
	if secret == "" {
		return nil, errors.New("unsubscribe signing secret is required")
	}
	if store == nil {
		return nil, errors.New("unsubscribe token store is required")
	}
	base, err := url.Parse(apiURL)
	if err != nil || base.Scheme == "" || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return nil, errors.New("valid absolute API URL is required for managed unsubscribe")
	}
	if production && base.Scheme != "https" {
		return nil, errors.New("managed unsubscribe API URL must use HTTPS in production")
	}
	base.RawQuery, base.Fragment = "", ""
	base.Path = strings.TrimRight(base.Path, "/")
	return &Issuer{secret: secret, base: base, store: store}, nil
}

func (i *Issuer) Issue(ctx context.Context, userID, agentID, recipient string) (string, error) {
	if err := i.Ready(); err != nil {
		return "", err
	}
	token, err := Derive(i.secret, userID, agentID, recipient)
	if err != nil {
		return "", err
	}
	userID = strings.TrimSpace(userID)
	agentID = strings.ToLower(strings.TrimSpace(agentID))
	recipient = strings.ToLower(strings.TrimSpace(recipient))
	if err := i.store.PutUnsubscribeToken(ctx, Hash(token), userID, agentID, recipient); err != nil {
		return "", fmt.Errorf("store unsubscribe token: %w", err)
	}
	u := *i.base
	u.Path = strings.TrimRight(u.Path, "/") + "/u/" + token
	return u.String(), nil
}
