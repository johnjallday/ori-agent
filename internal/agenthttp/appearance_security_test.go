package agenthttp

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every seam where user input reaches storage, a filesystem path, or a
// response. The upload path is the sharp one: it takes bytes and a filename
// from the client and turns them into a file on disk (PRD FR-8, FR-63 through
// FR-65, FR-106).

func TestUploadFilenamesAreServerGeneratedAndConfined(t *testing.T) {
	cases := []struct {
		agentName string
		want      string
	}{
		{"scout", "scout.png"},
		// Path separators, traversal, and everything else outside the allowlist
		// collapse to underscores; leading and trailing ones are trimmed, so the
		// result is always a plain basename with a known extension.
		{"../../etc/passwd", "etc_passwd.png"},
		{"a/b/c", "a_b_c.png"},
		{"My Agent", "My_Agent.png"},
		{"agent\x00null", "agent_null.png"},
		// A name that survives nothing still yields a usable filename rather
		// than an empty one or a bare extension.
		{"..", "agent.png"},
		{"", "agent.png"},
		{"???", "agent.png"},
		{"emoji🙂name", "emoji_name.png"},
	}
	for _, tc := range cases {
		got := appearanceUploadFilename(tc.agentName, ".png")
		if got != tc.want {
			t.Errorf("appearanceUploadFilename(%q) = %q, want %q", tc.agentName, got, tc.want)
		}
		// Whatever the input, the output must be usable as a bare filename.
		if got != filepath.Base(got) {
			t.Errorf("appearanceUploadFilename(%q) = %q, which is not a plain basename", tc.agentName, got)
		}
		if strings.ContainsAny(got, "/\\\x00") {
			t.Errorf("appearanceUploadFilename(%q) = %q, which contains a path character", tc.agentName, got)
		}
	}
}

func TestRemoveAppearanceUploadRefusesAnythingButAPlainFilename(t *testing.T) {
	dir := isolateAvatarDir(t)
	if err := os.MkdirAll(AvatarDir, 0o755); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	// A file outside the avatar directory that a traversal would reach.
	outside := filepath.Join(dir, "precious.txt")
	if err := os.WriteFile(outside, []byte("keep me"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	for _, name := range []string{"../precious.txt", "..", ".", "", "   ", "sub/dir.png"} {
		removeAppearanceUpload(name)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("a traversal reached outside the avatar directory: %v", err)
	}
}

func TestUploadRejectsEveryDisallowedContentType(t *testing.T) {
	isolateAvatarDir(t)
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "sniffed")
	h := uploadHandler(t, ts)

	// The declared type and the extension are both irrelevant — the bytes
	// decide. These are the shapes that would be dangerous to serve.
	payloads := map[string][]byte{
		"svg":        []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"html":       []byte("<!doctype html><html><body><script>alert(1)</script></body></html>"),
		"javascript": []byte("alert(1); // not an image"),
		"pdf":        []byte("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n"),
		"webm":       append([]byte{0x1A, 0x45, 0xDF, 0xA3}, bytes.Repeat([]byte{0}, 64)...),
		"empty":      {},
	}
	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, uploadRequest(t, "sniffed", payload, nil))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
			ag, _ := ts.store.GetAgent("sniffed")
			if ag.Appearance.UploadedImage() != "" {
				t.Fatalf("a rejected %s upload was stored", name)
			}
		})
	}
}

func TestUploadRejectsAnOversizeBody(t *testing.T) {
	isolateAvatarDir(t)
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "oversize")

	// A real PNG header followed by more than the limit, so the rejection is on
	// size rather than on content sniffing.
	oversize := append(append([]byte{}, pngBytes...), bytes.Repeat([]byte{0}, MaxAvatarSize+1024)...)
	rec := httptest.NewRecorder()
	uploadHandler(t, ts).ServeHTTP(rec, uploadRequest(t, "oversize", oversize, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	ag, _ := ts.store.GetAgent("oversize")
	if ag.Appearance.UploadedImage() != "" {
		t.Fatal("an oversize upload was stored")
	}
}

func TestUploadRejectsAMissingOrMisnamedFormField(t *testing.T) {
	isolateAvatarDir(t)
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "fieldname")
	h := uploadHandler(t, ts)

	// No file at all.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, uploadRequest(t, "fieldname", nil, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a missing file, got %d", rec.Code)
	}

	// The retired field name from the old contract must not still work.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("avatar", "a.png")
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(pngBytes); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/agents/fieldname/appearance/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("the retired \"avatar\" field name still works: %d", rec.Code)
	}
}

func TestHostileAppearanceValuesAreRejectedNotStored(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "hostile")

	// Colour is the only free-text field a client can write, and it reaches a
	// stylesheet, so it is validated as a colour rather than escaped (FR-7).
	hostile := []string{
		"#fff;background:url(evil)",
		"expression(alert(1))",
		"url(javascript:alert(1))",
		"</style><script>alert(1)</script>",
		"#ff0000 !important",
		"var(--x)",
	}
	for _, value := range hostile {
		rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=hostile", map[string]any{
			"appearance": map[string]any{"generated": map[string]any{"color": value}},
		})
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%q was accepted (status %d)", value, rr.Code)
		}
	}

	ag, _ := ts.store.GetAgent("hostile")
	if ag.Appearance.GeneratedColor() != "" {
		t.Fatalf("a hostile colour reached storage: %q", ag.Appearance.GeneratedColor())
	}
}

