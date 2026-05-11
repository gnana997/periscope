package cve

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
	"golang.org/x/sync/singleflight"
	"k8s.io/client-go/kubernetes"

	"github.com/gnana997/periscope/internal/awsec2"
	"github.com/gnana997/periscope/internal/clusters"
)

// InspectorAPI is the subset of awsinspector.Client the manager needs.
// Defined here so tests can substitute a stub without depending on the
// SDK. The map-keyed return shape is the load-bearing contract: each
// requested ID must appear in the result so the caller can iterate
// the input list and trust the lookup (see ensureKeys in
// awsinspector).
type InspectorAPI interface {
	IsEnabled(ctx context.Context) (bool, error)
	ListFindingsByInstance(ctx context.Context, instanceIDs []string) (map[string][]Finding, error)
	ListFindingsByImageDigest(ctx context.Context, digests []string) (map[string][]Finding, error)
}

// EC2API is the subset of awsec2.Client the manager needs.
type EC2API interface {
	DescribeInstances(ctx context.Context, ids []string) ([]awsec2.InstanceMeta, error)
}

// ClientFactory builds a K8s clientset for a cluster on demand.
// Threaded through Manager so the watch hook + hydrate's pod-list can
// reuse the existing internal/k8s factory (Pod Identity / IRSA / agent
// tunnel) without re-implementing it here.
type ClientFactory func(ctx context.Context, cluster clusters.Cluster) (kubernetes.Interface, error)

// Config is the operator-controlled tuning. Loaded from
// PERISCOPE_INSPECTOR_* env vars in cmd/periscope/main.go; defaults
// match the issue's spec.
type Config struct {
	// RefreshInterval is the per-entry TTL: entries older than this
	// are re-fetched by the background TTL loop.
	RefreshInterval time.Duration

	// EvictAfter is the cooldown before a zero-ref entry is dropped
	// from the cache. Keeps recently-deleted pods' digests warm so a
	// scale-up doesn't have to wait for a fresh fetch.
	EvictAfter time.Duration

	// HydrateBatchSize is the Inspector batch size used during the
	// cold-path hydrate. Capped by awsinspector.BatchSize regardless
	// of what the operator sets; the field exists so a future change
	// can lower the ceiling without rebuilding.
	HydrateBatchSize int

	// TickInterval governs how often the TTL/eviction loops wake up.
	// Exposed for tests; production code leaves it zero and the
	// manager defaults to 1 minute (TTL) / 5 minutes (eviction).
	TTLScanInterval      time.Duration
	EvictionScanInterval time.Duration

	// MaxConcurrentDeltaFetches caps the goroutine count for
	// watch-driven async refreshes. A rolling deploy of a 200-pod
	// workload could otherwise spawn hundreds of goroutines, each
	// holding an Inspector socket. Default 8.
	MaxConcurrentDeltaFetches int
}

// DefaultConfig returns the production defaults. Mirrors the issue's
// spec; main.go fills any unset field with these values.
func DefaultConfig() Config {
	return Config{
		RefreshInterval:           6 * time.Hour,
		EvictAfter:                24 * time.Hour,
		HydrateBatchSize:          50,
		TTLScanInterval:           1 * time.Minute,
		EvictionScanInterval:      5 * time.Minute,
		MaxConcurrentDeltaFetches: 8,
	}
}

// clusterState is the per-cluster runtime state held by Manager.
// once gates the cold hydrate; podInformerCancel stops the long-lived
// pod informer when the manager is shut down.
type clusterState struct {
	store             *Store
	once              sync.Once
	hydrateErr        error
	podInformerCancel context.CancelFunc
	clusterRef        clusters.Cluster
}

