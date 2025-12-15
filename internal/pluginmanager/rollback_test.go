package pluginmanager

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewVersionManager(t *testing.T) {
	vm := NewVersionManager("/tmp/versions")
	if vm == nil {
		t.Fatal("NewVersionManager returned nil")
	}
	if vm.versionHistory == nil {
		t.Error("versionHistory map not initialized")
	}
	if vm.versionsDir != "/tmp/versions" {
		t.Errorf("Expected versionsDir /tmp/versions, got %s", vm.versionsDir)
	}
}

func TestVersionManager_StoreVersion(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "rollback-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	vm := NewVersionManager(filepath.Join(tmpDir, "versions"))

	// Create a dummy plugin binary
	pluginPath := filepath.Join(tmpDir, "test-plugin")
	if err := os.WriteFile(pluginPath, []byte("dummy binary v1.0"), 0755); err != nil {
		t.Fatal(err)
	}

	// Test store version
	err = vm.StoreVersion("test-plugin", "1.0.0", pluginPath)
	if err != nil {
		t.Errorf("StoreVersion() error = %v", err)
	}

	// Verify version was stored
	history := vm.GetVersionHistory("test-plugin")
	if len(history) != 1 {
		t.Errorf("Expected 1 version in history, got %d", len(history))
	}
	if history[0].Version != "1.0.0" {
		t.Errorf("Expected version 1.0.0, got %s", history[0].Version)
	}

	// Verify backup file exists
	if _, err := os.Stat(history[0].Path); os.IsNotExist(err) {
		t.Error("Backup file was not created")
	}
}

