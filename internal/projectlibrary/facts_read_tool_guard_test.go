package projectlibrary

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// assistant-reads-projects FR30: the one tool that reads the listed songs is
// built for the read turn alone and offered to no other agent. That holds
// because no registry, tool provider or handler outside this package names it:
// a name that appears nowhere else cannot be offered anywhere else. This guard
// fails if production code elsewhere starts to.
func TestFactsReadTool_IsNamedOnlyByTheReadTurn(t *testing.T) {
	root := filepath.Join("..", "..")
	var offenders []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) == "projectlibrary" {
			return nil
		}
		data, err := os.ReadFile(path) // #nosec G304 -- walking this repository's own source tree
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "read_listed_songs") || strings.Contains(string(data), "FactsReadToolName") {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("the read tool is named outside the read turn, so another agent could be offered it: %v", offenders)
	}
}
