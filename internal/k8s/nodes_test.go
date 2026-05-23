package k8s

// nodes_test.go — regression coverage for the Node YAML handler.
// GetNodeYAML must stamp apiVersion/kind: client-go's typed Get()
// returns a Node with empty TypeMeta, and a blank apiVersion/kind
// surfaces in the SPA as a blank Monaco editor on Edit. The node
// route itself was also missing entirely until this fix — see
// cmd/periscope/main.go. This test locks the handler behavior in.

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gnana997/periscope/internal/clusters"
)

func TestGetNodeYAMLSetsTypeMeta(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "ip-10-0-1-23"},
	})
	swapNewClientFn(t, cs)

	yaml, err := GetNodeYAML(context.Background(), stubProvider{}, GetNodeArgs{
		Cluster: clusters.Cluster{Name: "test"}, Name: "ip-10-0-1-23",
	})
	if err != nil {
		t.Fatalf("GetNodeYAML: %v", err)
	}
	if !strings.Contains(yaml, "apiVersion: v1") {
		t.Fatalf("yaml missing apiVersion: v1\n%s", yaml)
	}
	if !strings.Contains(yaml, "kind: Node") {
		t.Fatalf("yaml missing kind: Node\n%s", yaml)
	}
	if !strings.Contains(yaml, "name: ip-10-0-1-23") {
		t.Fatalf("yaml missing node name\n%s", yaml)
	}
}
