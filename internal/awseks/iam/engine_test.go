package iam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
)

// ── Stub PolicyFetcher ───────────────────────────────────────────

// stubFetcher answers PolicyFetcher calls from in-memory maps,
// counts each call, and can inject errors. Sufficient for engine
// tests without involving the AWS SDK or the testdata loader.
type stubFetcher struct {
	inlineList   map[string][]string          // roleArn → []policyName
	inlineDoc    map[string]map[string][]byte // roleArn → policyName → docJSON
	attachedList map[string][]AttachedPolicy  // roleArn → attached refs
	managedDoc   map[string][]byte            // policyArn → docJSON

	// Error injection — non-nil means the respective method returns
	// it on every call (next call after err is reset returns success).
	listRolePoliciesErr        error
	getRolePolicyErr           error
	listAttachedRolePoliciesErr error
	getPolicyDocumentErr       error

	// Targeted error: a specific (roleArn, policyName) inline fetch
	// fails, others succeed. Used to test partial-fetch soft-fail.
	getRolePolicyTargetErr map[string]error // "roleArn::policyName" → err

	calls struct {
		listRolePolicies         int32
		getRolePolicy            int32
		listAttachedRolePolicies int32
		getPolicyDocument        int32
	}
}

func (s *stubFetcher) ListRolePolicies(ctx context.Context, roleArn string) ([]string, error) {
	atomic.AddInt32(&s.calls.listRolePolicies, 1)
	if s.listRolePoliciesErr != nil {
		return nil, s.listRolePoliciesErr
	}
	return s.inlineList[roleArn], nil
}
func (s *stubFetcher) GetRolePolicy(ctx context.Context, roleArn, policyName string) (json.RawMessage, error) {
	atomic.AddInt32(&s.calls.getRolePolicy, 1)
	if s.getRolePolicyErr != nil {
		return nil, s.getRolePolicyErr
	}
	if err, ok := s.getRolePolicyTargetErr[roleArn+"::"+policyName]; ok {
		return nil, err
	}
	doc := s.inlineDoc[roleArn][policyName]
	return json.RawMessage(doc), nil
}
func (s *stubFetcher) ListAttachedRolePolicies(ctx context.Context, roleArn string) ([]AttachedPolicy, error) {
	atomic.AddInt32(&s.calls.listAttachedRolePolicies, 1)
	if s.listAttachedRolePoliciesErr != nil {
		return nil, s.listAttachedRolePoliciesErr
	}
	return s.attachedList[roleArn], nil
}
func (s *stubFetcher) GetPolicyDocument(ctx context.Context, policyArn string) (json.RawMessage, error) {
	atomic.AddInt32(&s.calls.getPolicyDocument, 1)
	if s.getPolicyDocumentErr != nil {
		return nil, s.getPolicyDocumentErr
	}
	doc, ok := s.managedDoc[policyArn]
	if !ok {
		return nil, fmt.Errorf("stub: no managed doc for %s", policyArn)
	}
	return json.RawMessage(doc), nil
}

// ── Stub SARoleIndexer ───────────────────────────────────────────

type stubSAIndexer struct {
	bindings map[string][]SARoleBinding // cluster → bindings
	err      error
}

func (s *stubSAIndexer) SARoleSnapshot(ctx context.Context, cluster string) ([]SARoleBinding, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.bindings[cluster], nil
}

// ── Helpers ──────────────────────────────────────────────────────

func newTestEngine(t *testing.T, f *stubFetcher, s *stubSAIndexer) (*Engine, *clockwork.FakeClock) {
	t.Helper()
	if f == nil {
		f = &stubFetcher{}
	}
	if s == nil {
		s = &stubSAIndexer{}
	}
	clock := clockwork.NewFakeClock()
	e := NewEngine("test-cluster", f, s, Config{}, slog.Default())
	e.clock = clock
	return e, clock
}

func adminPolicyJSON() []byte {
	return []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`)
}

func passrolePolicyJSON() []byte {
	return []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"iam:PassRole","Resource":"*"}]}`)
}

// ── Construction defaults ───────────────────────────────────────

