package skillshttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (f *marketplaceFixture) install(t *testing.T) string {
	t.Helper()
	if rr := f.post(t, "marketplace/install", `{"package":"vercel-labs/skills@find-skills"}`); rr.Code != http.StatusOK {
		t.Fatalf("install: %d %s", rr.Code, rr.Body.String())
	}
	return filepath.Join(f.skillsDir, "find-skills")
}

func (f *marketplaceFixture) check(t *testing.T) skillUpdateStatus {
	t.Helper()
	rr := f.post(t, "marketplace/check", `{}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("check: %d %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Skills []skillUpdateStatus `json:"skills"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Skills) != 1 {
		t.Fatalf("check skills = %+v", body.Skills)
	}
	return body.Skills[0]
}

func TestCheckFindsNoUpdateWhenTheDigestMatches(t *testing.T) {
	f := newMarketplaceFixture(t)
	f.install(t)
	status := f.check(t)
	if status.UpdateAvailable || status.LocallyModified || status.Error != "" || status.Package != "vercel-labs/skills@find-skills" {
		t.Fatalf("status = %+v", status)
	}
	if got := f.tool.calls[len(f.tool.calls)-1]; got[0] != "add" || got[1] != "vercel-labs/skills@find-skills" {
		t.Fatalf("check did not download a fresh copy: %v", got)
	}
	f.assertDownloadsCleaned(t)
}

func TestCheckFindsAnUpdateWhenTheDownloadDiffers(t *testing.T) {
	f := newMarketplaceFixture(t)
	f.install(t)
	f.tool.content = "---\nname: find-skills\n---\nNewer instructions.\n"
	status := f.check(t)
	if !status.UpdateAvailable || status.LocallyModified {
		t.Fatalf("status = %+v", status)
	}
	f.assertDownloadsCleaned(t)
}

func TestCheckReportsALocallyEditedSkill(t *testing.T) {
	f := newMarketplaceFixture(t)
	dir := f.install(t)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("my own edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Finder's .DS_Store is not an edit.
	if err := os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("finder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status := f.check(t); !status.LocallyModified {
		t.Fatalf("status = %+v", status)
	}
}

func TestADSStoreAloneIsNotALocalEdit(t *testing.T) {
	f := newMarketplaceFixture(t)
	dir := f.install(t)
	if err := os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("finder"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status := f.check(t); status.LocallyModified {
		t.Fatalf("a .DS_Store counted as an edit: %+v", status)
	}
}

func TestUpdateRefusesALocallyEditedSkillWithoutForce(t *testing.T) {
	f := newMarketplaceFixture(t)
	dir := f.install(t)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("my own edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.tool.content = "---\nname: find-skills\n---\nNewer instructions.\n"

	rr := f.post(t, "marketplace/update", `{"skill":"find-skills"}`)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"locally_modified"`) ||
		!strings.Contains(rr.Body.String(), "You changed this skill since it was installed. Updating will replace your changes.") {
		t.Fatalf("update without force: %d %s", rr.Code, rr.Body.String())
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(data) != "my own edit\n" {
		t.Fatalf("the refused update changed the skill: %q", data)
	}

	before, _, _ := readSkillSource(dir)
	rr = f.post(t, "marketplace/update", `{"skill":"find-skills","force":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("update with force: %d %s", rr.Code, rr.Body.String())
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); !strings.Contains(string(data), "Newer instructions.") {
		t.Fatalf("the update did not replace the skill: %q", data)
	}
	after, found, err := readSkillSource(dir)
	current, _ := skillFolderDigest(dir)
	if err != nil || !found || after.TreeDigest != current || !after.InstalledAt.Equal(before.InstalledAt) ||
		!after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("source after update = %+v (before %+v, digest %s)", after, before, current)
	}
	f.assertDownloadsCleaned(t)
	f.assertNoUpdateLeftovers(t)
}

func TestTheOldFolderIsRestoredWhenTheSwapFails(t *testing.T) {
	f := newMarketplaceFixture(t)
	dir := f.install(t)
	original, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	f.tool.content = "---\nname: find-skills\n---\nNewer instructions.\n"

	realRename := renameSkillPath
	t.Cleanup(func() { renameSkillPath = realRename })
	renameSkillPath = func(from, to string) error {
		if strings.Contains(filepath.Base(from), ".ori-update-") {
			return errors.New("injected rename failure")
		}
		return realRename(from, to)
	}

	rr := f.post(t, "marketplace/update", `{"skill":"find-skills"}`)
	if rr.Code == http.StatusOK {
		t.Fatalf("a failed swap reported success: %s", rr.Body.String())
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(data) != string(original) {
		t.Fatalf("the old skill was not restored: %q", data)
	}
	f.assertDownloadsCleaned(t)
	f.assertNoUpdateLeftovers(t)
}

func TestUpdateRefusesASkillWithoutASourceFile(t *testing.T) {
	f := newMarketplaceFixture(t)
	handmade := filepath.Join(f.skillsDir, "handmade")
	if err := os.MkdirAll(handmade, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handmade, "SKILL.md"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rr := f.post(t, "marketplace/update", `{"skill":"handmade"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("update of a hand-made skill: %d %s", rr.Code, rr.Body.String())
	}
	if rr := f.post(t, "marketplace/update", `{"skill":"../escape"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("update of a path: %d %s", rr.Code, rr.Body.String())
	}
}

// assertNoUpdateLeftovers checks that no staged or set-aside folder remains.
func (f *marketplaceFixture) assertNoUpdateLeftovers(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(f.skillsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".ori-") {
			t.Errorf("an update left %s in the Skills folder", entry.Name())
		}
	}
}
