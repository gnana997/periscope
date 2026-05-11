package cve

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/gnana997/periscope/internal/awsec2"
	"github.com/gnana997/periscope/internal/clusters"
)

// hydrate runs the cold-path scan for a cluster:
//
//  1. Probe Inspector v2 enablement; if disabled, mark store +
//     return without touching the rest.
//  2. List EC2 instances tagged to this cluster, batch-fetch
//     instance findings, classify owners, and populate the store's
//     instance entries.
//  3. List Pods in the cluster, extract image digests, batch-fetch
//     image findings, and populate the store's digest entries with
//     correct PodRef counts.
//  4. Start the long-lived pod informer that keeps the digest set
//     and ref counts in sync as pods roll.
//  5. MarkHydrated (or MarkDisabled).
//
// Returning a non-nil error still leaves the store in a usable state
// — at minimum, MarkHydrated/MarkDisabled is called so future
// EnsureHydrated callers don't block forever.
func (m *Manager) hydrate(ctx context.Context, cluster clusters.Cluster, st *clusterState) error {
	enabled, err := m.inspector.IsEnabled(ctx)
	if err != nil {
		m.log.Warn("cve hydrate: inspector enablement probe failed", "cluster", cluster.Name, "err", err)
		// Treat probe failure as "do not hydrate, do not retry now".
		// The store stays empty but un-disabled; the next manual
		// refresh or a future SIGHUP can retry. Mark hydrated so
		// readers don't deadlock.
		st.store.MarkHydrated()
		return err
	}
	if !enabled {
		m.log.Info("cve hydrate: inspector v2 not enabled", "cluster", cluster.Name)
		st.store.MarkDisabled()
		return nil
	}

	// Defer the MarkHydrated so an early-return from instance- or
	// pod-side hydration still unblocks waiters with whatever data
	// we managed to collect.
	defer st.store.MarkHydrated()

	cs, err := m.clientFor(ctx, cluster)
	if err != nil {
		return fmt.Errorf("build k8s client for %q: %w", cluster.Name, err)
	}

	// Phase 1: nodes → instance IDs → Inspector instance findings.
	instanceIDs, instanceFindings, err := m.hydrateInstances(ctx, cluster, cs, st)
	if err != nil {
		m.log.Warn("cve hydrate instances", "cluster", cluster.Name, "err", err)
	}
	_ = instanceIDs
	_ = instanceFindings

	// Phase 2: pods → digests → Inspector image findings.
	if err := m.hydratePods(ctx, cluster, cs, st); err != nil {
		m.log.Warn("cve hydrate pods", "cluster", cluster.Name, "err", err)
	}

	// Phase 3: start the long-lived pod informer for delta updates.
	// Failure here only loses the live-delta path; TTL still works.
	if err := m.startPodInformer(ctx, cluster, cs, st); err != nil {
		m.log.Warn("cve hydrate: pod informer", "cluster", cluster.Name, "err", err)
	}
	return nil
}

// hydrateInstances reads K8s Nodes, extracts EC2 instance IDs from
// their Spec.ProviderID, classifies each instance via the owner
// resolver, and bulk-fetches Inspector findings for them. Returns
// the deduped instance ID list and the flat findings list (currently
// shared across all instances — see fetchInstances TODO).
func (m *Manager) hydrateInstances(ctx context.Context, cluster clusters.Cluster, cs kubernetes.Interface, st *clusterState) ([]string, []Finding, error) {
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("list nodes: %w", err)
	}
	if len(nodes.Items) == 0 {
		return nil, nil, nil
	}

	instanceIDs := make([]string, 0, len(nodes.Items))
	idToNode := make(map[string]string, len(nodes.Items))
	for _, n := range nodes.Items {
		id := instanceIDFromProviderID(n.Spec.ProviderID)
		if id == "" {
			continue
		}
		instanceIDs = append(instanceIDs, id)
		idToNode[id] = n.Name
	}
	if len(instanceIDs) == 0 {
		return nil, nil, nil
	}

	// Owner classification needs the EC2 instance tags.
	resolver := NewOwnerResolver(asEC2Client(m.ec2), func(_ context.Context) (kubernetes.Interface, error) { return cs, nil })
	metas, err := m.ec2.DescribeInstances(ctx, instanceIDs)
	if err != nil {
		// We can still register Inspector findings against instance
		// IDs even without EC2 tags; the owner falls back to
		// unmanaged. Don't abort hydrate.
		m.log.Warn("cve hydrate: ec2 describe instances", "cluster", cluster.Name, "err", err)
	}
	kinds, names, _ := resolver.Resolve(ctx, metas)

	findings, err := m.inspector.ListFindingsByInstance(ctx, instanceIDs)
	if err != nil {
		// Inspector AccessDenied mid-hydrate flips us to disabled.
		// We don't have a clean signal for that without re-checking;
		// best-effort: surface the error, leave instances empty.
		return instanceIDs, nil, err
	}

	now := m.clock.Now()
	for _, id := range instanceIDs {
		kind := kinds[id]
		if kind == "" {
			kind = OwnerUnmanaged
		}
		st.store.UpsertInstance(id, findings, kind, names[id], now)
		st.store.IncInstanceRef(id)
	}
	return instanceIDs, findings, nil
}

// hydratePods reads K8s Pods, extracts image digests via the
// containerStatuses[].imageID predicate, and populates the digest
// side of the store. PodRefs are incremented at the same time so the
// eviction sweeper has the right starting state.
func (m *Manager) hydratePods(ctx context.Context, cluster clusters.Cluster, cs kubernetes.Interface, st *clusterState) error {
	pods, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list pods: %w", err)
	}

	// First pass: count refs and collect the unique digest set.
	seenDigest := make(map[string]struct{}, len(pods.Items))
	digestList := make([]string, 0, len(pods.Items))
	for i := range pods.Items {
		for _, d := range PodImageDigests(&pods.Items[i]) {
			st.store.IncDigestRef(d)
			if _, ok := seenDigest[d]; !ok {
				seenDigest[d] = struct{}{}
				digestList = append(digestList, d)
			}
		}
	}
	if len(digestList) == 0 {
		return nil
	}

	// Second pass: fetch + upsert. Refs are already in place from
	// the first pass so UpsertDigest preserves them.
	findings, err := m.inspector.ListFindingsByImageDigest(ctx, digestList)
	if err != nil {
		return err
	}
	now := m.clock.Now()
	// Without per-resource attribution on Finding (see
	// fetchInstances TODO), we currently store the shared findings
	// list per digest. The API layer (#165) filters on the digest
	// it cares about.
	for _, d := range digestList {
		st.store.UpsertDigest(d, findings, now)
	}
	return nil
}

// asEC2Client wraps the EC2API the manager holds into the concrete
// *awsec2.Client the OwnerResolver expects. In tests the manager
// can be given a stub for EC2API; if a test path reaches the owner
// resolver and the underlying client isn't the concrete type, the
// resolver still works because its only EC2 use is DescribeInstances
// — which we already fetched above and pass in directly. The
// adapter is here to satisfy the constructor signature.
func asEC2Client(api EC2API) *awsec2.Client {
	if c, ok := api.(*awsec2.Client); ok {
		return c
	}
	return nil
}

