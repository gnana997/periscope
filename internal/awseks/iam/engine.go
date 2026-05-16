package iam

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
)

// ── Engine ────────────────────────────────────────────────────────

// Engine is the per-cluster IAM policy resolution engine. Holds the
// SDK seam (PolicyFetcher), the SA→role index seam (SARoleIndexer),
// a per-role policy cache with TTL, and the locked sensitive-perms
// catalog.
//
// One Engine per cluster — mirrors identity.Manager's lifecycle.
// cmd/periscope's engine cache instantiates one Engine the first
// time a handler is invoked for a given cluster, then reuses it.
//
// Concurrency: many readers may call RolePermissions / ReverseLookup
// in parallel. Cache reads use RLock; cache writes happen post-
// fetch under Lock with a double-check.
type Engine struct {
	clusterName string
	client      PolicyFetcher
	saIndex     SARoleIndexer
	catalog     *Catalog
	cfg         Config
	clock       clockwork.Clock
	log         *slog.Logger

	cacheMu sync.RWMutex
	cache   map[string]cacheEntry // keyed by lower-cased role ARN
}

type cacheEntry struct {
	result    RolePermissionsResult
	fetchedAt time.Time
	err       error // last fetch's error (nil on success); cached so retries don't hammer AWS
}

// NewEngine constructs an Engine for the given cluster. Zero-value
// Config fields fall back to DefaultPolicyTTL / DefaultMaxRowsCap /
// DefaultPodRefsLimit.
//
// catalog is intentionally not a constructor parameter — the engine
// uses DefaultCatalog() (the process-wide embedded YAML). Tests
// override via the engine's exported Catalog field if they need to
// exercise alternate catalogs without poisoning the default.
func NewEngine(clusterName string, client PolicyFetcher, saIndex SARoleIndexer, cfg Config, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	return &Engine{
		clusterName: clusterName,
		client:      client,
		saIndex:     saIndex,
		catalog:     DefaultCatalog(),
		cfg:         cfg.WithDefaults(),
		clock:       clockwork.NewRealClock(),
		log:         log,
		cache:       map[string]cacheEntry{},
	}
}

// ── RolePermissions ──────────────────────────────────────────────

// RolePermissions returns the projected, render-ready Permission
// rows for an IAM role plus any RawStatement entries the SPA can't
// render directly (NotAction / NotResource / NotPrincipal).
//
// Two-pass TTL cache: pass 1 reads under RLock; on cache miss the
// fetch happens without a lock (long network calls), then the
// result is stamped into the cache under Lock. Two concurrent
// requests for the same role on a cold cache will both fetch; we
// accept the small AWS-call duplication in v1.1 rather than add
// singleflight (matches identity.Manager's pattern).
//
// Soft-fail mirror: if any individual policy fetch errors,
// PolicyFetchPartial is set true and the returned err is the first
// non-nil fetch error. The SPA renders a banner; the result still
// carries every Permission row that did parse.
func (e *Engine) RolePermissions(ctx context.Context, roleArn string) (RolePermissionsResult, error) {
	key := strings.ToLower(roleArn)

	// Pass 1: cache check.
	e.cacheMu.RLock()
	entry, ok := e.cache[key]
	e.cacheMu.RUnlock()
	if ok && e.clock.Now().Sub(entry.fetchedAt) < e.cfg.PolicyTTL {
		return entry.result, entry.err
	}

	// Pass 2: fetch + expand (no lock held — long network calls).
	result, fetchErr := e.fetchAndExpand(ctx, roleArn)

	// Pass 3: stamp into cache.
	e.cacheMu.Lock()
	e.cache[key] = cacheEntry{result: result, fetchedAt: e.clock.Now(), err: fetchErr}
	e.cacheMu.Unlock()

	return result, fetchErr
}

