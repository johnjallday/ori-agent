package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// outputsSecret is what the files outside the outputs folder hold. No refused
// request may ever answer with it.
const outputsSecret = "outside the outputs folder"

// newOutputsHandlerTest returns a store holding one workspace, and the real
// route table, so every request below is matched the way the server matches it.
func newOutputsHandlerTest(t *testing.T, id, name string) (*FileStore, *Workspace, *HTTPHandler, *http.ServeMux) {
	t.Helper()
	store, ws, handler := newFolderHandlerTest(t, id, name)
	mux := http.NewServeMux()
	RegisterRoutes(mux, handler)
	return store, ws, handler, mux
}

func writeOutputFile(t *testing.T, store *FileStore, workspaceID, relativePath, content string) {
	t.Helper()
	full := filepath.Join(store.GetOutputsPath(workspaceID), filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatalf("MkdirAll %s: %v", relativePath, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", relativePath, err)
	}
}

// linkOutsideOutputs puts a file and a folder outside the outputs folder and
// links to each from inside it: `link.txt` and `escape/`.
func linkOutsideOutputs(t *testing.T, store *FileStore, workspaceID string) {
	t.Helper()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte(outputsSecret), 0o600); err != nil {
		t.Fatalf("WriteFile outside: %v", err)
	}
	outputs := store.GetOutputsPath(workspaceID)
	if err := os.MkdirAll(outputs, 0o750); err != nil {
		t.Fatalf("MkdirAll outputs: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(outputs, "link.txt")); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(outputs, "escape")); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}
}

func getOutputs(mux *http.ServeMux, target string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))
	return rr
}

