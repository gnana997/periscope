# RFC 0005 — Related-resources graph: a centralized, watch-driven index

| | |
|---|---|
| **Status** | Proposed — deferred to post-v1.1 (no work scheduled; this RFC pins the design so it can be picked up cleanly when prioritized) |
| **Owner** | @gnana997 |
| **Started** | 2026-05-08 |
| **Targets** | v1.2 (Phase 0–1, the 80% UX), v1.3+ (Phases 2–4) |
| **Related** | `docs/architecture/watch-streams.md` (informer feed this builds on), RFC 0003 (audit log — same pattern of per-event extractor running off the live feed), `docs/setup/helm-releases.md` (existing Helm release-Secret indexing — the prior art this generalizes) |

---

## 1. Summary

Periscope today shows pod relationships in a few places (the Deployment / StatefulSet / DaemonSet detail panes list their pods), but every other relationship in the cluster — what ConfigMap a Pod mounts, which Service exposes a workload, which Ingress fronts a Service, which RoleBinding grants a ServiceAccount its permissions — is invisible from the dashboard. Operators reach for `kubectl describe`, `kubectl get -o yaml | grep`, or memorise their cluster's topology.

This RFC describes the design for a **centralized, watch-driven Related-Resources Graph**: a per-cluster in-memory bidirectional adjacency index that subscribes to the same informer caches the SPA's watch streams already feed off, computes edges via per-kind extractor functions on every watch event, and serves `(cluster, gvk, ns, name) → []Edge` queries to a single new SPA component (`<RelatedList>`) rendered as a "Related" tab on every detail pane.

**This is a design memo, not a build plan.** No work is scheduled. The point of the RFC is (a) to write down the architecture before the team forgets the constraints discovered while implementing the existing Helm release-Secret tracking and the pod-relationship code, (b) to phase the work so the first ship is small, and (c) to surface the load-bearing decision (the selector strategy) before any code is written.

---

## 2. Motivation

Two forces converge:

1. **The watch streams already pay the read cost.** Periscope holds 25+ live informer caches per cluster (see `docs/architecture/watch-streams.md` for the shipped list). Every object the index would need to know about is already in memory, indexed by GVK + ns + name, and updated incrementally. Building an index on top of those caches costs only the per-event extractor — sub-microsecond — relative to the watch decode itself.
2. **Prior art exists in-tree.** Helm release tracking already maintains a `release-Secret → rendered-objects` mapping. EKS add-on tracking does something similar. Both are bespoke. Generalising into one `RelationshipIndex` lets future work (ArgoCD Application tracking, kustomize attribution, CRD ownership) plug in by adding an extractor instead of reinventing the index.

The non-goal of this RFC is "match Lens / k9s / Argo's relationship UIs feature-for-feature." The goal is the 80% case (ownership, ref-by-name, selector matching for the obvious selector-bearing kinds), shipped in a way that does not collapse on a 50k-object cluster.

---

## 3. Goals

1. **One index per cluster, not per-page.** Every consumer (detail pane "Related" tab, future graph view, future "find orphaned ConfigMap" report) reads the same bidirectional adjacency.
2. **Build off the existing informer caches.** No new watch connections to the apiserver. The index is a passive subscriber to the events the SPA's SSE streams already consume.
3. **Sub-millisecond queries.** Hash lookup of the requested ref, return inbound + outbound edges. No graph-traversal at query time.
4. **Bounded memory** — proportional to edge count, not object count. Empirically that's MB to low-tens-of-MB for typical 5k-object clusters.
5. **Coverage you can reason about.** Every edge type ships with an extractor whose source lives in one well-named file; missing edges are diagnosable by reading one function, not by searching the codebase.
6. **Phased delivery.** The first ship covers `metadata.ownerReferences` (zero per-kind code, free coverage of Deployment → RS → Pod, Job → Pod, etc.) and validates the architecture end-to-end before any per-kind extractor is written.

## 4. Non-goals

