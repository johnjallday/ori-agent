package resourcecatalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// linkTestChild links a child of the given kind under the fixture's Home and
// binds one agent instance to its one project role.
func (f *catalogFixture) linkTestChild(t *testing.T, id, kind, roleID, agentName string) (*workspace.Workspace, LookupCaller) {
	t.Helper()
	state := f.home.GetAssistantProgramState()
	key := state.Key.Normalize()
	blueprint, team := "reaper-song", "reaper-song-team"
	if kind == workspace.AssistantWorkspaceKindResource {
		blueprint, team = "reaper-studio", "reaper-studio-team"
	}
	owner := &workspace.AssistantProjectProviderOwner{
		PluginID: "reaper-plugin", PluginVersion: "0.11.0", BlueprintID: blueprint, BlueprintVersion: 1,
		ProjectTeamID: team, ProjectTeamSchema: 1, ProjectTeamVersion: 1, ProjectTeamDigest: strings.Repeat("c", 64),
		PluginGeneration: 5, ComponentFingerprint: strings.Repeat("d", 64), WorkspaceKind: kind,
	}
	child := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: id})
	child.ID, child.OwnerUserID = id, "local"
	child.SetTemplateProvenance(&workspace.TemplateProvenance{
		TemplateID: "plugin:reaper-plugin:" + blueprint, AssistantProgram: state.Declaration,
		AssistantProjectRoles: []workspace.AssistantProgramRoleSpec{{ID: roleID, Label: agentName,
			Scope: workspace.AssistantRoleScopeProject, Required: true, Primary: true, SystemPrompt: "Work here."}},
		GroupRequirement: &workspace.GroupRequirementSnapshot{
			SchemaVersion: workspace.GroupRequirementSnapshotSchemaVersion, Policy: "required", SelectedComposition: workspace.GroupRequirementCompositionGrouped,
			TemplateID: "plugin:reaper-plugin:" + blueprint, DefinitionDigest: strings.Repeat("e", 64), ReviewDigest: strings.Repeat("f", 64),
			OperationDigest: strings.Repeat("1", 64), AppliedAt: time.Now().UTC(), ProgramKey: &key,
			HomeWorkspaceID: f.home.ID, ProjectLinkID: workspace.AssistantProjectLinkID(f.home.ID, id),
			HomeProvider: state.HomeProvider, ProjectProvider: owner,
		},
	})
	if err := f.store.Save(child); err != nil {
		t.Fatal(err)
	}
	if _, _, err := workspace.NewAssistantProgramStore(f.store).EnsureProjectStation(child.ID); err != nil {
		t.Fatalf("link %s: %v", id, err)
	}
	return f.bindAgent(t, id, roleID, agentName)
}

