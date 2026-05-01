import { Section, KV, Pills } from "../CustomResourceDescribe";
import type { CRDRendererProps } from "./index";

/**
 * monitoring.coreos.com/ServiceMonitor renderer.
 *
 * Two questions an SRE asks: "what's it scraping?" and "how often?"
 * Selector + endpoints answer both.
 */
export function ServiceMonitor({ obj }: CRDRendererProps) {
  const spec = (obj.spec ?? {}) as Record<string, unknown>;
  const selector = (spec.selector ?? {}) as Record<string, unknown>;
  const matchLabels = (selector.matchLabels ?? {}) as Record<string, string>;
  const matchExpressions = Array.isArray(selector.matchExpressions)
    ? (selector.matchExpressions as Array<Record<string, unknown>>)
    : [];
  const nsSelector = (spec.namespaceSelector ?? {}) as Record<string, unknown>;
  const endpoints = Array.isArray(spec.endpoints)
    ? (spec.endpoints as Array<Record<string, unknown>>)
    : [];

  const nsLabel = nsSelector.any
    ? "any"
    : Array.isArray(nsSelector.matchNames)
      ? (nsSelector.matchNames as string[]).join(", ")
      : "current namespace only";

  return (
    <>
      <Section title="selector">
        <KV k="namespaces" v={nsLabel} />
        {Object.keys(matchLabels).length > 0 ? (
          <div className="mt-2">
            <div className="mb-1 text-[10px] uppercase tracking-[0.08em] text-ink-faint">
              labels
            </div>
            <Pills items={matchLabels} />
          </div>
        ) : null}
        {matchExpressions.length > 0 ? (
          <div className="mt-2">
            <div className="mb-1 text-[10px] uppercase tracking-[0.08em] text-ink-faint">
              expressions
            </div>
            <ul className="space-y-0.5 font-mono text-[11.5px] text-ink">
              {matchExpressions.map((e, i) => {
                const vals = Array.isArray(e.values)
                  ? (e.values as string[]).join(",")
                  : "";
                return (
                  <li key={i}>
                    {String(e.key ?? "")} {String(e.operator ?? "")} {vals ? `[${vals}]` : ""}
                  </li>
                );
              })}
            </ul>
          </div>
        ) : null}
      </Section>

      {endpoints.length > 0 ? (
        <Section title={`endpoints (${endpoints.length})`}>
          <div className="overflow-x-auto">
            <table className="w-full text-[11.5px]">
              <thead>
                <tr className="text-left text-ink-faint">
                  <th className="pb-1 pr-3 font-normal">port</th>
                  <th className="pb-1 pr-3 font-normal">path</th>
                  <th className="pb-1 pr-3 font-normal">scheme</th>
                  <th className="pb-1 pr-3 font-normal">interval</th>
                  <th className="pb-1 font-normal">timeout</th>
                </tr>
              </thead>
              <tbody>
                {endpoints.map((e, i) => (
                  <tr key={i} className="border-t border-border/40">
                    <td className="py-1 pr-3 text-ink">
                      {String(e.port ?? e.targetPort ?? "—")}
                    </td>
                    <td className="py-1 pr-3 text-ink-muted">
                      {String(e.path ?? "/metrics")}
                    </td>
                    <td className="py-1 pr-3 text-ink-muted">
                      {String(e.scheme ?? "http")}
                    </td>
                    <td className="py-1 pr-3 text-ink-muted">
                      {e.interval ? String(e.interval) : "—"}
                    </td>
                    <td className="py-1 text-ink-muted">
                      {e.scrapeTimeout ? String(e.scrapeTimeout) : "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Section>
      ) : null}

      {spec.jobLabel || spec.targetLabels ? (
        <Section title="labels">
          {spec.jobLabel ? <KV k="job label" v={String(spec.jobLabel)} /> : null}
          {Array.isArray(spec.targetLabels) ? (
            <KV
              k="target labels"
              v={(spec.targetLabels as string[]).join(", ")}
            />
          ) : null}
          {Array.isArray(spec.podTargetLabels) ? (
            <KV
              k="pod labels"
              v={(spec.podTargetLabels as string[]).join(", ")}
            />
          ) : null}
        </Section>
      ) : null}
    </>
  );
}
