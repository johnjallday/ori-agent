package skillshttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/skills"
)

type importFixture struct {
	*marketplaceFixture
	agentsSkills string
	configSkills string
}

func newImportFixture(t *testing.T) *importFixture {
	t.Helper()
	f := &importFixture{marketplaceFixture: newMarketplaceFixture(t)}
	t.Setenv("XDG_CONFIG_HOME", "")
	f.agentsSkills = filepath.Join(f.home, ".agents", "skills")
	f.configSkills = filepath.Join(f.home, ".config", "agents", "skills")
	return f
}

func writeSkillFolder(t *testing.T, dir, name, description string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\nInstructions for " + name + ".\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *importFixture) candidates(t *testing.T) (map[string]importCandidate, bool) {
	t.Helper()
	rr := httptest.NewRecorder()
	f.handler.ImportCandidates(rr, httptest.NewRequest(http.MethodGet, "/api/skills/import/candidates", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("candidates: %d %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Candidates []importCandidate `json:"candidates"`
		ShouldShow bool              `json:"should_show"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	byName := map[string]importCandidate{}
	for _, candidate := range body.Candidates {
		byName[candidate.Name] = candidate
	}
	return byName, body.ShouldShow
}

func (f *importFixture) importNames(t *testing.T, names ...string) (imported, skipped []string, failed []importFailure) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"names": names})
	rr := httptest.NewRecorder()
	f.handler.Import(rr, httptest.NewRequest(http.MethodPost, "/api/skills/import", strings.NewReader(string(payload))))
	if rr.Code != http.StatusOK {
		t.Fatalf("import: %d %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Imported []string        `json:"imported"`
		Skipped  []string        `json:"skipped"`
		Failed   []importFailure `json:"failed"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Imported, body.Skipped, body.Failed
}

func TestImportCandidatesFollowTheFilterRules(t *testing.T) {
	f := newImportFixture(t)
	writeSkillFolder(t, filepath.Join(f.agentsSkills, "writing-helper"), "writing-helper", "Helps with writing")
	writeSkillFolder(t, filepath.Join(f.configSkills, "from-config"), "from-config", "Installed with -g")
	writeSkillFolder(t, filepath.Join(f.agentsSkills, ".hidden-skill"), "hidden", "Hidden")
	if err := os.MkdirAll(filepath.Join(f.agentsSkills, "no-skill-file"), 0o750); err != nil {
		t.Fatal(err)
	}
	pluginCopy := filepath.Join(f.agentsSkills, "reaper-mixing")
	writeSkillFolder(t, pluginCopy, "reaper-mixing", "A plugin's copy")
	if err := os.WriteFile(filepath.Join(pluginCopy, plugin.LegacySkillReceiptFileName), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeSkillFolder(t, filepath.Join(f.agentsSkills, "mine-already"), "mine-already", "Already imported")
	writeSkillFolder(t, filepath.Join(f.skillsDir, "mine-already"), "mine-already", "Already imported")

	candidates, shouldShow := f.candidates(t)
	if len(candidates) != 3 {
		t.Fatalf("candidates = %+v", candidates)
	}
	if got := candidates["writing-helper"]; got.Description != "Helps with writing" || got.SourceFolder != f.agentsSkills || got.AlreadyPresent {
		t.Errorf("writing-helper = %+v", got)
	}
	if got := candidates["from-config"]; got.SourceFolder != f.configSkills {
		t.Errorf("from-config = %+v", got)
	}
	if got := candidates["mine-already"]; !got.AlreadyPresent {
		t.Errorf("mine-already = %+v", got)
	}
	if !shouldShow {
		t.Error("the panel should show while something is importable")
	}
}

func TestImportCandidatesHideSkillsAnEnabledPluginProvides(t *testing.T) {
	f := newImportFixture(t)
	writeSkillFolder(t, filepath.Join(f.agentsSkills, "reaper-mixing"), "reaper-mixing", "An old copy with no receipt")
	pluginDir := filepath.Join(t.TempDir(), "reaper", "skills", "reaper-mixing")
	writeSkillFolder(t, pluginDir, "reaper-mixing", "From the plugin")
	f.handler.manager.SetPluginSkillProvider(func(string) []skills.PluginSkill {
		return []skills.PluginSkill{{Plugin: "reaper", Name: "reaper-mixing", Dir: pluginDir}}
	})
	if candidates, shouldShow := f.candidates(t); len(candidates) != 0 || shouldShow {
		t.Fatalf("a plugin's skill was offered for import: %+v", candidates)
	}
}

func TestImportCopiesSymlinksAsContentAndLeavesOriginalsUnchanged(t *testing.T) {
	f := newImportFixture(t)
	real := filepath.Join(t.TempDir(), "real-skill")
	writeSkillFolder(t, real, "linked-skill", "Reached through a link")
	if err := os.MkdirAll(f.agentsSkills, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(f.agentsSkills, "linked-skill")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// A linked file inside a real folder is copied as a file too.
	plain := filepath.Join(f.agentsSkills, "plain-skill")
	writeSkillFolder(t, plain, "plain-skill", "Plain")
	notes := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(notes, []byte("shared notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(notes, filepath.Join(plain, "notes.md")); err != nil {
		t.Fatal(err)
	}
	beforeReal, _ := plugin.TreeDigest(real, nil)
	beforePlainSkill, _ := os.ReadFile(filepath.Join(plain, "SKILL.md"))

	imported, _, failed := f.importNames(t, "linked-skill", "plain-skill")
	if len(imported) != 2 || len(failed) != 0 {
		t.Fatalf("imported %v failed %v", imported, failed)
	}
	for _, path := range []string{
		filepath.Join(f.skillsDir, "linked-skill"),
		filepath.Join(f.skillsDir, "linked-skill", "SKILL.md"),
		filepath.Join(f.skillsDir, "plain-skill", "notes.md"),
	} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			t.Errorf("%s was not copied as real content: %v %v", path, info, err)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(f.skillsDir, "plain-skill", "notes.md")); string(data) != "shared notes" {
		t.Errorf("linked file content = %q", data)
	}

	afterReal, _ := plugin.TreeDigest(real, nil)
	afterPlainSkill, _ := os.ReadFile(filepath.Join(plain, "SKILL.md"))
	if beforeReal != afterReal || string(beforePlainSkill) != string(afterPlainSkill) {
		t.Fatal("an original was changed by the import")
	}
	if info, err := os.Lstat(filepath.Join(f.agentsSkills, "linked-skill")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the original link was moved or replaced")
	}
}

func TestImportWritesASourceFileFromTheLockFile(t *testing.T) {
	f := newImportFixture(t)
	writeSkillFolder(t, filepath.Join(f.agentsSkills, "find-skills"), "find-skills", "Finds skills")
	writeSkillFolder(t, filepath.Join(f.agentsSkills, "handmade"), "handmade", "Written by hand")
	lock := `{"version":3,"skills":{"find-skills":{"source":"vercel-labs/skills","sourceType":"github"}}}`
	if err := os.WriteFile(filepath.Join(f.home, ".agents", ".skill-lock.json"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}

	imported, _, failed := f.importNames(t, "find-skills", "handmade")
	if len(imported) != 2 || len(failed) != 0 {
		t.Fatalf("imported %v failed %v", imported, failed)
	}
	source, found, err := readSkillSource(filepath.Join(f.skillsDir, "find-skills"))
	digest, _ := skillFolderDigest(filepath.Join(f.skillsDir, "find-skills"))
	if err != nil || !found || source.Package != "vercel-labs/skills@find-skills" || source.TreeDigest != digest {
		t.Fatalf("find-skills source = %+v %v %v", source, found, err)
	}
	if _, found, _ := readSkillSource(filepath.Join(f.skillsDir, "handmade")); found {
		t.Fatal("a skill with no lock entry got a source file")
	}
}

func TestOneImportFailureDoesNotStopTheRest(t *testing.T) {
	f := newImportFixture(t)
	writeSkillFolder(t, filepath.Join(f.agentsSkills, "good-skill"), "good-skill", "Good")
	broken := filepath.Join(f.agentsSkills, "broken-skill")
	writeSkillFolder(t, broken, "broken-skill", "Has a dangling link")
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), filepath.Join(broken, "dangling.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	imported, _, failed := f.importNames(t, "broken-skill", "good-skill", "never-existed")
	if len(imported) != 1 || imported[0] != "good-skill" {
		t.Fatalf("imported = %v", imported)
	}
	if len(failed) != 2 || failed[0].Name != "broken-skill" || failed[0].Reason == "" || failed[1].Name != "never-existed" {
		t.Fatalf("failed = %+v", failed)
	}
	if _, err := os.Stat(filepath.Join(f.skillsDir, "broken-skill")); !os.IsNotExist(err) {
		t.Fatal("a failed import left a partial folder")
	}
	entries, _ := os.ReadDir(f.skillsDir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".ori-") {
			t.Fatalf("an import left %s behind", entry.Name())
		}
	}
}

func TestTheAnswerIsStoredPerWorkspaceDirectory(t *testing.T) {
	f := newImportFixture(t)
	writeSkillFolder(t, filepath.Join(f.agentsSkills, "writing-helper"), "writing-helper", "Helps with writing")
	if _, shouldShow := f.candidates(t); !shouldShow {
		t.Fatal("the panel should show the first time")
	}
	rr := httptest.NewRecorder()
	f.handler.DismissImport(rr, httptest.NewRequest(http.MethodPost, "/api/skills/import/dismiss", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", rr.Code, rr.Body.String())
	}
	if candidates, shouldShow := f.candidates(t); shouldShow || len(candidates) != 1 {
		t.Fatalf("after Not now: should_show %v candidates %v", shouldShow, candidates)
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, skillsImportStateFileName)); err != nil {
		t.Fatalf("the choice was not stored in the data dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.skillsDir), skillsImportStateFileName)); !os.IsNotExist(err) {
		t.Fatal("the choice was stored in the Workspace Directory")
	}

	// Another Workspace Directory has not answered yet.
	otherSkills := filepath.Join(t.TempDir(), "Other Root", "Skills")
	f.handler.manager = skills.NewManager(skills.ManagerConfig{
		AgentStorePath: filepath.Join(f.dataDir, "agents.json"), PersonalSkillsDir: otherSkills,
	})
	if _, shouldShow := f.candidates(t); !shouldShow {
		t.Fatal("the panel did not show for a different Workspace Directory")
	}
	// Importing counts as answering too.
	f.importNames(t, "writing-helper")
	if _, shouldShow := f.candidates(t); shouldShow {
		t.Fatal("the panel showed again after an import")
	}
}
