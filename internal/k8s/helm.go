package k8s

// helm.go — read-only Helm release access via direct K8s Secret /
// ConfigMap reads, no Helm SDK. We decode the release blob (base64
// + gzip + JSON) ourselves into a minimal struct that captures the
// fields the SPA renders.
//
// Why no Helm SDK: helm.sh/helm/{v3,v4} transitively pulls
// k8s.io/kubectl whose pinned k8s.io/api version conflicts with
// the rest of Periscope's deps. The release blob format is stable
// across Helm 3+ — name, namespace, info, chart.metadata, config,
// manifest are documented and unchanged for the lifetime of the v1
// storage layout — so owning the decoder is cheap and isolates us
// from the SDK's transitive dep churn.
//
// Storage backend: Helm 3+ stores release state as Secrets of type
// helm.sh/release.v1 in the release namespace by default. We
// auto-probe Secrets→ConfigMaps so deployments running with the
// non-default ConfigMaps driver still work without operator config.
//
// The list path uses one cluster-wide Secret/ConfigMap LIST under
// the impersonating client. A user without cluster-wide list
// permission on the storage kind will receive a 403 — surfaced as
// the standard ForbiddenState by the handler.
//
// Diff: dyff produces a semantic structured diff suitable for both
// the SPA's monaco renderer (raw YAML strings) and future LLM-tool
// callers (the structured changes array).

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"strconv"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"

	"github.com/gonvenience/ytbx"
	"github.com/homeport/dyff/pkg/dyff"
	yamlv3 "go.yaml.in/yaml/v3"
)

// helmOwnerLabel is the Helm-set marker on the storage object. Every
// driver (secret / configmap / sql) tags releases with owner=helm.
const helmOwnerLabel = "owner=helm"

// HelmReleaseSummary is one row in the list endpoint. Slim — meant
// for tabular rendering on the SPA. The rendered manifest and values
// are not present here; fetch the detail endpoint per release.
type HelmReleaseSummary struct {
	Name         string    `json:"name"`
	Namespace    string    `json:"namespace"`
	Revision     int       `json:"revision"`
	Status       string    `json:"status"`
	ChartName    string    `json:"chartName"`
	ChartVersion string    `json:"chartVersion"`
	AppVersion   string    `json:"appVersion"`
	Updated      time.Time `json:"updated"`
}

// HelmReleaseDetail is the unified per-revision blob the SPA consumes
// for the values / manifest / metadata tabs — one Storage.Get returns
// all of this, so we avoid three round-trips.
type HelmReleaseDetail struct {
	Name         string    `json:"name"`
	Namespace    string    `json:"namespace"`
	Revision     int       `json:"revision"`
	Status       string    `json:"status"`
	Description  string    `json:"description,omitempty"`
	ChartName    string    `json:"chartName"`
	ChartVersion string    `json:"chartVersion"`
	AppVersion   string    `json:"appVersion"`
	Icon         string    `json:"icon,omitempty"`
	Updated      time.Time `json:"updated"`
	Notes        string    `json:"notes,omitempty"`
	// ValuesYAML is the merged user-supplied values for this revision
	// rendered as YAML. Empty when the release was installed with
	// no overrides.
	ValuesYAML string `json:"valuesYaml"`
	// ManifestYAML is the multi-doc rendered K8s manifest the chart
	// produced for this revision.
	ManifestYAML string `json:"manifestYaml"`
	// Resources is the parsed list of (apiVersion, kind, namespace,
	// name) tuples extracted from ManifestYAML. Powers the detail
	// header resource summary in v1; the v2 SAR-gating layer for
	// write ops will reuse the same list to compute compound
	// permission checks.
	Resources []HelmManifestObject `json:"resources"`
}

