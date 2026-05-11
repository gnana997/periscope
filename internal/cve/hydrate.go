package cve

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/gnana997/periscope/internal/clusters"
)

// hydrate runs the cold-path scan for a cluster:
//
//  1. Probe Inspector v2 enablement; if disabled, mark store +
//     return without touching the rest.
//  2. List EC2 instances tagged to this cluster, batch-fetch
//     instance findings (grouped per-instance), classify owners,
//     and populate the store's instance entries.
//  3. List Pods in the cluster, extract image digests, batch-fetch
//     image findings (grouped per-digest), and populate the store's
//     digest entries with correct PodRef counts.
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
		st.store.MarkHydrated(m.clock.Now())
		return err
	}
	if !enabled {
		m.log.Info("cve hydrate: inspector v2 not enabled", "cluster", cluster.Name)
		st.store.MarkDisabled(m.clock.Now())
		return nil
	}

	// Defer the MarkHydrated so an early-return from instance- or
	// pod-side hydration still unblocks waiters with whatever data
	// we managed to collect.
	defer st.store.MarkHydrated(m.clock.Now())

	cs, err := m.clientFor(ctx, cluster)
	if err != nil {
		return fmt.Errorf("build k8s client for %q: %w", cluster.Name, err)
	}

	// Phase 1: nodes → instance IDs → Inspector instance findings.
	if err := m.hydrateInstances(ctx, cluster, cs, st); err != nil {
		m.log.Warn("cve hydrate instances", "cluster", cluster.Name, "err", err)
	}

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
// resolver, and bulk-fetches Inspector findings for them. Findings
// are grouped per-instance by the Inspector client so each row gets
// its own subset (not the union, as the v1.1-review caught).
func (m *Manager) hydrateInstances(ctx context.Context, cluster clusters.Cluster, cs kubernetes.Interface, st *clusterState) error {
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	if len(nodes.Items) == 0 {
		return nil
	}

	instanceIDs := make([]string, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		if id := instanceIDFromProviderID(n.Spec.ProviderID); id != "" {
			instanceIDs = append(instanceIDs, id)
		}
	}
	if len(instanceIDs) == 0 {
		return nil
	}

	// Owner classification needs the EC2 instance tags.
	resolver := NewOwnerResolver(func(_ context.Context) (kubernetes.Interface, error) { return cs, nil })
	metas, err := m.ec2.DescribeInstances(ctx, instanceIDs)
	if err != nil {
		// We can still register Inspector findings against instance
		// IDs even without EC2 tags; the owner falls back to
		// unmanaged. Don't abort hydrate.
		m.log.Warn("cve hydrate: ec2 describe instances", "cluster", cluster.Name, "err", err)
	}
	kinds, names, _ := resolver.Resolve(ctx, metas)
	amiByID := make(map[string]string, len(metas))
	for _, mt := range metas {
		amiByID[mt.InstanceID] = mt.AMI
	}

	grouped, err := m.inspector.ListFindingsByInstance(ctx, instanceIDs)
	if err != nil {
		// AccessDenied mid-fetch matches the "Inspector got
		// disabled" race; surface the error, leave instance findings
		// empty.
		return err
	}

	now := m.clock.Now()
	for _, id := range instanceIDs {
		kind := kinds[id]
		if kind == "" {
			kind = OwnerUnmanaged
		}
		st.store.UpsertInstance(id, grouped[id], kind, names[id], amiByID[id], now)
		st.store.IncInstanceRef(id)
	}
	return nil
}

// hydratePods reads K8s Pods, extracts image digests via the
// containerStatuses[].imageID predicate, and populates the digest
// side of the store. PodRefs are incremented at the same time so the
// eviction sweeper has the right starting state.
func (m *Manager) hydratePods(ctx context.Context, cluster clusters.Cluster, cs kubernetes.Interface, st *clusterState) error {
	pods, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	m.log.Info("cve hydratePods listed", "cluster", cluster.Name, "pod_count", len(pods.Items), "list_err", err)
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
	m.log.Info("cve hydratePods digests", "cluster", cluster.Name, "unique_digests", len(digestList))
	if len(digestList) == 0 {
		return nil
	}

	// Second pass: fetch + upsert. Refs are already in place from
	// the first pass so UpsertDigest preserves them.
	grouped, err := m.inspector.ListFindingsByImageDigest(ctx, digestList)
	if err != nil {
		return err
	}
	now := m.clock.Now()
	for _, d := range digestList {
		st.store.UpsertDigest(d, grouped[d], now)
	}
	return nil
}
