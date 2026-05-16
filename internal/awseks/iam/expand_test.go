package iam

import (
	"strings"
	"testing"
)

// helper: parse fixture + return single-statement document for
// targeted tests. Most tests below operate on one statement at a
// time so the helper trims boilerplate.
func mustParseOne(t *testing.T, relPath string) Statement {
	t.Helper()
	doc, err := ParsePolicyDocument(loadFixture(t, relPath))
	if err != nil {
		t.Fatalf("parse %s: %v", relPath, err)
	}
	if len(doc.Statement) != 1 {
		t.Fatalf("%s has %d statements; helper expects exactly 1", relPath, len(doc.Statement))
	}
	return doc.Statement[0]
}

func newMeta() StatementMeta {
	return StatementMeta{
		PolicyArn:    "arn:aws:iam::123456789012:policy/TestPolicy",
		PolicyName:   "TestPolicy",
		PolicySource: PolicySourceManaged,
		StatementIdx: 0,
	}
}

// ── Cartesian expansion ─────────────────────────────────────────

// AdministratorAccess: Action "*", Resource "*" → 1 Permission.
// Simplest baseline.
func TestExpand_AdministratorAccess(t *testing.T) {
	s := mustParseOne(t, "managed/AdministratorAccess.json")
	perms, raw := s.Expand(newMeta())
	if raw != nil {
		t.Fatalf("RawStatement returned for vanilla Allow/*/*: %+v", raw)
	}
	if len(perms) != 1 {
		t.Fatalf("len(perms) = %d, want 1", len(perms))
	}
	p := perms[0]
	if p.Action != "*" {
		t.Errorf("Action = %q, want *", p.Action)
	}
	if p.Service != "*" {
		t.Errorf("Service = %q, want *", p.Service)
	}
	if p.Resource != "*" {
		t.Errorf("Resource = %q, want *", p.Resource)
	}
	if p.Effect != EffectAllow {
		t.Errorf("Effect = %q, want Allow", p.Effect)
	}
	if !p.Sensitive || p.SensitiveReason != SensitiveWildcard {
		t.Errorf("Sensitive=%v Reason=%q, want true / wildcard", p.Sensitive, p.SensitiveReason)
	}
	if !p.Wildcard {
		t.Error("Wildcard = false, want true (Action == *)")
	}
}

// AmazonS3FullAccess: Action [s3:*, s3-object-lambda:*] × Resource "*"
// → 2 Permissions. Verifies cartesian over Action[].
func TestExpand_CartesianActions(t *testing.T) {
	s := mustParseOne(t, "managed/AmazonS3FullAccess.json")
	perms, raw := s.Expand(newMeta())
	if raw != nil {
		t.Fatalf("RawStatement returned, want nil")
	}
	if len(perms) != 2 {
		t.Fatalf("len(perms) = %d, want 2 (s3:* + s3-object-lambda:*)", len(perms))
	}
	got := map[string]bool{}
	for _, p := range perms {
		got[p.Action] = true
		if p.Resource != "*" {
			t.Errorf("Resource = %q, want *", p.Resource)
		}
	}
	if !got["s3:*"] || !got["s3-object-lambda:*"] {
		t.Errorf("missing expected actions; got = %v", got)
	}
}

// resource-named-bucket-all-objs: [s3:GetObject, s3:PutObject] × scoped
// bucket ARN → 2 Permissions, both Wildcard=true (resource has `*`).
func TestExpand_CartesianActionsAndResources(t *testing.T) {
	s := mustParseOne(t, "wildcard/resource-named-bucket-all-objs.json")
	perms, _ := s.Expand(newMeta())
	if len(perms) != 2 {
		t.Fatalf("len(perms) = %d, want 2 (2 actions × 1 resource)", len(perms))
	}
	for _, p := range perms {
		if !p.Wildcard {
			t.Errorf("Wildcard=false for resource %q (has '*')", p.Resource)
		}
		if p.Service != "s3" {
			t.Errorf("Service = %q, want s3", p.Service)
		}
	}
}

// ── Sensitive catalog firing ────────────────────────────────────

