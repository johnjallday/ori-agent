package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
)

// An endpoint demo of current evidence, not an implemented arrangement UI. The
// fixture builds production routes and overrides HOME/data before setup.
func TestArrangementCharacterization_RealHostFlatAndNestedAttachments(t *testing.T) {
	f := newDraftServerFixture(t)
	provider := &capturingChatProvider{}
	f.builder.llmFactory.Register("claude_code", provider)
	flat := seedAlbumsCollection(t)
	nested := filepath.Join(os.Getenv("HOME"), "Desktop")
	file := filepath.Join(nested, "Albums", "Album A", "Song 1", "song.rpp")
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("synthetic source; never read by attachment"), 0o600); err != nil {
		t.Fatal(err)
	}
	count := len(f.builder.workspaceFileStore.CachedWorkspaces())
	for _, tc := range []struct{ chip, root string }{{"music", flat}, {"desktop", nested}} {
		t.Run(tc.chip, func(t *testing.T) {
			before := folderTreeHash(t, tc.root)
			code, body := f.call(t, http.MethodPost, "/api/home-assistant/folder-context/select", map[string]any{
				"draft_id": uuid.NewString(), "revision": "", "mode": "chip", "chip": tc.chip,
			})
			if code != http.StatusOK {
				t.Fatalf("select: %d %v", code, body)
			}
			encoded, err := json.Marshal(body)
			if err != nil || strings.Contains(string(encoded), tc.root) || strings.Contains(string(encoded), "synthetic source") {
				t.Fatalf("private evidence in response: %s %v", encoded, err)
			}
			var response struct {
				Observation *foldercontext.Observation `json:"observation"`
			}
			if err := json.Unmarshal(encoded, &response); err != nil || response.Observation == nil || response.Observation.Validate() != nil {
				t.Fatalf("missing observation: %s %v", encoded, err)
			}
			if tc.chip == "desktop" {
				if len(response.Observation.Projects) != 2 || !strings.Contains(string(encoded), "Song 1") {
					t.Fatalf("nested metadata missing: %s", encoded)
				}
				if _, present := body["structure"]; present {
					t.Fatal("nested tree was misrepresented as an executable song collection")
				}
			}
			if folderTreeHash(t, tc.root) != before || len(provider.requests) != 0 || len(f.builder.workspaceFileStore.CachedWorkspaces()) != count {
				t.Fatal("preview changed files, called a model or created a workspace")
			}
			t.Logf("observed metadata only; no source changes/model/workspace effects; source_hash=%s response=%s", before, encoded)
		})
	}
}
