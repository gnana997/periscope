package k8s

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

type ListSecretsArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

func ListSecrets(ctx context.Context, p credentials.Provider, args ListSecretsArgs) (SecretList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return SecretList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().Secrets(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return SecretList{}, fmt.Errorf("list secrets: %w", err)
	}

	out := SecretList{Secrets: make([]Secret, 0, len(raw.Items))}
	for _, s := range raw.Items {
		out.Secrets = append(out.Secrets, secretSummary(&s))
	}
	return out, nil
}

type GetSecretArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func GetSecret(ctx context.Context, p credentials.Provider, args GetSecretArgs) (SecretDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return SecretDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().Secrets(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return SecretDetail{}, fmt.Errorf("get secret %s/%s: %w", args.Namespace, args.Name, err)
	}

	keys := make([]SecretKey, 0, len(raw.Data))
	for k, v := range raw.Data {
		keys = append(keys, SecretKey{Name: k, Size: len(v)})
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Name < keys[j].Name })

	immutable := raw.Immutable != nil && *raw.Immutable

	return SecretDetail{
		Secret:      secretSummary(raw),
		Keys:        keys,
		Labels:      raw.Labels,
		Annotations: raw.Annotations,
		Immutable:   immutable,
	}, nil
}

// GetSecretYAMLWithKeys returns the Secret manifest as YAML plus the
// sorted list of data keys present on the Secret. The handler layer
// (cmd/periscope.secretYamlHandler) uses the key list to populate
// the `secret_reveal` audit row so operators see *which* keys were
// exposed by a YAML download, not just that "some secret" was read.
func GetSecretYAMLWithKeys(
	ctx context.Context,
	p credentials.Provider,
	args GetSecretArgs,
) (string, []string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", nil, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().Secrets(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", nil, fmt.Errorf("get secret %s/%s: %w", args.Namespace, args.Name, err)
	}
	keys := make([]string, 0, len(raw.Data))
	for k := range raw.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	raw.APIVersion = "v1"
	raw.Kind = "Secret"
	yaml, err := formatYAML(raw)
	if err != nil {
		return "", nil, err
	}
	return yaml, keys, nil
}

// GetSecretValueArgs identifies a single key of a single secret to read.
type GetSecretValueArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
	Key       string
}

// GetSecretValue returns the decoded bytes of one key of one secret.
// Per GROUND_RULES this is a deliberate, reviewable read action —
// never bundled into other endpoints. Audit emission is the
// handler's responsibility (cmd/periscope.secretRevealHandler) so
// the k8s package has no dependency on the audit pipeline.
func GetSecretValue(ctx context.Context, p credentials.Provider, args GetSecretValueArgs) ([]byte, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().Secrets(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get secret %s/%s: %w", args.Namespace, args.Name, err)
	}
	value, ok := raw.Data[args.Key]
	if !ok {
		return nil, fmt.Errorf("key %q not found in secret %s/%s", args.Key, args.Namespace, args.Name)
	}
	return value, nil
}

func secretSummary(s *corev1.Secret) Secret {
	return Secret{
		Name:      s.Name,
		Namespace: s.Namespace,
		Type:      string(s.Type),
		KeyCount:  len(s.Data),
		CreatedAt: s.CreationTimestamp.Time,
	}
}
