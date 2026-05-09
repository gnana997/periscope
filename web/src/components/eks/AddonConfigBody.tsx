// AddonConfigBody — body of the "config" tab on AddonDetailPane.
// Renders the addon's stored configurationValues (operator's
// overrides) in a read-only Monaco YAML viewer. The data is the
// same `useAddon(cluster, name)` query the describe tab uses, so
// React Query dedupes — both tabs share one network round-trip.
//
// When the addon has no configurationValues, we show an empty-state
// hint pointing operators at the Upgrade dialog (the only path to
// add or change overrides).
//
// Why not use AddonDetailBody's existing `Section` primitive: the
// Monaco viewer needs a fixed-height parent (h-full inside resolves
// to 0 against indefinite-height parents), and the viewer should
// fill the tab body, not be sandwiched between the standard label +
// content rhythm. So this body owns its own layout.

import { useAddon } from "../../hooks/useAddons";
import { MonacoYAML } from "../helm/MonacoYAML";

interface AddonConfigBodyProps {
  cluster: string;
  name: string;
}

export function AddonConfigBody({ cluster, name }: AddonConfigBodyProps) {
  const { data, isLoading, isError, error } = useAddon(cluster, name);

  if (isLoading) {
    return <p className="text-[12px] italic text-ink-faint">Loading…</p>;
  }
  if (isError) {
    return (
      <p className="text-[12px] text-red">
        Failed to load detail: {(error as Error)?.message ?? "unknown error"}.
      </p>
    );
  }
  if (!data) return null;

  const values = data.configurationValues ?? "";

  if (!values) {
    return (
      <div className="space-y-2">
        <p className="text-[12.5px] text-ink-muted">
          No configuration overrides — this add-on runs with the
          AWS-published defaults for{" "}
          <span className="font-mono">{data.version ?? "this version"}</span>.
        </p>
        <p className="text-[11.5px] italic text-ink-faint">
          Configure overrides via the Upgrade… dialog.
        </p>
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col gap-1.5">
      <div className="font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
        configurationValues · {data.version ?? "(version unknown)"}
      </div>
      {/* Fixed h-[460px]: MonacoYAML's container uses h-full min-h-0
          which only resolves against a *definite* parent height.
          Same rule that bit us in HelmValuesEditor — see that
          file's preamble for the gory details. */}
      <div className="flex h-[460px] flex-col overflow-hidden rounded-sm border border-border bg-bg">
        <MonacoYAML value={values} />
      </div>
    </div>
  );
}
