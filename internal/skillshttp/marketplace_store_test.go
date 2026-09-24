package skillshttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/skills"
)

// fakeSkillsTool stands in for `npx skills`: `add` writes the layout the real
// tool leaves in its working folder. It never runs npx.
type fakeSkillsTool struct {
	calls   [][]string
	dirs    []string
	content string // SKILL.md body written by add
	err     error
}

func (f *fakeSkillsTool) run(_ context.Context, dir string, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	f.dirs = append(f.dirs, dir)
	if f.err != nil {
		return "tool failed", f.err
	}
	if len(args) >= 2 && args[0] == "add" {
		name := args[1][strings.LastIndex(args[1], "@")+1:]
		skillDir := filepath.Join(dir, ".agents", "skills", name)
		if err := os.MkdirAll(skillDir, 0o750); err != nil {
			return "", err
		}
		body := f.content
		if body == "" {
			body = "---\nname: " + name + "\ndescription: from the marketplace\n---\nInstructions.\n"
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o600); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, "skills-lock.json"), []byte(`{"version":1}`), 0o600); err != nil {
			return "", err
		}
		return "Installed 1 skill", nil
	}
	return "", nil
}

type marketplaceFixture struct {
	handler   *Handler
	tool      *fakeSkillsTool
	home      string
	dataDir   string
	skillsDir string
	trashed   []string
}

func newMarketplaceFixture(t *testing.T) *marketplaceFixture {
	t.Helper()
	f := &marketplaceFixture{
		tool:    &fakeSkillsTool{},
		home:    t.TempDir(),
		dataDir: t.TempDir(),
	}
	f.skillsDir = filepath.Join(t.TempDir(), "Ori Workspaces", "Skills")
	t.Setenv("HOME", f.home)
	t.Setenv("ORI_DATA_DIR", f.dataDir)
	manager := skills.NewManager(skills.ManagerConfig{
		AgentStorePath:    filepath.Join(f.dataDir, "agents.json"),
		PersonalSkillsDir: f.skillsDir,
	})
	f.handler = New(manager, nil, nil, nil)
	f.handler.skillsCLIInDir = f.tool.run

	trash := t.TempDir()
	originalSupported, originalMove := trashSupported, moveToTrash
	trashSupported = func() bool { return true }
	moveToTrash = func(path string) (string, error) {
		f.trashed = append(f.trashed, path)
		dest := filepath.Join(trash, filepath.Base(path))
		return dest, os.Rename(path, dest)
	}
	t.Cleanup(func() { trashSupported, moveToTrash = originalSupported, originalMove })
	return f
}

func (f *marketplaceFixture) post(t *testing.T, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	f.handler.Handle(rr, httptest.NewRequest(http.MethodPost, "/api/skills/"+path, strings.NewReader(body)))
	return rr
}

func (f *marketplaceFixture) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	f.handler.Handle(rr, httptest.NewRequest(http.MethodGet, "/api/skills/"+path, nil))
	return rr
}

// assertDownloadsCleaned checks that no temporary download folder is left.
func (f *marketplaceFixture) assertDownloadsCleaned(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.dataDir, "skill-downloads"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a download folder was left behind: %v", entries)
	}
}

func (f *marketplaceFixture) assertNothingUnderHome(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(f.home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("something was written under HOME: %v", entries)
	}
}

func TestInstallMovesTheSkillIntoTheSkillsFolderWithASourceFile(t *testing.T) {
	f := newMarketplaceFixture(t)
	rr := f.post(t, "marketplace/install", `{"package":"vercel-labs/skills@find-skills"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("install status %d body %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body["skill"] != "find-skills" || body["location"] != filepath.Join("Ori Workspaces", "Skills", "find-skills") {
		t.Fatalf("install response = %v", body)
	}

	if got := f.tool.calls[0]; strings.Join(got, " ") != "add vercel-labs/skills@find-skills -y --agent universal --copy" {
		t.Fatalf("skills tool args = %v (must never pass -g)", got)
	}
	if !strings.HasPrefix(f.tool.dirs[0], filepath.Join(f.dataDir, "skill-downloads")) {
		t.Fatalf("the tool ran in %q, not a download folder in the data dir", f.tool.dirs[0])
	}

	skillDir := filepath.Join(f.skillsDir, "find-skills")
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Fatalf("the skill is not in the Skills folder: %v", err)
	}
	source, found, err := readSkillSource(skillDir)
	if err != nil || !found {
		t.Fatalf("no source file: %v %v", found, err)
	}
	digest, _ := skillFolderDigest(skillDir)
	if source.SchemaVersion != 1 || source.Package != "vercel-labs/skills@find-skills" || source.TreeDigest != digest ||
		source.InstalledAt.IsZero() || !source.UpdatedAt.Equal(source.InstalledAt) {
		t.Fatalf("source file = %+v (digest now %s)", source, digest)
	}
	if _, err := os.Stat(filepath.Join(f.skillsDir, "skills-lock.json")); !os.IsNotExist(err) {
		t.Fatal("the tool's lock file was carried into the Skills folder")
	}
	f.assertDownloadsCleaned(t)
	f.assertNothingUnderHome(t)
}

func TestInstallRefusesANameAlreadyInTheSkillsFolder(t *testing.T) {
	f := newMarketplaceFixture(t)
	existing := filepath.Join(f.skillsDir, "find-skills")
	if err := os.MkdirAll(existing, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(existing, "SKILL.md"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}

	rr := f.post(t, "marketplace/install", `{"package":"vercel-labs/skills@find-skills"}`)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `A skill named \"find-skills\" is already in your Skills folder.`) {
		t.Fatalf("install status %d body %s", rr.Code, rr.Body.String())
	}
	if data, _ := os.ReadFile(filepath.Join(existing, "SKILL.md")); string(data) != "mine" {
		t.Fatalf("the existing skill was overwritten: %q", data)
	}
	f.assertDownloadsCleaned(t)
}

