// SchemaMissingBanner — surfaces "we couldn't load a K8s schema for
// this kind, so you're editing without autocomplete / validation."
//
// Triggered when YamlEditor's `schemaState === "missing"` (the
// /openapi/v3 fetch succeeded but findSchemaForGVK returned no
// match — typical for CRDs without aggregated v3 schemas, e.g.
// legacy v1beta1 CRDs or pre-1.27 clusters).
//
// Sits above ApplyErrorBanner in the YamlEditor banner stack. Read
// in YamlEditor only — keep this file pure presentation.

import { cn } from "../../../lib/cn";

interface SchemaMissingBannerProps {
  /** Display label for the kind (e.g. "Issuer" / "deployments").
   *  Optional — banner reads fine without it. */
  kindLabel?: string;
}

export function SchemaMissingBanner({ kindLabel }: SchemaMissingBannerProps) {
  return (
    <div
      className={cn(
        "flex shrink-0 items-start gap-3 border-y px-4 py-2",
        "border-yellow/50 bg-yellow/10",
      )}
      role="status"
    >
      <span aria-hidden className="mt-1 size-2 shrink-0 rounded-sm bg-yellow" />
      <div className="min-w-0 flex-1">
        <div className="font-mono text-[11px] font-medium text-yellow-700 dark:text-yellow-300">
          schema unavailable — validation off
        </div>
        <div className="mt-0.5 font-mono text-[11.5px] text-ink-muted">
          {kindLabel ? (
            <>
              <span className="font-medium text-ink">{kindLabel}</span>
              {" — "}
            </>
          ) : null}
          this CRD didn't ship an OpenAPI v3 schema, so you'll edit without autocomplete or live validation. The cluster still validates on apply.
        </div>
      </div>
    </div>
  );
}
