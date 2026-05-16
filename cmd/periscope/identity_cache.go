package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"k8s.io/client-go/kubernetes"

	"github.com/gnana997/periscope/internal/awseks/identity"
	"github.com/gnana997/periscope/internal/clusters"
)

// identityK8sFactory builds a K8s clientset for the cluster using the
// server's shared identity (non-impersonated). Long-lived informers
// must not depend on per-request user creds; this factory mirrors the
// pattern used by the CVE manager wiring in main.go.
type identityK8sFactory func(ctx context.Context, c clusters.Cluster) (kubernetes.Interface, error)

// identityCache is a per-cluster registry of long-lived
// *identity.Manager instances. The manager owns a ServiceAccount
// informer + an in-memory SA↔Role index + a role-existence cache;
// constructing a fresh one per request would waste an AWS round-trip
// and re-list every SA on each tab open.
//
// One Manager per cluster. First request for a cluster builds it
// lazily; subsequent requests reuse it. Shutdown() cancels every
// informer in one shot during server shutdown.
type identityCache struct {
	mu       sync.Mutex
	managers map[string]*identityCacheEntry

	awsCfg     aws.Config
	k8sFactory identityK8sFactory
	parentCtx  context.Context
	cfg        identity.Config
	log        *slog.Logger

	// podInformer toggles whether the Pod informer attaches alongside
	// the SA informer. Set false when the AWS Access surface (#188)
	// is disabled via PERISCOPE_AWS_ACCESS_ENABLED=false so the Pod
	// cache's memory and watch don't run for operators who've opted
	// out of the workload-permissions / reverse-lookup features.
	podInformer bool
}

type identityCacheEntry struct {
	manager *identity.Manager
	cancel  context.CancelFunc
}

// newIdentityCache constructs the cache. parentCtx scopes the
// long-lived informers — cancelling it (typically server shutdown)
// stops every informer.
func newIdentityCache(parentCtx context.Context, awsCfg aws.Config, k8sFactory identityK8sFactory, cfg identity.Config, log *slog.Logger) *identityCache {
	if log == nil {
		log = slog.Default()
	}
	return &identityCache{
		managers:    map[string]*identityCacheEntry{},
		awsCfg:      awsCfg,
		k8sFactory:  k8sFactory,
		parentCtx:   parentCtx,
		cfg:         cfg,
		log:         log,
		podInformer: true,
	}
}

// SetPodInformerEnabled flips the per-cluster Pod informer on or
// off for managers created after this call. Existing managers keep
// their original setting; the cache is a one-shot at construction.
// Call this once at startup before any handler triggers For().
func (c *identityCache) SetPodInformerEnabled(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.podInformer = enabled
}

// For returns the per-cluster Manager, lazily constructing it on
// the first request. The construction path:
//
//  1. Build an identity.Client backed by EKS + IAM SDK clients using
//     the shared aws.Config with the cluster's Region overlaid.
//  2. Build a K8s clientset via the injected factory (server's
//     non-impersonated identity).
//  3. Start the SA informer rooted at parentCtx so it survives the
//     request that triggered construction.
//
// Errors building the K8s clientset are NOT cached — the next request
// retries. A failed informer start, however, leaks the (already-built)
// AWS client; we accept that since failure here implies the cluster
// is unreachable and the next attempt will rebuild from scratch.
func (c *identityCache) For(ctx context.Context, cl clusters.Cluster) (*identity.Manager, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.managers[cl.Name]; ok {
		return e.manager, nil
	}

	// Use the same factory the request-path handlers use so tests
	// can swap it once and affect both call sites.
	client := newIdentityClient(c.awsCfg, cl)
	mgr := identity.NewManager(cl.Name, client, client, nil, c.cfg, c.log.With("cluster", cl.Name))

	cs, err := c.k8sFactory(ctx, cl)
	if err != nil {
		return nil, fmt.Errorf("identity: build k8s clientset for %s: %w", cl.Name, err)
	}
	cancel, err := identity.StartSAInformer(c.parentCtx, cs, mgr, identity.WithPodInformer(c.podInformer))
	if err != nil {
		return nil, fmt.Errorf("identity: start SA informer for %s: %w", cl.Name, err)
	}
	c.managers[cl.Name] = &identityCacheEntry{manager: mgr, cancel: cancel}
	return mgr, nil
}

// Shutdown cancels every per-cluster informer. Safe to call multiple
// times; subsequent calls are no-ops.
func (c *identityCache) Shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.managers {
		e.cancel()
	}
	c.managers = map[string]*identityCacheEntry{}
}

// newIdentityClient builds a per-request identity.Client for the
// handlers that don't go through the Manager (access-entries,
// aws-auth-diff, pod-identity). Mirrors defaultNewEKSAddonsClient.
//
// var so handler tests can swap in a stub.
var newIdentityClient = func(awsCfg aws.Config, cl clusters.Cluster) *identity.Client {
	cfg := awsCfg.Copy()
	cfg.Region = cl.Region
	return identity.New(cfg)
}
