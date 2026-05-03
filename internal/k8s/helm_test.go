package k8s

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
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
