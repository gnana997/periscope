package identity

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
)

// IrsaAnnotation is the standard EKS annotation that wires a
// ServiceAccount to an IAM role for the legacy IRSA path. The
// manager reads this from each SA in the cluster.
const IrsaAnnotation = "eks.amazonaws.com/role-arn"

// IRSALister returns the map of (namespace, saName) →
// eks.amazonaws.com/role-arn annotation value for every ServiceAccount
// currently in the cluster's informer cache. Implementations are
// expected to be cheap (in-memory cache walk).
//
// Defined as a func type so watch_hook can wire its informer's lister
// without exporting a wrapper interface, and tests can pass an inline
// closure with synthetic data.
type IRSALister func() (map[SAKey]string, error)

// PodIdentityLister returns the cluster's Pod Identity associations.
// Implemented by *Client; stubbable for manager tests that don't want
// the full EKS API surface.
type PodIdentityLister interface {
	ListPodIdentityAssociations(ctx context.Context, clusterName string) ([]PodIdentityAssoc, error)
}

// Default TTLs for the index and the role-existence cache. Public so
// tests and the cmd/periscope construction site can override them
// without copy-pasting the magic numbers.
const (
	DefaultIndexTTL = 5 * time.Minute
	DefaultRoleTTL  = 15 * time.Minute
)

// Config bundles the per-cluster manager parameters. Zero values
// fall back to the Defaults above so cmd/periscope can supply just
// the fields it wants to override.
type Config struct {
	IndexTTL time.Duration
	RoleTTL  time.Duration
}

func (c Config) withDefaults() Config {
	if c.IndexTTL == 0 {
		c.IndexTTL = DefaultIndexTTL
	}
	if c.RoleTTL == 0 {
		c.RoleTTL = DefaultRoleTTL
	}
	return c
}

// Manager owns one cluster's SA↔Role index. It fans out IRSA
// (from the SA informer cache via IRSALister) + Pod Identity (from
// the EKS API) + iam:GetRole (with its own 15-min TTL cache) to
// build a unified set of SARoleIndexEntry rows on demand.
//
// Concurrency model: many readers may call Ensure simultaneously;
// the rebuild path is serialized by ensureMu with a double-check
// to avoid re-doing work just-completed by a peer. The role-exists
// cache has its own mutex so the rebuild can populate it in parallel
// without blocking unrelated reads.
type Manager struct {
	clusterName  string
	saLister     IRSALister
	podIdentity  PodIdentityLister
	roleResolver IAMRoleResolver
	store        *Store
	clock        clockwork.Clock
	cfg          Config
	log          *slog.Logger

	ensureMu sync.Mutex

	roleMu     sync.Mutex
	roleCache  map[string]roleCacheEntry // keyed by normalized role ARN
}

type roleCacheEntry struct {
	exists    bool
	fetchedAt time.Time
}

// NewManager constructs a per-cluster manager. The SA informer lister
// is set later (typically via SetIRSALister called from watch_hook
// once the informer's cache is synced); until then Ensure soft-fails
// with an empty index rather than panic.
func NewManager(clusterName string, podIdentity PodIdentityLister, roleResolver IAMRoleResolver, clock clockwork.Clock, cfg Config, log *slog.Logger) *Manager {
	if clock == nil {
		clock = clockwork.NewRealClock()
	}
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		clusterName:  clusterName,
		podIdentity:  podIdentity,
		roleResolver: roleResolver,
		store:        NewStore(),
		clock:        clock,
		cfg:          cfg.withDefaults(),
		log:          log,
		roleCache:    map[string]roleCacheEntry{},
	}
}

// SetIRSALister wires the informer-backed SA lister. Called once by
// watch_hook after the informer's cache is synced.
func (m *Manager) SetIRSALister(l IRSALister) {
	m.ensureMu.Lock()
	defer m.ensureMu.Unlock()
	m.saLister = l
}

// Store exposes the underlying store so the watch hook can call
// Invalidate from event handlers without going through Manager.
func (m *Manager) Store() *Store {
	return m.store
}

