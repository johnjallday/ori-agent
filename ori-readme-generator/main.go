package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/go-plugin"
	"github.com/johnjallday/ori-agent/pluginapi"
)

//go:embed plugin.yaml
var configYAML string

// Ensure readmeGeneratorTool implements required interfaces
var (
	_ pluginapi.PluginTool          = (*readmeGeneratorTool)(nil)
	_ pluginapi.VersionedTool       = (*readmeGeneratorTool)(nil)
	_ pluginapi.PluginCompatibility = (*readmeGeneratorTool)(nil)
	_ pluginapi.MetadataProvider    = (*readmeGeneratorTool)(nil)
)

// readmeGeneratorTool generates README sections for ori-agent
type readmeGeneratorTool struct {
	pluginapi.BasePlugin
}

// Definition returns the tool definition
func (r *readmeGeneratorTool) Definition() pluginapi.Tool {
	return pluginapi.Tool{
		Name:        "generate_readme",
		Description: "Generate or update README.md sections based on ori-agent codebase analysis",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"section": map[string]interface{}{
					"type":        "string",
					"description": "Which section to generate: 'plugins' (list available plugins)",
					"enum":        []string{"plugins"},
				},
			},
			"required": []string{"section"},
		},
	}
}

// Call executes the README generation
func (r *readmeGeneratorTool) Call(ctx context.Context, args string) (string, error) {
	var params struct {
		Section string `json:"section"`
	}

	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	switch params.Section {
	case "plugins":
		return r.generatePluginsSection()
	default:
		return "", fmt.Errorf("unknown section: %s", params.Section)
	}
}

// generatePluginsSection scans the plugin registry and generates a plugins list
func (r *readmeGeneratorTool) generatePluginsSection() (string, error) {
	// Look for local_plugin_registry.json in current directory
	registryPath := "local_plugin_registry.json"

	data, err := os.ReadFile(registryPath)
	if err != nil {
		// If file doesn't exist, return empty list
		if os.IsNotExist(err) {
			return "## Available Plugins\n\nNo plugins currently installed.\n", nil
		}
		return "", fmt.Errorf("failed to read registry: %w", err)
	}

	var registry struct {
		Plugins []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Path        string `json:"path"`
		} `json:"plugins"`
	}

	if err := json.Unmarshal(data, &registry); err != nil {
		return "", fmt.Errorf("failed to parse registry: %w", err)
	}

	// Generate markdown
	var sb strings.Builder
	sb.WriteString("## Available Plugins\n\n")

	if len(registry.Plugins) == 0 {
		sb.WriteString("No plugins currently installed.\n")
	} else {
		sb.WriteString("| Plugin | Description | Location |\n")
		sb.WriteString("|--------|-------------|----------|\n")

		for _, p := range registry.Plugins {
			// Get relative path for readability
			relPath := filepath.Base(filepath.Dir(p.Path))
			sb.WriteString(fmt.Sprintf("| **%s** | %s | `%s/` |\n",
				p.Name,
				p.Description,
				relPath))
		}
	}

	return sb.String(), nil
}

func main() {
	// Parse plugin config
	config := pluginapi.ReadPluginConfig(configYAML)

	// Create the tool
	tool := &readmeGeneratorTool{
		BasePlugin: pluginapi.NewBasePlugin(
			"readme-generator",
			config.Version,
			config.Requirements.MinOriVersion,
			"",
			"v1",
		),
	}

	// Set metadata
	if metadata, err := config.ToMetadata(); err == nil {
		tool.SetMetadata(metadata)
	}

	// Serve the plugin
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: pluginapi.Handshake,
		Plugins: map[string]plugin.Plugin{
			"tool": &pluginapi.ToolRPCPlugin{Impl: tool},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}
