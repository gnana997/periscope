package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

func jsonUnmarshalManifest(body []byte, m *ocispec.Manifest) error {
	return json.Unmarshal(body, m)
}

// OCI chart fetcher via oras-go/v2.
//
// Helm-on-OCI manifest layout:
//
//	manifest.config.mediaType   = application/vnd.cncf.helm.config.v1+json
//	manifest.layers[0].mediaType = application/vnd.cncf.helm.chart.content.v1.tar+gzip
//
// The config layer contains Chart.yaml-as-JSON; the content layer is
// the chart tarball. We pull the content layer, hand it to unpackChart,
// and trust unpackChart's own decoder for everything chart-shaped.
//
// Tag listing uses the OCI distribution v2 spec's /v2/<name>/tags/list
// endpoint via oras-go's Tags(); we filter to chart-typed tags by
// fetching each manifest's config.mediaType (one extra HEAD per tag).
// For repos with hundreds of tags this would be expensive, so we cap
// the inspection count and bail early once we have enough semver-
// shaped chart tags for the picker.

// Helm OCI media types (frozen in the Helm spec; we reference rather
// than import to avoid pulling helm.sh/helm).
const (
	mediaTypeHelmConfig    = "application/vnd.cncf.helm.config.v1+json"
	mediaTypeHelmChartTGZ  = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"
	mediaTypeHelmProvFile  = "application/vnd.cncf.helm.chart.provenance.v1.prov"

	// ociTagInspectMax bounds how many tags we'll manifest-probe to
	// classify. Big org registries can have hundreds of tags across
	// containers + charts in the same repo path; we don't need to
	// inspect all of them.
	ociTagInspectMax = 200
)

// fetchOCIVersions lists tags for an oci:// ref, filters to ones
// that are Helm charts (by media type) and parse as semver, sorts
// newest-first, and caps at MaxVersionsReturned.
func fetchOCIVersions(ctx context.Context, ref string) (ChartVersionsResult, error) {
	repo, err := newOCIRepo(ref)
	if err != nil {
		return ChartVersionsResult{}, err
	}

	var rawTags []string
	tagsErr := repo.Tags(ctx, "", func(tags []string) error {
		rawTags = append(rawTags, tags...)
		// Stop once we've collected enough that even the worst-case
		// (every other tag is a non-chart non-semver) leaves us
		// plenty of headroom for filtering.
		if len(rawTags) >= ociTagInspectMax {
			return io.EOF
		}
		return nil
	})
	if tagsErr != nil && !errors.Is(tagsErr, io.EOF) {
		return ChartVersionsResult{}, classifyOCIErr(tagsErr)
	}

	// Pre-filter to semver-shaped tags before we burn manifest
	// HEADs on non-chart-looking entries (latest, main, sha tags).
	semverTags := sortAndCapVersions(rawTags)

	// For each candidate, verify it's a Helm chart by inspecting
	// the manifest's config.mediaType. Non-chart artifacts colocated
	// in the same repo path get filtered out here.
	confirmed := make([]string, 0, len(semverTags))
	for _, tag := range semverTags {
		isChart, err := ociTagIsHelmChart(ctx, repo, tag)
		if err != nil {
			// One failed manifest probe shouldn't kill the whole
			// listing — the registry might be flaky for one tag.
			// Log and skip.
			continue
		}
		if isChart {
			confirmed = append(confirmed, tag)
		}
		if len(confirmed) >= MaxVersionsReturned {
			break
		}
	}

	out := ChartVersionsResult{
		Ref:      ref,
		Versions: confirmed,
	}
	if len(confirmed) > 0 {
		out.Latest = confirmed[0]
	}
	return out, nil
}