// Manager owns one Store per cluster plus the background TTL +
// eviction loops. Hydration is lazy (per cluster on first access);
// the loops scan every cluster's store on their own schedule.
type Manager struct {
	inspector InspectorAPI
	ec2       EC2API
	clientFor ClientFactory
	clock     clockwork.Clock
	cfg       Config
	log       *slog.Logger

	mu       sync.Mutex
	clusters map[string]*clusterState

	// sf collapses concurrent fetches of the same digest / instance
	// batch into a single Inspector call. Inspector data is account-
	// scoped, not cluster-scoped, so the keys deliberately omit the
	// cluster name — a digest seen in cluster A's TTL scan and
	// cluster B's manual refresh resolves to the same Inspector call.
	sf singleflight.Group

	// deltaSem caps concurrent watch-driven async refreshes.
	deltaSem chan struct{}

	stopOnce sync.Once
	cancel   context.CancelFunc
	doneCh   chan struct{}
}

// NewManager constructs a Manager. The returned manager is dormant
// until Start is called; tests that exercise only the store + hydrate
// can call EnsureHydrated directly without Start.
func NewManager(inspector InspectorAPI, ec2 EC2API, clientFor ClientFactory, clock clockwork.Clock, cfg Config, log *slog.Logger) *Manager {
	if clock == nil {
		clock = clockwork.NewRealClock()
	}
	if log == nil {
		log = slog.Default()
	}
	if cfg.RefreshInterval == 0 {
		cfg.RefreshInterval = DefaultConfig().RefreshInterval
	}
	if cfg.EvictAfter == 0 {
		cfg.EvictAfter = DefaultConfig().EvictAfter
	}
	if cfg.HydrateBatchSize == 0 {
		cfg.HydrateBatchSize = DefaultConfig().HydrateBatchSize
	}
	if cfg.TTLScanInterval == 0 {
		cfg.TTLScanInterval = DefaultConfig().TTLScanInterval
	}
	if cfg.EvictionScanInterval == 0 {
		cfg.EvictionScanInterval = DefaultConfig().EvictionScanInterval
	}
	if cfg.MaxConcurrentDeltaFetches <= 0 {
		cfg.MaxConcurrentDeltaFetches = DefaultConfig().MaxConcurrentDeltaFetches
	}
	return &Manager{
		inspector: inspector,
		ec2:       ec2,
		clientFor: clientFor,
		clock:     clock,
		cfg:       cfg,
		log:       log,
		clusters:  map[string]*clusterState{},
		deltaSem:  make(chan struct{}, cfg.MaxConcurrentDeltaFetches),
		doneCh:    make(chan struct{}),
	}
}

// Start spins up the TTL refresh + eviction loops. parent is the
// server context; the loops exit cleanly when parent is cancelled.
// Idempotent: a second call is a no-op.
func (m *Manager) Start(parent context.Context) {
	if m.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	go m.runLoops(ctx)
	m.log.Info("cve manager started",
		"refresh_interval", m.cfg.RefreshInterval,
		"evict_after", m.cfg.EvictAfter,
	)
}

// Stop signals the loops to exit and stops every long-lived pod
// informer. Returns when all goroutines have exited or after a 5s
// timeout. Idempotent and safe to call on a never-Start'd manager
// (no-op for the loops, still cancels per-cluster pod informers).
func (m *Manager) Stop() {
	m.stopOnce.Do(func() {
		started := m.cancel != nil
		if started {
			m.cancel()
		}
		// Stop per-cluster pod informers.
		m.mu.Lock()
		for _, st := range m.clusters {
			if st.podInformerCancel != nil {
				st.podInformerCancel()
			}
		}
		m.mu.Unlock()

		if !started {
			return
		}
		select {
		case <-m.doneCh:
		case <-time.After(5 * time.Second):
			m.log.Warn("cve manager stop timed out")
		}
	})
}

// EnsureHydrated triggers the cold-path hydrate for cluster (once)
// and blocks until it completes (or the context is cancelled). Safe
// to call concurrently from many request handlers: only the first
// caller per cluster runs the hydrate, the rest WaitHydrated on the
// same store.
//
// Returns the Store for the cluster (never nil) and any hydrate error.
// Even on error the Store is returned so the API layer can render a
// graceful empty state.
func (m *Manager) EnsureHydrated(ctx context.Context, cluster clusters.Cluster) (*Store, error) {
	st := m.stateFor(cluster)
	st.once.Do(func() {
		st.hydrateErr = m.hydrate(ctx, cluster, st)
	})
	if err := st.store.WaitHydrated(ctx); err != nil {
		return st.store, err
	}
	return st.store, st.hydrateErr
}

