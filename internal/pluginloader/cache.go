package pluginloader

import (
	"errors"
	"path/filepath"
	"plugin"
	"strings"
	"sync"

	"github.com/johnjallday/dolphin-agent/pluginapi"
)

// Global plugin cache to handle "plugin already loaded" errors from Go's plugin system
var (
	cache   = make(map[string]pluginapi.Tool)
	cacheMu sync.RWMutex
	
	// Track plugins by their definition name as well for cross-referencing
	nameToPathCache = make(map[string]string)
	nameToPathMu    sync.RWMutex
)

// LoadWithCache loads a plugin from the given path, using a global cache
// to handle "plugin already loaded" errors from Go's plugin system.
func LoadWithCache(path string) (pluginapi.Tool, error) {
	// Get absolute path for cache key
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path // fallback to original path
	}
	
	// Check cache first
	cacheMu.RLock()
	if tool, exists := cache[absPath]; exists {
		cacheMu.RUnlock()
		return tool, nil
	}
	cacheMu.RUnlock()
	
	// Load plugin
	p, err := plugin.Open(path)
	if err != nil {
		// If plugin is already loaded by Go runtime, try to find it in our cache
		if strings.Contains(err.Error(), "plugin already loaded") {
			// Search through all cached plugins to find one from this path
			cacheMu.RLock()
			// First try exact path match
			if tool, exists := cache[absPath]; exists {
				cacheMu.RUnlock()
				return tool, nil
			}
			
			// If not found in cache by path, search for any plugin from a similar path
			// Check if any cached plugin has the same base filename
			pathBase := filepath.Base(absPath)
			var foundTool pluginapi.Tool
			for cachedPath, tool := range cache {
				if filepath.Base(cachedPath) == pathBase {
					// Found a plugin with the same filename, reuse it
					foundTool = tool
					break
				}
			}
			cacheMu.RUnlock()
			
			if foundTool != nil {
				// Also cache it under the new path for future lookups
				cacheMu.Lock()
				cache[absPath] = foundTool
				cacheMu.Unlock()
				return foundTool, nil
			}
			
			// If still not found, the plugin might be loaded but from a different session
			return nil, errors.New("plugin file already loaded in Go runtime but not accessible. Restart the server to clear plugin state.")
		}
		return nil, err
	}
	sym, err := p.Lookup("Tool")
	if err != nil {
		return nil, err
	}
	tool, ok := sym.(pluginapi.Tool)
	if !ok {
		return nil, errors.New("invalid plugin type: symbol Tool does not implement pluginapi.Tool")
	}
	
	// Cache the loaded plugin
	cacheMu.Lock()
	cache[absPath] = tool
	cacheMu.Unlock()
	
	// Also cache the name-to-path mapping for cross-referencing
	def := tool.Definition()
	nameToPathMu.Lock()
	nameToPathCache[def.Name] = absPath
	nameToPathMu.Unlock()
	
	return tool, nil
}

// AddToCache manually adds a plugin tool to the cache for a given path.
// This is useful for pre-populating the cache with already loaded plugins.
func AddToCache(absPath string, tool pluginapi.Tool) {
	cacheMu.Lock()
	cache[absPath] = tool
	cacheMu.Unlock()
	
	// Also cache the name-to-path mapping
	def := tool.Definition()
	nameToPathMu.Lock()
	nameToPathCache[def.Name] = absPath
	nameToPathMu.Unlock()
}