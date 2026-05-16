// Cluster Access page (#178). Reconciles EKS Access Entries with the
// legacy aws-auth ConfigMap, shows the unified SA→Role index
// (IRSA + Pod Identity), and the role-centric Pod Identity view.
//
// The page mirrors UpgradeReadinessPage's structure: one top-level
// "is this an EKS cluster" gate around three independent sections,
// each driven by its own hook so a 403 on one (e.g. aws-auth
// visibility) doesn't blank the rest of the page.

import { isBackendNotEKS } from "../lib/api";

import { AccessEntriesSection } from "../components/identity/AccessEntriesSection";
import { PodIdentitySection } from "../components/identity/PodIdentitySection";
import { SARolesSection } from "../components/identity/SARolesSection";
import {
  useAccessEntries,
  useAwsAuthDiff,
  usePodIdentity,
  useSARoles,
} from "../hooks/useIdentity";

export function ClusterAccessPage({ cluster }: { cluster: string }) {
  // Four queries fire in parallel; TanStack Query dedupes within a
  // single render.
  const entries = useAccessEntries(cluster);
  const diff = useAwsAuthDiff(cluster);
  const saRoles = useSARoles(cluster);
  const podIdentity = usePodIdentity(cluster);

  // Page-level not-EKS gate. Any of the four hooks returning 422
  // means the cluster isn't EKS-backed; the others will too. Show
  // a single empty-state instead of three duplicated error chips.
  const isNotEKS =
    (entries.isError && isBackendNotEKS(entries.error)) ||
    (diff.isError && isBackendNotEKS(diff.error)) ||
    (saRoles.isError && isBackendNotEKS(saRoles.error)) ||
    (podIdentity.isError && isBackendNotEKS(podIdentity.error));

  if (isNotEKS) {
    return (
      <div className="px-6 py-8">
        <h1 className="mb-2 text-[16px] font-medium">Cluster Access</h1>
        <p className="text-[13px] text-ink-faint">
          Cluster Access surfaces (EKS Access Entries, Pod Identity, IRSA)
          are EKS features; this cluster is not backed by EKS.
        </p>
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col overflow-y-auto px-6 py-5">
      <header className="mb-4">
        <h1 className="text-[16px] font-medium">Cluster Access</h1>
        <p className="mt-0.5 text-[12px] text-ink-faint">
          EKS Access Entries, the legacy aws-auth ConfigMap, IRSA
          annotations, and Pod Identity associations — reconciled.
        </p>
      </header>

      <AccessEntriesSection
        data={diff.data}
        isLoading={diff.isLoading}
        isError={diff.isError}
        error={diff.error}
      />

      <SARolesSection
        data={saRoles.data}
        isLoading={saRoles.isLoading}
        isError={saRoles.isError}
        error={saRoles.error}
      />

      <PodIdentitySection
        data={podIdentity.data}
        isLoading={podIdentity.isLoading}
        isError={podIdentity.isError}
        error={podIdentity.error}
      />

      {/* `entries` (raw Access Entries list) is fetched alongside
          the diff above; the raw list is reserved for v1.2's
          "click an entry → see policies" drill-in. We pre-fetch it
          here so the cache is warm. */}
      {entries.data && entries.data.length === 0 && (
        <p className="text-[12px] text-ink-faint">
          Cluster has {entries.data.length} access entries.
        </p>
      )}
    </div>
  );
}