func (f *catalogFixture) bindAgent(t *testing.T, workspaceID, roleID, agentName string) (*workspace.Workspace, LookupCaller) {
	t.Helper()
	instanceID := "inst-" + workspaceID
	if err := f.store.Update(workspaceID, func(current *workspace.Workspace) error {
		current.AgentInstances = append(current.AgentInstances, workspace.AgentInstance{ID: instanceID, Name: agentName, RoleID: roleID})
		link := current.GetAssistantProjectLink()
		if len(link.ProjectRoles) == 0 {
			link.ProjectRoles = []workspace.AssistantProgramRoleSpec{{ID: roleID, Label: agentName, Scope: workspace.AssistantRoleScopeProject, Required: true, Primary: true}}
		}
		link.ProjectBindings = workspace.AssistantRoleBindingSet{StateRevision: 1, Bindings: []workspace.AssistantRoleBinding{{RoleID: roleID, AgentInstanceID: instanceID, AgentName: agentName}}}
		current.SetAssistantProjectLink(link)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SaveWorkspaceAgent(workspaceID, agentName, &agent.Agent{Status: types.AgentStatusActive}); err != nil {
		t.Fatal(err)
	}
	ws, err := f.store.Get(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return ws, LookupCaller{WorkspaceID: workspaceID, AgentInstanceID: instanceID, AgentName: agentName}
}

func newLookupFixture(t *testing.T) (*catalogFixture, LookupCaller) {
	t.Helper()
	f := newCatalogFixture(t)
	f.homeAvailable = true
	f.service = f.newService(f.dataDir)
	f.approveAll(t)
	_, caller := f.linkTestChild(t, "song-one", "", "reaper-assistant", "REAPER Assistant")
	return f, caller
}

func lookupReason(err error) string {
	var refused *LookupError
	if errors.As(err, &refused) {
		return refused.Reason
	}
	return ""
}

func TestLookup_ALinkedSongAssistantFindsBoundedCitedEntries(t *testing.T) {
	f, caller := newLookupFixture(t)
	lyrics := EntryID("inst_machine_a1", "reaper_scripts", "Cockos/Lyrics.lua")
	if _, err := f.service.SetAnnotation("local", f.studio.ID, lyrics, AnnotationInput{Favorite: true, Note: "Use on the vocal bus. " + strings.Repeat("x", 600)}); err != nil {
		t.Fatal(err)
	}
	answer, err := f.service.Search(caller, LookupQuery{Query: "lyrics"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(answer.Results) != 1 || answer.DataNotice != DataNotice || answer.CatalogRevision != 1 || len(answer.Coverage) != 3 {
		t.Fatalf("answer = %+v", answer)
	}
	result := answer.Results[0]
	if result.Name != "Lyrics" || !result.Favorite || len(result.Note) > lookupNoteExcerpt+4 ||
		!strings.HasPrefix(result.StudioURL, "/workspaces/") || !strings.Contains(result.StudioURL, "/catalog?entry="+lyrics) {
		t.Fatalf("result = %+v", result)
	}
	encoded, _ := json.Marshal(answer)
	for _, leak := range []string{"Cockos/Lyrics.lua", "Application Support", "~/", "native_id", "location"} {
		if strings.Contains(string(encoded), leak) {
			t.Fatalf("lookup leaks %q: %s", leak, encoded)
		}
	}
	detail, err := f.service.Detail(caller, lyrics)
	if err != nil || len(detail.Results) != 1 || !strings.HasSuffix(detail.Results[0].Note, "xxxx") {
		t.Fatalf("detail = %+v, %v", detail, err)
	}
	if answer, err := f.service.Search(caller, LookupQuery{Limit: 2}); err != nil || len(answer.Results) != 2 || answer.Total != 3 {
		t.Fatalf("bounded search = %+v, %v", answer, err)
	}
	if _, err := f.service.Search(caller, LookupQuery{Limit: lookupMaxLimit + 1}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("oversized limit err = %v", err)
	}
}

func TestLookup_TheStudiosOwnAssistantReadsItsCatalog(t *testing.T) {
	f, _ := newLookupFixture(t)
	_, studioCaller := f.bindAgent(t, f.studio.ID, "reaper-studio-assistant", "REAPER Studio Assistant")
	if answer, err := f.service.Search(studioCaller, LookupQuery{}); err != nil || answer.Total != 3 {
		t.Fatalf("studio search = %+v, %v", answer, err)
	}
}

func TestLookup_RefusesEveryCallerTheChainDoesNotProve(t *testing.T) {
	f, caller := newLookupFixture(t)
	other, otherCaller := f.linkTestChild(t, "song-two", "", "reaper-assistant", "REAPER Assistant")
	_ = other
	cases := map[string]LookupCaller{
		"no instance":                  {WorkspaceID: caller.WorkspaceID, AgentName: caller.AgentName},
		"another workspace's instance": {WorkspaceID: caller.WorkspaceID, AgentInstanceID: otherCaller.AgentInstanceID, AgentName: caller.AgentName},
		"a name the instance is not":   {WorkspaceID: caller.WorkspaceID, AgentInstanceID: caller.AgentInstanceID, AgentName: "Someone"},
		"the Home itself":              {WorkspaceID: f.home.ID, AgentInstanceID: caller.AgentInstanceID, AgentName: caller.AgentName},
		"an unknown workspace":         {WorkspaceID: "nowhere", AgentInstanceID: caller.AgentInstanceID, AgentName: caller.AgentName},
	}
	for name, spoofed := range cases {
		if _, err := f.service.Search(spoofed, LookupQuery{}); lookupReason(err) != LookupNotAuthorized {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// An agent that sits in the song but holds no bound role.
	if err := f.store.Update(caller.WorkspaceID, func(current *workspace.Workspace) error {
		current.AgentInstances = append(current.AgentInstances, workspace.AgentInstance{ID: "inst-loose", Name: "Helper"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Search(LookupCaller{WorkspaceID: caller.WorkspaceID, AgentInstanceID: "inst-loose", AgentName: "Helper"}, LookupQuery{}); lookupReason(err) != LookupNotAuthorized {
		t.Fatalf("unbound agent err = %v", err)
	}
	// A disabled agent.
	if err := f.store.SaveWorkspaceAgent(caller.WorkspaceID, caller.AgentName, &agent.Agent{Status: types.AgentStatusDisabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Search(caller, LookupQuery{}); lookupReason(err) != LookupNotAuthorized {
		t.Fatalf("disabled agent err = %v", err)
	}
}

func TestLookup_FollowsWithdrawalProvidersAndApproval(t *testing.T) {
	f, caller := newLookupFixture(t)
	lyrics := EntryID("inst_machine_a1", "reaper_scripts", "Cockos/Lyrics.lua")
	if _, err := f.service.Detail(caller, lyrics); err != nil {
		t.Fatalf("detail before withdrawal: %v", err)
	}
	review, err := f.service.ReviewWithdraw("local", f.studio.ID, "reaper_scripts")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CommitWithdraw("local", f.studio.ID, review.Token); err != nil {
		t.Fatal(err)
	}
	// A cached entry ID from before is not a way around the withdrawal.
	if _, err := f.service.Detail(caller, lyrics); !errors.Is(err, ErrNotFound) {
		t.Fatalf("detail of a withdrawn entry err = %v", err)
	}
	if answer, _ := f.service.Search(caller, LookupQuery{Query: "lyrics"}); len(answer.Results) != 0 {
		t.Fatalf("search shows a withdrawn entry: %+v", answer)
	}
	f.provider.set(func(p *fakeProvider) { p.status = ProviderStatus{Reason: ReasonProviderDisabled} })
	if _, err := f.service.Search(caller, LookupQuery{}); lookupReason(err) != LookupProviderUnavailable {
		t.Fatalf("disabled plugin err = %v", err)
	}
	f.provider.set(func(p *fakeProvider) { p.status = newFakeProvider(t).status })
	f.homeAvailable = false
	if _, err := f.service.Search(caller, LookupQuery{}); lookupReason(err) != LookupProviderUnavailable {
		t.Fatalf("unavailable Home package err = %v", err)
	}
	f.homeAvailable = true
	// Another computer: the same folders, no approval there.
	elsewhere := f.newService(t.TempDir())
	if _, err := elsewhere.Search(caller, LookupQuery{}); lookupReason(err) != LookupNothingApproved {
		t.Fatalf("another computer err = %v", err)
	}
}

// PRD FR33 / AC11: the existing reviewed disconnect detaches a Studio. Its
// lookups stop, nothing can be inspected, and its owner keeps the catalog
// and notes; a new Studio could be set up beside it.
func TestLifecycle_TheReviewedDisconnectDetachesAndKeepsHistory(t *testing.T) {
	f, caller := newLookupFixture(t)
	lyrics := EntryID("inst_machine_a1", "reaper_scripts", "Cockos/Lyrics.lua")
	if _, err := f.service.SetAnnotation("local", f.studio.ID, lyrics, AnnotationInput{Note: "Keep me"}); err != nil {
		t.Fatal(err)
	}
	programs := workspace.NewAssistantProgramStore(f.store)
	home, err := f.store.Get(f.home.ID)
	if err != nil {
		t.Fatal(err)
	}
	review, err := programs.ReviewDisconnect(f.home.ID, f.studio.ID, home.GetAssistantProgramState().StateRevision)
	if err != nil {
		t.Fatalf("review disconnect: %v", err)
	}
	if _, err := programs.CommitDisconnect(f.home.ID, review.Token, "disconnect-studio"); err != nil {
		t.Fatalf("commit disconnect: %v", err)
	}
	if _, err := f.service.Search(caller, LookupQuery{}); lookupReason(err) != LookupNoStudio {
		t.Fatalf("song lookup after disconnect err = %v", err)
	}
	if _, err := f.service.StartInspection(context.Background(), "local", f.studio.ID, "after-disconnect", ModeRefresh, nil); err == nil {
		t.Fatal("a detached Studio could be inspected")
	}
	view, err := f.service.Overview("local", f.studio.ID)
	if err != nil || view.Attached || view.CanInspect || categoryNamed(view, "scripts").Counts.Present != 1 {
		t.Fatalf("owner view after disconnect = %+v, %v", view, err)
	}
	if detail, err := f.service.Entry("local", f.studio.ID, lyrics); err != nil || detail.Note != "Keep me" {
		t.Fatalf("notes after disconnect = %+v, %v", detail, err)
	}
	// Access can still be taken back on a detached Studio.
	withdraw, err := f.service.ReviewWithdraw("local", f.studio.ID, "reaper_scripts")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CommitWithdraw("local", f.studio.ID, withdraw.Token); err != nil {
		t.Fatal(err)
	}
}

func TestLookup_StopsWhenTheStudioLeavesItsHome(t *testing.T) {
	f, caller := newLookupFixture(t)
	if err := f.store.Update(f.home.ID, func(current *workspace.Workspace) error {
		state := current.GetAssistantProgramState()
		kept := state.LinkedProjectIDs[:0]
		for _, id := range state.LinkedProjectIDs {
			if id != f.studio.ID {
				kept = append(kept, id)
			}
		}
		state.LinkedProjectIDs = kept
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Search(caller, LookupQuery{}); lookupReason(err) != LookupNoStudio {
		t.Fatalf("after detach err = %v", err)
	}
	// Its owner can still read what was saved.
	if view, err := f.service.Overview("local", f.studio.ID); err != nil || view.Attached || categoryNamed(view, "templates").Counts.Present != 2 {
		t.Fatalf("owner view after detach = %+v, %v", view, err)
	}
}