// fetchAndExpand orchestrates the per-role pipeline: list inline
// policy names + fetch each → list attached managed → fetch each
// document. Per-step errors set PolicyFetchPartial without dropping
// the whole result.
func (e *Engine) fetchAndExpand(ctx context.Context, roleArn string) (RolePermissionsResult, error) {
	result := RolePermissionsResult{
		RoleArn:        roleArn,
		FetchedAt:      e.clock.Now(),
		CatalogVersion: e.catalog.Version,
	}
	var firstErr error
	markPartial := func(err error) {
		result.PolicyFetchPartial = true
		if firstErr == nil {
			firstErr = err
		}
	}

	// ── Inline policies ────────────────────────────────────────
	inlineNames, err := e.client.ListRolePolicies(ctx, roleArn)
	if err != nil {
		markPartial(fmt.Errorf("ListRolePolicies %s: %w", roleArn, err))
	} else {
		for _, name := range inlineNames {
			doc, err := e.client.GetRolePolicy(ctx, roleArn, name)
			if err != nil {
				markPartial(fmt.Errorf("GetRolePolicy %s/%s: %w", roleArn, name, err))
				continue
			}
			parsed, err := ParsePolicyDocument(doc)
			if err != nil {
				markPartial(fmt.Errorf("parse inline policy %s/%s: %w", roleArn, name, err))
				continue
			}
			e.expandInto(&result, parsed, "", name, PolicySourceInline, roleArn)
		}
	}

	// ── Attached managed policies ──────────────────────────────
	attached, err := e.client.ListAttachedRolePolicies(ctx, roleArn)
	if err != nil {
		markPartial(fmt.Errorf("ListAttachedRolePolicies %s: %w", roleArn, err))
	} else {
		for _, ap := range attached {
			doc, err := e.client.GetPolicyDocument(ctx, ap.PolicyArn)
			if err != nil {
				markPartial(fmt.Errorf("GetPolicyDocument %s: %w", ap.PolicyArn, err))
				continue
			}
			parsed, err := ParsePolicyDocument(doc)
			if err != nil {
				markPartial(fmt.Errorf("parse managed policy %s: %w", ap.PolicyArn, err))
				continue
			}
			e.expandInto(&result, parsed, ap.PolicyArn, ap.PolicyName, PolicySourceManaged, roleArn)
		}
	}

	// ── Sort + truncate ────────────────────────────────────────
	sortPermissions(result.Permissions)
	result.TotalCount = len(result.Permissions)
	if result.TotalCount > e.cfg.MaxRowsCap {
		result.Truncated = true
		result.Permissions = result.Permissions[:e.cfg.MaxRowsCap]
	}

	return result, firstErr
}

// expandInto iterates a parsed policy's statements, projecting each
// via Statement.Expand and stamping ConsoleURL onto any
// RawStatements produced.
func (e *Engine) expandInto(result *RolePermissionsResult, doc PolicyDocument, policyArn, policyName string, source PolicySource, roleArn string) {
	for i, stmt := range doc.Statement {
		meta := StatementMeta{
			PolicyArn:    policyArn,
			PolicyName:   policyName,
			PolicySource: source,
			StatementIdx: i,
			Catalog:      e.catalog,
		}
		perms, raw := stmt.Expand(meta)
		result.Permissions = append(result.Permissions, perms...)
		if raw != nil {
			raw.ConsoleURL = buildConsoleURL(policyArn, source, roleArn)
			result.RawStatements = append(result.RawStatements, *raw)
		}
	}
}

// sortPermissions imposes a deterministic order so SPA renders and
// golden tests are stable. Sort key: (Service, Action, Resource,
// Effect) — service grouping is the SPA's primary view, then
// action / resource for within-group readability.
func sortPermissions(perms []Permission) {
	sort.SliceStable(perms, func(i, j int) bool {
		if perms[i].Service != perms[j].Service {
			return perms[i].Service < perms[j].Service
		}
		if perms[i].Action != perms[j].Action {
			return perms[i].Action < perms[j].Action
		}
		if perms[i].Resource != perms[j].Resource {
			return perms[i].Resource < perms[j].Resource
		}
		return perms[i].Effect < perms[j].Effect
	})
}

// ── ReverseLookup ────────────────────────────────────────────────

