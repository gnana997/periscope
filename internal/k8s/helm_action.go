// helm_action.go — shared write-path infrastructure for helm SDK calls.
//
// This is the entry point for every helm SDK action (preview / install /
// upgrade / rollback). The read paths (release browser, chart fetch) do
// NOT use this file; they decode storage blobs and tarballs directly to
// avoid pulling in the helm SDK's transitive kubectl dependency. Read
// the policy preamble in helm.go for the boundary.
//
// What this file owns:
//
//   - simpleRESTClientGetter — a genericclioptions.RESTClientGetter
//     implementation that returns the impersonation-aware rest.Config
//     produced by buildRestConfig. Helm's pkg/action.Configuration
//     requires a RESTClientGetter to construct the discovery client +
//     RESTMapper + (sometimes) raw kubeconfig. We hand it our existing
//     rest.Config so impersonation headers propagate to every K8s call
//     helm makes downstream (manifest apply, hook execution, watch).
//
//   - buildHelmActionConfig — constructs an *action.Configuration ready
//     to feed into action.NewInstall / NewUpgrade / NewRollback. Wires
//     the storage driver via the existing resolveHelmDriver probe so
//     the action talks to the same storage backend the read browser
//     reads (Secrets vs ConfigMaps).
//
// PR #101 (rollback) introduces a near-identical simpleRESTClientGetter
// in its own helm_write.go. When that PR rebases on this branch, its
// type and helper become redundant — the contributor drops them and
// imports buildHelmActionConfig from here. Comment on #101 sets that
// expectation.

package k8s

import (
	"context"
	"fmt"

	"helm.sh/helm/v3/pkg/action"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// simpleRESTClientGetter implements genericclioptions.RESTClientGetter
// against an existing rest.Config. The four methods cover what helm's
// pkg/action.Configuration.Init requires — REST config (with our
// impersonation headers), discovery client (memory-cached so repeated
// calls within a single preview don't re-hit the apiserver), REST
// mapper (deferred-discovery so unknown CRDs trigger a refresh), and
// the raw kubeconfig loader (which helm rarely needs but constructs
// at config init time anyway — we return an empty in-memory config so
// helm's "load kubeconfig" path resolves without error).
//
// The struct is intentionally minimal — no exec hooks, no kubeconfig
// merging, no override flags. Periscope's auth path is upstream of
// helm; by the time helm sees the config, every credential decision
// has been made.
type simpleRESTClientGetter struct {
	cfg       *rest.Config
	namespace string
}

func (s *simpleRESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	return s.cfg, nil
}

func (s *simpleRESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	c, err := discovery.NewDiscoveryClientForConfig(s.cfg)
	if err != nil {
		return nil, err
	}
	return memory.NewMemCacheClient(c), nil
}

func (s *simpleRESTClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	d, err := s.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(d)
	return restmapper.NewShortcutExpander(mapper, d, nil), nil
}

func (s *simpleRESTClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return clientcmd.NewDefaultClientConfig(*clientcmdapi.NewConfig(), &clientcmd.ConfigOverrides{})
}

// Compile-time assertion that simpleRESTClientGetter satisfies the
// helm SDK's RESTClientGetter contract. If helm's interface gains
// methods in a future version this catches the gap at build time.
var _ genericclioptions.RESTClientGetter = (*simpleRESTClientGetter)(nil)

// buildHelmActionConfig produces an *action.Configuration ready for
// helm's action.NewInstall / NewUpgrade / NewRollback constructors.
// It runs the same auth path every other K8s call in Periscope uses
// (via buildRestConfig) plus the storage driver auto-probe from the
// read browser (resolveHelmDriver), so a helm action sees the same
// backend the SPA's release-list view sees.
//
// The `namespace` argument scopes operations that need a default
// namespace (install with no Release.Namespace, upgrade lookups). It
// does NOT restrict what helm can read — release storage queries go
// against whichever namespace the release lives in.
//
// The trailing `log func(string, ...any)` argument to Init is silenced
// (helm logs at info level for operations Periscope already audits;
// we don't need its output cluttering the server log). If a future
// debug flag wants verbose helm logs, swap the no-op for slog.Debug.
func buildHelmActionConfig(ctx context.Context, p credentials.Provider, c clusters.Cluster, namespace string) (*action.Configuration, error) {
	cfg, err := buildRestConfig(ctx, p, c)
	if err != nil {
		return nil, fmt.Errorf("helm action config: rest config: %w", err)
	}
	cs, err := newClientFn(ctx, p, c)
	if err != nil {
		return nil, fmt.Errorf("helm action config: clientset: %w", err)
	}
	drv, err := resolveHelmDriver(ctx, cs, c)
	if err != nil {
		return nil, fmt.Errorf("helm action config: storage driver: %w", err)
	}

	getter := &simpleRESTClientGetter{cfg: cfg, namespace: namespace}
	actionCfg := new(action.Configuration)
	if err := actionCfg.Init(getter, namespace, drv, helmDebugSilent); err != nil {
		return nil, fmt.Errorf("helm action config: init: %w", err)
	}
	return actionCfg, nil
}

// buildHelmActionConfigFn is the test seam for action.Configuration
// construction. Production wires defaultBuildHelmActionConfig (the
// real impl below); tests substitute a fake that returns a stub
// *action.Configuration without touching newClientFn or the
// apiserver discovery probe.
var buildHelmActionConfigFn = buildHelmActionConfig

// helmDebugSilent is the no-op variant of helm's DebugLog. We don't
// pipe helm's internal verbose logs into the server's slog because
// every operation Periscope kicks off via the helm SDK is already
// audited — the helm-level chatter is duplicate signal at best.
func helmDebugSilent(_ string, _ ...interface{}) {}
