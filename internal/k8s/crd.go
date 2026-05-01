package k8s

import (
	"context"
	"fmt"
	"sort"
	"time"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// --- CRD discovery types -------------------------------------------------
//
// These DTOs power the "Custom Resources" catalog page on the frontend.
// They mirror the relevant slice of v1.CustomResourceDefinition without
// leaking the kubernetes API surface.

// CRD describes a single custom resource definition installed on the
// cluster — what the operator can pick from to inspect custom
// resources of that kind.
type CRD struct {
	// Name is the metadata.name of the CRD object itself, always
	// "<plural>.<group>" by convention (e.g. "certificates.cert-manager.io").
	Name string `json:"name"`

	Group       string `json:"group"`
	Kind        string `json:"kind"`
	Plural      string `json:"plural"`
	Singular    string `json:"singular,omitempty"`
	ShortNames  []string `json:"shortNames,omitempty"`

	// Scope is "Namespaced" or "Cluster".
	Scope string `json:"scope"`

	// Versions advertised by the CRD. Only served versions are
	// returned (skipped or non-served are noise for listing). The UI
	// queries against ServedVersion when listing custom resources.
	Versions       []CRDVersion `json:"versions"`
	ServedVersion  string       `json:"servedVersion"`
	StorageVersion string       `json:"storageVersion"`

	CreatedAt time.Time `json:"createdAt"`
}

// CRDVersion mirrors spec.versions[]. The printer columns are the heart
// of v1's "schema-aware" treatment — the CRD author tells us which
// fields are worth showing in a list, and we honor that.
type CRDVersion struct {
	Name           string          `json:"name"`
	Served         bool            `json:"served"`
	Storage        bool            `json:"storage"`
	Deprecated     bool            `json:"deprecated,omitempty"`
	PrinterColumns []PrinterColumn `json:"printerColumns,omitempty"`
}

// PrinterColumn matches v1.CustomResourceColumnDefinition. The
// JSONPath is the kubectl-style path (e.g. ".status.conditions[?(@.type==\"Ready\")].status")
// that we evaluate against an unstructured object to pull a value out
// for the list table. Priority>0 means kubectl only shows it with
// `-o wide`; we omit those from default list columns to avoid clutter.
type PrinterColumn struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Format      string `json:"format,omitempty"`
	Description string `json:"description,omitempty"`
	JSONPath    string `json:"jsonPath"`
	Priority    int32  `json:"priority,omitempty"`
}

type CRDList struct {
	CRDs []CRD `json:"crds"`
}

// --- Discovery -----------------------------------------------------------

// ListCRDs returns every CustomResourceDefinition installed on the
// cluster. Sorted by group then kind so the catalog page renders
// stably across reloads. Fetched via the apiextensions clientset (CRDs
// are themselves K8s resources stored in
// apiextensions.k8s.io/v1.customresourcedefinitions).
func ListCRDs(ctx context.Context, p credentials.Provider, c clusters.Cluster) (CRDList, error) {
	cfg, err := buildRestConfig(ctx, p, c)
	if err != nil {
		return CRDList{}, err
	}
	cs, err := apiextclient.NewForConfig(cfg)
	if err != nil {
		return CRDList{}, fmt.Errorf("build apiextensions client: %w", err)
	}
	list, err := cs.ApiextensionsV1().CustomResourceDefinitions().List(ctx, metav1.ListOptions{})
	if err != nil {
		return CRDList{}, fmt.Errorf("list CRDs: %w", err)
	}

	out := make([]CRD, 0, len(list.Items))
	for i := range list.Items {
		c := convertCRD(&list.Items[i])
		// Skip CRDs with no served versions — we wouldn't be able to
		// list custom resources of them anyway. This is rare but
		// happens during CRD upgrades or when the operator has
		// unserved versions for storage migration.
		if c.ServedVersion == "" {
			continue
		}
		out = append(out, c)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Kind < out[j].Kind
	})

	return CRDList{CRDs: out}, nil
}

// convertCRD pulls the bits the UI needs out of the apiextensions v1
// CRD type. Picks the version we'll actually query against (preferring
// the CRD's storage version when it's served; otherwise the first
// served version).
func convertCRD(in *apiextv1.CustomResourceDefinition) CRD {
	versions := make([]CRDVersion, 0, len(in.Spec.Versions))
	var served, storage string
	for _, v := range in.Spec.Versions {
		if !v.Served {
			continue
		}
		cols := make([]PrinterColumn, 0, len(v.AdditionalPrinterColumns))
		for _, c := range v.AdditionalPrinterColumns {
			cols = append(cols, PrinterColumn{
				Name:        c.Name,
				Type:        c.Type,
				Format:      c.Format,
				Description: c.Description,
				JSONPath:    c.JSONPath,
				Priority:    c.Priority,
			})
		}
		versions = append(versions, CRDVersion{
			Name:           v.Name,
			Served:         v.Served,
			Storage:        v.Storage,
			Deprecated:     v.Deprecated,
			PrinterColumns: cols,
		})
		if v.Storage {
			storage = v.Name
			if served == "" {
				served = v.Name
			}
		}
		if served == "" && v.Served {
			served = v.Name
		}
	}

	return CRD{
		Name:           in.Name,
		Group:          in.Spec.Group,
		Kind:           in.Spec.Names.Kind,
		Plural:         in.Spec.Names.Plural,
		Singular:       in.Spec.Names.Singular,
		ShortNames:     in.Spec.Names.ShortNames,
		Scope:          string(in.Spec.Scope),
		Versions:       versions,
		ServedVersion:  served,
		StorageVersion: storage,
		CreatedAt:      in.CreationTimestamp.Time,
	}
}

// FindCRDByPlural returns the CRD whose group/version/plural triple
// matches the request. Used by the customresource handler to look up
// printer-column definitions before listing the underlying resources.
//
// We re-list CRDs each time rather than caching: list calls are cheap
// (~200 CRDs in a heavy cluster, ~15ms) and the dashboard's request
// volume is operator-paced, not per-frame. Caching would mean staleness
// when an operator installs a new CRD.
func FindCRDByPlural(ctx context.Context, p credentials.Provider, c clusters.Cluster, group, version, plural string) (*CRD, error) {
	list, err := ListCRDs(ctx, p, c)
	if err != nil {
		return nil, err
	}
	for i := range list.CRDs {
		crd := &list.CRDs[i]
		if crd.Group != group || crd.Plural != plural {
			continue
		}
		// Confirm the requested version is actually served. If not we
		// fall through and treat as not-found — protects against the
		// frontend caching a URL pointing at a now-removed version.
		for _, v := range crd.Versions {
			if v.Name == version && v.Served {
				return crd, nil
			}
		}
	}
	return nil, fmt.Errorf("CRD not found: group=%q version=%q plural=%q", group, version, plural)
}