// ErrInvalidQuery wraps any ReverseLookupQuery validation failure
// so cmd/periscope's handler can return 400 vs 5xx cleanly.
var ErrInvalidQuery = errors.New("iam: invalid reverse-lookup query")

// ReverseLookup answers "which (SA, role, permission) tuples match
// the given (action, resource)?" — the headline #188 SPA flow.
//
// For each SA→role binding in the cluster's index, the engine
// fetches the role's permissions (via the cached RolePermissions
// path) and runs matchAction + matchResource against each row.
// Permissions that match contribute one ReverseLookupMatch.
//
// PodRefs / PodCount stay empty here — the engine doesn't know
// pods. cmd/periscope's handler enriches each match with pod refs
// from the K8s informer cache before returning to the SPA.
//
// Optional filters:
//   - Namespace: scope the SA→role iteration to one namespace
//   - Resource: empty matches any resource (action-only query)
func (e *Engine) ReverseLookup(ctx context.Context, q ReverseLookupQuery) ([]ReverseLookupMatch, error) {
	if strings.TrimSpace(q.Action) == "" {
		return nil, fmt.Errorf("%w: Action is required", ErrInvalidQuery)
	}

	bindings, err := e.saIndex.SARoleSnapshot(ctx, e.clusterName)
	if err != nil {
		return nil, fmt.Errorf("iam: SARoleSnapshot: %w", err)
	}
	if q.Namespace != "" {
		bindings = filterByNamespace(bindings, q.Namespace)
	}

	// Per-role result memo so two SAs binding the same role don't
	// trigger two AWS round-trips. The engine's own RolePermissions
	// cache also dedupes, but this avoids double-walking the
	// already-cached result for every binding.
	memo := map[string]RolePermissionsResult{}
	var matches []ReverseLookupMatch
	for _, b := range bindings {
		key := strings.ToLower(b.RoleArn)
		result, ok := memo[key]
		if !ok {
			r, ferr := e.RolePermissions(ctx, b.RoleArn)
			if ferr != nil {
				// Soft-fail per role: log + continue. Other roles
				// can still contribute matches. The SPA gets the
				// rows we did resolve.
				e.log.Warn("iam: reverse-lookup partial role fetch",
					"cluster", e.clusterName, "role", b.RoleArn, "err", ferr)
			}
			memo[key] = r
			result = r
		}
		for _, p := range result.Permissions {
			if !matchAction(p.Action, q.Action) {
				continue
			}
			if q.Resource != "" && !matchResource(p.Resource, q.Resource) {
				continue
			}
			matches = append(matches, ReverseLookupMatch{
				SAName:     b.SAName,
				Namespace:  b.Namespace,
				RoleArn:    b.RoleArn,
				Permission: p,
			})
		}
	}

	sortMatches(matches)
	return matches, nil
}

// filterByNamespace returns only bindings whose Namespace equals ns.
// Linear scan is fine — bindings per cluster are bounded (≤ low
// thousands in any realistic EKS cluster).
func filterByNamespace(bindings []SARoleBinding, ns string) []SARoleBinding {
	out := make([]SARoleBinding, 0, len(bindings))
	for _, b := range bindings {
		if b.Namespace == ns {
			out = append(out, b)
		}
	}
	return out
}

// sortMatches orders results: sensitive rows first (so the SPA
// renders the flagged hits at the top), then by (namespace, saName,
// action, resource) for deterministic within-group ordering.
func sortMatches(matches []ReverseLookupMatch) {
	sort.SliceStable(matches, func(i, j int) bool {
		ai, aj := matches[i], matches[j]
		if ai.Permission.Sensitive != aj.Permission.Sensitive {
			return ai.Permission.Sensitive // true first
		}
		if ai.Namespace != aj.Namespace {
			return ai.Namespace < aj.Namespace
		}
		if ai.SAName != aj.SAName {
			return ai.SAName < aj.SAName
		}
		if ai.Permission.Action != aj.Permission.Action {
			return ai.Permission.Action < aj.Permission.Action
		}
		return ai.Permission.Resource < aj.Permission.Resource
	})
}

