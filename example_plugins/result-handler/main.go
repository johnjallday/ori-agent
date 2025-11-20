package main

//go:generate ../../bin/ori-plugin-gen -yaml=plugin.yaml -output=result_handler_generated.go

import (
	"context"
	_ "embed"
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/johnjallday/ori-agent/pluginapi"
)

//go:embed plugin.yaml
var configYAML string

// result_handlerTool implements pluginapi.Tool for handling chat result actions
// Note: Compile-time interface check is in result_handler_generated.go
type result_handlerTool struct {
	pluginapi.BasePlugin
}

// OperationHandler is a function that handles a specific operation
type OperationHandler func(t *result_handlerTool, params *ResultHandlerParams) (string, error)

// operationRegistry maps operation names to their handler functions
var operationRegistry = map[string]OperationHandler{
	"open_directory":   handleOpenDirectory,
	"open_file":        handleOpenFile,
	"open_url":         handleOpenURL,
	"reveal_in_finder": handleOpenDirectory, // Same as open_directory
}

// Note: Definition() is inherited from BasePlugin, which automatically reads from plugin.yaml
// Note: Call() is auto-generated in result_handler_generated.go from plugin.yaml

// Execute contains the business logic - called by the generated Call() method
func (t *result_handlerTool) Execute(ctx context.Context, params *ResultHandlerParams) (string, error) {
	// Look up handler in registry
	handler, ok := operationRegistry[params.Action]
	if !ok {
		return "", fmt.Errorf("unknown action: %s. Valid actions: open_directory, open_file, open_url, reveal_in_finder", params.Action)
	}

	// Execute the handler
	return handler(t, params)
}

// Operation handlers

func handleOpenDirectory(t *result_handlerTool, params *ResultHandlerParams) (string, error) {
	return t.openDirectory(params.Path, params.Context)
}

func handleOpenFile(t *result_handlerTool, params *ResultHandlerParams) (string, error) {
	return t.openFile(params.Path, params.Context)
}

func handleOpenURL(t *result_handlerTool, params *ResultHandlerParams) (string, error) {
	return t.openURL(params.Path, params.Context)
}

func (t *result_handlerTool) openDirectory(dirPath, context string) (string, error) {
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

func (t *result_handlerTool) openFile(filePath, context string) (string, error) {
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

func (t *result_handlerTool) openURL(url, context string) (string, error) {
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

func main() {
	pluginapi.ServePlugin(&result_handlerTool{}, configYAML)
}
