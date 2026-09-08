package workspace

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func setupProjectOpenTest(t *testing.T) (*FileStore, *Workspace, *HTTPHandler, string) {
	t.Helper()
	store, ws, handler := newFolderHandlerTest(t, "ws-project-open", "Project Open")
	workspaceRoot, err := store.GetFolderPath(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	projectRoot := filepath.Join(workspaceRoot, "song")
	if err := os.MkdirAll(projectRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	entryPath := filepath.Join(projectRoot, "song.rpp")
	if err := os.WriteFile(entryPath, []byte("project"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ws.ID, func(stored *Workspace) error {
		stored.ProjectPath = "song"
		if stored.SharedData == nil {
			stored.SharedData = make(map[string]any)
		}
		return SetProjectEntryPath(stored.SharedData, "song.rpp")
	}); err != nil {
		t.Fatal(err)
	}
	return store, ws, handler, entryPath
}

func localProjectOpenRequest(method, workspaceID string, body *bytes.Reader) *http.Request {
	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, "/api/workspaces/"+workspaceID+"/project/open", nil)
	} else {
		req = httptest.NewRequest(method, "/api/workspaces/"+workspaceID+"/project/open", body)
	}
	req.RemoteAddr = "127.0.0.1:43210"
	req.SetPathValue("workspaceID", workspaceID)
	return req
}

func TestOpenWorkspaceProjectUsesPersistedEntry(t *testing.T) {
	_, ws, handler, wantPath := setupProjectOpenTest(t)
	var opened string
	handler.openFile = func(path string) error {
		opened = path
		return nil
	}

	req := localProjectOpenRequest(http.MethodPost, ws.ID, nil)
	rr := httptest.NewRecorder()
	handler.OpenWorkspaceProject(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if filepath.Clean(opened) != filepath.Clean(wantPath) {
		t.Fatalf("opener path = %q, want %q", opened, wantPath)
	}
	if !strings.Contains(rr.Body.String(), `"path":"song.rpp"`) {
		t.Fatalf("response missing portable path: %s", rr.Body.String())
	}
}

func TestOpenWorkspaceProjectResolvesExactDirectoryReferenceWithoutExposingAbsolutePath(t *testing.T) {
	store, ws, handler := newFolderHandlerTest(t, "ws-external-project-open", "External Project Open")
	externalRoot := t.TempDir()
	entryPath := filepath.Join(externalRoot, "Existing Song.RPP")
	if err := os.WriteFile(entryPath, []byte("project"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ws.ID, func(stored *Workspace) error {
		if err := stored.AddDirectoryReference(DirectoryReference{ID: "external-project", Name: "Existing", Path: externalRoot}); err != nil {
			return err
		}
		return SetProjectEntryLocator(stored.SharedData, ProjectEntryLocator{
			SchemaVersion: ProjectEntryLocatorSchemaVersion,
			Kind:          ProjectEntryDirectoryReference, DirectoryReferenceID: "external-project",
			RelativePath: "Existing Song.RPP",
		})
	}); err != nil {
		t.Fatal(err)
	}
	var opened string
	handler.openFile = func(path string) error { opened = path; return nil }
	recorder := httptest.NewRecorder()
	handler.OpenWorkspaceProject(recorder, localProjectOpenRequest(http.MethodPost, ws.ID, nil))
	if recorder.Code != http.StatusOK || opened != entryPath {
		t.Fatalf("external open = status %d path %q body %s", recorder.Code, opened, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), externalRoot) || !strings.Contains(recorder.Body.String(), `"relative_path":"Existing Song.RPP"`) {
		t.Fatalf("external open response leaked or omitted locator data: %s", recorder.Body.String())
	}
}

func TestOpenWorkspaceProjectReadsCanonicalFolderMetadata(t *testing.T) {
	primary := NewInMemoryStore()
	fileStore, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fileStore.Close() })

	primaryWS := newTestWorkspace("ws-canonical-project-open", "Canonical Project Open")
	primaryWS.ProjectPath = "stale-project"
	primaryWS.SharedData[ProjectEntryPathKey] = "stale.rpp"
	if err := primary.Save(primaryWS); err != nil {
		t.Fatal(err)
	}

	canonicalWS := newTestWorkspace(primaryWS.ID, primaryWS.Name)
	canonicalWS.ProjectPath = "song"
	canonicalWS.SharedData[ProjectEntryPathKey] = "song.rpp"
	if err := fileStore.Save(canonicalWS); err != nil {
		t.Fatal(err)
	}
	workspaceRoot, err := fileStore.GetFolderPath(canonicalWS.ID)
	if err != nil {
		t.Fatal(err)
	}
	projectRoot := filepath.Join(workspaceRoot, "song")
	if err := os.MkdirAll(projectRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(projectRoot, "song.rpp")
	if err := os.WriteFile(wantPath, []byte("project"), 0o640); err != nil {
		t.Fatal(err)
	}

	handler := NewHTTPHandler(NewSyncStore(primary, fileStore), nil, nil)
	var opened string
	handler.openFile = func(path string) error { opened = path; return nil }
	rr := httptest.NewRecorder()
	handler.OpenWorkspaceProject(rr, localProjectOpenRequest(http.MethodPost, canonicalWS.ID, nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected canonical metadata open to return 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if filepath.Clean(opened) != filepath.Clean(wantPath) {
		t.Fatalf("opened %q from stale mirror, want canonical %q", opened, wantPath)
	}
}

func TestOpenWorkspaceProjectAllowsIPv6Loopback(t *testing.T) {
	_, ws, handler, _ := setupProjectOpenTest(t)
	called := false
	handler.openFile = func(string) error { called = true; return nil }
	req := localProjectOpenRequest(http.MethodPost, ws.ID, nil)
	req.RemoteAddr = "[::1]:43210"
	rr := httptest.NewRecorder()
	handler.OpenWorkspaceProject(rr, req)
	if rr.Code != http.StatusOK || !called {
		t.Fatalf("IPv6 loopback expected 200/call, got %d called=%v: %s", rr.Code, called, rr.Body.String())
	}
}

func TestOpenWorkspaceProjectRejectsMethodAndBody(t *testing.T) {
	_, ws, handler, _ := setupProjectOpenTest(t)
	called := false
	handler.openFile = func(string) error { called = true; return nil }

	// Method rejection (GET, etc.) is enforced by ServeMux (POST-only pattern),
	// covered by the server golden route-table test.
	var rr *httptest.ResponseRecorder

	for name, body := range map[string][]byte{
		"caller path": []byte(`{"path":"outside.rpp"}`),
		"whitespace":  []byte(" \n\t"),
		"padded path": append(bytes.Repeat([]byte(" "), 2048), []byte(`{"path":"outside.rpp"}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			called = false
			rr = httptest.NewRecorder()
			handler.OpenWorkspaceProject(rr, localProjectOpenRequest(http.MethodPost, ws.ID, bytes.NewReader(body)))
			if rr.Code != http.StatusBadRequest || called {
				t.Fatalf("body expected 400/no open, got %d called=%v: %s", rr.Code, called, rr.Body.String())
			}
		})
	}
}

func TestOpenWorkspaceProjectRejectsRemotePeersAndForwarding(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
	}{
		{name: "remote ipv4", remoteAddr: "192.168.1.44:1234"},
		{name: "remote ipv6", remoteAddr: "[2001:db8::44]:1234"},
		{name: "spoofed loopback forwarding", remoteAddr: "192.168.1.44:1234", headers: map[string]string{"X-Forwarded-For": "127.0.0.1"}},
		{name: "proxy remote xff", remoteAddr: "127.0.0.1:1234", headers: map[string]string{"X-Forwarded-For": "203.0.113.5"}},
		{name: "proxy remote real ip", remoteAddr: "127.0.0.1:1234", headers: map[string]string{"X-Real-IP": "203.0.113.5"}},
		{name: "proxy remote forwarded", remoteAddr: "127.0.0.1:1234", headers: map[string]string{"Forwarded": "for=203.0.113.5;proto=https"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ws, handler, _ := setupProjectOpenTest(t)
			called := false
			handler.openFile = func(string) error { called = true; return nil }
			req := localProjectOpenRequest(http.MethodPost, ws.ID, nil)
			req.RemoteAddr = tt.remoteAddr
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}
			rr := httptest.NewRecorder()
			handler.OpenWorkspaceProject(rr, req)
			if rr.Code != http.StatusForbidden || called {
				t.Fatalf("expected 403/no open, got %d called=%v: %s", rr.Code, called, rr.Body.String())
			}
		})
	}
}

func TestOpenWorkspaceProjectRejectsMissingAndMalformedMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Workspace)
		status int
	}{
		{name: "no project", mutate: func(ws *Workspace) { ws.ProjectPath = "" }, status: http.StatusNotFound},
		{name: "project traversal", mutate: func(ws *Workspace) { ws.ProjectPath = "../outside" }, status: http.StatusBadRequest},
		{name: "no entry", mutate: func(ws *Workspace) { ClearProjectEntryPath(ws.SharedData) }, status: http.StatusNotFound},
		{name: "entry traversal", mutate: func(ws *Workspace) { ws.SharedData[ProjectEntryPathKey] = "../outside.rpp" }, status: http.StatusBadRequest},
		{name: "entry absolute", mutate: func(ws *Workspace) { ws.SharedData[ProjectEntryPathKey] = "/tmp/outside.rpp" }, status: http.StatusBadRequest},
		{name: "entry wrong type", mutate: func(ws *Workspace) { ws.SharedData[ProjectEntryPathKey] = 123 }, status: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, ws, handler, _ := setupProjectOpenTest(t)
			if err := store.Update(ws.ID, func(stored *Workspace) error { tt.mutate(stored); return nil }); err != nil {
				t.Fatal(err)
			}
			called := false
			handler.openFile = func(string) error { called = true; return nil }
			rr := httptest.NewRecorder()
			handler.OpenWorkspaceProject(rr, localProjectOpenRequest(http.MethodPost, ws.ID, nil))
			if rr.Code != tt.status || called {
				t.Fatalf("expected %d/no open, got %d called=%v: %s", tt.status, rr.Code, called, rr.Body.String())
			}
		})
	}
}

func TestOpenWorkspaceProjectRejectsUnsafeFilesystemTargets(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, store *FileStore, ws *Workspace, entryPath string)
		status int
	}{
		{name: "missing file", status: http.StatusNotFound, mutate: func(t *testing.T, _ *FileStore, _ *Workspace, entryPath string) {
			if err := os.Remove(entryPath); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "entry is directory", status: http.StatusBadRequest, mutate: func(t *testing.T, _ *FileStore, _ *Workspace, entryPath string) {
			if err := os.Remove(entryPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(entryPath, 0o750); err != nil {
				t.Fatal(err)
			}
		}},
	}
	if runtime.GOOS != "windows" {
		tests = append(tests,
			struct {
				name   string
				mutate func(*testing.T, *FileStore, *Workspace, string)
				status int
			}{name: "entry symlink", status: http.StatusBadRequest, mutate: func(t *testing.T, _ *FileStore, _ *Workspace, entryPath string) {
				outside := filepath.Join(t.TempDir(), "outside.rpp")
				if err := os.WriteFile(outside, []byte("outside"), 0o640); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(entryPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, entryPath); err != nil {
					t.Fatal(err)
				}
			}},
			struct {
				name   string
				mutate func(*testing.T, *FileStore, *Workspace, string)
				status int
			}{name: "project symlink", status: http.StatusBadRequest, mutate: func(t *testing.T, _ *FileStore, _ *Workspace, entryPath string) {
				projectRoot := filepath.Dir(entryPath)
				outside := t.TempDir()
				if err := os.WriteFile(filepath.Join(outside, "song.rpp"), []byte("outside"), 0o640); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(projectRoot); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, projectRoot); err != nil {
					t.Fatal(err)
				}
			}},
			struct {
				name   string
				mutate func(*testing.T, *FileStore, *Workspace, string)
				status int
			}{name: "parent symlink", status: http.StatusBadRequest, mutate: func(t *testing.T, store *FileStore, ws *Workspace, entryPath string) {
				outside := t.TempDir()
				if err := os.WriteFile(filepath.Join(outside, "song.rpp"), []byte("outside"), 0o640); err != nil {
					t.Fatal(err)
				}
				link := filepath.Join(filepath.Dir(entryPath), "linked")
				if err := os.Symlink(outside, link); err != nil {
					t.Fatal(err)
				}
				if err := store.Update(ws.ID, func(stored *Workspace) error {
					stored.SharedData[ProjectEntryPathKey] = "linked/song.rpp"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}},
		)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, ws, handler, entryPath := setupProjectOpenTest(t)
			tt.mutate(t, store, ws, entryPath)
			called := false
			handler.openFile = func(string) error { called = true; return nil }
			rr := httptest.NewRecorder()
			handler.OpenWorkspaceProject(rr, localProjectOpenRequest(http.MethodPost, ws.ID, nil))
			if rr.Code != tt.status || called {
				t.Fatalf("expected %d/no open, got %d called=%v: %s", tt.status, rr.Code, called, rr.Body.String())
			}
		})
	}
}

func TestOpenWorkspaceProjectOpenerFailureDoesNotMutateWorkspace(t *testing.T) {
	store, ws, handler, _ := setupProjectOpenTest(t)
	handler.openFile = func(string) error { return errors.New("no file association") }
	rr := httptest.NewRecorder()
	handler.OpenWorkspaceProject(rr, localProjectOpenRequest(http.MethodPost, ws.ID, nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
	stored, err := store.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ProjectPath != "song" || stored.SharedData[ProjectEntryPathKey] != "song.rpp" {
		t.Fatalf("opener failure mutated workspace: %+v", stored)
	}
}

func TestOpenWorkspaceProjectRequiresFolderResolver(t *testing.T) {
	store := NewInMemoryStore()
	ws := newTestWorkspace("ws-no-folder-resolver", "No Resolver")
	ws.ProjectPath = "song"
	ws.SharedData[ProjectEntryPathKey] = "song.rpp"
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(store, nil, nil)
	handler.openFile = func(string) error { t.Fatal("opener must not be called"); return nil }
	rr := httptest.NewRecorder()
	handler.OpenWorkspaceProject(rr, localProjectOpenRequest(http.MethodPost, ws.ID, nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
}
