package clustershell

import (
	"context"
	"fmt"
	"time"

	authnv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// KubeconfigParams is the typed input for BuildKubeconfig.
type KubeconfigParams struct {
	// SessionID names the context inside the rendered kubeconfig.
	// Surfaces in kubectl `--context` if an operator wants to be
	// explicit; otherwise harmless.
	SessionID string

	// ClusterName names the cluster entry inside the rendered
	// kubeconfig. Doesn't have to match the operator-facing cluster
	// name; pick something short and stable.
	ClusterName string

	// Server is the apiserver URL the kubeconfig points at. For the
	// in-cluster path this is "https://kubernetes.default.svc" so
	// the shell pod talks to its own cluster's apiserver over
	// localhost-ish networking with no external DNS hop.
	Server string

	// CAData is the PEM-encoded CA certificate bundle the kubeconfig
	// trusts. Read from the in-cluster CA mount or the cluster's
	// rest.Config TLSClientConfig.CAData.
	CAData []byte

	// Token is the bearer token kubectl presents on each apiserver
	// call. Minted by MintSessionToken; lifetime matches the
	// session's IdleTimeout.
	Token string

	// Impersonate is the user the apiserver will evaluate RBAC
	// against — set to the operator's OIDC subject so apiserver
	// audit rows carry the human identity, not the per-tier SA.
	Impersonate string

	// ImpersonateGroups is the set of groups attached to the
	// impersonated identity — exactly one entry (the
	// "periscope-tier:<tier>" group), matching the SA's
	// resourceNames-narrow impersonate-on-groups grant.
	ImpersonateGroups []string

	// ImpersonateUserExtra carries the audit annotations that ride
	// into the apiserver's audit log as "Impersonate-Extra-*"
	// headers. The cluster-shell-specific annotations
	// (audit.periscope.io/session-id, audit.periscope.io/actor) let
	// reviewers join Periscope's own audit rows to K8s apiserver
	// audit rows on a single ID.
	ImpersonateUserExtra map[string][]string
}

// BuildKubeconfig serialises a per-session kubeconfig as YAML using
// the clientcmd library — round-tripping through clientcmd.Load() in
// tests is the correctness check that a hand-rolled YAML printer
// would miss (escaping, ordering, default fields, etc.).
//
// The returned bytes are intended to be wrapped in a corev1.Secret
// and mounted into the shell pod at /etc/periscope/kubeconfig.
func BuildKubeconfig(p KubeconfigParams) ([]byte, error) {
	if p.SessionID == "" {
		return nil, fmt.Errorf("kubeconfig: SessionID required")
	}
	if p.ClusterName == "" {
		return nil, fmt.Errorf("kubeconfig: ClusterName required")
	}
	if p.Server == "" {
		return nil, fmt.Errorf("kubeconfig: Server required")
	}
	if p.Token == "" {
		return nil, fmt.Errorf("kubeconfig: Token required")
	}
	if p.Impersonate == "" {
		return nil, fmt.Errorf("kubeconfig: Impersonate required")
	}

	userName := "periscope-session"
	contextName := p.SessionID

	cfg := clientcmdapi.Config{
		Kind:       "Config",
		APIVersion: "v1",
		Clusters: map[string]*clientcmdapi.Cluster{
			p.ClusterName: {
				Server:                   p.Server,
				CertificateAuthorityData: p.CAData,
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			userName: {
				Token:                p.Token,
				Impersonate:          p.Impersonate,
				ImpersonateGroups:    append([]string(nil), p.ImpersonateGroups...),
				ImpersonateUserExtra: copyExtraMap(p.ImpersonateUserExtra),
			},
		},
		Contexts: map[string]*clientcmdapi.Context{
			contextName: {
				Cluster:  p.ClusterName,
				AuthInfo: userName,
			},
		},
		CurrentContext: contextName,
	}

	return clientcmd.Write(cfg)
}

// MintSessionToken calls TokenRequest on the per-tier ServiceAccount
// in the configured cluster-shell namespace and returns the resulting
// bearer token. v1.2 mints an unbound (TTL-only) token — simpler
// ordering, no Pod chicken-and-egg.
//
// The Apiserver enforces a 10-minute floor on ExpirationSeconds;
// LoadConfig clamps cfg.TokenTTL to honor this.
//
// An optional BoundObjectRef would tie the token to a specific Pod's
// UID so apiserver invalidates the token the moment the pod is
// deleted (vs. waiting for TTL). Deferred to v1.3 along with the
// "create-pod-then-mint-token-then-create-secret" ordering it
// requires.
func MintSessionToken(ctx context.Context, cs kubernetes.Interface, namespace, serviceAccountName string, ttl time.Duration) (string, error) {
	seconds := int64(ttl.Seconds())
	tr := &authnv1.TokenRequest{
		Spec: authnv1.TokenRequestSpec{
			// Audiences left nil → defaults to the apiserver.
			ExpirationSeconds: &seconds,
		},
	}
	out, err := cs.CoreV1().ServiceAccounts(namespace).CreateToken(ctx, serviceAccountName, tr, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("create token for SA %s/%s: %w", namespace, serviceAccountName, err)
	}
	if out.Status.Token == "" {
		return "", fmt.Errorf("TokenRequest returned an empty token for SA %s/%s", namespace, serviceAccountName)
	}
	return out.Status.Token, nil
}

func copyExtraMap(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}
