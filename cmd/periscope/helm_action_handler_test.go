package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

func actionHandlerInvoke(
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

func withInstallReleaseFn(t *testing.T, fn func(context.Context, credentials.Provider, clusters.Cluster, k8s.InstallArgs) (*k8s.HelmActionResult, error)) {
	t.Helper()
	prev := installHelmReleaseFn
	installHelmReleaseFn = fn
	t.Cleanup(func() { installHelmReleaseFn = prev })
}

func withUpgradeReleaseFn(t *testing.T, fn func(context.Context, credentials.Provider, clusters.Cluster, k8s.UpgradeArgs) (*k8s.HelmActionResult, error)) {
	t.Helper()
	prev := upgradeHelmReleaseFn
	upgradeHelmReleaseFn = fn
	t.Cleanup(func() { upgradeHelmReleaseFn = prev })
}

var fakeInstallSuccess = &k8s.HelmActionResult{
	Release: k8s.ActionReleaseInfo{
		Name: "web", Namespace: "app-ns", Revision: 1,
		Status: "deployed", Notes: "thanks",
		Chart: k8s.ActionChartRef{Name: "nginx", Version: "1.0.0"},
		DeployedAt: "2026-05-06T12:00:00Z",
	},
}

// ── Install handler ───────────────────────────────────────────────

func TestHelmInstallHandler_HappyPathEmitsAuditPair(t *testing.T) {
	withInstallReleaseFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, args k8s.InstallArgs) (*k8s.HelmActionResult, error) {
		// Verify the handler-side defaults flowed through.
		if !args.Atomic {
			t.Errorf("Atomic default should be true, got false")
		}
		if !args.Wait {
			t.Errorf("Wait default should be true, got false")
		}
		if !args.IncludeCRDs {
			t.Errorf("IncludeCRDs default should be true, got false")
		}
		return fakeInstallSuccess, nil
	})

	reg := testRegistry(t)
	body := mustJSON(t, helmInstallRequest{
		Ref: "oci://example/c", Version: "1.0.0", Namespace: "app-ns", ReleaseName: "web",
	})
	rec, sink := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmInstallHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/install", nil, body,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got k8s.HelmActionResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Release.Revision != 1 {
		t.Errorf("Release.Revision = %d, want 1", got.Release.Revision)
	}

	// Audit row pair: intent THEN outcome.
	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("expected 2 audit events (intent + outcome), got %d", len(events))
	}
	if events[0].Verb != audit.VerbHelmInstallIntent {
		t.Errorf("first event verb = %q, want %q", events[0].Verb, audit.VerbHelmInstallIntent)
	}
	if events[1].Verb != audit.VerbHelmInstall {
		t.Errorf("second event verb = %q, want %q", events[1].Verb, audit.VerbHelmInstall)
	}
	if events[1].Outcome != audit.OutcomeSuccess {
		t.Errorf("outcome row = %q, want success", events[1].Outcome)
	}
	if rev, _ := events[1].Extra["revision"].(int); rev != 1 {
		t.Errorf("outcome row Extra.revision = %v, want 1", events[1].Extra["revision"])
	}
}

func TestHelmInstallHandler_PreflightDeniedReturns403(t *testing.T) {
	withInstallReleaseFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ k8s.InstallArgs) (*k8s.HelmActionResult, error) {
		return nil, &k8s.DeniedError{Denials: []k8s.PreviewDenial{
			{Group: "networking.k8s.io", Resource: "ingresses", Verb: "create", Reason: "denied"},
		}}
	})

	reg := testRegistry(t)
	body := mustJSON(t, helmInstallRequest{
		Ref: "oci://example/c", Version: "1.0.0", Namespace: "app-ns", ReleaseName: "web",
	})
	rec, sink := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmInstallHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/install", nil, body,
	)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "E_HELM_PREFLIGHT_DENIED") {
		t.Errorf("body missing E_HELM_PREFLIGHT_DENIED: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ingresses") {
		t.Errorf("body should include the denied resource list: %s", rec.Body.String())
	}
	// Audit pair: intent THEN denied outcome.
	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("expected 2 audit events, got %d", len(events))
	}
	if events[1].Outcome != audit.OutcomeDenied {
		t.Errorf("outcome row = %q, want denied", events[1].Outcome)
	}
}