func TestNewEngine_Defaults(t *testing.T) {
	e := NewEngine("c", &stubFetcher{}, &stubSAIndexer{}, Config{}, nil)
	if e.cfg.PolicyTTL != DefaultPolicyTTL {
		t.Errorf("PolicyTTL = %v, want default", e.cfg.PolicyTTL)
	}
	if e.cfg.MaxRowsCap != DefaultMaxRowsCap {
		t.Errorf("MaxRowsCap = %d, want default", e.cfg.MaxRowsCap)
	}
	if e.clusterName != "c" {
		t.Errorf("clusterName = %q", e.clusterName)
	}
}

// ── RolePermissions cache behavior ──────────────────────────────

func TestRolePermissions_ColdMissThenCacheHit(t *testing.T) {
	roleArn := "arn:aws:iam::123:role/r"
	f := &stubFetcher{
		inlineList: map[string][]string{roleArn: {"inline1"}},
		inlineDoc:  map[string]map[string][]byte{roleArn: {"inline1": passrolePolicyJSON()}},
	}
	e, _ := newTestEngine(t, f, nil)
	ctx := context.Background()

	r1, err := e.RolePermissions(ctx, roleArn)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(r1.Permissions) != 1 {
		t.Fatalf("perms = %d, want 1", len(r1.Permissions))
	}
	if atomic.LoadInt32(&f.calls.getRolePolicy) != 1 {
		t.Errorf("getRolePolicy calls = %d, want 1", f.calls.getRolePolicy)
	}

	// Second call within TTL — must hit cache, no new fetches.
	r2, err := e.RolePermissions(ctx, roleArn)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if len(r2.Permissions) != 1 {
		t.Errorf("cache hit returned %d perms, want 1", len(r2.Permissions))
	}
	if atomic.LoadInt32(&f.calls.getRolePolicy) != 1 {
		t.Errorf("getRolePolicy calls after cache hit = %d, want still 1", f.calls.getRolePolicy)
	}
}

func TestRolePermissions_TTLExpiryRefetches(t *testing.T) {
	roleArn := "arn:aws:iam::123:role/r"
	f := &stubFetcher{
		inlineList: map[string][]string{roleArn: {"inline1"}},
		inlineDoc:  map[string]map[string][]byte{roleArn: {"inline1": passrolePolicyJSON()}},
	}
	e, clock := newTestEngine(t, f, nil)
	ctx := context.Background()

	if _, err := e.RolePermissions(ctx, roleArn); err != nil {
		t.Fatalf("first: %v", err)
	}
	clock.Advance(DefaultPolicyTTL + time.Second)
	if _, err := e.RolePermissions(ctx, roleArn); err != nil {
		t.Fatalf("second: %v", err)
	}
	if got := atomic.LoadInt32(&f.calls.getRolePolicy); got != 2 {
		t.Errorf("getRolePolicy calls after TTL expiry = %d, want 2", got)
	}
}

// ── Soft-fail semantics ─────────────────────────────────────────

// One inline policy fetch errors; others succeed. Engine returns
// the partial result with PolicyFetchPartial=true. Caller can
// render banner + still show the rows that did succeed.
func TestRolePermissions_PartialFetchSoftFail(t *testing.T) {
	roleArn := "arn:aws:iam::123:role/r"
	f := &stubFetcher{
		inlineList: map[string][]string{roleArn: {"good", "bad"}},
		inlineDoc: map[string]map[string][]byte{
			roleArn: {
				"good": passrolePolicyJSON(),
				"bad":  passrolePolicyJSON(),
			},
		},
		getRolePolicyTargetErr: map[string]error{
			roleArn + "::bad": errors.New("aws: AccessDenied for bad"),
		},
	}
	e, _ := newTestEngine(t, f, nil)

	result, err := e.RolePermissions(context.Background(), roleArn)
	if err == nil {
		t.Error("err = nil, want non-nil (partial-fetch case)")
	}
	if !result.PolicyFetchPartial {
		t.Error("PolicyFetchPartial = false, want true")
	}
	if len(result.Permissions) != 1 {
		t.Errorf("Permissions = %d, want 1 (good policy's row only)", len(result.Permissions))
	}
}

