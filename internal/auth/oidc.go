package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCClient bundles the OIDC verifier, OAuth2 config, and discovered
// endpoints into one type with the operations the auth handlers need.
type OIDCClient struct {
	provider     *oidc.Provider
	verifier     *oidc.IDTokenVerifier
	oauth        *oauth2.Config
	logoutURL    string
	groupsClaim  string
	postLogout   string
	audience     string
}

// NewOIDCClient performs OIDC discovery against the issuer and returns
// a configured client. Network failure here is fatal at startup.
func NewOIDCClient(ctx context.Context, c OIDCConfig, groupsClaim string) (*OIDCClient, error) {
	provider, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}

	scopes := c.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "profile", "email", "offline_access", "groups"}
	}

	oauthCfg := &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  c.RedirectURL,
		Scopes:       scopes,
	}

	// end_session_endpoint is not part of OIDC core's typed claims; pull
	// it from the raw discovery doc.
	var meta struct {
		EndSession string `json:"end_session_endpoint"`
	}
	_ = provider.Claims(&meta) // best-effort; nil if OIDC hasn't published it

	return &OIDCClient{
		provider:    provider,
		verifier:    provider.Verifier(&oidc.Config{ClientID: c.ClientID}),
		oauth:       oauthCfg,
		logoutURL:   meta.EndSession,
		groupsClaim: groupsClaim,
		postLogout:  c.PostLogoutRedirect,
		audience:    c.Audience,
	}, nil
}

// AuthCodeURL builds the redirect URL to OIDC with state + PKCE
// challenge. The caller is responsible for storing state and verifier
// in a short-lived cookie until /auth/callback fires.
func (c *OIDCClient) AuthCodeURL(state, verifier string) string {
	challenge := pkceChallenge(verifier)
	opts := []oauth2.AuthCodeOption{
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	}
	if c.audience != "" {
		opts = append(opts, oauth2.SetAuthURLParam("audience", c.audience))
	}
	return c.oauth.AuthCodeURL(state, opts...)
}

// Exchange swaps the auth code for tokens using the PKCE verifier,
// validates the ID token, and returns a populated Session. Caller
// supplies the session ID + absolute expiry; everything else is
// derived from the OIDC response.
func (c *OIDCClient) Exchange(ctx context.Context, code, verifier string, sessionID string, absoluteTimeout time.Duration) (Session, error) {
	tok, err := c.oauth.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", verifier),
	)
	if err != nil {
		return Session{}, fmt.Errorf("code exchange: %w", err)
	}

	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return Session{}, errors.New("id_token missing from OIDC response")
	}

	idTok, err := c.verifier.Verify(ctx, rawID)
	if err != nil {
		return Session{}, fmt.Errorf("id token verify: %w", err)
	}

	var claims struct {
		Email string `json:"email"`
	}
	if err := idTok.Claims(&claims); err != nil {
		return Session{}, fmt.Errorf("id token claims: %w", err)
	}

	groups, err := c.extractGroups(tok, idTok)
	if err != nil {
		return Session{}, err
	}

	now := time.Now()
	return Session{
		ID:             sessionID,
		Subject:        idTok.Subject,
		Email:          claims.Email,
		Groups:         groups,
		AccessToken:    tok.AccessToken,
		RefreshToken:   tok.RefreshToken,
		IDToken:        rawID,
		AccessExpiry:   tok.Expiry,
		AbsoluteExpiry: now.Add(absoluteTimeout),
		LastActivity:   now,
	}, nil
}

// Refresh exchanges the session's refresh token for a new access
// token (and possibly a rotated refresh token). Returns an updated
// Session; caller stores it.
func (c *OIDCClient) Refresh(ctx context.Context, s Session) (Session, error) {
	if s.RefreshToken == "" {
		return s, errors.New("no refresh token")
	}
	src := c.oauth.TokenSource(ctx, &oauth2.Token{
		RefreshToken: s.RefreshToken,
	})
	tok, err := src.Token()
	if err != nil {
		return s, fmt.Errorf("refresh: %w", err)
	}
	s.AccessToken = tok.AccessToken
	if tok.RefreshToken != "" {
		s.RefreshToken = tok.RefreshToken
	}
	s.AccessExpiry = tok.Expiry
	if rawID, ok := tok.Extra("id_token").(string); ok && rawID != "" {
		s.IDToken = rawID
	}
	return s, nil
}

// LogoutURL returns the RP-initiated logout redirect URL, or "" if
// OIDC didn't publish end_session_endpoint.
func (c *OIDCClient) LogoutURL(idTokenHint string) string {
	if c.logoutURL == "" {
		return ""
	}
	u := c.logoutURL + "?id_token_hint=" + idTokenHint
	if c.postLogout != "" {
		u += "&post_logout_redirect_uri=" + c.postLogout
	}
	return u
}

// extractGroups reads the configured groups claim from the access
// token first (OIDC best practice for backend authorization), falling
// back to the ID token, then to /userinfo as a last resort.
func (c *OIDCClient) extractGroups(tok *oauth2.Token, idTok *oidc.IDToken) ([]string, error) {
	// Try ID token first — simpler and synchronous.
	var idClaims map[string]any
	_ = idTok.Claims(&idClaims)
	if g, ok := stringSliceClaim(idClaims, c.groupsClaim); ok {
		return g, nil
	}

	// Try access token. We won't validate the access-token JWT
	// signature here (it's OIDC's, not ours) — we just decode it.
	if tok.AccessToken != "" {
		if claims, ok := decodeJWTPayload(tok.AccessToken); ok {
			if g, ok := stringSliceClaim(claims, c.groupsClaim); ok {
				return g, nil
			}
		}
	}

	// Fall through: no groups in either token. Authorization will
	// reject if the deployment requires groups.
	return nil, nil
}

// pkceChallenge computes the S256 PKCE challenge for a verifier.
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// NewPKCEVerifier returns a 64-char URL-safe-base64 random string,
// suitable as a PKCE code_verifier.
func NewPKCEVerifier() (string, error) {
	b := make([]byte, 48)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: rand.Read failed: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewState returns a 128-bit random string for the state parameter.
func NewState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: rand.Read failed: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// decodeJWTPayload splits the JWT and base64-decodes the middle
// segment. Returns false on any malformedness.
func decodeJWTPayload(jwt string) (map[string]any, bool) {
	parts := splitDot(jwt)
	if len(parts) != 3 {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Some tokens use std-padding base64; try that too.
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, false
		}
	}
	var out map[string]any
	if err := jsonDecode(payload, &out); err != nil {
		return nil, false
	}
	return out, true
}

func stringSliceClaim(claims map[string]any, key string) ([]string, bool) {
	if claims == nil {
		return nil, false
	}
	v, ok := claims[key]
	if !ok {
		return nil, false
	}
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out, true
	case []string:
		return t, true
	case string:
		return []string{t}, true
	}
	return nil, false
}
