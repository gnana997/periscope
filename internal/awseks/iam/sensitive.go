package iam

import (
	_ "embed"
	"fmt"
	"path"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// SensitivePermissionsCatalogVersion is the version string returned
// on every RolePermissionsResult.CatalogVersion. Bump in
// sensitive.yaml's `version` field when entries change; this var
// is populated from the YAML at init() time so the two stay in sync.
var SensitivePermissionsCatalogVersion = ""

// catalogYAML embeds the YAML source so the catalog is a single file
// reviewers can read without grepping Go code. Loaded at init() into
// defaultCatalog.
//
//go:embed sensitive.yaml
var catalogYAML []byte

// Catalog answers "is this action sensitive, and if so, which
// category?" against the locked sensitive-permissions list. Built
// once at process start from the embedded YAML.
//
// Lookup is O(1) for exact entries and O(n) for pattern entries
// (n ≤ ~20). The literal "*" action is handled outside the catalog
// — see Classify.
type Catalog struct {
	Version  string
	exact    map[string]SensitiveCategory
	patterns []patternEntry
}

type patternEntry struct {
	pattern  string // lower-cased glob
	category SensitiveCategory
}

// defaultCatalog is the process-wide catalog parsed from
// sensitive.yaml at init() time. Engine constructors use it by
// default; tests can build a Catalog from arbitrary YAML for
// scenario coverage.
var defaultCatalog = mustLoadCatalog(catalogYAML)

// DefaultCatalog returns the process-wide sensitive-permissions
// catalog. Safe for concurrent reads — Catalog is immutable after
// load.
func DefaultCatalog() *Catalog { return defaultCatalog }

// LoadCatalog parses a YAML catalog blob into a *Catalog. Tests use
// this to build alternative catalogs; production code uses
// DefaultCatalog().
func LoadCatalog(data []byte) (*Catalog, error) {
	var raw struct {
		Version string                       `json:"version"`
		Entries map[string]SensitiveCategory `json:"entries"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("iam: parse sensitive catalog YAML: %w", err)
	}
	if raw.Version == "" {
		return nil, fmt.Errorf("iam: sensitive catalog missing version")
	}
	c := &Catalog{
		Version: raw.Version,
		exact:   make(map[string]SensitiveCategory, len(raw.Entries)),
	}
	for k, v := range raw.Entries {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" {
			continue
		}
		if !validCategory(v) {
			return nil, fmt.Errorf("iam: catalog entry %q has unknown category %q", k, v)
		}
		if strings.ContainsAny(k, "*?") {
			c.patterns = append(c.patterns, patternEntry{pattern: k, category: v})
			continue
		}
		c.exact[k] = v
	}
	return c, nil
}

func mustLoadCatalog(data []byte) *Catalog {
	c, err := LoadCatalog(data)
	if err != nil {
		panic("iam: sensitive catalog failed to load — fix sensitive.yaml: " + err.Error())
	}
	SensitivePermissionsCatalogVersion = c.Version
	return c
}

// Classify reports whether action is in the sensitive catalog and,
// if so, which category. The literal "*" action always returns
// SensitiveWildcard (handled outside the YAML so operators can't
// remove the wildcard fallback).
//
// Matching is case-insensitive — IAM actions are case-insensitive
// in evaluation, so we normalize the query before lookup.
//
// Lookup order: literal "*" → exact → pattern.
func (c *Catalog) Classify(action string) (SensitiveCategory, bool) {
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		return "", false
	}
	if action == "*" {
		return SensitiveWildcard, true
	}
	if cat, ok := c.exact[action]; ok {
		return cat, true
	}
	for _, p := range c.patterns {
		matched, err := path.Match(p.pattern, action)
		if err == nil && matched {
			return p.category, true
		}
	}
	return "", false
}

// Size reports the total number of catalog entries (exact + pattern).
// The literal "*" wildcard is NOT counted here — it's a runtime
// fallback, not a YAML-defined entry.
func (c *Catalog) Size() int {
	return len(c.exact) + len(c.patterns)
}

// CatalogEntry is one row exposed via the cluster-agnostic
// /api/identity/sensitive-catalog endpoint so the SPA's chip
// palette and the reverse-lookup autocomplete share the server's
// source of truth.
type CatalogEntry struct {
	Action   string            `json:"action"`
	Category SensitiveCategory `json:"category"`
	Pattern  bool              `json:"pattern"`
}

// Entries returns a stable, deterministic snapshot of the catalog's
// rows for serialisation. Sorted alphabetically by action so the
// wire format is byte-stable across calls (cheap to diff in tests
// + good for client-side ETag caching if added later).
//
// The literal "*" action is NOT included — it's a runtime
// classifier rule, not a YAML entry, and including it would invite
// "operators can disable wildcard" misuse.
func (c *Catalog) Entries() []CatalogEntry {
	out := make([]CatalogEntry, 0, len(c.exact)+len(c.patterns))
	for action, cat := range c.exact {
		out = append(out, CatalogEntry{Action: action, Category: cat, Pattern: false})
	}
	for _, p := range c.patterns {
		out = append(out, CatalogEntry{Action: p.pattern, Category: p.category, Pattern: true})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Action < out[j].Action
	})
	return out
}

func validCategory(c SensitiveCategory) bool {
	switch c {
	case SensitivePrivEsc,
		SensitiveData,
		SensitiveCrossAccount,
		SensitiveDestructive,
		SensitiveCluster,
		SensitiveWildcard:
		return true
	}
	return false
}