// iam-passrole fixture: 1 action, 1 resource → 1 Permission with
// Sensitive=true, Reason=privilege-escalation.
func TestExpand_Sensitive_IAMPassRole(t *testing.T) {
	s := mustParseOne(t, "sensitive/iam-passrole.json")
	perms, _ := s.Expand(newMeta())
	if len(perms) != 1 {
		t.Fatalf("len(perms) = %d, want 1", len(perms))
	}
	p := perms[0]
	if !p.Sensitive {
		t.Error("Sensitive = false, want true (iam:PassRole)")
	}
	if p.SensitiveReason != SensitivePrivEsc {
		t.Errorf("Reason = %q, want %q", p.SensitiveReason, SensitivePrivEsc)
	}
}

// sensitive/action-star-wildcard: Action "*" should fire the
// literal-wildcard fallback in the catalog.
func TestExpand_Sensitive_LiteralWildcard(t *testing.T) {
	s := mustParseOne(t, "sensitive/action-star-wildcard.json")
	perms, _ := s.Expand(newMeta())
	if len(perms) != 1 {
		t.Fatalf("len(perms) = %d, want 1", len(perms))
	}
	if perms[0].SensitiveReason != SensitiveWildcard {
		t.Errorf("Reason = %q, want %q", perms[0].SensitiveReason, SensitiveWildcard)
	}
}

// s3:DeleteObject* pattern entry → s3:DeleteObject (the fixture's
// concrete action) must classify as destructive.
func TestExpand_Sensitive_PatternMatch(t *testing.T) {
	s := mustParseOne(t, "sensitive/s3-deleteobject-star.json")
	perms, _ := s.Expand(newMeta())
	if perms[0].SensitiveReason != SensitiveDestructive {
		t.Errorf("Reason = %q, want %q (s3:DeleteObject matches s3:DeleteObject*)",
			perms[0].SensitiveReason, SensitiveDestructive)
	}
}

// secretsmanager:getsecretvalue (lowercase) must still classify as
// data — IAM is case-insensitive; catalog must be too.
func TestExpand_Sensitive_CaseInsensitive(t *testing.T) {
	s := mustParseOne(t, "wildcard/action-case-mixed.json")
	perms, _ := s.Expand(newMeta())
	if !perms[0].Sensitive {
		t.Error("Sensitive = false for lowercase action, want true")
	}
	if perms[0].SensitiveReason != SensitiveData {
		t.Errorf("Reason = %q, want %q", perms[0].SensitiveReason, SensitiveData)
	}
}

// s3:Get* is NOT in the sensitive catalog (Get is read-only, not
// flagged). Expand should produce 1 Permission with Sensitive=false
// but Wildcard=true.
func TestExpand_WildcardActionNonSensitive(t *testing.T) {
	s := mustParseOne(t, "wildcard/action-prefix-s3-get.json")
	perms, _ := s.Expand(newMeta())
	if len(perms) != 1 {
		t.Fatalf("len(perms) = %d, want 1", len(perms))
	}
	if perms[0].Sensitive {
		t.Errorf("Sensitive = true, want false (s3:Get* is not in catalog)")
	}
	if !perms[0].Wildcard {
		t.Error("Wildcard = false, want true (action contains *)")
	}
}

// ── Service field always lower-cased ────────────────────────────

// Even when the policy author wrote canonical case "iam:PassRole",
// Service must be "iam" (lower-cased) so the SPA groups
// consistently regardless of source case.
func TestExpand_ServiceAlwaysLowercase(t *testing.T) {
	s := mustParseOne(t, "sensitive/iam-passrole.json")
	perms, _ := s.Expand(newMeta())
	if perms[0].Service != "iam" {
		t.Errorf("Service = %q, want iam", perms[0].Service)
	}
}

