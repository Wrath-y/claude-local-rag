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

func TestOperabilityConsumerUsesAPIAndSchemaRatherThanServiceSemVer(t *testing.T) {
	decode := func(serviceVersion string) struct {
		APIs         []string `json:"api_versions"`
		Schemas      []string `json:"supported_schema_versions"`
		Capabilities []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"capabilities"`
	} {
		payload := []byte(`{"service_version":"` + serviceVersion + `","api_versions":["v1"],"supported_schema_versions":["1.0"],"capabilities":[{"name":"task_polling","state":"available"}],"future_addition":{"ignored":true}}`)
		var consumer struct {
			APIs         []string `json:"api_versions"`
			Schemas      []string `json:"supported_schema_versions"`
			Capabilities []struct {
				Name  string `json:"name"`
				State string `json:"state"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal(payload, &consumer); err != nil {
			t.Fatal(err)
		}
		return consumer
	}
	for _, version := range []string{"0.0.0-dev", "9.99.0"} {
		consumer := decode(version)
		if len(consumer.APIs) != 1 || consumer.APIs[0] != "v1" || len(consumer.Schemas) != 1 || consumer.Schemas[0] != "1.0" || len(consumer.Capabilities) != 1 || consumer.Capabilities[0].Name != "task_polling" || consumer.Capabilities[0].State != "available" {
			t.Fatalf("version %s consumer=%+v", version, consumer)
		}
	}
}

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
