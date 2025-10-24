package pluginloader

import (
	"fmt"
	"os"

	"github.com/johnjallday/ori-agent/pluginapi"
)

// LoadPluginUnified loads a plugin as an RPC executable
// All plugins are now RPC-based executables for cross-platform compatibility
func LoadPluginUnified(path string) (pluginapi.Tool, error) {
	// Verify file exists
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("plugin file not found: %w", err)
	}

	// Load as RPC executable
	rpcClient, err := LoadPluginRPC(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load RPC plugin: %w", err)
	}
	return rpcClient, nil
}

// IsRPCPlugin checks if a tool was loaded via RPC
func IsRPCPlugin(tool pluginapi.Tool) bool {
	_, ok := tool.(*RPCPluginClient)
	return ok
}

// CloseRPCPlugin safely closes an RPC plugin if it is one
func CloseRPCPlugin(tool pluginapi.Tool) {
	if rpcClient, ok := tool.(*RPCPluginClient); ok {
		rpcClient.Kill()
	}
}
