package main

// eks_insights_uri.go — pure parser that turns the
// `kubernetesResourceUri` string returned by the EKS DescribeInsight
// API into a deep link to Periscope's per-resource YAML editor.
//
// EKS returns a standard Kubernetes REST path:
//
//   /api/v1[/namespaces/{ns}]/{plural}/{name}             (core group)
//   /apis/{group}/{version}[/namespaces/{ns}]/{plural}/{name}
//
// The SPA does not have a dedicated detail/editor route per kind.
// Each list page (PodsPage, DeploymentsPage, …) reads `selNs` and
// `sel` from the query string and opens the YAML tab when
// `tab=yaml`. So the deep link this parser builds is always:
//
//   /clusters/{c}/{spaPlural}?selNs={ns}&sel={name}&tab=yaml
//
// where `spaPlural` matches the SPA route segment. For built-in
// kinds the segment is the K8s plural with three aliases
// (persistentvolumeclaims→pvcs, persistentvolumes→pvs,
// customresourcedefinitions→crds). Anything else falls through to
// the customresources route which takes the GVR verbatim.
//
// Critically, no cluster discovery is required. The mapping is a
// pure function of the URI string — which is what makes this safe
// to compute server-side and serve from a cluster-keyed cache that
// no longer has access to a live apiserver.
//
// One caveat documented in docs/setup/eks-upgrade-readiness.md
// (PR-2): when EKS flags a deprecated apiVersion (e.g.
// policy/v1beta1 PDB), the built-in route opens the resource at the
// SPA's current served version — actually the right UX, since the
// editor shows live state. For CRDs at no-longer-served versions
// the editor will surface a load error from the apiserver. We do
// not try to be clever about this in v1.

import (
	"net/url"
	"strings"
)

// builtinSPAPlurals maps a Kubernetes plural to the SPA's route
// segment for the kinds that have a dedicated list page in
// web/src/routes.tsx. Anything not in this map falls through to the
// `/customresources/{group}/{version}/{plural}` route which accepts
// any GVR.
//
// Keep this map in sync with web/src/routes.tsx. The SPA test
// `eksInsights.test.tsx` covers the link shape end-to-end so a
// missing entry here surfaces as a router 404 in the test, not a
// silent regression.
var builtinSPAPlurals = map[string]string{
	"pods":                              "pods",
	"deployments":                       "deployments",
	"statefulsets":                      "statefulsets",
	"daemonsets":                        "daemonsets",
	"jobs":                              "jobs",
	"cronjobs":                          "cronjobs",
	"services":                          "services",
	"ingresses":                         "ingresses",
	"configmaps":                        "configmaps",
	"secrets":                           "secrets",
	"nodes":                             "nodes",
	"namespaces":                        "namespaces",
	"events":                            "events",
	"persistentvolumeclaims":            "pvcs",
	"persistentvolumes":                 "pvs",
	"storageclasses":                    "storageclasses",
	"roles":                             "roles",
	"clusterroles":                      "clusterroles",
	"rolebindings":                      "rolebindings",
	"clusterrolebindings":               "clusterrolebindings",
	"serviceaccounts":                   "serviceaccounts",
	"horizontalpodautoscalers":          "horizontalpodautoscalers",
	"poddisruptionbudgets":              "poddisruptionbudgets",
	"replicasets":                       "replicasets",
	"networkpolicies":                   "networkpolicies",
	"endpointslices":                    "endpointslices",
	"ingressclasses":                    "ingressclasses",
	"resourcequotas":                    "resourcequotas",
	"limitranges":                       "limitranges",
	"priorityclasses":                   "priorityclasses",
	"runtimeclasses":                    "runtimeclasses",
	"customresourcedefinitions":         "crds",
}

