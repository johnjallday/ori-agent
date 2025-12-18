package marketplace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

func TestNewStore(t *testing.T) {
	store := NewStore("test.json")
	if store == nil {
		t.Fatal("NewStore returned nil")
	}
	if store.filePath != "test.json" {
		t.Errorf("expected filePath to be test.json, got %s", store.filePath)
	}
}

func TestStoreLoadCreatesDefault(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "marketplace_config.json")

	store := NewStore(filePath)
	err := store.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Should have created file with official marketplace
	marketplaces := store.List()
	if len(marketplaces) != 1 {
		t.Fatalf("expected 1 marketplace, got %d", len(marketplaces))
	}

	if marketplaces[0].ID != types.OfficialMarketplaceID {
		t.Errorf("expected official marketplace, got ID %s", marketplaces[0].ID)
	}

	if !marketplaces[0].IsOfficial {
		t.Error("expected IsOfficial to be true")
	}

	// Verify file was created
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Error("config file was not created")
	}
}

func TestStoreAdd(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "marketplace_config.json")

	store := NewStore(filePath)
	_ = store.Load()

	// Add a new marketplace
	mp := types.Marketplace{
		Name:   "Test Marketplace",
		Source: "testuser/testrepo",
	}

	err := store.Add(mp)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	marketplaces := store.List()
	if len(marketplaces) != 2 {
		t.Fatalf("expected 2 marketplaces, got %d", len(marketplaces))
	}

	// Find the new marketplace
	var found *types.Marketplace
	for _, m := range marketplaces {
		if m.Name == "Test Marketplace" {
			found = &m
			break
		}
	}

	if found == nil {
		t.Fatal("added marketplace not found")
	}

	if found.SourceType != "github" {
		t.Errorf("expected SourceType github, got %s", found.SourceType)
	}

	if found.IsOfficial {
		t.Error("new marketplace should not be marked as official")
	}
}

func TestStoreAddValidation(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "marketplace_config.json")

	store := NewStore(filePath)
	_ = store.Load()

	// Try to add marketplace without name
	mp := types.Marketplace{
		Source: "testuser/testrepo",
	}

	err := store.Add(mp)
	if err == nil {
		t.Error("expected error for missing name")
	}

	// Try to add marketplace without source
	mp = types.Marketplace{
		Name: "Test",
	}

	err = store.Add(mp)
	if err == nil {
		t.Error("expected error for missing source")
	}
}

func TestStoreUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "marketplace_config.json")

	store := NewStore(filePath)
	_ = store.Load()

	// Add a marketplace
	mp := types.Marketplace{
		Name:   "Test Marketplace",
		Source: "testuser/testrepo",
	}
	_ = store.Add(mp)

	// Get the marketplace to find its ID
	marketplaces := store.List()
	var testMP *types.Marketplace
	for _, m := range marketplaces {
		if m.Name == "Test Marketplace" {
			testMP = &m
			break
		}
	}

	// Update it
	testMP.Name = "Updated Marketplace"
	testMP.Enabled = false

	err := store.Update(*testMP)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify update
	updated, err := store.Get(testMP.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if updated.Name != "Updated Marketplace" {
		t.Errorf("expected name 'Updated Marketplace', got %s", updated.Name)
	}

	if updated.Enabled {
		t.Error("expected Enabled to be false")
	}
}

func TestStoreRemove(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "marketplace_config.json")

	store := NewStore(filePath)
	_ = store.Load()

	// Add a marketplace
	mp := types.Marketplace{
		Name:   "Test Marketplace",
		Source: "testuser/testrepo",
	}
	_ = store.Add(mp)

	// Get the marketplace to find its ID
	marketplaces := store.List()
	var testID string
	for _, m := range marketplaces {
		if m.Name == "Test Marketplace" {
			testID = m.ID
			break
		}
	}

	// Remove it
	err := store.Remove(testID)
	if err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	// Verify removal
	marketplaces = store.List()
	if len(marketplaces) != 1 {
		t.Errorf("expected 1 marketplace after removal, got %d", len(marketplaces))
	}
}

func TestStoreCannotRemoveOfficial(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "marketplace_config.json")

	store := NewStore(filePath)
	_ = store.Load()

	err := store.Remove(types.OfficialMarketplaceID)
	if err == nil {
		t.Error("expected error when removing official marketplace")
	}
}

func TestStoreReorder(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "marketplace_config.json")

	store := NewStore(filePath)
	_ = store.Load()

	// Add two more marketplaces
	_ = store.Add(types.Marketplace{Name: "Second", Source: "user/second"})
	_ = store.Add(types.Marketplace{Name: "Third", Source: "user/third"})

	marketplaces := store.List()
	if len(marketplaces) != 3 {
		t.Fatalf("expected 3 marketplaces, got %d", len(marketplaces))
	}

	// Get IDs in current order
	ids := make([]string, len(marketplaces))
	for i, m := range marketplaces {
		ids[i] = m.ID
	}

	// Reverse the order
	reversedIDs := []string{ids[2], ids[1], ids[0]}

	err := store.Reorder(reversedIDs)
	if err != nil {
		t.Fatalf("Reorder failed: %v", err)
	}

	// Verify new order
	reordered := store.List()
	if reordered[0].ID != ids[2] {
		t.Errorf("expected first marketplace ID %s, got %s", ids[2], reordered[0].ID)
	}
	if reordered[2].ID != ids[0] {
		t.Errorf("expected last marketplace ID %s, got %s", ids[0], reordered[2].ID)
	}
}

func TestStoreGetEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "marketplace_config.json")

	store := NewStore(filePath)
	_ = store.Load()

	// Add a disabled marketplace
	mp := types.Marketplace{
		Name:    "Disabled",
		Source:  "user/disabled",
		Enabled: false,
	}
	_ = store.Add(mp)

	// Add an enabled marketplace
	mp2 := types.Marketplace{
		Name:    "Enabled",
		Source:  "user/enabled",
		Enabled: true,
	}
	_ = store.Add(mp2)

	enabled := store.GetEnabled()

	// Should have official + one enabled
	if len(enabled) != 2 {
		t.Errorf("expected 2 enabled marketplaces, got %d", len(enabled))
	}

	// Verify disabled is not included
	for _, m := range enabled {
		if m.Name == "Disabled" {
			t.Error("disabled marketplace should not be in GetEnabled results")
		}
	}
}

func TestMarketplaceResolveURL(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		sourceType string
		expected   string
	}{
		{
			name:       "GitHub repo",
			source:     "testuser/testrepo",
			sourceType: "github",
			expected:   "https://raw.githubusercontent.com/testuser/testrepo/main/plugin_registry.json",
		},
		{
			name:       "Direct URL",
			source:     "https://example.com/plugins.json",
			sourceType: "url",
			expected:   "https://example.com/plugins.json",
		},
		{
			name:       "Auto-detect GitHub",
			source:     "myorg/myrepo",
			sourceType: "",
			expected:   "https://raw.githubusercontent.com/myorg/myrepo/main/plugin_registry.json",
		},
		{
			name:       "Auto-detect URL",
			source:     "https://registry.example.com/plugins.json",
			sourceType: "",
			expected:   "https://registry.example.com/plugins.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mp := types.Marketplace{
				Source:     tt.source,
				SourceType: tt.sourceType,
			}
			result := mp.ResolveURL()
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}
