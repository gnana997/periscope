// Package auth implements Periscope's user-identity layer (Layer A in
// RFC 0002): OIDC login via Authorization Code + PKCE in a
// Backend-for-Frontend pattern. Tokens never leave the backend; the
// browser only sees an httpOnly session cookie.
//
// In dev mode (the default when no auth file is configured), OIDC is
// disabled entirely and a fixed dev session is auto-injected on first
// request. This keeps `go run ./cmd/periscope` zero-config for local
// development.
package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Mode picks the auth backend.
type Mode string

const (
	// ModeDev auto-creates a fixed dev session on every request. No
	// OIDC dependency. Default when PERISCOPE_AUTH_FILE is unset and
	// PERISCOPE_AUTH_MODE is unset or "dev".
	ModeDev Mode = "dev"

	// ModeOIDC requires a fully-populated auth file with an okta:
	// block plus OIDC_CLIENT_SECRET in the environment.
	ModeOIDC Mode = "oidc"
)

// Config is the parsed auth.yaml plus the resolved mode.
type Config struct {
	Mode          Mode                `yaml:"-"`
	OIDC          OIDCConfig          `yaml:"oidc"`
	Session       SessionConfig       `yaml:"session"`
	Authorization AuthorizationConfig `yaml:"authorization"`
	Dev           DevConfig           `yaml:"dev"`
}

type OIDCConfig struct {
	Issuer       string   `yaml:"issuer"`
	ClientID     string   `yaml:"clientID"`
	ClientSecret string   `yaml:"clientSecret"`
	RedirectURL  string   `yaml:"redirectURL"`
	Scopes       []string `yaml:"scopes"`
	Audience     string   `yaml:"audience"`
	// ProviderName is the human-friendly IdP label shown on the SPA
	// LoginScreen ("sign in with auth0"). Optional — auto-detected
	// from the issuer URL when empty.
	ProviderName       string `yaml:"providerName"`
	PostLogoutRedirect string `yaml:"postLogoutRedirect"`
}

type SessionConfig struct {
	CookieName      string        `yaml:"cookieName"`
	IdleTimeout     time.Duration `yaml:"idleTimeout"`
	AbsoluteTimeout time.Duration `yaml:"absoluteTimeout"`
	CookieDomain    string        `yaml:"cookieDomain"`
}

type AuthorizationConfig struct {
	// Mode picks the K8s authorization strategy.
	// One of: shared (default) | tier | raw. See RFC 0002 4.
	Mode string `yaml:"mode"`

	// AllowedGroups gates Periscope access. Empty list = any
	// authenticated user is allowed. In tier mode, the gate is
	// the union of allowedGroups and groupTiers keys.
	AllowedGroups []string `yaml:"allowedGroups"`

	// GroupsClaim is the IdP token claim that holds groups.
	// Auth0 needs a namespaced custom claim (e.g.
	// https://periscope/groups); Okta exposes "groups".
	GroupsClaim string `yaml:"groupsClaim"`

	// --- tier-mode only ---

	// GroupTiers maps IdP group names to one of the five built-in
	// tier names (read/triage/write/maintain/admin).
	GroupTiers map[string]string `yaml:"groupTiers"`

	// DefaultTier is applied when a user matches none of the
	// listed groups. Empty string = deny (user gets no tier).
	DefaultTier string `yaml:"defaultTier"`

	// --- raw-mode only ---

	// GroupPrefix is prepended to each of the user's IdP groups
	// when impersonating. Default "periscope:".
	GroupPrefix string `yaml:"groupPrefix"`
}

// DevConfig only applies to ModeDev. The fields show up in the SPA so
// operators can spot at a glance that they're running in dev mode.
type DevConfig struct {
	Subject string   `yaml:"subject"`
	Email   string   `yaml:"email"`
	Groups  []string `yaml:"groups"`
}

// Default returns a dev-mode config with sensible defaults. Used when
// no auth file is configured.
func Default() Config {
	return Config{

		Session: SessionConfig{
			CookieName:      "periscope_session",
			IdleTimeout:     30 * time.Minute,
			AbsoluteTimeout: 8 * time.Hour,
		},
		Dev: DevConfig{
			Subject: "dev@local",
			Email:   "dev@local",
			Groups:  []string{"dev"},
		},
	}
}

