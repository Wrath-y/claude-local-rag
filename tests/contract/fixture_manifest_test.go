package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestGraphSnapshotFixtureManifest(t *testing.T) {
	dir := filepath.Join("fixtures", "graph-snapshot-v1")
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		FixtureVersion string            `json:"fixture_version"`
		Files          map[string]string `json:"files"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.FixtureVersion != "1.0" || len(manifest.Files) == 0 {
		t.Fatalf("invalid fixture manifest: %#v", manifest)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "manifest.json" || entry.Name() == "README.md" {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if len(names) != len(manifest.Files) {
		t.Fatalf("manifest files=%d, fixture files=%d", len(manifest.Files), len(names))
	}
	for _, name := range names {
		want, ok := manifest.Files[name]
		if !ok {
			t.Fatalf("fixture %s is not in manifest", name)
		}
		contents, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		actual := sha256.Sum256(contents)
		if got := hex.EncodeToString(actual[:]); got != want {
			t.Fatalf("fixture %s digest=%s, want %s", name, got, want)
		}
	}
}
