package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

// readCandidateContribution parses a coordinated candidate checkout's manifest
// and requires every host feature it lists, including resource workspaces.
func readCandidateContribution(t *testing.T, root string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, OriManifestDir, OriManifestFile)) // #nosec G304 -- explicit coordinated local-plugin test root
	if err != nil {
		t.Fatal(err)
	}
	contribution, err := ParseSurfaceContribution(raw)
	if err != nil {
		t.Fatalf("parse the candidate's contribution: %v", err)
	}
	if !HostSupportsFeatures(contribution.RequiresHostFeatures) || !slices.Contains(contribution.RequiresHostFeatures, HostFeatureResourceWorkspacesV1) {
		t.Fatalf("host features = %v", contribution.RequiresHostFeatures)
	}
	return raw
}

// TestLocalResourceCatalogProviderFitsTheContract is opt-in: it checks a
// project plugin candidate's catalog, its two operations and its resource team
// against the contract vectors this host is tested with.
func TestLocalResourceCatalogProviderFitsTheContract(t *testing.T) {
	root := os.Getenv("ORI_REAPER_PLUGIN_PATH")
	if root == "" {
		t.Skip("ORI_REAPER_PLUGIN_PATH is not set")
	}
	raw := readCandidateContribution(t, root)
	var manifest struct {
		Blueprints []struct {
			ID       string `json:"id"`
			Manifest string `json:"manifest"`
		} `json:"blueprints"`
		Services []struct {
			ID         string            `json:"id"`
			Operations []json.RawMessage `json:"operations"`
		} `json:"services"`
		ResourceCatalog json.RawMessage `json:"resource_catalog"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}

	var catalog map[string]any
	if err := json.Unmarshal(resourceContractFile(t, "declaration.valid.json"), &catalog); err != nil {
		t.Fatal(err)
	}
	var declared struct {
		ServiceID string `json:"service_id"`
	}
	if err := json.Unmarshal(manifest.ResourceCatalog, &declared); err != nil || declared.ServiceID == "" {
		t.Fatalf("resource_catalog = %s (%v)", manifest.ResourceCatalog, err)
	}
	catalog["service_id"] = declared.ServiceID
	want, _ := json.Marshal(catalog)
	if !sameJSON(t, manifest.ResourceCatalog, want) {
		t.Errorf("the candidate's catalog differs from declaration.valid.json:\n%s", manifest.ResourceCatalog)
	}

	var contractOperations []json.RawMessage
	if err := json.Unmarshal(resourceContractFile(t, "operations.json"), &contractOperations); err != nil {
		t.Fatal(err)
	}
	candidateOperations := map[string]json.RawMessage{}
	for _, service := range manifest.Services {
		if service.ID != declared.ServiceID {
			continue
		}
		for _, operation := range service.Operations {
			var id struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(operation, &id); err != nil {
				t.Fatal(err)
			}
			candidateOperations[id.ID] = operation
		}
	}
	for _, operation := range contractOperations {
		var id struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(operation, &id); err != nil {
			t.Fatal(err)
		}
		got, ok := candidateOperations[id.ID]
		if !ok {
			t.Errorf("the candidate's %s service does not declare %s", declared.ServiceID, id.ID)
			continue
		}
		if !sameJSON(t, got, operation) {
			t.Errorf("the candidate's %s differs from operations.json:\n%s", id.ID, got)
		}
	}

	// The resource team is the contract's, apart from the provider's own
	// display name, description and role prompt.
	manifestPath := ""
	for _, blueprint := range manifest.Blueprints {
		if blueprint.ID == "reaper-studio" {
			manifestPath = blueprint.Manifest
		}
	}
	if manifestPath == "" {
		t.Fatal("the candidate declares no reaper-studio blueprint")
	}
	template, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(manifestPath))) // #nosec G304 -- explicit coordinated local-plugin test root
	if err != nil {
		t.Fatal(err)
	}
	withoutProse := func(document []byte) []byte {
		var value map[string]any
		if err := json.Unmarshal(document, &value); err != nil {
			t.Fatal(err)
		}
		delete(value, "name")
		delete(value, "description")
		project, _ := value["assistant_project"].(map[string]any)
		roles, _ := project["roles"].([]any)
		for _, role := range roles {
			if role, ok := role.(map[string]any); ok {
				delete(role, "system_prompt")
			}
		}
		out, _ := json.Marshal(value)
		return out
	}
	if !sameJSON(t, withoutProse(template), withoutProse(resourceContractFile(t, "studio-template.valid.json"))) {
		t.Errorf("the candidate's Studio template differs from studio-template.valid.json:\n%s", template)
	}
}

// TestLocalResourceAttachmentHomeFitsTheContract is opt-in: a Home candidate
// allows exactly the contract's resource attachment, after its song attachment.
func TestLocalResourceAttachmentHomeFitsTheContract(t *testing.T) {
	root := os.Getenv("ORI_MUSIC_PLUGIN_PATH")
	if root == "" {
		t.Skip("ORI_MUSIC_PLUGIN_PATH is not set")
	}
	raw := readCandidateContribution(t, root)
	var manifest struct {
		Homes []struct {
			Attachments []json.RawMessage `json:"allowed_project_attachments"`
		} `json:"assistant_program_homes"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Homes) != 1 {
		t.Fatalf("homes = %d, want 1", len(manifest.Homes))
	}
	attachments := manifest.Homes[0].Attachments
	resources := 0
	for i, attachment := range attachments {
		var kind struct {
			WorkspaceKind string `json:"workspace_kind"`
		}
		if err := json.Unmarshal(attachment, &kind); err != nil {
			t.Fatal(err)
		}
		if kind.WorkspaceKind != "resource" {
			continue
		}
		resources++
		if i == 0 {
			t.Error("the resource attachment comes before the Home's project attachment")
		}
		if !sameJSON(t, attachment, resourceContractFile(t, "home-attachment.valid.json")) {
			t.Errorf("the candidate's resource attachment differs from home-attachment.valid.json:\n%s", attachment)
		}
	}
	if resources != 1 {
		t.Fatalf("resource attachments = %d, want 1", resources)
	}
}
