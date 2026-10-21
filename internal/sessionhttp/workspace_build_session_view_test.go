package sessionhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

type scopedBuildViewFolders struct {
	*fakeBuildFolders
	target foldercontext.Target
}

func (f *scopedBuildViewFolders) ResolveRoot(ctx context.Context, target foldercontext.Target, id string) (string, error) {
	if target != f.target || id != f.observation.ID {
		return "", personalassistant.ErrFolderSelection
	}
	return f.fakeBuildFolders.ResolveRoot(ctx, target, id)
}

func TestWorkspaceBuildView_FolderDetailsAreOwnerScopedAndNeverStoredOrSharedWithModel(t *testing.T) {
	f := newBuildFixture(t, false, describeReply)
	folders := &fakeBuildFolders{observation: testObservation(), root: "/private/synthetic-parent/Elite School"}
	folders.observation.ScannedAt = f.store.Now()
	f.withFolders(t, folders)
	build := f.start(t)
	f.handler.workspaceBuild.Folders = &scopedBuildViewFolders{folders, buildFolderTarget("local", f.assistant, build.ID)}
	code, body := f.attachFolder(t, build, map[string]any{"mode": "chip", "chip": "documents"})
	if code != http.StatusOK || body["session"].(map[string]any)["folder_path"] != folders.root {
		t.Fatalf("missing selected-folder details: %d %v", code, body)
	}
	attached := sessionOf(t, body)
	code, _ = f.do(t, http.MethodPost, "/api/workspaces/build-sessions/"+build.ID+"/turns", proposalRequest(attached, 1))
	if code != http.StatusOK || f.provider.calls != 1 {
		t.Fatalf("proposal: %d, calls %d", code, f.provider.calls)
	}
	doc, err := f.store.Read(context.Background(), "local")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	prompts, err := json.Marshal([]any{f.provider.systems, f.provider.messages})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), folders.root) || strings.Contains(string(prompts), folders.root) {
		t.Fatal("local display path entered storage or model context")
	}
	for _, lost := range []string{"selection lost on restart", "assistant scope changed"} {
		if lost == "selection lost on restart" {
			folders.rootErr = personalassistant.ErrFolderSelection
		} else {
			folders.rootErr = nil
			f.assistant.HQWorkspaceID = "another-hq"
		}
		code, body = f.do(t, http.MethodGet, "/api/workspaces/build-sessions/"+build.ID, nil)
		view := body["session"].(map[string]any)
		expires, parseErr := time.Parse(time.RFC3339, view["folder_expires_at"].(string))
		if code != http.StatusOK || view["folder_path"] != nil || parseErr != nil || expires.After(f.store.Now()) {
			t.Fatalf("%s did not invalidate the held selection: %d %v", lost, code, body)
		}
		if f.provider.calls != 1 || sessionOf(t, body).Draft.Name != doc.Session(build.ID).Draft.Name {
			t.Fatal("reading an unavailable selection retried inference or lost the draft")
		}
	}
}
