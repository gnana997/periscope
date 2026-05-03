// useTriggerCronJob — fires the CronJob's jobTemplate as a fresh
// Job, equivalent to `kubectl create job --from=cronjob/<name>`. The
// new Job is server-generated; on success we invalidate the Jobs
// list cache so it appears wherever a Jobs view is open. The CronJob
// itself isn't mutated, but its `lastScheduleTime` is unaffected —
// this is an out-of-band run, not a schedule trigger.

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ApiError, api } from "../../lib/api";
import { queryKeys } from "../../lib/queryKeys";
import { showToast } from "../../lib/toastBus";

interface TriggerArgs {
  cluster: string;
  namespace: string;
  name: string;
}

export function useTriggerCronJob(args: TriggerArgs) {
  const qc = useQueryClient();

  return useMutation<{ jobName: string }, ApiError | Error, void>({
    mutationFn: () =>
      api.triggerCronJob(args.cluster, args.namespace, args.name),
    onSuccess: async (result) => {
      // The new Job spawns a Pod cascade; invalidate Jobs (where the
      // new row will appear) and Pods (which our 15s polling would
      // catch anyway, but invalidating shaves the lag).
      await qc.invalidateQueries({
        queryKey: queryKeys.cluster(args.cluster).kind("jobs").all,
      });
      await qc.invalidateQueries({
        queryKey: queryKeys.cluster(args.cluster).kind("pods").all,
      });
      // Also bump the CronJob's own detail so the child-jobs strip
      // refreshes (CronJobDetail inlines the last N spawned jobs).
      await qc.invalidateQueries({
        queryKey: queryKeys
          .cluster(args.cluster)
          .kind("cronjobs")
          .detail(args.namespace, args.name),
      });
      showToast(`triggered ${args.name} → ${result.jobName}`, "success", 2500);
    },
    onError: (err) => {
      showToast(
        `failed to trigger ${args.name}: ${err?.message ?? "unknown"}`,
        "error",
        5000,
      );
    },
  });
}
