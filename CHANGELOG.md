# Changelog

All notable changes to Periscope are tracked here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html):
the public HTTP API, the OIDC / cluster-registry config shape, and Helm
chart values are the surfaces covered by semver.

For per-release container images and signed Helm charts, see the
[GitHub Releases](https://github.com/gnana997/periscope/releases) page;
its auto-generated notes complement this file with the full PR list per
tag.

## [Unreleased]

### Fixed

- Auth: explicit logout no longer loops back through silent SSO when
  the IdP session is still valid. The SPA bundle is now served outside
  the chi router so the auth middleware's "redirect HTML browser
  navigations to `/api/auth/login`" fallback can't run on the
  post-logout `/` load. Logout handlers redirect to `/?signedOut=1`;
  the SPA's AuthProvider strips the flag after reading and stays on
  the login screen instead of re-authenticating against Auth0's still-
  valid session (#98).

### Changed

- Auth: when `auth.groupsClaim` is configured but absent from BOTH the
  ID token and the access token at callback time, login now hard-fails
  with `401 Unauthorized` and a "your IdP did not return a groups
  claim" message. Previously the user was silently funneled to
  `defaultTier`, masking IdP misconfiguration. Operators upgrading from
  v1.0 with conditional / per-app group enrichment must verify the
  configured claim is present on every login path before this lands.
  Claim **present-but-empty** (`"groups": []`) still resolves cleanly
  to zero groups — only an absent claim is refused (#98).
- Auth: `/api/auth/whoami` now always emits `"groups": []` for users
  with zero groups, never `null`. The wire shape is now stable across
  all session states — the SPA's UserMenu can drop its null-guard.
  
### Added

- Karpenter dashboard (#118). Curated read-only view at
  `/clusters/{c}/karpenter` that auto-detects via the
  `karpenter.sh/v1` CRD probe and joins three data sources kubectl
  cant: NodePools (with weight + disruption budgets + per-pool
  $/hr from the controllers `/metrics` exposition), NodeClaims
  grouped by NodePool with the `Drifted` condition surfaced as a
  badge, and pending pods waiting on Karpenter with the per-NodePool
  rejection reasons extracted from the `FailedScheduling` apiserver
  Event. The pending-pods join is the differentiator: operators no
  longer have to grep karpenter-controller logs to find out why a
  pod isnt being scheduled — each rejected NodePool renders inline
  next to the pod with the wrapped reason ("incompatible
  requirements", "insufficient capacity", etc.). Cost summary uses
  the apiserver service-proxy verb to scrape
  `karpenter_cloudprovider_instance_type_offering_price_estimate`
  and joins it to live NodeClaim labels (instance type / capacity
  type / zone). Graceful degradation: missing /metrics or events
  list failures degrade individual panels (`metricsAvailable: false`,
  empty incompatibility breakdown) without failing the whole
  response. Sidebar entry auto-hides on clusters without
  Karpenter installed. New `karpenter_read` audit verb mirrors the
  existing `eks_insights_read` pattern (read-style, emit-on-every-call).
  Phase 2 follow-ups (disruption-blocked badge, AMI drift rollout,
  ICE blacklist panel) tracked in #148.

- Schema form: form sections start collapsed; users open what they
  need to edit. Replaces the prior "primary section open by default"
  layout — for Deployment / StatefulSet (7+ sections) the always-
  open header was overwhelming, so all sections at every depth
  (top-level, container row, container row sub-section) start
  closed. New `+ expand all` / `− collapse all` buttons in the
  form header sweep every accordion in one click (VSCode-style).
  Last-opened L1 section per kind is remembered in localStorage so
  the next visit restores your preferred entry point.

- Schema-aware form editor: Deployment + StatefulSet support, with
  per-kind L1 sectioning (#136, builds on #144). Each workload kind
  now opens with the most-edited fields up top — Containers
  (replicas + containers[] + initContainers[]) is always open;
  Volumes auto-expands when populated; Strategy & lifecycle, Pod
  metadata, the Deployment's own Metadata, the immutable Selector,
  and Advanced (15-field count) live in collapsed `<details>`
  sections. StatefulSet adds a Persistent storage section for
  `volumeClaimTemplates` + retention policy. Container array rows
  themselves get an L2 sub-section split inside the row — Primary
  (image / env / resources), Probes & lifecycle, Volume mounts
  (auto-expanding when populated), and Container advanced (count).
  Walker stamps both L1 and L2 section ids via a single resolver
  using `*` as the array-element boundary in synthetic absolute
  paths. Brings in K8s-style polymorphism support too: Probe /
  LifecycleHandler / Volume / EnvVarSource render as proper
  discriminator pickers via a hint table, instead of surfacing all
  ~30 sibling Volume types as simultaneously editable.

- Helm release rollback (#77). New `POST /api/clusters/{c}/helm/
  releases/{ns}/{name}/rollback` endpoint patches the cluster from
  the current revision to the operator-selected target revision via
  the helm SDK. Pre-flight SAR (verb=patch) runs against the target
  revision’s manifest list; denials block the action with a 403 and
  surface the (verb, GVR) tuples in the modal so the operator can
  take them to their cluster admin. Audit emits a pre/post pair
  (`helm_rollback_intent` / `helm_rollback`) carrying
  `fromRevision` / `toRevision` / `newRevision`. The release page
  history tab adds a per-row Rollback button (disabled on the
  current revision); the modal renders a side-by-side diff of the
  current vs. target manifests before submission and stays open with
  a spinner + inline error / denial state until the mutation
  settles.

- Form-mode coverage for `oneOf` discriminators and `allOf`
  composition (#132, builds on #131). Adds two extensions to the
  shared `lib/schemaForm/` engine that close the most common
  "yaml only" gaps in real-world schemas:
  - **`oneOf` discriminator UI** — the walker now emits a new
    `discriminator` descriptor type for `oneOf` shapes (gated on
    `allowOneOfDiscriminator`, opted-in by K8s, off for Helm).
    Detects two structural shapes: whole-value oneOf (e.g.
    `Service.spec.ports[].targetPort` string-or-int,
    `Ingress.spec.rules[].http.paths[].backend` Service-or-Resource)
    and object-level oneOf with required-key branches (e.g.
    cert-manager `Issuer.spec` selfSigned/ca/vault/acme,
    `aws-ebs-csi-driver` IRSA-vs-PodIdentity). The renderer is a
    segmented branch picker + sub-form for the active branch;
    switching branches is destructive (confirm-then-wipe, mirroring
    the form↔yaml toggle pattern). Branch labels come from the
    sub-schema's `title`, the single required-key name, or the
    single property name as a fallback heuristic.
  - **`allOf` merger** — pure schema-merge function that flattens
    `allOf:[{$ref: Base}, {properties: Override}]` shapes into a
    single rendered schema before the walker emits descriptors.
    Runs inside `derefIfNeeded`, so all consumers (Helm + K8s)
    benefit without opting in. Unblocks the `metadata` field on
    every K8s kind (was always yaml-only because metadata is
    `allOf:[{$ref: ObjectMeta}]` everywhere) — operators can now
    edit labels / annotations through the form. Type conflicts
    across allOf entries abort the merge cleanly and surface as
    the existing unsupported badge.
  - **Description tooltips** — long OpenAPI v3 descriptions
    (the `host` field on Ingress carries a ~700-char paragraph)
    no longer render inline. A small `(i)` icon next to each
    label opens a tooltip with the description on hover/focus.
    Single-file change benefits every form-mode consumer (K8s
    edit pane, Helm install/upgrade dialogs, EKS add-on dialogs).

- Schema-aware form editor for ConfigMap, Secret, Service, and
  Ingress (#116, supersedes #130). The detail-pane YAML tab on these
  four kinds now renders a structured form by default, driven by the
  apiserver's OpenAPI v3 schema filtered through per-kind allowlists
  that hide noise (status, managedFields, creationTimestamp, etc.).
  Operators toggle to a Monaco YAML view via a header switch that
  persists per-user in localStorage; switching modes with unsaved
  edits prompts a confirm-discard. metadata.name, metadata.namespace,
  and Service spec.clusterIP render read-only in edit mode (immutable
  on update). Secret data values are decoded to plaintext for editing
  with a "show raw base64" toggle, and the apply payload re-encodes
  to base64 so AWS / apiserver receives the canonical wire shape.
  Apply flows through the same SSA pipeline as the YAML editor; on
  409 conflict the form-mode banner directs the operator to YAML for
  the per-field conflict-resolution view.
  
  Internal: extracts the JSON Schema → React form engine (originally
  built for Helm in #109) into a reusable `web/src/lib/schemaForm/`
  library with K8s-specific extensions (`resolveRef`, `allowKvMap`,
  `allowArrayOfObjects`, `createOnlyPaths`). The Helm install /
  upgrade dialogs and the EKS add-on install / upgrade dialogs now
  share the same renderer; their existing form/YAML toggle (#126),
  controlled `mode` / `onModeChange` props (#128), comment-loss
  banner, and YAML-stub auto-default behavior all consume the
  shared `SchemaFormBridge` via the bridge's optional `banner`
  slot. Soft-deprecates `web/src/lib/helmSchema.ts` to a re-export
  shim — drop in v1.2.

## [1.0.4] - 2026-05-07

### Added

- EKS add-on detail pane: configurationValues + Pod Identity, plus
  state-stickiness fixes (#128). New `describe` / `config` tab strip
  on the right-edge `AddonDetailPane` in installed mode. The `config`
  tab renders the addon's stored `configurationValues` (the operator's
  own overrides) in a read-only Monaco YAML viewer — the data was
  already in the `AddonDetail` blob from AWS, but the pane never
  rendered it, so operators had to open the Upgrade dialog just to
  see what they previously set. Backend now exposes
  `podIdentityAssociations` from the `DescribeAddon` response (was
  being dropped on the floor in the SDK mapper); the describe tab
  surfaces Pod Identity ARNs under a dedicated section, with a
  "No IAM identity attached — addon runs with the cluster's
  node-instance role" fallback when both IRSA and Pod Identity are
  absent. State-stickiness fixes from rc-2 testing: stale-selection
  guard clears the pane when the underlying row vanishes from the
  addons / catalog list (delete settled, refetch returned a smaller
  set — was sticking on "Failed to load detail" before); YAML stub
  re-seeds on schema change only when the buffer matches the
  previously-seeded stub (preserves operator edits across version
  pick); explicit Form/YAML mode is preserved across version change
  via lifted dialog-level state. No new IAM action — Pod Identity
  is part of the existing `eks:DescribeAddon` response.

- EKS add-on dialog polish (#127). `Upgrade to` button on the pane's
  version-history rows now only renders for versions strictly newer
  than installed — the original implementation offered downgrades by
  reflex, which carry CRD / IAM / schema risk that one-click obscures
  (downgrade path stays available via the kebab → Upgrade modal,
  where the deliberate older-eksbuild pick is visible). Generated
  commented YAML stub seeds the install / upgrade dialog editor
  when an addon has no `configurationValues` yet, so YAML mode is
  discoverable for `$ref` / `allOf`-heavy schemas — `aws-ebs-csi-driver`
  no longer opens to a blank textarea. Monaco container height fix:
  YAML wrappers now use fixed `h-[420px]` / `h-[460px]` instead of
  `min-h-` + `flex-1`, since `min-height` alone is indefinite for
  percentage resolution and Monaco's `h-full` container needs a
  definite-height parent — without this the editor rendered as a
  silent grey rectangle.

- Right-edge detail pane + form/yaml toggle for EKS add-ons (#126).
  Replaces the original inline-row-expand on `AddOnsPage` and
  `EKSAddOnsCatalogPage` with a `SplitPane` + new `AddonDetailPane`,
  matching the pattern every other resource page (pods / deps /
  configmaps / secrets / RBAC) already uses. The pane scrolls
  independently of the page (so the version-history table no longer
  pushes the ARN below the fold) and width persists under the shared
  `periscope.detailWidth.v4` storage key — operator's chosen pane
  width travels across every resource page in the app. Both installed
  rows (Upgrade / Delete) and available catalog rows (Install) open
  the pane on click; the row's primary action button stops
  propagation so the modal-direct path still works. New form/yaml
  toggle on `HelmValuesEditor` lets operators escape to raw YAML
  when a chart's `values.schema.json` contains `$ref` / `allOf` /
  `anyOf` / arrays-of-objects (auto-defaults to YAML when the schema
  has any required-but-unrenderable field). Version-history rows in
  the pane are clickable to open the upgrade dialog with that version
  pre-selected; new `compatible with k8s [X ▼]` dropdown above the
  table answers "which add-on version supports the next k8s minor?"
  inline (the question raised by the row's "blocks next k8s minor"
  warning chip).

- EKS managed add-ons read-only introspection surface (#122,
  closing issue #117). New `Add-ons` sidebar entry under EKS opens
  a table of installed managed add-ons with health (●/▲/✕), installed
  version, latest available, k8s compatibility window, and status.
  The `blocks next k8s minor` chip on each row warns when no compatible
  addon version exists for the cluster's next minor — feeds the
  upgrade-readiness story alongside #97 / #113. New endpoints
  `GET /api/clusters/{c}/eks/addons` (list) and
  `GET /api/clusters/{c}/eks/addons/{name}` (detail) wire
  `eks:ListAddons` + `eks:DescribeAddon` (addon-scoped resource
  ARN — see `docs/setup/deploy.md` 4.1 for the policy split) plus
  `eks:DescribeAddonVersions`. Two-tier server-side cache: per-cluster
  1h, plus a shared `(addonName, k8sVersion)` 6h sticky-error catalog
  so a fleet of N 1.31 clusters hits AWS once per cache window.
  Status-aware refetch in `useAddons` polls every 4s while any addon
  is in `CREATING` / `UPDATING` / `DELETING`, off otherwise. Read-only
  in this PR — install / catalog browse / upgrade / delete actions
  ship in #119.

- EKS add-on upgrade + delete actions (#119, PR-3 of three,
  closing the issue). New `PUT /api/clusters/{c}/eks/addons/{name}`
  and `DELETE /api/clusters/{c}/eks/addons/{name}?preserve=...`
  wire `eks:UpdateAddon` and `eks:DeleteAddon`. Same async-by-design
  contract as install: returns 202 with status `UPDATING` /
  `DELETING`; the SPA's status-aware polling watches the flip. Both
  endpoints reuse the install handler's body validation, audit
  pair (`eks_addon_upgrade_intent` + `eks_addon_upgrade`,
  `eks_addon_delete_intent` + `eks_addon_delete`), and cache-
  invalidation paths. New `KebabMenu` UI primitive surfaces
  Upgrade / Delete on installed-addon rows in both `AddOnsPage`
  and `EKSAddOnsCatalogPage`. New `UpgradeAddOnDialog` parallels
  the install dialog (target version radio, schema-aware config
  editor, `PRESERVE` default for resolveConflicts since upgrades
  usually keep cluster-side overrides). New `DeleteAddOnModal`
  wraps `ConfirmActionModal` with a `preserve` checkbox so an
  operator doesn't accidentally rip out coredns and break DNS.
  Kebab actions are disabled while AWS is mid-transition
  (`CREATING`/`UPDATING`/`DELETING`) — sending another mutation
  during a pending one produces opaque AWS errors. The new IAM
  actions `eks:UpdateAddon` and `eks:DeleteAddon` join the
  `EKSAddonScoped` statement (same addon-ARN scoping as
  `eks:DescribeAddon`).
  
- EKS add-on install action (#119, PR-2 of three). New
  `POST /api/clusters/{c}/eks/addons` wires `eks:CreateAddon`;
  body whitelist-validates `resolveConflicts ∈ {NONE, OVERWRITE,
  PRESERVE}` server-side and forwards `configurationValues` /
  `serviceAccountRoleArn` verbatim. Returns 202 with the addon
  detail in status `CREATING`; the SPA polls `/eks/addons/{name}`
  to watch the flip via status-aware refetch in `useAddons` /
  `useAddon` (4s interval while any addon is in
  `CREATING`/`UPDATING`/`DELETING`, off otherwise). New
  `InstallAddOnDialog` component opens from the catalog page's
  "+ Install" button — schema-aware via the existing
  `HelmValuesEditor` (form when AWS ships a JSON Schema for the
  version, Monaco YAML when it doesn't). New
  `GET /api/clusters/{c}/eks/addons/catalog/{name}/configuration?version=X`
  fetches the schema lazily; 24 h cache keyed by `(addon, version)`
  since AWS schemas are immutable per version. Audit pair
  `eks_addon_install_intent` + `eks_addon_install` mirrors the
  workload-rollback shape (intent before the SDK call, outcome
  after — denial / failure / success). The new IAM action
  `eks:CreateAddon` joins the cluster-scoped statement; optional
  `iam:PassRole` documented as a conditional add-on for operators
  who set `serviceAccountRoleArn`.
  
- EKS add-on catalog (#119, PR-1 of three). New
  `GET /api/clusters/{c}/eks/addons/catalog` returns every
  AWS-published add-on available on the cluster's K8s version, with
  per-addon ownership / type / publisher / marketplace flag /
  compatibility matrix and installed-state annotation merged from
  the existing `/eks/addons` cache. New `Add-ons catalog` sidebar
  entry under EKS opens the browse page; filter chips narrow by
  AWS / third-party / type. One unfiltered
  `eks:DescribeAddonVersions` call per `(k8sVersion)` drives the
  endpoint; cached server-side for 6 h with sticky errors so a
  fleet of N 1.30 clusters hits AWS once per cache window.
  Read-only in this PR; install / upgrade / delete actions ship in
  follow-up PRs.
  
- Helm install-ref pre-fill on the upgrade dialog (#76 follow-up). On
  successful install / upgrade actions, Periscope patches the helm
  release storage Secret/ConfigMap with two annotations —
  `periscope.io/install-ref` and `periscope.io/install-chart-name` —
  capturing the chart ref + chart name the operator originally used.
  The release detail endpoint reads them back and exposes
  `installRef` / `installChartName` on `HelmReleaseDetail`. The
  upgrade dialog uses these to pre-fill, closing the "why doesn't
  this remember anything" UX gap (helm itself doesn't persist the
  install ref). Best-effort writes on the action path —
  annotation-write failure does not fail the install/upgrade. Read
  failures fall back to empty fields. **Limitation**: only releases
  installed or upgraded via Periscope carry the annotations —
  releases installed via the helm CLI directly still require
  pasting the ref on first Periscope upgrade; subsequent upgrades
  pre-fill once the annotation is on the new revision.

- Helm release uninstall, end-to-end (#123, sub-task of #72). New
  `DELETE /api/clusters/{c}/helm/releases/{ns}/{name}` endpoint with
  `?keepHistory=true|false` and `?disableHooks=true|false` query
  flags (both default false). Sync, with the same pre-flight SAR
  pattern from #76 — verb=`delete` against each kind in the
  current release's manifest list, denials short-circuit with
  403 + inline list. Audit emits a `helm_uninstall_intent` +
  `helm_uninstall` pre/post pair; outcome row carries the
  `revisionsRemoved` count. SPA gains a red `[uninstall]` button
  next to `[upgrade]` on the release detail page header, opening
  a type-the-name confirmation modal (mirrors `DeleteResourceModal`'s
  destructive-action friction pattern) with checkboxes for the two
  flags. On success the SPA toasts the revision count and
  navigates back to the release list.

- Helm install + upgrade actions, end-to-end (#76, sub-task of #72). Two
  new endpoints — `POST /api/clusters/{c}/helm/install` and
  `POST /api/clusters/{c}/helm/releases/{ns}/{name}/upgrade` — and the
  full SPA UI to drive them. Both are sync (handler blocks until the
  helm SDK call returns; default 5min timeout, capped at 10min server-
  side) with `Atomic=true` by default — failed installs / upgrades
  auto-rollback so no half-deployed state lingers. Pre-flight SAR runs
  before the SDK call; pre-flight denial returns 403 with the denied
  list inline (`E_HELM_PREFLIGHT_DENIED`). Audit emits an intent +
  outcome pair per call (`helm_install_intent` + `helm_install`,
  `helm_upgrade_intent` + `helm_upgrade`) so hung / partitioned
  operations still leave a forensic trail. Outcome rows carry the new
  revision number and the `rolledBack` flag when Atomic caught a
  partial failure. The SPA wires both flows into a unified
  `ChartActionDialog` with a `mode` prop (replaces the earlier
  install-only `HelmInstallDialog` from #74) — install via the
  releases-list page, upgrade via a new `[Upgrade]` button on the
  release detail page header. The dialog gains a collapsible Preview
  pane that calls the #75 preview endpoints and surfaces the rendered
  manifests + RBAC denial list + (upgrade) diff inline before commit.
  Release detail pages gain a `notes` tab alongside values / manifest /
  history that renders the chart's NOTES.txt.

- Helm dry-run + diff preview backend (#75, sub-task of #72). Two new
  endpoints — `POST /api/clusters/{c}/helm/install-preview` and
  `POST /api/clusters/{c}/helm/releases/{ns}/{name}/upgrade-preview` —
  return the rendered manifest list helm would apply, plus (for upgrade
  mode) a semantic diff against the live cluster state via the existing
  dyff helper. Both endpoints run a per-manifest RBAC pre-flight (verb
  `create` for install, `patch` for upgrade) and surface the denied
  list inline so the install dialog can show the operator exactly what
  the apiserver would reject before they hit Apply. Audit emits one
  `verb=helm_preview` row per call with `op` distinguishing install
  vs upgrade in `Extra`; pre-flight denials mark the row
  `OutcomeDenied`. **This PR introduces `helm.sh/helm/v3` to the
  project** as the foundation for write-path features (preview now,
  rollback / install / upgrade later). The boundary is documented in
  ``internal/k8s/helm.go``'s preamble: read paths use minimal
  kubectl-free decoders; write paths use `pkg/action` directly because
  the helm internals it wraps (templating, hook ordering, capabilities
  resolution, post-rendering) are non-trivial to reimplement.

- Helm chart fetch backend (#73, sub-task of #72). Two new endpoints
  `GET /api/clusters/{c}/helm/chart/versions` and
  `POST /api/clusters/{c}/helm/chart/values` for the Helm install
  dialog's version picker + values loader. Supports public HTTP chart
  repos (decode `index.yaml` ourselves) and public OCI refs (via
  `oras.land/oras-go/v2`). Both deps are kubectl-free, preserving the
  v1.0 release-decoder philosophy of isolating Periscope from helm
  SDK transitive churn. Rejects charts with sub-chart dependencies
  with a structured `422 unsupported_dependencies` error. OCI tag
  listing is pinned: media-type filter
  (`application/vnd.cncf.helm.config.v1+json`), semver sort, 50-cap.
  Two independent server-side caches with `?nocache=true` bypass.
  Audit emits one `verb=helm_chart_fetch` row per `/values` call;
  `/versions` is silent (called while typing). v1.1 ships
  unauthenticated public refs only — private OCI auth (ECR via Pod
  Identity / IRSA) is a follow-up sub-task. Frontend types + API
  client + TanStack hooks ship alongside the backend; the install-
  dialog UI lands in a sibling issue.
  
- Helm install dialog UI (#74, sub-task of #72). New "+ install
  chart" button in the Helm releases page header opens a modal with
  a single-pane top-to-bottom flow: chart-ref input → fetch
  versions → version dropdown → load values → schema-aware editor.
  When the chart ships `values.schema.json`, renders a structured
  form with live `ajv`-backed validation (required fields, enums,
  type / range / pattern / length); when not, falls back to a
  Monaco YAML editor. Schema features the form can't render
  cleanly ($ref / allOf / anyOf / oneOf / arrays-of-objects) are
  surfaced as "edit in YAML mode" hints rather than silently
  ignored. `Install` and `Dry-run preview` footer buttons are
  disabled stubs — those backends are sibling issues under the
  epic. Rendering only; no install/upgrade actions yet.

### Security

- Helm chart fetch endpoints reject SSRF attempts at dial time
  (#73). The HTTP / OCI clients now run a `net.Dialer.Control`
  callback that validates the resolved IP against blocklists:
  - **Always blocked:** AWS IMDS (`169.254.169.254`), all
    link-local (covers IPv6 IMDS + `fe80::/10`).
  - **Blocked by default, env-var opt-in:** RFC1918 + IPv6 ULA
    via `PERISCOPE_HELM_FETCH_ALLOW_PRIVATE=true` for operators
    running internal chart repos.
  - **Loopback** stays blocked even with the opt-in flag (no
    legitimate chart-repo reason for chart-fetch from a Periscope
    pod to reach localhost).
  Caught by CodeQL on PR #106 before merge.

### Fixed

- IAM policy snippet in `docs/setup/deploy.md` 4.1 and
  `docs/setup/eks-upgrade-readiness.md` was incomplete: it grouped
  `eks:DescribeNodegroup` with the cluster-scoped EKS actions under
  `Resource: arn:aws:eks:*:<account>:cluster/*`. AWS scopes
  `eks:DescribeNodegroup` to the **nodegroup** resource
  (`arn:aws:eks:region:account:nodegroup/cluster-name/nodegroup-name/uuid`),
  not the cluster — so operators following the doc literally got
  `AccessDenied` on the nodegroup detail / AMI drift endpoints
  even though the list endpoint worked. Split the policy into two
  statements (cluster-scoped and nodegroup-scoped), added a "Resource
  type" column to the action table, and called out the gotcha
  inline so future readers do not reintroduce it.

- EKS Upgrade Insights and Node Groups surfaces now work on
  `in-cluster`, `agent`, and `kubeconfig` backends when the cluster
  entry has both `arn` and `region` set. Before this fix, the
  surfaces 422'd on any non-`eks` backend regardless of ARN, so an
  operator running Periscope inside an EKS cluster (`backend:
  in-cluster`, ARN configured for AWS-side queries) saw "this
  cluster is not backed by EKS" instead of the actual insights.
  The K8s-auth backend and the AWS-side EKS metadata are now
  treated as orthogonal, with the same field validation
  (`arn` + `region` together, ARN parseable to `:cluster/<name>`)
  applied uniformly. Surfaced via a new `Cluster.EKSCapable()`
  method; registry validation rejects mismatched configurations
  (ARN without region, malformed ARN) at startup.

## [1.0.3-rc1] - 2026-05-06

### Added

- **Apply YAML — multi-doc paste / upload, dry-run, server-side apply,
  per-doc RBAC pre-flight, audit** (#53, #54, #55). New SPA dialog
  reachable from the page header, the cluster sidebar, the cluster
  overview banner, and the Cmd+K palette. Drag-drop or paste any number
  of K8s manifests, get a dry-run + diff before commit, then server-side
  apply with field-ownership glyphs. Per-doc `SelfSubjectAccessReview`
  pre-flight blocks docs the user can't write rather than failing
  mid-stream. Each apply emits one structured audit row per doc with
  the kind/namespace/name/operation tuple.

- **EKS Upgrade Insights viewer** (#103). New read-only surface wrapping
  AWS EKS `ListInsights` / `DescribeInsight` (UPGRADE_READINESS
  category). Worst-first insight rows on a dedicated page, expandable
  detail with deprecated-API summaries, and per-resource deep links
  that open the SPA's existing YAML editor on the affected object.
  Cluster-keyed cache, 1h TTL (AWS itself only refreshes daily).
  Non-EKS clusters return 422 + stable code `E_BACKEND_NOT_EKS` so the
  UI renders a calm note instead of a generic error. New audit verb
  `eks_insights_read` (Periscope's first read verb — added at
  compliance reviewers' request for upgrade-readiness traceability).

- **EKS managed node groups + AMI drift detection** (#103). New
  surface listing managed node groups with current AMI release version
  and days-behind-latest drift. Latest-AMI lookup uses SSM public
  parameters (`/aws/service/eks/optimized-ami/...`,
  `/aws/service/bottlerocket/...`) as the primary source and
  `ec2:DescribeImages` as a fallback when SSM is denied / unavailable.
  Custom-AMI node groups (`AmiType=CUSTOM`) are explicitly badged "not
  tracked" — AWS does not publish a "latest" for custom images.
  Shared `(amiType, k8sVersion)` AMI cache (30 min TTL) so a fleet view
  of N nodegroups makes 1 SSM call per family per half-hour. New audit
  verb `eks_nodegroups_read`.

- **Workload rollback** for Deployment / StatefulSet / DaemonSet
  (#71). Revision picker with Monaco YAML diff preview of the current
  pod template vs the target revision. Mirrors `kubectl rollout undo`
  — strategic-merge-patches `spec.template` and writes the
  `kubernetes.io/change-cause` annotation. Pre-flight warnings cover
  the three production footguns: GitOps-managed workloads (ArgoCD /
  Helm / Flux annotations or labels) get a yellow banner warning that
  reconcile will revert the rollback; paused Deployments get a
  "resume rollout" pane instead of the picker; HPA-targeted workloads
  get an inline note. Optional reason field flows into both the
  change-cause annotation and the structured audit row. New API
  endpoints `GET /revisions` (history + pre-flight metadata) and
  `POST /rollback` (the patch); two new audit verbs `rollback_intent`
  (pre-patch) + `rollback` (post-outcome) so incident review captures
  attempts that hang or fail mid-flight. See
  [`docs/setup/workload-rollback.md`](docs/setup/workload-rollback.md).

- SSE watch streams for ConfigMaps, ResourceQuotas, LimitRanges, and
  ServiceAccounts (#17).

### Changed

- AWS SDK errors are now classified by `smithy.APIError` code and
  surfaced with meaningful HTTP statuses + stable error codes
  (`E_AWS_FORBIDDEN` 403, `E_AWS_NOT_FOUND` 404, `E_AWS_THROTTLED`
  429) instead of always collapsing to `502 / E_AWS_API`. The SPA's
  Upgrade Readiness and Node Groups pages branch on these codes and
  render permission-specific or rate-limit copy. Anything
  unrecognized still reads as `502 / E_AWS_API` so existing callers
  stay compatible.

- Helm `values.schema.json` now strictly validates
  `watchStreams.kinds`; deployments with typos that previously
  silently dropped now fail at helm install time.

### Fixed

- NamespacePicker dropdown was anchored to the button's left edge and
  clipped off the right of the viewport when used in the page
  header's trailing slot. The picker is also no longer covered by the
  FilterStrip — `PageHeader` and `FilterStrip` both opened
  `backdrop-blur` stacking contexts at z-20, so the picker's inner
  z-30 only applied within the header bottle. Header bumped to z-30;
  modal/drawer overlays at z-40+ still win. (#111)

- NamespacePicker on clusters with 50+ namespaces was tedious to
  scan: added a sticky search input (autofocus on open, case-
  insensitive substring), bumped the panel max height from 320px to
  `min(70vh, 520px)` so larger lists no longer require dozens of
  scrolls. (#111)

### Upgrading

If you plan to use the new EKS Upgrade Insights or Node Groups
features, extend Periscope's AWS role with the following IAM actions
(scoped as shown):

- `eks:ListInsights`, `eks:DescribeInsight`
- `eks:ListNodegroups`, `eks:DescribeNodegroup`
- `ssm:GetParameter` (resource: `arn:aws:ssm:*::parameter/aws/service/eks/*`
  and `arn:aws:ssm:*::parameter/aws/service/bottlerocket/*`)
- `ec2:DescribeImages` (resource: `*` — the API has no per-image ARN)

The full IAM policy snippet is in
[`docs/setup/deploy.md` 4.1](docs/setup/deploy.md). The Helm chart
itself does not change; non-EKS clusters and existing features
continue to work without these additions.
## [1.0.0]

Initial stable release.

### Added

- **Authentication & access**
  - OIDC user authentication (Auth0 and Okta tested) with PKCE,
    state validation, and HttpOnly / Secure / SameSite session
    cookies.
  - Per-cluster RBAC enforced via `Impersonate-User` /
    `Impersonate-Group` headers — every K8s call carries the human
    user's identity.
  - Three authorization modes: `shared`, `tier`, `raw` — operator
    chooses how IdP groups map to in-cluster identity.
  - Pre-flight RBAC checks (SAR / SSRR) so disabled actions in the
    UI explain themselves instead of failing on click.
  - Pod Identity / IRSA factory for AWS access — no static AWS
    credentials on the pod.

- **Multi-cluster**
  - Fleet view aggregator at `/` over every registered cluster.
  - Cluster rail (left bar) for context switching.
  - Per-cluster scoping for every resource view.
  - In-cluster cluster backend for self-managed deployments — the
    chart auto-binds the periscope ServiceAccount to the
    impersonator role when a cluster is registered with
    `backend: in-cluster`.
  - Agent backend (#42) — per-cluster `periscope-agent` pod
    dials out to the central server over a long-lived mTLS-pinned
    WebSocket. Adds managed clusters via one `helm install` on the
    target cluster; works on EKS / GKE / AKS / on-prem k3s / kind,
    no IAM trust per cluster. PKI bootstrapped at server startup
    (per-deployment ECDSA P-256 CA in a K8s Secret); 15-min single-
    use bootstrap tokens; 90-day rotating client certs.
  - SPA "+ onboard cluster" button (admin-tier only) on the fleet
    page mints a token and renders the helm install command with the
    token baked in, copy-paste ready.
  - **Pod exec on agent-managed clusters** (#43, collapses into
    #42 per RFC 0004 10). client-go's WebSocket and SPDY exec
    executors bypass `rest.Config.Transport`, so a loopback HTTP
    CONNECT proxy in `internal/k8s/agent_exec_proxy.go` translates
    per-cluster CONNECTs into tunnel dials. The agent's reverse
    proxy implements `http.Hijacker` so the WS / SPDY upgrade
    succeeds. Validation in `internal/k8s/exec_tunnel_test.go`
    (Tier 1 in-process) + `hack/poc-exec-tunnel/` (Tier 2 kind e2e).
  - Agent-side per-connection idle timeout
    (`agent.execIdleSeconds`, default `600`) for hijacked exec
    WS / SPDY streams. Defense-in-depth so a stuck exec stream gets
    reaped on the agent side if the server crashes / partitions
    mid-session, even when the server-side cascade close doesn't
    fire. Activity = any successful read; only idle streams are
    killed. `0` disables.

- **Browsing & inspection**
  - List, detail, describe, events, and YAML for the common
    workload, networking, storage, RBAC, and config kinds.
  - Full Custom Resource catalog driven by `/openapi/v3`.
  - Live pod logs with follow + filtering.
  - In-browser pod shell (`exec`) with reconnect on transient
    disconnects, audited open / close events.
  - `Cmd+K` palette for cluster-wide name search.

- **Real-time updates (watch streams)**
  - 21+ resource kinds streamed over SSE (workloads, networking,
    storage, cluster-scoped) with a polling fallback.
  - `Last-Event-ID` resume on transient disconnects.
  - Per-user concurrency cap (`PERISCOPE_WATCH_PER_USER_LIMIT`,
    default 60) to protect apiserver watch quota.
  - Operator opt-out via Helm: subset, group aliases (`workloads`,
    `networking`, `storage`, `cluster`, `core`), or full disable.

- **Editing**
  - Inline Monaco YAML editor for built-in kinds and CRDs.
  - Schema-aware autocomplete and validation against the cluster's
    `/openapi/v3`.
  - Server-side apply with minimal diffs and field-ownership glyphs.
  - Per-field conflict resolution and live drift detection.
  - Unsaved-changes guards on refresh / nav / row-click.

- **Helm**
  - Read-only release browser per cluster — values, manifest,
    history, and `dyff`-based diff between revisions.
  - Auto-probes Secret vs ConfigMap storage drivers per cluster.
  - Bounded TTL cache for release listings.

- **Audit & observability**
  - Persistent SQLite audit sink with retention / size caps and
    a fail-open boot path (warn, continue with stdout-only).
  - First-class in-app audit view with filters by actor, verb,
    outcome, time range, namespace, request id; density timeline.
  - Tier-mode audit-admin groups see every actor's rows; everyone
    else sees their own.
  - Structured JSON events also stream to stdout for shipping to
    CloudWatch / Loki / OpenSearch / Datadog.

- **Packaging & supply chain**
  - Multi-arch container image (`linux/amd64`, `linux/arm64`)
    published to `ghcr.io/gnana997/periscope`.
  - Helm chart published to `ghcr.io/gnana997/charts/periscope`
    as an OCI artifact, discoverable on Artifact Hub.
  - Cosign keyless signatures (Sigstore) for both the image and
    the chart; SPDX SBOM attached to the image.
  - Distroless static base, non-root UID 65532, read-only root
    filesystem, all capabilities dropped, `RuntimeDefault`
    seccomp profile in the Helm chart.

### Fixed

- LogStream component no longer hits an infinite render loop when
  toggling wrap mode (#66).
- Auth: `periscope_session` cookie is now `SameSite=Lax` (was
  `Strict`). Strict suppressed the cookie on the post-OIDC-callback
  redirect to `/`, so first-time sign-in landed on the
  unauthenticated page until the user manually refreshed (#37).
- Auth: browser navigations to `/` (or any deep link) without a
  session now `302` to `/api/auth/login` instead of returning plain
  `401 unauthenticated` — XHR callers still get the 401 (#37).
- Fixed stale `PERISCOPE_WATCH_PER_USER_LIMIT` default in
  `docs/architecture/watch-streams.md` (was 30, code is 60).

### Security

- OIDC session and PKCE/state generation now propagate `crypto/rand`
  failures as errors instead of panicking the pod (#35). Login
  callbacks return 500 on the (vanishingly rare) RNG failure path
  rather than crashing the process and dropping every active
  session on the same replica.

### Documentation

- Added [`docs/architecture/README.md`](docs/architecture/README.md) —
  top-level architecture overview: component map, source-tree
  guide, suggested reading order for new contributors, and
  cross-cutting design choices (single binary + embedded SPA,
  stateless w.r.t. credentials, impersonation everywhere,
  pre-flight RBAC, audit-before-action).
- Added [RFC 0003 — Audit log: schema and retention semantics](docs/rfcs/0003-audit-log.md),
  formalizing the verb taxonomy, wire-stable event shape, SQLite
  schema, retention algorithm, `/api/audit` read-side RBAC, semver
  coverage, and the v1.0 security model (operator-trust now;
  hash-chain signing in v2).
- Added [RFC 0004 — Exec over the agent tunnel](docs/rfcs/0004-exec-over-agent-tunnel-poc.md) —
  design + findings for the loopback CONNECT proxy and agent
  Hijack shim. Status stamped as "Implemented in v1.0.0."
- Added [`docs/api.md`](docs/api.md) — HTTP API reference with
  three stability tiers (Tier 1 stable, Tier 2 SPA-coupled,
  Tier 3 live channels), authentication / cookie / session
  contract, error-code enum, CSRF posture, and the
  `/api/v2/...` versioning policy for future majors. Includes
  the three agent-backend endpoints (`POST /api/agents/tokens`
  admin-only, `POST /api/agents/register` unauth + token-gated,
  `WS /api/agents/connect` mTLS-required), with the `/register`
  description tightened to clarify "before the agent has obtained
  its long-lived mTLS identity" rather than the ambiguous "does
  not yet."
- Added [`docs/setup/values.md`](docs/setup/values.md) — flat
  reference for every value in the periscope and periscope-agent
  Helm charts, organised by section, with type / default / notes
  per field. Single page operators can grep during a `helm upgrade`.
- Added [`docs/setup/environment-variables.md`](docs/setup/environment-variables.md) —
  centralized reference for every `PERISCOPE_*` env var (and
  `PORT`) the binary reads, with defaults, Helm-value mapping,
  and the semver coverage rules for the configuration surface.
  Covers the two server-side and six agent-side env vars
  introduced by #42.
- Added [`docs/architecture/agent-tunnel.md`](docs/architecture/agent-tunnel.md) —
  design walkthrough for the agent backend: topology, PKI lifecycle,
  registration handshake, mTLS session lifecycle, the
  `rest.Config.Transport` substitution that keeps existing handlers
  unchanged, identity propagation, audit shape, and failure modes.
- Added [`docs/setup/agent-onboarding.md`](docs/setup/agent-onboarding.md) —
  operator how-to for registering a managed cluster via the agent
  backend: same-account flow with prereqs, the 5-step register-
  install-verify sequence, troubleshooting (mTLS handshake, token
  expiry, SAN mismatch), security notes, cross-account note.
- Added [`examples/agent/`](examples/agent/) — sample values files
  for both server + agent charts and a reference
  `register-and-install.sh` script.
- Extended [`docs/setup/pod-exec.md`](docs/setup/pod-exec.md) with a
  dedicated "Operator notes for agent-backed clusters" section
  (transport path, RBAC, audit, latency, disconnect behavior) and
  an agent-specific troubleshooting bullet for the
  `cmd/periscope-agent/observability.go` Hijack shim regression.
- Extended [`docs/setup/cluster-rbac.md`](docs/setup/cluster-rbac.md)
  with the agent-backend RBAC story (the agent SA's impersonation
  lever, default ClusterRole shape, how to tighten).
- Normalized version nomenclature in operator-facing docs: `v1.x.0`
  / `v1.x.+` / `v1.x.1` collapsed to `v1.0` / `post-1.0` / `v1.x`
  for consistency.
- README: explicit note that pod exec works on every backend
  including `agent`; new top-level architecture-overview link.
- Added GitHub issue templates (`bug_report.yml`,
  `feature_request.yml`) and a pull-request template under
  `.github/`. Bug reports require backend, OIDC provider, and
  Periscope version up front; PR template prompts surfaces
  touched and a tested-paths summary.

[Unreleased]: https://github.com/gnana997/periscope/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/gnana997/periscope/releases/tag/v1.0.0
