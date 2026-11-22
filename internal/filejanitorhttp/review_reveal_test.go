package filejanitorhttp

import (
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
	request := func(route, origin, peer, action, id string, want int) {
		t.Helper()
		r := httptest.NewRequest("POST", "http://localhost"+pathFor("file-janitor", "ws-1", "/batches/"+picture.BatchID+"/picture/files/"+id+"/"+route+"?revision="+picture.Revision), nil)
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