// SortReverseLookupRows orders the row-per-pod result list:
// sensitive rows first, then by (namespace, saName, action,
// resource, podName) for deterministic within-group ordering.
// Mirrors sortMatches but adds the pod-name tie-breaker so two
// rows that flatten from the same (perm, SA) but different pods
// render adjacent.
//
// Exported so cmd/periscope's reverse-lookup handler can sort
// after the pod-cache flatten.
func SortReverseLookupRows(rows []ReverseLookupPodRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		ai, aj := rows[i], rows[j]
		if ai.Permission.Sensitive != aj.Permission.Sensitive {
			return ai.Permission.Sensitive // true first
		}
		if ai.Namespace != aj.Namespace {
			return ai.Namespace < aj.Namespace
		}
		if ai.SAName != aj.SAName {
			return ai.SAName < aj.SAName
		}
		if ai.Permission.Action != aj.Permission.Action {
			return ai.Permission.Action < aj.Permission.Action
		}
		if ai.Permission.Resource != aj.Permission.Resource {
			return ai.Permission.Resource < aj.Permission.Resource
		}
		return ai.Pod.Name < aj.Pod.Name
	})
}

// ── ConsoleURL construction ──────────────────────────────────────

// buildConsoleURL produces an AWS IAM Console deep link for a
// RawStatement, partition-aware. Empty string for unknown
// partitions or unparseable role ARNs — SPA renders the chip
// without a link in that case.
//
//   - Managed policy → policy-page deep link (only requires policyArn).
//   - Inline policy  → role-page deep link (requires roleArn).
//
// Per partition:
//
//   aws         → console.aws.amazon.com
//   aws-us-gov  → console.amazonaws-us-gov.com
//   aws-cn      → console.amazonaws.cn
func buildConsoleURL(policyArn string, source PolicySource, roleArn string) string {
	// Partition is identified by whichever ARN we have — for managed
	// policies that's the policy ARN; for inline it's the role ARN.
	var partition string
	if source == PolicySourceManaged && policyArn != "" {
		partition = partitionFromArn(policyArn)
	} else {
		partition = partitionFromArn(roleArn)
	}
	host := consoleHostFor(partition)
	if host == "" {
		return ""
	}

	if source == PolicySourceManaged && policyArn != "" {
		// IAM Console: /iam/home#/policies/<arn>$jsonEditor
		return fmt.Sprintf("https://%s/iam/home#/policies/%s$jsonEditor", host, policyArn)
	}

	roleName := iamRoleNameFromArn(roleArn)
	if roleName == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/iam/home#/roles/%s", host, roleName)
}

// partitionFromArn extracts the partition segment of an AWS ARN
// (arn:PARTITION:service:region:account:resource). Empty string
// for unparseable input.
func partitionFromArn(arn string) string {
	if !strings.HasPrefix(arn, "arn:") {
		return ""
	}
	rest := arn[len("arn:"):]
	if i := strings.Index(rest, ":"); i > 0 {
		return rest[:i]
	}
	return ""
}

// consoleHostFor returns the IAM Console hostname for a partition.
// Empty string for unknown partitions — caller falls back to
// chip-without-link rendering.
func consoleHostFor(partition string) string {
	switch partition {
	case "aws":
		return "console.aws.amazon.com"
	case "aws-us-gov":
		return "console.amazonaws-us-gov.com"
	case "aws-cn":
		return "console.amazonaws.cn"
	}
	return ""
}

// iamRoleNameFromArn extracts the role name from an IAM role ARN.
// Duplicated from internal/awseks/identity/client.go's
// roleNameFromArn to keep the iam package importable without a
// reverse dependency on identity.
func iamRoleNameFromArn(arn string) string {
	const prefix = ":role/"
	i := strings.Index(arn, prefix)
	if i < 0 {
		return ""
	}
	tail := arn[i+len(prefix):]
	if slash := strings.LastIndex(tail, "/"); slash >= 0 {
		tail = tail[slash+1:]
	}
	return tail
}
