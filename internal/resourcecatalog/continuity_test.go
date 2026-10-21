package resourcecatalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The workspace package cannot import this one, so it repeats the lock path
// and the scratch prefix. This keeps the copies equal.
func TestContinuityExclusionsMatchTheCatalogFiles(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "workspace", "continuity_folder_inventory.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, `"`+ScratchPrefix+`"`) {
		t.Fatalf("continuity does not skip the writer scratch %q", ScratchPrefix)
	}
	if !strings.Contains(text, `"`+CatalogDir+"/"+LockFile+`"`) {
		t.Fatalf("continuity does not skip the lock %q", CatalogDir+"/"+LockFile)
	}
}
