package characterhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newHandler(t *testing.T) *Handler {
	t.Helper()
	h, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h
}

func getCatalog(t *testing.T, h *Handler) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeCatalog(rec, httptest.NewRequest(http.MethodGet, "/api/characters", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return rec, body
}

func TestServesTheWorkingCatalog(t *testing.T) {
	_, body := getCatalog(t, newHandler(t))

	chars, ok := body["characters"].([]any)
	if !ok {
		t.Fatalf("expected a characters array, got %T", body["characters"])
	}
	if len(chars) < 8 {
		t.Fatalf("expected at least 8 working characters, got %d", len(chars))
	}
	if body["guide"] == nil {
		t.Fatal("expected a guide identity")
	}
	if body["catalog_version"] == "" {
		t.Fatal("expected a catalog version")
	}
}

// Ori is served on its own key, never inside the assignable list. A client that
// renders `characters` as picker options therefore cannot offer the guide
// identity by construction (FR-28/FR-71).
func TestGuideIsNotInTheAssignableList(t *testing.T) {
	_, body := getCatalog(t, newHandler(t))
	reserved, _ := body["reserved_guide_id"].(string)
	if reserved == "" {
		t.Fatal("expected a reserved guide id")
	}
	for _, raw := range body["characters"].([]any) {
		ch := raw.(map[string]any)
		if ch["id"] == reserved {
			t.Fatalf("reserved guide id %q appears in the assignable list", reserved)
		}
		if ch["kind"] != "working" {
			t.Errorf("character %v is not kind=working", ch["id"])
		}
	}
}

func TestOrderingIsStableAcrossRequests(t *testing.T) {
	h := newHandler(t)
	ids := func() []string {
		_, body := getCatalog(t, h)
		var out []string
		for _, raw := range body["characters"].([]any) {
			out = append(out, raw.(map[string]any)["id"].(string))
		}
		return out
	}
	first, second := ids(), ids()
	if len(first) != len(second) {
		t.Fatal("length changed between requests")
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("order changed at %d: %q vs %q", i, first[i], second[i])
		}
	}
}

// The register path is a local repository detail; shipping it to the browser
// would leak the documentation layout for no UI benefit.
func TestResponseDoesNotLeakLocalFilesystemPaths(t *testing.T) {
	rec, _ := getCatalog(t, newHandler(t))
	body := rec.Body.String()
	for _, needle := range []string{"provenance", "docs/", "internal/", "/Users/", ".md"} {
		if strings.Contains(body, needle) {
			t.Errorf("response leaks %q", needle)
		}
	}
}

func TestAssetPathsAreBrowserURLs(t *testing.T) {
	_, body := getCatalog(t, newHandler(t))
	check := func(label string, ch map[string]any) {
		assets := ch["assets"].(map[string]any)
		for _, key := range []string{"portrait", "sprite", "static"} {
			p, _ := assets[key].(string)
			if !strings.HasPrefix(p, "/characters/") {
				t.Errorf("%s %s asset %q should be a /characters/ URL", label, key, p)
			}
			if !strings.HasSuffix(p, ".svg") {
				t.Errorf("%s %s asset %q should be an svg", label, key, p)
			}
		}
	}
	check("guide", body["guide"].(map[string]any))
	for _, raw := range body["characters"].([]any) {
		ch := raw.(map[string]any)
		check(ch["id"].(string), ch)
	}
}

func TestCachesWithAnETag(t *testing.T) {
	h := newHandler(t)
	rec, _ := getCatalog(t, h)
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected an ETag")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/characters", nil)
	req.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	h.ServeCatalog(second, req)
	if second.Code != http.StatusNotModified {
		t.Fatalf("expected 304 for a matching ETag, got %d", second.Code)
	}
}

// There is no mutation route here, and there must never be one: character
// assignment belongs to the agent endpoints, which re-validate the ID.
func TestNonGetMethodsAreRejected(t *testing.T) {
	h := newHandler(t)
	for _, method := range []string{
		http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodHead,
	} {
		t.Run(method, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeCatalog(rec, httptest.NewRequest(method, "/api/characters", strings.NewReader(`{"id":"x"}`)))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("expected 405 for %s, got %d", method, rec.Code)
			}
		})
	}
}
