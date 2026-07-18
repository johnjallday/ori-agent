package fileshttp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/sessionfiles"
)

// TestRegisterRoutes_DispatchesToHandlers pins the pattern registration: it
// drives requests through a real ServeMux (exactly how the server wires these
// routes) and asserts each intended (method, path) reaches the correct handler.
//
// The server golden route-table test collapses handler output into a coarse
// routing class, so a same-class mis-route (one real route reaching a different
// but also-"handled" handler) would slip past it. This test closes that gap by
// asserting handler-distinctive behavior.
func TestRegisterRoutes_DispatchesToHandlers(t *testing.T) {
	store, err := sessionfiles.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	h := NewHandler(store, nil)

	mux := http.NewServeMux()
	RegisterRoutes(mux, h)

	const sess = "s1"
	// Seed one file so the {fileId} routes exercise real behavior.
	src := filepath.Join(t.TempDir(), "seed.txt")
	if err := os.WriteFile(src, []byte("seed-content"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	entry, err := h.store.AddFile(sess, src, "seed.txt")
	if err != nil {
		t.Fatalf("seed file: %v", err)
	}

	do := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr
	}

	base := "/api/sessions/" + sess

	t.Run("list reaches ListFiles", func(t *testing.T) {
		rr := do(http.MethodGet, base+"/files")
		if rr.Code != http.StatusOK {
			t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), `"files"`) || !strings.Contains(rr.Body.String(), `"count"`) {
			t.Fatalf("ListFiles body expected, got %s", rr.Body.String())
		}
	})

	t.Run("get reaches GetFile", func(t *testing.T) {
		rr := do(http.MethodGet, base+"/files/"+entry.ID)
		if rr.Code != http.StatusOK {
			t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), entry.ID) {
			t.Fatalf("GetFile should return the entry, got %s", rr.Body.String())
		}
	})

	t.Run("download reaches DownloadFile", func(t *testing.T) {
		rr := do(http.MethodGet, base+"/files/"+entry.ID+"/download")
		if rr.Code != http.StatusOK {
			t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
		}
		if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "seed.txt") {
			t.Fatalf("DownloadFile should set Content-Disposition, got %q", cd)
		}
	})

	t.Run("validate reaches ValidateLinks", func(t *testing.T) {
		rr := do(http.MethodPost, base+"/files/validate")
		if rr.Code != http.StatusOK {
			t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), `"broken_links"`) {
			t.Fatalf("ValidateLinks body expected, got %s", rr.Body.String())
		}
	})

	t.Run("upload reaches UploadFile", func(t *testing.T) {
		// No multipart body -> UploadFile validates and 400s (proves it reached
		// UploadFile, not some other handler).
		rr := do(http.MethodPost, base+"/files/upload")
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("expected UploadFile to 400 on empty body, got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("delete reaches DeleteFile", func(t *testing.T) {
		rr := do(http.MethodDelete, base+"/files/"+entry.ID)
		if rr.Code != http.StatusOK {
			t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
		}
		if files, _ := h.store.ListFiles(sess); len(files) != 0 {
			t.Fatalf("DeleteFile should have removed the file, %d remain", len(files))
		}
	})
}
