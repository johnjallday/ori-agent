package sessionhttp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/secondbrain"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

// notesTreeHash is a hash of every entry under dir: name, mode and bytes.
func notesTreeHash(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		line := rel + " " + info.Mode().String()
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path) // #nosec G304 -- the test's own temp tree
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			line += " " + hex.EncodeToString(sum[:])
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// TestCreateAnchoredWorkspace_AProjectOfASecondBrainIsAnOrdinaryAnchoredWorkspace
// is FR19: making Projects/<name> a workspace gives agents that one folder,
// read-only, and nothing of the rest of the notes; Ori's own state lives in
// the sidecar and the second brain is not written to.
func TestCreateAnchoredWorkspace_AProjectOfASecondBrainIsAnOrdinaryAnchoredWorkspace(t *testing.T) {
	h, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	parent := canonicalAnchorTestDir(t)
	creation, err := secondbrain.Creator{}.Create(parent, "Second Brain", secondbrain.Seeds{
		FollowUps: []secondbrain.SeedFollowUp{{Title: "Call the distributor"}},
		People:    []secondbrain.SeedPerson{{Name: "Dana Lee", How: "Dana Lee is your distributor."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	brain := creation.Path
	project := filepath.Join(brain, "Projects", "Launch")
	if err := os.MkdirAll(project, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "Plan.md"), []byte("# Launch plan\n\n- [ ] Book the venue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := notesTreeHash(t, brain)

	body, err := json.Marshal(map[string]any{"name": "Launch", "anchor_path": project})
	if err != nil {
		t.Fatal(err)
	}
	response, payload := postCreateWorkspace(t, h, string(body))
	if response.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	created := payload["folder"].(map[string]any)
	if created["shape"] != "anchored" {
		t.Fatalf("shape = %v, want anchored", created["shape"])
	}
	ws, err := h.workspaceStore.Get(created["id"].(string))
	if err != nil {
		t.Fatal(err)
	}

	// The anchor is exactly the project folder, not the notes folder around it.
	if len(ws.DirectoryReferences) != 1 || ws.DirectoryReferences[0].Path != project {
		t.Fatalf("anchor = %+v, want exactly %s", ws.DirectoryReferences, project)
	}
	if len(ws.MCPBindings) != 1 {
		t.Fatalf("bindings = %+v", ws.MCPBindings)
	}
	binding := ws.MCPBindings[0]
	roots, _ := binding.Config["roots"].([]any)
	if binding.ServerName != "filesystem" || binding.DefaultSideEffect != agentworkspace.SideEffectRead || !binding.Enabled ||
		len(roots) != 1 || roots[0] != project {
		t.Fatalf("binding = %+v, roots %v; want one read binding over %s", binding, roots, project)
	}
	for _, root := range roots {
		path, _ := root.(string)
		if path == brain || strings.HasPrefix(brain, path+string(filepath.Separator)) || strings.HasPrefix(path, filepath.Join(brain, "Inbox")) ||
			strings.HasPrefix(path, filepath.Join(brain, "Daily")) || strings.HasPrefix(path, filepath.Join(brain, "People")) {
			t.Fatalf("a root reaches the rest of the notes: %s", path)
		}
	}
	for _, forbidden := range []string{"write_file", "edit_file", "create_directory", "move_file", "delete_file", "copy_file"} {
		if slices.Contains(binding.AllowedTools, forbidden) {
			t.Fatalf("mutation tool granted: %s", forbidden)
		}
	}

	// Ori's own state is in the sidecar; nothing was written into the notes.
	sidecar, err := h.workspaceStore.GetFolderPath(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(sidecar, brain+string(filepath.Separator)) {
		t.Fatalf("the workspace's state is inside the second brain: %s", sidecar)
	}
	if after := notesTreeHash(t, brain); after != before {
		t.Fatalf("creating the workspace changed the notes.\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// A workspace over the whole notes folder would be a separate, deliberate
	// choice (nested anchors are allowed). Whatever happens to that request, it
	// does not widen the project workspace.
	body, _ = json.Marshal(map[string]any{"name": "Everything", "anchor_path": brain})
	_, _ = postCreateWorkspace(t, h, string(body))
	again, err := h.workspaceStore.Get(ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.MCPBindings[0].Config["roots"].([]any); len(got) != 1 || got[0] != project {
		t.Fatalf("the project workspace's roots changed: %v", got)
	}
}