// Action without a colon prefix (legal but rare; e.g. SCP-style)
// → empty Service field.
func TestExpand_ServiceEmptyForNoPrefix(t *testing.T) {
	raw := []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"NoColonAction","Resource":"*"}]}`)
	doc, _ := ParsePolicyDocument(raw)
	perms, _ := doc.Statement[0].Expand(newMeta())
	if perms[0].Service != "" {
		t.Errorf("Service = %q, want empty for action without colon", perms[0].Service)
	}
}

// ── HasCondition flag ───────────────────────────────────────────

// AmazonEC2FullAccess has a Condition on its iam:CreateServiceLinkedRole
// statement. Every Permission from that statement must have
// HasCondition=true; the other 4 statements have no Condition.
func TestExpand_HasCondition(t *testing.T) {
	doc, err := ParsePolicyDocument(loadFixture(t, "managed/AmazonEC2FullAccess.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var withCondCount, withoutCondCount int
	for i, s := range doc.Statement {
		meta := newMeta()
		meta.StatementIdx = i
		perms, _ := s.Expand(meta)
		for _, p := range perms {
			if p.HasCondition {
				withCondCount++
			} else {
				withoutCondCount++
			}
		}
	}
	if withCondCount == 0 {
		t.Error("no Permission with HasCondition=true; fixture has a Condition block")
	}
	if withoutCondCount == 0 {
		t.Error("no Permission with HasCondition=false; fixture has unconditional statements")
	}
}

// ── NotAction / NotResource → RawStatement ──────────────────────

// pathological/not-action-deny → no Permissions, one RawStatement
// with Reason=NotAction.
func TestExpand_NotActionProducesRawStatement(t *testing.T) {
	s := mustParseOne(t, "pathological/not-action-deny.json")
	perms, raw := s.Expand(newMeta())
	if len(perms) != 0 {
		t.Errorf("Permissions = %d, want 0 (NotAction must NOT project)", len(perms))
	}
	if raw == nil {
		t.Fatal("RawStatement = nil, want non-nil")
	}
	if raw.Reason != "NotAction" {
		t.Errorf("Reason = %q, want NotAction", raw.Reason)
	}
	if raw.Summary == "" {
		t.Error("Summary empty; want descriptive text")
	}
	if !strings.Contains(raw.Summary, "deny") {
		t.Errorf("Summary = %q, want it to mention 'deny'", raw.Summary)
	}
}

// pathological/not-resource-allow → RawStatement, Reason=NotResource.
func TestExpand_NotResourceProducesRawStatement(t *testing.T) {
	s := mustParseOne(t, "pathological/not-resource-allow.json")
	perms, raw := s.Expand(newMeta())
	if len(perms) != 0 {
		t.Errorf("Permissions = %d, want 0", len(perms))
	}
	if raw == nil || raw.Reason != "NotResource" {
		t.Fatalf("raw = %+v, want Reason=NotResource", raw)
	}
	if !strings.Contains(raw.Summary, "s3:GetObject") {
		t.Errorf("Summary should reference the Action; got %q", raw.Summary)
	}
}

// PowerUserAccess mixes one NotAction statement and one Action
// statement. Document-level Expand (caller iterates each statement)
// must produce both — RawStatement for stmt[0], Permissions for
// stmt[1].
func TestExpand_PowerUserAccess_MixedShape(t *testing.T) {
	doc, _ := ParsePolicyDocument(loadFixture(t, "managed/PowerUserAccess.json"))
	if len(doc.Statement) != 2 {
		t.Fatalf("len(doc.Statement) = %d, want 2", len(doc.Statement))
	}

	// Stmt 0: NotAction → RawStatement.
	perms0, raw0 := doc.Statement[0].Expand(newMeta())
	if len(perms0) != 0 {
		t.Errorf("stmt[0] perms = %d, want 0", len(perms0))
	}
	if raw0 == nil || raw0.Reason != "NotAction" {
		t.Errorf("stmt[0] raw = %+v, want NotAction", raw0)
	}

	// Stmt 1: 6 actions × 1 resource → 6 Permissions.
	meta1 := newMeta()
	meta1.StatementIdx = 1
	perms1, raw1 := doc.Statement[1].Expand(meta1)
	if raw1 != nil {
		t.Errorf("stmt[1] raw = %+v, want nil", raw1)
	}
	if len(perms1) != 6 {
		t.Errorf("stmt[1] perms = %d, want 6", len(perms1))
	}
}

// ── StatementMeta stamped onto every Permission ─────────────────

func TestExpand_MetaStamping(t *testing.T) {
	// SecretsManagerReadWrite has 2 statements with explicit Sids;
	// use statement 0 directly so we can verify Sid + StatementIdx
	// stamp correctly across multiple Permissions.
	doc, _ := ParsePolicyDocument(loadFixture(t, "managed/SecretsManagerReadWrite.json"))
	meta := StatementMeta{
		PolicyArn:    "arn:aws:iam::aws:policy/SecretsManagerReadWrite",
		PolicyName:   "SecretsManagerReadWrite",
		PolicySource: PolicySourceManaged,
		StatementIdx: 0,
	}
	perms, _ := doc.Statement[0].Expand(meta)
	if len(perms) == 0 {
		t.Fatal("no perms")
	}
	for _, p := range perms {
		if p.PolicyArn != meta.PolicyArn {
			t.Errorf("PolicyArn = %q, want %q", p.PolicyArn, meta.PolicyArn)
		}
		if p.PolicyName != meta.PolicyName {
			t.Errorf("PolicyName = %q, want %q", p.PolicyName, meta.PolicyName)
		}
		if p.PolicySource != PolicySourceManaged {
			t.Errorf("PolicySource = %q, want managed", p.PolicySource)
		}
		if p.StatementIdx != 0 {
			t.Errorf("StatementIdx = %d, want 0", p.StatementIdx)
		}
		if p.StatementSid != "BasePermissions" {
			t.Errorf("StatementSid = %q, want BasePermissions", p.StatementSid)
		}
	}
}

// Inline policies have empty PolicyArn — verify it stamps cleanly.
func TestExpand_MetaStamping_Inline(t *testing.T) {
	s := mustParseOne(t, "sensitive/iam-passrole.json")
	meta := StatementMeta{
		PolicyArn:    "", // inline
		PolicyName:   "InlinePolicy",
		PolicySource: PolicySourceInline,
		StatementIdx: 7,
	}
	perms, _ := s.Expand(meta)
	if perms[0].PolicyArn != "" {
		t.Errorf("PolicyArn = %q, want empty for inline", perms[0].PolicyArn)
	}
	if perms[0].PolicySource != PolicySourceInline {
		t.Errorf("PolicySource = %q, want inline", perms[0].PolicySource)
	}
	if perms[0].StatementIdx != 7 {
		t.Errorf("StatementIdx = %d, want 7", perms[0].StatementIdx)
	}
}

// ── Catalog injection (test-only override) ──────────────────────

func TestExpand_CustomCatalog(t *testing.T) {
	// Empty catalog — nothing is sensitive.
	emptyCat, err := LoadCatalog([]byte(`version: "test-empty"
