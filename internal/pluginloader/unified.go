package pluginloader

import (
	"fmt"
	"os"

	"github.com/oriagent/ori-pluginapi"
)

// LoadPluginUnified loads a plugin executable via direct gRPC.
func LoadPluginUnified(path string) (pluginapi.PluginTool, error) {
	// Verify file exists
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("plugin file not found at path %q: %w", path, err)
	}

	grpcClient, err := LoadPluginDirectGRPC(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load plugin from %q via direct gRPC: %w", path, err)
	}
	return grpcClient, nil
}

// IsRPCPlugin checks if a tool was loaded via RPC
func IsRPCPlugin(tool pluginapi.PluginTool) bool {
	_, ok := tool.(*DirectGRPCPluginClient)
	return ok
}

// CloseRPCPlugin safely closes an RPC plugin if it is one
func CloseRPCPlugin(tool pluginapi.PluginTool) {
	if grpcClient, ok := tool.(*DirectGRPCPluginClient); ok {
		grpcClient.Kill()
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
