package projectstaffing

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeAgents map[string]bool

func (f fakeAgents) ListAgents() []string {
	names := make([]string, 0, len(f))
	for name := range f {
		names = append(names, name)
	}
	return names
}

func (f fakeAgents) GetAgent(name string) (*agent.Agent, bool) {
	if f[name] {
		return &agent.Agent{}, true
	}
	return nil, false
}

var digest = strings.Repeat("a", 64)

func provider(d string) *workspace.AssistantProjectProviderOwner {
	return &workspace.AssistantProjectProviderOwner{
		PluginID: "ori-reaper", PluginVersion: "0.9.0", BlueprintID: "reaper-song", BlueprintVersion: 1,
		ProjectTeamID: "team", ProjectTeamSchema: 1, ProjectTeamVersion: 1, ProjectTeamDigest: d,
		PluginGeneration: 1, ComponentFingerprint: strings.Repeat("b", 64),
	}
}

func fixture(t *testing.T) (workspace.Store, *Service, fakeAgents) {
	t.Helper()
	store := workspace.NewInMemoryStore()
	seedHome(t, store)
	agents := fakeAgents{}
	return store, New(store, agents), agents
}

// seedHome saves a Home and two songs linked to it.
func seedHome(t *testing.T, store workspace.Store) {
	t.Helper()
	home := &workspace.Workspace{ID: "home", Name: "Home", Status: workspace.StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	home.SetAssistantProgramState(&workspace.AssistantProgramState{SchemaVersion: workspace.AssistantProgramStateSchemaVersion})
	if err := store.Save(home); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"song-a", "song-b"} {
		song := &workspace.Workspace{ID: id, Name: id, Status: workspace.StatusActive, CreatedAt: time.Now(), UpdatedAt: time.Now()}
		song.SetAssistantProjectLink(&workspace.AssistantProjectLink{
			StationWorkspaceID: "home", ProjectProvider: provider(digest),
			ProjectRoles: []workspace.AssistantProgramRoleSpec{{ID: "reaper-assistant", Label: "REAPER Assistant", Scope: workspace.AssistantRoleScopeProject, Required: true}},
		})
		if err := store.Save(song); err != nil {
			t.Fatal(err)
		}
	}
}

