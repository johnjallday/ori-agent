package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/go-plugin"
	"github.com/johnjallday/ori-agent/pluginapi"
)

//go:embed plugin.yaml
var configYAML string

// ensure weatherTool implements pluginapi.PluginTool and optional interfaces
var (
	_ pluginapi.PluginTool          = (*weatherTool)(nil)
	_ pluginapi.VersionedTool       = (*weatherTool)(nil)
	_ pluginapi.PluginCompatibility = (*weatherTool)(nil)
	_ pluginapi.MetadataProvider    = (*weatherTool)(nil)
)

// weatherTool implements pluginapi.Tool for fetching weather.
type weatherTool struct {
	pluginapi.BasePlugin // Embed BasePlugin to get version/metadata methods for free
}

// Note: Definition() is inherited from BasePlugin, which automatically reads from plugin.yaml

// Call is invoked with the function arguments and returns weather data.
func (w *weatherTool) Call(ctx context.Context, args string) (string, error) {
	var p struct {
		Location string `json:"location"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", err
	}
	// TODO: replace with real API call.
	result := fmt.Sprintf("Sunny, 25°C in %s", p.Location)
	return result, nil
}

func main() {
	// Parse plugin config from embedded YAML
	config := pluginapi.ReadPluginConfig(configYAML)

	// Create weather tool with base plugin
	tool := &weatherTool{
		BasePlugin: pluginapi.NewBasePlugin(
			"weather",                         // Plugin name
			config.Version,                    // Version from config
			config.Requirements.MinOriVersion, // Min agent version
			"",                                // Max agent version (no limit)
			"v1",                              // API version
		),
	}

	// Set plugin config for YAML-based features
	tool.SetPluginConfig(&config)

	// Set metadata from config
	if metadata, err := config.ToMetadata(); err == nil {
		tool.SetMetadata(metadata)
	}

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: pluginapi.Handshake,
		Plugins: map[string]plugin.Plugin{
			"tool": &pluginapi.ToolRPCPlugin{Impl: tool},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}