// HelmHistoryEntry is one row of the history table. Metadata-only —
// values/manifest are not embedded; the SPA fetches per-revision
// detail on click.
type HelmHistoryEntry struct {
	Revision     int       `json:"revision"`
	Status       string    `json:"status"`
	ChartName    string    `json:"chartName"`
	ChartVersion string    `json:"chartVersion"`
	AppVersion   string    `json:"appVersion"`
	Description  string    `json:"description,omitempty"`
	Updated      time.Time `json:"updated"`
}

// HelmManifestObject is one (apiVersion, kind, namespace, name)
// tuple from the rendered manifest.
type HelmManifestObject struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
}

// HelmDiff is the response shape for /releases/.../diff.
//
// `from` / `to` carry the raw manifest YAML for the SPA's monaco
// diff viewer. `changes` is the structured list of paths that
// differ — the agent-tool surface, dyff-generated.
type HelmDiff struct {
	From    HelmDiffSide   `json:"from"`
	To      HelmDiffSide   `json:"to"`
	Changes []HelmDiffItem `json:"changes"`
}

type HelmDiffSide struct {
	Revision int    `json:"revision"`
	YAML     string `json:"yaml"`
}

// HelmDiffItem is one entry in the structured change list. Kind
// follows dyff: "modify", "add", "remove", "order". Path uses
// dyff's go-patch-style notation, which is stable and readable
// (e.g. `/spec/template/spec/containers/name=app/image`).
type HelmDiffItem struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// helmRelease is the internal release struct. Mirrors the field
// names Helm 3+ writes into the storage blob (which is JSON-encoded
// per the v1 storage layout). Only the fields we surface are typed —
// everything else is ignored on unmarshal so additions in newer
// Helm versions don't break decode.
type helmRelease struct {
	Name      string                 `json:"name,omitempty"`
	Info      *helmReleaseInfo       `json:"info,omitempty"`
	Chart     *helmChart             `json:"chart,omitempty"`
	Config    map[string]interface{} `json:"config,omitempty"`
	Manifest  string                 `json:"manifest,omitempty"`
	Version   int                    `json:"version,omitempty"`
	Namespace string                 `json:"namespace,omitempty"`
}

type helmReleaseInfo struct {
	LastDeployed time.Time `json:"last_deployed,omitempty"`
	Description  string    `json:"description,omitempty"`
	Status       string    `json:"status,omitempty"`
	Notes        string    `json:"notes,omitempty"`
}

type helmChart struct {
	Metadata *helmChartMetadata `json:"metadata,omitempty"`
}

type helmChartMetadata struct {
	Name       string `json:"name,omitempty"`
	Version    string `json:"version,omitempty"`
	AppVersion string `json:"appVersion,omitempty"`
	Icon       string `json:"icon,omitempty"`
}

// helmDriverProbeResult caches the storage driver detected for a
// cluster so the second visit doesn't pay the probe round-trip.
type helmDriverProbeResult struct {
	driver  string // "secret" | "configmap"
	checked time.Time
}

var (
	// helmDriverCache is keyed by cluster name; cardinality is bounded
	// by the cluster registry (tens at most). No size cap needed.
	helmDriverCache   = map[string]helmDriverProbeResult{}
	helmDriverCacheMu sync.Mutex
)

const helmDriverCacheTTL = 5 * time.Minute

