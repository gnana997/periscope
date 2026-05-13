package iam

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrMalformedPolicy wraps every parse error so callers can branch
// via errors.Is. The engine surfaces matching errors to the SPA as
// RolePermissionsResult{PolicyFetchPartial: true} rather than
// dropping the whole page.
var ErrMalformedPolicy = errors.New("iam: malformed policy document")

// ParsePolicyDocument turns a raw IAM policy JSON blob (as returned
// by iam:GetRolePolicy / iam:GetPolicyVersion) into a normalized
// PolicyDocument. Handles AWS's documented JSON quirks:
//
//   - Statement may be a single object or an array of objects
//   - Action / NotAction / Resource / NotResource may be a string
//     or an array of strings
//   - Principal / NotPrincipal may be "*", a map keyed by principal
//     type with string-or-array values
//   - Effect is normalized case-insensitively to EffectAllow /
//     EffectDeny; surrounding whitespace is trimmed
//   - Condition is preserved as json.RawMessage; not interpreted
//     in v1.1 (HasCondition presence-only flag downstream)
//
// Returns (zero PolicyDocument, error wrapping ErrMalformedPolicy)
// on any failure.
func ParsePolicyDocument(raw []byte) (PolicyDocument, error) {
	var rd rawDocument
	if err := json.Unmarshal(raw, &rd); err != nil {
		return PolicyDocument{}, fmt.Errorf("%w: %v", ErrMalformedPolicy, err)
	}

	// Statement is object-or-array.
	rawStmts, err := unmarshalStatementBlock(rd.Statement)
	if err != nil {
		return PolicyDocument{}, fmt.Errorf("%w: parse Statement: %v", ErrMalformedPolicy, err)
	}

	stmts := make([]Statement, 0, len(rawStmts))
	for i, rs := range rawStmts {
		s, err := normalizeStatement(rs)
		if err != nil {
			return PolicyDocument{}, fmt.Errorf("%w: statement[%d]: %v", ErrMalformedPolicy, i, err)
		}
		stmts = append(stmts, s)
	}

	return PolicyDocument{
		Version:   rd.Version,
		Id:        rd.Id,
		Statement: stmts,
	}, nil
}

// ── Raw-shape pre-parsing types ───────────────────────────────────

// rawDocument is the top-level JSON shape with Statement as
// RawMessage so we can choose array-or-object at the next step.
type rawDocument struct {
	Version   string          `json:"Version,omitempty"`
	Id        string          `json:"Id,omitempty"`
	Statement json.RawMessage `json:"Statement,omitempty"`
}

// rawStatement is one statement with every flexible field kept as
// RawMessage so per-field normalization can run.
type rawStatement struct {
	Sid          string          `json:"Sid,omitempty"`
	Effect       string          `json:"Effect"`
	Action       json.RawMessage `json:"Action,omitempty"`
	NotAction    json.RawMessage `json:"NotAction,omitempty"`
	Resource     json.RawMessage `json:"Resource,omitempty"`
	NotResource  json.RawMessage `json:"NotResource,omitempty"`
	Principal    json.RawMessage `json:"Principal,omitempty"`
	NotPrincipal json.RawMessage `json:"NotPrincipal,omitempty"`
	Condition    json.RawMessage `json:"Condition,omitempty"`
}

// ── Normalization helpers ────────────────────────────────────────

// unmarshalStatementBlock accepts Statement as either a single
// object or an array of objects (AWS quirk). Returns nil + nil for
// an empty/missing Statement field.
func unmarshalStatementBlock(raw json.RawMessage) ([]rawStatement, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// Try array first (more common shape).
	var arr []rawStatement
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}
	// Fall back to single-object form. If THIS errors, surface it.
	var single rawStatement
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, err
	}
	return []rawStatement{single}, nil
}

// normalizeStatement converts a rawStatement into the canonical
// Statement shape. Validates Effect, normalizes flexible fields.
func normalizeStatement(rs rawStatement) (Statement, error) {
	effect, err := normalizeEffect(rs.Effect)
	if err != nil {
		return Statement{}, err
	}

	action, err := unmarshalStringOrStringArray(rs.Action)
	if err != nil {
		return Statement{}, fmt.Errorf("field %q: %v", "Action", err)
	}
	notAction, err := unmarshalStringOrStringArray(rs.NotAction)
	if err != nil {
		return Statement{}, fmt.Errorf("field %q: %v", "NotAction", err)
	}
	resource, err := unmarshalStringOrStringArray(rs.Resource)
	if err != nil {
		return Statement{}, fmt.Errorf("field %q: %v", "Resource", err)
	}
	notResource, err := unmarshalStringOrStringArray(rs.NotResource)
	if err != nil {
		return Statement{}, fmt.Errorf("field %q: %v", "NotResource", err)
	}

	principal, err := unmarshalPrincipalBlock(rs.Principal)
	if err != nil {
		return Statement{}, fmt.Errorf("field %q: %v", "Principal", err)
	}
	notPrincipal, err := unmarshalPrincipalBlock(rs.NotPrincipal)
	if err != nil {
		return Statement{}, fmt.Errorf("field %q: %v", "NotPrincipal", err)
	}

	return Statement{
		Sid:          rs.Sid,
		Effect:       effect,
		Action:       action,
		NotAction:    notAction,
		Resource:     resource,
		NotResource:  notResource,
		Principal:    principal,
		NotPrincipal: notPrincipal,
		Condition:    rs.Condition,
	}, nil
}

// normalizeEffect is case-insensitive + whitespace-tolerant. AWS
// spec is "Allow" / "Deny" exact, but real-world JSON has spotted
// generators emitting lowercase or padded values; the parser is
// defensive here so the engine doesn't blank pages on cosmetic
// drift.
func normalizeEffect(s string) (Effect, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "allow":
		return EffectAllow, nil
	case "deny":
		return EffectDeny, nil
	case "":
		return "", fmt.Errorf("Effect is required")
	default:
		return "", fmt.Errorf("unknown Effect %q (want Allow or Deny)", s)
	}
}

// unmarshalStringOrStringArray accepts either a JSON string or a
// JSON array of strings. Returns nil + nil for an empty/missing
// field — distinguishes "absent" from "present but empty array".
func unmarshalStringOrStringArray(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// Try array first.
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}
	// Fall back to single string.
	var single string
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, err
	}
	return []string{single}, nil
}

// unmarshalPrincipalBlock handles the three legal Principal shapes:
//
//   - "*"                                  → {"*": ["*"]}
//   - {"AWS": "arn:..."}                   → {"AWS": ["arn:..."]}
//   - {"AWS": ["a","b"], "Service": "s3"}  → {"AWS": ["a","b"], "Service": ["s3"]}
//
// Returns nil for absent / empty. v1.1 doesn't evaluate Principal
// (identity-based policies only), but the parser must accept all
// three forms because real policies attached to roles sometimes
// have them.
func unmarshalPrincipalBlock(raw json.RawMessage) (map[string][]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// "*" wildcard form.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return map[string][]string{"*": {s}}, nil
	}
	// Object form with per-key string-or-array values.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(obj))
	for k, v := range obj {
		vals, err := unmarshalStringOrStringArray(v)
		if err != nil {
			return nil, fmt.Errorf("Principal[%s]: %v", k, err)
		}
		out[k] = vals
	}
	return out, nil
}
