package agenthttp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/store"
)

// rootScoutWithImage is a composite whose root agent Scout has uploaded a PNG.
func rootScoutWithImage(t *testing.T) (*store.CompositeStore, string) {
	t.Helper()
	isolateAvatarDir(t)
	root := t.TempDir()
	c := compositeForRoot(t, root)
	if err := c.CreateAgent("Scout", &store.CreateAgentConfig{SystemPrompt: "hi"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	uploadImage(t, c, "Scout", pngBytes)
	return c, root
}

func uploadImage(t *testing.T, st store.Store, name string, img []byte) string {
	t.Helper()
	rec := httptest.NewRecorder()
	NewAppearanceUploadHandler(st, nil).ServeHTTP(rec, uploadRequest(t, name, img, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload for %s: %d %s", name, rec.Code, rec.Body.String())
	}
	ag, _ := st.GetAgent(name)
	return ag.Appearance.UploadedImage()
}

func TestARootAgentsImageIsWrittenToItsOwnFolder(t *testing.T) {
	c, root := rootScoutWithImage(t)
	folder := filepath.Join(root, "Agents", "Scout")
	if _, err := os.Stat(filepath.Join(folder, "Scout.png")); err != nil {
		t.Fatalf("the image is not in the agent's folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.DefaultAgentAvatarsDir(), "Scout.png")); !os.IsNotExist(err) {
		t.Error("a root agent's image must not be written to the shared folder")
	}
	if data, ok := ReadAvatarFile(c, "Scout.png"); !ok || !bytes.Equal(data, pngBytes) {
		t.Error("the uploaded image is not served")
	}

	// Replacing it with another format removes the superseded file.
	if got := uploadImage(t, c, "Scout", gifBytes); got != "Scout.gif" {
		t.Fatalf("replacement filename = %q", got)
	}
	if _, err := os.Stat(filepath.Join(folder, "Scout.png")); !os.IsNotExist(err) {
		t.Error("the previous image was left behind")
	}
	if _, ok := ReadAvatarFile(c, "Scout.gif"); !ok {
		t.Error("the replacement is not served")
	}
}

func TestServingNeverReadsAnythingButAnImage(t *testing.T) {
	c, _ := rootScoutWithImage(t)
	for _, name := range []string{"agent_settings.json", "../Scout/Scout.png", "..", ".hidden.png", "", "Scout"} {
		if _, ok := ReadAvatarFile(c, name); ok {
			t.Errorf("ReadAvatarFile(%q) served something", name)
		}
	}
}

func TestAMissingImageIsNotFoundAndTheAgentIsNotRewritten(t *testing.T) {
	c, root := rootScoutWithImage(t)
	folder := filepath.Join(root, "Agents", "Scout")
	definition := filepath.Join(folder, "agent_settings.json")
	before, err := os.ReadFile(definition)
	if err != nil {
		t.Fatalf("read definition: %v", err)
	}
	if err := os.Remove(filepath.Join(folder, "Scout.png")); err != nil {
		t.Fatalf("remove image: %v", err)
	}

	if _, ok := ReadAvatarFile(c, "Scout.png"); ok {
		t.Error("a deleted image is still served")
	}
	c.ReloadRoot()
	c.ListAgents()
	after, err := os.ReadFile(definition)
	if err != nil {
		t.Fatalf("read definition: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the definition was rewritten after its image went missing:\nbefore %s\nafter  %s", before, after)
	}
}

func TestRenamingCarriesTheImageAndDeletingRemovesIt(t *testing.T) {
	c, root := rootScoutWithImage(t)
	if err := c.RenameAgent("Scout", "Ranger"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	moved := filepath.Join(root, "Agents", "Ranger", "Scout.png")
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("the image did not move with the agent: %v", err)
	}
	if _, ok := ReadAvatarFile(c, "Scout.png"); !ok {
		t.Error("the renamed agent's image is not served")
	}

	if err := c.DeleteAgent("Ranger"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(moved); !os.IsNotExist(err) {
		t.Error("deleting the agent left its image behind")
	}
}

func TestANewAgentReusingAnOldNameGetsItsOwnImageFilename(t *testing.T) {
	c, _ := rootScoutWithImage(t)
	if err := c.RenameAgent("Scout", "Ranger"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if err := c.CreateAgent("Scout", &store.CreateAgentConfig{SystemPrompt: "new"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	filename := uploadImage(t, c, "Scout", pngBytes)
	if filename == "Scout.png" {
		t.Fatal("the new Scout reused the filename Ranger still names")
	}
	ranger, _ := c.GetAgent("Ranger")
	if ranger.Appearance.UploadedImage() != "Scout.png" {
		t.Errorf("Ranger's image = %q", ranger.Appearance.UploadedImage())
	}
	if _, ok := ReadAvatarFile(c, filename); !ok {
		t.Error("the new Scout's image is not served")
	}
	if _, ok := ReadAvatarFile(c, "Scout.png"); !ok {
		t.Error("Ranger's image is no longer served")
	}
}

func TestTheSharedAvatarFolderIgnoresTheWorkingDirectory(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ORI_DATA_DIR", dataDir)
	want := filepath.Join(dataDir, "agent_avatars")
	if got := config.DefaultAgentAvatarsDir(); got != want {
		t.Fatalf("DefaultAgentAvatarsDir = %q, want %q", got, want)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if got := config.DefaultAgentAvatarsDir(); got != want {
		t.Errorf("after changing directory, DefaultAgentAvatarsDir = %q, want %q", got, want)
	}
}
