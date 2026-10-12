package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func newAssistantFolderPaths(t *testing.T) (assistantFolderPaths, string, *workspace.InMemoryStore) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	for _, dir := range []string{
		filepath.Join(home, "Music", "Song Projects"),
		filepath.Join(home, ".ssh", "keys"),
		filepath.Join(home, "Ori Workspaces"),
		filepath.Join(home, "Library", "OriData"),
	} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	store := workspace.NewInMemoryStore()
	return assistantFolderPaths{
		guards: filejanitor.RootGuards{
			HomeDir: home, DataDir: filepath.Join(home, "Library", "OriData"), WorkspaceRoot: filepath.Join(home, "Ori Workspaces"),
		},
		selections: pathselection.NewStore(),
		workspaces: store,
	}, home, store
}

func TestAssistantFolderPaths_FindsTheFolderAMessagePointsAt(t *testing.T) {
	paths, home, _ := newAssistantFolderPaths(t)
	message := "my songs are in ~/Music/Song Projects, which is newest?"

	found, ok := paths.Find(message)
	if !ok || found.Path != filepath.Join(home, "Music", "Song Projects") || found.Name != "Song Projects" ||
		found.Text != "~/Music/Song Projects" || message[found.Start:found.End] != found.Text || found.Refusal != "" {
		t.Fatalf("found = %+v, %v", found, ok)
	}

	refused, ok := paths.Find("read ~/.ssh/keys")
	if !ok || refused.Path != "" || !strings.Contains(refused.Refusal, "passwords or keys") {
		t.Fatalf("refused = %+v, %v", refused, ok)
	}
	if _, ok := paths.Find("which of my songs is newest?"); ok {
		t.Fatal("a message without a path found a folder")
	}
}

// What the card confirms with is the selection the typed-path field issues: a
// link's is bound to its workspace, an explore's to none.
func TestAssistantFolderPaths_HoldIssuesTheSelectionTheCardConfirmsWith(t *testing.T) {
	paths, home, _ := newAssistantFolderPaths(t)
	songs := filepath.Join(home, "Music", "Song Projects")

	explore, err := paths.Hold(songs, "")
	if err != nil || explore == "" {
		t.Fatalf("Hold = %q, %v", explore, err)
	}
	if got, err := paths.selections.Resolve(explore); err != nil || got != songs {
		t.Fatalf("explore selection resolves to %q, %v", got, err)
	}

	link, err := paths.Hold(songs, "ws-1")
	if err != nil || link == "" || link == explore {
		t.Fatalf("Hold = %q, %v", link, err)
	}
	if got, err := paths.selections.ResolveFor(link, "ws-1"); err != nil || got != songs {
		t.Fatalf("link selection resolves to %q, %v", got, err)
	}
	// A link's selection is no good to another workspace, or to nobody.
	if _, err := paths.selections.ResolveFor(link, "ws-2"); err == nil {
		t.Fatal("a link selection resolved for another workspace")
	}
	if _, err := paths.selections.Resolve(link); err == nil {
		t.Fatal("a link selection resolved without its workspace")
	}

	// The folder is checked again when it is held: one that has gone, or one
	// Ori will not open, is never issued.
	for _, path := range []string{filepath.Join(home, "Music", "Gone"), filepath.Join(home, ".ssh", "keys")} {
		if token, err := paths.Hold(path, ""); err == nil || token != "" {
			t.Fatalf("Hold(%q) = %q, %v", path, token, err)
		}
	}
}

func TestAssistantFolderPaths_LinkedKnowsAFolderAPersonLinked(t *testing.T) {
	paths, home, store := newAssistantFolderPaths(t)
	songs := filepath.Join(home, "Music", "Song Projects")
	ws := &workspace.Workspace{ID: "ws-1", Name: "Night Drive", Status: workspace.StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ws.DirectoryReferences = []workspace.DirectoryReference{
		{ID: "dir-1", WorkspaceID: "ws-1", Name: "Songs", Path: songs + string(filepath.Separator)},
		// A capability's own folder is not a folder a person linked.
		{ID: "dir-2", WorkspaceID: "ws-1", Name: "Samples", Path: filepath.Join(home, "Music"), Purpose: "sample_library"},
	}
	if err := store.Save(ws); err != nil {
		t.Fatal(err)
	}

	if got := paths.LinkState("ws-1", songs); got != agenthttp.FolderLinked {
		t.Fatalf("a linked folder: state %q", got)
	}
	// The folder that holds the linked one is the collection around it; a
	// capability's own reference to it does not make it linked.
	if got := paths.LinkState("ws-1", filepath.Join(home, "Music")); got != agenthttp.FolderContainsLinked {
		t.Fatalf("the folder around a linked one: state %q", got)
	}
	if paths.LinkState("ws-2", songs) != "" || paths.LinkState("", songs) != "" || paths.LinkState("ws-1", "") != "" {
		t.Fatal("an unknown workspace or an empty path was reported linked")
	}
}
