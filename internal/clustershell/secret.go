package clustershell

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SecretName returns the Secret name used for the session's
// kubeconfig payload. Exported so other helpers (pod spec builder,
// teardown logic) reference the same name without re-deriving it.
func SecretName(sessionID string) string {
	return "periscope-shell-" + sessionID
}

// SecretKubeconfigKey is the key inside the Secret's Data map where
// the kubeconfig YAML lives. The pod spec mounts only this key,
// renamed to "kubeconfig" inside the container, so changing this
// constant requires updating BuildPodSpec's VolumeMount items.
const SecretKubeconfigKey = "kubeconfig"

// BuildSecret wraps a kubeconfig payload in a corev1.Secret. The
// labels match BuildPodSpec so a teardown sweep can list both the
// Pod and its Secret with a single selector.
func BuildSecret(sessionID, namespace, actorHash, tier string, kubeconfigYAML []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SecretName(sessionID),
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "periscope-shell",
				"app.kubernetes.io/managed-by": "periscope",
				"periscope.io/session-id":      sessionID,
				"periscope.io/tier":            tier,
				"periscope.io/actor-hash":      actorHash,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			SecretKubeconfigKey: kubeconfigYAML,
		},
	}
}
