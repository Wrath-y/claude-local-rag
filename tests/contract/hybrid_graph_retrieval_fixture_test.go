package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHybridGraphRetrievalFixtureManifest(t *testing.T) {
	root := filepath.Join("fixtures", "hybrid-graph-retrieval-v1")
	body, err := os.ReadFile(filepath.Join(root, "manifest.json"))
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
	if manifest.FixtureVersion != "1.0" || manifest.Algorithm != "sha256" || len(manifest.Files) != 5 {
		t.Fatalf("manifest=%#v", manifest)
	}
	for name, expected := range manifest.Files {
		contents, readErr := os.ReadFile(filepath.Join(root, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		sum := sha256.Sum256(contents)
		if actual := hex.EncodeToString(sum[:]); actual != expected {
			t.Fatalf("%s digest=%s want %s", name, actual, expected)
		}
	}
}
