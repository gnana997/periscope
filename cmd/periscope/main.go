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

	// --- LIST endpoints ---

	mux.HandleFunc("GET /api/clusters/{cluster}/namespaces", credentials.Wrap(factory,
		listResource(registry, "namespaces",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _ string) (k8s.NamespaceList, error) {
				return k8s.ListNamespaces(ctx, p, k8s.ListNamespacesArgs{Cluster: c})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/pods", credentials.Wrap(factory,
		listResource(registry, "pods",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.PodList, error) {
				return k8s.ListPods(ctx, p, k8s.ListPodsArgs{Cluster: c, Namespace: ns})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/deployments", credentials.Wrap(factory,
		listResource(registry, "deployments",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.DeploymentList, error) {
				return k8s.ListDeployments(ctx, p, k8s.ListDeploymentsArgs{Cluster: c, Namespace: ns})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/statefulsets", credentials.Wrap(factory,
		listResource(registry, "statefulsets",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.StatefulSetList, error) {
				return k8s.ListStatefulSets(ctx, p, k8s.ListStatefulSetsArgs{Cluster: c, Namespace: ns})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/daemonsets", credentials.Wrap(factory,
		listResource(registry, "daemonsets",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.DaemonSetList, error) {
				return k8s.ListDaemonSets(ctx, p, k8s.ListDaemonSetsArgs{Cluster: c, Namespace: ns})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/services", credentials.Wrap(factory,
		listResource(registry, "services",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.ServiceList, error) {
				return k8s.ListServices(ctx, p, k8s.ListServicesArgs{Cluster: c, Namespace: ns})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/ingresses", credentials.Wrap(factory,
		listResource(registry, "ingresses",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.IngressList, error) {
				return k8s.ListIngresses(ctx, p, k8s.ListIngressesArgs{Cluster: c, Namespace: ns})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/configmaps", credentials.Wrap(factory,
		listResource(registry, "configmaps",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.ConfigMapList, error) {
				return k8s.ListConfigMaps(ctx, p, k8s.ListConfigMapsArgs{Cluster: c, Namespace: ns})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/secrets", credentials.Wrap(factory,
		listResource(registry, "secrets",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.SecretList, error) {
				return k8s.ListSecrets(ctx, p, k8s.ListSecretsArgs{Cluster: c, Namespace: ns})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/jobs", credentials.Wrap(factory,
		listResource(registry, "jobs",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.JobList, error) {
				return k8s.ListJobs(ctx, p, k8s.ListJobsArgs{Cluster: c, Namespace: ns})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/cronjobs", credentials.Wrap(factory,
		listResource(registry, "cronjobs",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.CronJobList, error) {
				return k8s.ListCronJobs(ctx, p, k8s.ListCronJobsArgs{Cluster: c, Namespace: ns})
			})))

	// --- GET (detail) endpoints ---

	mux.HandleFunc("GET /api/clusters/{cluster}/pods/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "pod",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.PodDetail, error) {
				return k8s.GetPod(ctx, p, k8s.GetPodArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/deployments/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "deployment",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.DeploymentDetail, error) {
				return k8s.GetDeployment(ctx, p, k8s.GetDeploymentArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/statefulsets/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "statefulset",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.StatefulSetDetail, error) {
				return k8s.GetStatefulSet(ctx, p, k8s.GetStatefulSetArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/daemonsets/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "daemonset",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.DaemonSetDetail, error) {
				return k8s.GetDaemonSet(ctx, p, k8s.GetDaemonSetArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/services/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "service",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.ServiceDetail, error) {
				return k8s.GetService(ctx, p, k8s.GetServiceArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/ingresses/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "ingress",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.IngressDetail, error) {
				return k8s.GetIngress(ctx, p, k8s.GetIngressArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/configmaps/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "configmap",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.ConfigMapDetail, error) {
				return k8s.GetConfigMap(ctx, p, k8s.GetConfigMapArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/secrets/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "secret",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.SecretDetail, error) {
				return k8s.GetSecret(ctx, p, k8s.GetSecretArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/jobs/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "job",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.JobDetail, error) {
				return k8s.GetJob(ctx, p, k8s.GetJobArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/cronjobs/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "cronjob",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.CronJobDetail, error) {
				return k8s.GetCronJob(ctx, p, k8s.GetCronJobArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	// Namespaces are cluster-scoped: no {ns} segment.
	mux.HandleFunc("GET /api/clusters/{cluster}/namespaces/{name}", credentials.Wrap(factory,
		detailHandler(registry, "namespace",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (k8s.NamespaceDetail, error) {
				return k8s.GetNamespace(ctx, p, k8s.GetNamespaceArgs{Cluster: c, Name: name})
			})))

	// --- YAML endpoints ---

	mux.HandleFunc("GET /api/clusters/{cluster}/pods/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "pod",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetPodYAML(ctx, p, k8s.GetPodArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/deployments/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "deployment",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetDeploymentYAML(ctx, p, k8s.GetDeploymentArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/statefulsets/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "statefulset",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetStatefulSetYAML(ctx, p, k8s.GetStatefulSetArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/daemonsets/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "daemonset",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetDaemonSetYAML(ctx, p, k8s.GetDaemonSetArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/services/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "service",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetServiceYAML(ctx, p, k8s.GetServiceArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/ingresses/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "ingress",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetIngressYAML(ctx, p, k8s.GetIngressArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/configmaps/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "configmap",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetConfigMapYAML(ctx, p, k8s.GetConfigMapArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/secrets/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "secret",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetSecretYAML(ctx, p, k8s.GetSecretArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/jobs/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "job",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetJobYAML(ctx, p, k8s.GetJobArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/cronjobs/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "cronjob",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetCronJobYAML(ctx, p, k8s.GetCronJobArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	mux.HandleFunc("GET /api/clusters/{cluster}/namespaces/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "namespace",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (string, error) {
				return k8s.GetNamespaceYAML(ctx, p, k8s.GetNamespaceArgs{Cluster: c, Name: name})
			})))

	// --- Events endpoints (per object) ---

	mux.HandleFunc("GET /api/clusters/{cluster}/pods/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "Pod")))
	mux.HandleFunc("GET /api/clusters/{cluster}/deployments/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "Deployment")))
	mux.HandleFunc("GET /api/clusters/{cluster}/statefulsets/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "StatefulSet")))
	mux.HandleFunc("GET /api/clusters/{cluster}/daemonsets/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "DaemonSet")))
	mux.HandleFunc("GET /api/clusters/{cluster}/services/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "Service")))
	mux.HandleFunc("GET /api/clusters/{cluster}/ingresses/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "Ingress")))
	mux.HandleFunc("GET /api/clusters/{cluster}/configmaps/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "ConfigMap")))
	mux.HandleFunc("GET /api/clusters/{cluster}/secrets/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "Secret")))
	mux.HandleFunc("GET /api/clusters/{cluster}/jobs/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "Job")))
	mux.HandleFunc("GET /api/clusters/{cluster}/cronjobs/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "CronJob")))
	mux.HandleFunc("GET /api/clusters/{cluster}/namespaces/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "Namespace")))

	// --- Secret reveal endpoint (audit-logged, per-key) ---

	mux.HandleFunc("GET /api/clusters/{cluster}/secrets/{ns}/{name}/data/{key}",
		credentials.Wrap(factory, secretRevealHandler(registry)))

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

// listResource wraps a list-style operation.
func listResource[Resp any](
	reg *clusters.Registry,
	resource string,
	op func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (Resp, error),
) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(r.PathValue("cluster"))
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

// detailHandler wraps a Get-style operation that returns a typed DTO.
// {ns} is empty for cluster-scoped resources (e.g. namespaces).
func detailHandler[Resp any](
	reg *clusters.Registry,
	resource string,
	op func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (Resp, error),
) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(r.PathValue("cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		ns := r.PathValue("ns")
		name := r.PathValue("name")
		result, err := op(r.Context(), p, c, ns, name)
		if err != nil {
			slog.ErrorContext(r.Context(), "get operation failed",
				"resource", resource, "err", err,
				"cluster", c.Name, "ns", ns, "name", name, "actor", p.Actor())
			http.Error(w, "operation failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// yamlHandler wraps a Get-style operation that returns a YAML string.
func yamlHandler(
	reg *clusters.Registry,
	resource string,
	op func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error),
) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(r.PathValue("cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		ns := r.PathValue("ns")
		name := r.PathValue("name")
		result, err := op(r.Context(), p, c, ns, name)
		if err != nil {
			slog.ErrorContext(r.Context(), "yaml operation failed",
				"resource", resource, "err", err,
				"cluster", c.Name, "ns", ns, "name", name, "actor", p.Actor())
			http.Error(w, "operation failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(result))
	}
}

// eventsHandler wraps ListObjectEvents with a fixed Kind for the route.
func eventsHandler(reg *clusters.Registry, kind string) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(r.PathValue("cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		ns := r.PathValue("ns")
		name := r.PathValue("name")
		result, err := k8s.ListObjectEvents(r.Context(), p, k8s.ListObjectEventsArgs{
			Cluster: c, Kind: kind, Namespace: ns, Name: name,
		})
		if err != nil {
			slog.ErrorContext(r.Context(), "events operation failed",
				"kind", kind, "err", err,
				"cluster", c.Name, "ns", ns, "name", name, "actor", p.Actor())
			http.Error(w, "operation failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// secretRevealHandler wraps GetSecretValue. Audit logging is performed
// inside GetSecretValue itself so it's tied to the read action, not just
// the HTTP request envelope.
func secretRevealHandler(reg *clusters.Registry) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(r.PathValue("cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		ns := r.PathValue("ns")
		name := r.PathValue("name")
		key := r.PathValue("key")
		value, err := k8s.GetSecretValue(r.Context(), p, k8s.GetSecretValueArgs{
			Cluster: c, Namespace: ns, Name: name, Key: key,
		})
		if err != nil {
			slog.ErrorContext(r.Context(), "secret reveal failed",
				"err", err, "cluster", c.Name, "ns", ns, "name", name, "key", key,
				"actor", p.Actor())
			http.Error(w, "operation failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(value)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
