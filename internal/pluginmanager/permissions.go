package pluginmanager

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/oriagent/ori-pluginapi"
)

// PermissionStatus represents the approval status of plugin permissions.
type PermissionStatus string

const (
	PermissionStatusPending  PermissionStatus = "pending"
	PermissionStatusApproved PermissionStatus = "approved"
	PermissionStatusDenied   PermissionStatus = "denied"
	PermissionStatusRevoked  PermissionStatus = "revoked"
)

// PermissionEntry tracks permission requests and approvals for a plugin.
type PermissionEntry struct {
	PluginName  string                      `json:"plugin_name"`
	Permissions pluginapi.PluginPermissions `json:"permissions"`
	Status      PermissionStatus            `json:"status"`
	RequestedAt time.Time                   `json:"requested_at"`
	ApprovedAt  *time.Time                  `json:"approved_at,omitempty"`
	RevokedAt   *time.Time                  `json:"revoked_at,omitempty"`
	AuditLog    []PermissionAuditEntry      `json:"audit_log,omitempty"`
}

// PermissionAuditEntry represents a single audit log entry for permission changes.
type PermissionAuditEntry struct {
	Action    string    `json:"action"` // "requested", "approved", "denied", "revoked"
	Timestamp time.Time `json:"timestamp"`
	Reason    string    `json:"reason,omitempty"`
}

// PermissionManager manages plugin permission requests, approvals, and revocations.
type PermissionManager struct {
	mu          sync.RWMutex
	permissions map[string]*PermissionEntry // plugin name -> permission entry
	auditPath   string                      // path to audit log file
}

// NewPermissionManager creates a new PermissionManager instance.
func NewPermissionManager(auditPath string) *PermissionManager {
	return &PermissionManager{
		permissions: make(map[string]*PermissionEntry),
		auditPath:   auditPath,
	}
}

