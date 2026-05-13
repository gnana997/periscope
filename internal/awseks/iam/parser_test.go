package iam

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// ── Corpus sanity: every fixture parses ──────────────────────────

// TestParsePolicyDocument_ManagedCorpus walks the managed/ fixtures
// and asserts every AWS-managed policy parses without error. Drives
// "the simple shapes work" coverage — Action as string vs array,
// Resource as string vs array, single-statement vs multi-statement,
// with-Condition vs without.
func TestParsePolicyDocument_ManagedCorpus(t *testing.T) {
	for _, p := range loadFixturePairs(t, "managed") {
		t.Run(p.Name, func(t *testing.T) {
			doc, err := ParsePolicyDocument(p.Input)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if doc.Version != "2012-10-17" {
				t.Errorf("Version = %q, want 2012-10-17 (AWS-managed convention)", doc.Version)
			}
			if len(doc.Statement) == 0 {
				t.Errorf("empty Statement[] from non-empty managed policy")
			}
		})
	}
}

// TestParsePolicyDocument_SensitiveCorpus + WildcardCorpus exercise
// the small single-purpose fixtures. They're not testing parser
// correctness per se (the managed corpus does that more
// comprehensively) — they're testing that the corpus loads cleanly
// before Expand tests rely on it.
func TestParsePolicyDocument_SensitiveCorpus(t *testing.T) {
	for _, p := range loadFixturePairs(t, "sensitive") {
		t.Run(p.Name, func(t *testing.T) {
			if _, err := ParsePolicyDocument(p.Input); err != nil {
				t.Fatalf("parse: %v", err)
			}
		})
	}
}

func TestParsePolicyDocument_WildcardCorpus(t *testing.T) {
	for _, p := range loadFixturePairs(t, "wildcard") {
		t.Run(p.Name, func(t *testing.T) {
			if _, err := ParsePolicyDocument(p.Input); err != nil {
				t.Fatalf("parse: %v", err)
			}
		})
	}
}

// ── Targeted: each AWS JSON quirk ────────────────────────────────

// AdministratorAccess is the simplest possible policy: one Statement,
// string Action, string Resource. If this doesn't parse, nothing else
// will.
func TestParsePolicyDocument_AdministratorAccess(t *testing.T) {
	input := loadFixture(t, "managed/AdministratorAccess.json")
	doc, err := ParsePolicyDocument(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Statement) != 1 {
		t.Fatalf("Statement len = %d, want 1", len(doc.Statement))
	}
	s := doc.Statement[0]
	if s.Effect != EffectAllow {
		t.Errorf("Effect = %q, want %q", s.Effect, EffectAllow)
	}
	if len(s.Action) != 1 || s.Action[0] != "*" {
		t.Errorf(`Action = %v, want ["*"]`, s.Action)
	}
	if len(s.Resource) != 1 || s.Resource[0] != "*" {
		t.Errorf(`Resource = %v, want ["*"]`, s.Resource)
	}
}

