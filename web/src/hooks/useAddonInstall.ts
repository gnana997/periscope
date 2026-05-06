// useAddonInstall — install / configuration-schema hooks for the
// EKS add-on install dialog (issue #119, PR-2).
//
// Two hooks:
//
//   useAddonConfigurationSchema(cluster, name, version)
//     Lazy fetch — fires when (name, version) are both set. Backend
//     caches per-(addon, version) for 24h since AWS-published
//     schemas are immutable per version.
//
//   useInstallAddon(cluster)
//     Mutation. POSTs the install request; backend returns 202 with
//     the addon detail in status=CREATING. On success/failure
//     invalidates the addons list + detail queries so the cataloging
//     view + addons page see fresh state immediately. The actual
//     status flip from CREATING → ACTIVE / CREATE_FAILED happens
//     AWS-side over 1-5 min and is observed via useAddons / useAddon
//     refetch (status-aware polling lives in those hooks).

import {
  skipToken,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { ApiError, api } from "../lib/api";
import { queryKeys } from "../lib/queryKeys";
import type {
  AddonConfigurationResponse,
  AddonDetail,
  AddonInstallRequest,
} from "../lib/types";

// 24h staleTime — schemas are immutable per (addon, version) so a
// long stale time is safe. Backend caches at the same TTL.
const SCHEMA_STALE_MS = 24 * 60 * 60 * 1000;

export function useAddonConfigurationSchema(
  cluster: string,
  name: string,
  version: string,
) {
  const enabled = Boolean(cluster && name && version);
  return useQuery<AddonConfigurationResponse, ApiError | Error>({
    queryKey: queryKeys
      .cluster(cluster)
      .addons.configurationSchema(name, version),
    queryFn: enabled
      ? ({ signal }) => api.addonConfigurationSchema(cluster, name, version, signal)
      : skipToken,
    staleTime: SCHEMA_STALE_MS,
    retry: (count, err) => {
      // 422 (non-EKS) and 400 (missing version) are deterministic;
      // retrying just doubles the audit-log noise.
      if (
        err &&
        typeof err === "object" &&
        "status" in err &&
        ((err as { status: number }).status === 422 ||
          (err as { status: number }).status === 400)
      ) {
        return false;
      }
      return count < 1;
    },
  });
}

export function useInstallAddon(cluster: string) {
  const qc = useQueryClient();
  return useMutation<AddonDetail, ApiError | Error, AddonInstallRequest>({
    mutationFn: (req) => api.installAddon(cluster, req),
    // Invalidate on both success AND error. On error the addon may
    // have transitioned to CREATE_FAILED on the AWS side (e.g.,
    // partial provisioning that subsequently aborted), so the
    // cached row is unsafe to keep either way.
    onSettled: (_data, _err, req) => {
      qc.invalidateQueries({ queryKey: queryKeys.cluster(cluster).addons.list() });
      qc.invalidateQueries({
        queryKey: queryKeys.cluster(cluster).addons.detail(req.addonName),
      });
      qc.invalidateQueries({
        queryKey: queryKeys.cluster(cluster).addons.catalog(),
      });
    },
  });
}