func TestHelmInstallHandler_ValidationMissingFields(t *testing.T) {
	cases := []struct {
		name string
		body any
	}{
		{"empty ref", helmInstallRequest{Version: "1.0", Namespace: "n", ReleaseName: "r"}},
		{"empty version", helmInstallRequest{Ref: "oci://x/c", Namespace: "n", ReleaseName: "r"}},
		{"empty namespace", helmInstallRequest{Ref: "oci://x/c", Version: "1.0", ReleaseName: "r"}},
		{"empty releaseName", helmInstallRequest{Ref: "oci://x/c", Version: "1.0", Namespace: "n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := testRegistry(t)
			body := mustJSON(t, tc.body)
			rec, _ := actionHandlerInvoke(t,
				func(e *audit.Emitter) credentials.Handler { return helmInstallHandler(reg, e) },
				http.MethodPost, "/api/clusters/test/helm/install", nil, body,
			)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestHelmInstallHandler_FailureMappedAndAudited(t *testing.T) {
	cases := []struct {
		name           string
		err            error
		wantStatus     int
		wantCodeSubstr string
	}{
		{"chart not found → 404", k8s.ErrChartNotFound, http.StatusNotFound, "E_CHART_NOT_FOUND"},
		{"chart unsupported deps → 422", k8s.ErrChartUnsupportedDeps, http.StatusUnprocessableEntity, "E_CHART_UNSUPPORTED_DEPS"},
		{"helm release exists → 502 (unknown)", errors.New("cannot re-use a name that is still in use"), http.StatusBadGateway, "E_HELM_SDK"},
		{"unknown helm error → 502", errors.New("unknown helm error"), http.StatusBadGateway, "E_HELM_SDK"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withInstallReleaseFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ k8s.InstallArgs) (*k8s.HelmActionResult, error) {
				return nil, tc.err
			})
			reg := testRegistry(t)
			body := mustJSON(t, helmInstallRequest{
				Ref: "oci://example/c", Version: "1.0.0", Namespace: "app-ns", ReleaseName: "web",
			})
			rec, sink := actionHandlerInvoke(t,
				func(e *audit.Emitter) credentials.Handler { return helmInstallHandler(reg, e) },
				http.MethodPost, "/api/clusters/test/helm/install", nil, body,
			)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body=%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantCodeSubstr) {
				t.Errorf("body missing code %q: %s", tc.wantCodeSubstr, rec.Body.String())
			}
			events := sink.snapshot()
			if len(events) != 2 {
				t.Fatalf("expected 2 audit events, got %d", len(events))
			}
			if events[1].Outcome != audit.OutcomeFailure {
				t.Errorf("outcome row = %q, want failure", events[1].Outcome)
			}
		})
	}
}

func TestHelmInstallHandler_TimeoutClampedToMax(t *testing.T) {
	var capturedArgs k8s.InstallArgs
	withInstallReleaseFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, args k8s.InstallArgs) (*k8s.HelmActionResult, error) {
		capturedArgs = args
		return fakeInstallSuccess, nil
	})

	reg := testRegistry(t)
	// Request 1 hour timeout — server-side cap is 10 minutes.
	body := mustJSON(t, helmInstallRequest{
		Ref: "oci://example/c", Version: "1.0.0", Namespace: "app-ns", ReleaseName: "web",
		TimeoutSeconds: 3600,
	})
	rec, _ := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmInstallHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/install", nil, body,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if capturedArgs.Timeout != helmActionMaxTimeout {
		t.Errorf("Timeout = %v, want clamp at %v", capturedArgs.Timeout, helmActionMaxTimeout)
	}
}

func TestHelmInstallHandler_AtomicOptOut(t *testing.T) {
	atomicFalse := false
	var capturedArgs k8s.InstallArgs
	withInstallReleaseFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, args k8s.InstallArgs) (*k8s.HelmActionResult, error) {
		capturedArgs = args
		return fakeInstallSuccess, nil
	})
	reg := testRegistry(t)
	body := mustJSON(t, helmInstallRequest{
		Ref: "oci://example/c", Version: "1.0.0", Namespace: "app-ns", ReleaseName: "web",
		Atomic: &atomicFalse,
	})
	rec, _ := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmInstallHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/install", nil, body,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if capturedArgs.Atomic {
		t.Error("explicit Atomic=false should override default true")
	}
}

