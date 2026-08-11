package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConstrainedGraphQueryFixtureManifest(t *testing.T) {
	root := filepath.Join("fixtures", "constrained-graph-query-v1")
	body, err := os.ReadFile(filepath.Join(root, "manifest.json")); if err != nil { t.Fatal(err) }
	var manifest struct { FixtureVersion string `json:"fixture_version"`; Algorithm string `json:"algorithm"`; Files map[string]string `json:"files"` }
	if err := json.Unmarshal(body, &manifest); err != nil { t.Fatal(err) }
	if manifest.FixtureVersion != "1.0" || manifest.Algorithm != "sha256" || len(manifest.Files) == 0 { t.Fatalf("manifest=%#v", manifest) }
	for name, expected := range manifest.Files {
		contents, err := os.ReadFile(filepath.Join(root, name)); if err != nil { t.Fatal(err) }
		sum := sha256.Sum256(contents); if got := hex.EncodeToString(sum[:]); got != expected { t.Fatalf("%s digest=%s want %s", name, got, expected) }
	}
}
