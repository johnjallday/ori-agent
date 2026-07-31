package downloadsjanitorhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/downloadsjanitor"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeStore struct {
	root       string
	workspaces map[string]*workspace.Workspace
}

func (f *fakeStore) Get(id string) (*workspace.Workspace, error) {
	ws, ok := f.workspaces[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return ws, nil
}

func (f *fakeStore) Update(id string, fn func(*workspace.Workspace) error) error {
	ws, ok := f.workspaces[id]
	if !ok {
		return errors.New("not found")
	}
	return fn(ws)
}

func (f *fakeStore) GetFolderPath(workspaceID string) (string, error) {
	if _, ok := f.workspaces[workspaceID]; !ok {
		return "", errors.New("not found")
	}
	dir := filepath.Join(f.root, workspaceID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return dir, nil
}

type fixedUser string

func (u fixedUser) CurrentUserID(context.Context) (string, error) { return string(u), nil }

func newTestHandler(t *testing.T, owners map[string]string) (*Handler, *fakeStore) {
	t.Helper()
	store := &fakeStore{root: t.TempDir(), workspaces: map[string]*workspace.Workspace{}}
	for id, owner := range owners {
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: id})
		ws.ID = id
		ws.OwnerUserID = owner
		store.workspaces[id] = ws
	}
	service := downloadsjanitor.NewService(downloadsjanitor.NewStore(store), store)
	return NewHandler(service, store, fixedUser(userprofile.LocalUserID)), store
}

func serve(t *testing.T, h *Handler, method, target string, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux)
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var decoded map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("response is not JSON (%d): %s", rec.Code, rec.Body.String())
		}
	}
	return rec, decoded
}

// inboxFixture creates an isolated inbox folder and returns its CANONICAL path.
//
// Setup resolves symlinks when it canonicalizes the chosen folder (FR-47), and
// on macOS the per-test temp dir lives under /var, which is a symlink to
// /private/var. Returning the unresolved path would make assertions fail for
// the right reason — the service correctly stored the real directory.
func inboxFixture(t *testing.T) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	dir := filepath.Join(base, "Inbox")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGetStatus_ReportsSetupRequiredBeforeSetup(t *testing.T) {
	h, _ := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})

	rec, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	status, _ := body["status"].(map[string]any)
	readiness, _ := status["readiness"].(map[string]any)
	if readiness["state"] != string(downloadsjanitor.ReadinessSetupRequired) {
		t.Fatalf("state = %v, want setup_required", readiness["state"])
	}
}

func TestConfirmSetup_ConfiguresTheWorkspace(t *testing.T) {
	h, store := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})
	root := inboxFixture(t)

	payload, _ := json.Marshal(map[string]string{"path": root})
	rec, body := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/setup", string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	status, _ := body["status"].(map[string]any)
	settings, _ := status["settings"].(map[string]any)
	if settings["root_path"] != filepath.Clean(root) {
		t.Fatalf("root_path = %v, want %q", settings["root_path"], root)
	}
	if settings["content_mode"] != string(downloadsjanitor.ContentModeMetadataOnly) {
		t.Fatalf("content_mode = %v, want metadata_only", settings["content_mode"])
	}
	if len(store.workspaces["ws-1"].DirectoryReferences) != 1 {
		t.Fatal("setup should have linked exactly one folder")
	}
}

func TestConfirmSetup_RejectsAFileWithAStableCode(t *testing.T) {
	h, _ := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})
	file := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(map[string]string{"path": file})
	rec, body := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/setup", string(payload))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	apiErr, _ := body["error"].(map[string]any)
	if apiErr == nil {
		apiErr = body
	}
	if apiErr["code"] != downloadsjanitor.CodeNotADirectory {
		t.Fatalf("error code = %v, want %q (body: %s)", apiErr["code"], downloadsjanitor.CodeNotADirectory, rec.Body.String())
	}
}

