// KarpenterDetailPane — slide-in detail pane for the Karpenter
// dashboard. Renders the same describe / yaml / events tabs as the
// generic CR list page (CustomResourcesPage), but inline alongside
// the Karpenter panels so operators don't navigate away.
//
// Mirrors the data path of CustomResourcesPage.tsx: builds an
// EditorSource manually (since both NodePool and NodeClaim are
// well-known cluster-scoped CRDs we don't need to round-trip the
// CRD list to know their shape) and reuses
// CustomResourceDescribe + YamlView, plus a small inline events
// view so we don't have to refactor CustomResourcesPage to export
// its private helper.

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { DetailPane } from "../detail/DetailPane";
import {
  DetailEmpty,
  DetailError,
  DetailLoading,
} from "../detail/states";
import { YamlView } from "../detail/YamlView";
import { CustomResourceDescribe } from "../detail/describe/CustomResourceDescribe";
import { ResourceActions } from "../edit/ResourceActions";
import type { EditorSource } from "../../lib/customResources";
import { queryKeys } from "../../lib/queryKeys";
import { cn } from "../../lib/cn";

interface Props {
  cluster: string;
  /** Karpenter resource kind: "NodePool" or "NodeClaim". Both
   *  cluster-scoped, both `karpenter.sh/v1`. */
  kind: "NodePool" | "NodeClaim";
  name: string;
  onClose: () => void;
}

export function KarpenterDetailPane({ cluster, kind, name, onClose }: Props) {
  const [activeTab, setActiveTab] = useState<string>("describe");
  const plural = kind === "NodePool" ? "nodepools" : "nodeclaims";
  const group = "karpenter.sh";
  const version = "v1";

  const crSource: EditorSource = {
    kind: "custom",
    cr: {
      group,
      version,
      resource: plural,
      kind,
      scope: "Cluster",
    },
  };

  return (
    <DetailPane
      title={name}
      subtitle={`${kind} · cluster-scoped`}
      activeTab={activeTab}
      onTabChange={setActiveTab}
      onClose={onClose}
      actions={
        <ResourceActions
          cluster={cluster}
          source={crSource}
          namespace={null}
          name={name}
          onDeleted={onClose}
        />
      }
      tabs={[
        {
          id: "describe",
          label: "describe",
          ready: true,
          content: (
            <CustomResourceDescribe
              cluster={cluster}
              group={group}
              version={version}
              plural={plural}
              namespace={null}
              name={name}
            />
          ),
        },
        {
          id: "yaml",
          label: "yaml",
          ready: true,
          content: (
            <YamlView
              cluster={cluster}
              source={crSource}
              ns=""
              name={name}
            />
          ),
        },
        {
          id: "events",
          label: "events",
          ready: true,
          content: (
            <KarpenterEventsView
              cluster={cluster}
              group={group}
              version={version}
              plural={plural}
              name={name}
            />
          ),
        },
      ]}
    />
  );
}

interface EventsProps {
  cluster: string;
  group: string;
  version: string;
  plural: string;
  name: string;
}

// Inline events view — same fetch shape as the helper inside
// CustomResourcesPage. NodePool / NodeClaim are cluster-scoped, so
// the URL uses the placeholder namespace `_` per the events
// handler's contract.
function KarpenterEventsView({ cluster, group, version, plural, name }: EventsProps) {
  const eventsQuery = useQuery({
    queryKey: queryKeys.cluster(cluster).cr(group, version, plural).events("", name),
    queryFn: async ({ signal }) => {
      const url = `/api/clusters/${encodeURIComponent(cluster)}/customresources/${encodeURIComponent(group)}/${encodeURIComponent(version)}/${encodeURIComponent(plural)}/_/${encodeURIComponent(name)}/events`;
      const res = await fetch(url, { signal });
      if (!res.ok) throw new Error(`events fetch failed: ${res.status}`);
      return res.json() as Promise<{
        events: Array<{
          type: string;
          reason: string;
          message: string;
          count: number;
          last: string;
          source: string;
        }>;
      }>;
    },
    enabled: Boolean(name),
  });

  if (eventsQuery.isLoading) return <DetailLoading label="loading events…" />;
  if (eventsQuery.isError)
    return <DetailError message={(eventsQuery.error as Error)?.message ?? "unknown"} />;
  if (!eventsQuery.data || eventsQuery.data.events.length === 0)
    return <DetailEmpty label="no events for this object" />;

  return (
    <ul className="divide-y divide-border">
      {eventsQuery.data.events.map((ev, i) => (
        <li key={i} className="px-5 py-3">
          <div className="flex gap-3">
            <span
              className={cn(
                "mt-1 block size-1.5 shrink-0 rounded-full",
                ev.type === "Warning" ? "bg-red" : "bg-green",
              )}
            />
            <div className="min-w-0 flex-1">
              <div className="flex items-baseline gap-2 font-mono text-[11.5px]">
                <span className="text-ink">{ev.reason}</span>
                <span className="text-ink-faint">· {ev.source}</span>
                {ev.count > 1 ? (
                  <span className="text-ink-faint">× {ev.count}</span>
                ) : null}
                <span className="ml-auto text-ink-faint">{ev.last}</span>
              </div>
              <p className="mt-1 break-words font-mono text-[11px] text-ink-muted">
                {ev.message}
              </p>
            </div>
          </div>
        </li>
      ))}
    </ul>
  );
}
