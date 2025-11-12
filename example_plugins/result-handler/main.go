package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/hashicorp/go-plugin"
	"github.com/johnjallday/ori-agent/pluginapi"
	"github.com/openai/openai-go/v2"
)

//go:embed plugin.yaml
var configYAML string

// resultHandlerTool implements pluginapi.Tool for handling chat result actions
type resultHandlerTool struct {
	config pluginapi.PluginConfig
}

// Ensure compile-time conformance
var _ pluginapi.Tool = resultHandlerTool{}
var _ pluginapi.VersionedTool = resultHandlerTool{}
var _ pluginapi.PluginCompatibility = resultHandlerTool{}
var _ pluginapi.MetadataProvider = resultHandlerTool{}

func (t resultHandlerTool) Definition() openai.FunctionDefinitionParam {
	return openai.FunctionDefinitionParam{
		Name:        "result_handler",
		Description: openai.String("Handle actions on chat results like opening directories, files, or URLs"),
		Parameters: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "Action to perform",
					"enum":        []string{"open_directory", "open_file", "open_url", "reveal_in_finder"},
				},
				"path": map[string]any{
					"type":        "string",
					"description": "File path, directory path, or URL to open",
				},
				"context": map[string]any{
					"type":        "string",
					"description": "Optional context about what triggered this action (e.g., 'reaper_scripts', 'config_file')",
				},
			},
			"required": []string{"action", "path"},
		},
	}
}

func (t resultHandlerTool) Call(ctx context.Context, args string) (string, error) {
	var params struct {
		Action  string `json:"action"`
		Path    string `json:"path"`
		Context string `json:"context"`
	}

	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("failed to parse parameters: %w", err)
	}

	switch params.Action {
	case "open_directory", "reveal_in_finder":
		return t.openDirectory(params.Path, params.Context)
	case "open_file":
		return t.openFile(params.Path, params.Context)
	case "open_url":
		return t.openURL(params.Path, params.Context)
	default:
		return "", fmt.Errorf("unknown action: %s", params.Action)
	}
}

func (t resultHandlerTool) openDirectory(dirPath, context string) (string, error) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		// macOS: open directory in Finder
		cmd = exec.Command("open", dirPath)
	case "windows":
		// Windows: open directory in Explorer
		cmd = exec.Command("explorer", dirPath)
	case "linux":
		// Linux: try common file managers
		// Try in order: nautilus, dolphin, thunar, pcmanfm
		fileManagers := []string{"nautilus", "dolphin", "thunar", "pcmanfm", "xdg-open"}
		for _, fm := range fileManagers {
			if _, err := exec.LookPath(fm); err == nil {
				cmd = exec.Command(fm, dirPath)
				break
			}
		}
		if cmd == nil {
			return "", fmt.Errorf("no supported file manager found on Linux")
		}
	default:
		return "", fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to open directory %s: %w", dirPath, err)
	}

	contextMsg := ""
	if context != "" {
		contextMsg = fmt.Sprintf(" (%s)", context)
	}

	return fmt.Sprintf("📁 Opened directory in %s: %s%s", getFileManagerName(), dirPath, contextMsg), nil
}

func (t resultHandlerTool) openFile(filePath, context string) (string, error) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		// macOS: open file with default application
		cmd = exec.Command("open", filePath)
	case "windows":
		// Windows: open file with default application
		cmd = exec.Command("cmd", "/c", "start", "", filePath)
	case "linux":
		// Linux: use xdg-open
		cmd = exec.Command("xdg-open", filePath)
	default:
		return "", fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to open file %s: %w", filePath, err)
	}

	contextMsg := ""
	if context != "" {
		contextMsg = fmt.Sprintf(" (%s)", context)
	}

	return fmt.Sprintf("📄 Opened file: %s%s", filePath, contextMsg), nil
}

func (t resultHandlerTool) openURL(url, context string) (string, error) {
	// Ensure URL has a scheme
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "file://") {
		url = "https://" + url
	}

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		// macOS: open URL in default browser
		cmd = exec.Command("open", url)
	case "windows":
		// Windows: open URL in default browser
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "linux":
		// Linux: use xdg-open
		cmd = exec.Command("xdg-open", url)
	default:
		return "", fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to open URL %s: %w", url, err)
	}

	contextMsg := ""
	if context != "" {
		contextMsg = fmt.Sprintf(" (%s)", context)
	}

	return fmt.Sprintf("🌐 Opened URL in browser: %s%s", url, contextMsg), nil
}

func getFileManagerName() string {
	switch runtime.GOOS {
	case "darwin":
		return "Finder"
	case "windows":
		return "Explorer"
	case "linux":
		return "file manager"
	default:
		return "file manager"
	}
}

// Version returns the plugin version from config.
func (t resultHandlerTool) Version() string {
	return t.config.Version
}

// MinAgentVersion returns the minimum compatible agent version from config.
func (t resultHandlerTool) MinAgentVersion() string {
	return t.config.Requirements.MinOriVersion
}

// MaxAgentVersion returns the maximum compatible agent version (empty = no limit).
func (t resultHandlerTool) MaxAgentVersion() string {
	return ""
}

// APIVersion returns the API version this plugin implements.
func (t resultHandlerTool) APIVersion() string {
	return "v1"
}

// GetMetadata returns plugin metadata from config.
func (t resultHandlerTool) GetMetadata() (*pluginapi.PluginMetadata, error) {
	return t.config.ToMetadata()
}

func main() {
	// Parse plugin config from embedded YAML
	config := pluginapi.ReadPluginConfig(configYAML)

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: pluginapi.Handshake,
		Plugins: map[string]plugin.Plugin{
			"tool": &pluginapi.ToolRPCPlugin{Impl: resultHandlerTool{config: config}},
		},
		GRPCServer: plugin.DefaultGRPCServer,
	})
}
