import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import type { ResourceKind, ResourceListResponse } from "../lib/types";

interface ResourceQueryArgs {
  cluster: string | undefined;
  resource: ResourceKind;
  namespace?: string;
}

export function useResource({ cluster, resource, namespace }: ResourceQueryArgs) {
  return useQuery<ResourceListResponse>({
    queryKey: ["resource", cluster, resource, namespace ?? ""],
    queryFn: ({ signal }): Promise<ResourceListResponse> => {
      switch (resource) {
        case "namespaces":
          return api.namespaces(cluster!, signal);
        case "pods":
          return api.pods(cluster!, namespace, signal);
        case "deployments":
          return api.deployments(cluster!, namespace, signal);
        case "services":
          return api.services(cluster!, namespace, signal);
        case "configmaps":
          return api.configmaps(cluster!, namespace, signal);
      }
    },
    enabled: Boolean(cluster),
  });
}
