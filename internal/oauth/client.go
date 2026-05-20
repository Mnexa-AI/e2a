package oauth

import (
	"github.com/ory/fosite"
)

// Client is our concrete fosite.Client implementation. It carries the
// metadata persisted in oauth_clients plus an e2a-specific
// CreatedByUserID field used by the DCR-attribution flow.
//
// fosite.Client is an interface; the per-method getters below are what
// fosite reads when validating an authorization request, a token
// exchange, or a revocation. We deliberately don't expose any setters
// outside this package — once a client is loaded from the DB, fosite
// treats it as immutable for the duration of the request.
type Client struct {
	ID                       string
	Name                     string
	RedirectURIs             []string
	GrantTypeStrings         []string
	ResponseTypeStrings      []string
	ScopeStrings             []string
	AudienceStrings          []string
	Public                   bool
	SecretHash               []byte
	TokenEndpointAuthMethodS string
	CreatedByUserID          string
}

// fosite.Client interface methods. We satisfy the minimal Client
// interface; richer subtypes (e.g. ResponseModeClient, OpenIDConnect-
// Client) are deliberately not implemented because we don't support
// those flows.

func (c *Client) GetID() string                                  { return c.ID }
func (c *Client) GetHashedSecret() []byte                        { return c.SecretHash }
func (c *Client) GetRedirectURIs() []string                      { return c.RedirectURIs }
func (c *Client) GetGrantTypes() fosite.Arguments                { return fosite.Arguments(c.GrantTypeStrings) }
func (c *Client) GetResponseTypes() fosite.Arguments             { return fosite.Arguments(c.ResponseTypeStrings) }
func (c *Client) GetScopes() fosite.Arguments                    { return fosite.Arguments(c.ScopeStrings) }
func (c *Client) IsPublic() bool                                 { return c.Public }
func (c *Client) GetAudience() fosite.Arguments                  { return fosite.Arguments(c.AudienceStrings) }
func (c *Client) GetTokenEndpointAuthMethod() string             { return c.TokenEndpointAuthMethodS }
func (c *Client) GetTokenEndpointAuthSigningAlgorithm() string   { return "" }
func (c *Client) GetRequestObjectSigningAlgorithm() string       { return "" }
func (c *Client) GetRequestURIs() []string                       { return nil }
func (c *Client) GetJSONWebKeys() interface{}                    { return nil }
func (c *Client) GetJSONWebKeysURI() string                      { return "" }

// Compile-time check that *Client satisfies fosite.Client.
var _ fosite.Client = (*Client)(nil)
