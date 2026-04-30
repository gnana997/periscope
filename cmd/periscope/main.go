package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"

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
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/whoami", credentials.Wrap(factory, whoami))
	mux.HandleFunc("/api/clusters", listClustersHandler(registry))
	mux.HandleFunc("/api/clusters/", credentials.Wrap(factory, namespacesHandler(registry)))

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

func namespacesHandler(reg *clusters.Registry) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		name, ok := parseClusterNamespacesPath(r.URL.Path)
		if !ok {
			http.NotFound(w, r)
			return
		}
		c, ok := reg.ByName(name)
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		result, err := k8s.ListNamespaces(r.Context(), p, k8s.ListNamespacesArgs{Cluster: c})
		if err != nil {
			slog.ErrorContext(r.Context(), "ListNamespaces failed",
				"err", err, "cluster", name, "actor", p.Actor())
			http.Error(w, "list namespaces failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// parseClusterNamespacesPath extracts <name> from /api/clusters/<name>/namespaces.
// Returns false for any other shape under /api/clusters/.
func parseClusterNamespacesPath(path string) (string, bool) {
	const prefix = "/api/clusters/"
	const suffix = "/namespaces"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	name := path[len(prefix) : len(path)-len(suffix)]
	if name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return name, true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