// ── Upgrade handler ───────────────────────────────────────────────

func TestHelmUpgradeHandler_HappyPathEmitsAuditPair(t *testing.T) {
	withUpgradeReleaseFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, args k8s.UpgradeArgs) (*k8s.HelmActionResult, error) {
		if args.Namespace != "app-ns" || args.ReleaseName != "web" {
			t.Errorf("upgrade args ns/name should come from URL: %+v", args)
		}
		if !args.Atomic {
			t.Errorf("upgrade Atomic default should be true")
		}
		return &k8s.HelmActionResult{
			Release: k8s.ActionReleaseInfo{Name: "web", Namespace: "app-ns", Revision: 2, Status: "deployed"},
		}, nil
	})

	reg := testRegistry(t)
	body := mustJSON(t, helmUpgradeRequest{Ref: "oci://example/c", Version: "1.1.0"})
	rec, sink := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmUpgradeHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/releases/app-ns/web/upgrade",
		map[string]string{"cluster": "test", "ns": "app-ns", "name": "web"}, body,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("expected 2 audit events, got %d", len(events))
	}
	if events[0].Verb != audit.VerbHelmUpgradeIntent || events[1].Verb != audit.VerbHelmUpgrade {
		t.Errorf("verb pair wrong: %q + %q", events[0].Verb, events[1].Verb)
	}
}

func TestHelmUpgradeHandler_ReleaseNotFoundReturns404(t *testing.T) {
	withUpgradeReleaseFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ k8s.UpgradeArgs) (*k8s.HelmActionResult, error) {
		return nil, driver.ErrReleaseNotFound
	})
	reg := testRegistry(t)
	body := mustJSON(t, helmUpgradeRequest{Ref: "oci://x/c", Version: "1.0"})
	rec, _ := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmUpgradeHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/releases/app/web/upgrade",
		map[string]string{"cluster": "test", "ns": "app", "name": "web"}, body,
	)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "E_HELM_RELEASE_NOT_FOUND") {
		t.Errorf("body missing E_HELM_RELEASE_NOT_FOUND: %s", rec.Body.String())
	}
}

func TestHelmUpgradeHandler_ValidationMissingPathParams(t *testing.T) {
	reg := testRegistry(t)
	body := mustJSON(t, helmUpgradeRequest{Ref: "oci://x/c", Version: "1.0"})
	rec, _ := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmUpgradeHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/releases//web/upgrade",
		map[string]string{"cluster": "test", "ns": "", "name": "web"}, body,
	)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestClampTimeout(t *testing.T) {
	cases := map[int]string{
		0:    "default", // 5min
		-1:   "default",
		60:   "60s",
		300:  "5m",
		600:  "10m", // == max
		3600: "10m", // clamped
	}
	for sec, label := range cases {
		t.Run(label, func(t *testing.T) {
			d := clampTimeout(sec)
			if d < helmActionDefaultTimeout && sec > 0 && sec < 300 {
				// Below-default seconds get the literal value (60s in the table).
				return
			}
			if sec >= 600 && d != helmActionMaxTimeout {
				t.Errorf("timeoutSeconds=%d should clamp to %v, got %v", sec, helmActionMaxTimeout, d)
			}
			if sec <= 0 && d != helmActionDefaultTimeout {
				t.Errorf("timeoutSeconds=%d should default to %v, got %v", sec, helmActionDefaultTimeout, d)
			}
		})
	}
}

func TestBoolDefault(t *testing.T) {
	tr := true
	fa := false
	if boolDefault(nil, true) != true {
		t.Error("nil + true default → true expected")
	}
	if boolDefault(nil, false) != false {
		t.Error("nil + false default → false expected")
	}
	if boolDefault(&tr, false) != true {
		t.Error("explicit true should override default")
	}
	if boolDefault(&fa, true) != false {
		t.Error("explicit false should override default")
	}
}

// ── Uninstall handler ────────────────────────────────────────────

func withUninstallReleaseFn(t *testing.T, fn func(context.Context, credentials.Provider, clusters.Cluster, k8s.UninstallArgs) (*k8s.UninstallResult, error)) {
	t.Helper()
	prev := uninstallHelmReleaseFn
	uninstallHelmReleaseFn = fn
	t.Cleanup(func() { uninstallHelmReleaseFn = prev })
}

