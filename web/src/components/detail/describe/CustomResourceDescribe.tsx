import { useState } from "react";
import { Link } from "react-router-dom";
import { useCustomResourceDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { cn } from "../../../lib/cn";
import { DetailError, DetailLoading } from "../states";
import { getCRDRenderer } from "./crd-renderers";

/**
 * CustomResourceDescribe — generic describe panel for any CRD.
 *
 * The PR1 version of this component was a one-level KV dump with
 * "see yaml" placeholders for nested objects. That made operators
 * jump to the YAML tab even for simple inspections. This version
 * surfaces the most useful information inline, with five
 * improvements:
 *
 *   1. **Smart status badges** — well-known status paths
 *      (.status.phase, .status.url, .status.health.status,
 *      .status.sync.status, observedGeneration drift) float to a
 *      badge row at the top of the panel.
 *
 *   2. **OwnerReferences breadcrumbs** — when the CR is owned by
 *      another resource (Application → AppProject, etc.) we render a
 *      clickable chain so users can navigate up.
 *
 *   3. **Recursive collapsible JSON tree** for spec and status —
 *      every nested object/array is expandable inline. No more
 *      "see yaml" for everything beyond depth 1.
 *
 *   4. **Reference detection** — fields named `*Ref`, `secretName`,
 *      `serviceAccountName`, `configMapName`, etc. render as
 *      clickable links into the corresponding resource page.
 *
 *   5. **Conditions list** — `.status.conditions` is rendered as a
 *      structured list (matches our existing condition rendering).
 *
 * Each piece is optional — a Certificate without
 * `.status.conditions` simply skips the conditions section. The
 * fallback gracefully degrades to "what's actually in the object."
 */

interface Props {
  cluster: string;
  group: string;
  version: string;
  plural: string;
  namespace: string | null;
  name: string;
}

export function CustomResourceDescribe({
  cluster,
  group,
  version,
  plural,
  namespace,
  name,
}: Props) {
  const { data, isLoading, isError, error } = useCustomResourceDetail(
    cluster,
    group,
    version,
    plural,
    namespace,
    name,
  );
  if (isLoading) return <DetailLoading label="loading…" />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  const obj = data.object as Record<string, unknown>;
  const meta = (obj.metadata ?? {}) as Record<string, unknown>;
  const spec = (obj.spec ?? {}) as Record<string, unknown>;
  const status = (obj.status ?? {}) as Record<string, unknown>;

  const labels = (meta.labels ?? {}) as Record<string, string>;
  const annotations = (meta.annotations ?? {}) as Record<string, string>;
  const ownerRefs = Array.isArray(meta.ownerReferences)
    ? (meta.ownerReferences as Array<Record<string, unknown>>)
    : [];
  const conditions = Array.isArray(status.conditions)
    ? (status.conditions as Array<Record<string, unknown>>)
    : [];

  const badges = extractStatusBadges(obj);

  return (
    <div className="px-5 py-4 font-mono text-[12px]">
      {/* Owner-references breadcrumbs at the very top */}
      {ownerRefs.length > 0 && (
        <OwnerRefsBreadcrumbs
          owners={ownerRefs}
          cluster={cluster}
          namespace={data.namespace ?? null}
        />
      )}

      {/* Smart status badges row */}
      {badges.length > 0 && (
        <div className="mb-4 flex flex-wrap gap-2">
          {badges.map((b, i) => (
            <StatusBadge key={i} {...b} />
          ))}
        </div>
      )}

      <Section title="metadata">
        <KV k="kind" v={`${data.apiVersion}/${data.kind}`} />
        {data.namespace && <KV k="namespace" v={data.namespace} />}
        <KV k="age" v={ageFrom(data.createdAt)} />
        {meta.uid ? <KV k="uid" v={String(meta.uid)} /> : null}
        {typeof meta.generation === "number" && (
          <KV k="generation" v={String(meta.generation)} />
        )}
      </Section>

      {Object.keys(labels).length > 0 && (
        <Section title="labels">
          <Pills items={labels} />
        </Section>
      )}
      {Object.keys(annotations).length > 0 && (
        <Section title="annotations">
          <Pills items={annotations} />
        </Section>
      )}

      {conditions.length > 0 && (
        <Section title="conditions">
          <ConditionsList conditions={conditions} />
        </Section>
      )}

      {(() => {
        const specialized = getCRDRenderer(group, data.kind);
        if (specialized) {
          return specialized({
            obj,
            cluster,
            namespace: data.namespace ?? null,
          });
        }
        const rest = omitKey(status, "conditions");
        const restKeys = Object.keys(rest);
        return (
          <>
            {Object.keys(spec).length > 0 && (
              <Section title="spec">
                <JSONTree
                  value={spec}
                  cluster={cluster}
                  namespace={data.namespace ?? null}
                  initiallyOpen
                />
              </Section>
            )}
            {restKeys.length > 0 && (
              <Section title="status">
                <JSONTree
                  value={rest}
                  cluster={cluster}
                  namespace={data.namespace ?? null}
                  initiallyOpen
                />
              </Section>
            )}
          </>
        );
      })()}
    </div>
  );
}

// ---------------------------------------------------------------------
// Sections + simple components
// ---------------------------------------------------------------------

export function Section({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="mb-4">
      <h3 className="mb-2 text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint">
        {title}
      </h3>
      <div>{children}</div>
    </section>
  );
}

export function KV({ k, v }: { k: string; v: string }) {
  return (
    <div className="grid grid-cols-[110px_1fr] gap-3 py-0.5">
      <span className="text-ink-faint">{k}</span>
      <span className="break-all text-ink">{v}</span>
    </div>
  );
}

export function Pills({ items }: { items: Record<string, string> }) {
  return (
    <div className="grid grid-cols-1 gap-1.5 md:grid-cols-2">
      {Object.entries(items).map(([k, v]) => (
        <div
          key={k}
          className="flex min-w-0 items-center gap-1 rounded-md border border-border bg-surface-2/40 px-2 py-0.5 text-[11px]"
        >
          <span className="shrink-0 text-ink-muted">{k}</span>
          <span className="shrink-0 text-ink-faint">=</span>
          <span className="min-w-0 truncate text-ink" title={v}>
            {v}
          </span>
        </div>
      ))}
    </div>
  );
}

export function ConditionsList({
  conditions,
}: {
  conditions: Array<Record<string, unknown>>;
}) {
  return (
    <ul className="space-y-1.5">
      {conditions.map((c, i) => {
        const status = String(c.status ?? "");
        const ok = status === "True";
        const tone = ok
          ? "text-green"
          : status === "False"
            ? "text-yellow"
            : "text-ink-muted";
        const dot = ok
          ? "bg-green"
          : status === "False"
            ? "bg-yellow"
            : "bg-ink-faint";
        return (
          <li key={i}>
            <div className="flex items-baseline gap-2 text-[12px]">
              <span
                className={cn(
                  "mt-[3px] block size-1.5 shrink-0 self-center rounded-full",
                  dot,
                )}
              />
              <span className="text-ink">{String(c.type ?? "")}</span>
              {c.reason ? (
                <span className="text-ink-muted">· {String(c.reason)}</span>
              ) : null}
              <span className={cn("ml-auto", tone)}>{status}</span>
            </div>
            {c.message ? (
              <div className="ml-3.5 mt-0.5 break-words text-[11.5px] text-ink-muted">
                {String(c.message)}
              </div>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}

// ---------------------------------------------------------------------
// Smart status badges
// ---------------------------------------------------------------------

interface BadgeData {
  label: string;
  value: string;
  tone: "green" | "yellow" | "red" | "muted";
  href?: string;
}

export function StatusBadge({ label, value, tone, href }: BadgeData) {
  const surface =
    tone === "green"
      ? "border-green/40 bg-green-soft text-green"
      : tone === "yellow"
        ? "border-yellow/40 bg-yellow-soft text-yellow"
        : tone === "red"
          ? "border-red/40 bg-red-soft text-red"
          : "border-border bg-surface-2/40 text-ink-muted";
  const dot =
    tone === "green"
      ? "bg-green"
      : tone === "yellow"
        ? "bg-yellow"
        : tone === "red"
          ? "bg-red"
          : "bg-ink-faint/50";
  const inner = (
    <>
      <span aria-hidden className={cn("block size-1.5 rounded-full", dot)} />
      <span className="text-[10px] uppercase tracking-[0.08em] text-ink-faint">
        {label}
      </span>
      <span className="font-mono text-[11.5px]">{value}</span>
    </>
  );
  if (href) {
    return (
      <a
        href={href}
        target="_blank"
        rel="noreferrer noopener"
        className={cn(
          "flex items-center gap-1.5 rounded border px-2 py-1 transition-colors hover:underline",
          surface,
        )}
      >
        {inner}
      </a>
    );
  }
  return (
    <div
      className={cn(
        "flex items-center gap-1.5 rounded border px-2 py-1",
        surface,
      )}
    >
      {inner}
    </div>
  );
}

/**
 * Pattern-match well-known status paths and surface them as badges.
 * Falls through silently when nothing matches — generic CRDs still
 * get every other section.
 */
function extractStatusBadges(obj: Record<string, unknown>): BadgeData[] {
  const status = (obj.status ?? {}) as Record<string, unknown>;
  const meta = (obj.metadata ?? {}) as Record<string, unknown>;
  const out: BadgeData[] = [];

  // Generic Ready condition (cert-manager Certificate, many controllers)
  if (Array.isArray(status.conditions)) {
    const conds = status.conditions as Array<Record<string, unknown>>;
    const ready = conds.find((c) => c.type === "Ready");
    if (ready) {
      const ok = String(ready.status) === "True";
      out.push({
        label: "Ready",
        value: String(ready.status),
        tone: ok ? "green" : "yellow",
      });
    }
  }

  // .status.phase — common for batch-y workflows (Order, Job-likes)
  if (typeof status.phase === "string") {
    out.push({
      label: "Phase",
      value: status.phase,
      tone: phaseTone(status.phase),
    });
  }

  // .status.health.status — ArgoCD Application
  const health = (status.health ?? {}) as Record<string, unknown>;
  if (typeof health.status === "string") {
    out.push({
      label: "Health",
      value: health.status,
      tone: healthTone(health.status),
    });
  }

  // .status.sync.status — ArgoCD Application
  const sync = (status.sync ?? {}) as Record<string, unknown>;
  if (typeof sync.status === "string") {
    out.push({
      label: "Sync",
      value: sync.status,
      tone: syncTone(sync.status),
    });
  }

  // .status.url — knative Service, ingress, etc.
  if (typeof status.url === "string" && /^https?:\/\//.test(status.url)) {
    out.push({
      label: "URL",
      value: status.url,
      tone: "muted",
      href: status.url,
    });
  }

  // observedGeneration vs metadata.generation — drift indicator
  if (
    typeof status.observedGeneration === "number" &&
    typeof meta.generation === "number"
  ) {
    const drift = status.observedGeneration < meta.generation;
    out.push({
      label: "Generation",
      value: drift
        ? `drift (${status.observedGeneration} → ${meta.generation})`
        : `up to date (${meta.generation})`,
      tone: drift ? "yellow" : "green",
    });
  }

  return out;
}

function phaseTone(p: string): BadgeData["tone"] {
  switch (p) {
    case "Ready":
    case "Active":
    case "Valid":
    case "Succeeded":
      return "green";
    case "Pending":
    case "Issuing":
    case "Processing":
      return "yellow";
    case "Failed":
    case "Invalid":
    case "Errored":
      return "red";
    default:
      return "muted";
  }
}

function healthTone(s: string): BadgeData["tone"] {
  switch (s) {
    case "Healthy":
      return "green";
    case "Progressing":
    case "Suspended":
      return "yellow";
    case "Degraded":
    case "Missing":
      return "red";
    default:
      return "muted";
  }
}

function syncTone(s: string): BadgeData["tone"] {
  switch (s) {
    case "Synced":
      return "green";
    case "OutOfSync":
      return "yellow";
    case "Unknown":
      return "muted";
    default:
      return "muted";
  }
}

// ---------------------------------------------------------------------
// OwnerReferences breadcrumbs
// ---------------------------------------------------------------------

function OwnerRefsBreadcrumbs({
  owners,
  cluster,
  namespace,
}: {
  owners: Array<Record<string, unknown>>;
  cluster: string;
  namespace: string | null;
}) {
  if (owners.length === 0) return null;
  return (
    <div className="mb-4 flex flex-wrap items-center gap-x-2 gap-y-1 rounded-md border border-border bg-surface-2/40 px-3 py-1.5 text-[11px]">
      <span className="text-ink-faint">owned by</span>
      {owners.map((o, i) => {
        const kind = String(o.kind ?? "");
        const name = String(o.name ?? "");
        const apiVersion = String(o.apiVersion ?? "");
        const href = ownerHref(cluster, apiVersion, kind, namespace, name);
        return (
          <span key={i} className="flex items-center gap-1.5">
            <span className="text-ink-muted">{kind}</span>
            {href ? (
              <Link
                to={href}
                className="font-mono text-ink hover:text-accent hover:underline"
              >
                {name}
              </Link>
            ) : (
              <span className="font-mono text-ink">{name}</span>
            )}
            {i < owners.length - 1 && (
              <span className="text-ink-faint">·</span>
            )}
          </span>
        );
      })}
    </div>
  );
}

function ownerHref(
  cluster: string,
  apiVersion: string,
  kind: string,
  namespace: string | null,
  name: string,
): string | null {
  // Built-in resources: route to their dedicated pages.
  const builtin = builtinHref(cluster, kind, namespace, name);
  if (builtin) return builtin;

  // CRDs: route to /customresources/{group}/{version}/{plural}
  if (!apiVersion.includes("/")) return null;
  const [group, version] = apiVersion.split("/");
  if (!group || !version) return null;
  // We can't trivially derive plural from kind without the CRD object;
  // best-effort lowercase-pluralize. Most kinds follow simple rules.
  const plural = guessPlural(kind);
  const c = encodeURIComponent(cluster);
  const ns = encodeURIComponent(namespace ?? "_");
  return `/clusters/${c}/customresources/${encodeURIComponent(group)}/${encodeURIComponent(version)}/${encodeURIComponent(plural)}/${ns}/${encodeURIComponent(name)}`;
}

function builtinHref(
  cluster: string,
  kind: string,
  namespace: string | null,
  name: string,
): string | null {
  const c = encodeURIComponent(cluster);
  const ns = encodeURIComponent(namespace ?? "");
  const n = encodeURIComponent(name);
  switch (kind) {
    case "Pod":
      return `/clusters/${c}/pods?selNs=${ns}&sel=${n}&tab=describe`;
    case "Deployment":
      return `/clusters/${c}/deployments?selNs=${ns}&sel=${n}&tab=describe`;
    case "ReplicaSet":
      return `/clusters/${c}/replicasets?selNs=${ns}&sel=${n}&tab=describe`;
    case "StatefulSet":
      return `/clusters/${c}/statefulsets?selNs=${ns}&sel=${n}&tab=describe`;
    case "DaemonSet":
      return `/clusters/${c}/daemonsets?selNs=${ns}&sel=${n}&tab=describe`;
    case "Job":
      return `/clusters/${c}/jobs?selNs=${ns}&sel=${n}&tab=describe`;
    case "CronJob":
      return `/clusters/${c}/cronjobs?selNs=${ns}&sel=${n}&tab=describe`;
    case "Service":
      return `/clusters/${c}/services?selNs=${ns}&sel=${n}&tab=describe`;
    case "ConfigMap":
      return `/clusters/${c}/configmaps?selNs=${ns}&sel=${n}&tab=describe`;
    case "Secret":
      return `/clusters/${c}/secrets?selNs=${ns}&sel=${n}&tab=describe`;
    case "ServiceAccount":
      return `/clusters/${c}/serviceaccounts?selNs=${ns}&sel=${n}&tab=describe`;
    case "PersistentVolumeClaim":
      return `/clusters/${c}/pvcs?selNs=${ns}&sel=${n}&tab=describe`;
    case "PersistentVolume":
      return `/clusters/${c}/pvs?sel=${n}&tab=describe`;
    case "Namespace":
      return `/clusters/${c}/namespaces?sel=${n}&tab=describe`;
    case "Node":
      return `/clusters/${c}/nodes?sel=${n}&tab=describe`;
    default:
      return null;
  }
}

/**
 * guessPlural — heuristic for common kind→plural mappings. Works for
 * simple cases (Certificate→certificates, ClusterIssuer→clusterissuers)
 * without doing the full apiserver discovery dance. When wrong, the
 * link 404s and the user uses the YAML tab — annoying but rare in
 * practice.
 */
function guessPlural(kind: string): string {
  const lower = kind.toLowerCase();
  // Common irregulars
  if (lower.endsWith("y")) return lower.slice(0, -1) + "ies";
  if (
    lower.endsWith("s") ||
    lower.endsWith("ss") ||
    lower.endsWith("sh") ||
    lower.endsWith("ch") ||
    lower.endsWith("x") ||
    lower.endsWith("z")
  ) {
    return lower + "es";
  }
  return lower + "s";
}

// ---------------------------------------------------------------------
// Reference detection
// ---------------------------------------------------------------------

/**
 * Heuristic reference detection. Returns the resolved link href when
 * a key+value pair looks like a Kubernetes resource reference, or
 * null otherwise.
 *
 * Two patterns:
 *   (a) Scalar-named reference: `secretName: "tls"` →
 *       /secrets?selNs=&sel=tls
 *   (b) Object reference: `issuerRef: { name, kind, group }` →
 *       /customresources/<group>/<version>/<plural>/...
 */
const SCALAR_REF_KEYS: Record<string, string> = {
  secretName: "secrets",
  serviceAccountName: "serviceaccounts",
  configMapName: "configmaps",
  podName: "pods",
  ingressClassName: "ingressclasses",
  storageClassName: "storageclasses",
  persistentVolumeClaimName: "pvcs",
  priorityClassName: "priorityclasses",
  runtimeClassName: "runtimeclasses",
};

function detectScalarRef(
  cluster: string,
  namespace: string | null,
  key: string,
  value: unknown,
): string | null {
  if (typeof value !== "string" || !value) return null;
  const route = SCALAR_REF_KEYS[key];
  if (!route) return null;
  const c = encodeURIComponent(cluster);
  const n = encodeURIComponent(value);
  // Cluster-scoped routes don't take selNs.
  if (
    route === "ingressclasses" ||
    route === "storageclasses" ||
    route === "priorityclasses" ||
    route === "runtimeclasses"
  ) {
    return `/clusters/${c}/${route}?sel=${n}&tab=describe`;
  }
  const ns = encodeURIComponent(namespace ?? "");
  return `/clusters/${c}/${route}?selNs=${ns}&sel=${n}&tab=describe`;
}

function detectObjectRef(
  cluster: string,
  namespace: string | null,
  key: string,
  obj: Record<string, unknown>,
): string | null {
  // Common shape: { name, kind?, group?, namespace? }
  const name = typeof obj.name === "string" ? obj.name : "";
  if (!name) return null;
  const kind = typeof obj.kind === "string" ? obj.kind : "";
  const group = typeof obj.group === "string" ? obj.group : "";
  const apiGroup = typeof obj.apiGroup === "string" ? obj.apiGroup : "";
  const refNamespace =
    typeof obj.namespace === "string" ? obj.namespace : namespace;
  if (!key.toLowerCase().includes("ref")) return null;

  if (kind) {
    const builtin = builtinHref(cluster, kind, refNamespace, name);
    if (builtin) return builtin;
    // CRD reference — best-effort to its catalog page since we don't
    // know the version here.
    const g = group || apiGroup;
    if (g) {
      const c = encodeURIComponent(cluster);
      // Without version we can't deep-link to the resource detail —
      // surface the catalog filtered by group instead.
      return `/clusters/${c}/crds?q=${encodeURIComponent(g)}`;
    }
  }
  return null;
}

// ---------------------------------------------------------------------
// Recursive JSON tree
// ---------------------------------------------------------------------

interface JSONTreeProps {
  value: unknown;
  cluster: string;
  namespace: string | null;
  /** Set on the top-level Section so the first level renders open. */
  initiallyOpen?: boolean;
  /** Internal: current key path for reference-detection key matching. */
  parentKey?: string;
  /** Internal: nesting depth, used for indent. */
  depth?: number;
}

const MAX_INLINE_STRING = 80;

export function JSONTree({
  value,
  cluster,
  namespace,
  initiallyOpen,
  parentKey = "",
  depth = 0,
}: JSONTreeProps) {
  // Scalars render inline.
  if (value === null || value === undefined) {
    return <span className="text-ink-faint">—</span>;
  }
  if (typeof value === "string") {
    if (value.length > MAX_INLINE_STRING) {
      return <LongString value={value} />;
    }
    return <span className="text-ink">{value}</span>;
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return <span className="text-ink">{String(value)}</span>;
  }

  // Arrays
  if (Array.isArray(value)) {
    return (
      <ArrayNode
        value={value}
        cluster={cluster}
        namespace={namespace}
        initiallyOpen={initiallyOpen}
        depth={depth}
      />
    );
  }

  // Objects
  if (typeof value === "object") {
    const entries = Object.entries(value as Record<string, unknown>);
    return (
      <ObjectNode
        entries={entries}
        cluster={cluster}
        namespace={namespace}
        parentKey={parentKey}
        initiallyOpen={initiallyOpen}
        depth={depth}
      />
    );
  }

  return <span className="text-ink-faint">—</span>;
}

function ObjectNode({
  entries,
  cluster,
  namespace,
  parentKey,
  initiallyOpen,
  depth,
}: {
  entries: Array<[string, unknown]>;
  cluster: string;
  namespace: string | null;
  parentKey: string;
  initiallyOpen?: boolean;
  depth: number;
}) {
  // At depth 0 we just inline the keys (no toggle). The Section
  // already provides the visual frame.
  if (depth === 0 || initiallyOpen) {
    return (
      <ul className="space-y-0.5">
        {entries.map(([k, v]) => (
          <ObjectRow
            key={k}
            k={k}
            v={v}
            cluster={cluster}
            namespace={namespace}
            depth={depth}
          />
        ))}
      </ul>
    );
  }

  // Nested objects start collapsed by default — operator can drill in.
  return <CollapsibleObject entries={entries} cluster={cluster} namespace={namespace} parentKey={parentKey} depth={depth} />;
}

function CollapsibleObject({
  entries,
  cluster,
  namespace,
  depth,
}: {
  entries: Array<[string, unknown]>;
  cluster: string;
  namespace: string | null;
  parentKey: string;
  depth: number;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="inline-flex items-center gap-1 text-ink-muted hover:text-ink"
      >
        <Chevron open={open} />
        <span className="text-[11.5px]">
          {`{${entries.length} ${entries.length === 1 ? "field" : "fields"}}`}
        </span>
      </button>
      {open && (
        <ul className="mt-0.5 space-y-0.5">
          {entries.map(([k, v]) => (
            <ObjectRow
              key={k}
              k={k}
              v={v}
              cluster={cluster}
              namespace={namespace}
              depth={depth + 1}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

function ObjectRow({
  k,
  v,
  cluster,
  namespace,
  depth,
}: {
  k: string;
  v: unknown;
  cluster: string;
  namespace: string | null;
  depth: number;
}) {
  const indent = depth * 12;

  // Reference detection: scalar
  const scalarHref =
    typeof v === "string" ? detectScalarRef(cluster, namespace, k, v) : null;
  if (scalarHref && typeof v === "string") {
    return (
      <li
        className="grid grid-cols-[140px_1fr] gap-3 py-0.5 text-[12px]"
        style={{ paddingLeft: indent }}
      >
        <span className="text-ink-faint">{k}</span>
        <Link
          to={scalarHref}
          className="text-accent hover:underline"
          title={`go to ${v}`}
        >
          {v}
        </Link>
      </li>
    );
  }

  // Reference detection: object form (issuerRef, etc.)
  if (
    v &&
    typeof v === "object" &&
    !Array.isArray(v) &&
    k.toLowerCase().includes("ref")
  ) {
    const refHref = detectObjectRef(
      cluster,
      namespace,
      k,
      v as Record<string, unknown>,
    );
    if (refHref) {
      const obj = v as Record<string, unknown>;
      return (
        <li
          className="grid grid-cols-[140px_1fr] gap-3 py-0.5 text-[12px]"
          style={{ paddingLeft: indent }}
        >
          <span className="text-ink-faint">{k}</span>
          <Link
            to={refHref}
            className="font-mono text-accent hover:underline"
          >
            {String(obj.kind ?? "")} {String(obj.name ?? "")}
          </Link>
        </li>
      );
    }
  }

  // Scalars — inline value
  if (
    v === null ||
    v === undefined ||
    typeof v === "string" ||
    typeof v === "number" ||
    typeof v === "boolean"
  ) {
    return (
      <li
        className="grid grid-cols-[140px_1fr] gap-3 py-0.5 text-[12px]"
        style={{ paddingLeft: indent }}
      >
        <span className="text-ink-faint">{k}</span>
        <span className="min-w-0 break-words text-ink">
          <JSONTree value={v} cluster={cluster} namespace={namespace} parentKey={k} depth={depth + 1} />
        </span>
      </li>
    );
  }

  // Nested array/object — render with toggle on the same line.
  return (
    <li
      className="py-0.5 text-[12px]"
      style={{ paddingLeft: indent }}
    >
      <div className="grid grid-cols-[140px_1fr] gap-3 items-start">
        <span className="text-ink-faint">{k}</span>
        <div className="min-w-0">
          <JSONTree value={v} cluster={cluster} namespace={namespace} parentKey={k} depth={depth + 1} />
        </div>
      </div>
    </li>
  );
}

function ArrayNode({
  value,
  cluster,
  namespace,
  initiallyOpen,
  depth,
}: {
  value: unknown[];
  cluster: string;
  namespace: string | null;
  initiallyOpen?: boolean;
  depth: number;
}) {
  const [open, setOpen] = useState(initiallyOpen ?? false);
  if (value.length === 0) {
    return <span className="text-ink-faint">[]</span>;
  }
  return (
    <div>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="inline-flex items-center gap-1 text-ink-muted hover:text-ink"
      >
        <Chevron open={open} />
        <span className="text-[11.5px]">
          {`[${value.length} ${value.length === 1 ? "item" : "items"}]`}
        </span>
      </button>
      {open && (
        <ul className="mt-0.5 space-y-0.5">
          {value.map((item, i) => (
            <li key={i} className="py-0.5 text-[12px]" style={{ paddingLeft: 12 }}>
              <div className="grid grid-cols-[40px_1fr] gap-3 items-start">
                <span className="text-ink-faint">[{i}]</span>
                <div className="min-w-0">
                  <JSONTree value={item} cluster={cluster} namespace={namespace} depth={depth + 1} />
                </div>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function Chevron({ open }: { open: boolean }) {
  return (
    <svg
      width="9"
      height="9"
      viewBox="0 0 11 11"
      aria-hidden
      className={cn(
        "transition-transform duration-150",
        open ? "rotate-90" : "rotate-0",
      )}
    >
      <path
        d="M3.5 2l4 3.5-4 3.5"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
        strokeLinejoin="round"
        fill="none"
      />
    </svg>
  );
}

function LongString({ value }: { value: string }) {
  const [open, setOpen] = useState(false);
  return (
    <span>
      {open ? (
        <>
          <span className="break-all text-ink">{value}</span>{" "}
          <button
            type="button"
            onClick={() => setOpen(false)}
            className="text-[10px] text-ink-faint hover:text-ink-muted"
          >
            (collapse)
          </button>
        </>
      ) : (
        <>
          <span className="text-ink">{value.slice(0, MAX_INLINE_STRING)}…</span>{" "}
          <button
            type="button"
            onClick={() => setOpen(true)}
            className="text-[10px] text-ink-faint hover:text-ink-muted"
          >
            (show {value.length} chars)
          </button>
        </>
      )}
    </span>
  );
}

// ---------------------------------------------------------------------
// Tiny helper
// ---------------------------------------------------------------------

function omitKey<K extends string>(
  obj: Record<string, unknown>,
  key: K,
): Record<string, unknown> {
  const rest = { ...obj };
  delete rest[key as string];
  return rest;
}
