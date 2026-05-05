package k8s

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"sigs.k8s.io/yaml"
)

// unpackChart parses a chart tar.gz into a ChartFetchResult.
//
// Chart format (per Helm's spec):
//   <name>/Chart.yaml             — required, chart metadata
//   <name>/values.yaml            — optional but expected, default values
//   <name>/values.schema.json     — optional, JSON Schema for values
//   <name>/templates/             — ignored (SPA doesn't render templates)
//   <name>/charts/<sub>/...       — sub-chart deps; we reject if non-empty
//   <name>/...                    — other files (NOTES.txt, README.md) ignored
//
// Returns:
//   - ChartFetchResult on success
//   - ErrChartInvalid for malformed archives / missing Chart.yaml
//   - ErrChartUnsupportedDeps when Chart.yaml lists dependencies OR
//     when the archive contains a non-empty <name>/charts/ directory
func unpackChart(tarball []byte) (ChartFetchResult, error) {
	if len(tarball) == 0 {
		return ChartFetchResult{}, fmt.Errorf("%w: empty payload", ErrChartInvalid)
	}
	if len(tarball) > MaxChartBytes {
		return ChartFetchResult{}, fmt.Errorf("%w: chart exceeds %d bytes", ErrChartInvalid, MaxChartBytes)
	}

	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return ChartFetchResult{}, fmt.Errorf("%w: gzip: %v", ErrChartInvalid, err)
	}
	defer func() { _ = gz.Close() }()

	var (
		chartYAML  []byte
		valuesYAML []byte
		schemaJSON []byte
		// chartNameDir is the top-level folder name in the tarball
		// ("nginx" for nginx-1.0.0.tgz). We only allow files under
		// this prefix; everything else is rejected as malformed.
		chartNameDir string
		// hasSubChart means the tarball includes <name>/charts/<sub>/
		// — we reject these even before parsing Chart.yaml because
		// they're already a dependency violation.
		hasSubChart bool
	)

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ChartFetchResult{}, fmt.Errorf("%w: tar: %v", ErrChartInvalid, err)
		}
		// Defense against tar path traversal — reject any entry
		// containing ".." or starting with "/".
		clean := path.Clean(hdr.Name)
		if strings.Contains(clean, "..") || strings.HasPrefix(clean, "/") {
			return ChartFetchResult{}, fmt.Errorf("%w: unsafe tar path %q", ErrChartInvalid, hdr.Name)
		}

		segments := strings.Split(clean, "/")
		if len(segments) == 0 {
			continue
		}
		// First time we see anything, record the chart-name
		// directory. Reject if any later entry doesn't share it
		// (the chart spec requires a single top-level dir).
		if chartNameDir == "" {
			chartNameDir = segments[0]
		} else if segments[0] != chartNameDir {
			return ChartFetchResult{}, fmt.Errorf("%w: multiple top-level dirs in tarball", ErrChartInvalid)
		}

		if hdr.Typeflag == tar.TypeDir {
			continue
		}

		// Sub-chart detection. Even an empty charts/ dir doesn't
		// trip us; only actual files under it count as deps.
		if len(segments) >= 3 && segments[1] == "charts" {
			hasSubChart = true
			continue
		}

		// Only read files we care about; everything else is
		// streamed-past for memory safety.
		switch {
		case len(segments) == 2 && segments[1] == "Chart.yaml":
			chartYAML, err = readCapped(tr, MaxChartBytes)
		case len(segments) == 2 && segments[1] == "values.yaml":
			valuesYAML, err = readCapped(tr, MaxChartBytes)
		case len(segments) == 2 && segments[1] == "values.schema.json":
			schemaJSON, err = readCapped(tr, MaxChartBytes)
		}
		if err != nil {
			return ChartFetchResult{}, fmt.Errorf("%w: read %s: %v", ErrChartInvalid, hdr.Name, err)
		}
	}

	if len(chartYAML) == 0 {
		return ChartFetchResult{}, fmt.Errorf("%w: Chart.yaml missing from tarball", ErrChartInvalid)
	}

	var meta ChartMeta
	if err := yaml.Unmarshal(chartYAML, &meta); err != nil {
		return ChartFetchResult{}, fmt.Errorf("%w: Chart.yaml: %v", ErrChartInvalid, err)
	}
	// Default APIVersion when missing (Helm v1 charts pre-3.0).
	if meta.APIVersion == "" {
		meta.APIVersion = "v1"
	}

	// Reject sub-charts. Either the tarball had files under
	// <name>/charts/, or Chart.yaml's `dependencies:` is non-empty.
	// Both cases surface the same typed error so callers can render
	// the deps list with a clear "not yet supported" message.
	if hasSubChart || len(meta.Dependencies) > 0 {
		return ChartFetchResult{}, ErrChartUnsupportedDeps
	}

	result := ChartFetchResult{
		Meta:   meta,
		Values: string(valuesYAML),
	}
	if len(schemaJSON) > 0 {
		var schema map[string]interface{}
		if err := json.Unmarshal(schemaJSON, &schema); err != nil {
			return ChartFetchResult{}, fmt.Errorf("%w: values.schema.json: %v", ErrChartInvalid, err)
		}
		result.Schema = schema
	}
	return result, nil
}

func readCapped(r io.Reader, max int) ([]byte, error) {
	limited := io.LimitReader(r, int64(max)+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(buf) > max {
		return nil, fmt.Errorf("file exceeds %d bytes", max)
	}
	return buf, nil
}
