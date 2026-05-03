package k8s

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	testingaction "k8s.io/client-go/testing"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
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

func TestListHelmReleases_UsesNotInSelector(t *testing.T) {
	// The list path must scope the selector to exclude superseded /
	// uninstalled / uninstalling so the API server filters out the bulk
	// of revision blobs at the source instead of after wire transfer.
	helmDriverCacheMu.Lock()
	delete(helmDriverCache, "selector-cluster")
	helmDriverCacheMu.Unlock()

	cs := fake.NewClientset()
	var captured string
	cs.PrependReactor("list", "secrets", func(action testingaction.Action) (bool, runtime.Object, error) {
		la, ok := action.(testingaction.ListAction)
		if !ok {
			return false, nil, nil
		}
		// Only capture the *non-probe* list — the driver probe uses
		// Limit:1 with the bare owner-helm label. The browser LIST
		// uses no Limit and the full helmListSelector.
		if la.GetListRestrictions().Labels.String() != helmOwnerLabel {
			captured = la.GetListRestrictions().Labels.String()
		}
		return true, &corev1.SecretList{}, nil
	})

	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return cs, nil
	}
	t.Cleanup(func() { newClientFn = orig })

	if _, _, err := ListHelmReleases(context.Background(), stubProvider{}, clusters.Cluster{Name: "selector-cluster"}, 50); err != nil {
		t.Fatalf("ListHelmReleases: %v", err)
	}

	for _, want := range []string{"owner=helm", "status notin", "superseded", "uninstalled", "uninstalling"} {
		if !strings.Contains(captured, want) {
			t.Errorf("LIST selector %q missing %q", captured, want)
		}
	}
}

func TestListHelmReleases_FanOutOnClusterWide403(t *testing.T) {
	// User has no cluster-wide `list secrets` (the common namespace-
	// scoped RBAC posture). The list path must (a) detect 403,
	// (b) discover namespaces, (c) fan out per-namespace, and
	// (d) surface releases from namespaces the user CAN see.
	helmDriverCacheMu.Lock()
	delete(helmDriverCache, "fanout-cluster")
	helmDriverCacheMu.Unlock()

	cs := fake.NewClientset()

	// Cluster-wide secret list → 403; per-namespace lists are allowed.
	cs.PrependReactor("list", "secrets", func(action testingaction.Action) (bool, runtime.Object, error) {
		if action.GetNamespace() == "" {
			return true, nil, apierrors.NewForbidden(
				schema.GroupResource{Resource: "secrets"}, "",
				errors.New("RBAC: cluster-wide list denied"),
			)
		}
		// Defer to the default tracker for namespace-scoped lists.
		return false, nil, nil
	})

	// Plant two namespaces; only the second has a release.
	for _, ns := range []string{"team-a", "team-b"} {
		if _, err := cs.CoreV1().Namespaces().Create(
			context.Background(),
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}},
			metav1.CreateOptions{},
		); err != nil {
			t.Fatalf("seed namespace %s: %v", ns, err)
		}
	}

	rel := helmRelease{
		Name:      "my-app",
		Namespace: "team-b",
		Version:   3,
		Info: &helmReleaseInfo{
			Status:       "deployed",
			LastDeployed: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		},
		Chart: &helmChart{Metadata: &helmChartMetadata{
			Name: "my-app", Version: "1.0.0", AppVersion: "v1",
		}},
	}
	blob := makeHelmReleaseBlob(t, rel)
	if _, err := cs.CoreV1().Secrets("team-b").Create(
		context.Background(),
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "sh.helm.release.v1.my-app.v3",
				Namespace: "team-b",
				Labels: map[string]string{
					"owner":   "helm",
					"name":    "my-app",
					"version": "3",
					"status":  "deployed",
				},
			},
			Data: map[string][]byte{"release": blob},
		},
		metav1.CreateOptions{},
	); err != nil {
		t.Fatalf("seed release secret: %v", err)
	}

	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return cs, nil
	}
	t.Cleanup(func() { newClientFn = orig })

	got, truncated, err := ListHelmReleases(
		context.Background(), stubProvider{},
		clusters.Cluster{Name: "fanout-cluster"}, 50,
	)
	if err != nil {
		t.Fatalf("ListHelmReleases: %v", err)
	}
	if truncated {
		t.Errorf("unexpected truncated=true")
	}
	if len(got) != 1 {
		t.Fatalf("got %d releases, want 1: %+v", len(got), got)
	}
	if got[0].Name != "my-app" || got[0].Namespace != "team-b" || got[0].Revision != 3 {
		t.Errorf("wrong release: %+v", got[0])
	}
}

