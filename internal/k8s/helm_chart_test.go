package k8s

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── unpackChart tests ──────────────────────────────────────────────

func TestUnpackChart_Success(t *testing.T) {
	tarball := buildChartTarball(t, chartFiles{
		root: "nginx",
		chartYAML: `apiVersion: v2
name: nginx
version: 1.2.3
appVersion: "1.25.4"
description: NGINX chart
kubeVersion: ">=1.24"
keywords: [web, proxy]
maintainers:
  - name: alice
    email: alice@example.com
`,
		valuesYAML: "replicaCount: 3\nimage:\n  tag: latest\n",
		schema:     `{"type":"object","properties":{"replicaCount":{"type":"integer"}}}`,
	})

	got, err := unpackChart(tarball)
	if err != nil {
		t.Fatalf("unpackChart: %v", err)
	}
	if got.Meta.Name != "nginx" {
		t.Errorf("Name = %q, want nginx", got.Meta.Name)
	}
	if got.Meta.Version != "1.2.3" {
		t.Errorf("Version = %q, want 1.2.3", got.Meta.Version)
	}
	if got.Meta.AppVersion != "1.25.4" {
		t.Errorf("AppVersion = %q, want 1.25.4", got.Meta.AppVersion)
	}
	if got.Meta.KubeVersion != ">=1.24" {
		t.Errorf("KubeVersion = %q, want >=1.24", got.Meta.KubeVersion)
	}
	if !strings.Contains(got.Values, "replicaCount: 3") {
		t.Errorf("values.yaml not preserved: %q", got.Values)
	}
	if got.Schema == nil {
		t.Errorf("Schema not parsed; got nil")
	}
}

func TestUnpackChart_RejectsDependencies(t *testing.T) {
	tarball := buildChartTarball(t, chartFiles{
		root: "wordpress",
		chartYAML: `apiVersion: v2
name: wordpress
version: 1.0.0
dependencies:
  - name: mariadb
    version: 11.0.0
    repository: "https://charts.bitnami.com/bitnami"
`,
		valuesYAML: "",
	})
	_, err := unpackChart(tarball)
	if !errors.Is(err, ErrChartUnsupportedDeps) {
		t.Errorf("err = %v, want ErrChartUnsupportedDeps", err)
	}
}

