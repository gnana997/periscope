package k8s

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/jsonpath"
	"sigs.k8s.io/yaml"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// --- DTOs -----------------------------------------------------------------

// CustomResource is one row in the dynamic list view of a CRD's
// resources. Columns is keyed by PrinterColumn.Name and holds the
// pre-formatted string for that cell.
type CustomResource struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
	Columns   map[string]string `json:"columns"`
}

// CustomResourceList carries both the rows and the column definitions
// the rows were rendered against. The frontend builds a DataTable from
// these — dynamic per-CRD columns without compile-time knowledge.
type CustomResourceList struct {
	Items   []CustomResource `json:"items"`
	Columns []PrinterColumn  `json:"columns"`
	// Scope is echoed back so the SPA can decide whether to render the
	// namespace column for cluster-scoped CRDs (where Namespace is "").
	Scope string `json:"scope"`
}

// CustomResourceDetail is the unstructured object plus its resolved
// printer-column values. Object is the raw JSON map — the SPA can
// render it as YAML, pull individual fields for a describe view, etc.
type CustomResourceDetail struct {
	Name      string                 `json:"name"`
	Namespace string                 `json:"namespace,omitempty"`
	Kind      string                 `json:"kind"`
	APIVersion string                `json:"apiVersion"`
	CreatedAt time.Time              `json:"createdAt"`
	Object    map[string]interface{} `json:"object"`
}

// --- Args -----------------------------------------------------------------

type CustomResourceRef struct {
	Cluster   clusters.Cluster
	Group     string
	Version   string
	Plural    string
	Namespace string // optional — empty for cluster-scoped or all-NS list
	Name      string // only used for Get/YAML
}

// --- List -----------------------------------------------------------------

// ListCustomResources returns every custom resource of the given
// group/version/plural — namespaced when ref.Namespace is set, all
// namespaces otherwise (and ignored entirely for cluster-scoped
// CRDs).
//
// Printer-column values are resolved via JSONPath on each item, so the
// list view shows the same columns kubectl shows for `get
// <plural>`. Falls back to plain name+namespace+age when the CRD has
// no additionalPrinterColumns.
func ListCustomResources(ctx context.Context, p credentials.Provider, ref CustomResourceRef) (CustomResourceList, error) {
	crd, err := FindCRDByPlural(ctx, p, ref.Cluster, ref.Group, ref.Version, ref.Plural)
	if err != nil {
		return CustomResourceList{}, err
	}

	cfg, err := buildRestConfig(ctx, p, ref.Cluster)
	if err != nil {
		return CustomResourceList{}, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return CustomResourceList{}, fmt.Errorf("build dynamic client: %w", err)
	}

	gvr := schema.GroupVersionResource{
		Group:    ref.Group,
		Version:  ref.Version,
		Resource: ref.Plural,
	}

	var resources dynamic.ResourceInterface
	if crd.Scope == "Namespaced" {
		if ref.Namespace == "" {
			resources = dyn.Resource(gvr).Namespace(metav1.NamespaceAll)
		} else {
			resources = dyn.Resource(gvr).Namespace(ref.Namespace)
		}
	} else {
		resources = dyn.Resource(gvr)
	}

	list, err := resources.List(ctx, metav1.ListOptions{})
	if err != nil {
		return CustomResourceList{}, fmt.Errorf("list custom resources: %w", err)
	}

	cols := defaultPrinterColumns(crd, ref.Version)
	parsedCols := compilePrinterColumns(cols)

	items := make([]CustomResource, 0, len(list.Items))
	for i := range list.Items {
		obj := &list.Items[i]
		items = append(items, CustomResource{
			Name:      obj.GetName(),
			Namespace: obj.GetNamespace(),
			CreatedAt: obj.GetCreationTimestamp().Time,
			Columns:   evaluatePrinterColumns(obj.Object, parsedCols),
		})
	}

	return CustomResourceList{
		Items:   items,
		Columns: cols,
		Scope:   crd.Scope,
	}, nil
}

// --- Detail ---------------------------------------------------------------

func GetCustomResource(ctx context.Context, p credentials.Provider, ref CustomResourceRef) (CustomResourceDetail, error) {
	crd, err := FindCRDByPlural(ctx, p, ref.Cluster, ref.Group, ref.Version, ref.Plural)
	if err != nil {
		return CustomResourceDetail{}, err
	}
	obj, err := getCustomResourceObject(ctx, p, ref, crd.Scope)
	if err != nil {
		return CustomResourceDetail{}, err
	}
	return CustomResourceDetail{
		Name:       obj.GetName(),
		Namespace:  obj.GetNamespace(),
		Kind:       obj.GetKind(),
		APIVersion: obj.GetAPIVersion(),
		CreatedAt:  obj.GetCreationTimestamp().Time,
		Object:     obj.Object,
	}, nil
}