func TestListHelmReleases_NamespaceListAlsoForbidden(t *testing.T) {
	// When the user is denied cluster-wide list AND cannot list
	// namespaces, surface the original 403. Without a candidate
	// namespace set there's nothing useful we can fan out to.
	helmDriverCacheMu.Lock()
	delete(helmDriverCache, "ns-403-cluster")
	helmDriverCacheMu.Unlock()

	cs := fake.NewClientset()
	cs.PrependReactor("list", "secrets", func(action testingaction.Action) (bool, runtime.Object, error) {
		if action.GetNamespace() == "" {
			return true, nil, apierrors.NewForbidden(
				schema.GroupResource{Resource: "secrets"}, "",
				errors.New("RBAC: cluster-wide list denied"),
			)
		}
		return false, nil, nil
	})
	cs.PrependReactor("list", "namespaces", func(action testingaction.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Resource: "namespaces"}, "",
			errors.New("RBAC: list namespaces denied"),
		)
	})

	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return cs, nil
	}
	t.Cleanup(func() { newClientFn = orig })

	_, _, err := ListHelmReleases(
		context.Background(), stubProvider{},
		clusters.Cluster{Name: "ns-403-cluster"}, 50,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !apierrors.IsForbidden(err) {
		t.Errorf("expected forbidden error, got %v", err)
	}
}

func TestListHelmReleases_PartialFanOutFailureReturnsWhatWeHave(t *testing.T) {
	// Mixed-RBAC posture: user can list in team-a but is denied in
	// team-b. The fan-out drops 403s silently and returns whatever
	// succeeded — operator's UX shouldn't be all-or-nothing.
	helmDriverCacheMu.Lock()
	delete(helmDriverCache, "partial-cluster")
	helmDriverCacheMu.Unlock()

	cs := fake.NewClientset()
	cs.PrependReactor("list", "secrets", func(action testingaction.Action) (bool, runtime.Object, error) {
		switch action.GetNamespace() {
		case "":
			return true, nil, apierrors.NewForbidden(
				schema.GroupResource{Resource: "secrets"}, "",
				errors.New("RBAC: cluster-wide list denied"),
			)
		case "team-b":
			return true, nil, apierrors.NewForbidden(
				schema.GroupResource{Resource: "secrets"}, "",
				errors.New("RBAC: namespace list denied"),
			)
		}
		return false, nil, nil
	})

	for _, ns := range []string{"team-a", "team-b"} {
		if _, err := cs.CoreV1().Namespaces().Create(
			context.Background(),
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}},
			metav1.CreateOptions{},
		); err != nil {
			t.Fatalf("seed namespace %s: %v", ns, err)
		}
	}

	blob := makeHelmReleaseBlob(t, helmRelease{
		Name: "ok-app", Namespace: "team-a", Version: 1,
		Info: &helmReleaseInfo{Status: "deployed"},
	})
	if _, err := cs.CoreV1().Secrets("team-a").Create(
		context.Background(),
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "sh.helm.release.v1.ok-app.v1",
				Namespace: "team-a",
				Labels:    map[string]string{"owner": "helm", "name": "ok-app", "version": "1", "status": "deployed"},
			},
			Data: map[string][]byte{"release": blob},
		},
		metav1.CreateOptions{},
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return cs, nil
	}
	t.Cleanup(func() { newClientFn = orig })

	got, _, err := ListHelmReleases(
		context.Background(), stubProvider{},
		clusters.Cluster{Name: "partial-cluster"}, 50,
	)
	if err != nil {
		t.Fatalf("ListHelmReleases: %v", err)
	}
	if len(got) != 1 || got[0].Name != "ok-app" {
		t.Errorf("expected one release from team-a, got %+v", got)
	}
}