func TestGetWorkspaceOutputsTreeListsNestedFiles(t *testing.T) {
	store, ws, _, mux := newOutputsHandlerTest(t, "ws-outputs-tree", "Outputs Tree")
	writeOutputFile(t, store, ws.ID, "report.md", "# Report")
	writeOutputFile(t, store, ws.ID, "charts/notes.txt", "notes")
	writeOutputFile(t, store, ws.ID, "charts/2026/q3 results.csv", "a,b\n1,2\n")
	writeOutputFile(t, store, ws.ID, ".DS_Store", "hidden")
	writeOutputFile(t, store, ws.ID, ".ori/state.json", "{}")
	writeOutputFile(t, store, ws.ID, "charts/.cache/data.json", "{}")
	linkOutsideOutputs(t, store, ws.ID)

	rr := getOutputs(mux, "/api/workspaces/"+ws.ID+"/outputs/tree")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var response struct {
		Files     []FileInfo `json:"files"`
		Workspace string     `json:"workspace"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Workspace != ws.ID {
		t.Fatalf("expected workspace %q, got %q", ws.ID, response.Workspace)
	}

	want := []struct {
		path  string
		isDir bool
	}{
		{"charts", true},
		{filepath.Join("charts", "2026"), true},
		{filepath.Join("charts", "2026", "q3 results.csv"), false},
		{filepath.Join("charts", "notes.txt"), false},
		{"report.md", false},
	}
	if len(response.Files) != len(want) {
		t.Fatalf("expected %d entries, got %d: %#v", len(want), len(response.Files), response.Files)
	}
	for i, entry := range want {
		got := response.Files[i]
		if got.RelativePath != entry.path || got.IsDir != entry.isDir {
			t.Fatalf("entry %d: expected %q (is_dir=%v), got %q (is_dir=%v)", i, entry.path, entry.isDir, got.RelativePath, got.IsDir)
		}
		if got.Name != filepath.Base(entry.path) {
			t.Fatalf("entry %d: expected name %q, got %q", i, filepath.Base(entry.path), got.Name)
		}
		if got.Source != workspaceOutputSource {
			t.Fatalf("entry %d: expected source %q, got %q", i, workspaceOutputSource, got.Source)
		}
		if got.ModTime.IsZero() {
			t.Fatalf("entry %d: expected a modification time", i)
		}
	}

	csv := findFileInfo(response.Files, filepath.Join("charts", "2026", "q3 results.csv"))
	if csv.Size != int64(len("a,b\n1,2\n")) {
		t.Fatalf("expected the file's size, got %d", csv.Size)
	}
	if wantURL := "/api/workspaces/" + ws.ID + "/outputs/charts/2026/q3%20results.csv"; csv.URL != wantURL {
		t.Fatalf("expected URL %q, got %q", wantURL, csv.URL)
	}
	// The address the listing gives is the address the file is read from.
	if file := getOutputs(mux, csv.URL); file.Code != http.StatusOK || file.Body.String() != "a,b\n1,2\n" {
		t.Fatalf("expected the listed URL to serve the file, got %d: %q", file.Code, file.Body.String())
	}
}

func TestGetWorkspaceOutputsTreeWithoutOutputs(t *testing.T) {
	tests := []struct {
		name       string
		prepare    func(t *testing.T, store *FileStore, ws *Workspace)
		workspace  func(ws *Workspace) string
		wantStatus int
	}{
		{
			name:       "missing folder",
			prepare:    func(*testing.T, *FileStore, *Workspace) {},
			workspace:  func(ws *Workspace) string { return ws.ID },
			wantStatus: http.StatusOK,
		},
		{
			name: "empty folder",
			prepare: func(t *testing.T, store *FileStore, ws *Workspace) {
				if err := os.MkdirAll(store.GetOutputsPath(ws.ID), 0o750); err != nil {
					t.Fatalf("MkdirAll: %v", err)
				}
			},
			workspace:  func(ws *Workspace) string { return ws.ID },
			wantStatus: http.StatusOK,
		},
		{
			name: "only hidden files",
			prepare: func(t *testing.T, store *FileStore, ws *Workspace) {
				writeOutputFile(t, store, ws.ID, ".DS_Store", "hidden")
			},
			workspace:  func(ws *Workspace) string { return ws.ID },
			wantStatus: http.StatusOK,
		},
		{
			name:       "unknown workspace",
			prepare:    func(*testing.T, *FileStore, *Workspace) {},
			workspace:  func(*Workspace) string { return "no-such-workspace" },
			wantStatus: http.StatusNotFound,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store, ws, _, mux := newOutputsHandlerTest(t, "ws-outputs-none", "Outputs None")
			tc.prepare(t, store, ws)
			_, existedBefore := os.Stat(store.GetOutputsPath(ws.ID))

			rr := getOutputs(mux, "/api/workspaces/"+tc.workspace(ws)+"/outputs/tree")
			if rr.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rr.Code, rr.Body.String())
			}
			if tc.wantStatus == http.StatusOK && !strings.Contains(rr.Body.String(), `"files":[]`) {
				t.Fatalf("expected an empty list, not null, got %s", rr.Body.String())
			}
			// A read never creates the folder.
			if _, existsAfter := os.Stat(store.GetOutputsPath(ws.ID)); os.IsNotExist(existedBefore) != os.IsNotExist(existsAfter) {
				t.Fatalf("listing changed whether the outputs folder exists")
			}
		})
	}
}

func TestServeWorkspaceOutputFile(t *testing.T) {
	store, ws, handler, mux := newOutputsHandlerTest(t, "ws-outputs-read", "Outputs Read")
	writeOutputFile(t, store, ws.ID, "report.md", "# Report")
	writeOutputFile(t, store, ws.ID, "charts/2026/q3 results.csv", "a,b\n1,2\n")
	writeOutputFile(t, store, ws.ID, "100%2e.txt", "percent")
	writeOutputFile(t, store, ws.ID, ".env", outputsSecret)
	writeOutputFile(t, store, ws.ID, ".ori/state.json", outputsSecret)
	writeOutputFile(t, store, ws.ID, "charts/.cache/data.json", outputsSecret)
	linkOutsideOutputs(t, store, ws.ID)
	// What `..` would reach: the workspace's own settings file, one folder up.
	if _, err := os.Stat(filepath.Join(filepath.Dir(store.GetOutputsPath(ws.ID)), WorkspaceConfigFile)); err != nil {
		t.Fatalf("expected workspace.json beside the outputs folder: %v", err)
	}

	tests := []struct {
		name string
		// path is the wildcard as the handler receives it. Left empty, the
		// request goes through the route table as target instead.
		path       string
		target     string
		wantStatus int
		wantBody   string
	}{
		{name: "file at the top", target: "/outputs/report.md", wantStatus: http.StatusOK, wantBody: "# Report"},
		{name: "nested file with a space", target: "/outputs/charts/2026/q3%20results.csv", wantStatus: http.StatusOK, wantBody: "a,b\n1,2\n"},
		{name: "percent sign in a name is not decoded twice", target: "/outputs/100%252e.txt", wantStatus: http.StatusOK, wantBody: "percent"},

		{name: "parent folder", path: "../workspace.json", wantStatus: http.StatusBadRequest},
		{name: "parent folder from a sub-folder", path: "charts/../../workspace.json", wantStatus: http.StatusBadRequest},
		{name: "dot-dot that would cancel out", path: "charts/../report.md", wantStatus: http.StatusBadRequest},
		{name: "encoded dot-dot", target: "/outputs/%2e%2e/workspace.json", wantStatus: http.StatusBadRequest},
		{name: "encoded dot-dot and slash", target: "/outputs/%2e%2e%2fworkspace.json", wantStatus: http.StatusBadRequest},
		{name: "absolute path", path: "/etc/hosts", wantStatus: http.StatusBadRequest},
		{name: "encoded absolute path", target: "/outputs/%2fetc%2fhosts", wantStatus: http.StatusBadRequest},
		{name: "empty path", path: " ", wantStatus: http.StatusBadRequest},

		{name: "link to a file outside", target: "/outputs/link.txt", wantStatus: http.StatusBadRequest},
		{name: "file through a linked folder", target: "/outputs/escape/secret.txt", wantStatus: http.StatusBadRequest},

		{name: "hidden file", target: "/outputs/.env", wantStatus: http.StatusNotFound},
		{name: "file in an internal folder", target: "/outputs/.ori/state.json", wantStatus: http.StatusNotFound},
		{name: "file in a nested hidden folder", target: "/outputs/charts/.cache/data.json", wantStatus: http.StatusNotFound},
		{name: "hidden file that does not exist", target: "/outputs/.missing", wantStatus: http.StatusNotFound},

		{name: "directory", target: "/outputs/charts", wantStatus: http.StatusNotFound},
		{name: "nested directory", target: "/outputs/charts/2026", wantStatus: http.StatusNotFound},
		{name: "missing file", target: "/outputs/nope.txt", wantStatus: http.StatusNotFound},
		{name: "missing file in a missing folder", target: "/outputs/nope/nope.txt", wantStatus: http.StatusNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			if tc.target != "" {
				mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+tc.target, nil))
			} else {
				req := httptest.NewRequest(http.MethodGet, "/api/workspaces/"+ws.ID+"/outputs/x", nil)
				req.SetPathValue("workspaceID", ws.ID)
				req.SetPathValue("relativePath", tc.path)
				handler.ServeWorkspaceOutputFile(rr, req)
			}
			if rr.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rr.Code, rr.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				if rr.Body.String() != tc.wantBody {
					t.Fatalf("expected body %q, got %q", tc.wantBody, rr.Body.String())
				}
				return
			}
			body := rr.Body.String()
			if strings.Contains(body, outputsSecret) || strings.Contains(body, ws.Name) {
				t.Fatalf("a refused request answered with file content: %q", body)
			}
		})
	}
}

// A literal `..` never reaches the handler: the route table tidies the path
// first, and what is left is not an outputs address.
func TestServeWorkspaceOutputFileLiteralDotDotIsNotServed(t *testing.T) {
	_, ws, _, mux := newOutputsHandlerTest(t, "ws-outputs-dotdot", "Outputs DotDot")

	rr := getOutputs(mux, "/api/workspaces/"+ws.ID+"/outputs/../workspace.json")
	if rr.Code == http.StatusOK {
		t.Fatalf("expected the request not to be served, got 200: %s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), ws.Name) {
		t.Fatalf("expected workspace.json not to be served, got %q", rr.Body.String())
	}
}

func TestServeWorkspaceOutputFileWithoutOutputs(t *testing.T) {
	store, ws, _, mux := newOutputsHandlerTest(t, "ws-outputs-missing", "Outputs Missing")

	if rr := getOutputs(mux, "/api/workspaces/"+ws.ID+"/outputs/report.md"); rr.Code != http.StatusNotFound {
		t.Fatalf("missing outputs folder: expected status 404, got %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(store.GetOutputsPath(ws.ID)); !os.IsNotExist(err) {
		t.Fatalf("expected a read not to create the outputs folder, stat error: %v", err)
	}
	if rr := getOutputs(mux, "/api/workspaces/no-such-workspace/outputs/report.md"); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown workspace: expected status 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// A group is a workspace of kind "group" with an outputs folder of its own;
// both routes answer its id as they answer a workspace's.
func TestWorkspaceOutputsAcceptAGroup(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	group := newTestWorkspace("group-outputs", "Group Outputs")
	group.Kind = groupWorkspaceKind
	if err := store.Save(group); err != nil {
		t.Fatalf("Save group: %v", err)
	}
	mux := http.NewServeMux()
	RegisterRoutes(mux, NewHTTPHandler(store, nil, nil))
	writeOutputFile(t, store, group.ID, "summary/weekly.md", "# Weekly")

	tree := getOutputs(mux, "/api/workspaces/"+group.ID+"/outputs/tree")
	if tree.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", tree.Code, tree.Body.String())
	}
	files := decodeFileTreeResponse(t, tree.Body.Bytes())
	assertFileInfo(t, files, "summary", true)
	assertFileInfo(t, files, filepath.Join("summary", "weekly.md"), false)

	file := getOutputs(mux, "/api/workspaces/"+group.ID+"/outputs/summary/weekly.md")
	if file.Code != http.StatusOK || file.Body.String() != "# Weekly" {
		t.Fatalf("expected the group's output, got %d: %q", file.Code, file.Body.String())
	}
}

func TestServeWorkspaceOutputFileNeverServesSomethingABrowserRuns(t *testing.T) {
	store, ws, _, mux := newOutputsHandlerTest(t, "ws-outputs-types", "Outputs Types")
	page := "<script>alert(1)</script>"
	writeOutputFile(t, store, ws.ID, "page.html", page)
	writeOutputFile(t, store, ws.ID, "page.HTM", page)
	writeOutputFile(t, store, ws.ID, "feed.xml", "<feed/>")
	writeOutputFile(t, store, ws.ID, "run.js", "alert(1)")
	writeOutputFile(t, store, ws.ID, "chart.svg", "<svg xmlns=\"http://www.w3.org/2000/svg\"/>")
	writeOutputFile(t, store, ws.ID, "photo.png", "\x89PNG\r\n\x1a\n")
	writeOutputFile(t, store, ws.ID, "data.json", "{}")
	writeOutputFile(t, store, ws.ID, "mix.unknownext", "<html><script>alert(1)</script></html>")

	tests := []struct {
		file     string
		wantType string
	}{
		{"page.html", "text/plain; charset=utf-8"},
		{"page.HTM", "text/plain; charset=utf-8"},
		{"feed.xml", "text/plain; charset=utf-8"},
		{"run.js", "text/plain; charset=utf-8"},
		{"chart.svg", "image/svg+xml"},
		{"photo.png", "image/png"},
		{"data.json", "application/json"},
		// No known type: sent as bytes, and never sniffed into a page.
		{"mix.unknownext", "application/octet-stream"},
	}
	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			rr := getOutputs(mux, "/api/workspaces/"+ws.ID+"/outputs/"+tc.file)
			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d: %s", rr.Code, rr.Body.String())
			}
			if got := rr.Header().Get("Content-Type"); got != tc.wantType {
				t.Fatalf("expected Content-Type %q, got %q", tc.wantType, got)
			}
			if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("expected nosniff, got %q", got)
			}
			if got := rr.Header().Get("Content-Security-Policy"); !strings.Contains(got, "sandbox") || !strings.Contains(got, "default-src 'none'") {
				t.Fatalf("expected a sandboxing policy, got %q", got)
			}
		})
	}
}
