package agenthttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// isUploadNameFor reports whether name is a server-generated upload name for
// base: "<base>-<12 hex>.<ext>".
func isUploadNameFor(name, base, ext string) bool {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(base) + `-[0-9a-f]{12}` + regexp.QuoteMeta(ext) + `$`).MatchString(name)
}

// imageFiles lists the image files in dir, sorted, ignoring anything else.
func imageFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".png", ".gif", ".jpg", ".webp":
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// replaceFixture is one agent in a file store whose folders the test knows.
type replaceFixture struct {
	store     store.Store
	agentsDir string
	avatars   string
	upload    *AppearanceUploadHandler
	dash      *DashboardHandler
	patch     *Handler
}

func newReplaceFixture(t *testing.T, name string) replaceFixture {
	t.Helper()
	data := isolateAvatarDir(t)
	dir := t.TempDir()
	st, err := store.NewFileStore(filepath.Join(dir, "index.json"), types.Settings{Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := st.CreateAgent(name, &store.CreateAgentConfig{}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return replaceFixture{
		store:     st,
		agentsDir: filepath.Join(dir, "agents"),
		avatars:   filepath.Join(data, AvatarDir),
		upload:    NewAppearanceUploadHandler(st, nil),
		dash:      NewDashboardHandler(st),
		patch:     New(st),
	}
}

func (f replaceFixture) send(t *testing.T, name string, body []byte, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	f.upload.ServeHTTP(rec, uploadRequest(t, name, body, fields))
	return rec
}

func (f replaceFixture) remove(t *testing.T, name, version string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/api/agents/" + url.PathEscape(name) + "/appearance/upload"
	if version != "" {
		target += "?expected_version=" + url.QueryEscape(version)
	}
	rec := httptest.NewRecorder()
	f.upload.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, target, nil))
	return rec
}

func (f replaceFixture) image(t *testing.T, name string) string {
	t.Helper()
	ag, _ := f.store.GetAgent(name)
	return ag.Appearance.UploadedImage()
}

func responseVersion(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return body.Version
}

// A same-format replacement gets a new name, so the definition version moves:
// an editor still holding the old version is refused instead of silently
// replacing the new picture.
func TestSameFormatReplacementChangesTheVersion(t *testing.T) {
	f := newReplaceFixture(t, "Painter")
	first := f.send(t, "Painter", pngBytes, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first upload: %d %s", first.Code, first.Body.String())
	}
	firstName, firstVersion := f.image(t, "Painter"), responseVersion(t, first)
	if fetchAgentVersion(t, f.dash, "Painter") != firstVersion {
		t.Fatal("upload receipt version does not match the next read")
	}

	second := f.send(t, "Painter", pngBytes, map[string]string{"expected_version": firstVersion})
	if second.Code != http.StatusOK {
		t.Fatalf("same-format replacement: %d %s", second.Code, second.Body.String())
	}
	secondName := f.image(t, "Painter")
	if secondName == firstName || !isUploadNameFor(secondName, "Painter", ".png") {
		t.Fatalf("replacement name %q must differ from %q", secondName, firstName)
	}
	if responseVersion(t, second) == firstVersion {
		t.Fatal("same-format replacement left the version unchanged")
	}
	if got := imageFiles(t, f.avatars); len(got) != 1 || got[0] != secondName {
		t.Fatalf("expected only the new image on disk, got %v", got)
	}

	stale := f.send(t, "Painter", pngBytes, map[string]string{"expected_version": firstVersion})
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), staleAgentEditCode) {
		t.Fatalf("an upload from the old version must be refused: %d %s", stale.Code, stale.Body.String())
	}
}

// A refused upload leaves the image on display and no stray file.
func TestRefusedUploadKeepsTheCurrentImage(t *testing.T) {
	f := newReplaceFixture(t, "Keeper")
	if rec := f.send(t, "Keeper", pngBytes, nil); rec.Code != http.StatusOK {
		t.Fatalf("seed upload: %s", rec.Body.String())
	}
	kept := f.image(t, "Keeper")

	rec := f.send(t, "Keeper", gifBytes, map[string]string{"expected_version": "deadbeefdeadbeef"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d %s", rec.Code, rec.Body.String())
	}
	if f.image(t, "Keeper") != kept {
		t.Fatal("a refused upload changed the stored image")
	}
	if got := imageFiles(t, f.avatars); len(got) != 1 || got[0] != kept {
		t.Fatalf("a refused upload left files behind or removed the current one: %v", got)
	}
}

// An upload after the definition was edited on disk is refused with its own
// code, and the outside edit and current image both survive.
func TestUploadAfterAnEditOnDiskKeepsBoth(t *testing.T) {
	f := newReplaceFixture(t, "Disky")
	if rec := f.send(t, "Disky", pngBytes, nil); rec.Code != http.StatusOK {
		t.Fatalf("seed upload: %s", rec.Body.String())
	}
	kept := f.image(t, "Disky")
	path := filepath.Join(f.agentsDir, "Disky", "agent_settings.json")
	data, _ := os.ReadFile(path)
	edited := strings.Replace(string(data), `"model": "gpt-4o-mini"`, `"model": "edited-on-disk"`, 1)
	if edited == string(data) {
		t.Fatalf("fixture lacks the model: %s", data)
	}
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := f.send(t, "Disky", gifBytes, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), agentChangedOnDiskCode) {
		t.Fatalf("expected agent_changed_on_disk, got %d %s", rec.Code, rec.Body.String())
	}
	if after, _ := os.ReadFile(path); string(after) != edited {
		t.Fatal("the outside edit was overwritten")
	}
	if got := imageFiles(t, f.avatars); len(got) != 1 || got[0] != kept {
		t.Fatalf("expected only the current image, got %v", got)
	}
}

