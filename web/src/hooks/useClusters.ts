import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { queryKeys } from "../lib/queryKeys";

export function useClusters() {
  return useQuery({
    queryKey: queryKeys.clusters(),
    queryFn: ({ signal }) => api.clusters(signal),
  });
}

export function useNamespaces(cluster: string | undefined) {
  return useQuery({
    queryKey: queryKeys.cluster(cluster ?? "").namespaces(),
    queryFn: ({ signal }) => api.namespaces(cluster!, signal),
    enabled: Boolean(cluster),
  });
}