// ListRolePolicies errors entirely — inline branch fails but
// attached/managed still attempted. PolicyFetchPartial true.
func TestRolePermissions_ListInlineFailsButManagedSucceeds(t *testing.T) {
	roleArn := "arn:aws:iam::123:role/r"
	f := &stubFetcher{
		listRolePoliciesErr: errors.New("aws: AccessDenied"),
		attachedList: map[string][]AttachedPolicy{
			roleArn: {{PolicyArn: "arn:aws:iam::aws:policy/AdministratorAccess", PolicyName: "AdministratorAccess"}},
		},
		managedDoc: map[string][]byte{
			"arn:aws:iam::aws:policy/AdministratorAccess": adminPolicyJSON(),
		},
	}
	e, _ := newTestEngine(t, f, nil)
	result, err := e.RolePermissions(context.Background(), roleArn)
	if err == nil {
		t.Error("err = nil, want non-nil")
	}
	if !result.PolicyFetchPartial {
		t.Error("PolicyFetchPartial = false, want true")
	}
	if len(result.Permissions) != 1 {
		t.Errorf("Permissions = %d, want 1 (the admin row)", len(result.Permissions))
	}
}

// Malformed JSON from the SDK → soft-fail; rest of the policies
// still parse.
func TestRolePermissions_MalformedPolicySoftFail(t *testing.T) {
	roleArn := "arn:aws:iam::123:role/r"
	f := &stubFetcher{
		inlineList: map[string][]string{roleArn: {"good", "broken"}},
		inlineDoc: map[string]map[string][]byte{
			roleArn: {
				"good":   passrolePolicyJSON(),
				"broken": []byte("{not valid json"),
			},
		},
	}
	e, _ := newTestEngine(t, f, nil)
	result, err := e.RolePermissions(context.Background(), roleArn)
	if err == nil {
		t.Error("want non-nil err")
	}
	if !result.PolicyFetchPartial {
		t.Error("PolicyFetchPartial = false, want true")
	}
	if len(result.Permissions) != 1 {
		t.Errorf("Permissions = %d, want 1 (good policy only)", len(result.Permissions))
	}
}

// ── MaxRowsCap truncation ────────────────────────────────────────

func TestRolePermissions_TruncatesAtCap(t *testing.T) {
	// Build a policy with many actions × resources.
	actions := []string{}
	resources := []string{}
	for i := 0; i < 20; i++ {
		actions = append(actions, fmt.Sprintf(`"s3:Action%d"`, i))
		resources = append(resources, fmt.Sprintf(`"arn:aws:s3:::bucket-%d"`, i))
	}
	doc := []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":[` +
		strings.Join(actions, ",") + `],"Resource":[` + strings.Join(resources, ",") + `]}]}`)

	roleArn := "arn:aws:iam::123:role/r"
	f := &stubFetcher{
		inlineList: map[string][]string{roleArn: {"big"}},
		inlineDoc:  map[string]map[string][]byte{roleArn: {"big": doc}},
	}
	e, _ := newTestEngine(t, f, nil)
	// 20 × 20 = 400 rows; cap at 50.
	e.cfg.MaxRowsCap = 50

	result, err := e.RolePermissions(context.Background(), roleArn)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !result.Truncated {
		t.Error("Truncated = false, want true")
	}
	if result.TotalCount != 400 {
		t.Errorf("TotalCount = %d, want 400", result.TotalCount)
	}
	if len(result.Permissions) != 50 {
		t.Errorf("len(Permissions) = %d, want 50 (capped)", len(result.Permissions))
	}
}

// Cap not exceeded → Truncated=false, TotalCount==len(Permissions).
func TestRolePermissions_NotTruncatedUnderCap(t *testing.T) {
	roleArn := "arn:aws:iam::123:role/r"
	f := &stubFetcher{
		inlineList: map[string][]string{roleArn: {"inline"}},
		inlineDoc:  map[string]map[string][]byte{roleArn: {"inline": passrolePolicyJSON()}},
	}
	e, _ := newTestEngine(t, f, nil)
	result, err := e.RolePermissions(context.Background(), roleArn)
	if err != nil {
		t.Fatal(err)
	}
	if result.Truncated {
		t.Error("Truncated = true under cap")
	}
	if result.TotalCount != len(result.Permissions) {
		t.Errorf("TotalCount %d != len(Permissions) %d", result.TotalCount, len(result.Permissions))
	}
}

// ── ConsoleURL per partition ────────────────────────────────────

