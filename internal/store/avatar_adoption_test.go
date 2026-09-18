package store

import (
	"os"
	"path/filepath"
	"testing"
)

// withUploadedImage creates an agent whose appearance names filename.
func withUploadedImage(t *testing.T, st Store, name, filename string) {
	t.Helper()
	if err := st.CreateAgent(name, nil); err != nil {
		t.Fatalf("CreateAgent %s: %v", name, err)
	}
	ag, _ := st.GetAgent(name)
	ag.EnsureAppearance()
	ag.Appearance.SetUpload(filename)
	if err := st.SetAgent(name, ag); err != nil {
		t.Fatalf("SetAgent %s: %v", name, err)
	}
}

func writeImage(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
}

func readImage(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestAnOlderBuildsImageMovesIntoTheAgentsFolder(t *testing.T) {
	f := newCompositeFixture(t)
	c := f.open(t)
	shared := filepath.Join(f.dataDir, "agent_avatars")
	withUploadedImage(t, c, "Scout", "Scout.png")
	writeImage(t, shared, "Scout.png", "scout")

	adopted, err := c.AdoptLegacyAvatars(shared)
	if err != nil || len(adopted) != 1 || adopted[0] != "Scout" {
		t.Fatalf("adopted = %v, %v; want [Scout]", adopted, err)
	}
	if got := readImage(t, filepath.Join(f.root, "Agents", "Scout", "Scout.png")); got != "scout" {
		t.Errorf("image in the agent's folder = %q", got)
	}
	if _, err := os.Stat(filepath.Join(shared, "Scout.png")); !os.IsNotExist(err) {
		t.Error("the shared copy should be gone once the image moved")
	}

	// A second run finds every image in place and does nothing.
	again, err := c.AdoptLegacyAvatars(shared)
	if err != nil || len(again) != 0 {
		t.Errorf("second run = %v, %v; want nothing to do", again, err)
	}
}

func TestImageMoveNeverOverwritesAndKeepsSharedImages(t *testing.T) {
	f := newCompositeFixture(t)
	c := f.open(t)
	shared := filepath.Join(f.dataDir, "agent_avatars")

	// Scout already has its own image; the shared file is an older one.
	withUploadedImage(t, c, "Scout", "Scout.png")
	writeImage(t, filepath.Join(f.root, "Agents", "Scout"), "Scout.png", "current")
	writeImage(t, shared, "Scout.png", "older")

	// Quill's image is also named by an agent still in the data dir, so the
	// shared file must stay.
	withUploadedImage(t, c, "Quill", "shared.png")
	withUploadedImage(t, c.system, "Legacy", "shared.png")
	writeImage(t, shared, "shared.png", "both")

	// Hand-edited to name a non-image; it is never touched.
	withUploadedImage(t, c, "Mallory", "agent_settings.json")

	adopted, err := c.AdoptLegacyAvatars(shared)
	if err != nil || len(adopted) != 1 || adopted[0] != "Quill" {
		t.Fatalf("adopted = %v, %v; want [Quill]", adopted, err)
	}
	if got := readImage(t, filepath.Join(f.root, "Agents", "Scout", "Scout.png")); got != "current" {
		t.Errorf("Scout's own image was overwritten: %q", got)
	}
	if got := readImage(t, filepath.Join(f.root, "Agents", "Quill", "shared.png")); got != "both" {
		t.Errorf("Quill's copy = %q", got)
	}
	if got := readImage(t, filepath.Join(shared, "shared.png")); got != "both" {
		t.Errorf("the data-dir agent's image left the shared folder: %q", got)
	}
}

func TestImageMoveWithoutASharedFolderIsANoOp(t *testing.T) {
	f := newCompositeFixture(t)
	c := f.open(t)
	withUploadedImage(t, c, "Scout", "Scout.png")
	adopted, err := c.AdoptLegacyAvatars(filepath.Join(f.dataDir, "agent_avatars"))
	if err != nil || len(adopted) != 0 {
		t.Errorf("adopted = %v, %v; want nothing", adopted, err)
	}
}
