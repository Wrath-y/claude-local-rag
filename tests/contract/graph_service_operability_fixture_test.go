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

func TestGraphServiceOperabilityFixtureManifest(t *testing.T) {
	dir := filepath.Join("fixtures", "graph-service-operability-v1")
	body, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		FixtureVersion string            `json:"fixture_version"`
		Algorithm      string            `json:"algorithm"`
		Files          map[string]string `json:"files"`
	}
	if err = json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.FixtureVersion != "1.0" || manifest.Algorithm != "sha256" || len(manifest.Files) != 7 {
		t.Fatalf("manifest=%#v", manifest)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && entry.Name() != "README.md" && entry.Name() != "manifest.json" {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) != len(manifest.Files) {
		t.Fatalf("files=%v manifest=%v", names, manifest.Files)
	}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != manifest.Files[name] {
			t.Fatalf("%s=%s want %s", name, got, manifest.Files[name])
		}
	}
}
