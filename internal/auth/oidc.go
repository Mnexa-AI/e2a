package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/tokencanopy/e2a/internal/config"
	"github.com/tokencanopy/e2a/internal/identity"
)

const (
	oidcStateCookieName    = "e2a_oidc_state"
	oidcNonceCookieName    = "e2a_oidc_nonce"
	oidcVerifierCookieName = "e2a_oidc_verifier"
	oidcCookiePath         = "/api/auth/oidc"
	oidcTransactionMaxAge  = 10 * time.Minute
)

// OIDCAuth implements an optional OpenID Connect relying party for browser
// login. It accepts only Authorization Code responses initiated by HandleLogin,
// verifies the returned ID token, and maps a configured claim to an existing
// e2a users.id. It never provisions users.
type OIDCAuth struct {
	cfg         config.OIDCConfig
	oauthConfig *oauth2.Config
	verifier    *oidc.IDTokenVerifier
	store       *identity.Store
	secure      bool
	baseURL     string
}

// NewOIDCAuth returns nil without performing discovery when OIDC is disabled.
// Enabled configurations are discovered immediately so startup fails closed if
// the configured issuer cannot provide valid OIDC metadata.
func NewOIDCAuth(ctx context.Context, cfg config.OIDCConfig, store *identity.Store, production bool, baseURL string) (*OIDCAuth, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC issuer: %w", err)
	}

	return &OIDCAuth{
		cfg: cfg,
		oauthConfig: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		store:    store,
		secure:   production,
		baseURL:  strings.TrimRight(baseURL, "/"),
	}, nil
}

// HandleLogin creates a browser-bound OIDC transaction and redirects to the
// provider's authorization endpoint. The PKCE verifier and OIDC nonce never
// appear in application logs or identity-bearing cookies.
func (oa *OIDCAuth) HandleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := randomOIDCValue()
	if err != nil {
		log.Printf("[auth] OIDC login initialization failed: %v", err)
		http.Error(w, "login unavailable", http.StatusInternalServerError)
		return
	}
	nonce, err := randomOIDCValue()
	if err != nil {
		log.Printf("[auth] OIDC login initialization failed: %v", err)
		http.Error(w, "login unavailable", http.StatusInternalServerError)
		return
	}
	verifier := oauth2.GenerateVerifier()

	oa.setTransactionCookie(w, oidcStateCookieName, state, int(oidcTransactionMaxAge.Seconds()))
	oa.setTransactionCookie(w, oidcNonceCookieName, nonce, int(oidcTransactionMaxAge.Seconds()))
	oa.setTransactionCookie(w, oidcVerifierCookieName, verifier, int(oidcTransactionMaxAge.Seconds()))

	location := oa.oauthConfig.AuthCodeURL(
		state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	)
	http.Redirect(w, r, location, http.StatusFound)
}

// HandleCallback validates the browser transaction, exchanges the short-lived
// authorization code over the back channel, verifies the ID token, and creates
// the same e2a session used by the legacy Google flow.
func (oa *OIDCAuth) HandleCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, stateErr := r.Cookie(oidcStateCookieName)
	nonceCookie, nonceErr := r.Cookie(oidcNonceCookieName)
	verifierCookie, verifierErr := r.Cookie(oidcVerifierCookieName)
	if stateErr != nil || nonceErr != nil || verifierErr != nil {
		oa.clearTransactionCookies(w)
		http.Error(w, "invalid login transaction", http.StatusBadRequest)
		return
	}

	requestState := r.URL.Query().Get("state")
	if requestState == "" || subtle.ConstantTimeCompare([]byte(requestState), []byte(stateCookie.Value)) != 1 {
		oa.clearTransactionCookies(w)
		http.Error(w, "invalid login transaction", http.StatusBadRequest)
		return
	}

	// Consume the browser transaction before any network or database work.
	// Authorization codes remain single-use at the provider as required by OIDC.
	oa.clearTransactionCookies(w)

	if r.URL.Query().Get("error") != "" {
		log.Printf("[auth] OIDC provider rejected authorization")
		http.Error(w, "login rejected", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "invalid login response", http.StatusBadRequest)
		return
	}

	token, err := oa.oauthConfig.Exchange(r.Context(), code, oauth2.VerifierOption(verifierCookie.Value))
	if err != nil {
		log.Printf("[auth] OIDC code exchange failed: %v", err)
		http.Error(w, "login verification failed", http.StatusUnauthorized)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		log.Printf("[auth] OIDC token response omitted id_token")
		http.Error(w, "login verification failed", http.StatusUnauthorized)
		return
	}
	idToken, err := oa.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		log.Printf("[auth] OIDC ID token verification failed: %v", err)
		http.Error(w, "login verification failed", http.StatusUnauthorized)
		return
	}
	if idToken.Nonce == "" || subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(nonceCookie.Value)) != 1 {
		log.Printf("[auth] OIDC nonce verification failed")
		http.Error(w, "login verification failed", http.StatusUnauthorized)
		return
	}

	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		log.Printf("[auth] OIDC claims decoding failed: %v", err)
		http.Error(w, "login verification failed", http.StatusUnauthorized)
		return
	}
	rawUserID, ok := claims[oa.cfg.UserIDClaim].(string)
	userID := strings.TrimSpace(rawUserID)
	if !ok || userID == "" {
		log.Printf("[auth] OIDC ID token missing a valid user mapping claim")
		http.Error(w, "login verification failed", http.StatusUnauthorized)
		return
	}

	user, err := oa.store.GetUserByID(r.Context(), userID)
	if err != nil || user == nil {
		log.Printf("[auth] OIDC login referenced an unknown e2a user")
		http.Error(w, "user not found", http.StatusForbidden)
		return
	}
	sessionToken, err := oa.store.CreateUserSession(r.Context(), user.ID)
	if err != nil {
		log.Printf("[auth] OIDC session creation failed: %v", err)
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   oa.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(SessionMaxAge.Seconds()),
	})
	http.Redirect(w, r, oa.baseURL+"/dashboard", http.StatusFound)
}

func randomOIDCValue() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate secure random value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func (oa *OIDCAuth) setTransactionCookie(w http.ResponseWriter, name, value string, maxAge int) {
	cookie := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     oidcCookiePath,
		HttpOnly: true,
		Secure:   oa.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
	if maxAge > 0 {
		cookie.Expires = time.Now().Add(time.Duration(maxAge) * time.Second)
	} else {
		cookie.Expires = time.Unix(1, 0)
	}
	http.SetCookie(w, cookie)
}

func (oa *OIDCAuth) clearTransactionCookies(w http.ResponseWriter) {
	oa.setTransactionCookie(w, oidcStateCookieName, "", -1)
	oa.setTransactionCookie(w, oidcNonceCookieName, "", -1)
	oa.setTransactionCookie(w, oidcVerifierCookieName, "", -1)
}
