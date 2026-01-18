package pluginloader

import (
	"fmt"
	"os"

	"github.com/oriagent/ori-pluginapi"
)

// LoadPluginUnified loads a plugin as an RPC executable
// All plugins are now RPC-based executables for cross-platform compatibility
func LoadPluginUnified(path string) (pluginapi.PluginTool, error) {
	// Verify file exists
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("plugin file not found at path %q: %w", path, err)
	}

	// Load as RPC executable
	rpcClient, err := LoadPluginRPC(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load RPC plugin from %q: %w", path, err)
	}
	return rpcClient, nil
}

// IsRPCPlugin checks if a tool was loaded via RPC
func IsRPCPlugin(tool pluginapi.PluginTool) bool {
	_, ok := tool.(*RPCPluginClient)
	return ok
}

// CloseRPCPlugin safely closes an RPC plugin if it is one
func CloseRPCPlugin(tool pluginapi.PluginTool) {
	if rpcClient, ok := tool.(*RPCPluginClient); ok {
		rpcClient.Kill()
	}
}

// GetPluginMetadata retrieves metadata from a plugin if it implements MetadataProvider
func GetPluginMetadata(tool pluginapi.PluginTool) (*pluginapi.PluginMetadata, error) {
	if metadataProvider, ok := tool.(pluginapi.MetadataProvider); ok {
		return metadataProvider.GetMetadata()
	}
	return nil, nil // Plugin doesn't implement MetadataProvider
}

// GetPluginFileSupport checks if a plugin implements FileAttachmentHandler
// and returns the accepted file types. Returns (false, nil) if not supported.
func GetPluginFileSupport(tool pluginapi.PluginTool) (supportsFiles bool, acceptedTypes []string) {
	if fileHandler, ok := tool.(pluginapi.FileAttachmentHandler); ok {
		acceptedTypes = fileHandler.AcceptsFiles()
		return true, acceptedTypes
	}
	return false, nil
}

// PluginSupportsFiles returns true if the plugin implements FileAttachmentHandler
func PluginSupportsFiles(tool pluginapi.PluginTool) bool {
	_, ok := tool.(pluginapi.FileAttachmentHandler)
	return ok
}