// A workspace owned by another user must be indistinguishable from one that
// does not exist, and must never be configured through this API.
func TestEndpoints_RejectAnotherUsersWorkspace(t *testing.T) {
	h, store := newTestHandler(t, map[string]string{"ws-other": "someone-else"})
	root := inboxFixture(t)

	rec, _ := serve(t, h, http.MethodGet, "/api/workspaces/ws-other/downloads-janitor", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET status = %d, want 404", rec.Code)
	}

	payload, _ := json.Marshal(map[string]string{"path": root})
	rec, _ = serve(t, h, http.MethodPost, "/api/workspaces/ws-other/downloads-janitor/setup", string(payload))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST setup = %d, want 404", rec.Code)
	}
	if len(store.workspaces["ws-other"].DirectoryReferences) != 0 {
		t.Fatal("another user's workspace must not be configured")
	}
}

func TestEndpoints_UnknownWorkspaceIs404(t *testing.T) {
	h, _ := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})
	rec, _ := serve(t, h, http.MethodGet, "/api/workspaces/nope/downloads-janitor", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGetReadiness_ReturnsComponentChecks(t *testing.T) {
	h, _ := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})
	root := inboxFixture(t)
	payload, _ := json.Marshal(map[string]string{"path": root})
	if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/setup", string(payload)); rec.Code != http.StatusOK {
		t.Fatalf("setup failed: %s", rec.Body.String())
	}

	rec, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor/readiness", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	readiness, _ := body["readiness"].(map[string]any)
	checks, _ := readiness["checks"].([]any)
	if len(checks) != len(downloadsjanitor.RequiredComponents) {
		t.Fatalf("expected one check per required component, got %d", len(checks))
	}
}

func TestHandler_WithoutAServiceReports503(t *testing.T) {
	h := NewHandler(nil, nil, nil)
	rec, _ := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// Setup is the only endpoint that accepts a path at all; reading status must
// never take one, so a client cannot steer the Janitor by querystring.
func TestGetStatus_IgnoresClientSuppliedPaths(t *testing.T) {
	h, store := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})
	rec, _ := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor?path=/etc", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(store.workspaces["ws-1"].DirectoryReferences) != 0 {
		t.Fatal("reading status must not link any folder")
	}
}

// ---------------------------------------------------------------- batch API

// agedFile writes a file and backdates it so the scanner treats it as a
// finished download rather than one still being written.
func agedFile(t *testing.T, root, name string, size int) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

// configuredHandler returns a handler whose workspace is already set up against
// an isolated inbox folder.
func configuredHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	h, _ := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID, "ws-other": "someone-else"})
	root := inboxFixture(t)
	payload, _ := json.Marshal(map[string]string{"path": root})
	if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/setup", string(payload)); rec.Code != http.StatusOK {
		t.Fatalf("setup failed: %s", rec.Body.String())
	}
	return h, root
}

func TestTestScan_ReportsWithoutCreatingABatch(t *testing.T) {
	h, root := configuredHandler(t)
	agedFile(t, root, "report.pdf", 100)

	rec, body := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/test-scan", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	report, _ := body["report"].(map[string]any)
	if report["eligible_count"] != float64(1) {
		t.Fatalf("eligible_count = %v", report["eligible_count"])
	}

	rec, body = serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor/batches", "")
	if rec.Code != http.StatusOK || body["total"] != float64(0) {
		t.Fatalf("a test scan must create no batch: %s", rec.Body.String())
	}
}

func TestScanNow_CreatesABatchAndListsIt(t *testing.T) {
	h, root := configuredHandler(t)
	agedFile(t, root, "report.pdf", 100)
	agedFile(t, root, "photo.png", 50)

	rec, body := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/scan", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if body["created"] != true {
		t.Fatalf("expected a batch to be created: %s", rec.Body.String())
	}

	rec, body = serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor/batches/latest", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	candidates, _ := body["candidates"].([]any)
	if len(candidates) != 2 {
		t.Fatalf("candidates = %d: %s", len(candidates), rec.Body.String())
	}
	first, _ := candidates[0].(map[string]any)
	if first["state"] != "pending" || first["decision"] != nil {
		t.Fatalf("candidates must arrive pending and undecided: %+v", first)
	}
	// The destination is a label relative to the configured folder, never an
	// absolute path.
	destination, _ := first["destination"].(string)
	if destination == "" || strings.HasPrefix(destination, "/") {
		t.Fatalf("destination = %q, want a relative label", destination)
	}
	if strings.Contains(rec.Body.String(), root) {
		t.Fatal("the API response must not leak the absolute folder path")
	}
}

