package folderagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/directoryid"
)

func TestProjectDiscoveryBoundedWitnessedEvidence(t *testing.T) {
	root := t.TempDir()
	info, _ := os.Stat(root)
	d := ProjectDiscovery{root, directoryid.Witness(info), "receipt"}
	if d.Witness == "" {
		t.Skip("physical identity unavailable")
	}
	for _, name := range []string{"A", "B", "C", "Empty"} {
		p := filepath.Join(root, "School", name, "Labs")
		if err := os.MkdirAll(p, 0750); err != nil {
			t.Fatal(err)
		}
		if name != "Empty" {
			if err := os.WriteFile(filepath.Join(filepath.Dir(p), "Syllabus.pdf"), nil, 0000); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.Symlink(filepath.Join(root, "School"), filepath.Join(root, "Alias")); err != nil {
		t.Fatal(err)
	}
	f, err := d.Folders()
	if err != nil || len(f.Folders) != 2 {
		t.Fatalf("chooser: %+v %v", f, err)
	}
	parent := f.Folders[1].Ref
	a, err := d.Find(t.Context(), parent, nil)
	if err != nil || len(a.Suggestions) != 2 || a.Partial || a.Suggestions[0].Syllabus != "Syllabus.pdf" {
		t.Fatalf("find: %+v %v", a, err)
	}
	ignored := map[string]bool{a.Suggestions[0].Identity: true}
	b, err := d.Find(t.Context(), parent, ignored)
	if err != nil || len(b.Suggestions) != 2 || ignored[b.Suggestions[0].Identity] {
		t.Fatal(b, err)
	}
	if err := os.Rename(filepath.Join(root, "School"), filepath.Join(root, "Old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "School"), 0750); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Find(t.Context(), parent, nil); err == nil {
		t.Fatal("replacement parent inherited the look")
	}
}
