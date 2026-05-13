package iam

import "testing"

// ── matchAction ─────────────────────────────────────────────────

func TestMatchAction(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		target  string
		want    bool
	}{
		// Exact matches
		{"exact match", "s3:GetObject", "s3:GetObject", true},
		{"exact mismatch", "s3:GetObject", "s3:PutObject", false},
		{"different service", "s3:GetObject", "iam:GetRole", false},

		// Service-wide wildcards
		{"service:* matches", "s3:*", "s3:GetObject", true},
		{"service:* matches DeleteBucket", "s3:*", "s3:DeleteBucket", true},
		{"service:* does not match different service", "s3:*", "iam:GetRole", false},

		// Action-prefix wildcards
		{"s3:Get* matches GetObject", "s3:Get*", "s3:GetObject", true},
		{"s3:Get* matches GetObjectVersion", "s3:Get*", "s3:GetObjectVersion", true},
		{"s3:Get* does not match PutObject", "s3:Get*", "s3:PutObject", false},

		// Suffix wildcards
		{"s3:*Object matches GetObject", "s3:*Object", "s3:GetObject", true},
		{"s3:*Object matches PutObject", "s3:*Object", "s3:PutObject", true},
		{"s3:*Object does not match Object suffix off", "s3:*Object", "s3:GetObjectVersion", false},

		// Full wildcard
		{"* matches anything", "*", "s3:GetObject", true},
		{"* matches iam action", "*", "iam:PassRole", true},
		{"* matches no-colon action", "*", "WeirdAction", true},

		// Case-insensitivity (IAM evaluator is case-insensitive)
		{"target lower vs pattern canonical", "s3:GetObject", "s3:getobject", true},
		{"pattern lower vs target canonical", "s3:getobject", "s3:GetObject", true},
		{"both lower", "s3:getobject", "s3:getobject", true},
		{"service prefix case-insensitive", "S3:*", "s3:GetObject", true},

		// Single-character wildcard ?
		{"? matches single char", "?3:GetObject", "s3:GetObject", true},
		{"? does not match two chars", "?3:GetObject", "ss3:GetObject", false},

		// Empty edge cases
		{"empty pattern empty target", "", "", true},
		{"empty pattern non-empty target", "", "s3:GetObject", false},
		{"non-empty pattern empty target", "s3:*", "", false},
		{"only star pattern", "*", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchAction(tc.pattern, tc.target)
			if got != tc.want {
				t.Errorf("matchAction(%q, %q) = %v, want %v", tc.pattern, tc.target, got, tc.want)
			}
		})
	}
}

// ── matchResource ───────────────────────────────────────────────

// CRITICAL: IAM resource wildcards cross "/" boundaries — distinct
// from path.Match semantics. arn:aws:s3:::bucket/* matches
// arn:aws:s3:::bucket/path/to/file.txt (any depth). path.Match
// would reject this; this matcher MUST accept it.
func TestMatchResource(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		target  string
		want    bool
	}{
		// Exact matches
		{"exact ARN", "arn:aws:s3:::my-bucket/key.txt", "arn:aws:s3:::my-bucket/key.txt", true},
		{"exact mismatch", "arn:aws:s3:::bucket-a/key", "arn:aws:s3:::bucket-b/key", false},

		// Star-resource (allow-all)
		{"star matches any ARN", "*", "arn:aws:s3:::anything/anywhere", true},
		{"star matches even empty", "*", "", true},

		// Bucket-wildcard
		{"any bucket name", "arn:aws:s3:::*", "arn:aws:s3:::any-bucket", true},
		{"any bucket name doesn't match wrong service", "arn:aws:s3:::*", "arn:aws:iam::123:role/Foo", false},

		// Object-key-wildcard (the critical "/" case)
		{"object key wildcard flat", "arn:aws:s3:::bucket/*", "arn:aws:s3:::bucket/file.txt", true},
		{"object key wildcard nested (depth 1)", "arn:aws:s3:::bucket/*", "arn:aws:s3:::bucket/path/file.txt", true},
		{"object key wildcard nested (depth 4)", "arn:aws:s3:::bucket/*", "arn:aws:s3:::bucket/a/b/c/d/file.txt", true},
		{"object key wildcard wrong bucket", "arn:aws:s3:::bucket/*", "arn:aws:s3:::other-bucket/file.txt", false},

		// Combined bucket+key wildcard
		{"all buckets all keys", "arn:aws:s3:::*/*", "arn:aws:s3:::any-bucket/any-key", true},
		{"all buckets all keys deep nest", "arn:aws:s3:::*/*", "arn:aws:s3:::any-bucket/path/to/key", true},
		{"all-buckets pattern doesn't match bucket-only ARN", "arn:aws:s3:::*/*", "arn:aws:s3:::bucket-no-slash", false},

		// Partition boundaries
		{"aws partition pattern, gov-cloud target", "arn:aws:s3:::*", "arn:aws-us-gov:s3:::bucket", false},
		{"gov-cloud pattern, aws target", "arn:aws-us-gov:s3:::*", "arn:aws:s3:::bucket", false},
		{"china partition exact match", "arn:aws-cn:s3:::cn-bucket/*", "arn:aws-cn:s3:::cn-bucket/key", true},

		// Case-insensitivity (per AWS docs, ARN matching is case-insensitive)
		{"bucket name case-insensitive", "arn:aws:s3:::MyBucket/*", "arn:aws:s3:::mybucket/key", true},
		{"key case-insensitive", "arn:aws:s3:::bucket/Key.txt", "arn:aws:s3:::bucket/key.txt", true},

		// Single-character ?
		{"? matches single ARN char", "arn:aws:s3:::bucket-?", "arn:aws:s3:::bucket-1", true},
		{"? does not match two ARN chars", "arn:aws:s3:::bucket-?", "arn:aws:s3:::bucket-12", false},

		// Empty edge
		{"empty pattern empty target", "", "", true},
		{"empty pattern non-empty target", "", "arn:aws:s3:::bucket", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchResource(tc.pattern, tc.target)
			if got != tc.want {
				t.Errorf("matchResource(%q, %q) = %v, want %v", tc.pattern, tc.target, got, tc.want)
			}
		})
	}
}