// ListHelmReleases returns the latest revision of every release the
// user can see. Cluster-wide LIST under impersonation; a user without
// cluster-wide list permission on the storage kind will receive a
// forbidden error, surfaced as 403 by the handler.
//
// Returns up to `cap` releases; `truncated` is true when the storage
// returned more than `cap` (we slice in-memory because there is no
// K8s pagination semantics on label-selector lists in this scenario).
func ListHelmReleases(ctx context.Context, p credentials.Provider, c clusters.Cluster, cap int) (items []HelmReleaseSummary, truncated bool, err error) {
	if cap <= 0 {
		cap = 200
	}

	cs, err := newClientFn(ctx, p, c)
	if err != nil {
		return nil, false, fmt.Errorf("build clientset: %w", err)
	}

	drv, err := resolveHelmDriver(ctx, cs, c)
	if err != nil {
		return nil, false, err
	}

	// Read the storage blobs for every release. We collapse to
	// "latest revision per (namespace, name)" using the Secret/CM
	// labels — no need to decode bodies for the version comparison.
	type latest struct {
		release *helmRelease
		updated time.Time
	}
	latestByKey := map[string]latest{}

	if drv == "secret" {
		secrets, err := cs.CoreV1().Secrets("").List(ctx, helmListOpts())
		if err != nil {
			return nil, false, fmt.Errorf("list helm release secrets: %w", err)
		}
		for i := range secrets.Items {
			s := &secrets.Items[i]
			rev, _ := strconv.Atoi(s.Labels["version"])
			key := s.Namespace + "/" + s.Labels["name"]
			cur, ok := latestByKey[key]
			if ok && cur.release != nil && cur.release.Version >= rev {
				continue
			}
			rel, derr := decodeHelmRelease(s.Data["release"])
			if derr != nil || rel == nil {
				continue
			}
			latestByKey[key] = latest{release: rel}
		}
	} else {
		cms, err := cs.CoreV1().ConfigMaps("").List(ctx, helmListOpts())
		if err != nil {
			return nil, false, fmt.Errorf("list helm release configmaps: %w", err)
		}
		for i := range cms.Items {
			cm := &cms.Items[i]
			rev, _ := strconv.Atoi(cm.Labels["version"])
			key := cm.Namespace + "/" + cm.Labels["name"]
			cur, ok := latestByKey[key]
			if ok && cur.release != nil && cur.release.Version >= rev {
				continue
			}
			rel, derr := decodeHelmRelease([]byte(cm.Data["release"]))
			if derr != nil || rel == nil {
				continue
			}
			latestByKey[key] = latest{release: rel}
		}
	}

	out := make([]HelmReleaseSummary, 0, len(latestByKey))
	for _, l := range latestByKey {
		if l.release == nil {
			continue
		}
		out = append(out, releaseToSummary(l.release))
	}
	// Stable order: namespace, then name.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})

	if len(out) > cap {
		return out[:cap], true, nil
	}
	return out, false, nil
}