- **A graph database.** The data is in informer memory. Sync to Neo4j / Postgres / a separate store doubles the RAM and adds a sync problem this RFC explicitly rejects.
- **Multi-cluster edges.** Federated services, ArgoCD `Application` targeting another cluster — interesting but out of scope. Each cluster keeps an independent graph.
- **Performance-tuned selector resolution on launch day.** A simple cache is fine for v1; profile-driven optimisation (per-namespace label index, etc.) lands only if measured to matter.
- **Per-CRD extractor coverage.** The architecture supports CRD extractors but shipping them is its own design pass per CRD vendor (cert-manager, ArgoCD, Crossplane).
- **A separate "graph view" UI.** v1 ships only the "Related" tab on existing detail panes. A force-directed graph view is a future RFC.

---

## 5. Approach

### 5.1 The shape of relationships in K8s

Edges fall into four mechanically-different bins. Recognising the bin is the first step before designing the extractor:

| Bin | How K8s expresses it | Examples | Cost |
|---|---|---|---|
| **Ownership** | `metadata.ownerReferences` | Deployment → RS → Pod, Job → Pod, CronJob → Job, Helm → tracked objects | Free — universal field, no per-kind code |
| **Ref-by-name** | A field literally names another object | `Pod.spec.volumes[].configMap.name`, `Ingress.spec.tls[].secretName`, `Pod.spec.imagePullSecrets[].name`, `Pod.spec.serviceAccountName`, `HPA.spec.scaleTargetRef`, `RoleBinding.subjects[]` | Cheap — extractor pulls a `(kind, ns, name)` triple from the source |
| **Selector-based** | `selector` on one side matches `labels` on another | Service → Pods, NetworkPolicy → Pods, PDB → Pods, EndpointSlice → Service | Expensive — recomputes on either side changing |
| **Implicit / out-of-band** | Convention or controller-internal annotation | Helm release Secret → rendered objects, ArgoCD `Application` tracking labels, kustomize attribution | Per-source extractor + lookup |

The first two are the 80% UX win; selectors are the trap that tanks naive implementations; the last is where Periscope already has prior art (Helm release Secret indexing).

### 5.2 Architecture

```
                ┌─ apiserver watch ──────────────┐
                │                                │
        ┌───────▼─────────┐              ┌──────▼──────┐
        │ shared informer │ ── events ── │  Extractor  │  per-kind pure fn:
        │     cache       │              │  registry   │  obj → []Edge
        └───────┬─────────┘              └──────┬──────┘
                │ get / list                    │
                │                  diff prev vs new edges
                │                               │
        ┌───────▼─────────┐              ┌──────▼──────┐
        │  HTTP handlers  │ ── query ──▶ │ Relationship│  bidirectional adj:
        │ /related/{ref}  │              │    Index    │  ref → []Edge (in/out)
        └─────────────────┘              └──────┬──────┘
                                                │
                                         ┌──────▼──────┐
                                         │ Label index │  ns → label → []podRef
                                         │ (selector   │  (Phase 2 only — see
                                         │  fast path) │   selector strategy)
                                         └─────────────┘
```

Concrete components:

- **`internal/k8s/relationship/index.go`** — the `RelationshipIndex` type. One per cluster, lives next to the existing per-cluster informer plumbing. Bidirectional adjacency: `map[Ref]map[Ref]Edge`. Add / remove / lookup are O(1) hash ops. Goroutine-safe (RWMutex).
- **`internal/k8s/relationship/extractors/`** — one file per source kind: `pod.go`, `service.go`, `ingress.go`, `hpa.go`, `pvc.go`, etc. Each exports a pure `Extract(obj) []Edge` function. The registry maps `GVK → Extractor` and is consulted on every watch event.
- **`internal/k8s/relationship/wire.go`** — the watch subscription glue. Given an informer cache and the index, registers `OnAdd / OnUpdate / OnDelete` handlers that diff old vs new edges and call `index.Apply(diff)`.
- **`internal/api/related.go`** — single new HTTP handler: `GET /api/clusters/{c}/{gvr}/{ns}/{name}/related` returning `[]Edge` JSON. Reuses the existing impersonated REST mapper for RBAC scope.
- **`web/src/components/detail/RelatedList.tsx`** — single new component. Renders a list of edges grouped by `EdgeKind` (mounts / envFrom / exposes / owns / scaled-by / binds). Click an edge → navigate via existing `sourceToResourceRef` plumbing to that target's detail pane. One component renders for every kind because the data shape is uniform.

