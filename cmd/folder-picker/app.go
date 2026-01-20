package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const localServerPort = "21547" // Port for local control server

// App struct
type App struct {
	ctx                  context.Context
	baseURL              string
	preSelectedWorkspace string
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{
		baseURL: "http://localhost:8765",
	}
}

// startup is called when the app starts
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Start local control server
	go a.startLocalServer()
}

// startLocalServer starts a local HTTP server for control commands
func (a *App) startLocalServer() {
	mux := http.NewServeMux()

	mux.HandleFunc("/show", func(w http.ResponseWriter, r *http.Request) {
		// Check for workspace_id in query params or body
		workspaceID := r.URL.Query().Get("workspace_id")
		if workspaceID == "" && r.Body != nil {
			var body struct {
				WorkspaceID string `json:"workspace_id"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			workspaceID = body.WorkspaceID
		}
		if workspaceID != "" {
			a.preSelectedWorkspace = workspaceID
		}
		a.ShowWindow()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"success": true})
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"running": true})
	})

	mux.HandleFunc("/quit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"success": true})
		// Quit after sending response
		go func() {
			time.Sleep(100 * time.Millisecond)
			a.Quit()
		}()
	})

	listener, err := net.Listen("tcp", "127.0.0.1:"+localServerPort)
	if err != nil {
		// Port might be in use by another instance - that's fine
		return
	}

	http.Serve(listener, mux)
}

// ShowWindow brings the window to the front
func (a *App) ShowWindow() {
	runtime.WindowShow(a.ctx)
	runtime.WindowSetAlwaysOnTop(a.ctx, true)
	runtime.WindowSetAlwaysOnTop(a.ctx, false)
}

// HideWindow hides the window (minimize to tray)
func (a *App) HideWindow() {
	runtime.WindowHide(a.ctx)
}

// Workspace represents a workspace from the API
type Workspace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// DirectoryResult represents the result of adding a directory
type DirectoryResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// GetWorkspaces fetches available workspaces from ori-agent
func (a *App) GetWorkspaces() ([]Workspace, error) {
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(a.baseURL + "/api/studios")
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ori-agent server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var result struct {
		Studios []Workspace `json:"studios"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return result.Studios, nil
}

// OpenFolderDialog opens a native folder picker dialog
func (a *App) OpenFolderDialog() (string, error) {
	path, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select Directory to Add",
	})
	if err != nil {
		return "", err
	}
	return path, nil
}

// AddDirectory sends the directory path to ori-agent
func (a *App) AddDirectory(workspaceID, name, path string) DirectoryResult {
	// Validate inputs
	if workspaceID == "" {
		return DirectoryResult{Success: false, Error: "Please select a workspace"}
	}
	if path == "" {
		return DirectoryResult{Success: false, Error: "Please select a directory"}
	}

	// Use folder name if no custom name provided
	if name == "" {
		name = filepath.Base(path)
	}

	// Validate path exists and is a directory
	info, err := os.Stat(path)
	if err != nil {
		return DirectoryResult{Success: false, Error: fmt.Sprintf("Cannot access path: %v", err)}
	}
	if !info.IsDir() {
		return DirectoryResult{Success: false, Error: "Selected path is not a directory"}
	}

	// Send to API
	payload := map[string]interface{}{
		"name": name,
		"path": path,
		"x":    400,
		"y":    300,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return DirectoryResult{Success: false, Error: "Failed to prepare request"}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("%s/api/studios/%s/directories", a.baseURL, workspaceID)

	resp, err := client.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return DirectoryResult{Success: false, Error: fmt.Sprintf("Failed to connect to server: %v", err)}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return DirectoryResult{Success: false, Error: errResp.Error}
		}
		return DirectoryResult{Success: false, Error: fmt.Sprintf("Server error: %s", string(body))}
	}

	return DirectoryResult{
		Success: true,
		Message: fmt.Sprintf("Directory '%s' added successfully!", name),
	}
}

// ValidatePath checks if a path exists and is a directory
func (a *App) ValidatePath(path string) DirectoryResult {
	if path == "" {
		return DirectoryResult{Success: false, Error: "Path is empty"}
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DirectoryResult{Success: false, Error: "Path does not exist"}
		}
		return DirectoryResult{Success: false, Error: fmt.Sprintf("Cannot access path: %v", err)}
	}

	if !info.IsDir() {
		return DirectoryResult{Success: false, Error: "Path is not a directory"}
	}

	return DirectoryResult{Success: true, Message: "Valid directory"}
}

// CheckServerConnection checks if ori-agent server is running
func (a *App) CheckServerConnection() bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(a.baseURL + "/api/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// GetPreSelectedWorkspace returns the pre-selected workspace ID and clears it
func (a *App) GetPreSelectedWorkspace() string {
	ws := a.preSelectedWorkspace
	a.preSelectedWorkspace = ""
	return ws
}

// Quit closes the application
func (a *App) Quit() {
	runtime.Quit(a.ctx)
}
