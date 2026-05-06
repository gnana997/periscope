package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/storage/driver"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// previewHandlerInvoke wraps invokeAuthenticated with the path params
// the install/upgrade preview handlers read from chi. Returns the
// recorder + sink so tests can assert response status, body, and
// audit emission in one shot.
func previewHandlerInvoke(
	t *testing.T,
	makeHandler func(*audit.Emitter) credentials.Handler,
	method, url string,
	pathParams map[string]string,
	body []byte,
) (*httptest.ResponseRecorder, *recordingSink) {
	t.Helper()
	if pathParams == nil {
		pathParams = map[string]string{"cluster": "test"}
	} else if _, ok := pathParams["cluster"]; !ok {
		pathParams["cluster"] = "test"
	}
	return invokeAuthenticated(t, makeHandler, method, url, pathParams, body)
}

// fakePreviewResult is the canned successful response substituted in
// place of k8s.PreviewHelm{Install,Upgrade} for happy-path tests.
var fakePreviewResult = &k8s.PreviewResult{
	Manifests: []k8s.HelmManifestObject{
		{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "app-ns", Name: "web"},
		{APIVersion: "v1", Kind: "ConfigMap", Namespace: "app-ns", Name: "web-config"},
	},
}

// withPreviewInstallFn substitutes the install preview seam for the
// duration of the test. Cleanup restores the original.
func withPreviewInstallFn(t *testing.T, fn func(context.Context, credentials.Provider, clusters.Cluster, k8s.PreviewArgs) (*k8s.PreviewResult, error)) {
	t.Helper()
	prev := previewHelmInstallFn
	previewHelmInstallFn = fn
	t.Cleanup(func() { previewHelmInstallFn = prev })
}

func withPreviewUpgradeFn(t *testing.T, fn func(context.Context, credentials.Provider, clusters.Cluster, k8s.PreviewArgs) (*k8s.PreviewResult, error)) {
	t.Helper()
	prev := previewHelmUpgradeFn
	previewHelmUpgradeFn = fn
	t.Cleanup(func() { previewHelmUpgradeFn = prev })
}

// ── Install preview handler ───────────────────────────────────────

func TestHelmInstallPreviewHandler_HappyPath(t *testing.T) {
	withPreviewInstallFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, args k8s.PreviewArgs) (*k8s.PreviewResult, error) {
		// Verify the args flow through — handler must pass through
		// every field unchanged.
		if args.Ref != "oci://example/c" || args.Version != "1.0.0" ||
			args.Namespace != "app" || args.ReleaseName != "web" {
			t.Errorf("unexpected args passed to PreviewHelmInstall: %+v", args)
		}
		return fakePreviewResult, nil
	})

	reg := testRegistry(t)
	body := mustJSON(t, helmInstallPreviewRequest{
		Ref: "oci://example/c", Version: "1.0.0", Namespace: "app", ReleaseName: "web",
	})
	rec, sink := previewHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmInstallPreviewHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/install-preview", nil, body,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got k8s.PreviewResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Manifests) != 2 {
		t.Errorf("expected 2 manifests, got %d", len(got.Manifests))
	}
	// Audit row spot-check.
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	if events[0].Verb != audit.VerbHelmPreview {
		t.Errorf("audit verb = %q, want %q", events[0].Verb, audit.VerbHelmPreview)
	}
	if events[0].Outcome != audit.OutcomeSuccess {
		t.Errorf("audit outcome = %q, want success", events[0].Outcome)
	}
	if op, _ := events[0].Extra["op"].(string); op != "install" {
		t.Errorf("audit Extra.op = %v, want \"install\"", events[0].Extra["op"])
	}
}

