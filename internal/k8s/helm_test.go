package k8s

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	testingaction "k8s.io/client-go/testing"

	"github.com/gnana997/periscope/internal/clusters"
)

// makeHelmReleaseBlob produces the storage-blob shape Helm writes
// into Secret.data["release"] / ConfigMap.data["release"]:
//
//	base64(gzip(json(*Release)))
//
// The K8s clientset already does the outer base64 decode for Secrets,
// so what decodeHelmRelease receives matches what this returns.
func makeHelmReleaseBlob(t *testing.T, rel helmRelease) []byte {
	t.Helper()
	body, err := json.Marshal(rel)
	if err != nil {
		t.Fatalf("marshal release: %v", err)
	}
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	if _, err := w.Write(body); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return []byte(base64.StdEncoding.EncodeToString(gz.Bytes()))
}

func TestDecodeHelmRelease_Roundtrip(t *testing.T) {
	in := helmRelease{
		Name:      "traefik",
		Namespace: "kube-system",
		Version:   4,
		Manifest:  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: traefik\n",
		Info: &helmReleaseInfo{
			Status:       "deployed",
			Description:  "Upgrade complete",
			LastDeployed: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			Notes:        "thanks for installing",
		},
		Chart: &helmChart{Metadata: &helmChartMetadata{
			Name:       "traefik",
			Version:    "26.1.0",
			AppVersion: "v3.0.0",
			Icon:       "https://example.com/traefik.png",
		}},
		Config: map[string]interface{}{"replicas": 3},
	}
	blob := makeHelmReleaseBlob(t, in)

	got, err := decodeHelmRelease(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != in.Name || got.Namespace != in.Namespace || got.Version != in.Version {
		t.Errorf("identity drift: got %+v", got)
	}
	if got.Info == nil || got.Info.Status != "deployed" || got.Info.Description != in.Info.Description {
		t.Errorf("info: got %+v", got.Info)
	}
	if got.Chart == nil || got.Chart.Metadata == nil || got.Chart.Metadata.Name != "traefik" {
		t.Errorf("chart: got %+v", got.Chart)
	}
	if got.Manifest != in.Manifest {
		t.Errorf("manifest drift")
	}
}

func TestDecodeHelmRelease_RejectsEmpty(t *testing.T) {
	if _, err := decodeHelmRelease(nil); err == nil {
		t.Error("expected error on empty blob")
	}
	if _, err := decodeHelmRelease([]byte("not-base64!!!")); err == nil {
		t.Error("expected error on garbage blob")
	}
}

func TestParseManifestObjects(t *testing.T) {
	manifest := `# Source: traefik/templates/sa.yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: traefik
  namespace: ingress
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: traefik
---
# empty doc below — should be skipped
---
apiVersion: v1
kind: Service
metadata:
  name: traefik
  namespace: ingress
`
	objs := parseManifestObjects(manifest, "kube-system")
	if len(objs) != 3 {
		t.Fatalf("expected 3 objects, got %d: %+v", len(objs), objs)
	}

	// First doc: ns set explicitly → that wins.
	if objs[0].Kind != "ServiceAccount" || objs[0].Namespace != "ingress" {
		t.Errorf("obj0: %+v", objs[0])
	}
	// Second doc: no namespace → falls back to release namespace.
	if objs[1].Kind != "Deployment" || objs[1].Namespace != "kube-system" {
		t.Errorf("obj1: %+v", objs[1])
	}
	// Third doc: explicit ns again.
	if objs[2].Kind != "Service" || objs[2].Namespace != "ingress" {
		t.Errorf("obj2: %+v", objs[2])
	}
}

func TestParseManifestObjects_Empty(t *testing.T) {
	if got := parseManifestObjects("", "default"); len(got) != 0 {
		t.Errorf("expected empty, got %+v", got)
	}
}

func TestStorageObjectName(t *testing.T) {
	if got := storageObjectName("traefik", 4); got != "sh.helm.release.v1.traefik.v4" {
		t.Errorf("storageObjectName: %q", got)
	}
}

func TestDiffYAMLDocuments(t *testing.T) {
	from := "image: nginx:1.19\nport: 80\n"
	to := "image: nginx:1.20\nport: 80\n"
	items, err := diffYAMLDocuments(from, to)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	// dyff should report exactly one modify on /image.
	if len(items) == 0 {
		t.Fatal("expected at least one diff item")
	}
	found := false
	for _, it := range items {
		if it.Kind == "modify" && it.Before == "nginx:1.19" && it.After == "nginx:1.20" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected modify nginx:1.19→nginx:1.20, got %+v", items)
	}
}

func TestParseManifestObjects_SkipsMalformedDocInMiddle(t *testing.T) {
	// Repro for the bug fixed in this PR: pre-fix the decoder broke on the
	// first malformed YAML doc and dropped every doc after it. Post-fix
	// each doc is decoded independently, so a single bad doc loses only
	// itself.
	manifest := `apiVersion: v1
kind: ServiceAccount
metadata:
  name: good-before
  namespace: ns-a
---
this: is: not: valid: yaml: at: all
  bad indentation
    even worse
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: good-after
  namespace: ns-b
---
apiVersion: v1
kind: Service
metadata:
  name: also-good-after
`
	objs := parseManifestObjects(manifest, "default")
	// Expect the SA, Deployment, and Service — three valid docs — even
	// though the middle doc is malformed.
	if len(objs) != 3 {
		t.Fatalf("expected 3 objects (one bad doc skipped), got %d: %+v", len(objs), objs)
	}
	if objs[0].Name != "good-before" || objs[1].Name != "good-after" || objs[2].Name != "also-good-after" {
		t.Errorf("wrong objects (the docs after the bad one were dropped?): %+v", objs)
	}
}

func TestSplitYAMLDocs(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantLen int
	}{
		{"empty", "", 0},
		{"single doc, no separator", "kind: Pod\nname: a\n", 1},
		{"two docs", "kind: A\n---\nkind: B\n", 2},
		{"leading separator", "---\nkind: A\n---\nkind: B\n", 2},
		{"trailing separator", "kind: A\n---\nkind: B\n---\n", 2},
		{"separator with trailing whitespace", "kind: A\n---  \nkind: B\n", 2},
		{"empty doc between", "kind: A\n---\n---\nkind: B\n", 2},
		{"nested --- inside string is NOT a separator", "data: |\n  one\n  ---\n  two\nkind: ConfigMap\n", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitYAMLDocs(tc.in)
			if len(got) != tc.wantLen {
				t.Errorf("splitYAMLDocs %q: got %d docs, want %d: %+v", tc.in, len(got), tc.wantLen, got)
			}
		})
	}
}