func TestRolePermissions_ConsoleURL_AWSPartition(t *testing.T) {
	roleArn := "arn:aws:iam::123:role/r"
	// NotAction policy → RawStatement with ConsoleURL.
	doc := []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Deny","NotAction":["iam:*"],"Resource":"*"}]}`)
	f := &stubFetcher{
		inlineList: map[string][]string{roleArn: {"inline-not-action"}},
		inlineDoc:  map[string]map[string][]byte{roleArn: {"inline-not-action": doc}},
	}
	e, _ := newTestEngine(t, f, nil)
	result, _ := e.RolePermissions(context.Background(), roleArn)
	if len(result.RawStatements) != 1 {
		t.Fatalf("RawStatements = %d, want 1", len(result.RawStatements))
	}
	url := result.RawStatements[0].ConsoleURL
	if !strings.Contains(url, "console.aws.amazon.com") {
		t.Errorf("ConsoleURL = %q, want console.aws.amazon.com (aws partition)", url)
	}
	// Inline → role page deep link
	if !strings.Contains(url, "/roles/r") {
		t.Errorf("ConsoleURL = %q, want /roles/r (inline → role deep link)", url)
	}
}

func TestRolePermissions_ConsoleURL_GovCloud(t *testing.T) {
	roleArn := "arn:aws-us-gov:iam::123:role/gov-role"
	doc := []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","NotResource":["arn:x"],"Action":"s3:GetObject"}]}`)
	f := &stubFetcher{
		inlineList: map[string][]string{roleArn: {"i"}},
		inlineDoc:  map[string]map[string][]byte{roleArn: {"i": doc}},
	}
	e, _ := newTestEngine(t, f, nil)
	result, _ := e.RolePermissions(context.Background(), roleArn)
	if len(result.RawStatements) != 1 {
		t.Fatalf("RawStatements = %d, want 1", len(result.RawStatements))
	}
	if !strings.Contains(result.RawStatements[0].ConsoleURL, "amazonaws-us-gov.com") {
		t.Errorf("ConsoleURL = %q, want gov-cloud console host", result.RawStatements[0].ConsoleURL)
	}
}

func TestRolePermissions_ConsoleURL_China(t *testing.T) {
	roleArn := "arn:aws-cn:iam::123:role/cn-role"
	doc := []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","NotResource":["arn:x"],"Action":"s3:GetObject"}]}`)
	f := &stubFetcher{
		inlineList: map[string][]string{roleArn: {"i"}},
		inlineDoc:  map[string]map[string][]byte{roleArn: {"i": doc}},
	}
	e, _ := newTestEngine(t, f, nil)
	result, _ := e.RolePermissions(context.Background(), roleArn)
	if !strings.Contains(result.RawStatements[0].ConsoleURL, "amazonaws.cn") {
		t.Errorf("ConsoleURL = %q, want china console host", result.RawStatements[0].ConsoleURL)
	}
}

// Managed policy → policy-page deep link, NOT role page.
func TestRolePermissions_ConsoleURL_ManagedPolicy(t *testing.T) {
	roleArn := "arn:aws:iam::123:role/r"
	policyArn := "arn:aws:iam::123:policy/CustomDeny"
	doc := []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Deny","NotAction":["iam:*"],"Resource":"*"}]}`)
	f := &stubFetcher{
		attachedList: map[string][]AttachedPolicy{
			roleArn: {{PolicyArn: policyArn, PolicyName: "CustomDeny"}},
		},
		managedDoc: map[string][]byte{policyArn: doc},
	}
	e, _ := newTestEngine(t, f, nil)
	result, _ := e.RolePermissions(context.Background(), roleArn)
	if len(result.RawStatements) != 1 {
		t.Fatalf("RawStatements = %d, want 1", len(result.RawStatements))
	}
	url := result.RawStatements[0].ConsoleURL
	if !strings.Contains(url, "/policies/") {
		t.Errorf("ConsoleURL = %q, want /policies/... (managed → policy deep link)", url)
	}
	if strings.Contains(url, "/roles/") {
		t.Errorf("managed-policy ConsoleURL must not link to role; got %q", url)
	}
}

// ── Sort determinism ────────────────────────────────────────────

