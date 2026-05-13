package iam

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fixturePair is one (input, optional-golden) pair from the
// testdata corpus. Goldens are written by the parser/engine tests
// via the -update flag once those land; for the wire-shape commit
// the corpus is input-only and the smoke test just validates JSON
// parsability.
type fixturePair struct {
	Name   string // e.g. "managed/AdministratorAccess"
	Input  []byte // raw policy JSON
	Golden []byte // expected RolePermissionsResult JSON; nil if no golden file
}

// loadFixturePairs walks testdata/<subdir>/ and returns every
// .json file (excluding .golden.json files) paired with its
// matching .golden.json if present. Returns pairs sorted by name
// for deterministic test ordering.
//
// Subdirs: managed, sensitive, wildcard, pathological.
func loadFixturePairs(t *testing.T, subdir string) []fixturePair {
	t.Helper()
	dir := filepath.Join("testdata", subdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read fixture dir %s: %v", dir, err)
	}

	var pairs []fixturePair
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".golden.json") {
			continue
		}
		base := strings.TrimSuffix(name, ".json")
		inputPath := filepath.Join(dir, name)
		input, err := os.ReadFile(inputPath)
		if err != nil {
			t.Fatalf("read %s: %v", inputPath, err)
		}
		goldenPath := filepath.Join(dir, base+".golden.json")
		var golden []byte
		if g, err := os.ReadFile(goldenPath); err == nil {
			golden = g
		}
		pairs = append(pairs, fixturePair{
			Name:   subdir + "/" + base,
			Input:  input,
			Golden: golden,
		})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Name < pairs[j].Name })
	return pairs
}

// loadFixture is the single-fixture loader for tests that target
// one specific case (e.g. the malformed-json error path).
func loadFixture(t *testing.T, relPath string) []byte {
	t.Helper()
	full := filepath.Join("testdata", relPath)
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("read %s: %v", full, err)
	}
	return data
}
