package pluginhttp

import (
	"fmt"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/pluginmanager"
	"github.com/johnjallday/ori-agent/internal/registry"
)

// PermissionsHandler handles plugin permission management operations

type PermissionsHandler struct {
	PermissionManager *pluginmanager.PermissionManager
	RegistryManager   *registry.Manager
}

// NewPermissionsHandler creates a new permissions handler
func NewPermissionsHandler(
	permMgr *pluginmanager.PermissionManager,
	regMgr *registry.Manager,
) *PermissionsHandler {
	return &PermissionsHandler{
		PermissionManager: permMgr,
		RegistryManager:   regMgr,
	}
}

// HandleGetPermissions returns permission details for a plugin
// GET /api/plugins/:name/permissions
func (h *PermissionsHandler) HandleGetPermissions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		orihttp.BadRequest(w, "Plugin name required")
		return
	}

	// Get permissions from permission manager
	permissionEntry, err := h.PermissionManager.GetPermissionEntry(pluginName)
	if err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to get permissions: %v", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	orihttp.WriteJSON(w, permissionEntry)
}

// HandleApprovePermissions approves requested permissions for a plugin
// POST /api/plugins/:name/permissions/approve
func (h *PermissionsHandler) HandleApprovePermissions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		orihttp.BadRequest(w, "Plugin name required")
		return
	}

	// Approve permissions
	if err := h.PermissionManager.ApprovePermissions(pluginName); err != nil {
		orihttp.InternalError(w, fmt.Sprintf("Failed to approve permissions: %v", err))
		return
	}

	// Update registry to mark permissions as approved

	permissionEntry, err := h.PermissionManager.GetPermissionEntry(pluginName)
	if err == nil && permissionEntry != nil {
		permMap := map[string]interface{}{
			"file_access":     permissionEntry.Permissions.FileAccess,
			"network_access":  permissionEntry.Permissions.NetworkAccess,
			"system_commands": permissionEntry.Permissions.SystemCommands,
		}
		_ = h.RegistryManager.UpdatePluginPermissions(pluginName, permMap, true)
	}

	w.Header().Set("Content-Type", "application/json")
	orihttp.WriteJSON(w, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Permissions approved for plugin %s", pluginName),
	})
}

func (h *PermissionsHandler) extractPluginName(path string) string {
	// Remove /api/plugins/ prefix
	pluginPath := strings.TrimPrefix(path, "/api/plugins/")

	// Split by / and take first component (plugin name)
	parts := strings.Split(pluginPath, "/")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}