func TestHelmUninstallHandler_HappyPathEmitsAuditPair(t *testing.T) {
	withUninstallReleaseFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, args k8s.UninstallArgs) (*k8s.UninstallResult, error) {
		if args.Namespace != "app-ns" || args.ReleaseName != "web" {
			t.Errorf("uninstall args ns/name should come from URL: %+v", args)
		}
		if args.KeepHistory {
			t.Error("KeepHistory default should be false (no query param passed)")
		}
		return &k8s.UninstallResult{
			Release: k8s.ActionReleaseInfo{
				Name: "web", Namespace: "app-ns", Revision: 5, Status: "uninstalled",
			},
			RevisionsRemoved: 5,
		}, nil
	})

	reg := testRegistry(t)
	rec, sink := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmUninstallHandler(reg, e) },
		http.MethodDelete, "/api/clusters/test/helm/releases/app-ns/web",
		map[string]string{"cluster": "test", "ns": "app-ns", "name": "web"}, nil,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("expected 2 audit events (intent + outcome), got %d", len(events))
	}
	if events[0].Verb != audit.VerbHelmUninstallIntent || events[1].Verb != audit.VerbHelmUninstall {
		t.Errorf("verb pair wrong: %q + %q", events[0].Verb, events[1].Verb)
	}
	if events[1].Outcome != audit.OutcomeSuccess {
		t.Errorf("outcome row = %q, want success", events[1].Outcome)
	}
	if removed, _ := events[1].Extra["revisionsRemoved"].(int); removed != 5 {
		t.Errorf("Extra.revisionsRemoved = %v, want 5", events[1].Extra["revisionsRemoved"])
	}
}

func TestHelmUninstallHandler_KeepHistoryQueryParam(t *testing.T) {
	var capturedArgs k8s.UninstallArgs
	withUninstallReleaseFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, args k8s.UninstallArgs) (*k8s.UninstallResult, error) {
		capturedArgs = args
		return &k8s.UninstallResult{Release: k8s.ActionReleaseInfo{Name: "web"}}, nil
	})
	reg := testRegistry(t)
	rec, _ := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmUninstallHandler(reg, e) },
		http.MethodDelete, "/api/clusters/test/helm/releases/app-ns/web?keepHistory=true&disableHooks=true",
		map[string]string{"cluster": "test", "ns": "app-ns", "name": "web"}, nil,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !capturedArgs.KeepHistory {
		t.Error("KeepHistory should be true when query param keepHistory=true")
	}
	if !capturedArgs.DisableHooks {
		t.Error("DisableHooks should be true when query param disableHooks=true")
	}
}

func TestHelmUninstallHandler_PreflightDeniedReturns403(t *testing.T) {
	withUninstallReleaseFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ k8s.UninstallArgs) (*k8s.UninstallResult, error) {
		return nil, &k8s.DeniedError{Denials: []k8s.PreviewDenial{
			{Group: "", Resource: "secrets", Verb: "delete", Reason: "denied"},
		}}
	})
	reg := testRegistry(t)
	rec, sink := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmUninstallHandler(reg, e) },
		http.MethodDelete, "/api/clusters/test/helm/releases/app-ns/web",
		map[string]string{"cluster": "test", "ns": "app-ns", "name": "web"}, nil,
	)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "E_HELM_PREFLIGHT_DENIED") {
		t.Errorf("body missing E_HELM_PREFLIGHT_DENIED: %s", rec.Body.String())
	}
	events := sink.snapshot()
	if len(events) != 2 || events[1].Outcome != audit.OutcomeDenied {
		t.Errorf("expected intent + denied outcome pair; got %+v", events)
	}
}

func TestHelmUninstallHandler_ReleaseNotFoundReturns404(t *testing.T) {
	withUninstallReleaseFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ k8s.UninstallArgs) (*k8s.UninstallResult, error) {
		return nil, driver.ErrReleaseNotFound
	})
	reg := testRegistry(t)
	rec, _ := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmUninstallHandler(reg, e) },
		http.MethodDelete, "/api/clusters/test/helm/releases/app/web",
		map[string]string{"cluster": "test", "ns": "app", "name": "web"}, nil,
	)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "E_HELM_RELEASE_NOT_FOUND") {
		t.Errorf("body missing E_HELM_RELEASE_NOT_FOUND: %s", rec.Body.String())
	}
}