entries: {}`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	s := mustParseOne(t, "sensitive/iam-passrole.json")
	meta := newMeta()
	meta.Catalog = emptyCat
	perms, _ := s.Expand(meta)
	if perms[0].Sensitive {
		t.Errorf("Sensitive=true with empty catalog; want false")
	}
	// "*" still fires (handled in code, not YAML).
	starS := mustParseOne(t, "sensitive/action-star-wildcard.json")
	starP, _ := starS.Expand(meta)
	if !starP[0].Sensitive {
		t.Error("literal-* should always fire wildcard regardless of catalog")
	}
}

// ── Empty Action / Resource defensive cases ─────────────────────

// Statement with empty Action (and no NotAction) → 0 Permissions.
// This shouldn't happen with valid AWS policies but the parser
// might emit this if Action was malformed-but-tolerated; Expand
// must not panic.
func TestExpand_EmptyActionNoNot(t *testing.T) {
	s := Statement{Effect: EffectAllow, Resource: []string{"*"}}
	perms, raw := s.Expand(newMeta())
	if len(perms) != 0 {
		t.Errorf("perms = %d, want 0", len(perms))
	}
	if raw != nil {
		t.Errorf("raw = %+v, want nil (no Not* fields present)", raw)
	}
}

// Statement with Action but empty Resource → AWS treats absent
// Resource on identity-based policy as invalid; Expand is
// defensive and treats it as Resource: ["*"] so the matcher still
// returns rows.
func TestExpand_EmptyResourceImpliesStar(t *testing.T) {
	s := Statement{
		Effect: EffectAllow,
		Action: []string{"s3:GetObject"},
	}
	perms, _ := s.Expand(newMeta())
	if len(perms) != 1 {
		t.Fatalf("perms = %d, want 1", len(perms))
	}
	if perms[0].Resource != "*" {
		t.Errorf("Resource = %q, want * (defensive default)", perms[0].Resource)
	}
}
