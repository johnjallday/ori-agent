package workspace

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

const folderPickerControlPort = "21547"

// LaunchFolderPicker handles POST /api/launch-folder-picker
// Shows the folder picker window if already running, or launches the app
func (h *HTTPHandler) LaunchFolderPicker(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse optional workspace_id from request body
	var reqBody struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
	}

	// First, try to show existing window via local control server
	if showExistingFolderPicker(reqBody.WorkspaceID) {
		logger.Info("Showed existing folder picker window", logger.Fields{"workspace_id": reqBody.WorkspaceID})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Folder picker shown",
		})
		return
	}

	// App not running, need to launch it
	appPath, err := findFolderPickerApp()
	if err != nil {
		logger.Error("Folder picker app not found", logger.Fields{"error": err})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Folder picker app not found. Please build it with: ./scripts/build-folder-picker.sh",
		})
		return
	}

	// Launch the app with workspace_id if provided
	var cmd *exec.Cmd
	args := []string{}
	if reqBody.WorkspaceID != "" {
		args = append(args, "-workspace", reqBody.WorkspaceID)
	}

	switch runtime.GOOS {
	case "darwin":
		// On macOS, 'open' doesn't pass arguments to the app easily unless using --args
		if reqBody.WorkspaceID != "" {
			cmd = exec.Command("open", appPath, "--args", "-workspace", reqBody.WorkspaceID)
		} else {
			cmd = exec.Command("open", appPath)
		}
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", appPath)
		if reqBody.WorkspaceID != "" {
			cmd.Args = append(cmd.Args, "-workspace", reqBody.WorkspaceID)
		}
	default: // linux and others
		cmd = exec.Command(appPath, args...)
	}

	if err := cmd.Start(); err != nil {
		logger.Error("Failed to launch folder picker", logger.Fields{"error": err, "path": appPath})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   "Failed to launch folder picker: " + err.Error(),
		})
		return
	}

	logger.Info("Launched folder picker app", logger.Fields{"path": appPath})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Folder picker launched",
	})
}

// showExistingFolderPicker tries to show the window of an already running folder picker
func showExistingFolderPicker(workspaceID string) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}

	url := "http://127.0.0.1:" + folderPickerControlPort + "/show"
	if workspaceID != "" {
		url += "?workspace_id=" + workspaceID
	}

	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	return resp.StatusCode == http.StatusOK
}

// ShutdownFolderPicker sends a quit signal to the folder picker app if it's running
func ShutdownFolderPicker() {
	client := &http.Client{Timeout: 500 * time.Millisecond}

	resp, err := client.Post("http://127.0.0.1:"+folderPickerControlPort+"/quit", "application/json", nil)
	if err != nil {
		// App not running, nothing to do
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		logger.Info("Folder picker app shutdown signal sent", nil)
	}
}

// findFolderPickerApp searches for the folder picker app in common locations
func findFolderPickerApp() (string, error) {
	// Get the executable directory
	execPath, err := os.Executable()
	if err != nil {
		execPath = "."
	}
	execDir := filepath.Dir(execPath)

	// List of paths to search
	var searchPaths []string

	switch runtime.GOOS {
	case "darwin":
		searchPaths = []string{
			filepath.Join(execDir, "ori-folder-picker.app"),
			filepath.Join(execDir, "..", "ori-folder-picker.app"),
			"./bin/ori-folder-picker.app",
			"./cmd/folder-picker/build/bin/ori-folder-picker.app",
			"/Applications/ori-folder-picker.app",
			filepath.Join(os.Getenv("HOME"), "Applications", "ori-folder-picker.app"),
		}
	case "windows":
		searchPaths = []string{
			filepath.Join(execDir, "ori-folder-picker.exe"),
			filepath.Join(execDir, "..", "ori-folder-picker.exe"),
			"./bin/ori-folder-picker.exe",
			"./cmd/folder-picker/build/bin/ori-folder-picker.exe",
		}
	default: // linux
		searchPaths = []string{
			filepath.Join(execDir, "ori-folder-picker"),
			filepath.Join(execDir, "..", "ori-folder-picker"),
			"./bin/ori-folder-picker",
			"./cmd/folder-picker/build/bin/ori-folder-picker",
			"/usr/local/bin/ori-folder-picker",
		}
	}

	for _, path := range searchPaths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", os.ErrNotExist
}
