package filejanitorhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/platform"
)

func TestReviewInspectionSelectsOnlyTheUnchangedApprovedFile(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Finder is macOS only")
	}
	t.Setenv(platform.NoDesktopOpenEnv, "0")
	h, root := pictureHandler(t, []pictureFile{{"one.png", 10}, {"notes.zip", 20}})
	picture, err := h.service.ReadReviewPicture(t.Context(), "local", "ws-1", "latest")
	if err != nil {
		t.Fatal(err)
	}
	_, candidates, err := h.service.BatchDetail("ws-1", picture.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]string)
	for _, candidate := range candidates {
		ids[candidate.Name] = candidate.ID
	}
	id := ids["one.png"]
	if id == "" || ids["notes.zip"] == "" {
		t.Fatal("missing fixture candidate")
	}
	mux := http.NewServeMux()
	h.Register(mux)
	var reveals, opened []string
	h.openDocument = func(p string) error { opened = append(opened, p); return nil }
	h.revealFile = func(p string) error { reveals = append(reveals, p); return nil }
	inspect := func(workspace, batch, revision, route, origin, peer, action, id string, want int) {
		t.Helper()
		r := httptest.NewRequest("POST", "http://localhost"+pathFor("file-janitor", workspace, "/batches/"+batch+"/picture/files/"+id+"/"+route+"?revision="+revision), nil)
		r.RemoteAddr = peer
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Ori-Filing-Action", action)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		if rec.Code != want || strings.Contains(rec.Body.String(), root) {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
	}
	const local = "127.0.0.1:18000"
	request := func(route, origin, peer, action, id string, want int) {
		t.Helper()
		inspect("ws-1", picture.BatchID, picture.Revision, route, origin, peer, action, id, want)
	}
	readPage := func(batch, peer string, offset string, forwarded bool) (string, []candidateDTO) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "http://localhost"+pathFor("file-janitor", "ws-1", "/batches/"+batch+"?limit=1&offset="+offset), nil)
		r.RemoteAddr = peer
		if forwarded {
			r.Header.Set("X-Forwarded-For", local)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("page status %d: %s", rec.Code, rec.Body)
		}
		var body struct {
			Revision   string         `json:"inspection_revision"`
			Candidates []candidateDTO `json:"candidates"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Candidates) != 1 {
			t.Fatalf("page: %s (%v)", rec.Body, err)
		}
		return body.Revision, body.Candidates
	}
	viewers := map[string]string{}
	for offset, batch := range map[string]string{"0": "latest", "1": picture.BatchID} {
		revision, page := readPage(batch, local, offset, false)
		if revision != picture.Revision || !page[0].CanInspect {
			t.Fatalf("unbound page %s: %q %+v", offset, revision, page[0])
		}
		viewers[page[0].ID] = page[0].Viewer
	}
	if viewer, found := viewers[ids["notes.zip"]]; !found || viewer != "" || viewers[id] != "Preview" {
		t.Fatalf("expected Preview and one Finder-only file: %+v", viewers)
	}
	for _, mode := range []struct {
		peer      string
		forwarded bool
	}{
		{"192.0.2.1:1234", false}, {local, true},
	} {
		revision, page := readPage(picture.BatchID, mode.peer, "0", mode.forwarded)
		if revision != "" || page[0].CanInspect || page[0].Viewer != "" {
			t.Fatalf("nonlocal client received inspection metadata: %q %+v", revision, page[0])
		}
	}
	t.Setenv(platform.NoDesktopOpenEnv, "1")
	revision, page := readPage(picture.BatchID, local, "0", false)
	if revision != "" || page[0].CanInspect || page[0].Viewer != "" {
		t.Fatalf("desktop disabled but inspection advertised: %q %+v", revision, page[0])
	}
	t.Setenv(platform.NoDesktopOpenEnv, "0")

	for _, action := range []string{"open", "reveal"} {
		request(action, "http://localhost", local, action, id, 200)
		request(action, "http://localhost:9001", local, action, id, 403)
		request(action, "", local, action, id, 403)
		request(action, "http://localhost", local, "", id, 403)
		request(action, "http://localhost", "192.0.2.1:1234", action, id, 403)
		request(action, "http://localhost", local, action, "unknown", 409)
	}
	request("open", "http://localhost", local, "reveal", id, 403)
	request("open", "http://localhost", local, "open", ids["notes.zip"], 400)
	inspect("ws-1", picture.BatchID, "older-revision", "open", "http://localhost", local, "open", id, 409)
	inspect("ws-1", "latest", picture.Revision, "open", "http://localhost", local, "open", id, 404)
	inspect("other-workspace", picture.BatchID, picture.Revision, "open", "http://localhost", local, "open", id, 404)
	path := filepath.Join(root, "one.png")
	if err := os.WriteFile(path, []byte("a changed file"), 0600); err != nil {
		t.Fatal(err)
	}
	request("reveal", "http://localhost", local, "reveal", id, 409)
	request("open", "http://localhost", local, "open", id, 409)
	if len(opened) != 1 || opened[0] != path {
		t.Fatalf("unexpected native opens: %q", opened)
	}
	if len(reveals) != 1 || reveals[0] != path {
		t.Fatalf("unexpected native reveals: %q", reveals)
	}
}
