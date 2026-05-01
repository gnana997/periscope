import { Section, KV } from "../CustomResourceDescribe";
import type { CRDRendererProps } from "./index";

/**
 * networking.istio.io/VirtualService renderer.
 *
 * Hosts + gateways frame the routing surface. Then http/tcp rules
 * render as a list of "match → destination(s)" cards with weight pills
 * for canary splits.
 */
export function VirtualService({ obj }: CRDRendererProps) {
  const spec = (obj.spec ?? {}) as Record<string, unknown>;
  const hosts = arr<string>(spec.hosts).map(String);
  const gateways = arr<string>(spec.gateways).map(String);
  const httpRules = arr<Record<string, unknown>>(spec.http);
  const tcpRules = arr<Record<string, unknown>>(spec.tcp);
  const tlsRules = arr<Record<string, unknown>>(spec.tls);
  const exportTo = arr<string>(spec.exportTo).map(String);

  return (
    <>
      <Section title="routing">
        {hosts.length > 0 ? <KV k="hosts" v={hosts.join(", ")} /> : null}
        {gateways.length > 0 ? (
          <KV k="gateways" v={gateways.join(", ")} />
        ) : (
          <KV k="gateways" v="mesh (default)" />
        )}
        {exportTo.length > 0 ? (
          <KV k="exported to" v={exportTo.join(", ")} />
        ) : null}
      </Section>

      {httpRules.length > 0 ? (
        <Section title={`http rules (${httpRules.length})`}>
          <ul className="space-y-2">
            {httpRules.map((r, i) => (
              <RouteCard key={i} rule={r} />
            ))}
          </ul>
        </Section>
      ) : null}

      {tlsRules.length > 0 ? (
        <Section title={`tls rules (${tlsRules.length})`}>
          <ul className="space-y-2">
            {tlsRules.map((r, i) => (
              <RouteCard key={i} rule={r} />
            ))}
          </ul>
        </Section>
      ) : null}

      {tcpRules.length > 0 ? (
        <Section title={`tcp rules (${tcpRules.length})`}>
          <ul className="space-y-2">
            {tcpRules.map((r, i) => (
              <RouteCard key={i} rule={r} />
            ))}
          </ul>
        </Section>
      ) : null}
    </>
  );
}

function RouteCard({ rule }: { rule: Record<string, unknown> }) {
  const name = rule.name ? String(rule.name) : null;
  const matches = arr<Record<string, unknown>>(rule.match);
  const routes = arr<Record<string, unknown>>(rule.route);

  return (
    <li className="rounded-md border border-border bg-surface-2/30 px-2 py-1.5">
      {name ? (
        <div className="mb-1 text-[10px] uppercase tracking-[0.08em] text-ink-faint">
          {name}
        </div>
      ) : null}

      {matches.length > 0 ? (
        <div className="mb-1.5 space-y-0.5 text-[11.5px] text-ink-muted">
          {matches.map((m, i) => (
            <div key={i}>match: {formatMatch(m)}</div>
          ))}
        </div>
      ) : null}

      {routes.length > 0 ? (
        <ul className="space-y-0.5 font-mono text-[12px] text-ink">
          {routes.map((rt, i) => {
            const dest = (rt.destination ?? {}) as Record<string, unknown>;
            const port = (dest.port ?? {}) as Record<string, unknown>;
            const portNum = port.number ? `:${String(port.number)}` : "";
            const subset = dest.subset ? ` [${String(dest.subset)}]` : "";
            return (
              <li key={i} className="flex items-center gap-2">
                <span className="text-accent">→</span>
                <span className="break-all">
                  {String(dest.host ?? "?")}
                  {portNum}
                  {subset}
                </span>
                {typeof rt.weight === "number" ? (
                  <span className="ml-auto rounded-md border border-border bg-surface-2/40 px-1.5 py-0.5 text-[10.5px] tabular-nums text-ink-muted">
                    {rt.weight}%
                  </span>
                ) : null}
              </li>
            );
          })}
        </ul>
      ) : null}
    </li>
  );
}

function formatMatch(m: Record<string, unknown>): string {
  const parts: string[] = [];
  const uri = m.uri as Record<string, unknown> | undefined;
  if (uri) {
    if (uri.exact) parts.push(`uri = ${String(uri.exact)}`);
    else if (uri.prefix) parts.push(`uri ^${String(uri.prefix)}`);
    else if (uri.regex) parts.push(`uri ~${String(uri.regex)}`);
  }
  const method = m.method as Record<string, unknown> | undefined;
  if (method?.exact) parts.push(`method ${String(method.exact)}`);
  if (m.headers && typeof m.headers === "object") {
    const keys = Object.keys(m.headers as Record<string, unknown>);
    if (keys.length > 0) parts.push(`headers[${keys.join(",")}]`);
  }
  if (m.scheme && typeof m.scheme === "object") {
    const s = m.scheme as Record<string, unknown>;
    if (s.exact) parts.push(`scheme ${String(s.exact)}`);
  }
  if (m.port) parts.push(`port ${String(m.port)}`);
  if (Array.isArray(m.gateways) && m.gateways.length > 0) {
    parts.push(`gw ${(m.gateways as string[]).join(",")}`);
  }
  return parts.length > 0 ? parts.join(" · ") : "(any)";
}

function arr<T>(v: unknown): T[] {
  return Array.isArray(v) ? (v as T[]) : [];
}
