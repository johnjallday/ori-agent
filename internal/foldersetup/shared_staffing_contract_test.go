package foldersetup

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projectstaffing"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/specialist"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// These tests drive two songs through the REAL setup journey with the REAL
// staffing adapter and the REAL Home consent: only the project-connect and
// workspace-mode owners stay synthetic. They prove "hire once, assign many"
// end to end: the first song creates the assistant, the second binds it.

const (
	sharedHomeID  = "music-home"
	sharedRoleID  = "reaper-assistant"
	sharedLabel   = "REAPER Assistant"
	sharedProgram = "music-producer-assistant"
)

var sharedDigest = strings.Repeat("e", 64)

// homeProjectStep is the synthetic project owner, reporting the Home the
// project joined so the journey can scope its staffing.
type homeProjectStep struct{ stepAdapter }

func (a homeProjectStep) Read(ctx context.Context, scope setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
	read, err := a.stepAdapter.Read(ctx, scope)
	if read.Complete {
		read.Result.HomeWorkspaceID = sharedHomeID
	}
	return read, err
}

func (a homeProjectStep) Commit(ctx context.Context, scope setupjourney.ReadScope, action setupjourney.ActionID, input json.RawMessage, reviewed setupjourney.ActionReviewMaterial) (setupjourney.CanonicalResult, error) {
	result, err := a.stepAdapter.Commit(ctx, scope, action, input, reviewed)
	result.HomeWorkspaceID = sharedHomeID
	return result, err
}

type grantRecorder struct{ granted []string }

func (g *grantRecorder) Available(string) bool         { return true }
func (g *grantRecorder) AvailablePersonal(string) bool { return true }
func (g *grantRecorder) Grant(agent, skill string) error {
	g.granted = append(g.granted, agent+"/"+skill)
	return nil
}
func (g *grantRecorder) GrantPersonal(agent, skill string) error { return g.Grant(agent, skill) }
func (g *grantRecorder) Revoke(string, string) error             { return nil }

type sharedFixture struct {
	workspaces workspace.Store
	profiles   agentstore.Store
	grants     *grantRecorder
	service    *setupjourney.Service
	owners     *owners
	staffing   *projectstaffing.Service
}

func projectProvider(digest string) *workspace.AssistantProjectProviderOwner {
	return &workspace.AssistantProjectProviderOwner{
		PluginID: "ori-reaper", PluginVersion: "0.9.0", BlueprintID: "reaper-song", BlueprintVersion: 1,
		ProjectTeamID: "reaper-team", ProjectTeamSchema: 1, ProjectTeamVersion: 1, ProjectTeamDigest: digest,
		PluginGeneration: 1, ComponentFingerprint: strings.Repeat("f", 64),
	}
}