func TestRolePermissions_SortedDeterministic(t *testing.T) {
	roleArn := "arn:aws:iam::123:role/r"
	// Mixed services, mixed actions per service.
	doc := []byte(`{"Version":"2012-10-17","Statement":[
		{"Effect":"Allow","Action":["s3:PutObject","iam:CreateRole","s3:GetObject"],"Resource":"*"}
	]}`)
	f := &stubFetcher{
		inlineList: map[string][]string{roleArn: {"i"}},
		inlineDoc:  map[string]map[string][]byte{roleArn: {"i": doc}},
	}
	e, _ := newTestEngine(t, f, nil)
	r1, _ := e.RolePermissions(context.Background(), roleArn)

	// Trigger refetch via cache invalidation: bump time past TTL.
	clock := e.clock.(*clockwork.FakeClock)
	clock.Advance(DefaultPolicyTTL + time.Second)
	r2, _ := e.RolePermissions(context.Background(), roleArn)

	// Same input → same order, every call.
	if len(r1.Permissions) != len(r2.Permissions) {
		t.Fatalf("len mismatch: %d vs %d", len(r1.Permissions), len(r2.Permissions))
	}
	for i := range r1.Permissions {
		if r1.Permissions[i].Action != r2.Permissions[i].Action {
			t.Errorf("non-deterministic order at [%d]: %q vs %q",
				i, r1.Permissions[i].Action, r2.Permissions[i].Action)
		}
	}

	// First action sorted alphabetically (iam:CreateRole < s3:GetObject < s3:PutObject).
	if r1.Permissions[0].Action != "iam:CreateRole" {
		t.Errorf("sort order: first action = %q, want iam:CreateRole", r1.Permissions[0].Action)
	}
}

// ── ReverseLookup ────────────────────────────────────────────────