### 5.3 Edge representation

```go
package relationship

type EdgeKind string

const (
    EdgeOwns           EdgeKind = "owns"            // ownerReference -> child
    EdgeMounts         EdgeKind = "mounts"          // pod -> ConfigMap/Secret/PVC via volumes
    EdgeEnvFrom        EdgeKind = "envFrom"         // pod -> ConfigMap/Secret via envFrom
    EdgeValueFrom      EdgeKind = "valueFrom"       // pod -> CM/Secret key via env[].valueFrom
    EdgeImagePullSecret EdgeKind = "imagePullSecret" // pod -> Secret
    EdgeServiceAccount EdgeKind = "serviceAccount"  // pod -> SA
    EdgeExposes        EdgeKind = "exposes"         // Service -> Pod (selector); Ingress -> Service
    EdgeRoutes         EdgeKind = "routes"          // Ingress -> Service via rules[].http.paths[].backend
    EdgeTLS            EdgeKind = "tls"             // Ingress -> Secret (tls[].secretName)
    EdgeScales         EdgeKind = "scales"          // HPA -> scaleTargetRef
    EdgeBinds          EdgeKind = "binds"           // RoleBinding -> Role + Subject
    EdgeBoundTo        EdgeKind = "boundTo"         // PVC -> PV (volumeName); PVC -> StorageClass
    EdgeManages        EdgeKind = "manages"         // Helm release Secret / ArgoCD App -> tracked objects
)

type Ref struct {
    Cluster   string  // matches the existing per-cluster informer key
    GVK       GVK     // group/version/kind
    Namespace string  // empty for cluster-scoped
    Name      string
}

type Edge struct {
    From   Ref
    To     Ref
    Kind   EdgeKind
    Detail string  // optional context: "key=DATABASE_URL" for valueFrom; "host=api.example.com" for Ingress route
}
```

`Detail` is render-only — never used for lookups. Keeping it as a single string (vs a typed struct) means new edge kinds don't widen the schema.

### 5.4 The selector problem (the load-bearing decision)

Service / NetworkPolicy / PDB / EndpointSlice pick targets via `matchLabels` + `matchExpressions`. Naive evaluation on every Pod label change is O(services × pods_changing) per event — fine on a 100-pod cluster, problematic on 5k. There are two reasonable strategies:

