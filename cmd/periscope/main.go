package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx := context.Background()

	factory, err := credentials.NewSharedIrsaFactory(ctx)
	if err != nil {
		slog.Error("failed to initialize credentials factory", "err", err)
		os.Exit(1)
	}

	registry, err := loadRegistry()
	if err != nil {
		slog.Error("failed to load cluster registry", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/whoami", credentials.Wrap(factory, whoami))
	mux.HandleFunc("GET /api/clusters", listClustersHandler(registry))

	mux.HandleFunc("GET /api/clusters/{name}/namespaces", credentials.Wrap(factory,
		listResource(registry, "namespaces",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _ string) (k8s.NamespaceList, error) {
				return k8s.ListNamespaces(ctx, p, k8s.ListNamespacesArgs{Cluster: c})
			})))

	mux.HandleFunc("GET /api/clusters/{name}/pods", credentials.Wrap(factory,
		listResource(registry, "pods",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.PodList, error) {
				return k8s.ListPods(ctx, p, k8s.ListPodsArgs{Cluster: c, Namespace: ns})
			})))

	mux.HandleFunc("GET /api/clusters/{name}/deployments", credentials.Wrap(factory,
		listResource(registry, "deployments",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.DeploymentList, error) {
				return k8s.ListDeployments(ctx, p, k8s.ListDeploymentsArgs{Cluster: c, Namespace: ns})
			})))

	mux.HandleFunc("GET /api/clusters/{name}/services", credentials.Wrap(factory,
		listResource(registry, "services",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.ServiceList, error) {
				return k8s.ListServices(ctx, p, k8s.ListServicesArgs{Cluster: c, Namespace: ns})
			})))

	mux.HandleFunc("GET /api/clusters/{name}/configmaps", credentials.Wrap(factory,
		listResource(registry, "configmaps",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.ConfigMapList, error) {
				return k8s.ListConfigMaps(ctx, p, k8s.ListConfigMapsArgs{Cluster: c, Namespace: ns})
			})))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port
	slog.Info("periscope starting", "addr", addr, "clusters", len(registry.List()))
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("server failed", "err", err)
		os.Exit(1)
	}
}

func loadRegistry() (*clusters.Registry, error) {
	path := os.Getenv("PERISCOPE_CLUSTERS_FILE")
	if path == "" {
		slog.Warn("PERISCOPE_CLUSTERS_FILE not set; running with empty cluster registry")
		return clusters.Empty(), nil
	}
	return clusters.LoadFromFile(path)
}

func whoami(w http.ResponseWriter, _ *http.Request, p credentials.Provider) {
	writeJSON(w, http.StatusOK, map[string]string{"actor": p.Actor()})
}

func listClustersHandler(reg *clusters.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"clusters": reg.List()})
	}
}

// listResource wraps a list-style operation with the common HTTP machinery:
// resolve cluster from {name} path param, extract optional ?namespace=
// query param, invoke the typed op, JSON-encode the response, log errors
// with actor + cluster context.
func listResource[Resp any](
	reg *clusters.Registry,
	resource string,
	op func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (Resp, error),
) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(r.PathValue("name"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		result, err := op(r.Context(), p, c, r.URL.Query().Get("namespace"))
		if err != nil {
			slog.ErrorContext(r.Context(), "list operation failed",
				"resource", resource, "err", err,
				"cluster", c.Name, "actor", p.Actor())
			http.Error(w, "operation failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