// GetHelmRelease returns the per-revision detail blob. Pass
// revision=0 for the latest revision — the storage layer's labels
// expose `version` so we resolve "latest" by listing the release's
// blobs and picking the highest.
//
// detailMaxBytes caps the response payload size: a chart that
// renders > 5 MB of YAML is more legitimately broken than worth
// supporting in v1; we return an error past that cap so the handler
// can surface a clear error rather than blow request budgets.
func GetHelmRelease(ctx context.Context, p credentials.Provider, c clusters.Cluster, namespace, name string, revision int, detailMaxBytes int) (*HelmReleaseDetail, error) {
	cs, err := newClientFn(ctx, p, c)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}

	drv, err := resolveHelmDriver(ctx, cs, c)
	if err != nil {
		return nil, err
	}

	// "latest": list this release's blobs in its namespace, sort by
	// version label desc, take the first. Bounds the LIST scope to
	// one release name in one namespace.
	if revision <= 0 {
		latestRev, err := latestRevisionFor(ctx, cs, drv, namespace, name)
		if err != nil {
			return nil, err
		}
		revision = latestRev
	}
	if revision <= 0 {
		return nil, fmt.Errorf("helm release %s/%s: not found", namespace, name)
	}

	storageName := storageObjectName(name, revision)
	var raw []byte
	if drv == "secret" {
		s, err := cs.CoreV1().Secrets(namespace).Get(ctx, storageName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		raw = s.Data["release"]
	} else {
		cm, err := cs.CoreV1().ConfigMaps(namespace).Get(ctx, storageName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		raw = []byte(cm.Data["release"])
	}

	rel, err := decodeHelmRelease(raw)
	if err != nil {
		return nil, fmt.Errorf("decode release blob: %w", err)
	}
	detail, err := releaseToDetail(rel)
	if err != nil {
		return nil, err
	}
	if detailMaxBytes > 0 {
		size := len(detail.ManifestYAML) + len(detail.ValuesYAML) + len(detail.Notes)
		if size > detailMaxBytes {
			return nil, fmt.Errorf("helm release %s/%s revision %d: rendered detail %d bytes exceeds %d-byte limit", namespace, name, detail.Revision, size, detailMaxBytes)
		}
	}
	return detail, nil
}

// GetHelmHistory returns the revision list for a release, newest
// first. Helm's default is the last 10 revisions; we expose `max`
// for callers that want more.
func GetHelmHistory(ctx context.Context, p credentials.Provider, c clusters.Cluster, namespace, name string, max int) ([]HelmHistoryEntry, error) {
	if max <= 0 {
		max = 10
	}
	cs, err := newClientFn(ctx, p, c)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}
	drv, err := resolveHelmDriver(ctx, cs, c)
	if err != nil {
		return nil, err
	}

	out := []HelmHistoryEntry{}
	selector := helmOwnerLabel + ",name=" + name
	listOpts := metav1.ListOptions{LabelSelector: selector}

	if drv == "secret" {
		secrets, err := cs.CoreV1().Secrets(namespace).List(ctx, listOpts)
		if err != nil {
			return nil, fmt.Errorf("list helm history secrets: %w", err)
		}
		for i := range secrets.Items {
			s := &secrets.Items[i]
			rel, derr := decodeHelmRelease(s.Data["release"])
			if derr != nil || rel == nil {
				continue
			}
			out = append(out, releaseToHistoryEntry(rel))
		}
	} else {
		cms, err := cs.CoreV1().ConfigMaps(namespace).List(ctx, listOpts)
		if err != nil {
			return nil, fmt.Errorf("list helm history configmaps: %w", err)
		}
		for i := range cms.Items {
			cm := &cms.Items[i]
			rel, derr := decodeHelmRelease([]byte(cm.Data["release"]))
			if derr != nil || rel == nil {
				continue
			}
			out = append(out, releaseToHistoryEntry(rel))
		}
	}

	// Newest first, then cap.
	sort.Slice(out, func(i, j int) bool { return out[i].Revision > out[j].Revision })
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}

// DiffHelmRevisions renders a structured semantic diff between two
// revisions of a release. Both revisions must exist; either may be
// 0 to mean "latest".
//
// The `from` / `to` YAML strings in the response are the rendered
// manifests for the SPA's monaco diff viewer. The `changes` list is
// dyff's structured output, designed for callers (LLM tools, tests)
// that want pre-parsed change tuples rather than re-running a YAML
// diff client-side.
func DiffHelmRevisions(ctx context.Context, p credentials.Provider, c clusters.Cluster, namespace, name string, fromRev, toRev int, detailMaxBytes int) (*HelmDiff, error) {
	from, err := GetHelmRelease(ctx, p, c, namespace, name, fromRev, detailMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("from revision %d: %w", fromRev, err)
	}
	to, err := GetHelmRelease(ctx, p, c, namespace, name, toRev, detailMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("to revision %d: %w", toRev, err)
	}

	changes, err := diffYAMLDocuments(from.ManifestYAML, to.ManifestYAML)
	if err != nil {
		// Don't fail the whole call — the SPA can still show the raw
		// diff via monaco. Empty changes array means the structured
		// layer is unavailable for this diff. Log so operators can
		// debug; the most likely cause is a malformed manifest doc
		// from a chart that produces non-YAML output for some helper.
		slog.WarnContext(ctx, "helm diff: structured changes unavailable",
			"namespace", namespace, "name", name,
			"from", fromRev, "to", toRev, "err", err)
	}

	return &HelmDiff{
		From:    HelmDiffSide{Revision: from.Revision, YAML: from.ManifestYAML},
		To:      HelmDiffSide{Revision: to.Revision, YAML: to.ManifestYAML},
		Changes: changes,
	}, nil
}