func TestHelmInstallPreviewHandler_ValidationMissingFields(t *testing.T) {
	cases := []struct {
		name string
		body any
	}{
		{"empty ref", helmInstallPreviewRequest{Version: "1.0.0", Namespace: "app", ReleaseName: "web"}},
		{"empty version", helmInstallPreviewRequest{Ref: "oci://x/c", Namespace: "app", ReleaseName: "web"}},
		{"empty namespace", helmInstallPreviewRequest{Ref: "oci://x/c", Version: "1.0.0", ReleaseName: "web"}},
		{"empty releaseName", helmInstallPreviewRequest{Ref: "oci://x/c", Version: "1.0.0", Namespace: "app"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := testRegistry(t)
			body := mustJSON(t, tc.body)
			rec, _ := previewHandlerInvoke(t,
				func(e *audit.Emitter) credentials.Handler { return helmInstallPreviewHandler(reg, e) },
				http.MethodPost, "/api/clusters/test/helm/install-preview", nil, body,
			)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestHelmInstallPreviewHandler_MalformedBody(t *testing.T) {
	reg := testRegistry(t)
	rec, _ := previewHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmInstallPreviewHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/install-preview", nil,
		[]byte("{not json"),
	)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHelmInstallPreviewHandler_UnknownCluster(t *testing.T) {
	reg := testRegistry(t)
	body := mustJSON(t, helmInstallPreviewRequest{
		Ref: "oci://example/c", Version: "1.0.0", Namespace: "app", ReleaseName: "web",
	})
	rec, _ := previewHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmInstallPreviewHandler(reg, e) },
		http.MethodPost, "/api/clusters/missing/helm/install-preview",
		map[string]string{"cluster": "missing"}, body,
	)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHelmInstallPreviewHandler_DeniedMarksAuditOutcomeDenied(t *testing.T) {
	withPreviewInstallFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ k8s.PreviewArgs) (*k8s.PreviewResult, error) {
		return &k8s.PreviewResult{
			Manifests: []k8s.HelmManifestObject{{APIVersion: "v1", Kind: "Pod", Name: "x"}},
			Denied: []k8s.PreviewDenial{
				{Group: "", Resource: "pods", Verb: "create", Reason: "denied"},
			},
		}, nil
	})

	reg := testRegistry(t)
	body := mustJSON(t, helmInstallPreviewRequest{
		Ref: "oci://example/c", Version: "1.0.0", Namespace: "app", ReleaseName: "web",
	})
	rec, sink := previewHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmInstallPreviewHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/install-preview", nil, body,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 even on denial; got %d body=%s", rec.Code, rec.Body.String())
	}
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	if events[0].Outcome != audit.OutcomeDenied {
		t.Errorf("audit outcome = %q, want denied", events[0].Outcome)
	}
}

func TestHelmInstallPreviewHandler_FailureMappedAndAudited(t *testing.T) {
	cases := []struct {
		name           string
		err            error
		wantStatus     int
		wantCodeSubstr string
	}{
		{"chart not found → 404", k8s.ErrChartNotFound, http.StatusNotFound, "E_CHART_NOT_FOUND"},
		{"chart unsupported deps → 422", k8s.ErrChartUnsupportedDeps, http.StatusUnprocessableEntity, "E_CHART_UNSUPPORTED_DEPS"},
		{"chart invalid → 422", k8s.ErrChartInvalid, http.StatusUnprocessableEntity, "E_CHART_INVALID"},
		{"chart timeout → 504", k8s.ErrChartTimeout, http.StatusGatewayTimeout, "E_CHART_TIMEOUT"},
		{"helm release not found → 404", driver.ErrReleaseNotFound, http.StatusNotFound, "E_HELM_RELEASE_NOT_FOUND"},
		{"helm no deployed → 422", driver.ErrNoDeployedReleases, http.StatusUnprocessableEntity, "E_HELM_NO_DEPLOYED_RELEASES"},
		{"unknown helm error → 502", errors.New("unknown helm error"), http.StatusBadGateway, "E_HELM_SDK"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withPreviewInstallFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ k8s.PreviewArgs) (*k8s.PreviewResult, error) {
				return nil, tc.err
			})
			reg := testRegistry(t)
			body := mustJSON(t, helmInstallPreviewRequest{
				Ref: "oci://example/c", Version: "1.0.0", Namespace: "app", ReleaseName: "web",
			})
			rec, sink := previewHandlerInvoke(t,
				func(e *audit.Emitter) credentials.Handler { return helmInstallPreviewHandler(reg, e) },
				http.MethodPost, "/api/clusters/test/helm/install-preview", nil, body,
			)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body=%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantCodeSubstr) {
				t.Errorf("body missing code %q: %s", tc.wantCodeSubstr, rec.Body.String())
			}
			events := sink.snapshot()
			if len(events) != 1 {
				t.Fatalf("expected 1 audit event, got %d", len(events))
			}
			if events[0].Outcome != audit.OutcomeFailure {
				t.Errorf("audit outcome = %q, want failure", events[0].Outcome)
			}
		})
	}
}