// fetchOCIChartTarball pulls the chart content layer for (ref, version).
// Walks: ref → manifest → find layer with helm-chart media type → fetch.
func fetchOCIChartTarball(ctx context.Context, ref, version string) ([]byte, error) {
	repo, err := newOCIRepo(ref)
	if err != nil {
		return nil, err
	}
	desc, err := repo.Resolve(ctx, version)
	if err != nil {
		return nil, classifyOCIErr(err)
	}
	manifest, err := fetchOCIManifest(ctx, repo, desc)
	if err != nil {
		return nil, err
	}
	if manifest.Config.MediaType != mediaTypeHelmConfig {
		return nil, fmt.Errorf("%w: config media type %q", ErrChartNotAChart, manifest.Config.MediaType)
	}
	for _, layer := range manifest.Layers {
		if layer.MediaType != mediaTypeHelmChartTGZ {
			continue
		}
		rc, err := repo.Fetch(ctx, layer)
		if err != nil {
			return nil, classifyOCIErr(err)
		}
		defer rc.Close()
		return readCapped(rc, MaxChartBytes)
	}
	return nil, fmt.Errorf("%w: no helm-chart layer in manifest", ErrChartNotAChart)
}

// newOCIRepo parses an oci:// ref into the registry + repo path
// oras-go expects, and returns a configured Repository client. v1.1
// is unauthenticated only — the auth.Client uses zero-value creds,
// which sends no Authorization header. Public OCI repos respond
// directly; private ones return 401 and we surface ErrChartUnauthorized.
func newOCIRepo(ref string) (*remote.Repository, error) {
	if !strings.HasPrefix(ref, "oci://") {
		return nil, ErrChartUnsupportedRef
	}
	stripped := strings.TrimPrefix(ref, "oci://")
	repo, err := remote.NewRepository(stripped)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrChartUnsupportedRef, err)
	}
	repo.Client = &auth.Client{
		Client: retry.DefaultClient,
		Header: map[string][]string{
			"User-Agent": {"periscope-chart-fetcher"},
		},
		// No Credential function — public refs only in v1.1.
	}
	// PlainHTTP mostly applies to localhost/dev registries. Defaults
	// to false (HTTPS), which is what production registries expect.
	return repo, nil
}

// ociTagIsHelmChart fetches just the manifest for a tag and inspects
// its config.mediaType. Cheaper than pulling the full chart.
func ociTagIsHelmChart(ctx context.Context, repo *remote.Repository, tag string) (bool, error) {
	desc, err := repo.Resolve(ctx, tag)
	if err != nil {
		return false, classifyOCIErr(err)
	}
	manifest, err := fetchOCIManifest(ctx, repo, desc)
	if err != nil {
		return false, err
	}
	return manifest.Config.MediaType == mediaTypeHelmConfig, nil
}

func fetchOCIManifest(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor) (*ocispec.Manifest, error) {
	rc, err := repo.Fetch(ctx, desc)
	if err != nil {
		return nil, classifyOCIErr(err)
	}
	defer rc.Close()
	body, err := readCapped(rc, 1*1024*1024) // manifests are small
	if err != nil {
		return nil, fmt.Errorf("%w: read manifest: %v", ErrChartUnreachable, err)
	}
	manifest := &ocispec.Manifest{}
	if err := jsonUnmarshalManifest(body, manifest); err != nil {
		return nil, fmt.Errorf("%w: parse manifest: %v", ErrChartInvalid, err)
	}
	return manifest, nil
}

// classifyOCIErr collapses oras-go transport errors into our typed
// sentinels. oras returns *errcode.ErrorResponse for HTTP-level
// failures; we string-match the common cases since ErrorResponse's
// type isn't stable enough to errors.As against across versions.
func classifyOCIErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrChartTimeout
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "404") || strings.Contains(strings.ToLower(msg), "not found"):
		return fmt.Errorf("%w: %v", ErrChartNotFound, err)
	case strings.Contains(msg, "401") || strings.Contains(msg, "403") || strings.Contains(strings.ToLower(msg), "unauthorized"):
		return ErrChartUnauthorized
	}
	return fmt.Errorf("%w: %v", ErrChartUnreachable, err)
}
