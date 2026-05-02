// Package secrets resolves secret references at startup. The same
// resolver fronts every secret-bearing config field (currently
// oidc.clientSecret; more fields can hook in later).
//
// The reference syntax is intentionally obvious — operators reading
// auth.yaml should be able to tell what the value points at without
// consulting docs.
//
//	literal             "abc123"                       (literal value)
//	env var             "${OIDC_CLIENT_SECRET}"        (current proc env)
//	file                "file:///etc/periscope/x.txt"  (file contents, trim trailing newline)
//	secrets manager     "aws-secretsmanager://name"    (raw value)
//	                    "aws-secretsmanager://name#key"(JSON-shaped, pull key)
//	ssm parameter       "aws-ssm:///path/to/param"     (SecureString, WithDecryption)
//
// Resolution happens once at startup; rotated secrets require a
// restart. Auto-refresh is a v1.x concern (see RFC 0002 12).
package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// Resolver dispatches a reference string to its backend.
type Resolver struct {
	awsCfg aws.Config

	mu    sync.Mutex
	sm    *secretsmanager.Client
	param *ssm.Client

	cache sync.Map // ref → cachedValue (string)
}

// NewResolver returns a Resolver. The AWS config is used lazily — if
// no aws-secretsmanager:// or aws-ssm:// references appear, no AWS
// clients are constructed and no AWS calls happen.
func NewResolver(awsCfg aws.Config) *Resolver {
	return &Resolver{awsCfg: awsCfg}
}

// Resolve returns the plaintext value for ref. The empty string is a
// valid result for an empty reference (caller decides if that's an
// error in their config).
func (r *Resolver) Resolve(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	if v, ok := r.cache.Load(ref); ok {
		return v.(string), nil
	}
	v, err := r.resolveUncached(ctx, ref)
	if err != nil {
		return "", err
	}
	r.cache.Store(ref, v)
	return v, nil
}

func (r *Resolver) resolveUncached(ctx context.Context, ref string) (string, error) {
	switch {
	case strings.HasPrefix(ref, "${") && strings.HasSuffix(ref, "}"):
		name := ref[2 : len(ref)-1]
		v, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("secrets: env var %q not set (referenced by %q)", name, ref)
		}
		return v, nil
	case strings.HasPrefix(ref, "file://"):
		path := strings.TrimPrefix(ref, "file://")
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("secrets: read %q: %w", path, err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	case strings.HasPrefix(ref, "aws-secretsmanager://"):
		return r.resolveSecretsManager(ctx, strings.TrimPrefix(ref, "aws-secretsmanager://"))
	case strings.HasPrefix(ref, "aws-ssm://"):
		return r.resolveSSM(ctx, strings.TrimPrefix(ref, "aws-ssm://"))
	default:
		// No scheme: treat as a literal. Discouraged in prod but
		// useful in tests and for non-secret config that happens to
		// flow through the resolver.
		return ref, nil
	}
}

func (r *Resolver) resolveSecretsManager(ctx context.Context, body string) (string, error) {
	// Optional JSON key suffix: "secret-id#json-key".
	id, key, _ := strings.Cut(body, "#")
	if id == "" {
		return "", errors.New("secrets: empty secrets-manager id")
	}
	r.mu.Lock()
	if r.sm == nil {
		r.sm = secretsmanager.NewFromConfig(r.awsCfg)
	}
	r.mu.Unlock()

	out, err := r.sm.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &id,
	})
	if err != nil {
		return "", fmt.Errorf("secrets: get secrets-manager %q: %w", id, err)
	}
	val := ""
	switch {
	case out.SecretString != nil:
		val = *out.SecretString
	case len(out.SecretBinary) > 0:
		val = string(out.SecretBinary)
	default:
		return "", fmt.Errorf("secrets: secrets-manager %q has no value", id)
	}
	if key == "" {
		return val, nil
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(val), &obj); err != nil {
		return "", fmt.Errorf("secrets: secrets-manager %q is not JSON (needed key %q): %w", id, key, err)
	}
	v, ok := obj[key]
	if !ok {
		return "", fmt.Errorf("secrets: secrets-manager %q missing key %q", id, key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("secrets: secrets-manager %q key %q is not a string", id, key)
	}
	return s, nil
}

func (r *Resolver) resolveSSM(ctx context.Context, name string) (string, error) {
	if name == "" {
		return "", errors.New("secrets: empty ssm parameter name")
	}
	r.mu.Lock()
	if r.param == nil {
		r.param = ssm.NewFromConfig(r.awsCfg)
	}
	r.mu.Unlock()

	withDecryption := true
	out, err := r.param.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           &name,
		WithDecryption: &withDecryption,
	})
	if err != nil {
		return "", fmt.Errorf("secrets: get ssm %q: %w", name, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", fmt.Errorf("secrets: ssm %q has no value", name)
	}
	return *out.Parameter.Value, nil
}
