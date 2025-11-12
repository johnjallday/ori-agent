package pluginhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/plugindownloader"
	"github.com/johnjallday/ori-agent/internal/pluginloader"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

type RegistryHandler struct {
	store            store.Store
	registryManager  *registry.Manager
	pluginDownloader *plugindownloader.PluginDownloader
	agentStorePath   string
}

func NewRegistryHandler(store store.Store, registryManager *registry.Manager, pluginDownloader *plugindownloader.PluginDownloader, agentStorePath string) *RegistryHandler {
	return &RegistryHandler{
		store:            store,
		registryManager:  registryManager,
		pluginDownloader: pluginDownloader,
		agentStorePath:   agentStorePath,
	}
}

// getPluginEmoji returns an appropriate emoji for a plugin based on its name
func getPluginEmoji(pluginName string) string {
	name := strings.ToLower(pluginName)

	// Music/Audio related
	if strings.Contains(name, "music") || strings.Contains(name, "reaper") || strings.Contains(name, "audio") {
		return "🎵"
	}

	// Development/Code related
	if strings.Contains(name, "code") || strings.Contains(name, "dev") || strings.Contains(name, "git") {
		return "💻"
	}

	// File/System related
	if strings.Contains(name, "file") || strings.Contains(name, "system") || strings.Contains(name, "manager") {
		return "📁"
	}

	// Data/Database related
	if strings.Contains(name, "data") || strings.Contains(name, "database") || strings.Contains(name, "sql") {
		return "📊"
	}

	// Network/Web related
	if strings.Contains(name, "web") || strings.Contains(name, "http") || strings.Contains(name, "api") {
		return "🌐"
	}

	// Default plugin emoji
	return "🔌"
}