// Ensure returns the current SA↔Role index for this cluster. If the
// current snapshot is younger than the configured TTL, it is served
// directly. Otherwise the index is rebuilt by re-listing SAs, Pod
// Identity associations, and probing iam:GetRole for any role ARN
// whose existence isn't cached.
//
// On rebuild error, the previous snapshot (if any) is returned along
// with the error so the API layer can render a stale-but-useful page
// with a warning banner rather than blanking the UI.
func (m *Manager) Ensure(ctx context.Context) ([]SARoleIndexEntry, error) {
	if m.store.Age(m.clock.Now()) < m.cfg.IndexTTL {
		return m.store.Snapshot(), nil
	}

	m.ensureMu.Lock()
	defer m.ensureMu.Unlock()
	// Double-check after acquiring the lock — a peer may have just rebuilt.
	if m.store.Age(m.clock.Now()) < m.cfg.IndexTTL {
		return m.store.Snapshot(), nil
	}

	entries, err := m.rebuild(ctx)
	if err != nil {
		// Return whatever we have plus the error so callers can
		// surface a banner rather than wipe the page.
		return m.store.Snapshot(), err
	}
	m.store.Replace(entries, m.clock.Now())
	return m.store.Snapshot(), nil
}

func (m *Manager) rebuild(ctx context.Context) ([]SARoleIndexEntry, error) {
	if m.saLister == nil {
		// Informer hasn't synced yet — handler will translate this
		// into a 503 with Retry-After.
		return nil, ErrIRSAListerNotReady
	}

	irsa, err := m.saLister()
	if err != nil {
		return nil, fmt.Errorf("list IRSA-annotated SAs: %w", err)
	}

	podIdentity, err := m.podIdentity.ListPodIdentityAssociations(ctx, m.clusterName)
	if err != nil {
		return nil, fmt.Errorf("list pod identity associations: %w", err)
	}

	// Collect every distinct role ARN we'll need to verify.
	wantRoles := map[string]struct{}{}
	for _, role := range irsa {
		if role == "" {
			continue
		}
		wantRoles[NormalizePrincipalArn(role)] = struct{}{}
	}
	for _, a := range podIdentity {
		if a.RoleArn == "" {
			continue
		}
		wantRoles[NormalizePrincipalArn(a.RoleArn)] = struct{}{}
	}

	roleExists, err := m.resolveRoleExistence(ctx, wantRoles, irsa, podIdentity)
	if err != nil {
		// Soft-failure: log and continue — render all roles as
		// roleExists:false rather than dropping the page. The user
		// still sees the bindings; the warning chip points them at
		// IAM permissions to fix.
		m.log.Warn("cve identity: role-existence probe partial-failed", "cluster", m.clusterName, "err", err)
	}

	return UnifySARoles(m.clusterName, irsa, podIdentity, roleExists), nil
}

// resolveRoleExistence consults the per-role cache for each distinct
// ARN; for cache misses (or stale entries past RoleTTL), it calls
// iam:GetRole and updates the cache. Returns the resolved map keyed
// by normalized ARN. The irsa + podIdentity parameters are unused
// today but kept for a future optimization that probes only roles
// reachable from currently-bound SAs (today we already do that —
// the wantRoles set is built from those bindings).
func (m *Manager) resolveRoleExistence(ctx context.Context, want map[string]struct{}, _ map[SAKey]string, _ []PodIdentityAssoc) (map[string]bool, error) {
	now := m.clock.Now()
	out := map[string]bool{}

	// Pass 1: serve from cache where possible.
	missing := make([]string, 0, len(want))
	m.roleMu.Lock()
	for nk := range want {
		entry, ok := m.roleCache[nk]
		if ok && now.Sub(entry.fetchedAt) < m.cfg.RoleTTL {
			out[nk] = entry.exists
			continue
		}
		missing = append(missing, nk)
	}
	m.roleMu.Unlock()

	if len(missing) == 0 {
		return out, nil
	}

	// Pass 2: probe IAM for the misses, in their *normalized* form
	// (lower-cased; iam:GetRole is case-insensitive on role names).
	// We don't parallelize here — typical clusters have <50 distinct
	// roles and IAM throttles aggressive parallel reads.
	var firstErr error
	for _, nk := range missing {
		exists, err := m.roleResolver.RoleExists(ctx, nk)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			out[nk] = false
			continue
		}
		out[nk] = exists
		m.roleMu.Lock()
		m.roleCache[nk] = roleCacheEntry{exists: exists, fetchedAt: now}
		m.roleMu.Unlock()
	}

	return out, firstErr
}

// ErrIRSAListerNotReady is returned by Ensure when the SA informer
// hasn't finished its initial sync. The HTTP handler translates this
// into a 503 with Retry-After so the SPA can re-poll.
var ErrIRSAListerNotReady = errIRSAListerNotReady{}

type errIRSAListerNotReady struct{}

func (errIRSAListerNotReady) Error() string {
	return "identity: ServiceAccount informer not yet synced"
}
