package k8s

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// MetaArgs identifies the resource whose metadata to fetch. Mirrors
// ApplyResourceArgs's URL-param shape so the handler can construct it
// from the same chi.URLParam calls.
type MetaArgs struct {
	Cluster   clusters.Cluster
	Group     string
	Version   string
	Resource  string // plural URL segment, e.g. "pods", "deployments"
	Namespace string // empty for cluster-scoped
	Name      string
}

// ResourceMeta is the JSON shape returned to the SPA. Three fields:
//
//   - resourceVersion: opaque server token. The SPA snapshots this when
//     the editor opens; combined with periodic re-fetches (Phase 3) it
//     drives the drift banner.
//   - generation: bumped on spec changes, useful as a low-noise
//     "something committed" signal in the action bar.
//   - managedFields: the SSA ownership ledger. Drives glyph-margin
//     "owned by" badges in the editor and (in Phase 2) the per-field
//     conflict resolution view.
//
// Returned as-is from the apiserver — managedFields is already a
// stable, JSON-serialisable type. Phase 1 only renders ownership
// badges; the SPA does not need to interpret fieldsV1 yet.
type ResourceMeta struct {
	ResourceVersion string                      `json:"resourceVersion"`
	Generation      int64                       `json:"generation"`
	ManagedFields   []metav1.ManagedFieldsEntry `json:"managedFields"`
}

// newDynamicClientForMeta is swapped out by tests for a fake dynamic
// client. Production path: build a rest.Config via buildRestConfig
// (shared with apply.go) and a dynamic.Interface.
var newDynamicClientForMeta = func(ctx context.Context, p credentials.Provider, c clusters.Cluster) (dynamic.Interface, error) {
	cfg, err := buildRestConfig(ctx, p, c)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("meta: build dynamic client: %w", err)
	}
	return dyn, nil
}

// GetResourceMeta returns resourceVersion, generation, and managedFields
// for the resource at args. Apiserver errors propagate unwrapped so the
// HTTP layer's kerrors classification (Forbidden → 403, NotFound → 404)
// keeps working.
func GetResourceMeta(ctx context.Context, p credentials.Provider, args MetaArgs) (ResourceMeta, error) {
	dyn, err := newDynamicClientForMeta(ctx, p, args.Cluster)
	if err != nil {
		return ResourceMeta{}, err
	}
	gvr := schema.GroupVersionResource{
		Group:    args.Group,
		Version:  args.Version,
		Resource: args.Resource,
	}
	var ri dynamic.ResourceInterface
	if args.Namespace != "" {
		ri = dyn.Resource(gvr).Namespace(args.Namespace)
	} else {
		ri = dyn.Resource(gvr)
	}
	got, err := ri.Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return ResourceMeta{}, err
	}
	return ResourceMeta{
		ResourceVersion: got.GetResourceVersion(),
		Generation:      got.GetGeneration(),
		ManagedFields:   got.GetManagedFields(),
	}, nil
}
