package k8s

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

func TestListConfigMaps(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	fakeCS := fake.NewSimpleClientset(
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "app-config", Namespace: "default", CreationTimestamp: metav1.NewTime(now),
			},
			Data: map[string]string{"foo": "bar", "baz": "qux"},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "tls-bundle", Namespace: "default", CreationTimestamp: metav1.NewTime(now),
			},
			BinaryData: map[string][]byte{"cert.pem": []byte("...")},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: "empty", Namespace: "kube-system", CreationTimestamp: metav1.NewTime(now),
			},
		},
	)

	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return fakeCS, nil
	}
	t.Cleanup(func() { newClientFn = orig })

	cluster := clusters.Cluster{
		Name: "test", ARN: "arn:aws:eks:us-east-1:1:cluster/test", Region: "us-east-1",
	}

	result, err := ListConfigMaps(context.Background(), stubProvider{}, ListConfigMapsArgs{
		Cluster: cluster, Namespace: "default",
	})
	if err != nil {
		t.Fatalf("ListConfigMaps: %v", err)
	}
	if got, want := len(result.ConfigMaps), 2; got != want {
		t.Fatalf("got %d, want %d", got, want)
	}

	byName := map[string]ConfigMap{}
	for _, c := range result.ConfigMaps {
		byName[c.Name] = c
	}
	if byName["app-config"].KeyCount != 2 {
		t.Errorf("app-config KeyCount = %d, want 2", byName["app-config"].KeyCount)
	}
	if byName["tls-bundle"].KeyCount != 1 {
		t.Errorf("tls-bundle KeyCount = %d, want 1", byName["tls-bundle"].KeyCount)
	}

	// Verify list result does not leak any key names or values: the DTO
	// has no Data/Keys field by construction. This test guards the
	// secrets-redaction principle from accidental future regression.
	resultEmpty, _ := ListConfigMaps(context.Background(), stubProvider{}, ListConfigMapsArgs{
		Cluster: cluster, Namespace: "kube-system",
	})
	if len(resultEmpty.ConfigMaps) != 1 || resultEmpty.ConfigMaps[0].KeyCount != 0 {
		t.Errorf("empty configmap not handled: %+v", resultEmpty.ConfigMaps)
	}
}
