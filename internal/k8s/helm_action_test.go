package k8s

import (
	"testing"

	"k8s.io/client-go/rest"
)

// TestSimpleRESTClientGetter_ToRESTConfig confirms the getter returns
// the rest.Config we hand it untouched. Helm's pkg/action.Configuration
// downstream relies on this — if we mutated the config (e.g. cleared
// the impersonation block) it would silently break privilege
// propagation through every helm SDK call.
func TestSimpleRESTClientGetter_ToRESTConfig(t *testing.T) {
	cfg := &rest.Config{
		Host: "https://kube.example",
		Impersonate: rest.ImpersonationConfig{
			UserName: "alice@corp",
			Groups:   []string{"periscope-tier:read"},
		},
		BearerToken: "test-token",
	}
	g := &simpleRESTClientGetter{cfg: cfg, namespace: "test-ns"}

	got, err := g.ToRESTConfig()
	if err != nil {
		t.Fatalf("ToRESTConfig: %v", err)
	}
	if got != cfg {
		t.Errorf("ToRESTConfig returned a different config pointer; helm SDK requires the same config so impersonation propagates")
	}
	if got.Impersonate.UserName != "alice@corp" {
		t.Errorf("impersonation username dropped: %+v", got.Impersonate)
	}
	if len(got.Impersonate.Groups) != 1 || got.Impersonate.Groups[0] != "periscope-tier:read" {
		t.Errorf("impersonation groups dropped: %+v", got.Impersonate.Groups)
	}
}

// TestSimpleRESTClientGetter_ToRawKubeConfigLoader confirms the raw
// kubeconfig loader returns a non-nil ClientConfig. Helm calls this
// at action.Configuration.Init time even when it doesn't end up
// using the kubeconfig data; returning nil would panic helm SDK.
func TestSimpleRESTClientGetter_ToRawKubeConfigLoader(t *testing.T) {
	cfg := &rest.Config{Host: "https://kube.example"}
	g := &simpleRESTClientGetter{cfg: cfg, namespace: "ns"}

	loader := g.ToRawKubeConfigLoader()
	if loader == nil {
		t.Fatal("ToRawKubeConfigLoader returned nil; helm SDK Init will panic")
	}
}

// TestSimpleRESTClientGetter_NamespaceField confirms the namespace
// field round-trips through the struct. Helm uses this only as a
// hint for storage scoping; we don't depend on it for auth.
func TestSimpleRESTClientGetter_NamespaceField(t *testing.T) {
	cfg := &rest.Config{Host: "https://kube.example"}
	g := &simpleRESTClientGetter{cfg: cfg, namespace: "team-a"}
	if g.namespace != "team-a" {
		t.Errorf("namespace field = %q, want %q", g.namespace, "team-a")
	}
}

// TestHelmDebugSilent verifies the no-op debug logger doesn't panic
// on typical helm debug call shapes. Helm SDK calls debug() at
// arbitrary points during Init / Run; if our debug func panicked,
// every preview would 500.
func TestHelmDebugSilent(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("helmDebugSilent panicked: %v", r)
		}
	}()
	helmDebugSilent("plain message")
	helmDebugSilent("with %d args", 42)
	helmDebugSilent("with %s and %v", "string", map[string]int{"a": 1})
	helmDebugSilent("")
}
