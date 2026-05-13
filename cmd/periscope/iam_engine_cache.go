package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/gnana997/periscope/internal/awseks/iam"
	"github.com/gnana997/periscope/internal/awseks/identity"
	"github.com/gnana997/periscope/internal/clusters"
)

// iamEngineCache is the per-cluster registry of *iam.Engine. Engines
// hold a per-role policy cache + the SA→Role index seam, so one
// instance per cluster mirrors identityCache's lifecycle.
//
// Engines are lazy: instantiated on first request per cluster.
// Subsequent requests for the same cluster reuse the cached
// instance, sharing its TTL cache across handler calls.
//
// Depends on identityCache because the engine's SARoleIndexer is
// satisfied by an adapter wrapping identityCache's per-cluster
// identity.Manager — that manager already owns the SA informer +
// SA↔Role index (#178), so we don't duplicate the work here.
type iamEngineCache struct {
	mu      sync.Mutex
	engines map[string]*iam.Engine

	awsCfg     aws.Config
	identityC  *identityCache
	cfg        iam.Config
	log        *slog.Logger
}

// newIAMEngineCache constructs the cache. awsCfg is the shared aws.Config
// (per-cluster Region is overlaid in newIdentityClient). identityC
// supplies the per-cluster identity.Manager that the SARoleIndexer
// adapter wraps.
func newIAMEngineCache(awsCfg aws.Config, identityC *identityCache, cfg iam.Config, log *slog.Logger) *iamEngineCache {
	if log == nil {
		log = slog.Default()
	}
	return &iamEngineCache{
		engines:   map[string]*iam.Engine{},
		awsCfg:    awsCfg,
		identityC: identityC,
		cfg:       cfg,
		log:       log.With("component", "iam-engine"),
	}
}

// For returns the cluster's Engine, instantiating on first call.
//
// Construction wires three seams:
//   1. PolicyFetcher: *identity.Client (built via newIdentityClient
//      with the cluster's Region overlaid). The compile-time assertion
//      in internal/awseks/identity/interface_assert.go enforces that
//      *Client satisfies iam.PolicyFetcher.
//   2. SARoleIndexer: identityToIAMSAAdapter wrapping identityCache's
//      per-cluster Manager. The adapter flattens identity's
//      SARoleIndexEntry[]Bindings into iam's flat SARoleBinding list.
//   3. Catalog: the engine uses the process-wide default catalog
//      (sensitive.yaml).
//
// Errors from identityCache.For propagate — typically a misconfigured
// K8s clientset or a non-EKS cluster.
func (c *iamEngineCache) For(ctx context.Context, cl clusters.Cluster) (*iam.Engine, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.engines[cl.Name]; ok {
		return e, nil
	}

	// PolicyFetcher: build identity.Client with the cluster's Region.
	// newIdentityClient is the same var the identity handlers use,
	// so test stubs swap one place and affect both surfaces.
	client := newIdentityClient(c.awsCfg, cl)

	// SARoleIndexer: bind the existing per-cluster identity.Manager
	// behind a flat-binding adapter.
	mgr, err := c.identityC.For(ctx, cl)
	if err != nil {
		return nil, fmt.Errorf("iam engine: get identity manager for %s: %w", cl.Name, err)
	}
	indexer := &identityToIAMSAAdapter{mgr: mgr}

	engine := iam.NewEngine(cl.Name, client, indexer, c.cfg, c.log.With("cluster", cl.Name))
	c.engines[cl.Name] = engine
	return engine, nil
}

// Shutdown is a no-op today — the engine doesn't start any
// goroutines (no informer of its own; relies on identityCache for
// that). Reserved for future use (e.g. flushing audit aggregators)
// so the call site in main.go stays uniform with identityCache.Shutdown.
func (c *iamEngineCache) Shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.engines = map[string]*iam.Engine{}
}

// ── SARoleIndexer adapter ────────────────────────────────────────

// identityToIAMSAAdapter bridges the per-cluster identity.Manager
// (which owns the unified SA↔Role index from #178) to the engine's
// flat SARoleBinding shape.
//
// Each identity.SARoleIndexEntry can carry multiple Bindings (the
// dual-source case: IRSA annotation + Pod Identity association on
// the same SA). The adapter flattens to one iam.SARoleBinding per
// (SA, role) tuple — both rows appear in reverse-lookup results so
// operators see "this SA can do X via IRSA" AND "via Pod Identity"
// as separate matches if the two role bindings have different
// permissions.
//
// The cluster parameter on SARoleSnapshot is ignored — the wrapped
// manager is already per-cluster. Kept for interface conformance.
type identityToIAMSAAdapter struct {
	mgr *identity.Manager
}

func (a *identityToIAMSAAdapter) SARoleSnapshot(ctx context.Context, _ string) ([]iam.SARoleBinding, error) {
	entries, err := a.mgr.Ensure(ctx)
	if err != nil {
		return nil, fmt.Errorf("identity manager Ensure: %w", err)
	}
	var out []iam.SARoleBinding
	for _, e := range entries {
		for _, b := range e.Bindings {
			if b.RoleArn == "" {
				continue
			}
			out = append(out, iam.SARoleBinding{
				SAName:    e.SAName,
				Namespace: e.Namespace,
				RoleArn:   b.RoleArn,
			})
		}
	}
	return out, nil
}
