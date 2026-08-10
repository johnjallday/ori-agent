package agenthttp

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/types"
)

// A 1x1 PNG. Content sniffing is what decides the type, so the bytes have to be
// a real image rather than a plausible filename (FR-63).
var pngBytes = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
	0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
	0x42, 0x60, 0x82,
}

// A GIF header, used to prove that replacing an image with a different format
// lands at a different filename and cleans up the old file.
var gifBytes = append([]byte("GIF89a"), bytes.Repeat([]byte{0x00}, 32)...)

func uploadRequest(t *testing.T, agentName string, body []byte, fields map[string]string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field: %v", err)
		}
	}
	if body != nil {
		part, err := mw.CreateFormFile("image", "whatever.png")
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write(body); err != nil {
			t.Fatalf("write body: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	// Agent names may legally contain spaces, which are not legal in a request
	// line — so the segment is escaped here, exercising the handler's own
	// decoding rather than sidestepping it.
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/agents/"+url.PathEscape(agentName)+"/appearance/upload",
		&buf,
	)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

// isolateAvatarDir points uploads at a temp directory for the duration of one
// test, so no test writes into the package directory.
func isolateAvatarDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	return dir
}

func uploadHandler(t *testing.T, ts *TestServer) *AppearanceUploadHandler {
	t.Helper()
	return NewAppearanceUploadHandler(ts.store, nil)
}

func TestUploadActivatesTheImageInOneOperation(t *testing.T) {
	dir := isolateAvatarDir(t)
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "shooter")

	rec := httptest.NewRecorder()
	uploadHandler(t, ts).ServeHTTP(rec, uploadRequest(t, "shooter", pngBytes, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Appearance map[string]any `json:"appearance"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// The complete canonical object comes back, so an editor can adopt the
	// confirmed state without a second request (FR-59).
	if resp.Appearance["mode"] != "uploaded" {
		t.Fatalf("a successful upload must become the rendered source, got %v", resp.Appearance["mode"])
	}

	ag, _ := ts.store.GetAgent("shooter")
	if ag.Appearance.Mode != types.AppearanceModeUploaded {
		t.Errorf("stored mode = %q, want uploaded", ag.Appearance.Mode)
	}
	stored := ag.Appearance.UploadedImage()
	if stored != "shooter.png" {
		t.Errorf("stored filename = %q, want a server-generated name", stored)
	}
	if _, err := os.Stat(filepath.Join(dir, AvatarDir, stored)); err != nil {
		t.Errorf("the file was not written: %v", err)
	}
}

func TestUploadIgnoresTheClientFilename(t *testing.T) {
	isolateAvatarDir(t)
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "traverser")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// A traversal attempt in the client's filename must be irrelevant: the
	// server derives the name from the agent, never from the upload (FR-64).
	part, err := mw.CreateFormFile("image", "../../../etc/passwd.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(pngBytes); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/agents/traverser/appearance/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	rec := httptest.NewRecorder()
	uploadHandler(t, ts).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	ag, _ := ts.store.GetAgent("traverser")
	if got := ag.Appearance.UploadedImage(); got != "traverser.png" {
		t.Fatalf("stored filename = %q, want the server-generated name", got)
	}
}

func TestUploadRejectsNonImageContent(t *testing.T) {
	isolateAvatarDir(t)
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "liar")

	// An SVG is the case that matters most: it is an image by extension and a
	// script host by content, and the allowlist is content-sniffed precisely so
	// the declared type cannot get it in (FR-63).
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	rec := httptest.NewRecorder()
	uploadHandler(t, ts).ServeHTTP(rec, uploadRequest(t, "liar", svg, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	ag, _ := ts.store.GetAgent("liar")
	if ag.Appearance.UploadedImage() != "" || ag.Appearance.Mode != types.AppearanceModeGenerated {
		t.Fatalf("a rejected upload must not mutate the appearance: %+v", ag.Appearance)
	}
}

func TestUploadOnAMissingAgentDoesNotWriteAnything(t *testing.T) {
	dir := isolateAvatarDir(t)
	ts := setupTestServer(t)
	defer ts.cleanup()

	rec := httptest.NewRecorder()
	uploadHandler(t, ts).ServeHTTP(rec, uploadRequest(t, "ghost", pngBytes, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	if entries, err := os.ReadDir(filepath.Join(dir, AvatarDir)); err == nil && len(entries) > 0 {
		t.Fatalf("a rejected upload left files behind: %v", entries)
	}
}

func TestReplacingAnImageRemovesOnlyTheSupersededFile(t *testing.T) {
	dir := isolateAvatarDir(t)
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "swapper")
	h := uploadHandler(t, ts)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, uploadRequest(t, "swapper", pngBytes, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("first upload failed: %s", rec.Body.String())
	}

	// A different format lands at a different filename, which is the case where
	// the previous file would be orphaned if replacement did not clean up.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, uploadRequest(t, "swapper", gifBytes, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("replacement failed: %s", rec.Body.String())
	}

	ag, _ := ts.store.GetAgent("swapper")
	if got := ag.Appearance.UploadedImage(); got != "swapper.gif" {
		t.Fatalf("stored filename = %q, want swapper.gif", got)
	}
	if _, err := os.Stat(filepath.Join(dir, AvatarDir, "swapper.gif")); err != nil {
		t.Errorf("the replacement is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, AvatarDir, "swapper.png")); !os.IsNotExist(err) {
		t.Errorf("the superseded file was not cleaned up")
	}
	// No temporary artifact may survive a successful replacement.
	entries, _ := os.ReadDir(filepath.Join(dir, AvatarDir))
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("a temp artifact survived: %s", e.Name())
		}
	}
}

func TestDeleteReturnsToGeneratedOnlyWhenUploadWasActive(t *testing.T) {
	dir := isolateAvatarDir(t)
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "deleter")
	entry := assignableCharacter(t)
	h := uploadHandler(t, ts)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, uploadRequest(t, "deleter", pngBytes, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload failed: %s", rec.Body.String())
	}

	// Switch to Character so the upload is saved but inactive.
	rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=deleter", map[string]any{
		"appearance": map[string]any{"character": map[string]any{"catalog_id": string(entry.ID)}},
	})
	assertStatus(t, rr, http.StatusOK)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/agents/deleter/appearance/upload", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete failed: %s", rec.Body.String())
	}

	ag, _ := ts.store.GetAgent("deleter")
	// Deleting an inactive upload must leave the active mode — and the saved
	// character — alone (FR-39/FR-40).
	if ag.Appearance.Mode != types.AppearanceModeCharacter {
		t.Errorf("mode = %q, want the untouched character mode", ag.Appearance.Mode)
	}
	if ag.Appearance.CharacterCatalogID() != string(entry.ID) {
		t.Errorf("upload deletion discarded the character: %q", ag.Appearance.CharacterCatalogID())
	}
	if ag.Appearance.UploadedImage() != "" {
		t.Errorf("the upload reference survived deletion: %q", ag.Appearance.UploadedImage())
	}
	if _, err := os.Stat(filepath.Join(dir, AvatarDir, "deleter.png")); !os.IsNotExist(err) {
		t.Error("the file survived deletion")
	}

	// Deleting again is harmless.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/agents/deleter/appearance/upload", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat delete failed: %s", rec.Body.String())
	}
}

func TestDeleteOfAnActiveUploadFallsBackToGenerated(t *testing.T) {
	isolateAvatarDir(t)
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "active")
	h := uploadHandler(t, ts)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, uploadRequest(t, "active", pngBytes, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload failed: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/agents/active/appearance/upload", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("delete failed: %s", rec.Body.String())
	}

	ag, _ := ts.store.GetAgent("active")
	if ag.Appearance.Mode != types.AppearanceModeGenerated {
		t.Fatalf("mode = %q, want generated", ag.Appearance.Mode)
	}
}

func TestUploadHonoursTheStaleEditGuard(t *testing.T) {
	isolateAvatarDir(t)
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "guarded")

	// An upload is an appearance change like any other, so it must not be the
	// one path that can silently clobber a concurrent edit (FR-16/FR-42).
	rec := httptest.NewRecorder()
	uploadHandler(t, ts).ServeHTTP(rec, uploadRequest(t, "guarded", pngBytes, map[string]string{
		"expected_version": "not-the-current-version",
	}))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	ag, _ := ts.store.GetAgent("guarded")
	if ag.Appearance.UploadedImage() != "" {
		t.Fatal("a stale upload must be rejected before anything is written")
	}
}

func TestUploadAcceptsAMatchingVersion(t *testing.T) {
	isolateAvatarDir(t)
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "fresh")

	ag, _ := ts.store.GetAgent("fresh")
	rec := httptest.NewRecorder()
	uploadHandler(t, ts).ServeHTTP(rec, uploadRequest(t, "fresh", pngBytes, map[string]string{
		"expected_version": agentConfigVersion(ag),
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAppearanceUploadPathMatchingIsExact(t *testing.T) {
	cases := map[string]bool{
		"/api/agents/scout/appearance/upload":       true,
		"/api/agents/My%20Agent/appearance/upload":  true,
		"/api/agents/scout/avatar":                  false,
		"/api/agents/scout/appearance":              false,
		"/api/agents/scout/appearance/upload/extra": false,
		"/api/agents/a/b/appearance/upload":         false,
		"/api/agents//appearance/upload":            false,
		"/api/agents":                               false,
		// The substring test this replaces would have matched an agent whose
		// name merely contained the routed word (FR-60).
		"/api/agents/appearance-lab/detail": false,
	}
	for path, want := range cases {
		if got := IsAppearanceUploadPath(path); got != want {
			t.Errorf("IsAppearanceUploadPath(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestOldAvatarRouteNoLongerUploads(t *testing.T) {
	isolateAvatarDir(t)
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "legacy")

	// The old route is gone and must not proxy to the new one. Whatever the
	// generic agent dispatcher makes of the leftover path, the one thing it must
	// not do is store an image (FR-62).
	req := uploadRequest(t, "legacy", pngBytes, nil)
	req.URL.Path = "/api/agents/legacy/avatar"
	if IsAppearanceUploadPath(req.URL.Path) {
		t.Fatal("the old path must not route to the appearance upload handler")
	}

	ag, _ := ts.store.GetAgent("legacy")
	if ag.Appearance.UploadedImage() != "" {
		t.Fatal("the old route stored an image")
	}
}