// Get returns the Store for cluster if it has been hydrated; nil
// otherwise. The API layer uses this when it wants to read without
// triggering hydration (e.g. a periodic "is the cache warm yet?"
// poll from the SPA).
func (m *Manager) Get(cluster string) *Store {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.clusters[cluster]; ok {
		return st.store
	}
	return nil
}

// Refresh re-fetches the listed digests + instances synchronously,
// bypassing the TTL. Backs the "refresh now" UI button.
//
// Empty input is a no-op; returning the first error encountered does
// not abort the other fetches. Single-flight collapsing means a
// concurrent watch-hook fetch of the same digest will share the
// in-flight call rather than firing a second Inspector request.
func (m *Manager) Refresh(ctx context.Context, cluster clusters.Cluster, digests []string, instanceIDs []string) error {
	store, err := m.EnsureHydrated(ctx, cluster)
	if err != nil {
		return err
	}
	if store.Disabled() {
		return nil
	}
	var firstErr error
	if len(digests) > 0 {
		if err := m.fetchDigests(ctx, m.stateFor(cluster), digests); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if len(instanceIDs) > 0 {
		if err := m.fetchInstances(ctx, m.stateFor(cluster), instanceIDs); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// stateFor returns the clusterState for cluster, creating it on
// first use. Holds m.mu for the lookup so concurrent first-readers
// share a single sync.Once.
func (m *Manager) stateFor(cluster clusters.Cluster) *clusterState {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.clusters[cluster.Name]
	if !ok {
		st = &clusterState{
			store:      NewStore(),
			clusterRef: cluster,
		}
		m.clusters[cluster.Name] = st
	}
	return st
}

// runLoops is the background tick loop. Single goroutine handles
// both TTL refresh and eviction so we don't fight over m.mu.
func (m *Manager) runLoops(ctx context.Context) {
	defer close(m.doneCh)

	ttlTicker := m.clock.NewTicker(m.cfg.TTLScanInterval)
	defer ttlTicker.Stop()
	evictTicker := m.clock.NewTicker(m.cfg.EvictionScanInterval)
	defer evictTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ttlTicker.Chan():
			m.scanTTL(ctx)
		case <-evictTicker.Chan():
			m.scanEvict()
		}
	}
}

// scanTTL re-fetches any entry whose LastFetched is older than
// RefreshInterval. Stale digests and instances are batched into one
// Inspector call per cluster (chunked internally by awsinspector to
// BatchSize); this keeps a 500-stale-digest cluster at ~10 Inspector
// calls instead of 500.
func (m *Manager) scanTTL(ctx context.Context) {
	m.mu.Lock()
	snapshot := make([]*clusterState, 0, len(m.clusters))
	for _, st := range m.clusters {
		if st.store.Disabled() || !st.store.Hydrated() {
			continue
		}
		snapshot = append(snapshot, st)
	}
	m.mu.Unlock()

	for _, st := range snapshot {
		now := m.clock.Now()
		digests, instances := st.store.IterStale(now, m.cfg.RefreshInterval)
		if len(digests) > 0 {
			if err := m.fetchDigests(ctx, st, digests); err != nil {
				m.log.Debug("cve ttl digest refresh", "cluster", st.clusterRef.Name, "n", len(digests), "err", err)
			}
		}
		if len(instances) > 0 {
			if err := m.fetchInstances(ctx, st, instances); err != nil {
				m.log.Debug("cve ttl instance refresh", "cluster", st.clusterRef.Name, "n", len(instances), "err", err)
			}
		}
	}
}

// scanEvict drops zero-ref entries older than EvictAfter from every
// cluster's store.
func (m *Manager) scanEvict() {
	m.mu.Lock()
	snapshot := make([]*clusterState, 0, len(m.clusters))
	for _, st := range m.clusters {
		if st.store.Disabled() || !st.store.Hydrated() {
			continue
		}
		snapshot = append(snapshot, st)
	}
	m.mu.Unlock()

	now := m.clock.Now()
	for _, st := range snapshot {
		digests, instances := st.store.IterEvictable(now, m.cfg.EvictAfter)
		for _, d := range digests {
			st.store.EvictDigest(d)
		}
		for _, id := range instances {
			st.store.EvictInstance(id)
		}
		if len(digests) > 0 || len(instances) > 0 {
			m.log.Debug("cve evicted",
				"cluster", st.clusterRef.Name,
				"digests", len(digests),
				"instances", len(instances))
		}
	}
}

// fetchDigests batches a digest set into one Inspector call (further
// chunked by awsinspector.BatchSize). Single-flight collapsing keys
// off the sorted-and-joined ID list so two concurrent callers asking
// for the same set share the result. Inspector data is account-
// scoped, so the cluster name is intentionally NOT in the key.
func (m *Manager) fetchDigests(ctx context.Context, st *clusterState, digests []string) error {
	if len(digests) == 0 || st.store.Disabled() {
		return nil
	}
	keys := dedupSorted(digests)
	if len(keys) == 0 {
		return nil
	}
	sfKey := "d:" + joinKey(keys)
	_, err, _ := m.sf.Do(sfKey, func() (any, error) {
		grouped, err := m.inspector.ListFindingsByImageDigest(ctx, keys)
		if err != nil {
			return nil, err
		}
		now := m.clock.Now()
		for _, d := range keys {
			st.store.UpsertDigest(d, grouped[d], now)
		}
		return nil, nil
	})
	return err
}

// fetchInstances batches an instance set into one Inspector call.
// Owner classification is preserved across the upsert (the existing
// entry's OwnerKind/Name carries through); only the cold-path
// hydrate reassigns ownership.
func (m *Manager) fetchInstances(ctx context.Context, st *clusterState, ids []string) error {
	if len(ids) == 0 || st.store.Disabled() {
		return nil
	}
	keys := dedupSorted(ids)
	if len(keys) == 0 {
		return nil
	}
	sfKey := "i:" + joinKey(keys)
	_, err, _ := m.sf.Do(sfKey, func() (any, error) {
		grouped, err := m.inspector.ListFindingsByInstance(ctx, keys)
		if err != nil {
			return nil, err
		}
		now := m.clock.Now()
		for _, id := range keys {
			existing := st.store.GetInstance(id)
			var kind OwnerKind
			var name string
			if existing != nil {
				kind = existing.OwnerKind
				name = existing.OwnerName
			} else {
				kind = OwnerUnmanaged
			}
			st.store.UpsertInstance(id, grouped[id], kind, name, now)
		}
		return nil, nil
	})
	return err
}

// runDelta executes fn under the delta-fetch semaphore so a
// rolling-deploy storm doesn't spawn unbounded goroutines, each with
// an Inspector socket. Drops the work (logs at debug) if ctx is
// cancelled while waiting for a slot.
func (m *Manager) runDelta(ctx context.Context, fn func()) {
	select {
	case m.deltaSem <- struct{}{}:
		defer func() { <-m.deltaSem }()
		fn()
	case <-ctx.Done():
		return
	}
}

// dedupSorted returns the input deduped + sorted. Used for stable
// singleflight keys.
func dedupSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// joinKey produces a stable singleflight key from a deduped+sorted ID
// list. The leading length+`:` byte makes "ab","c" and "a","bc"
// collisions impossible.
func joinKey(sorted []string) string {
	return fmt.Sprintf("%d:", len(sorted)) + sortedJoin(sorted)
}

// sortedJoin is strings.Join with a separator that cannot appear in
// either ECR digests (sha256:hex) or EC2 instance IDs (i-hex).
func sortedJoin(sorted []string) string {
	if len(sorted) == 0 {
		return ""
	}
	// '\x1f' is the ASCII unit separator; not present in any
	// Inspector resource ID we accept.
	out := sorted[0]
	for _, s := range sorted[1:] {
		out += "\x1f" + s
	}
	return out
}