func TestGetBatch_LatestIsEmptyNotAnErrorWhenNothingPends(t *testing.T) {
	h, _ := configuredHandler(t)
	rec, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor/batches/latest", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if body["batch"] != nil {
		t.Fatalf("expected no pending batch: %s", rec.Body.String())
	}
}

func TestGetBatch_UnknownBatchIs404(t *testing.T) {
	h, _ := configuredHandler(t)
	rec, _ := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor/batches/nope", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestUpdateDecisions_AcceptsIDsAndCategoriesOnly(t *testing.T) {
	h, root := configuredHandler(t)
	agedFile(t, root, "report.pdf", 100)
	if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/scan", ""); rec.Code != http.StatusOK {
		t.Fatalf("scan failed: %s", rec.Body.String())
	}
	_, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor/batches/latest", "")
	candidates, _ := body["candidates"].([]any)
	first, _ := candidates[0].(map[string]any)
	id, _ := first["id"].(string)

	payload, _ := json.Marshal(map[string]any{
		"decisions": []map[string]string{{"candidate_id": id, "decision": "move", "category": "archives"}},
	})
	rec, body := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/decisions", string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	updated, _ := body["candidates"].([]any)
	got, _ := updated[0].(map[string]any)
	if got["decision"] != "move" || got["decision_category"] != "archives" {
		t.Fatalf("decision not recorded: %+v", got)
	}

	// The file has not moved: a decision is intent, not action.
	if _, err := os.Stat(filepath.Join(root, "report.pdf")); err != nil {
		t.Fatalf("recording a decision must not move the file: %v", err)
	}

	// A path-shaped category is rejected outright.
	payload, _ = json.Marshal(map[string]any{
		"decisions": []map[string]string{{"candidate_id": id, "decision": "move", "category": "../../etc"}},
	})
	if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/decisions", string(payload)); rec.Code != http.StatusBadRequest {
		t.Fatalf("a path-shaped category should be rejected, got %d", rec.Code)
	}

	// An unsupported operation is rejected before it reaches the service.
	payload, _ = json.Marshal(map[string]any{
		"decisions": []map[string]string{{"candidate_id": id, "decision": "delete"}},
	})
	if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/decisions", string(payload)); rec.Code != http.StatusBadRequest {
		t.Fatalf("an unsupported decision should be rejected, got %d", rec.Code)
	}
}

func TestUpdateDecisions_UnknownCandidateIs404(t *testing.T) {
	h, _ := configuredHandler(t)
	payload, _ := json.Marshal(map[string]any{
		"decisions": []map[string]string{{"candidate_id": "ghost", "decision": "skip"}},
	})
	rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/decisions", string(payload))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// Every batch route is workspace-scoped: another user's workspace is not
// listable, readable, scannable, or decidable.
func TestBatchRoutes_RejectAnotherUsersWorkspace(t *testing.T) {
	h, _ := configuredHandler(t)
	for _, target := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/workspaces/ws-other/downloads-janitor/batches"},
		{http.MethodGet, "/api/workspaces/ws-other/downloads-janitor/batches/latest"},
		{http.MethodPost, "/api/workspaces/ws-other/downloads-janitor/scan"},
		{http.MethodPost, "/api/workspaces/ws-other/downloads-janitor/test-scan"},
		{http.MethodGet, "/api/workspaces/ws-other/downloads-janitor/categories"},
	} {
		rec, _ := serve(t, h, target.method, target.path, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", target.method, target.path, rec.Code)
		}
	}
}

func TestListBatches_HasStablePaginationAndFilters(t *testing.T) {
	h, root := configuredHandler(t)
	agedFile(t, root, "report.pdf", 100)
	if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/scan", ""); rec.Code != http.StatusOK {
		t.Fatal("scan failed")
	}

	rec, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor/batches?limit=1&offset=0", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, key := range []string{"batches", "total", "limit", "offset"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("response is missing %q: %s", key, rec.Body.String())
		}
	}
	if body["limit"] != float64(1) {
		t.Fatalf("limit = %v", body["limit"])
	}

	// An out-of-range offset is an empty page, not an error.
	rec, body = serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor/batches?offset=999", "")
	batches, _ := body["batches"].([]any)
	if rec.Code != http.StatusOK || len(batches) != 0 {
		t.Fatalf("out-of-range offset = %d with %d batches", rec.Code, len(batches))
	}

	// A state filter that matches nothing is also just empty.
	_, body = serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor/batches?state=resolved", "")
	batches, _ = body["batches"].([]any)
	if len(batches) != 0 {
		t.Fatalf("expected no resolved batches, got %d", len(batches))
	}
}

