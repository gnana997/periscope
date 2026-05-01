import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";

export function useClusters() {
  return useQuery({
    queryKey: ["clusters"],
    queryFn: ({ signal }) => api.clusters(signal),
  });
}

export function useNamespaces(cluster: string | undefined) {
  return useQuery({
    queryKey: ["namespaces", cluster],
    queryFn: ({ signal }) => api.namespaces(cluster!, signal),
    enabled: Boolean(cluster),
  });
}
