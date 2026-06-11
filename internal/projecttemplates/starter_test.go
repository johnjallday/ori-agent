package projecttemplates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureLibraryMaterializesStarters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "templates")
	if err := EnsureLibrary(dir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	templates, err := ListLibrary(dir)
	if err != nil {
		t.Fatalf("ListLibrary: %v", err)
	}
	ids := make([]string, 0, len(templates))
	for _, tpl := range templates {
		ids = append(ids, tpl.ID)
	}
	if len(templates) != 2 || ids[0] != "reaper-song" || ids[1] != "writing-project" {
		t.Fatalf("unexpected starter set: %v", ids)
	}
	if templates[0].Name != "Reaper Song" || templates[1].Name != "Writing Project" {
		t.Errorf("manifest names not applied: %+v", templates)
	}
	if len(templates[0].Tags) != 2 || templates[0].Tags[0] != "music" || templates[0].Tags[1] != "reaper" {
		t.Errorf("reaper starter tags not applied: %+v", templates[0].Tags)
	}
	if len(templates[1].Tags) != 1 || templates[1].Tags[0] != "writing" {
		t.Errorf("writing starter tags not applied: %+v", templates[1].Tags)
	}

	// Seed file with token name plus the dot-file under chapters/ made it out
	// of the embed (all: prefix) and onto disk.
	if _, err := os.Stat(filepath.Join(dir, "reaper-song", "{{name}}.rpp")); err != nil {
		t.Errorf("reaper seed missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "writing-project", "chapters", ".keep")); err != nil {
		t.Errorf("chapters/.keep missing: %v", err)
	}
}

func TestEnsureLibraryNeverOverwritesUserEdits(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "templates")
	if err := EnsureLibrary(dir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	edited := filepath.Join(dir, "reaper-song", "{{name}}.rpp")
	if err := os.WriteFile(edited, []byte("user-edited"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := EnsureLibrary(dir); err != nil {
		t.Fatalf("EnsureLibrary (second run): %v", err)
	}
	data, err := os.ReadFile(edited)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "user-edited" {
		t.Error("starter materialization overwrote a user edit")
	}
}

func TestStarterInstantiatesEndToEnd(t *testing.T) {
	// Generality gate (PRD success metric 2): both starters run through the
	// identical engine path; the music template is in no way special.
	libDir := filepath.Join(t.TempDir(), "templates")
	if err := EnsureLibrary(libDir); err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}

	for _, tc := range []struct {
		id       string
		wantFile string
	}{
		{"reaper-song", "midnight.rpp"},
		{"writing-project", "outline.md"},
	} {
		wsDir := t.TempDir()
		tpl, err := FindLibraryTemplate(libDir, tc.id)
		if err != nil {
			t.Fatalf("FindLibraryTemplate(%s): %v", tc.id, err)
		}
		rel, err := Instantiate(tpl.Path, wsDir, "Midnight")
		if err != nil {
			t.Fatalf("Instantiate(%s): %v", tc.id, err)
		}
		target := filepath.Join(wsDir, rel, tc.wantFile)
		if _, err := os.Stat(target); err != nil {
			t.Errorf("%s: expected %s: %v", tc.id, tc.wantFile, err)
		}
	}
}

func TestReaperSeedLooksLikeAnRPP(t *testing.T) {
	data, err := starterFS.ReadFile("starter/reaper-song/{{name}}.rpp")
	if err != nil {
		t.Fatalf("embedded seed missing: %v", err)
	}
	if !strings.HasPrefix(string(data), "<REAPER_PROJECT") {
		t.Errorf("seed does not open with <REAPER_PROJECT: %q", data)
	}
}
