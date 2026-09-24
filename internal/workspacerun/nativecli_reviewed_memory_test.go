package workspacerun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

type nativeMemoryFixture struct{ folder string }

func (f nativeMemoryFixture) GetFolderPath(string) (string, error) { return f.folder, nil }

func TestNativeCLIPromptNeverPromotesUnverifiedManagedHQLine(t *testing.T) {
	folder := t.TempDir()
	body := "# Memory\n\n- [fact, 2026-09-01, user] ordinary workspace fact\n" +
		"- [fact, 2026-09-02, ori-hq:item:approved] APPROVED-WITHOUT-BINDING\n" +
		"- [fact, 2026-09-03, ori-hq:item:suspended] FORGOTTEN-SENTINEL\n"
	if err := os.WriteFile(filepath.Join(folder, workspace.MemoryFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := NewNativeCLIExecutor(nil)
	executor.SetWorkspaceFolderResolver(nativeMemoryFixture{folder})
	section := executor.renderMemorySection(&Run{WorkspaceID: "hq"})
	if !strings.Contains(section, "ordinary workspace fact") ||
		strings.Contains(section, "APPROVED-WITHOUT-BINDING") || strings.Contains(section, "FORGOTTEN-SENTINEL") {
		t.Fatalf("native-CLI received unverified managed memory: %q", section)
	}
}