// When the record cannot be written, the staged file is removed and the
// previous image is still on disk and still referenced.
func TestUploadRecordFailureKeepsThePreviousImage(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions enforced for this user")
	}
	f := newReplaceFixture(t, "Fragile")
	if rec := f.send(t, "Fragile", pngBytes, nil); rec.Code != http.StatusOK {
		t.Fatalf("seed upload: %s", rec.Body.String())
	}
	kept := f.image(t, "Fragile")
	folder := filepath.Join(f.agentsDir, "Fragile")
	if err := os.Chmod(folder, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(folder, 0o750) })

	rec := f.send(t, "Fragile", gifBytes, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d %s", rec.Code, rec.Body.String())
	}
	if f.image(t, "Fragile") != kept {
		t.Fatal("a failed record write still switched the image")
	}
	if got := imageFiles(t, f.avatars); len(got) != 1 || got[0] != kept {
		t.Fatalf("expected only the previous image, got %v", got)
	}
}

// An upload and a PATCH started from the same version: exactly one wins, and
// a losing upload leaves no file behind.
func TestOverlappingUploadAndPatchCommitOnce(t *testing.T) {
	for i := 0; i < 8; i++ {
		f := newReplaceFixture(t, "Racer")
		version := fetchAgentVersion(t, f.dash, "Racer")
		var wg sync.WaitGroup
		var uploadCode, patchCode int
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			uploadCode = f.send(t, "Racer", pngBytes, map[string]string{"expected_version": version}).Code
		}()
		go func() {
			defer wg.Done()
			<-start
			patchCode = patchAgent(t, f.patch, "Racer", `{"system_prompt":"raced","expected_version":"`+version+`"}`).Code
		}()
		close(start)
		wg.Wait()

		wins := 0
		for _, code := range []int{uploadCode, patchCode} {
			if code == http.StatusOK {
				wins++
			} else if code != http.StatusConflict {
				t.Fatalf("unexpected status %d (upload %d, patch %d)", code, uploadCode, patchCode)
			}
		}
		if wins != 1 {
			t.Fatalf("expected one winner, upload %d patch %d", uploadCode, patchCode)
		}
		files := imageFiles(t, f.avatars)
		if uploadCode == http.StatusOK && (len(files) != 1 || files[0] != f.image(t, "Racer")) {
			t.Fatalf("winning upload: files %v, stored %q", files, f.image(t, "Racer"))
		}
		if uploadCode != http.StatusOK && len(files) != 0 {
			t.Fatalf("losing upload left files behind: %v", files)
		}
	}
}

// Removal honours the version and keeps the file when refused.
func TestRemovalHonoursTheVersion(t *testing.T) {
	f := newReplaceFixture(t, "Remover")
	seed := f.send(t, "Remover", pngBytes, nil)
	if seed.Code != http.StatusOK {
		t.Fatalf("seed upload: %s", seed.Body.String())
	}
	kept := f.image(t, "Remover")

	if rec := f.remove(t, "Remover", "deadbeefdeadbeef"); rec.Code != http.StatusConflict {
		t.Fatalf("stale removal: expected 409, got %d %s", rec.Code, rec.Body.String())
	}
	if got := imageFiles(t, f.avatars); len(got) != 1 || got[0] != kept {
		t.Fatalf("a refused removal touched the file: %v", got)
	}

	rec := f.remove(t, "Remover", responseVersion(t, seed))
	if rec.Code != http.StatusOK {
		t.Fatalf("removal: %d %s", rec.Code, rec.Body.String())
	}
	if v := responseVersion(t, rec); v == "" || v != fetchAgentVersion(t, f.dash, "Remover") {
		t.Fatalf("removal receipt version %q does not match the next read", v)
	}
	if got := imageFiles(t, f.avatars); len(got) != 0 {
		t.Fatalf("removed image still on disk: %v", got)
	}
}

// Without confirmation, a shared agent's image is neither staged nor changed.
func TestSharedUploadWithoutConfirmationWritesNothing(t *testing.T) {
	isolateAvatarDir(t)
	h := guardTestHandler(t, []string{"Shared"}, map[string][]string{"ws-a": {"Shared"}, "ws-b": {"Shared"}})
	upload := NewAppearanceUploadHandler(h.State, h.workspaceStore)
	rec := httptest.NewRecorder()
	upload.ServeHTTP(rec, uploadRequest(t, "Shared", pngBytes, nil))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "shared_agent_edit_requires_confirmation") {
		t.Fatalf("expected confirmation request, got %d %s", rec.Code, rec.Body.String())
	}
	if got := imageFiles(t, avatarDirFor(h.State, "Shared")); len(got) != 0 {
		t.Fatalf("a declined shared upload wrote files: %v", got)
	}
	rec = httptest.NewRecorder()
	upload.ServeHTTP(rec, uploadRequest(t, "Shared", pngBytes, map[string]string{"confirm_shared_edit": "true"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("confirmed shared upload: %d %s", rec.Code, rec.Body.String())
	}
}