// resolveHelmDriver auto-probes the cluster's storage driver. Tries
// Secrets first (Helm 3+ default); on a successful empty list, probes
// ConfigMaps with the helm owner label as a fallback. Caches the
// answer per cluster so subsequent calls skip the probe.
//
// Permission errors short-circuit to the Secrets driver — the user
// genuinely cannot list this storage kind, which is the same
// downstream outcome as "no helm here" from the SPA's perspective.
// The caller's downstream LIST then bubbles the same 403 cleanly.
func resolveHelmDriver(ctx context.Context, cs kubernetes.Interface, c clusters.Cluster) (string, error) {
	helmDriverCacheMu.Lock()
	if cached, ok := helmDriverCache[c.Name]; ok && time.Since(cached.checked) < helmDriverCacheTTL {
		helmDriverCacheMu.Unlock()
		return cached.driver, nil
	}
	helmDriverCacheMu.Unlock()

	probeOpts := metav1.ListOptions{LabelSelector: helmOwnerLabel, Limit: 1}

	secrets, secretsErr := cs.CoreV1().Secrets("").List(ctx, probeOpts)
	if secretsErr == nil && len(secrets.Items) > 0 {
		cacheHelmDriver(c.Name, "secret")
		return "secret", nil
	}

	// Fall through to ConfigMap probe in two cases: (a) Secrets list
	// succeeded but returned no items (no Secret-driver releases here),
	// (b) Secrets list returned Forbidden / Unauthorized (the user has
	// no RBAC for cluster-wide Secret list but might still see the
	// ConfigMap-driver releases). Other errors (network, timeout)
	// short-circuit to the Secrets default — re-running the probe is
	// cheaper than spending another LIST budget.
	if secretsErr != nil && !apierrors.IsForbidden(secretsErr) && !apierrors.IsUnauthorized(secretsErr) {
		cacheHelmDriver(c.Name, "secret")
		return "secret", nil
	}

	cms, cmsErr := cs.CoreV1().ConfigMaps("").List(ctx, probeOpts)
	if cmsErr == nil && len(cms.Items) > 0 {
		cacheHelmDriver(c.Name, "configmap")
		return "configmap", nil
	}

	// Neither probe found releases. Default to Secrets driver — the
	// downstream LIST will surface the actual permission/no-data
	// distinction (403 vs empty list) in the handler.
	cacheHelmDriver(c.Name, "secret")
	return "secret", nil
}

func cacheHelmDriver(cluster, drv string) {
	helmDriverCacheMu.Lock()
	helmDriverCache[cluster] = helmDriverProbeResult{driver: drv, checked: time.Now()}
	helmDriverCacheMu.Unlock()
}

// helmListOpts returns ListOptions for "owner=helm" label selector.
// Used for cluster-wide LIST in the list path.
func helmListOpts() metav1.ListOptions {
	return metav1.ListOptions{LabelSelector: helmOwnerLabel}
}

// storageObjectName returns the canonical name of the Helm storage
// object for a given (release, revision). Same shape for Secret and
// ConfigMap drivers.
func storageObjectName(release string, revision int) string {
	return fmt.Sprintf("sh.helm.release.v1.%s.v%d", release, revision)
}

// latestRevisionFor enumerates the storage blobs for a release and
// returns the highest version label seen. Cheap — single LIST in
// one namespace, label-filtered to the release name.
func latestRevisionFor(ctx context.Context, cs kubernetes.Interface, drv, namespace, name string) (int, error) {
	listOpts := metav1.ListOptions{LabelSelector: helmOwnerLabel + ",name=" + name}
	max := 0
	if drv == "secret" {
		secrets, err := cs.CoreV1().Secrets(namespace).List(ctx, listOpts)
		if err != nil {
			return 0, err
		}
		for _, s := range secrets.Items {
			rev, _ := strconv.Atoi(s.Labels["version"])
			if rev > max {
				max = rev
			}
		}
	} else {
		cms, err := cs.CoreV1().ConfigMaps(namespace).List(ctx, listOpts)
		if err != nil {
			return 0, err
		}
		for _, cm := range cms.Items {
			rev, _ := strconv.Atoi(cm.Labels["version"])
			if rev > max {
				max = rev
			}
		}
	}
	return max, nil
}

