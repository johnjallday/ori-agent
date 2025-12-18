package pluginhttp

import (
	"encoding/json"
	"fmt"
	"net/http"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/pluginmanager"
)

// BackupHandler handles plugin configuration backup and export operations
type BackupHandler struct {
	BackupManager *pluginmanager.BackupManager
}

// NewBackupHandler creates a new backup handler
func NewBackupHandler(backupMgr *pluginmanager.BackupManager) *BackupHandler {
	return &BackupHandler{
		BackupManager: backupMgr,
	}
}

// HandleExportPluginConfig exports configuration for a single plugin or all plugins
// GET /api/plugins/export?plugin=name (single plugin)
// GET /api/plugins/export (all plugins)
func (h *BackupHandler) HandleExportPluginConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.RespondMethodNotAllowed(w)
		return
	}

	pluginName := r.URL.Query().Get("plugin")

	if pluginName != "" {
		// Export single plugin
		data, err := h.BackupManager.ExportPluginConfig(pluginName)
		if err != nil {
			orihttp.RespondInternalError(w, fmt.Sprintf("Failed to export plugin config: %v", err))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s-config.json", pluginName))
		_, _ = w.Write(data)
	} else {
		// Export all plugins
		data, err := h.BackupManager.ExportAllPluginConfigs()
		if err != nil {
			orihttp.RespondInternalError(w, fmt.Sprintf("Failed to export all configs: %v", err))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment; filename=all-plugins-config.json")
		_, _ = w.Write(data)
	}
}

// HandleImportPluginConfig imports plugin configuration from JSON data
// POST /api/plugins/import
// Request body: JSON configuration data (single or multiple plugins)
func (h *BackupHandler) HandleImportPluginConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.RespondMethodNotAllowed(w)
		return
	}

	// Read request body
	var configData json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&configData); err != nil {
		orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	// Validate config before import
	if err := h.BackupManager.ValidateImportedConfig(configData); err != nil {
		orihttp.RespondBadRequest(w, fmt.Sprintf("Invalid configuration: %v", err))
		return
	}

	// Try importing as single config first, then as multiple
	var importedCount int
	if err := h.BackupManager.ImportPluginConfig(configData); err == nil {
		importedCount = 1
	} else if err := h.BackupManager.ImportMultipleConfigs(configData); err != nil {
		orihttp.RespondInternalError(w, fmt.Sprintf("Failed to import config: %v", err))
		return
	} else {
		// Count how many configs were imported
		var configs []pluginmanager.PluginConfigExport
		if err := json.Unmarshal(configData, &configs); err == nil {
			importedCount = len(configs)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":        true,
		"message":        fmt.Sprintf("Successfully imported %d plugin configuration(s)", importedCount),
		"imported_count": importedCount,
	})
}