func TestResolveHelmDriver_SecretsForbiddenFallsThroughToConfigMap(t *testing.T) {
	// Repro for the bug: pre-fix, a 403 on Secrets list short-circuited
	// to "secret" default, hiding any ConfigMap-driver releases the user
	// CAN see. Post-fix, 403/Unauthorized on Secrets falls through to the
	// ConfigMap probe.
	//
	// Reset cache so test results don't leak between runs.
	helmDriverCacheMu.Lock()
	delete(helmDriverCache, "test-cluster-403")
	helmDriverCacheMu.Unlock()

	cs := fake.NewClientset()
	// Secrets list → 403 (PrependReactor to inject Forbidden response).
	cs.PrependReactor("list", "secrets", func(action testingaction.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Resource: "secrets"},
			"",
			errors.New("RBAC: forbidden"),
		)
	})
	// Plant a configmap with the helm owner label so the ConfigMap probe finds something.
	if _, err := cs.CoreV1().ConfigMaps("default").Create(
		context.Background(),
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "sh.helm.release.v1.test.v1",
				Namespace: "default",
				Labels:    map[string]string{"owner": "helm", "name": "test", "version": "1"},
			},
			Data: map[string]string{"release": "stub"},
		},
		metav1.CreateOptions{},
	); err != nil {
		t.Fatalf("seed configmap: %v", err)
	}

	drv, err := resolveHelmDriver(
		context.Background(), cs,
		clusters.Cluster{Name: "test-cluster-403"},
	)
	if err != nil {
		t.Fatalf("resolveHelmDriver: %v", err)
	}
	if drv != "configmap" {
		t.Errorf("expected ConfigMap probe to succeed when Secrets is 403; got driver=%q", drv)
	}
}

func TestResolveHelmDriver_NetworkErrorShortCircuits(t *testing.T) {
	// For non-permission errors (network, timeout), keep the existing
	// short-circuit: don't waste another LIST round-trip on the
	// ConfigMap probe.
	helmDriverCacheMu.Lock()
	delete(helmDriverCache, "test-cluster-network")
	helmDriverCacheMu.Unlock()

	cs := fake.NewClientset()
	cs.PrependReactor("list", "secrets", func(action testingaction.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})
	// Plant a ConfigMap that WOULD be picked up if we fell through. The
	// test asserts we DON'T fall through for network errors.
	if _, err := cs.CoreV1().ConfigMaps("default").Create(
		context.Background(),
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "sh.helm.release.v1.test.v1",
				Namespace: "default",
				Labels:    map[string]string{"owner": "helm"},
			},
		},
		metav1.CreateOptions{},
	); err != nil {
		t.Fatalf("seed configmap: %v", err)
	}

	drv, err := resolveHelmDriver(
		context.Background(), cs,
		clusters.Cluster{Name: "test-cluster-network"},
	)
	if err != nil {
		t.Fatalf("resolveHelmDriver: %v", err)
	}
	if drv != "secret" {
		t.Errorf("expected secret default for network error; got %q", drv)
	}
}
