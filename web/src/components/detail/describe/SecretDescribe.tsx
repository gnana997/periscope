import { useState } from "react";
import { useSecretDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { cn } from "../../../lib/cn";
import { DetailError, DetailLoading } from "../states";
import { MetaPills, SectionTitle, StatStrip } from "./shared";
import { SecretKeyRow } from "./SecretKeyRow";
import type { SecretKey } from "../../../lib/types";

/** Map K8s secret types to a short, scannable label.
 *  e.g. "kubernetes.io/dockerconfigjson" → "dockerconfigjson". */
function shortType(t: string): string {
  const slash = t.lastIndexOf("/");
  return slash >= 0 ? t.slice(slash + 1) : t;
}

export function SecretDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useSecretDetail(cluster, ns, name);

  if (isLoading) return <DetailLoading />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  return (
    <div>
      <StatStrip
        stats={[
          {
            label: "Type",
            value: <span title={data.type}>{shortType(data.type)}</span>,
            family: "sans",
          },
          { label: "Keys", value: String(data.keyCount) },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />

      <div className="px-5 py-4">
        {data.immutable && (
          <div className="mb-3 inline-flex items-center gap-1.5 rounded-md border border-border bg-surface-2/40 px-2 py-1 font-mono text-[11px] text-ink-muted">
            <span className="block size-1.5 rounded-full bg-ink-faint" />
            immutable
          </div>
        )}

        <SectionTitle>Keys</SectionTitle>
        {data.keys.length === 0 ? (
          <span className="text-[11.5px] text-ink-faint">—</span>
        ) : (
          <ul className="space-y-1">
            {data.keys.map((k) => (
              <SecretKeyRow
                key={k.name}
                cluster={cluster}
                ns={ns}
                name={name}
                k={k}
              />
            ))}
          </ul>
        )}

        <RedactionNotice />

        <SectionTitle>Labels</SectionTitle>
        <MetaPills map={data.labels} />

        <SectionTitle>Annotations</SectionTitle>
        <MetaPills map={data.annotations} />
      </div>
    </div>
  );
}

function RedactionNotice() {
  const [open, setOpen] = useState(false);
  return (
    <div className="mt-3 rounded-md border border-border bg-surface-2/40 px-3 py-2 text-[11.5px] text-ink-muted">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className={cn(
          "flex w-full items-center gap-1.5 text-left",
          "text-ink-muted hover:text-ink",
        )}
      >
        <span
          className={cn(
            "inline-block transition-transform",
            open ? "rotate-90" : "",
          )}
        >
          ›
        </span>
        <span>secrets are revealed per-key, audit-logged server-side</span>
      </button>
      {open && (
        <div className="mt-2 leading-relaxed text-ink-muted">
          <p>
            Periscope v1 uses a shared cluster role; any user logged in via
            Okta can reveal these values. Each <code className="rounded bg-bg px-1 py-0.5 text-[10.5px]">reveal</code> click is recorded on
            the backend with your identity, the cluster, namespace, secret
            name, and key. v2 will swap this for per-user identity
            pass-through, gating reveal by your own RBAC.
          </p>
        </div>
      )}
    </div>
  );
}

export type { SecretKey };