// parsedResourceURI is the structured form of a kubernetesResourceUri.
// EditorPath is empty when the URI shape is unrecognized; in that case
// the SPA renders the raw URI as a non-clickable monospace string.
type parsedResourceURI struct {
	Group     string
	Version   string
	Plural    string
	Namespace string
	Name      string
	// EditorPath is the cluster-rooted path the SPA navigates to so
	// the relevant list page selects the resource and opens its YAML
	// tab. Empty when the URI couldn't be parsed cleanly.
	EditorPath string
}

// parseKubernetesResourceURI decomposes the URI EKS returns and
// builds the SPA editor deep link. Cluster is the registry name —
// this is the {cluster} segment in the SPA route, NOT the EKS ARN.
//
// Returns (zero, false) when the URI is empty or doesn't match a
// recognized shape. Callers should still surface the raw URI in
// that case so the operator can chase it manually.
func parseKubernetesResourceURI(cluster, uri string) (parsedResourceURI, bool) {
	if uri == "" {
		return parsedResourceURI{}, false
	}
	// Strip a leading slash so strings.Split has a stable shape.
	trimmed := strings.TrimPrefix(uri, "/")
	parts := strings.Split(trimmed, "/")

	var group, version string
	var rest []string

	switch parts[0] {
	case "api":
		// /api/v1[/namespaces/{ns}]/{plural}/{name}
		if len(parts) < 2 {
			return parsedResourceURI{}, false
		}
		group = ""
		version = parts[1]
		rest = parts[2:]
	case "apis":
		// /apis/{group}/{version}[/namespaces/{ns}]/{plural}/{name}
		if len(parts) < 3 {
			return parsedResourceURI{}, false
		}
		group = parts[1]
		version = parts[2]
		rest = parts[3:]
	default:
		return parsedResourceURI{}, false
	}

	var ns, plural, name string
	switch {
	case len(rest) >= 4 && rest[0] == "namespaces":
		// namespaced: namespaces/{ns}/{plural}/{name}
		ns = rest[1]
		plural = rest[2]
		name = rest[3]
	case len(rest) >= 2 && rest[0] != "namespaces":
		// cluster-scoped: {plural}/{name}
		plural = rest[0]
		name = rest[1]
	default:
		return parsedResourceURI{}, false
	}

	if plural == "" || name == "" {
		return parsedResourceURI{}, false
	}
	if cluster == "" {
		return parsedResourceURI{}, false
	}

	out := parsedResourceURI{
		Group:     group,
		Version:   version,
		Plural:    plural,
		Namespace: ns,
		Name:      name,
	}
	out.EditorPath = buildEditorPath(cluster, group, version, plural, ns, name)
	return out, true
}

// buildEditorPath constructs the SPA path with selection query
// params. Built-in kinds use their dedicated route; everything else
// falls through to /customresources/{group}/{version}/{plural}.
//
// The Path itself is built with url.URL so the cluster name and
// resource name are escaped if they contain shell-special chars —
// EKS shouldn't return funky names in practice, but the cluster
// segment came from our registry which a misbehaving operator
// could in principle stuff with weird characters.
func buildEditorPath(cluster, group, version, plural, ns, name string) string {
	q := url.Values{}
	q.Set("selNs", ns)
	q.Set("sel", name)
	q.Set("tab", "yaml")

	var path string
	if spa, ok := builtinSPAPlurals[plural]; ok {
		path = "/clusters/" + url.PathEscape(cluster) + "/" + spa
	} else {
		// Custom Resources need group + version in the path. The core
		// group is empty in REST URIs but the customresources route
		// requires a non-empty group segment. Treating an empty group
		// as unmappable here is safe — every core-group kind is in
		// the built-in map already, so this branch only fires for
		// genuine CRDs.
		if group == "" || version == "" {
			return ""
		}
		path = "/clusters/" + url.PathEscape(cluster) + "/customresources/" +
			url.PathEscape(group) + "/" + url.PathEscape(version) + "/" + url.PathEscape(plural)
	}
	return path + "?" + q.Encode()
}