func TestTheDownloadFolderIsRemovedWhenTheToolFails(t *testing.T) {
	f := newMarketplaceFixture(t)
	f.tool.err = errors.New("exit status 1")

	rr := f.post(t, "marketplace/install", `{"package":"vercel-labs/skills@find-skills"}`)
	if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "tool failed") {
		t.Fatalf("install status %d body %s", rr.Code, rr.Body.String())
	}
	f.assertDownloadsCleaned(t)
	if _, err := os.Stat(f.skillsDir); !os.IsNotExist(err) {
		t.Fatal("a failed install touched the Skills folder")
	}
}

func TestAMissingNodeSaysNodeIsRequired(t *testing.T) {
	f := newMarketplaceFixture(t)
	f.tool.err = fmt.Errorf("skills command failed: %w", &exec.Error{Name: "npx", Err: exec.ErrNotFound})

	rr := f.post(t, "marketplace/install", `{"package":"vercel-labs/skills@find-skills"}`)
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "Node.js is required") {
		t.Fatalf("install status %d body %s", rr.Code, rr.Body.String())
	}
	rr = f.post(t, "marketplace/search", `{"query":"find"}`)
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "Node.js is required") {
		t.Fatalf("search status %d body %s", rr.Code, rr.Body.String())
	}
	f.assertDownloadsCleaned(t)
}

func TestTheInstalledListReadsSourceFilesOnly(t *testing.T) {
	f := newMarketplaceFixture(t)
	if rr := f.post(t, "marketplace/install", `{"package":"vercel-labs/skills@find-skills"}`); rr.Code != http.StatusOK {
		t.Fatalf("install: %s", rr.Body.String())
	}
	handmade := filepath.Join(f.skillsDir, "handmade")
	if err := os.MkdirAll(handmade, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handmade, "SKILL.md"), []byte("---\nname: handmade\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	callsBefore := len(f.tool.calls)

	rr := f.get(t, "marketplace/installed")
	if rr.Code != http.StatusOK {
		t.Fatalf("list status %d body %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Skills []installedMarketplaceSkill `json:"skills"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Skills) != 1 || body.Skills[0].Name != "find-skills" || body.Skills[0].Package != "vercel-labs/skills@find-skills" ||
		body.Skills[0].InstalledAt.IsZero() {
		t.Fatalf("installed list = %+v", body.Skills)
	}
	if len(f.tool.calls) != callsBefore {
		t.Fatal("listing ran the skills tool")
	}
}

func TestRemoveRefusesPathsAndMovesAValidSkillToTheTrash(t *testing.T) {
	f := newMarketplaceFixture(t)
	if rr := f.post(t, "marketplace/install", `{"package":"vercel-labs/skills@find-skills"}`); rr.Code != http.StatusOK {
		t.Fatalf("install: %s", rr.Body.String())
	}
	for _, bad := range []string{"..", "../Agents", "a/b", ".hidden"} {
		rr := f.post(t, "marketplace/remove", `{"skill":"`+bad+`"}`)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("remove %q: status %d body %s", bad, rr.Code, rr.Body.String())
		}
	}
	if len(f.trashed) != 0 {
		t.Fatalf("a refused name reached the Trash: %v", f.trashed)
	}

	rr := f.post(t, "marketplace/remove", `{"skill":"find-skills"}`)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Moved to Trash") {
		t.Fatalf("remove status %d body %s", rr.Code, rr.Body.String())
	}
	if len(f.trashed) != 1 || f.trashed[0] != filepath.Join(f.skillsDir, "find-skills") {
		t.Fatalf("trashed = %v", f.trashed)
	}
	if _, err := os.Stat(filepath.Join(f.skillsDir, "find-skills")); !os.IsNotExist(err) {
		t.Fatal("the skill folder is still in the Skills folder")
	}
	if rr := f.post(t, "marketplace/remove", `{"skill":"find-skills"}`); !strings.Contains(rr.Body.String(), `"not_found"`) {
		t.Fatalf("removing again: %s", rr.Body.String())
	}
}

func TestRemoveRefusesWithoutATrash(t *testing.T) {
	f := newMarketplaceFixture(t)
	if rr := f.post(t, "marketplace/install", `{"package":"vercel-labs/skills@find-skills"}`); rr.Code != http.StatusOK {
		t.Fatalf("install: %s", rr.Body.String())
	}
	trashSupported = func() bool { return false }
	rr := f.post(t, "marketplace/remove", `{"skill":"find-skills"}`)
	if rr.Code != http.StatusInternalServerError || !strings.Contains(rr.Body.String(), "no Trash") {
		t.Fatalf("remove status %d body %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(f.skillsDir, "find-skills", "SKILL.md")); err != nil {
		t.Fatalf("the skill was deleted without a Trash: %v", err)
	}
}