func TestCategories_ServesTheFixedAllowlist(t *testing.T) {
	h, _ := configuredHandler(t)
	rec, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor/categories", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	categories, _ := body["categories"].([]any)
	if len(categories) != len(downloadsjanitor.CategoryRegistry) {
		t.Fatalf("categories = %d, want %d", len(categories), len(downloadsjanitor.CategoryRegistry))
	}
}

func TestResetSkipped_AcceptsAnEmptyBody(t *testing.T) {
	h, _ := configuredHandler(t)
	rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/skipped/reset", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
}

// ------------------------------------------------------- settings & privacy

func TestStatus_AlwaysStatesWhatOriReads(t *testing.T) {
	h, _ := configuredHandler(t)
	_, body := serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor", "")
	status, _ := body["status"].(map[string]any)
	privacy, _ := status["privacy"].(map[string]any)
	if privacy == nil {
		t.Fatalf("every status must carry the privacy state: %s", body)
	}
	if privacy["mode"] != "metadata_only" {
		t.Fatalf("mode = %v, want metadata_only by default", privacy["mode"])
	}
	if privacy["leaves_device"] == true {
		t.Fatal("metadata-only mode never leaves the device")
	}
	headline, _ := privacy["headline"].(string)
	if !strings.Contains(headline, "names, types, sizes, and dates") {
		t.Fatalf("headline = %q", headline)
	}
}

func TestUpdateSettings_LeavesUnsentFieldsAlone(t *testing.T) {
	h, _ := configuredHandler(t)

	payload, _ := json.Marshal(map[string]any{"daily_scan_local_time": "07:30"})
	rec, body := serve(t, h, http.MethodPatch, "/api/workspaces/ws-1/downloads-janitor/settings", string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	status, _ := body["status"].(map[string]any)
	settings, _ := status["settings"].(map[string]any)
	if settings["daily_scan_local_time"] != "07:30" {
		t.Fatalf("time = %v", settings["daily_scan_local_time"])
	}
	// The folder and content mode are untouched by a schedule change.
	if settings["root_path"] == nil || settings["content_mode"] != "metadata_only" {
		t.Fatalf("an unrelated field changed: %+v", settings)
	}

	// A bad value is rejected and changes nothing.
	payload, _ = json.Marshal(map[string]any{"daily_scan_local_time": "half past nine"})
	if rec, _ := serve(t, h, http.MethodPatch, "/api/workspaces/ws-1/downloads-janitor/settings", string(payload)); rec.Code == http.StatusOK {
		t.Fatal("an unusable time should be rejected")
	}
	_, body = serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor", "")
	status, _ = body["status"].(map[string]any)
	settings, _ = status["settings"].(map[string]any)
	if settings["daily_scan_local_time"] != "07:30" {
		t.Fatalf("a rejected change must not disturb the setting: %v", settings["daily_scan_local_time"])
	}
}

func TestPause_StopsAutomaticWorkAndSaysSo(t *testing.T) {
	h, _ := configuredHandler(t)
	payload, _ := json.Marshal(map[string]bool{"paused": true})
	rec, body := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/pause", string(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	status, _ := body["status"].(map[string]any)
	settings, _ := status["settings"].(map[string]any)
	if settings["paused"] != true {
		t.Fatalf("paused = %v", settings["paused"])
	}
	// Pausing keeps the folder configured.
	if settings["root_path"] == nil {
		t.Fatal("pausing must not unconfigure the workspace")
	}
}

func TestRevoke_DisconnectsTheFolder(t *testing.T) {
	h, _ := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})
	store := h.lookup.(*fakeStore)
	root := inboxFixture(t)
	payload, _ := json.Marshal(map[string]string{"path": root})
	if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/setup", string(payload)); rec.Code != http.StatusOK {
		t.Fatalf("setup failed: %s", rec.Body.String())
	}
	rec, body := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/revoke", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	status, _ := body["status"].(map[string]any)
	readiness, _ := status["readiness"].(map[string]any)
	if readiness["state"] != "setup_required" {
		t.Fatalf("state = %v, want setup_required", readiness["state"])
	}
	if len(store.workspaces["ws-1"].DirectoryReferences) != 0 {
		t.Fatal("the folder link must be gone")
	}
}

