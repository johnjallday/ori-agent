package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func resourceStudioFixture(name string) string {
	return filepath.Join("..", "..", "tests", "fixtures", "resource-studio", name)
}

// sameJSON compares two documents by value, so field order and spacing do
// not matter.
func sameJSON(t *testing.T, left, right []byte) bool {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(left, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(right, &b); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(a, b)
}

// The fixture plugins stand in for the Music and REAPER candidates in local
// demos and browser specs. They must stay valid for this host and must carry
// exactly the contract's operations, catalog and pinned service bytes.
func TestResourceStudioFixturePlugins_StayOnTheContract(t *testing.T) {
	for _, name := range []string{"music-home", "reaper-studio"} {
		raw, err := os.ReadFile(filepath.Join(resourceStudioFixture(name), ".ori-plugin", "plugin.json"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseSurfaceContribution(raw); err != nil {
			t.Fatalf("%s manifest: %v", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(resourceStudioFixture("reaper-studio"), ".ori-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Services []struct {
			Operations json.RawMessage `json:"operations"`
			Artifacts  []struct {
				ID     string `json:"id"`
				SHA256 string `json:"sha256"`
				Size   int    `json:"size"`
			} `json:"artifacts"`
		} `json:"services"`
		ResourceCatalog json.RawMessage `json:"resource_catalog"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if !sameJSON(t, manifest.Services[0].Operations, resourceContractFile(t, "operations.json")) {
		t.Fatal("the fixture's operations differ from the contract's operations.json")
	}
	var catalog map[string]any
	if err := json.Unmarshal(resourceContractFile(t, "declaration.valid.json"), &catalog); err != nil {
		t.Fatal(err)
	}
	catalog["service_id"] = "reaper-service"
	want, _ := json.Marshal(catalog)
	if !sameJSON(t, manifest.ResourceCatalog, want) {
		t.Fatal("the fixture's catalog differs from declaration.valid.json")
	}
	service, err := os.ReadFile(filepath.Join(resourceStudioFixture("reaper-studio"), "artifacts", "studio-service.py"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(service)
	for _, artifact := range manifest.Services[0].Artifacts {
		if artifact.SHA256 != hex.EncodeToString(sum[:]) || artifact.Size != len(service) {
			t.Fatalf("artifact %s pin = %s/%d, file = %x/%d", artifact.ID, artifact.SHA256, artifact.Size, sum, len(service))
		}
	}
}