// decodeHelmRelease unwraps the storage blob: K8s clientset already
// did the outer base64 decode, so we receive base64(gzip(json)) →
// decode b64 → gunzip → json.Unmarshal into our minimal struct.
func decodeHelmRelease(raw []byte) (*helmRelease, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty release blob")
	}
	// Helm sometimes leaves the inner b64-encoded data with newlines;
	// strict StdEncoding rejects those. Use the relaxed variant.
	inner, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(raw)))
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(inner))
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()
	body, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("gzip read: %w", err)
	}
	var rel helmRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("unmarshal release json: %w", err)
	}
	return &rel, nil
}

// releaseToSummary collapses chart + info onto the slim list shape.
// Defensive against malformed releases (older helm versions wrote
// partial blobs in failure paths).
func releaseToSummary(r *helmRelease) HelmReleaseSummary {
	s := HelmReleaseSummary{
		Name:      r.Name,
		Namespace: r.Namespace,
		Revision:  r.Version,
	}
	if r.Info != nil {
		s.Status = r.Info.Status
		s.Updated = r.Info.LastDeployed
	}
	if r.Chart != nil && r.Chart.Metadata != nil {
		s.ChartName = r.Chart.Metadata.Name
		s.ChartVersion = r.Chart.Metadata.Version
		s.AppVersion = r.Chart.Metadata.AppVersion
	}
	return s
}

func releaseToHistoryEntry(r *helmRelease) HelmHistoryEntry {
	e := HelmHistoryEntry{Revision: r.Version}
	if r.Info != nil {
		e.Status = r.Info.Status
		e.Description = r.Info.Description
		e.Updated = r.Info.LastDeployed
	}
	if r.Chart != nil && r.Chart.Metadata != nil {
		e.ChartName = r.Chart.Metadata.Name
		e.ChartVersion = r.Chart.Metadata.Version
		e.AppVersion = r.Chart.Metadata.AppVersion
	}
	return e
}

func releaseToDetail(r *helmRelease) (*HelmReleaseDetail, error) {
	d := &HelmReleaseDetail{
		Name:         r.Name,
		Namespace:    r.Namespace,
		Revision:     r.Version,
		ManifestYAML: r.Manifest,
	}
	if r.Info != nil {
		d.Status = r.Info.Status
		d.Description = r.Info.Description
		d.Updated = r.Info.LastDeployed
		d.Notes = r.Info.Notes
	}
	if r.Chart != nil && r.Chart.Metadata != nil {
		d.ChartName = r.Chart.Metadata.Name
		d.ChartVersion = r.Chart.Metadata.Version
		d.AppVersion = r.Chart.Metadata.AppVersion
		d.Icon = r.Chart.Metadata.Icon
	}
	if len(r.Config) > 0 {
		buf, err := yamlv3.Marshal(r.Config)
		if err == nil {
			d.ValuesYAML = string(buf)
		}
	}
	d.Resources = parseManifestObjects(r.Manifest, r.Namespace)
	return d, nil
}

