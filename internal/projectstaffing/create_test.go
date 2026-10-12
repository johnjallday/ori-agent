package projectstaffing

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

var (
	homeRole    = workspace.AssistantProgramRoleSpec{ID: "portfolio_manager", Label: "Music Portfolio Manager", Scope: workspace.AssistantRoleScopeHome, Required: true, Primary: true}
	libraryRole = workspace.AssistantProgramRoleSpec{ID: "sample_library", Label: "Sample Library Manager", Scope: workspace.AssistantRoleScopeHome}
	projectRole = workspace.AssistantProgramRoleSpec{ID: "reaper-assistant", Label: "REAPER Assistant", Scope: workspace.AssistantRoleScopeProject, Required: true}
)

// createFixture is the service fixture with the Home declaring its own roles,
// the way a reviewed create finds it.
func createFixture(t *testing.T) (workspace.Store, *Service, fakeAgents) {
	t.Helper()
	store, service, agents := fixture(t)
	if err := store.Update("home", func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.Declaration = &workspace.AssistantProgramDeclaration{
			SchemaVersion: 2, ID: "music", DefaultPrimaryName: "Portfolio Manager",
			Roles: []workspace.AssistantProgramRoleSpec{homeRole, libraryRole},
		}
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return store, service, agents
}

var reviewedCreate = &Grant{Source: workspace.ProjectStaffingConsentReviewedCreate, OfferID: digest}

func fillsByRole(fills []CreatedFill) map[string]CreatedFill {
	byRole := map[string]CreatedFill{}
	for _, fill := range fills {
		byRole[fill.RoleID] = fill
	}
	return byRole
}

// bindOnHome records a Home role filled, as the staffing adapter would.
func bindOnHome(t *testing.T, store workspace.Store, roleID, name string) {
	t.Helper()
	if err := store.Update("home", func(home *workspace.Workspace) error {
		home.AgentInstances = append(home.AgentInstances, workspace.AgentInstance{ID: "i-home", Name: name, RoleID: roleID})
		state := home.GetAssistantProgramState()
		state.HomeBindings.Bindings = append(state.HomeBindings.Bindings, workspace.AssistantRoleBinding{RoleID: roleID, AgentInstanceID: "i-home", AgentName: name})
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFillCreatedFillsBothRequiredRolesAndRecordsTheReviewedCreate(t *testing.T) {
	_, service, _ := createFixture(t)
	fills, err := service.FillCreated("song-a", nil, reviewedCreate, true)
	if err != nil || len(fills) != 2 {
		t.Fatalf("fills = %+v %v", fills, err)
	}
	byRole := fillsByRole(fills)
	// The Home's primary takes the program's own name; the optional Home role
	// is nobody's to fill here.
	if got := byRole["portfolio_manager"]; got.Mode != ModeCreate || got.Name != "Portfolio Manager" || got.Scope != workspace.AssistantRoleScopeHome {
		t.Fatalf("home role = %+v", got)
	}
	if got := byRole["reaper-assistant"]; got.Mode != ModeCreate || got.Name != "REAPER Assistant" || got.Scope != workspace.AssistantRoleScopeProject {
		t.Fatalf("project role = %+v", got)
	}
	consent, err := service.Consents().Read("home")
	if err != nil || consent == nil || consent.Source != workspace.ProjectStaffingConsentReviewedCreate ||
		consent.OfferID != digest || consent.Roles[0].PendingName != "REAPER Assistant" {
		t.Fatalf("consent = %+v %v", consent, err)
	}
}

func TestFillCreatedHiresOnce(t *testing.T) {
	store, service, agents := createFixture(t)
	if _, err := service.FillCreated("song-a", nil, reviewedCreate, true); err != nil {
		t.Fatal(err)
	}
	// The staffing adapter creates both agents; Settle records the shared one.
	agents["Portfolio Manager"], agents["REAPER Assistant"] = true, true
	bindOnHome(t, store, "portfolio_manager", "Portfolio Manager")
	bindInSong(t, store, "song-a", "REAPER Assistant", workspace.RoleSourceCreated)
	if err := service.Settle("song-a"); err != nil {
		t.Fatal(err)
	}

	fills, err := service.FillCreated("song-b", nil, reviewedCreate, true)
	if err != nil || len(fills) != 1 {
		t.Fatalf("second project fills = %+v %v", fills, err)
	}
	if fills[0].RoleID != "reaper-assistant" || fills[0].Mode != ModeBind || fills[0].Name != "REAPER Assistant" {
		t.Fatalf("the second project did not bind the Home's agent: %+v", fills[0])
	}
	// A project whose roles are filled has nothing left to decide.
	if again, err := service.FillCreated("song-a", nil, reviewedCreate, true); err != nil || len(again) != 0 {
		t.Fatalf("a staffed project was filled again: %+v %v", again, err)
	}
}

func TestFillCreatedLeavesTheUsersOwnChoiceAlone(t *testing.T) {
	_, service, _ := createFixture(t)
	fills, err := service.FillCreated("song-a", map[string]bool{"reaper-assistant": true}, reviewedCreate, true)
	if err != nil || len(fills) != 1 || fills[0].RoleID != "portfolio_manager" {
		t.Fatalf("fills = %+v %v", fills, err)
	}
	if consent, _ := service.Consents().Read("home"); consent != nil {
		t.Fatalf("a role the user filled themselves recorded a shared consent: %+v", consent)
	}
}

func TestFillCreatedWithoutAModelOnlyBindsWhatTheHomeHas(t *testing.T) {
	store, service, agents := createFixture(t)
	if fills, err := service.FillCreated("song-a", nil, reviewedCreate, false); err != nil || len(fills) != 0 {
		t.Fatalf("no model, nothing to bind: %+v %v", fills, err)
	}
	if consent, _ := service.Consents().Read("home"); consent != nil {
		t.Fatalf("a create that could not run recorded a consent: %+v", consent)
	}

	// Once the Home has its shared agent, a later project binds it with or
	// without a model: binding makes nothing new.
	if _, err := service.FillCreated("song-a", nil, reviewedCreate, true); err != nil {
		t.Fatal(err)
	}
	agents["REAPER Assistant"] = true
	bindInSong(t, store, "song-a", "REAPER Assistant", workspace.RoleSourceCreated)
	if err := service.Settle("song-a"); err != nil {
		t.Fatal(err)
	}
	fills, err := service.FillCreated("song-b", nil, reviewedCreate, false)
	if err != nil || len(fills) != 1 || fills[0].Mode != ModeBind {
		t.Fatalf("no model, shared agent exists: %+v %v", fills, err)
	}
}

func TestFillCreatedNeverTakesAnAgentThatSharesTheName(t *testing.T) {
	_, service, agents := createFixture(t)
	agents["Portfolio Manager"], agents["REAPER Assistant"] = true, true
	fills, err := service.FillCreated("song-a", nil, reviewedCreate, true)
	if err != nil {
		t.Fatal(err)
	}
	byRole := fillsByRole(fills)
	if byRole["portfolio_manager"].Name != "Portfolio Manager 2" || byRole["reaper-assistant"].Name != "REAPER Assistant 2" ||
		byRole["reaper-assistant"].Mode != ModeCreate {
		t.Fatalf("fills = %+v", fills)
	}
}

func TestFillCreatedReplacesASharedAgentThatIsGone(t *testing.T) {
	store, service, agents := createFixture(t)
	if _, err := service.FillCreated("song-a", nil, reviewedCreate, true); err != nil {
		t.Fatal(err)
	}
	agents["REAPER Assistant"] = true
	bindInSong(t, store, "song-a", "REAPER Assistant", workspace.RoleSourceCreated)
	if err := service.Settle("song-a"); err != nil {
		t.Fatal(err)
	}
	delete(agents, "REAPER Assistant") // The user deleted the agent.

	fills, err := service.FillCreated("song-b", nil, reviewedCreate, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := fillsByRole(fills)["reaper-assistant"]; got.Mode != ModeCreate || got.Name == "" {
		t.Fatalf("a missing shared agent was not replaced: %+v", got)
	}
}

func TestFillCreatedStaffsItsOwnAgentWhenTheHomeSharesAnotherBlueprint(t *testing.T) {
	_, service, _ := createFixture(t)
	if _, err := service.Consents().Grant("home", workspace.ProjectStaffingConsentGrant{
		Source: workspace.ProjectStaffingConsentHomeSwitch, PluginID: "other-plugin", BlueprintID: "other",
		TeamDigest: digest, RoleIDs: []string{"lead"},
	}); err != nil {
		t.Fatal(err)
	}
	fills, err := service.FillCreated("song-a", nil, reviewedCreate, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := fillsByRole(fills)["reaper-assistant"]; got.Mode != ModeCreate || got.Name != "REAPER Assistant" {
		t.Fatalf("project role = %+v", got)
	}
	// The other blueprint's consent is the user's and is not replaced.
	if consent, _ := service.Consents().Read("home"); consent == nil || consent.BlueprintID != "other" {
		t.Fatalf("another blueprint's consent was replaced: %+v", consent)
	}
}

func TestFillCreatedRenewsAConsentGivenForAnOlderTeam(t *testing.T) {
	store, service, agents := createFixture(t)
	if _, err := service.FillCreated("song-a", nil, reviewedCreate, true); err != nil {
		t.Fatal(err)
	}
	agents["REAPER Assistant"] = true
	bindInSong(t, store, "song-a", "REAPER Assistant", workspace.RoleSourceCreated)
	if err := service.Settle("song-a"); err != nil {
		t.Fatal(err)
	}
	// The blueprint's team changed: song-b is created from the newer one.
	newer := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if err := store.Update("song-b", func(song *workspace.Workspace) error {
		link := song.GetAssistantProjectLink()
		link.ProjectProvider = provider(newer)
		song.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fills, err := service.FillCreated("song-b", nil, reviewedCreate, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := fillsByRole(fills)["reaper-assistant"]; got.Mode != ModeBind || got.Name != "REAPER Assistant" {
		t.Fatalf("the Home's agent was not reused after its team changed: %+v", got)
	}
	if consent, _ := service.Consents().Read("home"); consent == nil || consent.TeamDigest != newer {
		t.Fatalf("consent = %+v", consent)
	}
}

func TestFillCreatedLeavesAnOlderProgramToItsOwnHire(t *testing.T) {
	store, service, _ := createFixture(t)
	if err := store.Update("home", func(home *workspace.Workspace) error {
		state := home.GetAssistantProgramState()
		state.Declaration.SchemaVersion = 1
		home.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if fills, err := service.FillCreated("song-a", nil, reviewedCreate, true); err == nil || len(fills) != 0 {
		t.Fatalf("an older program was staffed role by role: %+v %v", fills, err)
	}
}

func TestPreviewCreateNamesWhatTheCreateWillDoWithoutRecordingAnything(t *testing.T) {
	store, service, agents := createFixture(t)
	roles := []workspace.AssistantProgramRoleSpec{homeRole, libraryRole, projectRole}

	// Before the Home exists every required role is new.
	first := fillsByRole(service.PreviewCreate(CreatePreview{Roles: roles, DefaultPrimaryName: "Portfolio Manager", Provider: provider(digest)}))
	if len(first) != 2 || first["portfolio_manager"].Mode != ModeCreate || first["portfolio_manager"].Name != "Portfolio Manager" ||
		first["reaper-assistant"].Mode != ModeCreate || first["reaper-assistant"].Name != "REAPER Assistant" {
		t.Fatalf("first preview = %+v", first)
	}
	if consent, _ := service.Consents().Read("home"); consent != nil {
		t.Fatalf("a preview recorded a consent: %+v", consent)
	}

	// After the first project: the Home role is held and the project role binds.
	if _, err := service.FillCreated("song-a", nil, reviewedCreate, true); err != nil {
		t.Fatal(err)
	}
	agents["Portfolio Manager"], agents["REAPER Assistant"] = true, true
	bindInSong(t, store, "song-a", "REAPER Assistant", workspace.RoleSourceCreated)
	if err := service.Settle("song-a"); err != nil {
		t.Fatal(err)
	}
	home, err := store.Get("home")
	if err != nil {
		t.Fatal(err)
	}
	second := fillsByRole(service.PreviewCreate(CreatePreview{
		Home: home, Roles: roles, DefaultPrimaryName: "Portfolio Manager", Provider: provider(digest),
		Held: map[string]string{"portfolio_manager": "Portfolio Manager"},
	}))
	if second["portfolio_manager"].Mode != ModeHeld || second["portfolio_manager"].Name != "Portfolio Manager" ||
		second["reaper-assistant"].Mode != ModeBind || second["reaper-assistant"].Name != "REAPER Assistant" {
		t.Fatalf("second preview = %+v", second)
	}

	// A blueprint whose roles are not shared makes each project its own agent,
	// and a role the user filled is theirs.
	own := service.PreviewCreate(CreatePreview{Home: home, Roles: roles, Explicit: map[string]bool{"portfolio_manager": true}})
	if len(own) != 1 || own[0].Mode != ModeCreate || own[0].Name != "REAPER Assistant 2" {
		t.Fatalf("unshared preview = %+v", own)
	}
}