// Load resolves Mode, reads the file at path (if non-empty), and
// applies env-var interpolation on the OIDC client secret.
//
// Resolution order:
//   - PERISCOPE_AUTH_MODE=oidc + path required → ModeOIDC
//   - path empty → ModeDev (warn at startup)
//   - path set, no oidc block → ModeDev with overrides from file
func Load(path string) (Config, error) {
	cfg := Default()

	mode := strings.ToLower(strings.TrimSpace(os.Getenv("PERISCOPE_AUTH_MODE")))
	if mode != "" {
		cfg.Mode = Mode(mode)
	}

	if path == "" {
		if cfg.Mode == "" {
			cfg.Mode = ModeDev
		}
		if cfg.Mode == ModeOIDC {
			return cfg, errors.New("auth: PERISCOPE_AUTH_MODE=oidc but no auth file configured (set PERISCOPE_AUTH_FILE)")
		}
		return cfg, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("auth: read %q: %w", path, err)
	}

	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("auth: parse %q: %w", path, err)
	}

	if cfg.Session.CookieName == "" {
		cfg.Session.CookieName = "periscope_session"
	}
	if cfg.Session.IdleTimeout == 0 {
		cfg.Session.IdleTimeout = 30 * time.Minute
	}
	if cfg.Session.AbsoluteTimeout == 0 {
		cfg.Session.AbsoluteTimeout = 8 * time.Hour
	}
	if cfg.Authorization.GroupsClaim == "" {
		cfg.Authorization.GroupsClaim = "groups"
	}

	if cfg.Mode == "" {
		if cfg.OIDC.Issuer != "" {
			cfg.Mode = ModeOIDC
		} else {
			cfg.Mode = ModeDev
		}
	}

	if cfg.Mode == ModeOIDC {
		if err := validateOIDC(cfg.OIDC); err != nil {
			return cfg, err
		}
	}

	return cfg, nil
}

func validateOIDC(o OIDCConfig) error {
	missing := []string{}
	if o.Issuer == "" {
		missing = append(missing, "oidc.issuer")
	}
	if o.ClientID == "" {
		missing = append(missing, "oidc.clientID")
	}
	if o.RedirectURL == "" {
		missing = append(missing, "oidc.redirectURL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("auth: oidc mode missing required fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

// SecretResolver resolves secret-reference strings (env vars, file://,
// aws-secretsmanager://, aws-ssm://, ...) at load time. Implemented by
// internal/secrets.Resolver; declared as an interface here so the auth
// package doesn't pull in aws-sdk-go-v2 directly.
type SecretResolver interface {
	Resolve(ctx context.Context, ref string) (string, error)
}

// ResolveSecrets walks the config and replaces every secret-bearing
// field with its resolved plaintext. Today that's just oidc.clientSecret;
// new fields hook in here.
//
// Called after Load and before the OIDCClient is constructed, so the
// validation in Load doesn't see scheme strings as "missing."
func ResolveSecrets(ctx context.Context, cfg *Config, resolver SecretResolver) error {
	if resolver == nil || cfg.OIDC.ClientSecret == "" {
		return nil
	}
	v, err := resolver.Resolve(ctx, cfg.OIDC.ClientSecret)
	if err != nil {
		return fmt.Errorf("auth: resolve oidc.clientSecret: %w", err)
	}
	cfg.OIDC.ClientSecret = v
	return nil
}

// ProviderLabel returns the display name for the configured IdP.
// Operator override (oidc.providerName) wins; otherwise we infer from
// the issuer URL. Falls through to "OIDC" for unknown issuers.
func ProviderLabel(o OIDCConfig) string {
	if o.ProviderName != "" {
		return o.ProviderName
	}
	iss := strings.ToLower(o.Issuer)
	switch {
	case strings.Contains(iss, "auth0.com"):
		return "Auth0"
	case strings.Contains(iss, "okta.com") || strings.Contains(iss, "oktapreview.com"):
		return "Okta"
	case strings.Contains(iss, "microsoftonline.com") || strings.Contains(iss, "login.microsoft.com"):
		return "Microsoft Entra"
	case strings.Contains(iss, "accounts.google.com") || strings.Contains(iss, "googleapis.com"):
		return "Google"
	case strings.Contains(iss, "keycloak"):
		return "Keycloak"
	}
	return "OIDC"
}