func TestHelmUninstallHandler_ValidationMissingPathParams(t *testing.T) {
	reg := testRegistry(t)
	rec, _ := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmUninstallHandler(reg, e) },
		http.MethodDelete, "/api/clusters/test/helm/releases//web",
		map[string]string{"cluster": "test", "ns": "", "name": "web"}, nil,
	)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for empty ns", rec.Code)
	}
}

func withRollbackReleaseFn(t *testing.T, fn func(context.Context, credentials.Provider, clusters.Cluster, k8s.HelmRollbackArgs) (*k8s.HelmRollbackResult, error)) {
	t.Helper()
	prev := rollbackHelmReleaseFn
	rollbackHelmReleaseFn = fn
	t.Cleanup(func() { rollbackHelmReleaseFn = prev })
}

func TestHelmRollbackHandler_HappyPathEmitsAuditPair(t *testing.T) {
	withRollbackReleaseFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, args k8s.HelmRollbackArgs) (*k8s.HelmRollbackResult, error) {
		if args.Namespace != "app-ns" || args.ReleaseName != "web" {
			t.Errorf("rollback args ns/name should come from URL: %+v", args)
		}
		if args.Revision != 3 {
			t.Errorf("revision should come from body: %+v", args)
		}
		if !args.Wait {
			t.Error("Wait should default to true")
		}
		return &k8s.HelmRollbackResult{
			Release: k8s.ActionReleaseInfo{
				Name: "web", Namespace: "app-ns", Revision: 6, Status: "deployed",
			},
			NewRevision:  6,
			FromRevision: 5,
			ToRevision:   3,
		}, nil
	})

	reg := testRegistry(t)
	rec, sink := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmRollbackHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/releases/app-ns/web/rollback",
		map[string]string{"cluster": "test", "ns": "app-ns", "name": "web"},
		[]byte(`{"revision":3}`),
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("expected 2 audit events (intent + outcome), got %d", len(events))
	}
	if events[0].Verb != audit.VerbHelmRollbackIntent || events[1].Verb != audit.VerbHelmRollback {
		t.Errorf("verb pair wrong: %q + %q", events[0].Verb, events[1].Verb)
	}
	if events[1].Outcome != audit.OutcomeSuccess {
		t.Errorf("outcome row = %q, want success", events[1].Outcome)
	}
	if from, _ := events[1].Extra["fromRevision"].(int); from != 5 {
		t.Errorf("Extra.fromRevision = %v, want 5", events[1].Extra["fromRevision"])
	}
	if to, _ := events[1].Extra["toRevision"].(int); to != 3 {
		t.Errorf("Extra.toRevision = %v, want 3", events[1].Extra["toRevision"])
	}
	if newRev, _ := events[1].Extra["newRevision"].(int); newRev != 6 {
		t.Errorf("Extra.newRevision = %v, want 6", events[1].Extra["newRevision"])
	}
}

func TestHelmRollbackHandler_KnobsHonored(t *testing.T) {
	var captured k8s.HelmRollbackArgs
	withRollbackReleaseFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, args k8s.HelmRollbackArgs) (*k8s.HelmRollbackResult, error) {
		captured = args
		return &k8s.HelmRollbackResult{Release: k8s.ActionReleaseInfo{Name: "web"}}, nil
	})
	reg := testRegistry(t)
	body := `{"revision":2,"wait":false,"cleanupOnFail":true,"disableHooks":true,"timeoutSeconds":120}`
	rec, _ := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmRollbackHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/releases/app-ns/web/rollback",
		map[string]string{"cluster": "test", "ns": "app-ns", "name": "web"},
		[]byte(body),
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if captured.Wait {
		t.Error("Wait should be false when body wait=false")
	}
	if !captured.CleanupOnFail {
		t.Error("CleanupOnFail should be true when body cleanupOnFail=true")
	}
	if !captured.DisableHooks {
		t.Error("DisableHooks should be true when body disableHooks=true")
	}
	if captured.Timeout <= 0 {
		t.Error("Timeout should be set (positive duration) when timeoutSeconds=120")
	}
}

