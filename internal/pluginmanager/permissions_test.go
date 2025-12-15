package pluginmanager

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/pluginapi"
)

func TestNewPermissionManager(t *testing.T) {
	pm := NewPermissionManager("")
	if pm == nil {
		t.Fatal("NewPermissionManager returned nil")
	}
	if pm.permissions == nil {
		t.Error("permissions map not initialized")
	}
}

func TestPermissionManager_RequestPermissions(t *testing.T) {
	pm := NewPermissionManager("")

	tests := []struct {
		name        string
		pluginName  string
		permissions pluginapi.PluginPermissions
		wantErr     bool
	}{
		{
			name:       "request file access",
			pluginName: "plugin1",
			permissions: pluginapi.PluginPermissions{
				FileAccess:  true,
				Description: "Needs file access",
			},
			wantErr: false,
		},
		{
			name:       "request network access",
			pluginName: "plugin2",
			permissions: pluginapi.PluginPermissions{
				NetworkAccess: true,
				Description:   "Needs network access",
			},
			wantErr: false,
		},
		{
			name:       "empty plugin name",
			pluginName: "",
			permissions: pluginapi.PluginPermissions{
				FileAccess: true,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := pm.RequestPermissions(tt.pluginName, tt.permissions)
			if (err != nil) != tt.wantErr {
				t.Errorf("RequestPermissions() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPermissionManager_ApprovePermissions(t *testing.T) {
	pm := NewPermissionManager("")

	// Request permissions first
	perms := pluginapi.PluginPermissions{FileAccess: true}
	_ = pm.RequestPermissions("plugin1", perms)

	// Test approve
	err := pm.ApprovePermissions("plugin1")
	if err != nil {
		t.Errorf("ApprovePermissions() error = %v", err)
	}

	// Verify status
	entry, _ := pm.GetPermissionEntry("plugin1")
	if entry.Status != PermissionStatusApproved {
		t.Errorf("Expected status approved, got %s", entry.Status)
	}
	if entry.ApprovedAt == nil {
		t.Error("ApprovedAt should be set")
	}

	// Test approve non-existent plugin
	err = pm.ApprovePermissions("nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent plugin")
	}
}

func TestPermissionManager_DenyPermissions(t *testing.T) {
	pm := NewPermissionManager("")

	// Request permissions first
	perms := pluginapi.PluginPermissions{NetworkAccess: true}
	_ = pm.RequestPermissions("plugin1", perms)

	// Test deny
	err := pm.DenyPermissions("plugin1", "Security concern")
	if err != nil {
		t.Errorf("DenyPermissions() error = %v", err)
	}

	// Verify status
	entry, _ := pm.GetPermissionEntry("plugin1")
	if entry.Status != PermissionStatusDenied {
		t.Errorf("Expected status denied, got %s", entry.Status)
	}

	// Verify audit log
	if len(entry.AuditLog) == 0 {
		t.Error("Audit log should have entries")
	}
	found := false
	for _, log := range entry.AuditLog {
		if log.Action == "denied" && log.Reason == "Security concern" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Deny action not found in audit log")
	}
}

func TestPermissionManager_RevokePermissions(t *testing.T) {
	pm := NewPermissionManager("")

	// Request and approve permissions first
	perms := pluginapi.PluginPermissions{SystemCommands: true}
	_ = pm.RequestPermissions("plugin1", perms)
	_ = pm.ApprovePermissions("plugin1")

	// Test revoke
	err := pm.RevokePermissions("plugin1", "Policy change")
	if err != nil {
		t.Errorf("RevokePermissions() error = %v", err)
	}

	// Verify status
	entry, _ := pm.GetPermissionEntry("plugin1")
	if entry.Status != PermissionStatusRevoked {
		t.Errorf("Expected status revoked, got %s", entry.Status)
	}
	if entry.RevokedAt == nil {
		t.Error("RevokedAt should be set")
	}

	// Test revoke non-approved plugin
	_ = pm.RequestPermissions("plugin2", perms)
	err = pm.RevokePermissions("plugin2", "Test")
	if err == nil {
		t.Error("Expected error when revoking non-approved permissions")
	}
}

func TestPermissionManager_CheckPermission(t *testing.T) {
	pm := NewPermissionManager("")

	// Request and approve permissions
	perms := pluginapi.PluginPermissions{
		FileAccess:    true,
		NetworkAccess: false,
	}
	_ = pm.RequestPermissions("plugin1", perms)
	_ = pm.ApprovePermissions("plugin1")

	tests := []struct {
		name       string
		pluginName string
		permType   pluginapi.PermissionType
		want       bool
	}{
		{
			name:       "check approved file access",
			pluginName: "plugin1",
			permType:   pluginapi.PermissionFileAccess,
			want:       true,
		},
		{
			name:       "check non-requested network access",
			pluginName: "plugin1",
			permType:   pluginapi.PermissionNetworkAccess,
			want:       false,
		},
		{
			name:       "check non-existent plugin",
			pluginName: "nonexistent",
			permType:   pluginapi.PermissionFileAccess,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pm.CheckPermission(tt.pluginName, tt.permType)
			if got != tt.want {
				t.Errorf("CheckPermission() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPermissionManager_IsApproved(t *testing.T) {
	pm := NewPermissionManager("")

	perms := pluginapi.PluginPermissions{FileAccess: true}

	// Request but don't approve
	_ = pm.RequestPermissions("plugin1", perms)
	if pm.IsApproved("plugin1") {
		t.Error("Plugin1 should not be approved yet")
	}

	// Approve
	_ = pm.ApprovePermissions("plugin1")
	if !pm.IsApproved("plugin1") {
		t.Error("Plugin1 should be approved")
	}

	// Check non-existent plugin
	if pm.IsApproved("nonexistent") {
		t.Error("Non-existent plugin should not be approved")
	}
}

func TestPermissionManager_GetPendingApprovals(t *testing.T) {
	pm := NewPermissionManager("")

	// Add some plugins with different statuses
	_ = pm.RequestPermissions("plugin1", pluginapi.PluginPermissions{FileAccess: true})
	_ = pm.RequestPermissions("plugin2", pluginapi.PluginPermissions{NetworkAccess: true})
	_ = pm.RequestPermissions("plugin3", pluginapi.PluginPermissions{SystemCommands: true})
	_ = pm.ApprovePermissions("plugin2")

	pending := pm.GetPendingApprovals()

	if len(pending) != 2 {
		t.Errorf("Expected 2 pending approvals, got %d", len(pending))
	}

	// Verify correct plugins are pending
	names := make(map[string]bool)
	for _, entry := range pending {
		names[entry.PluginName] = true
	}
	if !names["plugin1"] || !names["plugin3"] {
		t.Error("Expected plugin1 and plugin3 to be pending")
	}
	if names["plugin2"] {
		t.Error("plugin2 should not be pending (it's approved)")
	}
}

func TestPermissionManager_RemovePermissionEntry(t *testing.T) {
	pm := NewPermissionManager("")

	_ = pm.RequestPermissions("plugin1", pluginapi.PluginPermissions{FileAccess: true})
	_ = pm.RequestPermissions("plugin2", pluginapi.PluginPermissions{NetworkAccess: true})

	// Remove plugin1
	pm.RemovePermissionEntry("plugin1")

	// Verify plugin1 is removed
	_, err := pm.GetPermissionEntry("plugin1")
	if err == nil {
		t.Error("Expected error for removed plugin")
	}

	// Verify plugin2 still exists
	_, err = pm.GetPermissionEntry("plugin2")
	if err != nil {
		t.Error("plugin2 should still exist")
	}
}

func TestPermissionManager_LoadPermissions(t *testing.T) {
	pm := NewPermissionManager("")

	// Pre-load some data
	if err := pm.RequestPermissions("old-plugin", pluginapi.PluginPermissions{FileAccess: true}); err != nil {
		t.Fatalf("Failed to request permissions: %v", err)
	}

	// Create new entries to load
	now := time.Now()
	newEntries := map[string]*PermissionEntry{
		"plugin1": {
			PluginName:  "plugin1",
			Permissions: pluginapi.PluginPermissions{FileAccess: true},
			Status:      PermissionStatusApproved,
			RequestedAt: now,
		},
		"plugin2": {
			PluginName:  "plugin2",
			Permissions: pluginapi.PluginPermissions{NetworkAccess: true},
			Status:      PermissionStatusPending,
			RequestedAt: now,
		},
	}

	pm.LoadPermissions(newEntries)

	// Verify old data is cleared
	_, err := pm.GetPermissionEntry("old-plugin")
	if err == nil {
		t.Error("Expected old-plugin to be cleared")
	}

	// Verify new data is loaded
	entry1, _ := pm.GetPermissionEntry("plugin1")
	if entry1.Status != PermissionStatusApproved {
		t.Errorf("Expected plugin1 status approved, got %s", entry1.Status)
	}

	entry2, _ := pm.GetPermissionEntry("plugin2")
	if entry2.Status != PermissionStatusPending {
		t.Errorf("Expected plugin2 status pending, got %s", entry2.Status)
	}
}

func TestPermissionManager_AuditLog(t *testing.T) {
	pm := NewPermissionManager("")

	perms := pluginapi.PluginPermissions{FileAccess: true}
	_ = pm.RequestPermissions("plugin1", perms)

	entry, _ := pm.GetPermissionEntry("plugin1")

	// Should have "requested" entry
	if len(entry.AuditLog) < 1 {
		t.Fatal("Expected at least 1 audit log entry")
	}
	if entry.AuditLog[0].Action != "requested" {
		t.Errorf("Expected first action to be 'requested', got %s", entry.AuditLog[0].Action)
	}

	// Approve and check audit log
	_ = pm.ApprovePermissions("plugin1")
	entry, _ = pm.GetPermissionEntry("plugin1")

	if len(entry.AuditLog) < 2 {
		t.Fatal("Expected at least 2 audit log entries")
	}

	found := false
	for _, log := range entry.AuditLog {
		if log.Action == "approved" {
			found = true
			break
		}
	}
	if !found {
		t.Error("'approved' action not found in audit log")
	}
}

func TestPermissionManager_UpdatePermissions(t *testing.T) {
	pm := NewPermissionManager("")

	// Request initial permissions
	perms1 := pluginapi.PluginPermissions{FileAccess: true}
	_ = pm.RequestPermissions("plugin1", perms1)
	_ = pm.ApprovePermissions("plugin1")

	// Request updated permissions (different)
	perms2 := pluginapi.PluginPermissions{FileAccess: true, NetworkAccess: true}
	_ = pm.RequestPermissions("plugin1", perms2)

	entry, _ := pm.GetPermissionEntry("plugin1")

	// Status should be back to pending
	if entry.Status != PermissionStatusPending {
		t.Errorf("Expected status pending after permission change, got %s", entry.Status)
	}

	// Permissions should be updated
	if !entry.Permissions.NetworkAccess {
		t.Error("NetworkAccess should be true in updated permissions")
	}

	// Should have "updated" in audit log
	found := false
	for _, log := range entry.AuditLog {
		if log.Action == "updated" {
			found = true
			break
		}
	}
	if !found {
		t.Error("'updated' action not found in audit log")
	}
}

func TestPermissionManager_FileIO(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "permissions-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	auditPath := filepath.Join(tmpDir, "permissions_audit.json")
	pm := NewPermissionManager(auditPath)

	// Request and approve some permissions
	_ = pm.RequestPermissions("plugin1", pluginapi.PluginPermissions{FileAccess: true})
	_ = pm.ApprovePermissions("plugin1")

	// Create new manager and load from file
	pm2 := NewPermissionManager(auditPath)
	if err := pm2.LoadFromFile(); err != nil {
		t.Errorf("LoadFromFile() error = %v", err)
	}

	// Verify data was loaded
	entry, err := pm2.GetPermissionEntry("plugin1")
	if err != nil {
		t.Errorf("Expected plugin1 to be loaded, got error: %v", err)
	}
	if entry.Status != PermissionStatusApproved {
		t.Errorf("Expected status approved, got %s", entry.Status)
	}
}

func TestPermissionsEqual(t *testing.T) {
	tests := []struct {
		name string
		a    pluginapi.PluginPermissions
		b    pluginapi.PluginPermissions
		want bool
	}{
		{
			name: "identical permissions",
			a:    pluginapi.PluginPermissions{FileAccess: true, NetworkAccess: false},
			b:    pluginapi.PluginPermissions{FileAccess: true, NetworkAccess: false},
			want: true,
		},
		{
			name: "different file access",
			a:    pluginapi.PluginPermissions{FileAccess: true},
			b:    pluginapi.PluginPermissions{FileAccess: false},
			want: false,
		},
		{
			name: "different network access",
			a:    pluginapi.PluginPermissions{NetworkAccess: true},
			b:    pluginapi.PluginPermissions{NetworkAccess: false},
			want: false,
		},
		{
			name: "all permissions different",
			a:    pluginapi.PluginPermissions{FileAccess: true, NetworkAccess: true, SystemCommands: true},
			b:    pluginapi.PluginPermissions{FileAccess: false, NetworkAccess: false, SystemCommands: false},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := permissionsEqual(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("permissionsEqual() = %v, want %v", got, tt.want)
			}
		})
	}
}
