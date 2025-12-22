package pluginhttp

import (
	"fmt"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
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
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		if err := orihttp.RespondBadRequest(w, "Plugin name required"); err != nil {
			logger.

				// Get permissions from permission manager
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	permissionEntry, err := h.PermissionManager.GetPermissionEntry(pluginName)
	if err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to get permissions: %v", err)); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	orihttp.WriteJSON(w, permissionEntry)
}

// HandleApprovePermissions approves requested permissions for a plugin
// POST /api/plugins/:name/permissions/approve
func (h *PermissionsHandler) HandleApprovePermissions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if err := orihttp.RespondMethodNotAllowed(w); err != nil {
			logger.Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	pluginName := h.extractPluginName(r.URL.Path)
	if pluginName == "" {
		if err := orihttp.RespondBadRequest(w, "Plugin name required"); err != nil {
			logger.

				// Approve permissions
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

	if err := h.PermissionManager.ApprovePermissions(pluginName); err != nil {
		if err := orihttp.RespondInternalError(w, fmt.Sprintf("Failed to approve permissions: %v", err)); err != nil {
			logger.

				// Update registry to mark permissions as approved
				Error("Failed to write response", logger.Fields{"error": err})
		}
		return
	}

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
