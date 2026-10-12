package workspace

import (
	"bytes"
	"context"
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
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/platform"
	"github.com/johnjallday/ori-agent/internal/resetstate"
)

const folderPickerControlPort = "21547"

// What the picker endpoints answer when desktop launches are switched off: the
// helper is neither started nor signalled, and the page offers a typed path.
// The reason is for code; the note is the sentence a person reads.
const (
	folderPickerDialogOffReason = "dialog_off"
	folderPickerDialogOffNote   = "The folder dialog is switched off in this session. Paste a folder path instead."
)

var errFolderPickerAppNotFound = errors.New("folder picker app not found")

// The helper's two side effects, replaced in tests so a handler test can never
// start or signal a picker.
var (
	showFolderPicker   = showExistingFolderPicker
	launchFolderPicker = launchFolderPickerApp
)

type folderPickerDialogOffResponse struct {
	Success  bool   `json:"success"`
	Launched bool   `json:"launched"`
	Selected bool   `json:"selected"`
	Reason   string `json:"reason"`
	Note     string `json:"note"`
	// Error repeats the note for callers that only print a failure's error.
	Error string `json:"error"`
}

// folderPickerDialogOff answers a picker request while desktop launches are
// switched off, logging the skipped launch. It reports whether it answered.
func folderPickerDialogOff(w http.ResponseWriter, action, workspaceID string) bool {
	if !platform.DesktopLaunchSkipped(action, workspaceID) {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(folderPickerDialogOffResponse{
		Reason: folderPickerDialogOffReason, Note: folderPickerDialogOffNote, Error: folderPickerDialogOffNote,
	})
	return true
}

var folderPickerProcess struct {
	sync.Mutex
	release func()
}

type folderPickerSelectPathRequest struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	Title       string `json:"title,omitempty"`
	// Path is a folder the user typed or pasted instead of using the dialog.
	// Blank means "open the dialog", as before.
	Path string `json:"path,omitempty"`
}

type folderPickerSelectPathResponse struct {
	Success        bool   `json:"success"`
	Selected       bool   `json:"selected,omitempty"`
	Path           string `json:"path,omitempty"`
	SelectionToken string `json:"selection_token,omitempty"`
	// Warning and WarningNote accompany a typed path that is allowed but worth
	// a second look; the note is the sentence to show.
	Warning     string `json:"warning,omitempty"`
	WarningNote string `json:"warning_note,omitempty"`
	Error       string `json:"error,omitempty"`
}

// TypedFolderPath is a typed path the server accepted.
type TypedFolderPath struct {
	// Path is the canonical folder the typed text names.
	Path string
	// Warning is a short code and WarningNote its sentence; both empty when
	// there is nothing to warn about.
	Warning     string
	WarningNote string
}