func newSharedFixture(t *testing.T) *sharedFixture {
	t.Helper()
	workspaces := workspace.NewInMemoryStore()
	profiles, err := agentstore.NewFileStore(filepath.Join(t.TempDir(), "agents.json"), types.Settings{Model: "gpt-4o-mini", Provider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	key := workspace.AssistantProgramKey{OwnerUserID: contractUser, PluginID: "music-project-management", ProgramID: sharedProgram}
	home := &workspace.Workspace{ID: sharedHomeID, Name: "Music Production Home", OwnerUserID: contractUser, Status: workspace.StatusActive,
		CreatedAt: time.Now(), UpdatedAt: time.Now()}
	home.SetAssistantProgramState(&workspace.AssistantProgramState{
		SchemaVersion: workspace.AssistantProgramStateSchemaVersion, StateRevision: 1, Key: key, PluginAvailable: true,
		HomeProvider: &workspace.AssistantProgramHomeOwner{PluginID: "music-project-management"},
		Declaration: &workspace.AssistantProgramDeclaration{
			SchemaVersion: workspace.AssistantProgramSchemaVersion, ID: sharedProgram, StationName: "Music Production Home",
			DefaultPrimaryName: "Portfolio Manager", HireTitle: "Staff",
			Roles: []workspace.AssistantProgramRoleSpec{{
				ID: "portfolio_manager", Label: "Portfolio Manager", Scope: workspace.AssistantRoleScopeHome,
				Required: true, Primary: true, Role: "orchestrator", Type: "tool_calling", SystemPrompt: "manage",
			}},
			Stages: []workspace.AssistantProgramStageSpec{{ID: "helper", Label: "Helper"}},
		},
	})
	if err := workspaces.Save(home); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"proj-1", "proj-2"} {
		song := &workspace.Workspace{ID: id, Name: "Song " + id, OwnerUserID: contractUser, Status: workspace.StatusActive,
			CreatedAt: time.Now(), UpdatedAt: time.Now()}
		song.SetAssistantProjectLink(&workspace.AssistantProjectLink{
			ID: workspace.AssistantProjectLinkID(sharedHomeID, id), SchemaVersion: workspace.AssistantProjectLinkSchemaVersion,
			StationWorkspaceID: sharedHomeID, Key: key, StateRevision: 1, ProjectProvider: projectProvider(sharedDigest),
			ProjectRoles: []workspace.AssistantProgramRoleSpec{{
				ID: sharedRoleID, Label: sharedLabel, Scope: workspace.AssistantRoleScopeProject, Required: true, Primary: true,
				Role: "specialist", Type: "tool_calling", SystemPrompt: "work on the song", Skills: []string{"reaper-files"},
			}},
		})
		if err := workspaces.Save(song); err != nil {
			t.Fatal(err)
		}
	}
	grants := &grantRecorder{}
	adapter := setupjourney.NewAssistantStaffingAdapter(workspaces, profiles, grants,
		func() (string, string) { return "openai", "gpt-4o-mini" }, nil)
	// The Home already has its Portfolio Manager.
	if err := adapter.StaffRoleOnWorkspace(context.Background(), sharedHomeID, []setupjourney.RoleFill{{RoleID: "portfolio_manager", Name: "Portfolio Manager"}}); err != nil {
		t.Fatalf("staff the Home: %v", err)
	}
	o := &owners{}
	service := contractServiceWith(t, o, map[specialist.SetupStepKind]contractStep{
		specialist.SetupStepProjectConnect:           homeProjectStep{stepAdapter{owners: o, kind: specialist.SetupStepProjectConnect}},
		specialist.SetupStepAssistantProgramStaffing: adapter,
	})
	return &sharedFixture{workspaces: workspaces, profiles: profiles, grants: grants, service: service, owners: o,
		staffing: projectstaffing.New(workspaces, profiles)}
}

func (f *sharedFixture) run(t *testing.T) Result {
	t.Helper()
	runner := &Runner{Journey: contractJourney{f.service}, Selections: fakeSelections{}, Progress: &recorder{},
		Shared: testShared{service: f.staffing}}
	result, err := runner.Run(context.Background(), testConfig())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result
}

func (f *sharedFixture) instance(t *testing.T, songID string) workspace.AgentInstance {
	t.Helper()
	song, err := f.workspaces.Get(songID)
	if err != nil || len(song.GetAgentInstances()) != 1 {
		t.Fatalf("%s instances = %+v %v", songID, song.GetAgentInstances(), err)
	}
	return song.GetAgentInstances()[0]
}

func (f *sharedFixture) consent(t *testing.T) *workspace.ProjectStaffingConsent {
	t.Helper()
	consent, err := f.staffing.Consents().Read(sharedHomeID)
	if err != nil || consent == nil {
		t.Fatalf("consent = %+v %v", consent, err)
	}
	return consent
}

// testShared is the host's adapter: Set up on the card is the consent.
type testShared struct{ service *projectstaffing.Service }

func (s testShared) Fill(_ context.Context, projectID string, roleIDs []string) ([]projectstaffing.Fill, error) {
	return s.service.Fill(projectID, roleIDs, &projectstaffing.Grant{Source: workspace.ProjectStaffingConsentFolderOffer, OfferID: "offer-1"})
}

func (s testShared) Settle(_ context.Context, projectID string) error {
	return s.service.Settle(projectID)
}

func reaperAssistants(profiles agentstore.Store) []string {
	var names []string
	for _, name := range profiles.ListAgents() {
		if strings.HasPrefix(name, sharedLabel) {
			names = append(names, name)
		}
	}
	return names
}

func TestRealJourneyHiresTheAssistantOnceAndSharesIt(t *testing.T) {
	f := newSharedFixture(t)
	first := f.run(t)
	if first.Status != personalassistant.FolderSetupDone {
		t.Fatalf("first song = %+v cause=%v", first, first.Cause)
	}
	created := f.instance(t, "proj-1")
	if created.Name != sharedLabel || created.RoleSource != workspace.RoleSourceCreated {
		t.Fatalf("the first song must create %q: %+v", sharedLabel, created)
	}
	consent := f.consent(t)
	if !consent.Active() || consent.Source != workspace.ProjectStaffingConsentFolderOffer || consent.OfferID != "offer-1" ||
		consent.TeamDigest != sharedDigest || consent.Roles[0].AgentName != sharedLabel || consent.Roles[0].PendingName != "" {
		t.Fatalf("consent after the first song = %+v", consent)
	}

	second := f.run(t) // the second folder: its own child run
	if second.Status != personalassistant.FolderSetupDone || second.RunID == first.RunID {
		t.Fatalf("second song = %+v cause=%v", second, second.Cause)
	}
	bound := f.instance(t, "proj-2")
	if bound.Name != sharedLabel || bound.RoleSource != workspace.RoleSourceAssigned {
		t.Fatalf("the second song must bind %q: %+v", sharedLabel, bound)
	}
	if names := reaperAssistants(f.profiles); len(names) != 1 {
		t.Fatalf("one assistant in the roster, got %v", names)
	}
	// Skills were granted once, by the create; a bind grants nothing.
	if len(f.grants.granted) != 1 || f.grants.granted[0] != sharedLabel+"/reaper-files" {
		t.Fatalf("grants = %v", f.grants.granted)
	}
	// The song keeps its own copy of the shared definition.
	if snapshot, found, err := f.workspaces.GetWorkspaceAgent("proj-2", sharedLabel); err != nil || !found || snapshot == nil {
		t.Fatalf("the bound song has no copy of the agent: %v %v", found, err)
	}
}

// D3: an agent the user already has under that name is never adopted.
func TestRealJourneyNeverAdoptsAStrangerWithTheSameName(t *testing.T) {
	f := newSharedFixture(t)
	if err := f.profiles.CreateAgent(sharedLabel, &agentstore.CreateAgentConfig{SystemPrompt: "my own customised agent"}); err != nil {
		t.Fatal(err)
	}
	if result := f.run(t); result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("first song = %+v cause=%v", result, result.Cause)
	}
	if created := f.instance(t, "proj-1"); created.Name != sharedLabel+" 2" || created.RoleSource != workspace.RoleSourceCreated {
		t.Fatalf("the create must use the next free name: %+v", created)
	}
	if result := f.run(t); result.Status != personalassistant.FolderSetupDone {
		t.Fatalf("second song = %+v cause=%v", result, result.Cause)
	}
	if bound := f.instance(t, "proj-2"); bound.Name != sharedLabel+" 2" || bound.RoleSource != workspace.RoleSourceAssigned {
		t.Fatalf("the second song must bind the consent's own agent: %+v", bound)
	}
	stranger, found := f.profiles.GetAgent(sharedLabel)
	if !found || stranger.Settings.SystemPrompt != "my own customised agent" {
		t.Fatalf("the user's own agent was touched: %+v", stranger)
	}
}