// parseManifestObjects extracts (apiVersion, kind, namespace, name)
// from a multi-doc YAML manifest. Skips empty / malformed documents
// rather than failing the whole parse — a chart with one bad doc
// shouldn't blank the resource list.
// parseManifestObjects extracts (apiVersion, kind, namespace, name)
// from a multi-doc YAML manifest. Splits the manifest on document
// boundaries first, then decodes each chunk independently — so a
// malformed YAML doc in the middle drops only itself, not every
// doc that follows it. (Naive use of yaml.Decoder with break/continue
// either truncates the rest or loops on the same error indefinitely;
// per-chunk decode side-steps both.)
//
// Document boundary = a line that is exactly "---" (with optional
// trailing whitespace), per the YAML 1.2 stream format that Helm
// emits. Edge cases with literal "---" inside multi-line strings
// would mis-split, but Helm-rendered manifests do not produce these
// in practice.
func parseManifestObjects(manifest, releaseNs string) []HelmManifestObject {
	if manifest == "" {
		return []HelmManifestObject{}
	}
	out := []HelmManifestObject{}
	for _, chunk := range splitYAMLDocs(manifest) {
		var doc map[string]interface{}
		if err := yamlv3.Unmarshal([]byte(chunk), &doc); err != nil {
			// Malformed individual doc — drop it, continue with the rest.
			continue
		}
		if doc == nil {
			continue
		}
		api, _ := doc["apiVersion"].(string)
		kind, _ := doc["kind"].(string)
		if kind == "" || api == "" {
			continue
		}
		md, _ := doc["metadata"].(map[string]interface{})
		name, _ := md["name"].(string)
		ns, _ := md["namespace"].(string)
		if ns == "" {
			ns = releaseNs
		}
		out = append(out, HelmManifestObject{
			APIVersion: api,
			Kind:       kind,
			Namespace:  ns,
			Name:       name,
		})
	}
	return out
}

// splitYAMLDocs splits a YAML stream into per-document strings on
// lines that are exactly "---" (with optional whitespace). Empty
// chunks are returned as empty strings; the caller skips them.
func splitYAMLDocs(stream string) []string {
	lines := strings.Split(stream, "\n")
	docs := []string{}
	current := []string{}
	flush := func() {
		if len(current) == 0 {
			return
		}
		doc := strings.Join(current, "\n")
		current = current[:0]
		if strings.TrimSpace(doc) == "" {
			return
		}
		docs = append(docs, doc)
	}
	for _, l := range lines {
		if strings.TrimSpace(l) == "---" {
			flush()
			continue
		}
		current = append(current, l)
	}
	flush()
	return docs
}
func diffYAMLDocuments(fromYAML, toYAML string) ([]HelmDiffItem, error) {
	from, err := ytbx.LoadDocuments([]byte(fromYAML))
	if err != nil {
		return nil, fmt.Errorf("load from documents: %w", err)
	}
	to, err := ytbx.LoadDocuments([]byte(toYAML))
	if err != nil {
		return nil, fmt.Errorf("load to documents: %w", err)
	}

	report, err := dyff.CompareInputFiles(
		ytbx.InputFile{Location: "from", Documents: from},
		ytbx.InputFile{Location: "to", Documents: to},
	)
	if err != nil {
		return nil, fmt.Errorf("dyff compare: %w", err)
	}

	out := make([]HelmDiffItem, 0, len(report.Diffs))
	for _, d := range report.Diffs {
		path := ""
		if d.Path != nil {
			path = d.Path.String()
		}
		for _, det := range d.Details {
			out = append(out, HelmDiffItem{
				Path:   path,
				Kind:   diffKindLabel(det.Kind),
				Before: nodeToString(det.From),
				After:  nodeToString(det.To),
			})
		}
	}
	return out, nil
}

// diffKindLabel maps dyff's rune-flagged Detail.Kind into stable
// string labels for the SPA + agent-tool consumers. dyff source:
// pkg/dyff/models.go (ADDITION='+', REMOVAL='-', MODIFICATION='±',
// ORDERCHANGE='⇆').
func diffKindLabel(r rune) string {
	switch r {
	case '±':
		return "modify"
	case '+':
		return "add"
	case '-':
		return "remove"
	case '⇆':
		return "order"
	default:
		return string(r)
	}
}

func nodeToString(n *yamlv3.Node) string {
	if n == nil {
		return ""
	}
	if n.Kind == yamlv3.ScalarNode {
		return n.Value
	}
	buf, err := yamlv3.Marshal(n)
	if err != nil {
		return ""
	}
	return string(buf)
}