func TestHelmRollbackHandler_ValidationMissingRevision(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty body", `{}`},
		{"zero revision", `{"revision":0}`},
		{"negative revision", `{"revision":-1}`},
		{"malformed json", `{not json`},
	}
	reg := testRegistry(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withRollbackReleaseFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ k8s.HelmRollbackArgs) (*k8s.HelmRollbackResult, error) {
				t.Fatal("rollback fn should NOT be called when validation fails")
				return nil, nil
			})
			rec, _ := actionHandlerInvoke(t,
				func(e *audit.Emitter) credentials.Handler { return helmRollbackHandler(reg, e) },
				http.MethodPost, "/api/clusters/test/helm/releases/app-ns/web/rollback",
				map[string]string{"cluster": "test", "ns": "app-ns", "name": "web"},
				[]byte(tc.body),
			)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHelmRollbackHandler_PreflightDeniedReturns403(t *testing.T) {
	withRollbackReleaseFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ k8s.HelmRollbackArgs) (*k8s.HelmRollbackResult, error) {
		return nil, &k8s.DeniedError{Denials: []k8s.PreviewDenial{
			{Group: "apps", Resource: "deployments", Verb: "patch", Reason: "denied"},
		}}
	})
	reg := testRegistry(t)
	rec, sink := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmRollbackHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/releases/app-ns/web/rollback",
		map[string]string{"cluster": "test", "ns": "app-ns", "name": "web"},
		[]byte(`{"revision":3}`),
	)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "E_HELM_PREFLIGHT_DENIED") {
		t.Errorf("body missing E_HELM_PREFLIGHT_DENIED: %s", rec.Body.String())
	}
	events := sink.snapshot()
	if len(events) != 2 || events[1].Outcome != audit.OutcomeDenied {
		t.Errorf("expected intent + denied outcome pair; got %+v", events)
	}
}

func TestHelmRollbackHandler_NoOpRollbackRejected(t *testing.T) {
	// rollback to current is rejected by the k8s layer; verify the
	// handler bubbles the error and emits a Failure outcome.
	withRollbackReleaseFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ k8s.HelmRollbackArgs) (*k8s.HelmRollbackResult, error) {
		return nil, fmt.Errorf("rollback: target revision 5 is already current")
	})
	reg := testRegistry(t)
	rec, sink := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmRollbackHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/releases/app-ns/web/rollback",
		map[string]string{"cluster": "test", "ns": "app-ns", "name": "web"},
		[]byte(`{"revision":5}`),
	)
	if rec.Code < 400 {
		t.Fatalf("status = %d, want >=400; body=%s", rec.Code, rec.Body.String())
	}
	events := sink.snapshot()
	if len(events) != 2 || events[1].Outcome != audit.OutcomeFailure {
		t.Errorf("expected intent + failure outcome pair; got %+v", events)
	}
}

func TestHelmRollbackHandler_ReleaseNotFoundReturns404(t *testing.T) {
	withRollbackReleaseFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ k8s.HelmRollbackArgs) (*k8s.HelmRollbackResult, error) {
		return nil, driver.ErrReleaseNotFound
	})
	reg := testRegistry(t)
	rec, _ := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return helmRollbackHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/helm/releases/app-ns/missing/rollback",
		map[string]string{"cluster": "test", "ns": "app-ns", "name": "missing"},
		[]byte(`{"revision":1}`),
	)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHelmRollbackHandler_ValidationMissingPathParams(t *testing.T) {
	cases := []struct {
		name        string
		urlSuffix   string
		params      map[string]string
		wantStatus  int
	}{
		{"missing ns", "/helm/releases//web/rollback", map[string]string{"cluster": "test", "ns": "", "name": "web"}, http.StatusBadRequest},
		{"missing name", "/helm/releases/app-ns//rollback", map[string]string{"cluster": "test", "ns": "app-ns", "name": ""}, http.StatusBadRequest},
		{"unknown cluster", "/helm/releases/app-ns/web/rollback", map[string]string{"cluster": "missing", "ns": "app-ns", "name": "web"}, http.StatusNotFound},
	}
	reg := testRegistry(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, _ := actionHandlerInvoke(t,
				func(e *audit.Emitter) credentials.Handler { return helmRollbackHandler(reg, e) },
				http.MethodPost, "/api/clusters/"+tc.params["cluster"]+tc.urlSuffix,
				tc.params, []byte(`{"revision":1}`),
			)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
		})
	}
}