// Every settings route is workspace-scoped.
func TestSettingsRoutes_RejectAnotherUsersWorkspace(t *testing.T) {
	h, _ := configuredHandler(t)
	payload, _ := json.Marshal(map[string]any{"paused": true})
	for _, target := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPatch, "/api/workspaces/ws-other/downloads-janitor/settings", payload2(payload)},
		{http.MethodPost, "/api/workspaces/ws-other/downloads-janitor/pause", payload2(payload)},
		{http.MethodPost, "/api/workspaces/ws-other/downloads-janitor/revoke", ""},
		{http.MethodPost, "/api/workspaces/ws-other/downloads-janitor/relink", `{"path":"/tmp"}`},
		{http.MethodGet, "/api/workspaces/ws-other/downloads-janitor/skipped", ""},
		{http.MethodGet, "/api/workspaces/ws-other/downloads-janitor/history", ""},
	} {
		rec, _ := serve(t, h, target.method, target.path, target.body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", target.method, target.path, rec.Code)
		}
	}
}

func payload2(b []byte) string { return string(b) }

// No response from any endpoint carries an absolute path except the configured
// root the user chose themselves, which settings must show them.
func TestResponses_DoNotLeakPathsBeyondTheConfiguredRoot(t *testing.T) {
	h, root := configuredHandler(t)
	agedFile(t, root, "report.pdf", 100)
	if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/scan", ""); rec.Code != http.StatusOK {
		t.Fatal("scan failed")
	}

	for _, path := range []string{
		"/api/workspaces/ws-1/downloads-janitor/batches",
		"/api/workspaces/ws-1/downloads-janitor/batches/latest",
		"/api/workspaces/ws-1/downloads-janitor/history",
		"/api/workspaces/ws-1/downloads-janitor/skipped",
	} {
		rec, _ := serve(t, h, http.MethodGet, path, "")
		if strings.Contains(rec.Body.String(), root) {
			t.Errorf("%s leaked the folder path", path)
		}
		// Nor the parent directory, which would disclose the user's home layout.
		if strings.Contains(rec.Body.String(), filepath.Dir(root)) {
			t.Errorf("%s leaked a parent path", path)
		}
	}
}

// Unlinking the folder from the generic Linked Folders surface is something a
// user can do at any time. The Janitor must explain it, not fail opaquely.
func TestUnlinkedFolder_IsExplainedNotAnInternalError(t *testing.T) {
	h, store := newTestHandler(t, map[string]string{"ws-1": userprofile.LocalUserID})
	root := inboxFixture(t)
	payload, _ := json.Marshal(map[string]string{"path": root})
	if rec, _ := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/setup", string(payload)); rec.Code != http.StatusOK {
		t.Fatalf("setup failed: %s", rec.Body.String())
	}

	// The user removes the linked folder from the generic directory UI.
	store.workspaces["ws-1"].DirectoryReferences = nil

	rec, body := serve(t, h, http.MethodPost, "/api/workspaces/ws-1/downloads-janitor/scan", "")
	if rec.Code == http.StatusInternalServerError {
		t.Fatalf("an unlinked folder is recoverable, not an internal error: %s", rec.Body.String())
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	apiErr, _ := body["error"].(map[string]any)
	if apiErr == nil {
		apiErr = body
	}
	if apiErr["code"] != "folder_unavailable" {
		t.Fatalf("code = %v", apiErr["code"])
	}
	message, _ := apiErr["message"].(string)
	if !strings.Contains(strings.ToLower(message), "reconnect") {
		t.Fatalf("the message must tell the user what to do: %q", message)
	}

	// And readiness says the same thing, with a repair.
	_, body = serve(t, h, http.MethodGet, "/api/workspaces/ws-1/downloads-janitor/readiness", "")
	readiness, _ := body["readiness"].(map[string]any)
	if readiness["state"] != "needs_attention" {
		t.Fatalf("readiness = %v", readiness["state"])
	}
}
