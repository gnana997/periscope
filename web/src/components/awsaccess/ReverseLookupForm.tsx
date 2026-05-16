// ReverseLookupForm renders the action + resource + namespace
// inputs for the per-cluster "which pods can do X" query. The
// sensitive-perms autocomplete comes from the server-side catalog
// — no chip mapping in TS.

import { useMemo, useState } from "react";

import type {
  SensitiveCatalogEntry,
} from "../../lib/identity";
import { SensitivePermChip } from "./SensitivePermChip";

export interface ReverseLookupFormProps {
  catalog: SensitiveCatalogEntry[] | undefined;
  initialAction?: string;
  initialResource?: string;
  initialNamespace?: string;
  onSubmit: (q: { action: string; resource: string; namespace: string }) => void;
  pending?: boolean;
}

export function ReverseLookupForm({
  catalog,
  initialAction = "",
  initialResource = "",
  initialNamespace = "",
  onSubmit,
  pending,
}: ReverseLookupFormProps) {
  const [action, setAction] = useState(initialAction);
  const [resource, setResource] = useState(initialResource);
  const [namespace, setNamespace] = useState(initialNamespace);

  const matches = useMemo(() => {
    if (!catalog || action.length < 1) return [];
    const q = action.toLowerCase();
    return catalog.filter((e) => e.action.toLowerCase().includes(q)).slice(0, 8);
  }, [catalog, action]);

  return (
    <form
      className="grid grid-cols-1 gap-3 md:grid-cols-[2fr,2fr,1fr,auto]"
      onSubmit={(e) => {
        e.preventDefault();
        if (!action.trim()) return;
        onSubmit({ action: action.trim(), resource: resource.trim(), namespace: namespace.trim() });
      }}
    >
      <div>
        <label className="text-[11.5px] font-medium uppercase tracking-wide text-ink-faint">
          Action
        </label>
        <input
          type="text"
          list="sensitive-catalog-list"
          placeholder="e.g. s3:GetObject, iam:PassRole, secretsmanager:*"
          value={action}
          onChange={(e) => setAction(e.target.value)}
          className="mt-1 w-full rounded-sm border border-border bg-surface px-2 py-1.5 font-mono text-[13px] text-ink focus:border-accent focus:outline-none"
        />
        <datalist id="sensitive-catalog-list">
          {(catalog ?? []).map((e) => (
            <option key={e.action} value={e.action} />
          ))}
        </datalist>
        {matches.length > 0 && action !== matches[0]?.action ? (
          <p className="mt-1 text-[11.5px] text-ink-faint">
            Matches: {matches.map((m) => m.action).slice(0, 4).join(", ")}
          </p>
        ) : null}
      </div>

      <div>
        <label className="text-[11.5px] font-medium uppercase tracking-wide text-ink-faint">
          Resource (optional)
        </label>
        <input
          type="text"
          placeholder="e.g. arn:aws:s3:::my-bucket/*"
          value={resource}
          onChange={(e) => setResource(e.target.value)}
          className="mt-1 w-full rounded-sm border border-border bg-surface px-2 py-1.5 font-mono text-[13px] text-ink focus:border-accent focus:outline-none"
        />
      </div>

      <div>
        <label className="text-[11.5px] font-medium uppercase tracking-wide text-ink-faint">
          Namespace (optional)
        </label>
        <input
          type="text"
          placeholder="all"
          value={namespace}
          onChange={(e) => setNamespace(e.target.value)}
          className="mt-1 w-full rounded-sm border border-border bg-surface px-2 py-1.5 font-mono text-[13px] text-ink focus:border-accent focus:outline-none"
        />
      </div>

      <div className="flex items-end">
        <button
          type="submit"
          disabled={pending || !action.trim()}
          className="rounded-sm border border-border bg-accent/10 px-4 py-1.5 text-[13px] font-medium text-accent hover:bg-accent/20 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {pending ? "Searching…" : "Search"}
        </button>
      </div>

      {catalog && catalog.length > 0 ? (
        <div className="md:col-span-4">
          <p className="mb-1 text-[11.5px] uppercase tracking-wide text-ink-faint">
            Quick chips · click to pre-fill
          </p>
          <div className="flex flex-wrap gap-1.5">
            {catalog.map((e) => (
              <SensitivePermChip
                key={e.action}
                category={e.category}
                label={e.action}
                onClick={() => {
                  setAction(e.reverseQuery.action);
                  setResource(e.reverseQuery.resource ?? "");
                  onSubmit({
                    action: e.reverseQuery.action,
                    resource: e.reverseQuery.resource ?? "",
                    namespace: namespace.trim(),
                  });
                }}
              />
            ))}
          </div>
        </div>
      ) : null}
    </form>
  );
}
