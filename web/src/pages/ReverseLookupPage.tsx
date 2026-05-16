// ReverseLookupPage (#188) — per-cluster "which pods can do X?"
// query surface. Locked-pane on top from capabilities; form +
// virtualized result table below.

import { useSearchParams } from "react-router-dom";
import { useEffect, useMemo, useState } from "react";

import { ReverseLookupForm } from "../components/awsaccess/ReverseLookupForm";
import { ReverseLookupResultTable } from "../components/awsaccess/ReverseLookupResultTable";
import { LockedFeaturePane } from "../components/ui/LockedFeaturePane";
import {
  useIdentityCapabilities,
  useReverseLookup,
  useSensitiveCatalog,
} from "../hooks/useIdentity";

export function ReverseLookupPage({ cluster }: { cluster: string }) {
  const [params, setParams] = useSearchParams();
  const initialAction = params.get("action") ?? "";
  const initialResource = params.get("resource") ?? "";
  const initialNamespace = params.get("namespace") ?? "";

  const [query, setQuery] = useState<{ action: string; resource: string; namespace: string }>({
    action: initialAction,
    resource: initialResource,
    namespace: initialNamespace,
  });
  const [submitted, setSubmitted] = useState(initialAction !== "");
  const [recheckPending, setRecheckPending] = useState(false);

  // Reflect query into URL so deep-links from forward-view chips
  // restore the same view on refresh / share.
  useEffect(() => {
    if (!submitted) return;
    const next = new URLSearchParams();
    if (query.action) next.set("action", query.action);
    if (query.resource) next.set("resource", query.resource);
    if (query.namespace) next.set("namespace", query.namespace);
    setParams(next, { replace: true });
  }, [query, submitted, setParams]);

  const cap = useIdentityCapabilities(cluster);
  const catalog = useSensitiveCatalog();
  const rev = useReverseLookup(cluster, query, submitted);

  const capFeature = cap.data?.features.reverseLookup;
  const showLocked = capFeature && !capFeature.available;

  const catalogEntries = useMemo(() => catalog.data?.entries ?? [], [catalog.data]);

  return (
    <div className="px-6 py-6">
      <header className="mb-4">
        <h1 className="text-[18px] font-medium text-ink">AWS Access · Reverse Lookup</h1>
        <p className="mt-1 text-[13px] text-ink-faint">
          Which pods in this cluster can perform an IAM action? Pre-filled queries from a Pod / Deployment AWS Access tab land here.
        </p>
      </header>

      {showLocked ? (
        <LockedFeaturePane
          feature={capFeature}
          title="Reverse Lookup"
          probedAt={cap.data?.fetchedAt}
          recheckPending={recheckPending}
          onRecheck={async () => {
            setRecheckPending(true);
            try {
              await cap.recheck();
            } finally {
              setRecheckPending(false);
            }
          }}
        />
      ) : (
        <>
          {capFeature?.note ? (
            <p className="mb-3 rounded-sm border border-border bg-surface-2 px-3 py-2 text-[12px] text-ink-muted">
              {capFeature.note}
            </p>
          ) : null}
          <ReverseLookupForm
            catalog={catalogEntries}
            initialAction={query.action}
            initialResource={query.resource}
            initialNamespace={query.namespace}
            pending={rev.isFetching && submitted}
            onSubmit={(q) => {
              setQuery(q);
              setSubmitted(true);
            }}
          />
          {submitted ? (
            rev.isError ? (
              <p className="mt-4 text-[13px] text-red">
                Reverse lookup failed: {(rev.error as Error).message}
              </p>
            ) : rev.isLoading ? (
              <p className="mt-4 text-[13px] text-ink-faint">Walking the SA→Role index…</p>
            ) : rev.data ? (
              <ReverseLookupResultTable
                cluster={cluster}
                rows={rev.data.rows}
                truncated={rev.data.truncated}
                totalPods={rev.data.totalPods}
              />
            ) : null
          ) : (
            <p className="mt-6 text-[13px] text-ink-faint">
              Pick a chip above, or type an IAM action and press Search.
            </p>
          )}
        </>
      )}
    </div>
  );
}

export default ReverseLookupPage;