// GetCustomResourceYAML returns the YAML-marshaled form of the
// resource. Same shape kubectl produces for `get -o yaml`. Uses
// sigs.k8s.io/yaml so the serialization matches the K8s convention
// (no anchors, struct field order, etc.).
func GetCustomResourceYAML(ctx context.Context, p credentials.Provider, ref CustomResourceRef) (string, error) {
	crd, err := FindCRDByPlural(ctx, p, ref.Cluster, ref.Group, ref.Version, ref.Plural)
	if err != nil {
		return "", err
	}
	obj, err := getCustomResourceObject(ctx, p, ref, crd.Scope)
	if err != nil {
		return "", err
	}
	bs, err := yaml.Marshal(obj.Object)
	if err != nil {
		return "", fmt.Errorf("marshal yaml: %w", err)
	}
	return string(bs), nil
}

func getCustomResourceObject(ctx context.Context, p credentials.Provider, ref CustomResourceRef, scope string) (*unstructured.Unstructured, error) {
	cfg, err := buildRestConfig(ctx, p, ref.Cluster)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}
	gvr := schema.GroupVersionResource{
		Group:    ref.Group,
		Version:  ref.Version,
		Resource: ref.Plural,
	}
	var resources dynamic.ResourceInterface
	if scope == "Namespaced" {
		if ref.Namespace == "" {
			return nil, fmt.Errorf("namespace required for namespace-scoped resource")
		}
		resources = dyn.Resource(gvr).Namespace(ref.Namespace)
	} else {
		resources = dyn.Resource(gvr)
	}
	obj, err := resources.Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get custom resource: %w", err)
	}
	return obj, nil
}

// --- Printer columns ------------------------------------------------------

// defaultPrinterColumns returns the printer-column list the SPA should
// render for the given CRD/version. We honor priority: kubectl only
// shows priority>0 columns with `-o wide`, so we mirror that — keeps
// the default list view from getting polluted with low-signal fields.
//
// When a CRD has no additionalPrinterColumns at all, we synthesize a
// single "Age" column. The handler also injects the standard Name +
// Namespace columns from the resource's metadata.
func defaultPrinterColumns(crd *CRD, version string) []PrinterColumn {
	for _, v := range crd.Versions {
		if v.Name != version {
			continue
		}
		out := make([]PrinterColumn, 0, len(v.PrinterColumns))
		for _, c := range v.PrinterColumns {
			if c.Priority > 0 {
				continue // skip wide-only columns
			}
			out = append(out, c)
		}
		return out
	}
	return nil
}

// jsonpathColumn is the parsed form of one printer column. We compile
// JSONPath expressions once per list call and re-use for every row.
type jsonpathColumn struct {
	col PrinterColumn
	tpl *jsonpath.JSONPath
}

func compilePrinterColumns(cols []PrinterColumn) []jsonpathColumn {
	out := make([]jsonpathColumn, 0, len(cols))
	for _, c := range cols {
		jp := jsonpath.New(c.Name).AllowMissingKeys(true)
		// kubectl JSONPath uses the literal path string; the parser
		// expects "{.path}" so we wrap if not already wrapped.
		expr := c.JSONPath
		if !strings.HasPrefix(expr, "{") {
			expr = "{" + expr + "}"
		}
		if err := jp.Parse(expr); err != nil {
			// Bad CRD-defined expression. Skip the column rather than
			// failing the whole list.
			continue
		}
		out = append(out, jsonpathColumn{col: c, tpl: jp})
	}
	return out
}

// evaluatePrinterColumns runs every parsed JSONPath against the given
// object and returns a name→value map. AllowMissingKeys means fields
// that don't exist render as "" rather than erroring — the right
// behavior for partially-populated CRs (e.g. a Certificate whose
// status.conditions hasn't been set yet).
func evaluatePrinterColumns(obj map[string]interface{}, cols []jsonpathColumn) map[string]string {
	out := make(map[string]string, len(cols))
	for _, c := range cols {
		var buf bytes.Buffer
		if err := c.tpl.Execute(&buf, obj); err != nil {
			out[c.col.Name] = ""
			continue
		}
		out[c.col.Name] = strings.TrimSpace(buf.String())
	}
	return out
}