func TestListHelmReleases_AllNamespaces403_ReturnsForbidden(t *testing.T) {
	// User has cluster-wide `list namespaces` (so we get into the
	// fan-out path) but no `list secrets` in ANY namespace. Every
	// per-namespace LIST returns 403 → the fan-out must surface that
	// 403 rather than masquerading as a 200-empty result, so the
	// frontend can render ForbiddenState instead of "no helm releases".
	helmDriverCacheMu.Lock()
	delete(helmDriverCache, "all-403-cluster")
	helmDriverCacheMu.Unlock()

	cs := fake.NewClientset()
	cs.PrependReactor("list", "secrets", func(action testingaction.Action) (bool, runtime.Object, error) {
		// Cluster-wide AND namespace-scoped LISTs both denied.
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Resource: "secrets"},
			"",
			errors.New("RBAC: forbidden"),
		)
	})

	for _, ns := range []string{"team-a", "team-b", "team-c"} {
		if _, err := cs.CoreV1().Namespaces().Create(
			context.Background(),
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}},
			metav1.CreateOptions{},
		); err != nil {
			t.Fatalf("seed namespace %s: %v", ns, err)
		}
	}

	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return cs, nil
	}
	t.Cleanup(func() { newClientFn = orig })

	_, _, err := ListHelmReleases(
		context.Background(), stubProvider{},
		clusters.Cluster{Name: "all-403-cluster"}, 50,
	)
	if err == nil {
		t.Fatal("expected forbidden error when every namespace LIST is 403, got nil")
	}
	if !apierrors.IsForbidden(err) {
		t.Errorf("expected forbidden, got %v", err)
	}
}

func TestListHelmReleases_PartialEmptyIsNotForbidden(t *testing.T) {
	// Mixed posture: user can list secrets in team-a (empty — no helm
	// releases planted) and is denied in team-b. This is "the user has
	// helm visibility somewhere, just no releases there" — must NOT
	// be reported as forbidden, since it would hide the genuine
	// empty-state diagnostic from the operator.
	helmDriverCacheMu.Lock()
	delete(helmDriverCache, "partial-empty-cluster")
	helmDriverCacheMu.Unlock()

	cs := fake.NewClientset()
	cs.PrependReactor("list", "secrets", func(action testingaction.Action) (bool, runtime.Object, error) {
		switch action.GetNamespace() {
		case "":
			return true, nil, apierrors.NewForbidden(
				schema.GroupResource{Resource: "secrets"}, "",
				errors.New("RBAC: cluster-wide list denied"),
			)
		case "team-b":
			return true, nil, apierrors.NewForbidden(
				schema.GroupResource{Resource: "secrets"}, "",
				errors.New("RBAC: namespace list denied"),
			)
		}
		// team-a: defer to default tracker (empty result).
		return false, nil, nil
	})

	for _, ns := range []string{"team-a", "team-b"} {
		if _, err := cs.CoreV1().Namespaces().Create(
			context.Background(),
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}},
			metav1.CreateOptions{},
		); err != nil {
			t.Fatalf("seed namespace %s: %v", ns, err)
		}
	}

	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return cs, nil
	}
	t.Cleanup(func() { newClientFn = orig })

	got, _, err := ListHelmReleases(
		context.Background(), stubProvider{},
		clusters.Cluster{Name: "partial-empty-cluster"}, 50,
	)
	if err != nil {
		t.Fatalf("expected nil error for partial-empty case, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty list, got %+v", got)
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