// ── EvaluateEffect — explicit Deny > Allow > implicit Deny ──────

func TestEvaluateEffect(t *testing.T) {
	allow := Permission{Effect: EffectAllow}
	deny := Permission{Effect: EffectDeny}

	cases := []struct {
		name  string
		perms []Permission
		want  bool
	}{
		{"empty → implicit deny", nil, false},
		{"single Allow", []Permission{allow}, true},
		{"single Deny", []Permission{deny}, false},
		{"Allow + Deny → Deny wins", []Permission{allow, deny}, false},
		{"Deny + Allow → Deny wins", []Permission{deny, allow}, false},
		{"multiple Allows", []Permission{allow, allow, allow}, true},
		{"multiple Denys", []Permission{deny, deny}, false},
		{"Allows + one Deny anywhere", []Permission{allow, allow, deny, allow}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateEffect(tc.perms)
			if got != tc.want {
				t.Errorf("EvaluateEffect(%+v) = %v, want %v", tc.perms, got, tc.want)
			}
		})
	}
}

// ── End-to-end: parser → expand → matcher on fixtures ───────────

// pathological/allow-deny-same-action.json has both an Allow and
// a Deny for s3:GetObject on the same bucket. EvaluateEffect over
// the matching Permission rows must return false (Deny wins).
func TestMatcher_PathologicalAllowDeny(t *testing.T) {
	doc, err := ParsePolicyDocument(loadFixture(t, "pathological/allow-deny-same-action.json"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	queryAction := "s3:GetObject"
	queryResource := "arn:aws:s3:::contested-bucket/file.txt"

	var matching []Permission
	for i, s := range doc.Statement {
		meta := StatementMeta{
			PolicyArn:    "arn:aws:iam::123:policy/Test",
			PolicyName:   "Test",
			PolicySource: PolicySourceManaged,
			StatementIdx: i,
		}
		perms, _ := s.Expand(meta)
		for _, p := range perms {
			if matchAction(p.Action, queryAction) && matchResource(p.Resource, queryResource) {
				matching = append(matching, p)
			}
		}
	}
	if len(matching) != 2 {
		t.Fatalf("matching rows = %d, want 2 (one Allow + one Deny)", len(matching))
	}
	if EvaluateEffect(matching) {
		t.Error("EvaluateEffect = true, want false (explicit Deny must win)")
	}
}

// wildcard/symmetric-intersection.json: s3:* on arn:aws:s3:::query-*.
// A query for s3:GetObject on arn:aws:s3:::query-bucket/key must
// match because BOTH patterns cover the concrete (action, resource).
func TestMatcher_SymmetricWildcardIntersection(t *testing.T) {
	doc, _ := ParsePolicyDocument(loadFixture(t, "wildcard/symmetric-intersection.json"))
	meta := StatementMeta{PolicyName: "T", PolicySource: PolicySourceManaged}
	perms, _ := doc.Statement[0].Expand(meta)
	if len(perms) != 1 {
		t.Fatalf("perms = %d, want 1", len(perms))
	}

	queryAction := "s3:GetObject"
	queryResource := "arn:aws:s3:::query-bucket/key"

	if !matchAction(perms[0].Action, queryAction) {
		t.Errorf("matchAction(%q, %q) = false, want true", perms[0].Action, queryAction)
	}
	if !matchResource(perms[0].Resource, queryResource) {
		t.Errorf("matchResource(%q, %q) = false, want true", perms[0].Resource, queryResource)
	}
}

// wildcard/gov-cloud-partition.json: pattern arn:aws-us-gov:s3:::...
// must NOT match a query against arn:aws:s3:::... (different
// partition). Cross-partition queries should not return hits.
func TestMatcher_PartitionBoundary(t *testing.T) {
	doc, _ := ParsePolicyDocument(loadFixture(t, "wildcard/gov-cloud-partition.json"))
	meta := StatementMeta{PolicyName: "T", PolicySource: PolicySourceManaged}
	perms, _ := doc.Statement[0].Expand(meta)
	if len(perms) != 1 {
		t.Fatalf("perms = %d, want 1", len(perms))
	}

	// Query in commercial AWS partition — must NOT hit the gov-cloud
	// policy.
	if matchResource(perms[0].Resource, "arn:aws:s3:::govcloud-bucket/file") {
		t.Error("gov-cloud pattern matched commercial-partition target; partition boundary not enforced")
	}
	// Same target, gov-cloud partition — should hit.
	if !matchResource(perms[0].Resource, "arn:aws-us-gov:s3:::govcloud-bucket/file") {
		t.Error("gov-cloud pattern did not match gov-cloud target")
	}
}