// AmazonS3FullAccess uses Action as an array — distinct from
// AdministratorAccess's string form. Same parser path must produce
// the canonical []string regardless of source shape.
func TestParsePolicyDocument_ActionAsArray(t *testing.T) {
	input := loadFixture(t, "managed/AmazonS3FullAccess.json")
	doc, err := ParsePolicyDocument(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := doc.Statement[0]
	if len(s.Action) != 2 {
		t.Fatalf("Action len = %d, want 2 (s3:* + s3-object-lambda:*)", len(s.Action))
	}
}

// pathological/single-statement-object.json: Statement is an object,
// NOT wrapped in an array. AWS accepts both shapes; the parser must
// lift the object to a single-element []Statement.
func TestParsePolicyDocument_StatementAsObject(t *testing.T) {
	input := loadFixture(t, "pathological/single-statement-object.json")
	doc, err := ParsePolicyDocument(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Statement) != 1 {
		t.Fatalf("Statement len = %d, want 1 (object lifted to array)", len(doc.Statement))
	}
	if doc.Statement[0].Action[0] != "s3:GetObject" {
		t.Errorf(`Action[0] = %q, want "s3:GetObject"`, doc.Statement[0].Action[0])
	}
}

// pathological/empty-statement-array.json: Statement: []. Valid IAM
// JSON, grants nothing. Parser must not error and must return zero
// statements.
func TestParsePolicyDocument_EmptyStatementArray(t *testing.T) {
	input := loadFixture(t, "pathological/empty-statement-array.json")
	doc, err := ParsePolicyDocument(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Statement) != 0 {
		t.Errorf("Statement len = %d, want 0", len(doc.Statement))
	}
}

// pathological/not-action-deny.json: NotAction preserved verbatim.
// Action MUST be empty (the mutex is honored on the input side).
func TestParsePolicyDocument_NotActionPreserved(t *testing.T) {
	input := loadFixture(t, "pathological/not-action-deny.json")
	doc, err := ParsePolicyDocument(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := doc.Statement[0]
	if len(s.Action) != 0 {
		t.Errorf("Action = %v, want empty when NotAction is present", s.Action)
	}
	if len(s.NotAction) != 3 {
		t.Errorf("NotAction len = %d, want 3", len(s.NotAction))
	}
	if s.Effect != EffectDeny {
		t.Errorf("Effect = %q, want %q", s.Effect, EffectDeny)
	}
}

// pathological/not-resource-allow.json: NotResource preserved.
// Action and Resource have different shapes (string vs array).
func TestParsePolicyDocument_NotResourcePreserved(t *testing.T) {
	input := loadFixture(t, "pathological/not-resource-allow.json")
	doc, err := ParsePolicyDocument(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := doc.Statement[0]
	if len(s.NotResource) != 2 {
		t.Errorf("NotResource len = %d, want 2", len(s.NotResource))
	}
	if len(s.Resource) != 0 {
		t.Errorf("Resource = %v, want empty when NotResource is present", s.Resource)
	}
}

// AmazonEC2FullAccess has a Condition block. Parser must preserve
// it as RawMessage (v1.1 doesn't evaluate; just flags presence).
func TestParsePolicyDocument_ConditionPreserved(t *testing.T) {
	input := loadFixture(t, "managed/AmazonEC2FullAccess.json")
	doc, err := ParsePolicyDocument(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// AmazonEC2FullAccess's 5th statement has the iam:CreateServiceLinkedRole
	// Condition. Find any statement with Condition; assert it's preserved.
	var withCondition int
	for _, s := range doc.Statement {
		if len(s.Condition) > 0 {
			withCondition++
		}
	}
	if withCondition == 0 {
		t.Fatal("no statement with Condition found — preservation failed")
	}
	// Round-trip the RawMessage to confirm it's valid JSON.
	for _, s := range doc.Statement {
		if len(s.Condition) == 0 {
			continue
		}
		var anyMap map[string]any
		if err := json.Unmarshal(s.Condition, &anyMap); err != nil {
			t.Errorf("Condition isn't valid JSON: %v", err)
		}
	}
}

// pathological/malformed-json.json: unterminated array. Parser MUST
// return error wrapping ErrMalformedPolicy so callers can branch
// via errors.Is.
func TestParsePolicyDocument_MalformedJSON(t *testing.T) {
	input := loadFixture(t, "pathological/malformed-json.json")
	_, err := ParsePolicyDocument(input)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !errors.Is(err, ErrMalformedPolicy) {
		t.Errorf("err = %v, want errors.Is(err, ErrMalformedPolicy) == true", err)
	}
}

// IAM spec says Effect is case-sensitive ("Allow"/"Deny"), but
// real-world policies sometimes appear with lowercase. Parser is
// defensive: accepts case-insensitive match, normalizes to canonical
// EffectAllow / EffectDeny.
func TestParsePolicyDocument_EffectCaseDefensive(t *testing.T) {
	cases := []struct {
		raw  string
		want Effect
	}{
		{"Allow", EffectAllow},
		{"allow", EffectAllow},
		{"ALLOW", EffectAllow},
		{"Deny", EffectDeny},
		{"deny", EffectDeny},
		{"DENY", EffectDeny},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			raw := []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"` + tc.raw + `","Action":"s3:GetObject","Resource":"*"}]}`)
			doc, err := ParsePolicyDocument(raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if doc.Statement[0].Effect != tc.want {
				t.Errorf("Effect %q parsed to %q, want %q", tc.raw, doc.Statement[0].Effect, tc.want)
			}
		})
	}
}

// Effect with an unknown value (typo, etc.) is malformed.
func TestParsePolicyDocument_EffectUnknown(t *testing.T) {
	raw := []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Maybe","Action":"s3:Get","Resource":"*"}]}`)
	_, err := ParsePolicyDocument(raw)
	if err == nil {
		t.Fatal("expected error for unknown Effect, got nil")
	}
	if !errors.Is(err, ErrMalformedPolicy) {
		t.Errorf("want ErrMalformedPolicy wrapping, got %v", err)
	}
}

// Sid is optional; absent Sid is fine, must round-trip as "".
func TestParsePolicyDocument_SidOptional(t *testing.T) {
	input := loadFixture(t, "managed/AdministratorAccess.json")
	doc, _ := ParsePolicyDocument(input)
	if doc.Statement[0].Sid != "" {
		t.Errorf("Sid = %q, want empty", doc.Statement[0].Sid)
	}
}

// Sid present is preserved.
func TestParsePolicyDocument_SidPreserved(t *testing.T) {
	input := loadFixture(t, "managed/SecretsManagerReadWrite.json")
	doc, err := ParsePolicyDocument(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	wantSids := []string{"BasePermissions", "LambdaPermissions"}
	if len(doc.Statement) != len(wantSids) {
		t.Fatalf("statement len = %d, want %d", len(doc.Statement), len(wantSids))
	}
	for i, want := range wantSids {
		if doc.Statement[i].Sid != want {
			t.Errorf("Sid[%d] = %q, want %q", i, doc.Statement[i].Sid, want)
		}
	}
}

// pathological/arn-variable-username.json: Resource contains
// ${aws:username}. Parser preserves the literal string — no
// substitution in v1.1.
func TestParsePolicyDocument_ARNVariablePreserved(t *testing.T) {
	input := loadFixture(t, "pathological/arn-variable-username.json")
	doc, err := ParsePolicyDocument(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := doc.Statement[0]
	found := false
	for _, r := range s.Resource {
		if strings.Contains(r, "${aws:username}") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ARN variable not preserved in Resource = %v", s.Resource)
	}
}

// pathological/fifty-statements.json: 50 statements, all valid.
// Tests we don't bail out partway through a long array.
func TestParsePolicyDocument_ManyStatements(t *testing.T) {
	input := loadFixture(t, "pathological/fifty-statements.json")
	doc, err := ParsePolicyDocument(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Statement) != 50 {
		t.Errorf("Statement len = %d, want 50", len(doc.Statement))
	}
}

// PowerUserAccess mixes a NotAction statement with a regular
// Action statement — parser must handle both in the same document.
func TestParsePolicyDocument_MixedNotActionAndAction(t *testing.T) {
	input := loadFixture(t, "managed/PowerUserAccess.json")
	doc, err := ParsePolicyDocument(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Statement) != 2 {
		t.Fatalf("Statement len = %d, want 2", len(doc.Statement))
	}
	// Stmt 0: NotAction-only.
	if len(doc.Statement[0].NotAction) == 0 {
		t.Errorf("Statement[0] should have NotAction; got %+v", doc.Statement[0])
	}
	if len(doc.Statement[0].Action) != 0 {
		t.Errorf("Statement[0].Action should be empty (mutex with NotAction); got %v", doc.Statement[0].Action)
	}
	// Stmt 1: Action-only.
	if len(doc.Statement[1].Action) == 0 {
		t.Errorf("Statement[1] should have Action; got %+v", doc.Statement[1])
	}
	if len(doc.Statement[1].NotAction) != 0 {
		t.Errorf("Statement[1].NotAction should be empty; got %v", doc.Statement[1].NotAction)
	}
}

// AWS sometimes emits whitespace in Effect ("  Allow  ") if a
// generator over-pads. Parser must trim defensively.
func TestParsePolicyDocument_EffectWhitespaceTrimmed(t *testing.T) {
	raw := []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"  Allow  ","Action":"s3:GetObject","Resource":"*"}]}`)
	doc, err := ParsePolicyDocument(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.Statement[0].Effect != EffectAllow {
		t.Errorf("Effect = %q, want %q", doc.Statement[0].Effect, EffectAllow)
	}
}
