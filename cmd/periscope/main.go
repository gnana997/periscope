package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/auth"
	"github.com/gnana997/periscope/internal/authz"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	execsess "github.com/gnana997/periscope/internal/exec"
	"github.com/gnana997/periscope/internal/httpx"
	"github.com/gnana997/periscope/internal/k8s"
	"github.com/gnana997/periscope/internal/secrets"
	"github.com/gnana997/periscope/internal/spa"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Audit pipeline: stdout always on; SQLite attached when
	// PERISCOPE_AUDIT_ENABLED=true. Fail-open per design — if the
	// SQLite sink can't open (PVC not mounted, disk full, schema
	// migration error), we log a warning and continue with
	// stdout-only audit rather than block the pod from booting.
	ctx := context.Background()
	auditSinks := []audit.Sink{&audit.StdoutSink{Logger: logger}}
	auditCfg := audit.LoadSQLiteConfigFromEnv()
	var auditReader audit.Reader
	if auditCfg.Enabled {
		// Surface footguns (unbounded growth, hammer-disk vacuum
		// interval, cap > available disk) before they bite. Each
		// warning is independent; we never refuse to boot here.
		for _, w := range auditCfg.Validate() {
			slog.Warn(w)
		}
		sqliteSink, err := audit.OpenSQLiteSink(ctx, auditCfg)
		if err != nil {
			slog.Warn("audit: sqlite disabled (open failed)",
				"err", err, "path", auditCfg.Path)
		} else {
			auditSinks = append(auditSinks, sqliteSink)
			auditReader = sqliteSink
			slog.Info("audit: sqlite enabled",
				"path", auditCfg.Path,
				"retention_days", auditCfg.RetentionDays,
				"max_size_mb", auditCfg.MaxSizeMB)
		}
	}
	auditEmitter := audit.New(auditSinks...)

	factory, err := credentials.NewSharedIrsaFactory(ctx, nil)
	if err != nil {
		slog.Error("failed to initialize credentials factory", "err", err)
		os.Exit(1)
	}

	registry, err := loadRegistry()
	if err != nil {
		slog.Error("failed to load cluster registry", "err", err)
		os.Exit(1)
	}

	authCfg, err := auth.Load(os.Getenv("PERISCOPE_AUTH_FILE"))
	if err != nil {
		slog.Error("auth config", "err", err)
		os.Exit(1)
	}
	slog.Info("auth", "mode", authCfg.Mode)

	resolver := secrets.NewResolver(factory.AWSConfig())
	if err := auth.ResolveSecrets(ctx, &authCfg, resolver); err != nil {
		slog.Error("auth secrets", "err", err)
		os.Exit(1)
	}

	// Build the authz mode resolver from the loaded auth config and
	// attach it to the credentials factory. Done after ResolveSecrets
	// so the auth config is fully realized; done after factory
	// construction so the secrets resolver could use the same AWS
	// config without circular bootstrapping.
	authzResolver, err := authz.NewResolver(authz.Config{
		Mode:          authz.Mode(authCfg.Authorization.Mode),
		GroupTiers:    authCfg.Authorization.GroupTiers,
		DefaultTier:   authCfg.Authorization.DefaultTier,
		GroupPrefix:   authCfg.Authorization.GroupPrefix,
		GroupsClaim:   authCfg.Authorization.GroupsClaim,
		AllowedGroups: authCfg.Authorization.AllowedGroups,
	})
	if err != nil {
		slog.Error("authz resolver", "err", err)
		os.Exit(1)
	}
	factory.AttachResolver(authzResolver)
	slog.Info("authz", "mode", authzResolver.Mode())

	var authClient *auth.OIDCClient
	if authCfg.Mode == auth.ModeOIDC {
		authClient, err = auth.NewOIDCClient(ctx, authCfg.OIDC, authCfg.Authorization.GroupsClaim)
		if err != nil {
			slog.Error("oidc discovery", "err", err)
			os.Exit(1)
		}
	}
	sessionStore := auth.NewMemoryStore()
	go sessionStore.Run(ctx)

	var authMW func(http.Handler) http.Handler
	if authCfg.Mode == auth.ModeOIDC {
		authMW = auth.Middleware(authClient, sessionStore, authCfg)
	} else {
		authMW = auth.DevMiddleware(authCfg)
	}

	router := chi.NewRouter()
	router.Use(httpx.RequestID)
	router.Use(httpx.RealIP)
	router.Use(httpx.Recoverer)
	router.Use(httpx.AuditBegin)
	router.Use(authMW)
	auth.RegisterRoutes(router, authClient, sessionStore, authCfg, authzResolver)
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	router.Get("/api/whoami", credentials.Wrap(factory, whoami))
	router.Get("/api/clusters", listClustersHandler(registry))

	// Audit query endpoint. Registered only when SQLite is wired.
	// Authz is self-only by default; admins (tier mode) see all rows.
	if auditReader != nil {
		router.Get("/api/audit", auditQueryHandler(auditReader, authzResolver))
	}

	// --- Overview / dashboard ---

	router.Get("/api/clusters/{cluster}/dashboard", credentials.Wrap(factory,
		func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
			c, ok := registry.ByName(chi.URLParam(r, "cluster"))
			if !ok {
				http.Error(w, "cluster not found", http.StatusNotFound)
				return
			}
			summary, err := k8s.GetClusterSummary(r.Context(), p, k8s.GetClusterSummaryArgs{Cluster: c})
			if err != nil {
				slog.Error("cluster summary", "cluster", c.Name, "err", err)
				http.Error(w, err.Error(), httpStatusFor(err))
				return
			}
			writeJSON(w, http.StatusOK, summary)
		}))

	// --- Global search (Cmd+K palette) ---
	//
	// Returns up to N matches per kind across the cluster. Kinds and
	// limit are query params; both default to "everything" / 10. Errors
	// inside one kind do not fail the whole call — see SearchResources.
	router.Get("/api/clusters/{cluster}/search", credentials.Wrap(factory,
		func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
			c, ok := registry.ByName(chi.URLParam(r, "cluster"))
			if !ok {
				http.Error(w, "cluster not found", http.StatusNotFound)
				return
			}
			q := r.URL.Query().Get("q")
			kinds := parseSearchKinds(r.URL.Query().Get("kinds"))
			limit := 0
			if v := r.URL.Query().Get("limit"); v != "" {
				if n, err := strconv.Atoi(v); err == nil {
					limit = n
				}
			}
			results, err := k8s.SearchResources(r.Context(), p, k8s.SearchArgs{
				Cluster: c,
				Query:   q,
				Kinds:   kinds,
				Limit:   limit,
			})
			if err != nil {
				slog.Error("search", "cluster", c.Name, "err", err)
				http.Error(w, err.Error(), httpStatusFor(err))
				return
			}
			writeJSON(w, http.StatusOK, results)
		}))

	// --- CRDs + custom resources ---
	router.Get("/api/clusters/{cluster}/crds", credentials.Wrap(factory,
		func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
			c, ok := registry.ByName(chi.URLParam(r, "cluster"))
			if !ok {
				http.Error(w, "cluster not found", http.StatusNotFound)
				return
			}
			result, err := k8s.ListCRDs(r.Context(), p, c)
			if err != nil {
				slog.Error("list CRDs", "cluster", c.Name, "err", err)
				http.Error(w, err.Error(), httpStatusFor(err))
				return
			}
			writeJSON(w, http.StatusOK, result)
		}))

	router.Get("/api/clusters/{cluster}/customresources/{group}/{version}/{plural}",
		credentials.Wrap(factory, customResourceListHandler(registry)))
	router.Get("/api/clusters/{cluster}/customresources/{group}/{version}/{plural}/{ns}/{name}",
		credentials.Wrap(factory, customResourceDetailHandler(registry)))
	router.Get("/api/clusters/{cluster}/customresources/{group}/{version}/{plural}/{ns}/{name}/yaml",
		credentials.Wrap(factory, customResourceYAMLHandler(registry)))
	router.Get("/api/clusters/{cluster}/customresources/{group}/{version}/{plural}/{ns}/{name}/events",
		credentials.Wrap(factory, customResourceEventsHandler(registry)))

	// --- LIST endpoints ---

	router.Get("/api/clusters/{cluster}/nodes", credentials.Wrap(factory,
		listResource(registry, "nodes",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _ string) (k8s.NodeList, error) {
				return k8s.ListNodes(ctx, p, k8s.ListNodesArgs{Cluster: c})
			})))

	router.Get("/api/clusters/{cluster}/namespaces", credentials.Wrap(factory,
		listResource(registry, "namespaces",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _ string) (k8s.NamespaceList, error) {
				return k8s.ListNamespaces(ctx, p, k8s.ListNamespacesArgs{Cluster: c})
			})))

	router.Get("/api/clusters/{cluster}/pods", credentials.Wrap(factory,
		listResource(registry, "pods",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.PodList, error) {
				return k8s.ListPods(ctx, p, k8s.ListPodsArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/deployments", credentials.Wrap(factory,
		listResource(registry, "deployments",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.DeploymentList, error) {
				return k8s.ListDeployments(ctx, p, k8s.ListDeploymentsArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/statefulsets", credentials.Wrap(factory,
		listResource(registry, "statefulsets",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.StatefulSetList, error) {
				return k8s.ListStatefulSets(ctx, p, k8s.ListStatefulSetsArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/daemonsets", credentials.Wrap(factory,
		listResource(registry, "daemonsets",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.DaemonSetList, error) {
				return k8s.ListDaemonSets(ctx, p, k8s.ListDaemonSetsArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/services", credentials.Wrap(factory,
		listResource(registry, "services",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.ServiceList, error) {
				return k8s.ListServices(ctx, p, k8s.ListServicesArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/ingresses", credentials.Wrap(factory,
		listResource(registry, "ingresses",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.IngressList, error) {
				return k8s.ListIngresses(ctx, p, k8s.ListIngressesArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/configmaps", credentials.Wrap(factory,
		listResource(registry, "configmaps",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.ConfigMapList, error) {
				return k8s.ListConfigMaps(ctx, p, k8s.ListConfigMapsArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/secrets", credentials.Wrap(factory,
		listResource(registry, "secrets",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.SecretList, error) {
				return k8s.ListSecrets(ctx, p, k8s.ListSecretsArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/jobs", credentials.Wrap(factory,
		listResource(registry, "jobs",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.JobList, error) {
				return k8s.ListJobs(ctx, p, k8s.ListJobsArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/cronjobs", credentials.Wrap(factory,
		listResource(registry, "cronjobs",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.CronJobList, error) {
				return k8s.ListCronJobs(ctx, p, k8s.ListCronJobsArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/pvcs", credentials.Wrap(factory,
		listResource(registry, "pvcs",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.PVCList, error) {
				return k8s.ListPVCs(ctx, p, k8s.ListPVCsArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/pvs", credentials.Wrap(factory,
		listResource(registry, "pvs",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _ string) (k8s.PVList, error) {
				return k8s.ListPVs(ctx, p, k8s.ListPVsArgs{Cluster: c})
			})))

	router.Get("/api/clusters/{cluster}/storageclasses", credentials.Wrap(factory,
		listResource(registry, "storageclasses",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _ string) (k8s.StorageClassList, error) {
				return k8s.ListStorageClasses(ctx, p, k8s.ListStorageClassesArgs{Cluster: c})
			})))

	router.Get("/api/clusters/{cluster}/roles", credentials.Wrap(factory,
		listResource(registry, "roles",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.RoleList, error) {
				return k8s.ListRoles(ctx, p, k8s.ListRolesArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/clusterroles", credentials.Wrap(factory,
		listResource(registry, "clusterroles",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _ string) (k8s.ClusterRoleList, error) {
				return k8s.ListClusterRoles(ctx, p, k8s.ListClusterRolesArgs{Cluster: c})
			})))

	router.Get("/api/clusters/{cluster}/rolebindings", credentials.Wrap(factory,
		listResource(registry, "rolebindings",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.RoleBindingList, error) {
				return k8s.ListRoleBindings(ctx, p, k8s.ListRoleBindingsArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/clusterrolebindings", credentials.Wrap(factory,
		listResource(registry, "clusterrolebindings",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _ string) (k8s.ClusterRoleBindingList, error) {
				return k8s.ListClusterRoleBindings(ctx, p, k8s.ListClusterRoleBindingsArgs{Cluster: c})
			})))

	router.Get("/api/clusters/{cluster}/serviceaccounts", credentials.Wrap(factory,
		listResource(registry, "serviceaccounts",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.ServiceAccountList, error) {
				return k8s.ListServiceAccounts(ctx, p, k8s.ListServiceAccountsArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/horizontalpodautoscalers", credentials.Wrap(factory,
		listResource(registry, "horizontalpodautoscalers",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.HPAList, error) {
				return k8s.ListHPAs(ctx, p, k8s.ListHPAsArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/poddisruptionbudgets", credentials.Wrap(factory,
		listResource(registry, "poddisruptionbudgets",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.PDBList, error) {
				return k8s.ListPDBs(ctx, p, k8s.ListPDBsArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/replicasets", credentials.Wrap(factory,
		listResource(registry, "replicasets",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.ReplicaSetList, error) {
				return k8s.ListReplicaSets(ctx, p, k8s.ListReplicaSetsArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/networkpolicies", credentials.Wrap(factory,
		listResource(registry, "networkpolicies",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.NetworkPolicyList, error) {
				return k8s.ListNetworkPolicies(ctx, p, k8s.ListNetworkPoliciesArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/resourcequotas", credentials.Wrap(factory,
		listResource(registry, "resourcequotas",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.ResourceQuotaList, error) {
				return k8s.ListResourceQuotas(ctx, p, k8s.ListResourceQuotasArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/limitranges", credentials.Wrap(factory,
		listResource(registry, "limitranges",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.LimitRangeList, error) {
				return k8s.ListLimitRanges(ctx, p, k8s.ListLimitRangesArgs{Cluster: c, Namespace: ns})
			})))

	router.Get("/api/clusters/{cluster}/ingressclasses", credentials.Wrap(factory,
		listResource(registry, "ingressclasses",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _ string) (k8s.IngressClassList, error) {
				return k8s.ListIngressClasses(ctx, p, k8s.ListIngressClassesArgs{Cluster: c})
			})))

	router.Get("/api/clusters/{cluster}/priorityclasses", credentials.Wrap(factory,
		listResource(registry, "priorityclasses",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _ string) (k8s.PriorityClassList, error) {
				return k8s.ListPriorityClasses(ctx, p, k8s.ListPriorityClassesArgs{Cluster: c})
			})))

	router.Get("/api/clusters/{cluster}/runtimeclasses", credentials.Wrap(factory,
		listResource(registry, "runtimeclasses",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _ string) (k8s.RuntimeClassList, error) {
				return k8s.ListRuntimeClasses(ctx, p, k8s.ListRuntimeClassesArgs{Cluster: c})
			})))

	router.Get("/api/clusters/{cluster}/pods/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "pod",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.PodDetail, error) {
				return k8s.GetPod(ctx, p, k8s.GetPodArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/deployments/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "deployment",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.DeploymentDetail, error) {
				return k8s.GetDeployment(ctx, p, k8s.GetDeploymentArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/statefulsets/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "statefulset",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.StatefulSetDetail, error) {
				return k8s.GetStatefulSet(ctx, p, k8s.GetStatefulSetArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/daemonsets/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "daemonset",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.DaemonSetDetail, error) {
				return k8s.GetDaemonSet(ctx, p, k8s.GetDaemonSetArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/services/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "service",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.ServiceDetail, error) {
				return k8s.GetService(ctx, p, k8s.GetServiceArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/ingresses/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "ingress",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.IngressDetail, error) {
				return k8s.GetIngress(ctx, p, k8s.GetIngressArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/configmaps/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "configmap",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.ConfigMapDetail, error) {
				return k8s.GetConfigMap(ctx, p, k8s.GetConfigMapArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/secrets/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "secret",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.SecretDetail, error) {
				return k8s.GetSecret(ctx, p, k8s.GetSecretArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/jobs/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "job",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.JobDetail, error) {
				return k8s.GetJob(ctx, p, k8s.GetJobArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/cronjobs/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "cronjob",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.CronJobDetail, error) {
				return k8s.GetCronJob(ctx, p, k8s.GetCronJobArgs{Cluster: c, Namespace: ns, Name: name})
			})))


	router.Post("/api/clusters/{cluster}/cronjobs/{ns}/{name}/trigger",
		credentials.Wrap(factory, triggerCronJobHandler(registry, auditEmitter)))
	router.Get("/api/clusters/{cluster}/pvcs/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "pvc",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.PVCDetail, error) {
				return k8s.GetPVC(ctx, p, k8s.GetPVCArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/roles/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "role",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.RoleDetail, error) {
				return k8s.GetRole(ctx, p, k8s.GetRoleArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/rolebindings/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "rolebinding",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.RoleBindingDetail, error) {
				return k8s.GetRoleBinding(ctx, p, k8s.GetRoleBindingArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/serviceaccounts/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "serviceaccount",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.ServiceAccountDetail, error) {
				return k8s.GetServiceAccount(ctx, p, k8s.GetServiceAccountArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/horizontalpodautoscalers/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "hpa",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.HPADetail, error) {
				return k8s.GetHPA(ctx, p, k8s.GetHPAArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/poddisruptionbudgets/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "pdb",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.PDBDetail, error) {
				return k8s.GetPDB(ctx, p, k8s.GetPDBArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/replicasets/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "replicaset",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.ReplicaSetDetail, error) {
				return k8s.GetReplicaSet(ctx, p, k8s.GetReplicaSetArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/networkpolicies/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "networkpolicy",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.NetworkPolicyDetail, error) {
				return k8s.GetNetworkPolicy(ctx, p, k8s.GetNetworkPolicyArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/resourcequotas/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "resourcequota",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.ResourceQuota, error) {
				return k8s.GetResourceQuota(ctx, p, k8s.GetResourceQuotaArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/limitranges/{ns}/{name}", credentials.Wrap(factory,
		detailHandler(registry, "limitrange",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.LimitRangeDetail, error) {
				return k8s.GetLimitRange(ctx, p, k8s.GetLimitRangeArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/ingressclasses/{name}", credentials.Wrap(factory,
		detailHandler(registry, "ingressclass",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (k8s.IngressClassDetail, error) {
				return k8s.GetIngressClass(ctx, p, k8s.GetIngressClassArgs{Cluster: c, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/priorityclasses/{name}", credentials.Wrap(factory,
		detailHandler(registry, "priorityclass",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (k8s.PriorityClassDetail, error) {
				return k8s.GetPriorityClass(ctx, p, k8s.GetPriorityClassArgs{Cluster: c, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/runtimeclasses/{name}", credentials.Wrap(factory,
		detailHandler(registry, "runtimeclass",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (k8s.RuntimeClassDetail, error) {
				return k8s.GetRuntimeClass(ctx, p, k8s.GetRuntimeClassArgs{Cluster: c, Name: name})
			})))

	// Nodes, Namespaces, PVs, and StorageClasses are cluster-scoped: no {ns} segment.
	router.Get("/api/clusters/{cluster}/nodes/{name}", credentials.Wrap(factory,
		detailHandler(registry, "node",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (k8s.NodeDetail, error) {
				return k8s.GetNode(ctx, p, k8s.GetNodeArgs{Cluster: c, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/namespaces/{name}", credentials.Wrap(factory,
		detailHandler(registry, "namespace",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (k8s.NamespaceDetail, error) {
				return k8s.GetNamespace(ctx, p, k8s.GetNamespaceArgs{Cluster: c, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/pvs/{name}", credentials.Wrap(factory,
		detailHandler(registry, "pv",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (k8s.PVDetail, error) {
				return k8s.GetPV(ctx, p, k8s.GetPVArgs{Cluster: c, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/storageclasses/{name}", credentials.Wrap(factory,
		detailHandler(registry, "storageclass",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (k8s.StorageClassDetail, error) {
				return k8s.GetStorageClass(ctx, p, k8s.GetStorageClassArgs{Cluster: c, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/clusterroles/{name}", credentials.Wrap(factory,
		detailHandler(registry, "clusterrole",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (k8s.ClusterRoleDetail, error) {
				return k8s.GetClusterRole(ctx, p, k8s.GetClusterRoleArgs{Cluster: c, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/clusterrolebindings/{name}", credentials.Wrap(factory,
		detailHandler(registry, "clusterrolebinding",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (k8s.ClusterRoleBindingDetail, error) {
				return k8s.GetClusterRoleBinding(ctx, p, k8s.GetClusterRoleBindingArgs{Cluster: c, Name: name})
			})))

	// --- Metrics endpoints ---

	router.Get("/api/clusters/{cluster}/nodes/{name}/metrics", credentials.Wrap(factory,
		detailHandler(registry, "node-metrics",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (k8s.NodeMetrics, error) {
				return k8s.GetNodeMetrics(ctx, p, k8s.GetNodeArgs{Cluster: c, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/pods/{ns}/{name}/metrics", credentials.Wrap(factory,
		detailHandler(registry, "pod-metrics",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (k8s.PodMetrics, error) {
				return k8s.GetPodMetrics(ctx, p, k8s.GetPodArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	// --- YAML endpoints ---

	router.Get("/api/clusters/{cluster}/pods/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "pod",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetPodYAML(ctx, p, k8s.GetPodArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/deployments/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "deployment",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetDeploymentYAML(ctx, p, k8s.GetDeploymentArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/statefulsets/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "statefulset",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetStatefulSetYAML(ctx, p, k8s.GetStatefulSetArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/daemonsets/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "daemonset",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetDaemonSetYAML(ctx, p, k8s.GetDaemonSetArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/services/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "service",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetServiceYAML(ctx, p, k8s.GetServiceArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/ingresses/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "ingress",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetIngressYAML(ctx, p, k8s.GetIngressArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/configmaps/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "configmap",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetConfigMapYAML(ctx, p, k8s.GetConfigMapArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/secrets/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "secret",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetSecretYAML(ctx, p, k8s.GetSecretArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/jobs/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "job",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetJobYAML(ctx, p, k8s.GetJobArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/cronjobs/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "cronjob",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetCronJobYAML(ctx, p, k8s.GetCronJobArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/namespaces/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "namespace",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (string, error) {
				return k8s.GetNamespaceYAML(ctx, p, k8s.GetNamespaceArgs{Cluster: c, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/pvcs/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "pvc",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetPVCYAML(ctx, p, k8s.GetPVCArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/pvs/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "pv",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (string, error) {
				return k8s.GetPVYAML(ctx, p, k8s.GetPVArgs{Cluster: c, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/storageclasses/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "storageclass",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (string, error) {
				return k8s.GetStorageClassYAML(ctx, p, k8s.GetStorageClassArgs{Cluster: c, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/roles/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "role",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetRoleYAML(ctx, p, k8s.GetRoleArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/clusterroles/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "clusterrole",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (string, error) {
				return k8s.GetClusterRoleYAML(ctx, p, k8s.GetClusterRoleArgs{Cluster: c, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/rolebindings/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "rolebinding",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetRoleBindingYAML(ctx, p, k8s.GetRoleBindingArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/clusterrolebindings/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "clusterrolebinding",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (string, error) {
				return k8s.GetClusterRoleBindingYAML(ctx, p, k8s.GetClusterRoleBindingArgs{Cluster: c, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/serviceaccounts/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "serviceaccount",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetServiceAccountYAML(ctx, p, k8s.GetServiceAccountArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/horizontalpodautoscalers/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "hpa",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetHPAYAML(ctx, p, k8s.GetHPAArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/poddisruptionbudgets/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "pdb",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetPDBYAML(ctx, p, k8s.GetPDBArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/replicasets/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "replicaset",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetReplicaSetYAML(ctx, p, k8s.GetReplicaSetArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/networkpolicies/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "networkpolicy",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetNetworkPolicyYAML(ctx, p, k8s.GetNetworkPolicyArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/resourcequotas/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "resourcequota",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetResourceQuotaYAML(ctx, p, k8s.GetResourceQuotaArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/limitranges/{ns}/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "limitrange",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns, name string) (string, error) {
				return k8s.GetLimitRangeYAML(ctx, p, k8s.GetLimitRangeArgs{Cluster: c, Namespace: ns, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/ingressclasses/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "ingressclass",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (string, error) {
				return k8s.GetIngressClassYAML(ctx, p, k8s.GetIngressClassArgs{Cluster: c, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/priorityclasses/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "priorityclass",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (string, error) {
				return k8s.GetPriorityClassYAML(ctx, p, k8s.GetPriorityClassArgs{Cluster: c, Name: name})
			})))

	router.Get("/api/clusters/{cluster}/runtimeclasses/{name}/yaml", credentials.Wrap(factory,
		yamlHandler(registry, "runtimeclass",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, _, name string) (string, error) {
				return k8s.GetRuntimeClassYAML(ctx, p, k8s.GetRuntimeClassArgs{Cluster: c, Name: name})
			})))

	// --- Cluster-wide events list ---

	router.Get("/api/clusters/{cluster}/events", credentials.Wrap(factory,
		listResource(registry, "events",
			func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (k8s.ClusterEventList, error) {
				return k8s.ListClusterEvents(ctx, p, k8s.ListClusterEventsArgs{Cluster: c, Namespace: ns})
			})))

	// --- Events endpoints (per object) ---

	router.Get("/api/clusters/{cluster}/pods/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "Pod")))
	router.Get("/api/clusters/{cluster}/deployments/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "Deployment")))
	router.Get("/api/clusters/{cluster}/statefulsets/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "StatefulSet")))
	router.Get("/api/clusters/{cluster}/daemonsets/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "DaemonSet")))
	router.Get("/api/clusters/{cluster}/services/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "Service")))
	router.Get("/api/clusters/{cluster}/ingresses/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "Ingress")))
	router.Get("/api/clusters/{cluster}/configmaps/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "ConfigMap")))
	router.Get("/api/clusters/{cluster}/secrets/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "Secret")))
	router.Get("/api/clusters/{cluster}/jobs/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "Job")))
	router.Get("/api/clusters/{cluster}/cronjobs/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "CronJob")))
	router.Get("/api/clusters/{cluster}/namespaces/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "Namespace")))
	router.Get("/api/clusters/{cluster}/pvcs/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "PersistentVolumeClaim")))
	router.Get("/api/clusters/{cluster}/pvs/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "PersistentVolume")))
	router.Get("/api/clusters/{cluster}/storageclasses/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "StorageClass")))

	// --- New Tier 1 + 2 events endpoints ---
	router.Get("/api/clusters/{cluster}/horizontalpodautoscalers/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "HorizontalPodAutoscaler")))
	router.Get("/api/clusters/{cluster}/poddisruptionbudgets/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "PodDisruptionBudget")))
	router.Get("/api/clusters/{cluster}/replicasets/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "ReplicaSet")))
	router.Get("/api/clusters/{cluster}/networkpolicies/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "NetworkPolicy")))
	router.Get("/api/clusters/{cluster}/resourcequotas/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "ResourceQuota")))
	router.Get("/api/clusters/{cluster}/limitranges/{ns}/{name}/events",
		credentials.Wrap(factory, eventsHandler(registry, "LimitRange")))
	// --- Logs (SSE streaming) endpoints ---

	router.Get("/api/clusters/{cluster}/pods/{ns}/{name}/logs",
		credentials.Wrap(factory, podLogsHandler(registry)))
	router.Get("/api/clusters/{cluster}/deployments/{ns}/{name}/logs",
		credentials.Wrap(factory, workloadLogsHandler(registry, "deployment", k8s.StreamDeploymentLogs)))
	router.Get("/api/clusters/{cluster}/statefulsets/{ns}/{name}/logs",
		credentials.Wrap(factory, workloadLogsHandler(registry, "statefulset", k8s.StreamStatefulSetLogs)))
	router.Get("/api/clusters/{cluster}/daemonsets/{ns}/{name}/logs",
		credentials.Wrap(factory, workloadLogsHandler(registry, "daemonset", k8s.StreamDaemonSetLogs)))
	router.Get("/api/clusters/{cluster}/jobs/{ns}/{name}/logs",
		credentials.Wrap(factory, workloadLogsHandler(registry, "job", k8s.StreamJobLogs)))

	// --- Secret reveal endpoint (audit-logged, per-key) ---

	router.Get("/api/clusters/{cluster}/secrets/{ns}/{name}/data/{key}",
		credentials.Wrap(factory, secretRevealHandler(registry, auditEmitter)))

	// --- Generic resource mutation endpoints (PR-D) ---
	//
	// One pair of routes covers every editable resource — Pod, Deployment,
	// ConfigMap, your CRDs, all of them. The handler dispatches via the
	// dynamic client, so adding new resource types is zero new code.
	//
	// Group "core" maps to the empty K8s API group (kubectl api-resources
	// shows "" for v1 resources; we use a literal "core" in the URL because
	// URL segments can't be empty). Namespaced and cluster-scoped have
	// separate route patterns because chi doesn't allow optional URL params.
	router.Patch("/api/clusters/{cluster}/resources/{group}/{version}/{resource}/{ns}/{name}",
		credentials.Wrap(factory, applyResourceHandler(registry, auditEmitter)))
	router.Patch("/api/clusters/{cluster}/resources/{group}/{version}/{resource}/{name}",
		credentials.Wrap(factory, applyResourceHandler(registry, auditEmitter)))
	router.Delete("/api/clusters/{cluster}/resources/{group}/{version}/{resource}/{ns}/{name}",
		credentials.Wrap(factory, deleteResourceHandler(registry, auditEmitter)))
	router.Delete("/api/clusters/{cluster}/resources/{group}/{version}/{resource}/{name}",
		credentials.Wrap(factory, deleteResourceHandler(registry, auditEmitter)))

	// --- Resource meta (resourceVersion + generation + managedFields) ---
	// Drives the inline editor's per-field ownership badges and (Phase 3)
	// drift detection. Separate from the YAML GET because formatYAML strips
	// managedFields for display; the editor needs them out-of-band.
	router.Get("/api/clusters/{cluster}/resources/{group}/{version}/{resource}/{ns}/{name}/meta",
		credentials.Wrap(factory, metaResourceHandler(registry)))
	router.Get("/api/clusters/{cluster}/resources/{group}/{version}/{resource}/{name}/meta",
		credentials.Wrap(factory, metaResourceHandler(registry)))

	// --- OpenAPI v3 schema proxy ---
	// Lazy per-group-version fetch, in-memory cached per (cluster, path).
	// Powers monaco-yaml schema validation/autocomplete/hover in the SPA
	// editor. Always exact for the user's actual cluster — handles version
	// drift across the fleet (1.28–1.36) and CRD schemas for free.
	router.Get("/api/clusters/{cluster}/openapi/v3",
		credentials.Wrap(factory, openAPIHandler(registry)))
	router.Get("/api/clusters/{cluster}/openapi/v3/*",
		credentials.Wrap(factory, openAPIHandler(registry)))

	// --- Pod exec (interactive shell, WebSocket) — RFC 0001 ---
	execSessions := execsess.NewRegistry()
	execPolicy := k8s.NewPolicy(k8s.PolicyConfig{}) // defaults: 3 WS fails / 30m SPDY pin
	router.Get("/api/clusters/{cluster}/pods/{ns}/{name}/exec",
		credentials.Wrap(factory, execHandler(registry, execSessions, execPolicy, auditEmitter)))
	if os.Getenv("PERISCOPE_PROBE_CLUSTERS_ON_BOOT") == "1" {
		go probeClustersOnBoot(ctx, factory, registry, execPolicy)
	}

	if h := spa.Handler(); h != nil {
		router.NotFound(h.ServeHTTP)
		slog.Info("spa", "embedded", true)
	} else {
		slog.Info("spa", "embedded", false)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port
	slog.Info("periscope starting", "addr", addr, "clusters", len(registry.List()))
	srv := &http.Server{Addr: addr, Handler: router}

	// SIGTERM/SIGINT trigger a drain: stop accepting new conns, let in-flight
	// requests finish for up to 25s. Pair with chart-side terminationGracePeriodSeconds=30
	// (5s headroom before SIGKILL).
	shutdownCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			slog.Error("server failed", "err", err)
			os.Exit(1)
		}
	case <-shutdownCtx.Done():
		slog.Info("shutdown signal received, draining")
		drainCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if err := srv.Shutdown(drainCtx); err != nil {
			slog.Error("graceful shutdown failed", "err", err)
			os.Exit(1)
		}
		slog.Info("server stopped cleanly")
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

// listClustersHandler returns the registered clusters with PR4's
// execEnabled bit so the SPA can hide the Open Shell action and filter
// the empty-picker dropdown when an operator has set
// `exec.enabled: false` on a given cluster.
//
// We project the registry's Cluster slice into an explicit response
// shape rather than letting json marshal Cluster directly: the Exec
// config block stays out of the API surface (it's cluster-config, not
// cluster-identity), but its single derived bit comes through.
func listClustersHandler(reg *clusters.Registry) http.HandlerFunc {
	type clusterDTO struct {
		Name              string `json:"name"`
		Backend           string `json:"backend"`
		ARN               string `json:"arn,omitempty"`
		Region            string `json:"region,omitempty"`
		KubeconfigPath    string `json:"kubeconfigPath,omitempty"`
		KubeconfigContext string `json:"kubeconfigContext,omitempty"`
		ExecEnabled       bool   `json:"execEnabled"`
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		in := reg.List()
		out := make([]clusterDTO, 0, len(in))
		for _, c := range in {
			out = append(out, clusterDTO{
				Name:              c.Name,
				Backend:           c.Backend,
				ARN:               c.ARN,
				Region:            c.Region,
				KubeconfigPath:    c.KubeconfigPath,
				KubeconfigContext: c.KubeconfigContext,
				ExecEnabled:       c.ExecEnabled(),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"clusters": out})
	}
}

// listResource wraps a list-style operation.
func listResource[Resp any](
	reg *clusters.Registry,
	resource string,
	op func(ctx context.Context, p credentials.Provider, c clusters.Cluster, ns string) (Resp, error),
) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		result, err := op(r.Context(), p, c, r.URL.Query().Get("namespace"))
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.ErrorContext(r.Context(), "list operation failed",
				"resource", resource, "err", err,
				"cluster", c.Name, "actor", p.Actor())
			http.Error(w, "operation failed", httpStatusFor(err))
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
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		ns := chi.URLParam(r, "ns")
		name := chi.URLParam(r, "name")
		result, err := op(r.Context(), p, c, ns, name)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.ErrorContext(r.Context(), "get operation failed",
				"resource", resource, "err", err,
				"cluster", c.Name, "ns", ns, "name", name, "actor", p.Actor())
			http.Error(w, "operation failed", httpStatusFor(err))
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
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		ns := chi.URLParam(r, "ns")
		name := chi.URLParam(r, "name")
		result, err := op(r.Context(), p, c, ns, name)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.ErrorContext(r.Context(), "yaml operation failed",
				"resource", resource, "err", err,
				"cluster", c.Name, "ns", ns, "name", name, "actor", p.Actor())
			http.Error(w, "operation failed", httpStatusFor(err))
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
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		ns := chi.URLParam(r, "ns")
		name := chi.URLParam(r, "name")
		result, err := k8s.ListObjectEvents(r.Context(), p, k8s.ListObjectEventsArgs{
			Cluster: c, Kind: kind, Namespace: ns, Name: name,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.ErrorContext(r.Context(), "events operation failed",
				"kind", kind, "err", err,
				"cluster", c.Name, "ns", ns, "name", name, "actor", p.Actor())
			http.Error(w, "operation failed", httpStatusFor(err))
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// applyResourceHandler is the HTTP front-end of k8s.ApplyResource. It
// reads the YAML body, dispatches to the dynamic client under the
// caller's impersonated identity, and returns the post-apply state as
// JSON. Both namespaced and cluster-scoped routes share this handler;
// chi.URLParam("ns") returns "" for the cluster-scoped variant, which
// is exactly what ApplyResource expects.
//
// Query params:
//   - dryRun=true  → run as ?dryRun=All (no commit, returns the would-be state)
//   - force=true   → resolve field-manager conflicts (caller opts in,
//     usually on the second attempt after a 409)
//
// Group "core" in the URL is normalised to the empty string before
// reaching ApplyResource, mirroring how K8s itself models the v1 group.
func applyResourceHandler(reg *clusters.Registry, auditer *audit.Emitter) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		group := chi.URLParam(r, "group")
		if group == "core" {
			group = ""
		}
		body, err := readApplyBody(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		args := k8s.ApplyResourceArgs{
			Cluster:   c,
			Group:     group,
			Version:   chi.URLParam(r, "version"),
			Resource:  chi.URLParam(r, "resource"),
			Namespace: chi.URLParam(r, "ns"),
			Name:      chi.URLParam(r, "name"),
			Body:      body,
			DryRun:    r.URL.Query().Get("dryRun") == "true",
			Force:     r.URL.Query().Get("force") == "true",
		}
		evt := audit.Event{
			Actor:   actorFromContext(r.Context()),
			Verb:    audit.VerbApply,
			Cluster: c.Name,
			Resource: audit.ResourceRef{
				Group: args.Group, Version: args.Version, Resource: args.Resource,
				Namespace: args.Namespace, Name: args.Name,
			},
			Extra: map[string]any{
				"dryRun": args.DryRun,
				"force":  args.Force,
			},
		}
		result, err := k8s.ApplyResource(r.Context(), p, args)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			evt.Outcome = outcomeFor(err)
			evt.Reason = err.Error()
			auditer.Record(r.Context(), evt)
			// L1/L2 errors don't satisfy kerrors.* so they default to 500;
			// detect our own error prefix and surface as 400 instead so
			// the SPA can show "your YAML was malformed" rather than a
			// generic backend error.
			status := httpStatusFor(err)
			if strings.HasPrefix(err.Error(), "apply: ") {
				status = http.StatusBadRequest
			}
			writeAPIError(w, err, status)
			return
		}
		evt.Outcome = audit.OutcomeSuccess
		auditer.Record(r.Context(), evt)
		writeJSON(w, http.StatusOK, result)
	}
}

// triggerCronJobHandler implements POST /api/clusters/{c}/cronjobs/{ns}/{name}/trigger
// — the SPA's "Trigger now" affordance. Clones the CronJob's
// JobTemplate into a freshly-named Job, matching the semantics of
// `kubectl create job <name> --from=cronjob/<src>`. Audit-logged.
func triggerCronJobHandler(reg *clusters.Registry, auditer *audit.Emitter) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		args := k8s.TriggerCronJobArgs{
			Cluster:   c,
			Namespace: chi.URLParam(r, "ns"),
			Name:      chi.URLParam(r, "name"),
		}
		evt := audit.Event{
			Actor:   actorFromContext(r.Context()),
			Verb:    audit.VerbTrigger,
			Cluster: c.Name,
			Resource: audit.ResourceRef{
				Group: "batch", Version: "v1", Resource: "cronjobs",
				Namespace: args.Namespace, Name: args.Name,
			},
		}
		result, err := k8s.TriggerCronJob(r.Context(), p, args)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			evt.Outcome = outcomeFor(err)
			evt.Reason = err.Error()
			auditer.Record(r.Context(), evt)
			writeAPIError(w, err, httpStatusFor(err))
			return
		}
		evt.Outcome = audit.OutcomeSuccess
		evt.Extra = map[string]any{"jobName": result.JobName}
		auditer.Record(r.Context(), evt)
		writeJSON(w, http.StatusOK, result)
	}
}

// deleteResourceHandler is the symmetric DELETE entry point. Same
// route-param handling as applyResourceHandler. Audit-logged.
func deleteResourceHandler(reg *clusters.Registry, auditer *audit.Emitter) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		group := chi.URLParam(r, "group")
		if group == "core" {
			group = ""
		}
		args := k8s.DeleteResourceArgs{
			Cluster:   c,
			Group:     group,
			Version:   chi.URLParam(r, "version"),
			Resource:  chi.URLParam(r, "resource"),
			Namespace: chi.URLParam(r, "ns"),
			Name:      chi.URLParam(r, "name"),
		}
		evt := audit.Event{
			Actor:   actorFromContext(r.Context()),
			Verb:    audit.VerbDelete,
			Cluster: c.Name,
			Resource: audit.ResourceRef{
				Group: args.Group, Version: args.Version, Resource: args.Resource,
				Namespace: args.Namespace, Name: args.Name,
			},
		}
		if err := k8s.DeleteResource(r.Context(), p, args); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			evt.Outcome = outcomeFor(err)
			evt.Reason = err.Error()
			auditer.Record(r.Context(), evt)
			http.Error(w, err.Error(), httpStatusFor(err))
			return
		}
		evt.Outcome = audit.OutcomeSuccess
		auditer.Record(r.Context(), evt)
		w.WriteHeader(http.StatusNoContent)
	}
}

// openAPIHandler proxies the cluster's apiserver /openapi/v3 (and
// sub-paths) through to the SPA, with in-memory caching. The wildcard
// route uses chi's "*" param so the single handler covers both the
// discovery doc (no suffix) and per-group fetches like apis/apps/v1.
//
// Not audit-logged: schemas are public-info-viewer reads; no mutation
// happens here. Cache-Control is set so the browser can also short-
// circuit subsequent loads within a session.
func openAPIHandler(reg *clusters.Registry) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		path := chi.URLParam(r, "*")
		result, err := k8s.GetOpenAPI(r.Context(), p, k8s.OpenAPIArgs{
			Cluster: c,
			Path:    path,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.ErrorContext(r.Context(), "openapi fetch failed",
				"err", err, "cluster", c.Name, "path", path, "actor", p.Actor())
			http.Error(w, err.Error(), httpStatusFor(err))
			return
		}
		w.Header().Set("Content-Type", result.ContentType)
		// SPA-side react-query also caches; this layer is for browser
		// caching across react-query rehydrations and reloads.
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(result.Body)
	}
}

// metaResourceHandler returns resourceVersion, generation, and
// managedFields for the resource at the URL ref. Used by the inline
// editor to render per-field ownership badges and (Phase 3) drift
// detection. Same chi.URLParam shape as applyResourceHandler so the
// SPA reuses its resourceURL builder.
//
// Not audit-logged: this is a read of metadata the user already had
// access to via the YAML GET. RBAC: same as the apiserver's "get"
// verb on the resource (apiserver enforces).
func metaResourceHandler(reg *clusters.Registry) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		group := chi.URLParam(r, "group")
		if group == "core" {
			group = ""
		}
		args := k8s.MetaArgs{
			Cluster:   c,
			Group:     group,
			Version:   chi.URLParam(r, "version"),
			Resource:  chi.URLParam(r, "resource"),
			Namespace: chi.URLParam(r, "ns"),
			Name:      chi.URLParam(r, "name"),
		}
		result, err := k8s.GetResourceMeta(r.Context(), p, args)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.ErrorContext(r.Context(), "meta operation failed",
				"err", err, "cluster", c.Name,
				"group", args.Group, "version", args.Version, "resource", args.Resource,
				"ns", args.Namespace, "name", args.Name, "actor", p.Actor())
			http.Error(w, err.Error(), httpStatusFor(err))
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// readApplyBody pulls the YAML body off the request, capping the read
// at MaxApplyBytes+1 so a deliberately oversized payload returns a
// clean 400 rather than streaming megabytes into memory. The +1 byte
// is the sentinel: if the LimitReader fills, we know the payload was
// at least cap+1 bytes long (over the limit).
func readApplyBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, int64(k8s.MaxApplyBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if len(body) > k8s.MaxApplyBytes {
		return nil, fmt.Errorf("body exceeds %d bytes", k8s.MaxApplyBytes)
	}
	return body, nil
}

// secretRevealHandler wraps GetSecretValue. Audit emission lives
// here (not inside the k8s package) so the audit pipeline stays at
// the handler layer alongside apply/delete/exec, and the k8s
// package has no awareness of audit.
func secretRevealHandler(reg *clusters.Registry, auditer *audit.Emitter) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		ns := chi.URLParam(r, "ns")
		name := chi.URLParam(r, "name")
		key := chi.URLParam(r, "key")
		evt := audit.Event{
			Actor:   actorFromContext(r.Context()),
			Verb:    audit.VerbSecretReveal,
			Cluster: c.Name,
			Resource: audit.ResourceRef{
				Group: "", Version: "v1", Resource: "secrets",
				Namespace: ns, Name: name,
			},
			Extra: map[string]any{"key": key},
		}
		value, err := k8s.GetSecretValue(r.Context(), p, k8s.GetSecretValueArgs{
			Cluster: c, Namespace: ns, Name: name, Key: key,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			evt.Outcome = outcomeFor(err)
			evt.Reason = err.Error()
			auditer.Record(r.Context(), evt)
			http.Error(w, "operation failed", httpStatusFor(err))
			return
		}
		evt.Outcome = audit.OutcomeSuccess
		evt.Extra["size"] = len(value)
		auditer.Record(r.Context(), evt)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(value)
	}
}

// podLogsHandler streams a single pod's logs as Server-Sent Events.
//
// Each log line is emitted as: data: {"t":"<RFC3339Nano>","l":"<message>"}
// followed by an empty line. A heartbeat comment ": ping" is sent every 15s
// so reverse proxies don't sever idle connections during quiet periods.
func podLogsHandler(reg *clusters.Registry) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		q := r.URL.Query()
		args := k8s.PodLogsArgs{
			Cluster:    c,
			Namespace:  chi.URLParam(r, "ns"),
			Name:       chi.URLParam(r, "name"),
			Container:  q.Get("container"),
			Previous:   q.Get("previous") == "true",
			Follow:     q.Get("follow") != "false",
			Timestamps: true,
		}
		if v := q.Get("tailLines"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				args.TailLines = &n
			}
		}
		if v := q.Get("sinceSeconds"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				args.SinceSeconds = &n
			}
		}

		stream, err := k8s.OpenPodLogStream(r.Context(), p, args)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.ErrorContext(r.Context(), "open pod log stream failed",
				"err", err, "cluster", c.Name, "ns", args.Namespace,
				"name", args.Name, "container", args.Container, "actor", p.Actor())
			http.Error(w, "open log stream failed", http.StatusBadGateway)
			return
		}
		defer stream.Close()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		// Lines are read off the upstream in a goroutine so the main loop
		// can multiplex line emission with the heartbeat ticker. Only the
		// main loop writes to w (ResponseWriter is not goroutine-safe).
		type scanResult struct {
			line string
			err  error
			eof  bool
		}
		lineCh := make(chan scanResult, 64)
		go func() {
			scanner := bufio.NewScanner(stream)
			scanner.Buffer(make([]byte, 64*1024), 1<<20) // up to 1 MiB per line
			for scanner.Scan() {
				select {
				case <-r.Context().Done():
					return
				case lineCh <- scanResult{line: scanner.Text()}:
				}
			}
			select {
			case <-r.Context().Done():
			case lineCh <- scanResult{err: scanner.Err(), eof: true}:
			}
		}()

		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()

		type linePayload struct {
			T string `json:"t,omitempty"`
			L string `json:"l"`
		}

		for {
			select {
			case <-r.Context().Done():
				return
			case <-heartbeat.C:
				_, _ = fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			case res := <-lineCh:
				if res.eof {
					if res.err != nil && !errors.Is(res.err, context.Canceled) {
						payload, _ := json.Marshal(map[string]string{"message": res.err.Error()})
						_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", payload)
					} else {
						_, _ = fmt.Fprint(w, "event: done\ndata: {}\n\n")
					}
					flusher.Flush()
					return
				}
				ts, msg := k8s.SplitLogTimestamp(res.line)
				payload, _ := json.Marshal(linePayload{T: ts, L: msg})
				_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// deploymentEvent is an internal handle the workloadLogsHandler uses to
// fan in lines and pod-set updates from any k8s.Stream*Logs into the
// single goroutine that owns the SSE ResponseWriter.
type deploymentEvent struct {
	kind string // "line" | "podSet"
	pod  string
	line string
	pods []k8s.PodAttribution
}

// channelSink implements k8s.DeploymentLogSink by pushing into a buffered
// channel. Line uses non-blocking send and drops on overflow (frontend
// already maintains its own buffer); PodSet blocks because it's rare and
// changes need to be delivered.
type channelSink struct {
	ch  chan<- deploymentEvent
	ctx context.Context
}

func (s *channelSink) Line(pod, line string) {
	select {
	case s.ch <- deploymentEvent{kind: "line", pod: pod, line: line}:
	case <-s.ctx.Done():
	default:
		// Channel full; drop. Acceptable — the frontend ring buffer
		// continues, and over-driving log floods isn't useful UX anyway.
	}
}

func (s *channelSink) PodSet(pods []k8s.PodAttribution) {
	select {
	case s.ch <- deploymentEvent{kind: "podSet", pods: pods}:
	case <-s.ctx.Done():
	}
}

// workloadStreamFn matches the signature of every k8s.Stream*Logs func.
type workloadStreamFn func(ctx context.Context, p credentials.Provider, args k8s.WorkloadLogsArgs, sink k8s.DeploymentLogSink) error

// workloadLogsHandler streams aggregated logs for every pod under a
// controller (Deployment/StatefulSet/DaemonSet/Job) as Server-Sent Events.
// Lines carry a `p` field for pod attribution; pod-set changes go out as
// `event: meta` with name+node attribution so the frontend can color and
// (for DaemonSets) display node-first.
func workloadLogsHandler(reg *clusters.Registry, kind string, stream workloadStreamFn) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		q := r.URL.Query()
		args := k8s.WorkloadLogsArgs{
			Cluster:    c,
			Namespace:  chi.URLParam(r, "ns"),
			Name:       chi.URLParam(r, "name"),
			Container:  q.Get("container"),
			Previous:   q.Get("previous") == "true",
			Follow:     q.Get("follow") != "false",
			Timestamps: true,
		}
		if v := q.Get("tailLines"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				args.TailLines = &n
			}
		}
		if v := q.Get("sinceSeconds"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				args.SinceSeconds = &n
			}
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		eventCh := make(chan deploymentEvent, 4096)
		sink := &channelSink{ch: eventCh, ctx: r.Context()}

		var streamErr error
		streamDone := make(chan struct{})
		go func() {
			defer close(streamDone)
			defer close(eventCh)
			streamErr = stream(r.Context(), p, args, sink)
		}()

		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()

		type linePayload struct {
			T string `json:"t,omitempty"`
			P string `json:"p,omitempty"`
			L string `json:"l"`
		}
		type metaPayload struct {
			Pods []k8s.PodAttribution `json:"pods"`
		}

		for {
			select {
			case <-r.Context().Done():
				<-streamDone
				return
			case <-heartbeat.C:
				_, _ = fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			case ev, ok := <-eventCh:
				if !ok {
					<-streamDone
					if streamErr != nil && !errors.Is(streamErr, context.Canceled) {
						slog.ErrorContext(r.Context(), "workload log stream failed",
							"kind", kind, "err", streamErr,
							"cluster", c.Name, "ns", args.Namespace,
							"name", args.Name, "actor", p.Actor())
						payload, _ := json.Marshal(map[string]string{"message": streamErr.Error()})
						_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", payload)
					} else {
						_, _ = fmt.Fprint(w, "event: done\ndata: {}\n\n")
					}
					flusher.Flush()
					return
				}
				switch ev.kind {
				case "line":
					ts, msg := k8s.SplitLogTimestamp(ev.line)
					payload, _ := json.Marshal(linePayload{T: ts, P: ev.pod, L: msg})
					_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
				case "podSet":
					payload, _ := json.Marshal(metaPayload{Pods: ev.pods})
					_, _ = fmt.Fprintf(w, "event: meta\ndata: %s\n\n", payload)
				}
				flusher.Flush()
			}
		}
	}
}

// probeClustersOnBoot runs one zero-payload `/bin/true` exec against
// every registered cluster's kube-system namespace so the
// circuit-breaker policy gets a real signal about WS-vs-SPDY support
// before the first user click. Failures are non-fatal — the cluster
// just stays in default ws_then_spdy mode.
//
// Off by default (PERISCOPE_PROBE_CLUSTERS_ON_BOOT=1 to enable). The
// probe needs a service-account or kubeconfig identity that can list
// pods in kube-system AND exec into one — exactly the same RBAC the
// real exec endpoint requires, just used eagerly.
func probeClustersOnBoot(ctx context.Context, factory credentials.Factory, reg *clusters.Registry, policy *k8s.Policy) {
	// Use the dev session for probes — there's no real user request
	// here. v2 will need to revisit (per-user identity, etc.); for v1
	// the shared-IRSA / kubeconfig identity probes against itself.
	provider, err := factory.For(ctx, credentials.Session{Subject: "boot-probe"})
	if err != nil {
		slog.Warn("boot probe: skipping (credentials unavailable)", "err", err)
		return
	}
	for _, c := range reg.List() {
		if !c.ExecEnabled() {
			continue
		}
		go probeOneCluster(ctx, provider, c, policy)
	}
}

func probeOneCluster(ctx context.Context, p credentials.Provider, c clusters.Cluster, policy *k8s.Policy) {
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	// Find any kube-system pod with at least one container.
	cs, err := k8s.NewClientset(probeCtx, p, c)
	if err != nil {
		slog.Info("boot probe: clientset failed", "cluster", c.Name, "err", err)
		return
	}
	pods, err := cs.CoreV1().Pods("kube-system").List(probeCtx, metav1.ListOptions{Limit: 1})
	if err != nil || len(pods.Items) == 0 {
		slog.Info("boot probe: no kube-system pod to probe", "cluster", c.Name, "err", err)
		return
	}
	pod := pods.Items[0]
	containerName := ""
	if len(pod.Spec.Containers) > 0 {
		containerName = pod.Spec.Containers[0].Name
	}
	// Discard streams — we just need the transport choice to settle.
	_, err = k8s.ExecPod(probeCtx, p, k8s.ExecPodArgs{
		Cluster:   c,
		Namespace: pod.Namespace,
		Pod:       pod.Name,
		Container: containerName,
		Command:   []string{"true"},
		TTY:       false,
		Policy:    policy,
	})
	if err != nil {
		slog.Info("boot probe: completed with error (recorded by policy)", "cluster", c.Name, "err", err)
		return
	}
	wsFails, pinned := policy.State(c.Name)
	slog.Info("boot probe: ok", "cluster", c.Name, "ws_fails", wsFails, "pinned_spdy_until", pinned)
}

// parseSearchKinds turns the comma-separated `kinds` query param into
// the typed enum slice the search backend expects. Empty input means
// "all kinds" — handled by SearchResources itself, so we return nil.
// Unknown tokens are silently dropped; the SPA only sends valid kinds.
func parseSearchKinds(raw string) []k8s.SearchKind {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	known := map[string]k8s.SearchKind{
		"pods":         k8s.SearchKindPods,
		"deployments":  k8s.SearchKindDeployments,
		"statefulsets": k8s.SearchKindStatefulSets,
		"daemonsets":   k8s.SearchKindDaemonSets,
		"services":     k8s.SearchKindServices,
		"configmaps":   k8s.SearchKindConfigMaps,
		"secrets":      k8s.SearchKindSecrets,
		"namespaces":   k8s.SearchKindNamespaces,
	}
	out := make([]k8s.SearchKind, 0, len(parts))
	for _, p := range parts {
		if k, ok := known[strings.TrimSpace(p)]; ok {
			out = append(out, k)
		}
	}
	return out
}

// --- Custom-resource handler factories -----------------------------------
//
// Shape mirrors listResource / detailHandler for built-ins, but takes
// the GVR from the URL path. The "_" placeholder for {ns} marks
// cluster-scoped CRs — keeps the chi route tree single rather than
// branching on scope.
//
// The CRD lookup (FindCRDByPlural) inside ListCustomResources gives
// us the resource Kind for events, so we don't need a separate
// per-CRD route map.

const clusterScopedNamespacePlaceholder = "_"

func crdRefFromRequest(r *http.Request, withName bool) k8s.CustomResourceRef {
	ref := k8s.CustomResourceRef{
		Group:   chi.URLParam(r, "group"),
		Version: chi.URLParam(r, "version"),
		Plural:  chi.URLParam(r, "plural"),
	}
	ns := chi.URLParam(r, "ns")
	if ns != clusterScopedNamespacePlaceholder {
		ref.Namespace = ns
	}
	if withName {
		ref.Name = chi.URLParam(r, "name")
	}
	return ref
}

func customResourceListHandler(reg *clusters.Registry) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		ref := k8s.CustomResourceRef{
			Cluster:   c,
			Group:     chi.URLParam(r, "group"),
			Version:   chi.URLParam(r, "version"),
			Plural:    chi.URLParam(r, "plural"),
			Namespace: r.URL.Query().Get("namespace"),
		}
		result, err := k8s.ListCustomResources(r.Context(), p, ref)
		if err != nil {
			slog.Error("list custom resources", "cluster", c.Name, "gvr", ref.Group+"/"+ref.Version+"/"+ref.Plural, "err", err)
			http.Error(w, err.Error(), httpStatusFor(err))
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func customResourceDetailHandler(reg *clusters.Registry) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		ref := crdRefFromRequest(r, true)
		ref.Cluster = c
		result, err := k8s.GetCustomResource(r.Context(), p, ref)
		if err != nil {
			slog.Error("get custom resource", "cluster", c.Name, "name", ref.Name, "err", err)
			http.Error(w, err.Error(), httpStatusFor(err))
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func customResourceYAMLHandler(reg *clusters.Registry) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		ref := crdRefFromRequest(r, true)
		ref.Cluster = c
		yaml, err := k8s.GetCustomResourceYAML(r.Context(), p, ref)
		if err != nil {
			slog.Error("get custom resource yaml", "cluster", c.Name, "name", ref.Name, "err", err)
			http.Error(w, err.Error(), httpStatusFor(err))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(yaml))
	}
}

func customResourceEventsHandler(reg *clusters.Registry) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		ref := crdRefFromRequest(r, true)
		ref.Cluster = c
		// Look up the CRD's Kind so we can filter events by
		// involvedObject.kind. Caching the lookup would shave a
		// round-trip but the CRD list call is fast and the events tab
		// is opened on demand.
		crd, err := k8s.FindCRDByPlural(r.Context(), p, c, ref.Group, ref.Version, ref.Plural)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		events, err := k8s.ListObjectEvents(r.Context(), p, k8s.ListObjectEventsArgs{
			Cluster:   c,
			Namespace: ref.Namespace,
			Kind:      crd.Kind,
			Name:      ref.Name,
		})
		if err != nil {
			slog.Error("get custom resource events", "cluster", c.Name, "name", ref.Name, "err", err)
			http.Error(w, err.Error(), httpStatusFor(err))
			return
		}
		writeJSON(w, http.StatusOK, events)
	}
}
