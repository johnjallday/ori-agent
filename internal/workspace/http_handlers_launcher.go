package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
)

const folderPickerControlPort = "21547"

var errFolderPickerAppNotFound = errors.New("folder picker app not found")

type folderPickerSelectPathRequest struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Title       string `json:"title,omitempty"`
}

type folderPickerSelectPathResponse struct {
	Success  bool   `json:"success"`
	Selected bool   `json:"selected,omitempty"`
	Path     string `json:"path,omitempty"`
	Error    string `json:"error,omitempty"`
}

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
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"message": "Folder picker shown",
		})
		return
	}

	// App not running, need to launch it
	if err := launchFolderPickerApp(reqBody.WorkspaceID); err != nil {
		logger.Error("Failed to launch folder picker", logger.Fields{"error": err})
		w.Header().Set("Content-Type", "application/json")
		status, errMsg := folderPickerLaunchErrorResponse(err)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"error":   errMsg,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"message": "Folder picker launched",
	})
}

// SelectFolderPath handles POST /api/folder-picker/select-path
// It ensures the folder picker helper is running, then opens a native folder dialog and returns the selected path.
func (h *HTTPHandler) SelectFolderPath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req folderPickerSelectPathRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if !showExistingFolderPicker(req.WorkspaceID) {
		if err := launchFolderPickerApp(req.WorkspaceID); err != nil {
			logger.Error("Failed to launch folder picker for path selection", logger.Fields{"error": err})
			w.Header().Set("Content-Type", "application/json")
			status, errMsg := folderPickerLaunchErrorResponse(err)
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(folderPickerSelectPathResponse{
				Success: false,
				Error:   errMsg,
			})
			return
		}

		if err := waitForFolderPickerReady(4 * time.Second); err != nil {
			logger.Error("Folder picker did not become ready for path selection", logger.Fields{"error": err})
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusGatewayTimeout)
			_ = json.NewEncoder(w).Encode(folderPickerSelectPathResponse{
				Success: false,
				Error:   "Folder picker is taking too long to start",
			})
			return
		}
	}

	selectedPath, selected, err := requestFolderSelection(req.Title)
	if err != nil {
		logger.Error("Failed to select folder path from picker", logger.Fields{"error": err})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(folderPickerSelectPathResponse{
			Success: false,
			Error:   "Failed to select folder path: " + err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(folderPickerSelectPathResponse{
		Success:  true,
		Selected: selected,
		Path:     selectedPath,
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

func launchFolderPickerApp(workspaceID string) error {
	appPath, err := findFolderPickerApp()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %w", errFolderPickerAppNotFound, err)
		}
		return err
	}

	var cmd *exec.Cmd
	args := []string{}
	if workspaceID != "" {
		args = append(args, "-workspace", workspaceID)
	}

	switch runtime.GOOS {
	case "darwin":
		if workspaceID != "" {
			cmd = exec.Command("open", appPath, "--args", "-workspace", workspaceID)
		} else {
			cmd = exec.Command("open", appPath)
		}
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", appPath)
		if workspaceID != "" {
			cmd.Args = append(cmd.Args, "-workspace", workspaceID)
		}
	default:
		cmd = exec.Command(appPath, args...)
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	logger.Info("Launched folder picker app", logger.Fields{"path": appPath})
	return nil
}

func folderPickerLaunchErrorResponse(err error) (int, string) {
	if errors.Is(err, errFolderPickerAppNotFound) {
		return http.StatusNotFound, "Folder picker app not found. Please build it with: ./scripts/build-folder-picker.sh"
	}
	return http.StatusInternalServerError, "Failed to launch folder picker: " + err.Error()
}

func waitForFolderPickerReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if isFolderPickerReady() {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("folder picker did not become ready within %s", timeout)
}

func isFolderPickerReady() bool {
	client := &http.Client{Timeout: 350 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:" + folderPickerControlPort + "/health")
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

func requestFolderSelection(title string) (string, bool, error) {
	payload, err := json.Marshal(map[string]string{"title": title})
	if err != nil {
		return "", false, err
	}

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Post(
		"http://127.0.0.1:"+folderPickerControlPort+"/select-folder",
		"application/json",
		bytes.NewBuffer(payload),
	)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	var result folderPickerSelectPathResponse
	if len(body) > 0 {
		_ = json.Unmarshal(body, &result)
	}

	if resp.StatusCode != http.StatusOK || !result.Success {
		if result.Error != "" {
			return "", false, errors.New(result.Error)
		}
		return "", false, fmt.Errorf("folder picker returned status %d", resp.StatusCode)
	}

	return result.Path, result.Selected, nil
}

// findFolderPickerApp searches for the folder picker app in common locations
func findFolderPickerApp() (string, error) {
	// Get the executable directory
	execPath, err := os.Executable()
	if err != nil {
		execPath = "."
	}
	execDir := filepath.Dir(execPath)
	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		cwd = "."
	}

	// List of paths to search
	var searchPaths []string

	switch runtime.GOOS {
	case "darwin":
		searchPaths = []string{
			filepath.Join(execDir, "ori-folder-picker.app"),
			filepath.Join(execDir, "..", "ori-folder-picker.app"),
			// macOS app bundle: executable is in Contents/MacOS/, resources in Contents/Resources/
			filepath.Join(execDir, "..", "Resources", "ori-folder-picker.app"),
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

	// Add candidates by walking up from cwd and executable dir.
	// This makes local dev resilient when server cwd is ./ori-data (go run from project root).
	searchPaths = append(searchPaths, parentDirectorySearchCandidates(cwd, runtime.GOOS)...)
	searchPaths = append(searchPaths, parentDirectorySearchCandidates(execDir, runtime.GOOS)...)

	for _, path := range searchPaths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", os.ErrNotExist
}

func parentDirectorySearchCandidates(startDir, goos string) []string {
	candidates := make([]string, 0, 10)
	if strings.TrimSpace(startDir) == "" {
		return candidates
	}

	seen := map[string]struct{}{}
	current := filepath.Clean(startDir)
	for i := 0; i < 6; i++ {
		var candidate string
		switch goos {
		case "darwin":
			candidate = filepath.Join(current, "bin", "ori-folder-picker.app")
			if _, ok := seen[candidate]; !ok {
				seen[candidate] = struct{}{}
				candidates = append(candidates, candidate)
			}
			candidate = filepath.Join(current, "cmd", "folder-picker", "build", "bin", "ori-folder-picker.app")
		case "windows":
			candidate = filepath.Join(current, "bin", "ori-folder-picker.exe")
			if _, ok := seen[candidate]; !ok {
				seen[candidate] = struct{}{}
				candidates = append(candidates, candidate)
			}
			candidate = filepath.Join(current, "cmd", "folder-picker", "build", "bin", "ori-folder-picker.exe")
		default:
			candidate = filepath.Join(current, "bin", "ori-folder-picker")
			if _, ok := seen[candidate]; !ok {
				seen[candidate] = struct{}{}
				candidates = append(candidates, candidate)
			}
			candidate = filepath.Join(current, "cmd", "folder-picker", "build", "bin", "ori-folder-picker")
		}
		if _, ok := seen[candidate]; !ok {
			seen[candidate] = struct{}{}
			candidates = append(candidates, candidate)
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return candidates
}
