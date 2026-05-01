import { Section, KV } from "../CustomResourceDescribe";
import { cn } from "../../../../lib/cn";
import type { CRDRendererProps } from "./index";

/**
 * argoproj.io/Application renderer.
 *
 * The whole point of an ArgoCD Application is "is it Synced and is it
 * Healthy?" We surface those at the top, then source/destination, then
 * the per-resource state table.
 */
export function Application({ obj }: CRDRendererProps) {
  const spec = (obj.spec ?? {}) as Record<string, unknown>;
  const status = (obj.status ?? {}) as Record<string, unknown>;
  const source = (spec.source ?? {}) as Record<string, unknown>;
  const sources = Array.isArray(spec.sources)
    ? (spec.sources as Array<Record<string, unknown>>)
    : [];
  const dest = (spec.destination ?? {}) as Record<string, unknown>;
  const sync = (status.sync ?? {}) as Record<string, unknown>;
  const health = (status.health ?? {}) as Record<string, unknown>;
  const operationState = (status.operationState ?? {}) as Record<string, unknown>;
  const resources = Array.isArray(status.resources)
    ? (status.resources as Array<Record<string, unknown>>)
    : [];

  const sourceList = sources.length > 0 ? sources : [source];

  return (
    <>
      <Section title="source">
        {sourceList.map((s, i) => (
          <div
            key={i}
            className={i > 0 ? "mt-2 border-t border-border/50 pt-2" : undefined}
          >
            {s.repoURL ? <KV k="repo" v={String(s.repoURL)} /> : null}
            {s.path ? <KV k="path" v={String(s.path)} /> : null}
            {s.chart ? <KV k="chart" v={String(s.chart)} /> : null}
            {s.targetRevision ? (
              <KV k="revision" v={String(s.targetRevision)} />
            ) : null}
            {s.ref ? <KV k="ref" v={String(s.ref)} /> : null}
          </div>
        ))}
      </Section>

      <Section title="destination">
        {dest.server ? <KV k="server" v={String(dest.server)} /> : null}
        {dest.name ? <KV k="cluster" v={String(dest.name)} /> : null}
        {dest.namespace ? (
          <KV k="namespace" v={String(dest.namespace)} />
        ) : null}
      </Section>

      {spec.project || spec.syncPolicy ? (
        <Section title="config">
          {spec.project ? <KV k="project" v={String(spec.project)} /> : null}
          {(() => {
            const sp = (spec.syncPolicy ?? {}) as Record<string, unknown>;
            const auto = sp.automated;
            if (auto === undefined) return null;
            const a = (auto ?? {}) as Record<string, unknown>;
            const flags: string[] = [];
            if (a.prune) flags.push("prune");
            if (a.selfHeal) flags.push("selfHeal");
            if (a.allowEmpty) flags.push("allowEmpty");
            return (
              <KV
                k="auto-sync"
                v={flags.length > 0 ? flags.join(", ") : "enabled"}
              />
            );
          })()}
        </Section>
      ) : null}

      {sync.status || health.status || sync.revision ? (
        <Section title="state">
          {sync.status ? (
            <KV k="sync" v={String(sync.status)} />
          ) : null}
          {health.status ? (
            <KV k="health" v={String(health.status)} />
          ) : null}
          {sync.revision ? (
            <KV k="revision" v={String(sync.revision).slice(0, 12)} />
          ) : null}
          {operationState.phase ? (
            <KV k="last op" v={String(operationState.phase)} />
          ) : null}
          {operationState.message ? (
            <KV k="last msg" v={String(operationState.message)} />
          ) : null}
        </Section>
      ) : null}

      {resources.length > 0 ? (
        <Section title={`resources (${resources.length})`}>
          <div className="overflow-x-auto">
            <table className="w-full text-[11.5px]">
              <thead>
                <tr className="text-left text-ink-faint">
                  <th className="pb-1 pr-3 font-normal">kind</th>
                  <th className="pb-1 pr-3 font-normal">name</th>
                  <th className="pb-1 pr-3 font-normal">ns</th>
                  <th className="pb-1 pr-3 font-normal">sync</th>
                  <th className="pb-1 font-normal">health</th>
                </tr>
              </thead>
              <tbody>
                {resources.map((r, i) => {
                  const h = (r.health ?? {}) as Record<string, unknown>;
                  return (
                    <tr key={i} className="border-t border-border/40">
                      <td className="py-1 pr-3 text-ink-muted">
                        {String(r.kind ?? "—")}
                      </td>
                      <td className="py-1 pr-3 text-ink">
                        {String(r.name ?? "—")}
                      </td>
                      <td className="py-1 pr-3 text-ink-muted">
                        {r.namespace ? String(r.namespace) : "—"}
                      </td>
                      <td
                        className={cn(
                          "py-1 pr-3",
                          syncCellTone(String(r.status ?? "")),
                        )}
                      >
                        {r.status ? String(r.status) : "—"}
                      </td>
                      <td className={cn("py-1", healthCellTone(String(h.status ?? "")))}>
                        {h.status ? String(h.status) : "—"}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </Section>
      ) : null}
    </>
  );
}

function syncCellTone(s: string): string {
  if (s === "Synced") return "text-green";
  if (s === "OutOfSync") return "text-yellow";
  return "text-ink-muted";
}

function healthCellTone(s: string): string {
  if (s === "Healthy") return "text-green";
  if (s === "Degraded") return "text-red";
  if (s === "Progressing") return "text-yellow";
  if (s === "Suspended") return "text-ink-muted";
  if (s === "Missing") return "text-ink-faint";
  return "text-ink-muted";
}
