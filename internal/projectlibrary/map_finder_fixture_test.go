package projectlibrary

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Export ordinary portable workspaces for the real import API. No running
// database is edited. The provider pin comes from the sandbox's actual install.
func TestExportMapFinderFixture(t *testing.T) {
	raw := os.Getenv("ORI_MUSIC_FINDER_SANDBOX")
	if raw == "" {
		t.Skip("disposable Music Home demo only")
	}
	owner, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	sandbox, err := browserFixtureSandbox(raw, owner)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(sandbox, "finder-fixture")
	if err = os.Mkdir(root, 0o750); err != nil {
		t.Fatal(err)
	}
	plugins, err := plugin.NewStore(filepath.Join(sandbox, "plugins")).List()
	if err != nil {
		t.Fatal(err)
	}
	var music *plugin.InstalledPlugin
	for i := range plugins {
		if plugins[i].Name == "music-project-management" && plugins[i].Enabled {
			music = &plugins[i]
		}
	}
	if music == nil || music.WorkspaceSurfaces == nil || len(music.WorkspaceSurfaces.AssistantProgramHomes) != 1 {
		t.Fatal("stage the Music Home demo package first")
	}
	decl := music.WorkspaceSurfaces.AssistantProgramHomes[0]
	scope := Scope{HomeID: "finder-home", OwnerUserID: "local", ProgramID: decl.ID, ProviderID: music.Name}
	pin := &workspace.AssistantProgramHomeOwner{PluginID: music.Name, PluginVersion: music.Version, ProgramID: decl.ID, HomeSchemaVersion: decl.SchemaVersion, HomeVersion: decl.Version, DeclarationDigest: projecttemplates.AssistantProgramHomeDigest(decl), PluginGeneration: music.EvidenceGeneration(), ComponentFingerprint: music.ComponentFingerprint}
	if !plugin.IndependentHomeProviderEvidenceAvailable(plugins, pin) {
		t.Fatal("provider receipt unavailable")
	}
	folders, err := workspace.NewFileStore(filepath.Join(root, "exports"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = folders.Close() }()
	key := workspace.AssistantProgramKey{OwnerUserID: scope.OwnerUserID, PluginID: music.Name, ProgramID: decl.ID}
	home := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Music Production Home"})
	home.ID, home.OwnerUserID, home.Kind = scope.HomeID, "local", "group"
	home.SetAssistantProgramState(&workspace.AssistantProgramState{SchemaVersion: workspace.AssistantProgramStateSchemaVersion, Key: key, StateRevision: 1, PluginAvailable: true, Declaration: decl.AssistantProgram(), HomeProvider: pin, StageID: "helper", Level: 1, PrimaryName: decl.DefaultPrimaryName})
	if err = folders.Save(home); err != nil {
		t.Fatal(err)
	}
	library := NewStore(folders)
	initializeLibrary(t, library, scope)
	doc, err := library.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	source := filepath.Join(root, "Songs")
	mkdir := func(path string) {
		t.Helper()
		if err := os.MkdirAll(path, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	identity := func(path string) string {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		id, err := directoryIdentity(info)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	mkdir(source)
	doc.Roots = []Root{{ID: "songs", Path: source, FileIdentity: identity(source), Revision: 1, ApprovedAt: now}}
	doc.Scans = []Scan{{ID: "listed", RootID: "songs", RootRevision: 1, RootDigest: strings.Repeat("0", 64), ResultDigest: strings.Repeat("0", 64), Status: "complete", StartedAt: now, FinishedAt: &now}}
	for i := range 30 {
		id := fmt.Sprintf("early-%02d", i)
		doc.Entries = append(doc.Entries, Entry{ID: id, Revision: 1, Fields: Fields{DisplayName: fmt.Sprintf("Early project %02d", i)}, Observations: []Observation{{RootID: "songs", RelativeFolder: id, FileIdentity: id, Format: "reaper", Availability: "unavailable", ScanID: "listed", ScannedAt: now}}})
	}
	child := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "EP workspace"})
	child.ID, child.OwnerUserID = "finder-child", "local"
	link := workspace.AssistantProjectLinkID(home.ID, child.ID)
	child.SetAssistantProjectLink(&workspace.AssistantProjectLink{SchemaVersion: workspace.AssistantProjectLinkSchemaVersion, ID: link, StationWorkspaceID: home.ID, Key: key, StateRevision: 1})
	if err = folders.Save(child); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"EP", "Demo", "Unsupported", "Offline"} {
		rel := v + "/Night Drive"
		dir := filepath.Join(source, rel)
		mkdir(dir)
		files := []string{"Take.rpp"}
		name := "Night Drive"
		format, availability := "reaper", "available"
		if v == "Demo" {
			files = append(files, "Alternate.rpp")
			availability = "ambiguous"
		}
		if v == "Unsupported" {
			name = "Other format"
			format = "ableton"
			files = []string{"Take.als"}
		}
		if v == "Offline" {
			availability = "unavailable"
			name = "Offline song"
		}
		for _, file := range files {
			if err := os.WriteFile(filepath.Join(dir, file), []byte("fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		e := Entry{ID: v, Revision: 1, Fields: Fields{DisplayName: name}, Observations: []Observation{{RootID: "songs", RelativeFolder: rel, FileIdentity: identity(dir), Format: format, Alternates: files, ScanID: "listed", ScannedAt: now, FileModifiedAt: now, Availability: availability}}}
		if v == "EP" {
			e.Fields.Stage = "mixing"
			e.Fields.NextAction = "Comp the bridge."
			e.Link = &ExactLink{WorkspaceID: child.ID, LinkID: link, Revision: 1}
		}
		doc.Entries = append(doc.Entries, e)
	}
	doc.Sessions = []StudioSession{{ID: "session", EntryID: "EP", Revision: 1, Goal: "Pick a take", Recap: "Kept take 3", State: "accepted", Author: "local", UpdatedAt: now, AcceptedAt: &now}}
	doc.Revision++
	if !doc.valid(scope) {
		t.Fatal("invalid fixture")
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err = folders.Update(home.ID, func(ws *workspace.Workspace) error {
		s := ws.GetAssistantProgramState()
		s.ProjectLibrary = data
		s.LinkedProjectIDs = []string{child.ID}
		ws.SetAssistantProgramState(s)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, ws := range []*workspace.Workspace{home, child} {
		path, err := folders.GetFolderPath(ws.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s=%s", ws.ID, path)
	}
}
