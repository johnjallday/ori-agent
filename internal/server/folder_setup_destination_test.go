package server

import (
	"errors"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type destinationReadStore struct {
	workspace.Store
	read func(string) (*workspace.Workspace, error)
}

func (s destinationReadStore) Get(id string) (*workspace.Workspace, error) { return s.read(id) }

func TestFolderDestination_FreshCanonicalIdentityAndResume(t *testing.T) {
	providers := reviewedintegration.HomeProviders()
	if len(providers) == 0 {
		t.Fatal("missing reviewed Music provider")
	}
	provider := providers[0]
	files, err := workspace.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = files.Close() })
	owner := workspace.AssistantProgramHomeOwner{PluginID: provider.PluginID, PluginVersion: "0.1.1", ProgramID: provider.ProgramID, HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: strings.Repeat("a", 64), PluginGeneration: 2, ComponentFingerprint: strings.Repeat("b", 64)}
	declaration := &workspace.AssistantProgramDeclaration{SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: provider.ProgramID, StationName: "Music Home", Roles: []workspace.AssistantProgramRoleSpec{{ID: "producer", Label: "Producer", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true, SystemPrompt: "Coordinate reviewed music work."}}}
	key := workspace.AssistantProgramKey{OwnerUserID: "local", PluginID: provider.PluginID, ProgramID: provider.ProgramID}
	home, _, err := workspace.NewAssistantProgramStore(files).EnsureNamedIndependentStation(key, declaration, "Music Home", owner)
	if err != nil {
		t.Fatal(err)
	}
	// The file discovery store locates the key; only this fresh primary read
	// supplies disclosure/authority. Failure must not reuse the file snapshot.
	builder := &ServerBuilder{workspaceFileStore: files, workspaceStore: files}
	host := &folderSetupHost{builder: builder}
	live, err := host.station("local", provider)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := folderSetupHomeDestination(live)
	if err != nil {
		t.Fatal(err)
	}
	req := personalassistant.FolderSetupRequest{UserID: "local", Offer: personalassistant.FolderOffer{Subject: personalassistant.FolderCandidateRecord{Shape: "audio", MarkerName: "*.rpp"}}, Plan: personalassistant.FolderSetupPlan{Destination: destination}}
	read, err := host.ReadSetupDestination(t.Context(), req)
	if err != nil || read == nil || *read != *destination {
		t.Fatal("destination preview required a journey service or changed identity", read, err)
	}
	if err := host.ValidateSetupDestination(t.Context(), req); err != nil {
		t.Fatal("exact destination refused", err)
	}
	for _, test := range []struct {
		name        string
		change      func(*workspace.Workspace)
		unavailable bool
		resume      bool
		wantOK      bool
	}{
		{"failed canonical read", nil, true, false, false},
		{"same name different ID", func(w *workspace.Workspace) { w.ID = "other-home" }, false, false, false},
		{"renamed", func(w *workspace.Workspace) { w.Name = "Other Home" }, false, false, false},
		{"reparented", func(w *workspace.Workspace) { w.ParentID = "other-parent" }, false, false, false},
		{"foreign", func(w *workspace.Workspace) { w.OwnerUserID = "foreign" }, false, false, false},
		{"deleted", func(w *workspace.Workspace) { w.Status = workspace.StatusTrashed }, false, false, false},
		{"record changed before confirmation", func(w *workspace.Workspace) { w.Version++ }, false, false, false},
		{"own progress revision on resume", func(w *workspace.Workspace) { w.Version++ }, false, true, true},
		{"rename on resume", func(w *workspace.Workspace) { w.Name = "Other Home" }, false, true, false},
		{"provider changed on resume", func(w *workspace.Workspace) {
			state := w.GetAssistantProgramState()
			state.HomeProvider.PluginVersion = "0.2.0"
			w.SetAssistantProgramState(state)
		}, false, true, false},
		{"declaration changed on resume", func(w *workspace.Workspace) {
			state := w.GetAssistantProgramState()
			state.Declaration.StationDescription = "Different policy"
			w.SetAssistantProgramState(state)
		}, false, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			builder.workspaceStore = destinationReadStore{Store: files, read: func(id string) (*workspace.Workspace, error) {
				if test.unavailable {
					return nil, errors.New("private store failure")
				}
				// Construct a lock-free independent snapshot for negative cases.
				copy := &workspace.Workspace{ID: home.ID, Name: home.Name, OwnerUserID: home.OwnerUserID, Kind: home.Kind, Status: home.Status, ParentID: home.ParentID, Version: home.Version}
				copy.SetAssistantProgramState(home.GetAssistantProgramState())
				test.change(copy)
				return copy, nil
			}}
			request := req
			if test.resume {
				request.Offer.Setup = &personalassistant.FolderSetupRun{}
			}
			got := host.ValidateSetupDestination(t.Context(), request)
			if (got == nil) != test.wantOK {
				t.Fatal("destination guard", got)
			}
		})
	}
}