// ── Upgrade preview handler ───────────────────────────────────────

func TestHelmUpgradePreviewHandler_HappyPath(t *testing.T) {
	upgradeResult := &k8s.PreviewResult{
		Manifests: fakePreviewResult.Manifests,
		Diff: &k8s.HelmDiff{
			From: k8s.HelmDiffSide{YAML: "from yaml"},
			To:   k8s.HelmDiffSide{YAML: "to yaml"},
		},
	}
	withPreviewUpgradeFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, args k8s.PreviewArgs) (*k8s.PreviewResult, error) {
		if args.Namespace != "app" || args.ReleaseName != "web" {
			t.Errorf("upgrade handler should populate ns/name from URL: %+v", args)
		}
		if args.Ref != "oci://example/c" || args.Version != "1.1.0" {
			t.Errorf("upgrade body fields lost: %+v", args)
		}
		return upgradeResult, nil
	})

	reg := testRegistry(t)
	body := mustJSON(t, helmUpgradePreviewRequest{Ref: "oci://example/c", Version: "1.1.0"})
	rec, sink := previewHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmUpgradePreviewHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/releases/app/web/upgrade-preview",
		map[string]string{"cluster": "test", "ns": "app", "name": "web"}, body,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got k8s.PreviewResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Diff == nil {
		t.Error("expected non-nil diff for upgrade preview")
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].Verb != audit.VerbHelmPreview {
		t.Fatalf("expected one helm_preview audit row, got %+v", events)
	}
	if op, _ := events[0].Extra["op"].(string); op != "upgrade" {
		t.Errorf("audit Extra.op = %v, want \"upgrade\"", events[0].Extra["op"])
	}
}

func TestHelmUpgradePreviewHandler_ValidationMissingFields(t *testing.T) {
	reg := testRegistry(t)
	body := mustJSON(t, helmUpgradePreviewRequest{}) // both ref and version empty
	rec, _ := previewHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmUpgradePreviewHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/releases/app/web/upgrade-preview",
		map[string]string{"cluster": "test", "ns": "app", "name": "web"}, body,
	)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHelmUpgradePreviewHandler_MissingPathParams(t *testing.T) {
	reg := testRegistry(t)
	body := mustJSON(t, helmUpgradePreviewRequest{Ref: "oci://x/c", Version: "1.0"})
	// Empty ns / name path params — handler must reject.
	rec, _ := previewHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmUpgradePreviewHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/releases//web/upgrade-preview",
		map[string]string{"cluster": "test", "ns": "", "name": "web"}, body,
	)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for empty ns", rec.Code)
	}
}

func TestHelmUpgradePreviewHandler_FailureBubbles(t *testing.T) {
	withPreviewUpgradeFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ k8s.PreviewArgs) (*k8s.PreviewResult, error) {
		return nil, driver.ErrReleaseNotFound
	})
	reg := testRegistry(t)
	body := mustJSON(t, helmUpgradePreviewRequest{Ref: "oci://x/c", Version: "1.0"})
	rec, sink := previewHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmUpgradePreviewHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/releases/app/web/upgrade-preview",
		map[string]string{"cluster": "test", "ns": "app", "name": "web"}, body,
	)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "E_HELM_RELEASE_NOT_FOUND") {
		t.Errorf("body missing E_HELM_RELEASE_NOT_FOUND code: %s", rec.Body.String())
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].Outcome != audit.OutcomeFailure {
		t.Errorf("expected 1 failure audit row, got %+v", events)
	}
}

// ── Helpers ───────────────────────────────────────────────────────

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