// bindInSong records a role filled in a song, as the staffing adapter would.
func bindInSong(t *testing.T, store workspace.Store, songID, name string, source string) {
	t.Helper()
	if err := store.Update(songID, func(song *workspace.Workspace) error {
		song.AgentInstances = append(song.AgentInstances, workspace.AgentInstance{ID: "i-" + songID, Name: name, RoleSource: source})
		link := song.GetAssistantProjectLink()
		link.ProjectBindings.Bindings = append(link.ProjectBindings.Bindings, workspace.AssistantRoleBinding{RoleID: "reaper-assistant", AgentInstanceID: "i-" + songID, AgentName: name})
		song.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFillWithoutAGrantNeverStaffsWithoutConsent(t *testing.T) {
	_, service, _ := fixture(t)
	if _, err := service.Fill("song-a", []string{"reaper-assistant"}, nil); !errors.Is(err, ErrNoConsent) {
		t.Fatalf("no consent, no grant: %v", err)
	}
	if consent, _ := service.Consents().Read("home"); consent != nil {
		t.Fatalf("a read-only fill recorded a consent: %+v", consent)
	}
}

func TestFillGrantsCreatesThenBinds(t *testing.T) {
	store, service, agents := fixture(t)
	grant := &Grant{Source: workspace.ProjectStaffingConsentFolderOffer, OfferID: "offer-1"}
	fills, err := service.Fill("song-a", []string{"reaper-assistant"}, grant)
	if err != nil || len(fills) != 1 || fills[0].Mode != ModeCreate || fills[0].Name != "REAPER Assistant" {
		t.Fatalf("first fill = %+v %v", fills, err)
	}
	// The create happened (the staffing adapter's job), then Settle records it.
	agents["REAPER Assistant"] = true
	bindInSong(t, store, "song-a", "REAPER Assistant", workspace.RoleSourceCreated)
	if err := service.Settle("song-a"); err != nil {
		t.Fatal(err)
	}
	fills, err = service.Fill("song-b", []string{"reaper-assistant"}, nil)
	if err != nil || len(fills) != 1 || fills[0].Mode != ModeBind || fills[0].Name != "REAPER Assistant" {
		t.Fatalf("second fill = %+v %v", fills, err)
	}
}

// Settle records only the reserved name, created in that song: never an agent
// the user assigned by hand, never another name.
func TestSettleRecordsOnlyTheConsentsOwnCreate(t *testing.T) {
	for name, tc := range map[string]struct {
		agent  string
		source string
	}{
		"assigned by hand": {"REAPER Assistant", workspace.RoleSourceAssigned},
		"another name":     {"Producer · song-a", workspace.RoleSourceCreated},
	} {
		t.Run(name, func(t *testing.T) {
			store, service, _ := fixture(t)
			if _, err := service.Fill("song-a", []string{"reaper-assistant"}, &Grant{Source: workspace.ProjectStaffingConsentFolderOffer}); err != nil {
				t.Fatal(err)
			}
			bindInSong(t, store, "song-a", tc.agent, tc.source)
			if err := service.Settle("song-a"); err != nil {
				t.Fatal(err)
			}
			consent, _ := service.Consents().Read("home")
			if consent.Roles[0].AgentName != "" || consent.Roles[0].PendingName != "REAPER Assistant" {
				t.Fatalf("recorded something the consent did not create: %+v", consent.Roles[0])
			}
		})
	}
}

func TestFillRegrantsAfterTheSwitchWasTurnedOff(t *testing.T) {
	_, service, _ := fixture(t)
	if _, err := service.Fill("song-a", []string{"reaper-assistant"}, &Grant{Source: workspace.ProjectStaffingConsentFolderOffer}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Consents().Revoke("home"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Fill("song-b", []string{"reaper-assistant"}, nil); !errors.Is(err, ErrNoConsent) {
		t.Fatalf("switched off: %v", err)
	}
	// The card's Set up said it turns sharing back on.
	if _, err := service.Fill("song-b", []string{"reaper-assistant"}, &Grant{Source: workspace.ProjectStaffingConsentFolderOffer}); err != nil {
		t.Fatal(err)
	}
	if consent, _ := service.Consents().Read("home"); !consent.Active() {
		t.Fatalf("not switched back on: %+v", consent)
	}
}

// originAgents is an agent store that knows where each name comes from.
type originAgents struct {
	fakeAgents
	workspaceOnly map[string]bool
}

func (o originAgents) AgentOrigin(name string) (store.AgentOrigin, bool) {
	if o.workspaceOnly[name] {
		return store.AgentOrigin{Source: store.SourceWorkspace, WorkspaceID: "song-a"}, true
	}
	if o.fakeAgents[name] {
		return store.AgentOrigin{Source: store.SourceRoster}, true
	}
	return store.AgentOrigin{}, false
}

// D3 after a delete: a song's own leftover copy of the assistant is not the
// assistant, so the consent stops (assistant_missing) instead of binding it.
func TestFillNeverBindsASongsLeftoverCopy(t *testing.T) {
	st, _, _ := fixture(t)
	agents := originAgents{fakeAgents: fakeAgents{}, workspaceOnly: map[string]bool{}}
	service := New(st, agents)
	grant := &Grant{Source: "folder_offer"}
	if fills, err := service.Fill("song-a", []string{"reaper-assistant"}, grant); err != nil || fills[0].Name != "REAPER Assistant" {
		t.Fatalf("first fill = %+v %v", fills, err)
	}
	agents.fakeAgents["REAPER Assistant"] = true
	bindInSong(t, st, "song-a", "REAPER Assistant", "created")
	if err := service.Settle("song-a"); err != nil {
		t.Fatal(err)
	}
	if fills, err := service.Fill("song-b", []string{"reaper-assistant"}, nil); err != nil || fills[0].Mode != ModeBind {
		t.Fatalf("while it is in the roster = %+v %v", fills, err)
	}
	// Deleted from the roster; only a song's own copy is left under the name.
	agents.workspaceOnly["REAPER Assistant"] = true
	if service.AgentExists("REAPER Assistant") {
		t.Fatal("a workspace-only copy counted as the assistant")
	}
	if _, err := service.Fill("song-b", []string{"reaper-assistant"}, nil); !errors.Is(err, ErrAssistantMissing) {
		t.Fatalf("after the delete = %v", err)
	}
}

func TestFillRefusesWhatItCannotShare(t *testing.T) {
	store, service, _ := fixture(t)
	if err := store.Update("song-b", func(song *workspace.Workspace) error {
		link := song.GetAssistantProjectLink()
		link.ProjectProvider = nil // a combined program: no team digest to scope a consent to
		song.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Fill("song-b", []string{"reaper-assistant"}, &Grant{Source: workspace.ProjectStaffingConsentFolderOffer}); !errors.Is(err, ErrNotShared) {
		t.Fatalf("combined program: %v", err)
	}
	if _, err := service.Fill("song-a", []string{"mixer"}, &Grant{Source: workspace.ProjectStaffingConsentFolderOffer}); !errors.Is(err, ErrNotShared) {
		t.Fatalf("undeclared role: %v", err)
	}
	if _, err := service.Fill("missing", []string{"reaper-assistant"}, nil); !errors.Is(err, ErrNotShared) {
		t.Fatalf("missing song: %v", err)
	}
}
