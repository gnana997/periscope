package k8s

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// --- Search types ---------------------------------------------------------

type SearchKind string

const (
	SearchKindPods         SearchKind = "pods"
	SearchKindDeployments  SearchKind = "deployments"
	SearchKindStatefulSets SearchKind = "statefulsets"
	SearchKindDaemonSets   SearchKind = "daemonsets"
	SearchKindServices     SearchKind = "services"
	SearchKindConfigMaps   SearchKind = "configmaps"
	SearchKindSecrets      SearchKind = "secrets"
	SearchKindNamespaces   SearchKind = "namespaces"
)

// AllSearchKinds is the default scope when the caller doesn't specify
// kinds explicitly. Order matters: the UI renders results grouped in
// this order, so it doubles as a "most-clicked first" priority list.
var AllSearchKinds = []SearchKind{
	SearchKindPods,
	SearchKindDeployments,
	SearchKindStatefulSets,
	SearchKindDaemonSets,
	SearchKindServices,
	SearchKindConfigMaps,
	SearchKindSecrets,
	SearchKindNamespaces,
}

// SearchResult is one row in the command palette.
type SearchResult struct {
	Kind      SearchKind `json:"kind"`
	Name      string     `json:"name"`
	Namespace string     `json:"namespace,omitempty"` // empty for cluster-scoped (Namespaces)
	// Score is a relevance hint the UI can sort by (already sorted on
	// the backend). Higher is better. Surfaced for debug/tuning.
	Score int `json:"score"`
}

type SearchResultList struct {
	Results []SearchResult `json:"results"`
}

// SearchArgs is the input to SearchResources.
type SearchArgs struct {
	Cluster clusters.Cluster
	Query   string       // case-insensitive substring; empty returns no results
	Kinds   []SearchKind // empty = AllSearchKinds
	Limit   int          // per kind; <= 0 falls back to defaultSearchLimit
}

const defaultSearchLimit = 10

// SearchResources fans a name-substring search out across the
// requested kinds in parallel and returns up to Limit results per kind.
//
// Relevance scoring is intentionally simple — it's the difference
// between "exact name match" (score 100), "name starts with query"
// (score 70), "name contains query" (score 40), and "namespace contains
// query" (score 20). The palette UX wins more from speed and breadth
// than from clever ranking; if operators want fancier ranking, add it
// when there's a real signal that this isn't enough.
//
// All errors are absorbed into per-kind silence: a single misbehaving
// API call (e.g. Secrets RBAC denied) shouldn't blank the palette for
// every other kind. Callers see results from the kinds that succeeded.
func SearchResources(ctx context.Context, p credentials.Provider, args SearchArgs) (SearchResultList, error) {
	q := strings.TrimSpace(args.Query)
	if q == "" {
		return SearchResultList{Results: []SearchResult{}}, nil
	}
	q = strings.ToLower(q)

	kinds := args.Kinds
	if len(kinds) == 0 {
		kinds = AllSearchKinds
	}
	limit := args.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return SearchResultList{}, fmt.Errorf("build clientset: %w", err)
	}

	var (
		mu      sync.Mutex
		merged  = make([]SearchResult, 0, len(kinds)*limit)
		wg      sync.WaitGroup
	)

	collect := func(rs []SearchResult) {
		if len(rs) == 0 {
			return
		}
		mu.Lock()
		merged = append(merged, rs...)
		mu.Unlock()
	}

	for _, k := range kinds {
		k := k // capture
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, err := searchOne(ctx, cs, k, q, limit)
			if err != nil {
				return // degrade silently
			}
			collect(results)
		}()
	}
	wg.Wait()

	// The result order matters because the UI groups by kind; sorting
	// by (kind-priority, score, name) puts pods at the top, then
	// highest-relevance results inside each kind. We use the kind's
	// position in AllSearchKinds as the priority.
	kindOrder := make(map[SearchKind]int, len(AllSearchKinds))
	for i, k := range AllSearchKinds {
		kindOrder[k] = i
	}
	sort.SliceStable(merged, func(i, j int) bool {
		ai, aj := kindOrder[merged[i].Kind], kindOrder[merged[j].Kind]
		if ai != aj {
			return ai < aj
		}
		if merged[i].Score != merged[j].Score {
			return merged[i].Score > merged[j].Score
		}
		return merged[i].Name < merged[j].Name
	})

	return SearchResultList{Results: merged}, nil
}

// searchOne dispatches to the right list call for the given kind, then
// substring-matches the query against name (and namespace, where
// applicable) and returns the top-N matches by relevance.
func searchOne(ctx context.Context, cs kubernetes.Interface, kind SearchKind, query string, limit int) ([]SearchResult, error) {
	type item struct {
		name      string
		namespace string
	}
	var items []item

	switch kind {
	case SearchKindPods:
		l, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items = make([]item, 0, len(l.Items))
		for _, x := range l.Items {
			items = append(items, item{x.Name, x.Namespace})
		}
	case SearchKindDeployments:
		l, err := cs.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items = make([]item, 0, len(l.Items))
		for _, x := range l.Items {
			items = append(items, item{x.Name, x.Namespace})
		}
	case SearchKindStatefulSets:
		l, err := cs.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items = make([]item, 0, len(l.Items))
		for _, x := range l.Items {
			items = append(items, item{x.Name, x.Namespace})
		}
	case SearchKindDaemonSets:
		l, err := cs.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items = make([]item, 0, len(l.Items))
		for _, x := range l.Items {
			items = append(items, item{x.Name, x.Namespace})
		}
	case SearchKindServices:
		l, err := cs.CoreV1().Services("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items = make([]item, 0, len(l.Items))
		for _, x := range l.Items {
			items = append(items, item{x.Name, x.Namespace})
		}
	case SearchKindConfigMaps:
		l, err := cs.CoreV1().ConfigMaps("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items = make([]item, 0, len(l.Items))
		for _, x := range l.Items {
			items = append(items, item{x.Name, x.Namespace})
		}
	case SearchKindSecrets:
		l, err := cs.CoreV1().Secrets("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items = make([]item, 0, len(l.Items))
		for _, x := range l.Items {
			items = append(items, item{x.Name, x.Namespace})
		}
	case SearchKindNamespaces:
		l, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		items = make([]item, 0, len(l.Items))
		for _, x := range l.Items {
			items = append(items, item{x.Name, ""})
		}
	default:
		return nil, fmt.Errorf("unknown search kind %q", kind)
	}

	scored := make([]SearchResult, 0, limit*2)
	for _, it := range items {
		s := score(it.name, it.namespace, query)
		if s == 0 {
			continue
		}
		scored = append(scored, SearchResult{
			Kind:      kind,
			Name:      it.name,
			Namespace: it.namespace,
			Score:     s,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Name < scored[j].Name
	})

	if len(scored) > limit {
		scored = scored[:limit]
	}
	return scored, nil
}

// score returns 0 for "no match" and a positive integer reflecting how
// relevant the match is. Heuristic only — operators care about speed
// and breadth more than precision.
//
//	exact name match           → 100
//	name starts with query     →  70
//	name contains query        →  40
//	namespace contains query   →  20  (helpful when typing a namespace name)
func score(name, namespace, query string) int {
	n := strings.ToLower(name)
	if n == query {
		return 100
	}
	if strings.HasPrefix(n, query) {
		return 70
	}
	if strings.Contains(n, query) {
		return 40
	}
	if namespace != "" && strings.Contains(strings.ToLower(namespace), query) {
		return 20
	}
	return 0
}
