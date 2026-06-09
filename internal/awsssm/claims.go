package awsssm

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// IDTokenAud decodes the aud claim from an OIDC id_token WITHOUT
// verifying its signature (it was verified at login). aud may be a
// single string or an array per the JWT spec; both decode to a slice.
//
// Used for the preflight aud pre-check: comparing the token's aud to the
// trust policy's expected audience up front yields a precise error
// instead of an opaque STS AccessDenied — the commonest setup mistake is
// the id_token's aud (the OIDC client_id) not matching the policy.
func IDTokenAud(idToken string) ([]string, bool) {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	var claims struct {
		Aud audClaim `json:"aud"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, false
	}
	return claims.Aud, true
}

// AudMatches reports whether want is among the id_token's aud values.
func AudMatches(idToken, want string) bool {
	auds, ok := IDTokenAud(idToken)
	if !ok {
		return false
	}
	for _, a := range auds {
		if a == want {
			return true
		}
	}
	return false
}

// audClaim decodes a JWT aud that may be a single string or an array.
type audClaim []string

func (a *audClaim) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*a = []string{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}