func TestUnpackChart_RejectsSubChartFiles(t *testing.T) {
	// Even when Chart.yaml has no `dependencies:`, a populated
	// charts/ subdirectory in the tarball is a dep we don't support.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	writeTarFile(tw, "main/Chart.yaml", `apiVersion: v2
name: main
version: 1.0.0
`)
	writeTarFile(tw, "main/values.yaml", "")
	writeTarFile(tw, "main/charts/sub/Chart.yaml", `apiVersion: v2
name: sub
version: 1.0.0
`)
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	_, err := unpackChart(buf.Bytes())
	if !errors.Is(err, ErrChartUnsupportedDeps) {
		t.Errorf("err = %v, want ErrChartUnsupportedDeps", err)
	}
}

func TestUnpackChart_MissingChartYAML(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	writeTarFile(tw, "alone/values.yaml", "key: value")
	tw.Close()
	gz.Close()
	_, err := unpackChart(buf.Bytes())
	if !errors.Is(err, ErrChartInvalid) {
		t.Errorf("err = %v, want ErrChartInvalid", err)
	}
}

func TestUnpackChart_RejectsTarPathTraversal(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	writeTarFile(tw, "../../etc/passwd", "evil")
	tw.Close()
	gz.Close()
	_, err := unpackChart(buf.Bytes())
	if !errors.Is(err, ErrChartInvalid) {
		t.Errorf("err = %v, want ErrChartInvalid", err)
	}
}

func TestUnpackChart_DefaultsAPIVersion(t *testing.T) {
	tarball := buildChartTarball(t, chartFiles{
		root: "old",
		chartYAML: `name: old
version: 1.0.0
`, // no apiVersion
		valuesYAML: "",
	})
	got, err := unpackChart(tarball)
	if err != nil {
		t.Fatalf("unpackChart: %v", err)
	}
	if got.Meta.APIVersion != "v1" {
		t.Errorf("APIVersion default = %q, want v1", got.Meta.APIVersion)
	}
}

// ── sortAndCapVersions tests ──────────────────────────────────────

func TestSortAndCapVersions(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "newest first by semver",
			in:   []string{"1.0.0", "2.1.3", "1.5.0", "2.0.0"},
			want: []string{"2.1.3", "2.0.0", "1.5.0", "1.0.0"},
		},
		{
			name: "drops non-semver tags",
			in:   []string{"latest", "main", "dev", "1.2.3", "nightly"},
			want: []string{"1.2.3"},
		},
		{
			name: "preserves v-prefix when supplied",
			in:   []string{"v1.0.0", "v0.9.0"},
			want: []string{"v1.0.0", "v0.9.0"},
		},
		{
			name: "mixed v-prefix and bare",
			in:   []string{"v2.0.0", "1.5.0", "v1.0.0"},
			want: []string{"v2.0.0", "1.5.0", "v1.0.0"},
		},
		{
			name: "empty input",
			in:   []string{},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sortAndCapVersions(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d (got=%v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestSortAndCapVersions_CapsAtMaxVersionsReturned(t *testing.T) {
	in := make([]string, MaxVersionsReturned+10)
	for i := range in {
		in[i] = fmt.Sprintf("1.0.%d", i)
	}
	got := sortAndCapVersions(in)
	if len(got) != MaxVersionsReturned {
		t.Errorf("len = %d, want %d", len(got), MaxVersionsReturned)
	}
}

// ── HTTP repo fetch via httptest ──────────────────────────────────

func TestFetchHTTPVersions_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.yaml" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `apiVersion: v1
entries:
  nginx:
    - name: nginx
      version: 1.5.0
      urls: ["nginx-1.5.0.tgz"]
    - name: nginx
      version: 1.4.2
      urls: ["nginx-1.4.2.tgz"]
    - name: nginx
      version: 0.9.0
      urls: ["nginx-0.9.0.tgz"]
  redis:
    - name: redis
      version: 7.0.0
      urls: ["redis-7.0.0.tgz"]
`)
	}))
	defer srv.Close()

	got, err := fetchHTTPVersions(context.Background(), srv.URL, "nginx")
	if err != nil {
		t.Fatalf("fetchHTTPVersions: %v", err)
	}
	if got.Latest != "1.5.0" {
		t.Errorf("Latest = %q, want 1.5.0", got.Latest)
	}
	if len(got.Versions) != 3 {
		t.Errorf("Versions len = %d, want 3", len(got.Versions))
	}
	if got.Versions[0] != "1.5.0" {
		t.Errorf("Versions[0] = %q, want 1.5.0", got.Versions[0])
	}
}

func TestFetchHTTPVersions_ChartNotInIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `apiVersion: v1
entries:
  nginx:
    - name: nginx
      version: 1.0.0
      urls: ["nginx-1.0.0.tgz"]
`)
	}))
	defer srv.Close()

	_, err := fetchHTTPVersions(context.Background(), srv.URL, "missing")
	if !errors.Is(err, ErrChartNotFound) {
		t.Errorf("err = %v, want ErrChartNotFound", err)
	}
}

func TestFetchHTTPVersions_RegistryUnreachable(t *testing.T) {
	// Closed server → connection refused.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()
	_, err := fetchHTTPVersions(context.Background(), srv.URL, "nginx")
	if !errors.Is(err, ErrChartUnreachable) {
		t.Errorf("err = %v, want ErrChartUnreachable", err)
	}
}

func TestFetchHTTPVersions_Returns404AsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	_, err := fetchHTTPVersions(context.Background(), srv.URL, "x")
	if !errors.Is(err, ErrChartNotFound) {
		t.Errorf("err = %v, want ErrChartNotFound", err)
	}
}

func TestFetchHTTPVersions_ReturnsAuthAsUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := fetchHTTPVersions(context.Background(), srv.URL, "x")
	if !errors.Is(err, ErrChartUnauthorized) {
		t.Errorf("err = %v, want ErrChartUnauthorized", err)
	}
}

func TestFetchHTTPChartTarball_Success(t *testing.T) {
	tarballBytes := buildChartTarball(t, chartFiles{
		root:       "nginx",
		chartYAML:  "apiVersion: v2\nname: nginx\nversion: 1.0.0\n",
		valuesYAML: "replicaCount: 1\n",
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/index.yaml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `apiVersion: v1
entries:
  nginx:
    - name: nginx
      version: 1.0.0
      urls: ["nginx-1.0.0.tgz"]
`)
	})
	mux.HandleFunc("/nginx-1.0.0.tgz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(tarballBytes)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := fetchHTTPChartTarball(context.Background(), srv.URL, "nginx", "1.0.0")
	if err != nil {
		t.Fatalf("fetchHTTPChartTarball: %v", err)
	}
	if !bytes.Equal(got, tarballBytes) {
		t.Errorf("tarball roundtrip mismatch")
	}
}

func TestFetchHTTPChartTarball_VersionNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `apiVersion: v1
entries:
  nginx:
    - name: nginx
      version: 1.0.0
      urls: ["nginx-1.0.0.tgz"]
`)
	}))
	defer srv.Close()
	_, err := fetchHTTPChartTarball(context.Background(), srv.URL, "nginx", "99.0.0")
	if !errors.Is(err, ErrChartVersionNotFound) {
		t.Errorf("err = %v, want ErrChartVersionNotFound", err)
	}
}

// ── Top-level FetchChartVersions / FetchChartValues ───────────────

func TestFetchChartValues_HTTPHappyPath(t *testing.T) {
	tarballBytes := buildChartTarball(t, chartFiles{
		root: "nginx",
		chartYAML: `apiVersion: v2
name: nginx
version: 1.0.0
description: web server
`,
		valuesYAML: "key: value\n",
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/index.yaml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `apiVersion: v1
entries:
  nginx:
    - {name: nginx, version: 1.0.0, urls: ["nginx-1.0.0.tgz"]}
`)
	})
	mux.HandleFunc("/nginx-1.0.0.tgz", func(w http.ResponseWriter, r *http.Request) {
		w.Write(tarballBytes)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got, err := FetchChartValues(context.Background(), FetchValuesArgs{
		Ref: srv.URL, ChartName: "nginx", Version: "1.0.0",
	})
	if err != nil {
		t.Fatalf("FetchChartValues: %v", err)
	}
	if got.Meta.Name != "nginx" {
		t.Errorf("Name = %q, want nginx", got.Meta.Name)
	}
	if got.Meta.Description != "web server" {
		t.Errorf("Description = %q, want %q", got.Meta.Description, "web server")
	}
	if got.Values != "key: value\n" {
		t.Errorf("Values mismatch: %q", got.Values)
	}
}

func TestFetchChartVersions_RejectsBadScheme(t *testing.T) {
	_, err := FetchChartVersions(context.Background(), FetchVersionsArgs{
		Ref: "ftp://wrong.example.com", ChartName: "x",
	})
	if !errors.Is(err, ErrChartUnsupportedRef) {
		t.Errorf("err = %v, want ErrChartUnsupportedRef", err)
	}
}

func TestFetchChartValues_RejectsEmptyVersion(t *testing.T) {
	_, err := FetchChartValues(context.Background(), FetchValuesArgs{
		Ref: "https://example.com", ChartName: "x", Version: "",
	})
	if err == nil {
		t.Fatal("expected error for empty version")
	}
}

func TestFetchChartValues_RejectsHTTPRefWithoutChartName(t *testing.T) {
	_, err := FetchChartValues(context.Background(), FetchValuesArgs{
		Ref: "https://example.com", ChartName: "", Version: "1.0.0",
	})
	if !errors.Is(err, ErrChartUnsupportedRef) {
		t.Errorf("err = %v, want ErrChartUnsupportedRef", err)
	}
}

func TestFetchChartVersions_TimesOut(t *testing.T) {
	// Server hangs; client should give up via context deadline.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := FetchChartVersions(ctx, FetchVersionsArgs{Ref: srv.URL, ChartName: "x"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

// ── Test helpers ──────────────────────────────────────────────────

type chartFiles struct {
	root       string
	chartYAML  string
	valuesYAML string
	schema     string // empty = no schema file
}

func buildChartTarball(t *testing.T, f chartFiles) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	writeTarFile(tw, f.root+"/Chart.yaml", f.chartYAML)
	writeTarFile(tw, f.root+"/values.yaml", f.valuesYAML)
	if f.schema != "" {
		writeTarFile(tw, f.root+"/values.schema.json", f.schema)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	return buf.Bytes()
}

func writeTarFile(tw *tar.Writer, name, body string) {
	_ = tw.WriteHeader(&tar.Header{
		Name: name,
		Mode: 0o644,
		Size: int64(len(body)),
	})
	_, _ = tw.Write([]byte(body))
}