// RequestPermissions records a permission request from a plugin.
// This is called when a plugin declares its required permissions.
func (pm *PermissionManager) RequestPermissions(pluginName string, perms pluginapi.PluginPermissions) error {
	if pluginName == "" {
		return fmt.Errorf("plugin name cannot be empty")
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	now := time.Now()

	// Check if permissions already exist
	if entry, exists := pm.permissions[pluginName]; exists {
		// Update existing entry if permissions changed
		if !permissionsEqual(entry.Permissions, perms) {
			entry.Permissions = perms
			entry.Status = PermissionStatusPending
			entry.RequestedAt = now
			entry.AuditLog = append(entry.AuditLog, PermissionAuditEntry{
				Action:    "updated",
				Timestamp: now,
				Reason:    "Plugin permissions changed",
			})
		}
		return nil
	}

	// Create new permission entry
	pm.permissions[pluginName] = &PermissionEntry{
		PluginName:  pluginName,
		Permissions: perms,
		Status:      PermissionStatusPending,
		RequestedAt: now,
		AuditLog: []PermissionAuditEntry{
			{
				Action:    "requested",
				Timestamp: now,
			},
		},
	}

	return nil
}

// ApprovePermissions approves the permissions for a plugin.
func (pm *PermissionManager) ApprovePermissions(pluginName string) error {
	if pluginName == "" {
		return fmt.Errorf("plugin name cannot be empty")
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	entry, exists := pm.permissions[pluginName]
	if !exists {
		return fmt.Errorf("no permission request found for plugin %s", pluginName)
	}

	now := time.Now()
	entry.Status = PermissionStatusApproved
	entry.ApprovedAt = &now
	entry.AuditLog = append(entry.AuditLog, PermissionAuditEntry{
		Action:    "approved",
		Timestamp: now,
	})

	return pm.saveAuditLog()
}

// DenyPermissions denies the permissions for a plugin.
func (pm *PermissionManager) DenyPermissions(pluginName, reason string) error {
	if pluginName == "" {
		return fmt.Errorf("plugin name cannot be empty")
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	entry, exists := pm.permissions[pluginName]
	if !exists {
		return fmt.Errorf("no permission request found for plugin %s", pluginName)
	}

	now := time.Now()
	entry.Status = PermissionStatusDenied
	entry.AuditLog = append(entry.AuditLog, PermissionAuditEntry{
		Action:    "denied",
		Timestamp: now,
		Reason:    reason,
	})

	return pm.saveAuditLog()
}

// RevokePermissions revokes previously approved permissions for a plugin.
func (pm *PermissionManager) RevokePermissions(pluginName, reason string) error {
	if pluginName == "" {
		return fmt.Errorf("plugin name cannot be empty")
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	entry, exists := pm.permissions[pluginName]
	if !exists {
		return fmt.Errorf("no permission entry found for plugin %s", pluginName)
	}

	if entry.Status != PermissionStatusApproved {
		return fmt.Errorf("cannot revoke permissions that are not approved (current status: %s)", entry.Status)
	}

	now := time.Now()
	entry.Status = PermissionStatusRevoked
	entry.RevokedAt = &now
	entry.AuditLog = append(entry.AuditLog, PermissionAuditEntry{
		Action:    "revoked",
		Timestamp: now,
		Reason:    reason,
	})

	return pm.saveAuditLog()
}

// CheckPermission checks if a plugin has a specific permission approved.
// permType should be one of: "file_access", "network_access", "system_commands"
func (pm *PermissionManager) CheckPermission(pluginName string, permType pluginapi.PermissionType) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	entry, exists := pm.permissions[pluginName]
	if !exists || entry.Status != PermissionStatusApproved {
		return false
	}

	switch permType {
	case pluginapi.PermissionFileAccess:
		return entry.Permissions.FileAccess
	case pluginapi.PermissionNetworkAccess:
		return entry.Permissions.NetworkAccess
	case pluginapi.PermissionSystemCommands:
		return entry.Permissions.SystemCommands
	default:
		return false
	}
}

// GetPermissionEntry returns the permission entry for a plugin.
func (pm *PermissionManager) GetPermissionEntry(pluginName string) (*PermissionEntry, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	entry, exists := pm.permissions[pluginName]
	if !exists {
		return nil, fmt.Errorf("no permission entry found for plugin %s", pluginName)
	}

	// Return a copy to prevent external modification
	entryCopy := *entry
	return &entryCopy, nil
}

// IsApproved returns true if the plugin's permissions are approved.
func (pm *PermissionManager) IsApproved(pluginName string) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	entry, exists := pm.permissions[pluginName]
	return exists && entry.Status == PermissionStatusApproved
}

// GetPendingApprovals returns a list of plugins with pending permission requests.
func (pm *PermissionManager) GetPendingApprovals() []*PermissionEntry {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	var pending []*PermissionEntry
	for _, entry := range pm.permissions {
		if entry.Status == PermissionStatusPending {
			entryCopy := *entry
			pending = append(pending, &entryCopy)
		}
	}
	return pending
}

// GetAllPermissions returns all permission entries.
func (pm *PermissionManager) GetAllPermissions() map[string]*PermissionEntry {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	// Return a copy to prevent external modification
	result := make(map[string]*PermissionEntry, len(pm.permissions))
	for name, entry := range pm.permissions {
		entryCopy := *entry
		result[name] = &entryCopy
	}
	return result
}

// RemovePermissionEntry removes a permission entry for a plugin.
// This should be called when a plugin is uninstalled.
func (pm *PermissionManager) RemovePermissionEntry(pluginName string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	delete(pm.permissions, pluginName)
	_ = pm.saveAuditLog() // Ignore error on cleanup
}

// LoadPermissions loads permission entries from a map (used when restoring from storage).
func (pm *PermissionManager) LoadPermissions(entries map[string]*PermissionEntry) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.permissions = make(map[string]*PermissionEntry)
	for name, entry := range entries {
		entryCopy := *entry
		pm.permissions[name] = &entryCopy
	}
}

// saveAuditLog saves the current permission state to the audit log file.
// This is an internal method and assumes the caller holds the lock.
func (pm *PermissionManager) saveAuditLog() error {
	if pm.auditPath == "" {
		return nil // No audit log configured
	}

	data, err := json.MarshalIndent(pm.permissions, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal permissions: %w", err)
	}

	if err := os.WriteFile(pm.auditPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write audit log: %w", err)
	}

	return nil
}

// LoadFromFile loads permissions from the audit log file.
func (pm *PermissionManager) LoadFromFile() error {
	if pm.auditPath == "" {
		return nil // No audit log configured
	}

	data, err := os.ReadFile(pm.auditPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // File doesn't exist yet, that's okay
		}
		return fmt.Errorf("failed to read audit log: %w", err)
	}

	var entries map[string]*PermissionEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("failed to unmarshal permissions: %w", err)
	}

	pm.LoadPermissions(entries)
	return nil
}

// permissionsEqual checks if two PluginPermissions are equal.
func permissionsEqual(a, b pluginapi.PluginPermissions) bool {
	return a.FileAccess == b.FileAccess &&
		a.NetworkAccess == b.NetworkAccess &&
		a.SystemCommands == b.SystemCommands
}
