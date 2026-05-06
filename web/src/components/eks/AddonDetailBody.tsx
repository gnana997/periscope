import { useAddon } from "../../hooks/useAddons";
import { cn } from "../../lib/cn";

// AddonDetailBody — body rendered when an AddOnsPage row is
// expanded. Lives outside AddOnsPage.tsx so the row table stays
// readable, and so the next EKS surface that wants the same
// "Field / Section" atoms can import them from one place rather
// than re-deriving the layout.
//
// The Field / FieldGrid / Section primitives below are also exported
// because they're the SPA's house style for "row of label+value
// boxes" and the next EKS detail surface (e.g. addon-policy review)
// will want them.

export function AddonDetailBody({
  cluster,
  name,
}: {
  cluster: string;
  name: string;
}) {
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

  return (
    <div className="space-y-3">
      <FieldGrid>
        <Field label="Installed version">{data.version ?? "—"}</Field>
        <Field label="Latest version">{data.latestVersion ?? "—"}</Field>
        <Field label="Status">{data.status ?? "—"}</Field>
        <Field label="Modified">
          {data.modifiedAt ? new Date(data.modifiedAt).toUTCString() : "—"}
        </Field>
      </FieldGrid>

      {data.serviceAccountRoleArn && (
        <Section label="IAM service account role">
          <div className="break-all font-mono text-[11px] text-ink-muted">
            {data.serviceAccountRoleArn}
          </div>
        </Section>
      )}

      {data.healthIssues && data.healthIssues.length > 0 && (
        <Section label="Health issues">
          <ul className="space-y-1.5 text-[12px]">
            {data.healthIssues.map((issue, i) => (
              <li key={i} className="text-red">
                <span className="font-mono">{issue.code}</span>
                {issue.message && <> — {issue.message}</>}
                {issue.resourceIds && issue.resourceIds.length > 0 && (
                  <div className="ml-3 mt-1 font-mono text-[11px] text-ink-faint">
                    {issue.resourceIds.join(", ")}
                  </div>
                )}
              </li>
            ))}
          </ul>
        </Section>
      )}

      {data.availableVersions && data.availableVersions.length > 0 && (
        <Section label="Version history">
          <div className="overflow-hidden rounded-sm border border-border">
            <table className="w-full text-[11.5px]">
              <thead className="border-b border-border bg-surface text-[10px] uppercase tracking-[0.06em] text-ink-faint">
                <tr>
                  <th className="px-2 py-1 text-left">Version</th>
                  <th className="px-2 py-1 text-left">Compatible k8s</th>
                  <th className="px-2 py-1 text-left">Default</th>
                </tr>
              </thead>
              <tbody>
                {data.availableVersions.map((v) => {
                  const isInstalled = v.version === data.version;
                  return (
                    <tr
                      key={v.version}
                      className={cn(
                        "border-b border-border last:border-0",
                        isInstalled && "bg-accent-soft/30",
                      )}
                    >
                      <td className="px-2 py-1 font-mono">
                        {v.version}
                        {isInstalled && (
                          <span className="ml-2 text-[10px] uppercase tracking-[0.06em] text-accent">
                            installed
                          </span>
                        )}
                      </td>
                      <td className="px-2 py-1 font-mono text-ink-muted">
                        {v.compatibleK8sVersions.join(", ") || "—"}
                      </td>
                      <td className="px-2 py-1 text-ink-muted">
                        {v.defaultVersion ? "yes" : ""}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </Section>
      )}

      {(data.owner || data.publisher) && (
        <Section label="Source">
          <div className="text-[11.5px] text-ink-muted">
            {data.publisher ? `Published by ${data.publisher}` : null}
            {data.owner ? ` · owner: ${data.owner}` : null}
          </div>
        </Section>
      )}

      {data.arn && (
        <Section label="ARN">
          <div className="break-all font-mono text-[11px] text-ink-muted">
            {data.arn}
          </div>
        </Section>
      )}
    </div>
  );
}

// ── Layout primitives ──────────────────────────────────────────────
//
// Exported so siblings (future EKS detail surfaces) reuse the same
// label-above-value rhythm without re-deriving it.

export function Section({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="mb-1 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
        {label}
      </div>
      {children}
    </div>
  );
}

export function FieldGrid({ children }: { children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-2 gap-x-6 gap-y-2 lg:grid-cols-4">
      {children}
    </div>
  );
}

export function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
        {label}
      </div>
      <div className="mt-0.5 text-[12.5px]">{children}</div>
    </div>
  );
}
