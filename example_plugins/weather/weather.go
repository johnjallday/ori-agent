package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/go-plugin"
	"github.com/johnjallday/ori-agent/pluginapi"
	"github.com/openai/openai-go/v2"
)

//go:embed plugin.yaml
var configYAML string

// ensure weatherTool implements pluginapi.Tool and pluginapi.VersionedTool
var _ pluginapi.Tool = (*weatherTool)(nil)
var _ pluginapi.VersionedTool = (*weatherTool)(nil)
var _ pluginapi.PluginCompatibility = (*weatherTool)(nil)
var _ pluginapi.MetadataProvider = (*weatherTool)(nil)

// weatherTool implements pluginapi.Tool for fetching weather.
type weatherTool struct {
	config pluginapi.PluginConfig
}

// Definition returns the OpenAI function definition for get_weather.
func (w *weatherTool) Definition() openai.FunctionDefinitionParam {
	return openai.FunctionDefinitionParam{
		Name:        "get_weather",
		Description: openai.String("Get weather for a given location"),
		Parameters: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"location": map[string]any{
					"type":        "string",
					"description": "Location to get weather for",
				},
			},
			"required": []string{"location"},
		},
	}
}

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

// Version returns the plugin version from config.
func (w *weatherTool) Version() string {
	return w.config.Version
}

// MinAgentVersion returns the minimum compatible agent version from config.
func (w *weatherTool) MinAgentVersion() string {
	return w.config.Requirements.MinOriVersion
}

// MaxAgentVersion returns the maximum compatible agent version (empty = no limit).
func (w *weatherTool) MaxAgentVersion() string {
	return ""
}

// APIVersion returns the API version this plugin implements.
func (w *weatherTool) APIVersion() string {
	return "v1"
}

// GetMetadata returns plugin metadata from config.
func (w *weatherTool) GetMetadata() (*pluginapi.PluginMetadata, error) {
	return w.config.ToMetadata()
}

func main() {
	// Parse plugin config from embedded YAML
	config := pluginapi.ReadPluginConfig(configYAML)

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: pluginapi.Handshake,
		Plugins: map[string]plugin.Plugin{
			"tool": &pluginapi.ToolRPCPlugin{Impl: &weatherTool{config: config}},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}