func TestVersionManager_StoreVersion_Errors(t *testing.T) {
	vm := NewVersionManager("/tmp/versions")

	tests := []struct {
		name       string
		pluginName string
		version    string
		binaryPath string
		wantErr    bool
	}{
		{
			name:       "empty plugin name",
			pluginName: "",
			version:    "1.0.0",
			binaryPath: "/some/path",
			wantErr:    true,
		},
		{
			name:       "empty version",
			pluginName: "plugin1",
			version:    "",
			binaryPath: "/some/path",
			wantErr:    true,
		},
		{
			name:       "empty binary path",
			pluginName: "plugin1",
			version:    "1.0.0",
			binaryPath: "",
			wantErr:    true,
		},
		{
			name:       "non-existent binary",
			pluginName: "plugin1",
			version:    "1.0.0",
			binaryPath: "/nonexistent/path",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := vm.StoreVersion(tt.pluginName, tt.version, tt.binaryPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("StoreVersion() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestVersionManager_GetVersionHistory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "rollback-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	vm := NewVersionManager(filepath.Join(tmpDir, "versions"))

	// Create dummy binaries
	plugin1 := filepath.Join(tmpDir, "plugin1-v1")
	plugin2 := filepath.Join(tmpDir, "plugin1-v2")
	_ = os.WriteFile(plugin1, []byte("v1"), 0755)
	_ = os.WriteFile(plugin2, []byte("v2"), 0755)

	// Store multiple versions
	_ = vm.StoreVersion("plugin1", "1.0.0", plugin1)
	time.Sleep(10 * time.Millisecond) // Small delay to ensure different timestamps
	_ = vm.StoreVersion("plugin1", "1.1.0", plugin2)

	history := vm.GetVersionHistory("plugin1")

	if len(history) != 2 {
		t.Fatalf("Expected 2 versions in history, got %d", len(history))
	}

	// Verify sorted by newest first
	if history[0].Version != "1.1.0" {
		t.Errorf("Expected newest version first (1.1.0), got %s", history[0].Version)
	}
	if history[1].Version != "1.0.0" {
		t.Errorf("Expected oldest version second (1.0.0), got %s", history[1].Version)
	}

	// Test non-existent plugin
	history = vm.GetVersionHistory("nonexistent")
	if len(history) != 0 {
		t.Errorf("Expected empty history for nonexistent plugin, got %d", len(history))
	}
}

func TestVersionManager_RollbackToVersion(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "rollback-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	vm := NewVersionManager(filepath.Join(tmpDir, "versions"))

	// Create initial version
	v1Path := filepath.Join(tmpDir, "plugin-v1")
	if err := os.WriteFile(v1Path, []byte("version 1.0"), 0755); err != nil {
		t.Fatal(err)
	}

	// Store v1.0
	_ = vm.StoreVersion("test-plugin", "1.0.0", v1Path)

	// Update to v2.0
	currentPath := filepath.Join(tmpDir, "plugin-current")
	if err := os.WriteFile(currentPath, []byte("version 2.0"), 0755); err != nil {
		t.Fatal(err)
	}

	// Rollback to v1.0
	err = vm.RollbackToVersion("test-plugin", "1.0.0", currentPath)
	if err != nil {
		t.Errorf("RollbackToVersion() error = %v", err)
	}

	// Verify current version was replaced
	content, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "version 1.0" {
		t.Errorf("Expected rollback to restore 'version 1.0', got %s", string(content))
	}
}

func TestVersionManager_RollbackToVersion_Errors(t *testing.T) {
	vm := NewVersionManager("/tmp/versions")

	tests := []struct {
		name          string
		pluginName    string
		targetVersion string
		currentBinary string
		wantErr       bool
	}{
		{
			name:          "empty plugin name",
			pluginName:    "",
			targetVersion: "1.0.0",
			currentBinary: "/some/path",
			wantErr:       true,
		},
		{
			name:          "empty target version",
			pluginName:    "plugin1",
			targetVersion: "",
			currentBinary: "/some/path",
			wantErr:       true,
		},
		{
			name:          "empty current binary path",
			pluginName:    "plugin1",
			targetVersion: "1.0.0",
			currentBinary: "",
			wantErr:       true,
		},
		{
			name:          "no version history",
			pluginName:    "nonexistent",
			targetVersion: "1.0.0",
			currentBinary: "/some/path",
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := vm.RollbackToVersion(tt.pluginName, tt.targetVersion, tt.currentBinary)
			if (err != nil) != tt.wantErr {
				t.Errorf("RollbackToVersion() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestVersionManager_GetAvailableVersions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "rollback-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	vm := NewVersionManager(filepath.Join(tmpDir, "versions"))

	// Create dummy binaries and store versions
	for i, version := range []string{"1.0.0", "1.1.0", "1.2.0"} {
		pluginPath := filepath.Join(tmpDir, "plugin", version)
		_ = os.MkdirAll(filepath.Dir(pluginPath), 0755)
		_ = os.WriteFile(pluginPath, []byte("v"+version), 0755)
		_ = vm.StoreVersion("plugin1", version, pluginPath)
		time.Sleep(time.Duration(i+1) * 10 * time.Millisecond) // Ensure different timestamps
	}

	versions := vm.GetAvailableVersions("plugin1")

	if len(versions) != 3 {
		t.Errorf("Expected 3 versions, got %d", len(versions))
	}

	// Verify all versions are present (order doesn't matter for this test)
	versionMap := make(map[string]bool)
	for _, v := range versions {
		versionMap[v] = true
	}
	if !versionMap["1.0.0"] || !versionMap["1.1.0"] || !versionMap["1.2.0"] {
		t.Errorf("Missing expected versions, got %v", versions)
	}
}

func TestVersionManager_HasVersionHistory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "rollback-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	vm := NewVersionManager(filepath.Join(tmpDir, "versions"))

	// No history initially
	if vm.HasVersionHistory("plugin1") {
		t.Error("Expected no version history initially")
	}

	// Store a version
	pluginPath := filepath.Join(tmpDir, "plugin1")
	_ = os.WriteFile(pluginPath, []byte("v1"), 0755)
	_ = vm.StoreVersion("plugin1", "1.0.0", pluginPath)

	// Should have history now
	if !vm.HasVersionHistory("plugin1") {
		t.Error("Expected version history after storing version")
	}

	// Other plugin still has no history
	if vm.HasVersionHistory("plugin2") {
		t.Error("plugin2 should not have version history")
	}
}

func TestVersionManager_RemoveVersionHistory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "rollback-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	vm := NewVersionManager(filepath.Join(tmpDir, "versions"))

	// Store a version
	pluginPath := filepath.Join(tmpDir, "plugin1")
	_ = os.WriteFile(pluginPath, []byte("v1"), 0755)
	_ = vm.StoreVersion("plugin1", "1.0.0", pluginPath)

	// Verify history exists
	if !vm.HasVersionHistory("plugin1") {
		t.Error("Expected version history")
	}

	// Remove history
	err = vm.RemoveVersionHistory("plugin1")
	if err != nil {
		t.Errorf("RemoveVersionHistory() error = %v", err)
	}

	// Verify history is removed
	if vm.HasVersionHistory("plugin1") {
		t.Error("Expected version history to be removed")
	}

	// Verify directory is removed
	pluginVersionDir := filepath.Join(tmpDir, "versions", "plugin1")
	if _, err := os.Stat(pluginVersionDir); !os.IsNotExist(err) {
		t.Error("Expected version directory to be removed")
	}
}

func TestVersionManager_CleanupOldVersions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "rollback-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	vm := NewVersionManager(filepath.Join(tmpDir, "versions"))

	// Store more than MaxVersionHistory versions
	for i := 1; i <= 5; i++ {
		pluginPath := filepath.Join(tmpDir, "plugin", "v", string(rune(i)))
		_ = os.MkdirAll(filepath.Dir(pluginPath), 0755)
		_ = os.WriteFile(pluginPath, []byte("version"), 0755)
		_ = vm.StoreVersion("plugin1", "1."+string(rune('0'+i))+".0", pluginPath)
		time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	}

	// Should only keep MaxVersionHistory (3) versions
	history := vm.GetVersionHistory("plugin1")
	if len(history) != MaxVersionHistory {
		t.Errorf("Expected %d versions after cleanup, got %d", MaxVersionHistory, len(history))
	}

	// Verify we kept the newest versions
	versions := make([]string, len(history))
	for i, v := range history {
		versions[i] = v.Version
	}
	// The newest 3 should be 1.5.0, 1.4.0, 1.3.0
	expectedVersions := map[string]bool{
		"1.5.0": true,
		"1.4.0": true,
		"1.3.0": true,
	}
	for _, v := range versions {
		if !expectedVersions[v] {
			t.Errorf("Unexpected version %s in history (should have kept newest 3)", v)
		}
	}
}

func TestVersionManager_LoadVersionHistory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "rollback-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	versionsDir := filepath.Join(tmpDir, "versions")
	vm1 := NewVersionManager(versionsDir)

	// Store some versions with first manager
	pluginPath := filepath.Join(tmpDir, "plugin1")
	_ = os.WriteFile(pluginPath, []byte("v1"), 0755)
	_ = vm1.StoreVersion("plugin1", "1.0.0", pluginPath)

	// Create new manager and load from disk
	vm2 := NewVersionManager(versionsDir)
	err = vm2.LoadVersionHistory()
	if err != nil {
		t.Errorf("LoadVersionHistory() error = %v", err)
	}

	// Verify history was loaded
	if !vm2.HasVersionHistory("plugin1") {
		t.Error("Expected version history to be loaded")
	}

	history := vm2.GetVersionHistory("plugin1")
	if len(history) != 1 {
		t.Errorf("Expected 1 version in loaded history, got %d", len(history))
	}
}

func TestCopyFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "copyfile-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	srcPath := filepath.Join(tmpDir, "source.txt")
	dstPath := filepath.Join(tmpDir, "dest.txt")

	// Create source file
	content := []byte("test content")
	if err := os.WriteFile(srcPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	// Copy file
	if err := copyFile(srcPath, dstPath); err != nil {
		t.Errorf("copyFile() error = %v", err)
	}

	// Verify destination has same content
	dstContent, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(dstContent) != string(content) {
		t.Errorf("Expected copied content '%s', got '%s'", string(content), string(dstContent))
	}
}

func TestParseVersionFromFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{
			name:     "basic filename without extension",
			filename: "plugin-1-2-0-20240101",
			want:     "plugin-1-2-0-20240101",
		},
		{
			name:     "with extension",
			filename: "plugin-1-2-0-20240101.bin",
			want:     "plugin-1-2-0-20240101",
		},
		{
			name:     "path included",
			filename: "/path/to/plugin-1-2-0-20240101",
			want:     "plugin-1-2-0-20240101",
		},
		{
			name:     "filename with dots treated as extension",
			filename: "plugin-1.0.0-20240101",
			want:     "plugin-1.0", // filepath.Ext treats ".0-20240101" as extension
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseVersionFromFilename(tt.filename)
			if got != tt.want {
				t.Errorf("parseVersionFromFilename() = %v, want %v", got, tt.want)
			}
		})
	}
}