func TestRealJourneyStopsWhenTheConsentCannotApply(t *testing.T) {
	t.Run("the team changed", func(t *testing.T) {
		f := newSharedFixture(t)
		if result := f.run(t); result.Status != personalassistant.FolderSetupDone {
			t.Fatalf("first song = %+v", result)
		}
		if err := f.workspaces.Update("proj-2", func(song *workspace.Workspace) error {
			link := song.GetAssistantProjectLink()
			link.ProjectProvider = projectProvider(strings.Repeat("9", 64))
			song.SetAssistantProjectLink(link)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		result := f.run(t)
		if result.StopReason != personalassistant.FolderStopConsentStale {
			t.Fatalf("second song = %+v cause=%v", result, result.Cause)
		}
		song, _ := f.workspaces.Get("proj-2")
		if len(song.GetAgentInstances()) != 0 || len(reaperAssistants(f.profiles)) != 1 {
			t.Fatalf("a stale consent staffed anyway: %+v", song.GetAgentInstances())
		}
	})
	t.Run("the assistant was deleted", func(t *testing.T) {
		f := newSharedFixture(t)
		if result := f.run(t); result.Status != personalassistant.FolderSetupDone {
			t.Fatalf("first song = %+v", result)
		}
		if err := f.profiles.DeleteAgent(sharedLabel); err != nil {
			t.Fatal(err)
		}
		result := f.run(t)
		if result.StopReason != personalassistant.FolderStopAssistantMissing {
			t.Fatalf("second song = %+v cause=%v", result, result.Cause)
		}
		if len(reaperAssistants(f.profiles)) != 0 {
			t.Fatalf("a missing assistant was silently recreated: %v", reaperAssistants(f.profiles))
		}
	})
}
