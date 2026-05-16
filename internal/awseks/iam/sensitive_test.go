package iam

import (
	"strings"
	"testing"
)

func TestDefaultCatalog_Loads(t *testing.T) {
	c := DefaultCatalog()
	if c == nil {
		t.Fatal("DefaultCatalog() returned nil")
	}
	if c.Version == "" {
		t.Fatal("catalog Version is empty — sensitive.yaml must have a version")
	}
	if c.Size() < 10 {
		t.Errorf("catalog size = %d, want at least 10 entries (got smaller; check sensitive.yaml)", c.Size())
	}
}

func TestDefaultCatalog_VersionMatchesPackageVar(t *testing.T) {
	c := DefaultCatalog()
	if SensitivePermissionsCatalogVersion != c.Version {
		t.Errorf("package var = %q, catalog.Version = %q — must stay in sync",
			SensitivePermissionsCatalogVersion, c.Version)
	}
}

func TestClassify_ExactMatch(t *testing.T) {
	c := DefaultCatalog()
	cases := []struct {
		action string
		want   SensitiveCategory
	}{
		{"iam:PassRole", SensitivePrivEsc},
		{"iam:CreateAccessKey", SensitivePrivEsc},
		{"secretsmanager:GetSecretValue", SensitiveData},
		{"kms:Decrypt", SensitiveData},
		{"sts:AssumeRole", SensitiveCrossAccount},
		{"s3:DeleteBucket", SensitiveDestructive},
		{"ec2:TerminateInstances", SensitiveDestructive},
		{"eks:DeleteCluster", SensitiveCluster},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			got, ok := c.Classify(tc.action)
			if !ok {
				t.Fatalf("Classify(%q) = (_, false), want sensitive=true", tc.action)
			}
			if got != tc.want {
				t.Errorf("Classify(%q) = %q, want %q", tc.action, got, tc.want)
			}
		})
	}
}

func TestClassify_PatternMatch(t *testing.T) {
	// s3:DeleteObject* and ssm:GetParameter* are pattern entries.
	c := DefaultCatalog()
	cases := []struct {
		action string
		want   SensitiveCategory
	}{
		{"s3:DeleteObject", SensitiveDestructive},
		{"s3:DeleteObjectVersion", SensitiveDestructive},
		{"s3:DeleteObjectTagging", SensitiveDestructive},
		{"ssm:GetParameter", SensitiveData},
		{"ssm:GetParameters", SensitiveData},
		{"ssm:GetParametersByPath", SensitiveData},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			got, ok := c.Classify(tc.action)
			if !ok {
				t.Fatalf("Classify(%q) = (_, false), want sensitive=true (pattern match)", tc.action)
			}
			if got != tc.want {
				t.Errorf("Classify(%q) = %q, want %q", tc.action, got, tc.want)
			}
		})
	}
}

func TestClassify_LiteralWildcard(t *testing.T) {
	// The literal "*" action is handled outside the YAML — always
	// returns SensitiveWildcard regardless of catalog contents.
	c := DefaultCatalog()
	got, ok := c.Classify("*")
	if !ok {
		t.Fatal(`Classify("*") = (_, false), want sensitive=true (wildcard fallback)`)
	}
	if got != SensitiveWildcard {
		t.Errorf(`Classify("*") = %q, want %q`, got, SensitiveWildcard)
	}
}

func TestClassify_CaseInsensitive(t *testing.T) {
	c := DefaultCatalog()
	// IAM actions are case-insensitive in evaluation. The matcher
	// normalizes to lower-case; classify should agree.
	cases := []string{
		"IAM:PassRole",
		"iam:passrole",
		"Iam:PassRole",
		"iam:PASSROLE",
	}
	for _, action := range cases {
		t.Run(action, func(t *testing.T) {
			got, ok := c.Classify(action)
			if !ok {
				t.Fatalf("Classify(%q) = (_, false), want sensitive=true", action)
			}
			if got != SensitivePrivEsc {
				t.Errorf("Classify(%q) = %q, want %q", action, got, SensitivePrivEsc)
			}
		})
	}
}

func TestClassify_NonSensitive(t *testing.T) {
	c := DefaultCatalog()
	cases := []string{
		"s3:GetObject",
		"ec2:DescribeInstances",
		"sts:GetCallerIdentity",
		"logs:CreateLogStream",
		"",
		"   ",
	}
	for _, action := range cases {
		t.Run(action, func(t *testing.T) {
			_, ok := c.Classify(action)
			if ok {
				t.Errorf("Classify(%q) returned sensitive=true, want false", action)
			}
		})
	}
}

func TestLoadCatalog_RejectsUnknownCategory(t *testing.T) {
	data := []byte(`
version: "test"
entries:
  "foo:bar": invented-category
`)
	_, err := LoadCatalog(data)
	if err == nil {
		t.Fatal("LoadCatalog accepted unknown category, want error")
	}
	if !strings.Contains(err.Error(), "unknown category") {
		t.Errorf("err = %v, want 'unknown category' wording", err)
	}
}

func TestLoadCatalog_RejectsMissingVersion(t *testing.T) {
	data := []byte(`
entries:
  "iam:PassRole": privilege-escalation
`)
	_, err := LoadCatalog(data)
	if err == nil {
		t.Fatal("LoadCatalog accepted catalog without version, want error")
	}
}

func TestLoadCatalog_RejectsMalformedYAML(t *testing.T) {
	_, err := LoadCatalog([]byte("not: [valid yaml"))
	if err == nil {
		t.Fatal("LoadCatalog accepted malformed YAML, want error")
	}
}

func TestLoadCatalog_EmptyKeyIgnored(t *testing.T) {
	// Empty keys (e.g., from a trailing comma in YAML) are silently
	// skipped rather than failing the load — defensive parsing.
	data := []byte(`
version: "test"
entries:
  "": data
  "iam:PassRole": privilege-escalation
`)
	c, err := LoadCatalog(data)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if c.Size() != 1 {
		t.Errorf("size = %d, want 1 (empty key should be dropped)", c.Size())
	}
}

func TestConfig_WithDefaults(t *testing.T) {
	// Zero-value config gets every default populated.
	got := Config{}.WithDefaults()
	if got.PolicyTTL != DefaultPolicyTTL {
		t.Errorf("PolicyTTL = %v, want default %v", got.PolicyTTL, DefaultPolicyTTL)
	}
	if got.MaxRowsCap != DefaultMaxRowsCap {
		t.Errorf("MaxRowsCap = %d, want default %d", got.MaxRowsCap, DefaultMaxRowsCap)
	}
	if got.PodRefsLimit != DefaultPodRefsLimit {
		t.Errorf("PodRefsLimit = %d, want default %d", got.PodRefsLimit, DefaultPodRefsLimit)
	}
}