func TestReverseLookup_FindsSAWithMatchingAction(t *testing.T) {
	roleArn := "arn:aws:iam::123:role/r"
	f := &stubFetcher{
		inlineList: map[string][]string{roleArn: {"i"}},
		inlineDoc: map[string]map[string][]byte{
			roleArn: {"i": passrolePolicyJSON()},
		},
	}
	s := &stubSAIndexer{
		bindings: map[string][]SARoleBinding{
			"test-cluster": {
				{SAName: "api-sa", Namespace: "prod", RoleArn: roleArn},
			},
		},
	}
	e, _ := newTestEngine(t, f, s)

	matches, err := e.ReverseLookup(context.Background(), ReverseLookupQuery{
		Action: "iam:PassRole",
	})
	if err != nil {
		t.Fatalf("ReverseLookup: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(matches))
	}
	m := matches[0]
	if m.SAName != "api-sa" || m.Namespace != "prod" || m.RoleArn != roleArn {
		t.Errorf("match attribution = %+v", m)
	}
	if m.Permission.Action != "iam:PassRole" {
		t.Errorf("Permission.Action = %q", m.Permission.Action)
	}
}

func TestReverseLookup_NoMatchOnDifferentAction(t *testing.T) {
	roleArn := "arn:aws:iam::123:role/r"
	f := &stubFetcher{
		inlineList: map[string][]string{roleArn: {"i"}},
		inlineDoc: map[string]map[string][]byte{
			roleArn: {"i": passrolePolicyJSON()}, // grants iam:PassRole only
		},
	}
	s := &stubSAIndexer{
		bindings: map[string][]SARoleBinding{
			"test-cluster": {{SAName: "sa", Namespace: "ns", RoleArn: roleArn}},
		},
	}
	e, _ := newTestEngine(t, f, s)

	matches, err := e.ReverseLookup(context.Background(), ReverseLookupQuery{
		Action: "s3:DeleteBucket", // not granted
	})
	if err != nil {
		t.Fatalf("ReverseLookup: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("matches = %d, want 0", len(matches))
	}
}

func TestReverseLookup_WildcardActionPolicyMatches(t *testing.T) {
	// Policy grants s3:* — query for s3:DeleteBucket should hit.
	roleArn := "arn:aws:iam::123:role/r"
	doc := []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`)
	f := &stubFetcher{
		inlineList: map[string][]string{roleArn: {"i"}},
		inlineDoc:  map[string]map[string][]byte{roleArn: {"i": doc}},
	}
	s := &stubSAIndexer{
		bindings: map[string][]SARoleBinding{
			"test-cluster": {{SAName: "sa", Namespace: "ns", RoleArn: roleArn}},
		},
	}
	e, _ := newTestEngine(t, f, s)
	matches, _ := e.ReverseLookup(context.Background(), ReverseLookupQuery{Action: "s3:DeleteBucket"})
	if len(matches) != 1 {
		t.Errorf("matches = %d, want 1 (s3:* should match s3:DeleteBucket)", len(matches))
	}
}

func TestReverseLookup_NamespaceFilter(t *testing.T) {
	roleArn := "arn:aws:iam::123:role/r"
	f := &stubFetcher{
		inlineList: map[string][]string{roleArn: {"i"}},
		inlineDoc:  map[string]map[string][]byte{roleArn: {"i": passrolePolicyJSON()}},
	}
	s := &stubSAIndexer{
		bindings: map[string][]SARoleBinding{
			"test-cluster": {
				{SAName: "a", Namespace: "prod", RoleArn: roleArn},
				{SAName: "b", Namespace: "staging", RoleArn: roleArn},
			},
		},
	}
	e, _ := newTestEngine(t, f, s)

	matches, _ := e.ReverseLookup(context.Background(), ReverseLookupQuery{
		Action:    "iam:PassRole",
		Namespace: "prod",
	})
	if len(matches) != 1 {
		t.Fatalf("matches = %d, want 1 (namespace-scoped)", len(matches))
	}
	if matches[0].Namespace != "prod" {
		t.Errorf("matched namespace = %q, want prod", matches[0].Namespace)
	}
}

func TestReverseLookup_DedupRoleFetch(t *testing.T) {
	// Two SAs binding the same role — engine must fetch policies once.
	roleArn := "arn:aws:iam::123:role/shared"
	f := &stubFetcher{
		inlineList: map[string][]string{roleArn: {"i"}},
		inlineDoc:  map[string]map[string][]byte{roleArn: {"i": passrolePolicyJSON()}},
	}
	s := &stubSAIndexer{
		bindings: map[string][]SARoleBinding{
			"test-cluster": {
				{SAName: "a", Namespace: "ns", RoleArn: roleArn},
				{SAName: "b", Namespace: "ns", RoleArn: roleArn},
			},
		},
	}
	e, _ := newTestEngine(t, f, s)
	matches, _ := e.ReverseLookup(context.Background(), ReverseLookupQuery{Action: "iam:PassRole"})
	if len(matches) != 2 {
		t.Errorf("matches = %d, want 2 (both SAs)", len(matches))
	}
	if got := atomic.LoadInt32(&f.calls.getRolePolicy); got != 1 {
		t.Errorf("getRolePolicy calls = %d, want 1 (role fetch must dedupe)", got)
	}
}

func TestReverseLookup_RequiresAction(t *testing.T) {
	e, _ := newTestEngine(t, nil, nil)
	_, err := e.ReverseLookup(context.Background(), ReverseLookupQuery{})
	if err == nil {
		t.Error("err = nil for empty Action, want non-nil")
	}
}

func TestReverseLookup_ResourceFilter(t *testing.T) {
	// Policy: s3:GetObject on arn:aws:s3:::my-bucket/*
	roleArn := "arn:aws:iam::123:role/r"
	doc := []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"arn:aws:s3:::my-bucket/*"}]}`)
	f := &stubFetcher{
		inlineList: map[string][]string{roleArn: {"i"}},
		inlineDoc:  map[string]map[string][]byte{roleArn: {"i": doc}},
	}
	s := &stubSAIndexer{
		bindings: map[string][]SARoleBinding{
			"test-cluster": {{SAName: "sa", Namespace: "ns", RoleArn: roleArn}},
		},
	}
	e, _ := newTestEngine(t, f, s)

	// Hit: same bucket
	matches, _ := e.ReverseLookup(context.Background(), ReverseLookupQuery{
		Action: "s3:GetObject", Resource: "arn:aws:s3:::my-bucket/some-file",
	})
	if len(matches) != 1 {
		t.Errorf("same-bucket query: matches = %d, want 1", len(matches))
	}
	// Miss: different bucket
	matches, _ = e.ReverseLookup(context.Background(), ReverseLookupQuery{
		Action: "s3:GetObject", Resource: "arn:aws:s3:::other-bucket/some-file",
	})
	if len(matches) != 0 {
		t.Errorf("different-bucket query: matches = %d, want 0", len(matches))
	}
}
