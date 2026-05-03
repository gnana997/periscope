package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/authz"
	"github.com/gnana997/periscope/internal/credentials"
)

// fakeReader records the args it received and returns canned rows.
// Tests assert against the captured args to verify the actor-override
// security boundary is honored.
type fakeReader struct {
	got   audit.QueryArgs
	rows  []audit.Row
	total int
}

func (f *fakeReader) Query(_ context.Context, args audit.QueryArgs) (audit.QueryResult, error) {
	f.got = args
	return audit.QueryResult{Items: f.rows, Total: f.total, Limit: args.Limit}, nil
}

// inject a session into context (mirrors what credentials.Wrap does).
func ctxWithSession(s credentials.Session) context.Context {
	return credentials.WithSession(context.Background(), s)
}

func TestAuditHandler_NonAdminCannotSpoofActor(t *testing.T) {
	// Shared mode + empty AllowedGroups → never audit-admin.
	resolver, err := authz.NewResolver(authz.Config{Mode: authz.ModeShared})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	reader := &fakeReader{}
	h := auditQueryHandler(reader, resolver)

	req := httptest.NewRequest(http.MethodGet,
		"/api/audit?actor=carol&verb=apply", nil)
	req = req.WithContext(ctxWithSession(credentials.Session{
		Subject: "alice",
		Groups:  []string{"engineers"},
	}))
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if reader.got.Actor != "alice" {
		t.Errorf("actor not overridden to alice; got %q (carol leaked through)", reader.got.Actor)
	}
	if scope := rec.Header().Get("X-Audit-Scope"); scope != "self" {
		t.Errorf("X-Audit-Scope = %q, want self", scope)
	}
}

func TestAuditHandler_AdminCanQueryOtherActors(t *testing.T) {
	// Tier mode, alice's group maps to admin.
	resolver, err := authz.NewResolver(authz.Config{
		Mode:       authz.ModeTier,
		GroupTiers: map[string]string{"sre-platform": "admin"},
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	reader := &fakeReader{}
	h := auditQueryHandler(reader, resolver)

	req := httptest.NewRequest(http.MethodGet,
		"/api/audit?actor=carol&verb=apply", nil)
	req = req.WithContext(ctxWithSession(credentials.Session{
		Subject: "alice",
		Groups:  []string{"sre-platform"},
	}))
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if reader.got.Actor != "carol" {
		t.Errorf("admin actor filter dropped; got %q want carol", reader.got.Actor)
	}
	if scope := rec.Header().Get("X-Audit-Scope"); scope != "all" {
		t.Errorf("X-Audit-Scope = %q, want all", scope)
	}
}

func TestAuditHandler_ExplicitAuditAdminGroupGrantsScopeAll(t *testing.T) {
	// Shared mode, but operator explicitly grants audit-admin to a group.
	resolver, err := authz.NewResolver(authz.Config{
		Mode:             authz.ModeShared,
		AuditAdminGroups: []string{"sec-team"},
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	reader := &fakeReader{}
	h := auditQueryHandler(reader, resolver)

	req := httptest.NewRequest(http.MethodGet, "/api/audit?actor=eve", nil)
	req = req.WithContext(ctxWithSession(credentials.Session{
		Subject: "alice",
		Groups:  []string{"sec-team"},
	}))
	rec := httptest.NewRecorder()
	h(rec, req)

	if scope := rec.Header().Get("X-Audit-Scope"); scope != "all" {
		t.Errorf("X-Audit-Scope = %q, want all", scope)
	}
	if reader.got.Actor != "eve" {
		t.Errorf("admin actor filter dropped; got %q want eve", reader.got.Actor)
	}
}

func TestAuditHandler_RawModeDefaultsToSelfOnly(t *testing.T) {
	// Raw mode without AuditAdminGroups → always self-only.
	resolver, err := authz.NewResolver(authz.Config{Mode: authz.ModeRaw})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	reader := &fakeReader{}
	h := auditQueryHandler(reader, resolver)

	req := httptest.NewRequest(http.MethodGet, "/api/audit?actor=carol", nil)
	req = req.WithContext(ctxWithSession(credentials.Session{
		Subject: "alice",
		Groups:  []string{"sres", "admins"}, // K8s groups, but no dashboard-admin signal
	}))
	rec := httptest.NewRecorder()
	h(rec, req)

	if reader.got.Actor != "alice" {
		t.Errorf("raw mode should self-only; got actor=%q", reader.got.Actor)
	}
	if scope := rec.Header().Get("X-Audit-Scope"); scope != "self" {
		t.Errorf("X-Audit-Scope = %q, want self", scope)
	}
}

func TestAuditHandler_AnonymousIs401(t *testing.T) {
	resolver, _ := authz.NewResolver(authz.Config{Mode: authz.ModeShared})
	h := auditQueryHandler(&fakeReader{}, resolver)

	req := httptest.NewRequest(http.MethodGet, "/api/audit", nil)
	// No session in context → SessionFromContext returns zero, Subject="anonymous".
	req = req.WithContext(ctxWithSession(credentials.Session{Subject: "anonymous"}))
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", rec.Code)
	}
}

func TestAuditHandler_BadFromTimeIs400(t *testing.T) {
	resolver, _ := authz.NewResolver(authz.Config{Mode: authz.ModeShared})
	h := auditQueryHandler(&fakeReader{}, resolver)

	req := httptest.NewRequest(http.MethodGet, "/api/audit?from=not-a-time", nil)
	req = req.WithContext(ctxWithSession(credentials.Session{Subject: "alice"}))
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestAuditHandler_ResponseBodyShape(t *testing.T) {
	resolver, _ := authz.NewResolver(authz.Config{Mode: authz.ModeShared})
	reader := &fakeReader{
		rows:  []audit.Row{{Verb: audit.VerbApply, Outcome: audit.OutcomeSuccess}},
		total: 1,
	}
	h := auditQueryHandler(reader, resolver)

	req := httptest.NewRequest(http.MethodGet, "/api/audit", nil)
	req = req.WithContext(ctxWithSession(credentials.Session{Subject: "alice"}))
	rec := httptest.NewRecorder()
	h(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["items"]; !ok {
		t.Error("response missing items field")
	}
	if _, ok := body["total"]; !ok {
		t.Error("response missing total field")
	}
}