func TestCatalogIdsAreValidatedAgainstTheCatalogNotSanitized(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()
	createPlainAgent(t, ts, "catalog-guard")

	// A catalog id ends up in a data attribute and an asset path, so an id that
	// is not in the catalog is refused outright rather than cleaned up and
	// stored (FR-9).
	for _, id := range []string{
		"../../etc/passwd",
		"<script>alert(1)</script>",
		"sable' onload='alert(1)",
		"does-not-exist",
	} {
		rr := ts.doRequest(t, http.MethodPatch, "/api/agents?name=catalog-guard", map[string]any{
			"appearance": map[string]any{"character": map[string]any{"catalog_id": id}},
		})
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%q was accepted (status %d)", id, rr.Code)
		}
	}

	ag, _ := ts.store.GetAgent("catalog-guard")
	if ag.Appearance.CharacterCatalogID() != "" {
		t.Fatalf("an unvalidated catalog id reached storage: %q", ag.Appearance.CharacterCatalogID())
	}
}

func TestErrorMessagesDoNotLeakFilesystemPaths(t *testing.T) {
	isolateAvatarDir(t)
	ts := setupTestServer(t)
	defer ts.cleanup()

	// A 404 for an unknown agent, a 400 for a bad type: neither should describe
	// where on disk anything lives (FR-73).
	h := uploadHandler(t, ts)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, uploadRequest(t, "nobody", pngBytes, nil))
	assertNoPaths(t, rec.Body.String())

	createPlainAgent(t, ts, "leaky")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, uploadRequest(t, "leaky", []byte("not an image at all"), nil))
	assertNoPaths(t, rec.Body.String())
}

func assertNoPaths(t *testing.T, body string) {
	t.Helper()
	for _, marker := range []string{"/Users/", "/tmp/", "/private/", AvatarDir + "/"} {
		if strings.Contains(body, marker) {
			t.Errorf("response leaks a filesystem path (%q): %s", marker, body)
		}
	}
}

func TestMarkupBearingAgentNamesNeverReachStorage(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	// The renderer escapes names on the way out, but the first line of defence
	// is that a name carrying markup or a quote never gets created at all. This
	// pins that guard, because the upload filename is derived from the name.
	for _, name := range []string{`Quote"Agent`, `<script>alert(1)</script>`, "back\\slash"} {
		rr := ts.doRequest(t, http.MethodPost, "/api/agents", map[string]any{
			"name":  name,
			"model": "gpt-4o-mini",
		})
		if rr.Code != http.StatusBadRequest {
			t.Errorf("agent name %q was accepted (status %d)", name, rr.Code)
		}
	}
}

func TestAppearanceRoundTripsThroughJSONForEverySource(t *testing.T) {
	isolateAvatarDir(t)
	ts := setupTestServer(t)
	defer ts.cleanup()
	entry := assignableCharacter(t)
	// Spaces are legal in agent names and are the case that actually reaches the
	// filename sanitizer in practice.
	name := "My Demo Agent"
	rr := ts.doRequest(t, http.MethodPost, "/api/agents", map[string]any{
		"name":  name,
		"model": "gpt-4o-mini",
	})
	assertStatus(t, rr, http.StatusOK)

	rr = ts.doRequest(t, http.MethodPatch, "/api/agents?name="+name, map[string]any{
		"appearance": map[string]any{
			"generated": map[string]any{"color": "#6d5dfc"},
			"character": map[string]any{"catalog_id": string(entry.ID)},
		},
	})
	assertStatus(t, rr, http.StatusOK)

	rec := httptest.NewRecorder()
	uploadHandler(t, ts).ServeHTTP(rec, uploadRequest(t, name, pngBytes, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload failed: %s", rec.Body.String())
	}

	rr = ts.doRequest(t, http.MethodGet, "/api/agents/"+name+"/detail", nil)
	assertStatus(t, rr, http.StatusOK)
	var detail map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	appearance, ok := detail["appearance"].(map[string]any)
	if !ok {
		t.Fatalf("appearance missing from the response: %#v", detail["appearance"])
	}
	// All three sources present at once, and the stored filename is the
	// sanitized one rather than the raw agent name.
	uploaded, _ := appearance["uploaded"].(map[string]any)
	if uploaded["image"] != "My_Demo_Agent.png" {
		t.Errorf("stored filename = %v, want the sanitized name", uploaded["image"])
	}
	generated, _ := appearance["generated"].(map[string]any)
	if generated["color"] != "#6d5dfc" {
		t.Errorf("colour = %v", generated["color"])
	}
	character, _ := appearance["character"].(map[string]any)
	if character["catalog_id"] != string(entry.ID) {
		t.Errorf("catalog id = %v", character["catalog_id"])
	}
}