// TypedFolderPathChecker decides whether a folder path the page typed may stand
// in for a native dialog choice. The dialog is a gesture the page cannot make;
// a typed path is not, so nothing is issued for one without this decision.
type TypedFolderPathChecker interface {
	// Allow refuses a request that did not come from Ori's own page.
	Allow(*http.Request) error
	// Check returns the folder, or an error whose text is shown as it is.
	Check(raw string) (TypedFolderPath, error)
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
	if folderPickerDialogOff(w, "launch_folder_picker", reqBody.WorkspaceID) {
		return
	}

	// Claim persistent-child ownership before launch or acknowledgement. The
	// claim survives the request until verified shutdown; a reset fence can
	// therefore never race a detached picker that may still call back.
	folderPickerProcess.Lock()
	if err := claimFolderPickerLocked(h.admissionGate); err != nil {
		folderPickerProcess.Unlock()
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	// First, try to show existing window via local control server.
	if showFolderPicker(reqBody.WorkspaceID) {
		folderPickerProcess.Unlock()
		logger.Info("Showed existing folder picker window", logger.Fields{"workspace_id": reqBody.WorkspaceID})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"message": "Folder picker shown",
		})
		return
	}

	// App not running, need to launch it.
	if err := launchFolderPicker(reqBody.WorkspaceID); err != nil {
		releaseFolderPickerLocked()
		folderPickerProcess.Unlock()
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
	folderPickerProcess.Unlock()

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
	// A typed path needs no dialog, so it is answered before the dialog is
	// asked for and works while desktop launches are switched off.
	if strings.TrimSpace(req.Path) != "" {
		h.selectTypedFolderPath(w, r, req)
		return
	}
	if folderPickerDialogOff(w, "select_folder_path", req.WorkspaceID) {
		return
	}

	folderPickerProcess.Lock()
	if err := claimFolderPickerLocked(h.admissionGate); err != nil {
		folderPickerProcess.Unlock()
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if !showFolderPicker(req.WorkspaceID) {
		if err := launchFolderPicker(req.WorkspaceID); err != nil {
			releaseFolderPickerLocked()
			folderPickerProcess.Unlock()
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
			// Launch succeeded but child state is ambiguous: retain ownership.
			folderPickerProcess.Unlock()
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
	folderPickerProcess.Unlock()

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

	selectionToken := ""
	if selected {
		if selectionToken, err = h.issueFolderSelection(selectedPath, req.WorkspaceID); err != nil {
			writeFolderSelectionUnavailable(w, err)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(folderPickerSelectPathResponse{
		Success: true, Selected: selected, Path: selectedPath, SelectionToken: selectionToken,
	})
}

// selectTypedFolderPath answers select-path for a folder the user typed. An
// accepted path gets the same answer, and the same selection token, as a folder
// chosen in the dialog, so every caller keeps one flow.
func (h *HTTPHandler) selectTypedFolderPath(w http.ResponseWriter, r *http.Request, req folderPickerSelectPathRequest) {
	w.Header().Set("Content-Type", "application/json")
	if h.typedFolderPaths == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(folderPickerSelectPathResponse{Error: "Typing a folder path is unavailable here. Use the folder dialog instead."})
		return
	}
	if err := h.typedFolderPaths.Allow(r); err != nil {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(folderPickerSelectPathResponse{Error: "That request did not come from Ori."})
		return
	}
	typed, err := h.typedFolderPaths.Check(req.Path)
	if err != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(folderPickerSelectPathResponse{Error: err.Error()})
		return
	}
	selectionToken, err := h.issueFolderSelection(typed.Path, req.WorkspaceID)
	if err != nil {
		writeFolderSelectionUnavailable(w, err)
		return
	}
	_ = json.NewEncoder(w).Encode(folderPickerSelectPathResponse{
		Success: true, Selected: true, Path: typed.Path, SelectionToken: selectionToken,
		Warning: typed.Warning, WarningNote: typed.WarningNote,
	})
}

// issueFolderSelection mints the opaque receipt for a selected folder, scoped
// to the workspace when the request named one. "" when no issuer is wired.
func (h *HTTPHandler) issueFolderSelection(path, workspaceID string) (string, error) {
	if h.pathSelectionIssuer == nil {
		return "", nil
	}
	if scoped, ok := h.pathSelectionIssuer.(interface {
		IssueFor(path, scope string) (string, error)
	}); ok && strings.TrimSpace(workspaceID) != "" {
		return scoped.IssueFor(path, workspaceID)
	}
	return h.pathSelectionIssuer.Issue(path)
}

func writeFolderSelectionUnavailable(w http.ResponseWriter, err error) {
	logger.Error("Failed to issue trusted folder selection", logger.Fields{"error": err})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(folderPickerSelectPathResponse{Success: false, Error: "Trusted folder selection is unavailable"})
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

func claimFolderPickerLocked(gate *resetstate.WorkGate) error {
	if folderPickerProcess.release != nil {
		return nil
	}
	release, err := gate.EnterLifetime()
	if err != nil {
		return err
	}
	folderPickerProcess.release = release
	return nil
}

func releaseFolderPickerLocked() {
	if folderPickerProcess.release != nil {
		folderPickerProcess.release()
		folderPickerProcess.release = nil
	}
}

// ShutdownFolderPicker stops only a child previously claimed by this runtime
// and releases lifetime ownership only after the private listener is gone.
func ShutdownFolderPicker(ctx context.Context) error {
	folderPickerProcess.Lock()
	defer folderPickerProcess.Unlock()
	if folderPickerProcess.release == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Post("http://127.0.0.1:"+folderPickerControlPort+"/quit", "application/json", nil)
	if err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("folder picker refused shutdown: %s", resp.Status)
		}
		logger.Info("Folder picker app shutdown signal sent", nil)
	}
	for isFolderPickerReady() {
		select {
		case <-deadlineCtx.Done():
			return fmt.Errorf("folder picker shutdown remains ambiguous: %w", deadlineCtx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
	releaseFolderPickerLocked()
	return nil
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
			// #nosec G204 G702 -- appPath is resolved from a fixed set of known
			// install locations by findFolderPickerApp, never from request
			// input; workspaceID is passed as a discrete argv element (not
			// through a shell), so it cannot inject additional commands.
			cmd = exec.Command("open", appPath, "--args", "-workspace", workspaceID)
		} else {
			// #nosec G204 G702 -- appPath is resolved from a fixed set of known
			// install locations, never from request input.
			cmd = exec.Command("open", appPath)
		}
	case "windows":
		// #nosec G204 G702 -- appPath is resolved from a fixed set of known
		// install locations, never from request input.
		cmd = exec.Command("cmd", "/c", "start", "", appPath)
		if workspaceID != "" {
			cmd.Args = append(cmd.Args, "-workspace", workspaceID)
		}
	default:
		// #nosec G204 G702 -- appPath is resolved from a fixed set of known
		// install locations, never from request input.
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
		// #nosec G304 G703 -- path is drawn from a fixed set of known install
		// locations derived from the executable directory and cwd, never
		// from request input; this only stats candidate paths.
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