// PluginRegistryHandler handles plugin registry operations
func (h *RegistryHandler) PluginRegistryHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		reg, _, err := h.registryManager.Load()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Get filter_compatible query parameter (defaults to "true")
		filterCompatible := r.URL.Query().Get("filter_compatible")
		if filterCompatible == "" || filterCompatible == "true" {
			// Filter plugins by OS/arch compatibility
			compatiblePlugins := []types.PluginRegistryEntry{}
			currentOS := runtime.GOOS
			currentArch := runtime.GOARCH

			for _, plugin := range reg.Plugins {
				if plugin.IsCompatibleWithSystem(currentOS, currentArch) {
					compatiblePlugins = append(compatiblePlugins, plugin)
				}
			}

			reg.Plugins = compatiblePlugins
		}

		_ = json.NewEncoder(w).Encode(reg)

	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}

		reg, _, err := h.registryManager.Load()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// find entry by name
		var entryPath string
		var found bool
		for _, e := range reg.Plugins {
			if e.Name == req.Name {
				// Use plugin downloader to get the plugin (handles both local and remote)
				var err error
				var wasCached bool
				entryPath, wasCached, err = h.pluginDownloader.GetPlugin(e)
				if err != nil {
					http.Error(w, fmt.Sprintf("failed to get plugin %s: %v", e.Name, err), http.StatusInternalServerError)
					return
				}

				if wasCached {
					fmt.Printf("Plugin %s is already downloaded\n", e.Name)
				}

				// Ensure path is absolute
				if abs, err := filepath.Abs(entryPath); err == nil {
					entryPath = abs
				}
				// skip if already loaded for current agent (avoid duplicate plugin.Open errors)
				_, current := h.store.ListAgents()
				ag, ok := h.store.GetAgent(current)
				if ok {
					for _, lp := range ag.Plugins {
						// Check if plugin is already loaded from the same file path
						lpAbsPath, err1 := filepath.Abs(lp.Path)
						if err1 == nil && lpAbsPath == entryPath {
							w.WriteHeader(http.StatusOK)
							return
						}
						// Also check by definition name for backward compatibility
						if strings.EqualFold(lp.Definition.Name, e.Name) {
							w.WriteHeader(http.StatusOK)
							return
						}
					}
				}
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "plugin not found in registry", http.StatusBadRequest)
			return
		}

		// load plugin using unified loader (supports both .so and RPC executables)
		tool, err := pluginloader.LoadPluginUnified(entryPath)
		if err != nil {
			http.Error(w, fmt.Sprintf("load plugin %s: %v", entryPath, err), http.StatusInternalServerError)
			return
		}
		def := tool.Definition()

		// attach to current agent
		_, current := h.store.ListAgents()

		// Set agent context for plugins that support it
		// Construct the agent-specific path since agentStorePath is now the index file
		agentSpecificPath := filepath.Join(filepath.Dir(h.agentStorePath), current, "config.json")
		// Pass empty location for now - location will be updated when location manager is available
		pluginloader.SetAgentContext(tool, current, agentSpecificPath, "")

		// Extract plugin settings schema for this agent if plugin supports get_settings
		if err := pluginloader.ExtractPluginSettingsSchema(tool, current); err != nil {
			log.Printf("Warning: failed to extract settings schema for plugin %s in agent %s: %v", def.Name, current, err)
		}
		ag, ok := h.store.GetAgent(current)
		if !ok {
			http.Error(w, "current agent not found", http.StatusInternalServerError)
			return
		}
		if ag.Plugins == nil {
			ag.Plugins = map[string]types.LoadedPlugin{}
		}
		version := pluginloader.GetPluginVersion(tool)
		ag.Plugins[def.Name] = types.LoadedPlugin{Tool: tool, Definition: def, Path: entryPath, Version: version}
		if err := h.store.SetAgent(current, ag); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Generic plugin welcome - try to display settings for any plugin that supports it
		go func() {
			// Small delay to ensure plugin is fully loaded
			time.Sleep(100 * time.Millisecond)

			ctx := context.Background()

			// Try common settings operations - plugins can implement any of these
			settingsOperations := []string{
				`{"operation":"get_settings"}`,
				`{"operation":"status"}`,
				`{"operation":"info"}`,
			}

			var settingsResult string
			var settingsErr error

			// Try each operation until one works
			for _, operation := range settingsOperations {
				settingsResult, settingsErr = tool.Call(ctx, operation)
				if settingsErr == nil {
					break
				}
			}

			if settingsErr != nil {
				// If no settings operation works, just log the basic load message
				log.Printf("🔌 Plugin '%s' loaded successfully!", def.Name)
				return
			}

			// Get a suitable emoji based on plugin name
			emoji := getPluginEmoji(def.Name)

			log.Printf("%s Plugin '%s' loaded successfully!", emoji, def.Name)
			log.Printf("Current settings/status:\n%s", settingsResult)
		}()

		w.WriteHeader(http.StatusOK)

	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		if strings.TrimSpace(name) == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}

		// Only delete from local registry (user uploaded plugins)
		localReg, err := h.registryManager.LoadLocal()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Find and remove the plugin from local registry only
		var foundIndex = -1
		var pluginToDelete types.PluginRegistryEntry
		for i, plugin := range localReg.Plugins {
			if plugin.Name == name {
				foundIndex = i
				pluginToDelete = plugin
				break
			}
		}

		if foundIndex == -1 {
			http.Error(w, "plugin not found in local registry (only user uploaded plugins can be deleted)", http.StatusNotFound)
			return
		}

		// Remove plugin from local registry
		localReg.Plugins = append(localReg.Plugins[:foundIndex], localReg.Plugins[foundIndex+1:]...)

		// Save updated local registry
		if err := h.registryManager.SaveLocal(localReg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Remove the plugin file if it's in uploaded_plugins directory
		if pluginToDelete.Path != "" && strings.Contains(pluginToDelete.Path, "uploaded_plugins") {
			if err := os.Remove(pluginToDelete.Path); err != nil {
				// Log the error but don't fail the request - registry entry is already removed
				log.Printf("Warning: Failed to remove plugin file %s: %v", pluginToDelete.Path, err)
			}
		}

		// Also unload the plugin from current agent if it's loaded
		_, current := h.store.ListAgents()
		ag, ok := h.store.GetAgent(current)
		if ok && ag.Plugins != nil {
			// Clean up RPC plugin if it is one
			if loadedPlugin, exists := ag.Plugins[name]; exists {
				pluginloader.CloseRPCPlugin(loadedPlugin.Tool)
			}
			delete(ag.Plugins, name)
			if err := h.store.SetAgent(current, ag); err != nil {
				log.Printf("Warning: Failed to unload plugin %s from agent: %v", name, err)
			}
		}

		w.WriteHeader(http.StatusOK)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// PluginUpdatesHandler handles plugin update operations
func (h *RegistryHandler) PluginUpdatesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Check for available updates
		reg, _, err := h.registryManager.Load()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		updates, err := h.pluginDownloader.CheckForUpdates(reg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"available_updates": updates,
			"count":             len(updates),
		})

	case http.MethodPost:
		// Trigger update for specific plugins or all
		var req struct {
			PluginNames []string `json:"plugin_names,omitempty"` // Empty = update all
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		reg, _, err := h.registryManager.Load()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var updated []string
		var errors []string

		for _, entry := range reg.Plugins {
			// Skip if specific plugins requested and this isn't one of them
			if len(req.PluginNames) > 0 {
				found := false
				for _, name := range req.PluginNames {
					if name == entry.Name {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}

			// Only update plugins with URLs and auto-update enabled
			if entry.URL != "" && entry.AutoUpdate {
				_, _, err := h.pluginDownloader.GetPlugin(entry)
				if err != nil {
					errors = append(errors, fmt.Sprintf("%s: %v", entry.Name, err))
				} else {
					updated = append(updated, entry.Name)
				}
			}
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"updated": updated,
			"errors":  errors,
		})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// PluginDownloadHandler downloads GitHub plugins to uploaded_plugins folder
func (h *RegistryHandler) PluginDownloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "method not allowed",
		})
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "name required",
		})
		return
	}

	// Load registry to find the plugin
	reg, _, err := h.registryManager.Load()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// Find the plugin entry
	var pluginEntry *types.PluginRegistryEntry
	for _, entry := range reg.Plugins {
		if entry.Name == req.Name {
			pluginEntry = &entry
			break
		}
	}

	if pluginEntry == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "plugin not found in registry",
		})
		return
	}

	// Check platform compatibility
	currentPlatform := platform.DetectPlatform()
	if !pluginEntry.IsCompatibleWith(currentPlatform) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)

		// Build response with detailed compatibility information
		response := map[string]any{
			"success":             false,
			"error":               "platform_incompatible",
			"message":             "Plugin not available for your platform",
			"user_platform":       currentPlatform,
			"supported_platforms": pluginEntry.Platforms,
			"supported_os":        pluginEntry.SupportedOS,
			"supported_arch":      pluginEntry.SupportedArch,
		}

		log.Printf("Plugin download blocked: %s not compatible with %s", req.Name, currentPlatform)
		json.NewEncoder(w).Encode(response)
		return
	}

	// Check if it has a download URL
	if pluginEntry.DownloadURL == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "plugin does not have a download URL",
		})
		return
	}

	// Create uploaded_plugins directory if it doesn't exist
	uploadDir := "uploaded_plugins"
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": fmt.Sprintf("failed to create upload directory: %v", err),
		})
		return
	}

	// Download the plugin file
	log.Printf("Downloading plugin from URL: %s", pluginEntry.DownloadURL)
	resp, err := http.Get(pluginEntry.DownloadURL)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": fmt.Sprintf("failed to download plugin: %v", err),
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Download failed for %s: status %d", pluginEntry.DownloadURL, resp.StatusCode)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": fmt.Sprintf("download failed with status %d", resp.StatusCode),
		})
		return
	}

	// Determine the filename from the download URL
	filename := fmt.Sprintf("%s.so", pluginEntry.Name)
	if pluginEntry.DownloadURL != "" {
		// Extract filename from download URL
		parsedURL, err := url.Parse(pluginEntry.DownloadURL)
		if err == nil {
			urlFilename := filepath.Base(parsedURL.Path)
			if urlFilename != "." && urlFilename != "/" {
				filename = urlFilename
			}
		}
	}

	filePath := filepath.Join(uploadDir, filename)

	// Create the file
	file, err := os.Create(filePath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": fmt.Sprintf("failed to create file: %v", err),
		})
		return
	}
	defer file.Close()

	// Copy the downloaded content to the file
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": fmt.Sprintf("failed to save file: %v", err),
		})
		return
	}

	// Close file before setting permissions
	file.Close()

	// Make the file executable (for RPC plugins that are binaries)
	if err := os.Chmod(filePath, 0755); err != nil {
		log.Printf("Warning: failed to set executable permissions on %s: %v", filePath, err)
	}

	// Scan uploaded plugins to auto-register the newly downloaded plugin
	if err := h.registryManager.ScanUploadedPlugins(); err != nil {
		log.Printf("Warning: failed to scan uploaded_plugins after download: %v", err)
	}

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success":  true,
		"message":  fmt.Sprintf("Plugin %s downloaded successfully", pluginEntry.Name),
		"filename": filename,
		"path":     filePath,
	})
}

// PluginUpdatesCheckHandler checks for available updates for all installed plugins
func (h *RegistryHandler) PluginUpdatesCheckHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "method not allowed",
		})
		return
	}

	// Load registry to get latest versions
	reg, _, err := h.registryManager.Load()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	// Get currently installed plugins
	_, currentAgent := h.store.ListAgents()
	ag, ok := h.store.GetAgent(currentAgent)
	if !ok {
		http.Error(w, "current agent not found", http.StatusInternalServerError)
		return
	}

	// Check for updates
	var updates []map[string]any
	for name, installedPlugin := range ag.Plugins {
		// Find plugin in registry
		for _, registryEntry := range reg.Plugins {
			if registryEntry.Name == name && registryEntry.GitHubRepo != "" {
				// Compare versions
				if installedPlugin.Version != registryEntry.Version {
					updates = append(updates, map[string]any{
						"name":            name,
						"currentVersion":  installedPlugin.Version,
						"latestVersion":   registryEntry.Version,
						"updateAvailable": true,
						"description":     registryEntry.Description,
						"githubRepo":      registryEntry.GitHubRepo,
						"downloadURL":     registryEntry.DownloadURL,
					})
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success":      true,
		"updatesCount": len(updates),
		"updates":      updates,
	})
}