**Strategy A — Reverse selector cache (recommended for v1).**
On selector-bearing object change: re-evaluate the selector against the current pod cache, store the resulting `selector → []podRef` set. On Pod label change: scan only those cached selectors whose match-set might be affected (heuristic: scan all selectors in the same namespace; selectors don't change often, this scan is the rare path). Simple code, slightly more invalidation work than (B), no data structure beyond `map[selectorOwnerRef][]podRef`.

**Strategy B — Per-namespace label index.**
Maintain `map[namespace]map[labelKey]map[labelValue]map[podRef]struct{}`. Selector resolution intersects the buckets for each `(key, value)` pair in the selector. O(distinct selector keys) expected. Faster but more memory and more bookkeeping.

**Decision:** ship Strategy A in the v1 implementation. Selectors don't change often; pod labels do change often but most aren't selector-relevant. If profiling on a real 5k+ pod cluster shows selector resolution dominating CPU during pod-churn storms, swap in Strategy B. The interface (`resolveSelector(selectorOwner) []podRef`) does not change between strategies.

Cross-namespace selectors (NetworkPolicy.spec.namespaceSelector + podSelector) need a small extension to the resolution function — out of scope for v1, deferred to Phase 2.

### 5.5 Watcher reconnection and consistency

Informers do their own resync. The index reconciles on each full LIST result (rebuild that GVR's edges from scratch, diff against current state, apply the delta). Standard informer pattern; the index does **not** assume watch events are gospel.

Two implications:

- The extractor must be idempotent. Calling `Extract(obj)` twice on the same object returns the same edges. (Pure functions handle this trivially.)
- Edge garbage collection on object deletion is automatic: `OnDelete(obj) → diff against last-seen edges → remove all of them from the index.` The index never holds references to deleted source objects.

### 5.6 RBAC and impersonation

Read-side: `GET /api/clusters/{c}/{gvr}/{ns}/{name}/related` runs the same impersonated SSAR as the existing detail-pane fetches. If the operator can't read the **target** of an edge, that edge is filtered from the response (don't leak the existence of resources they can't see). The index itself runs in the cluster-process identity (the same identity the informers run under) — it has full visibility; the API layer enforces per-user filtering.

### 5.7 Audit and observability

A new metric per extractor per cluster: `periscope_relationship_edges_total{cluster, source_gvk, edge_kind}`. Operators / contributors see at a glance whether extractor coverage is healthy ("Service extractor produced 0 exposes-edges in 24h on cluster X" → either the cluster has no Services or the extractor regressed).

Edge extraction does **not** emit audit events. This is read-side derivation, not a privileged action.

---

## 6. Phasing

The phases are independently shippable. Each phase is a separate PR or two; nothing in a later phase blocks an earlier one.

### Phase 0 — Skeleton + ownerReferences (validates the architecture)

- `RelationshipIndex` type, watch wiring for one informer (Pod), one extractor (`ownerReferences`).
- One HTTP handler: `GET .../related`.
- One SPA component: `<RelatedList>` rendering on the Pod detail pane only.
- **Ships visible UX** (Pod → owning ReplicaSet → owning Deployment) on day one.
- **Validates** the wiring, the API shape, the SPA rendering, and the metric — without any per-kind extractor sprawl.

After Phase 0 you know the architecture works end-to-end; everything else is "add an extractor and wire its informer."

### Phase 1 — Spec-driven ref-by-name extractors (the 80% UX)

For the kinds in the existing watch stream list (`docs/architecture/watch-streams.md`):

- **Pod / pod-template-bearing kinds** (Deployment, StatefulSet, DaemonSet, Job, CronJob via owned Pods) — `volumes[].configMap`, `volumes[].secret`, `volumes[].persistentVolumeClaim`, `envFrom[]`, `env[].valueFrom`, `imagePullSecrets[]`, `serviceAccountName`.
- **Ingress** → Service (`rules[].http.paths[].backend.service.name`), Secret (`tls[].secretName`).
- **HPA** → scale target (`spec.scaleTargetRef`).
- **PVC** → PV (`spec.volumeName`), StorageClass (`spec.storageClassName`).

After Phase 1: "what uses this ConfigMap?" and "what does this Pod mount?" both work.

### Phase 2 — Selector resolution

- `Service` → matched Pods (via Strategy A).
- `NetworkPolicy` → matched Pods.
- `PDB` → matched Pods.
- `EndpointSlice` → owning Service (already a label-driven name match, simpler).

The Strategy A cache lands here. After Phase 2: "what Service exposes this Pod?" works.

### Phase 3 — RBAC graph

- `RoleBinding` / `ClusterRoleBinding` → `Role` / `ClusterRole` + each subject (`User` / `Group` / `ServiceAccount`).
- ServiceAccount → owning Pods (already covered by Phase 1's `serviceAccountName` extractor; this phase just adds the inbound view to the SA detail pane).

After Phase 3: "what permissions does this ServiceAccount have, and what Pods use it?" works in one view.

### Phase 4 — Fold existing in-tree relationship code into the index

- Helm release Secret → tracked objects: replace the bespoke index with a `helm-release` extractor.
- EKS add-on → managed resources: same shape.
- Existing pod-related-resources logic in detail panes: replace with `<RelatedList>`.

After Phase 4 there is one source of truth for "what is related to what" in Periscope, and the per-feature ad-hoc indices are deleted.

### Out of scope (post-v1.3, separate RFC if pursued)

- CRD extractors (cert-manager `Certificate` → `Issuer` + `Secret`, ArgoCD `Application` → tracked objects, Crossplane `Composition` → composed resources).
- Cross-cluster federation edges.
- Force-directed graph view UI.
- "Find orphaned ConfigMap / Secret" derived report.

---

## 7. UI surface

A single `Related` tab on every detail pane, rendered by `<RelatedList edges={edges} />`. Edges are grouped by `EdgeKind`, with inbound and outbound separated. Sketch:

```
Related ─────────────────────────────────────────
Used by                                          
  Deployment       api          mounts data       ← ConfigMap inbound
  Deployment       api          envFrom           
  Pod              api-7d94...  mounts data       
                                                  
References                                        
  ServiceAccount   default                        ← Pod outbound
  Secret           regcred      imagePullSecrets  
                                                  
Owned by                                          
  ReplicaSet       api-7d94...                    
```

Click any line → navigate to that resource's detail pane via the existing `sourceToResourceRef` routing. Empty groups are hidden. No edges → render the existing detail-pane body unchanged (no empty "Related" tab on resources with nothing to show).

---

## 8. Cost ballpark

- **Memory:** ~MB to low-tens-of-MB per cluster for typical sizes. Linear in edge count, not object count. Sparse on most clusters.
- **CPU per watch event:** extractor is microseconds (pure function over an in-memory object), diff is a small set difference, index update is a few hash ops. Effectively free relative to the watch decode itself.
- **Query latency:** single hash lookup, sub-millisecond regardless of cluster size.

The ballpark answers the "is this even feasible at scale" question affirmatively before any code is written.

---

## 9. Open questions

These are flagged for the team to decide when work resumes; nothing here is a blocker for the design as written.

1. **Should the index persist across process restarts?** No, in v1. Informers re-LIST on startup; the index rebuilds from those LISTs. Persistence buys nothing functionally and adds a sync problem. Revisit only if startup latency on huge clusters becomes a UX problem.
2. **Should per-CRD extractor coverage ship before Phase 4?** Probably not — Argo / cert-manager / Crossplane each deserve their own design pass. The architecture is CRD-ready (just register an extractor) but the routing UX is not.
3. **What's the index's response when a query targets a non-watched kind?** Two options: (a) return 404, (b) lazily fetch via the impersonated client and extract on demand. Recommendation: ship (a) in v1, revisit if operators complain about specific kinds. Lazy fetch defeats the whole point of pre-computation.
4. **How does this interact with the existing pod relationships shown today?** Phase 4 explicitly folds them in. Until Phase 4 lands, both code paths coexist; the existing UI keeps working unchanged.
5. **Does the SPA need a "graph view" eventually, or is the list UX enough?** This RFC commits only to the list. The data model (`Edge`) is general enough to back a graph view; a future RFC can add the rendering without touching the index.
6. **Periscope's audit trail** — RFC 0003 covers privileged-action audit. Related-resources is read-side; no audit changes needed. Confirm this when work resumes (one paragraph in the implementation PR).

---

## 10. Decision

**Accept the design as written. Defer implementation to post-v1.1.**

Rationale:

- v1.1 is already busy: schema-form editor for the four config kinds (#116, shipped), oneOf/allOf engine extensions (#132, shipped via #133), Deployment + StatefulSet form coverage (#136, in flight via #135), plus the existing Helm/EKS/auth surface that needs to stay green.
- The related-resources index touches the backend's hottest path (informer event handling) — better to land it after v1.1 stabilises than to bolt it on mid-cycle.
- The architecture is well-understood (Argo / Lens / k9s ship versions of it); the load-bearing decision (selector strategy A) is documented; the phasing plan ships visible UX in Phase 0 with minimal risk.

When work resumes, the entry point is **Phase 0**: cut a branch, scaffold `internal/k8s/relationship/`, wire the Pod informer, ship `<RelatedList>` on the Pod detail pane only. Everything beyond Phase 0 is "add an extractor and wire its informer." Estimated effort, ballpark: Phase 0 ≈ 1 week, Phase 1 ≈ 2 weeks, Phase 2 ≈ 1 week, Phase 3 ≈ 1 week, Phase 4 ≈ 1 week. Frontend work is small throughout (one shared component, edge-rendering polish per phase).
