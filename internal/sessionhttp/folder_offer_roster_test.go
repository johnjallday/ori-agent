package sessionhttp

import (
	"errors"
	"testing"

	agentstore "github.com/johnjallday/ori-agent/internal/store"
)

// A folder offer's review reads the blueprint's roster the way the Team step
// does, and reading it makes nothing.
func TestFolderOfferRosterReadsTheEntryAssistantAndCreatesNothing(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeRosterTemplate(t, handler, "offer-roster", `{
		"name":"Offer Roster",
		"agents":[
			{"name":"Development Assistant","role":"general","system_prompt":"understand and plan"},
			{"name":"Second","role":"researcher","system_prompt":"not the entry agent"}
		]
	}`)
	tpl, err := handler.resolveProjectTemplate("offer-roster", "")
	if err != nil {
		t.Fatalf("resolve template: %v", err)
	}
	want := handler.buildTemplateAgentPlan(tpl)
	before := len(handler.agentStore.ListAgents())

	roster, err := handler.FolderOfferRoster("offer-roster", "")
	if err != nil {
		t.Fatalf("FolderOfferRoster: %v", err)
	}
	if roster.TemplateID != tpl.ID || roster.TemplateName != "Offer Roster" || roster.TemplateRevision != tpl.Revision {
		t.Errorf("template = %+v", roster)
	}
	if roster.DeclaredName != "Development Assistant" || roster.Assistant != "Development Assistant" || roster.Action != want.Agents[0].Action {
		t.Errorf("assistant = %+v, want the plan's first agent %+v", roster, want.Agents[0])
	}
	// The revision is the one the creation endpoint checks a reviewed create
	// against, so a review made from it is accepted by that endpoint unchanged.
	if roster.PlanRevision == "" || roster.PlanRevision != want.Revision {
		t.Errorf("plan revision = %q, want %q", roster.PlanRevision, want.Revision)
	}

	// A rename is the Team step's own override: the name changes, the blueprint's
	// revision does not.
	renamed, err := handler.FolderOfferRoster("offer-roster", "  Dev Pat ")
	if err != nil {
		t.Fatalf("renamed roster: %v", err)
	}
	if renamed.Assistant != "Dev Pat" || renamed.DeclaredName != "Development Assistant" || renamed.PlanRevision != want.Revision {
		t.Errorf("renamed = %+v", renamed)
	}

	if after := len(handler.agentStore.ListAgents()); after != before {
		t.Fatalf("reading a roster changed the saved agents: %d → %d", before, after)
	}
	for _, name := range []string{"Development Assistant", "Dev Pat", "Second"} {
		if _, exists := handler.agentStore.GetAgent(name); exists {
			t.Errorf("reading a roster created %q", name)
		}
	}
}

// Naming a saved agent means that agent, as saved: the review must say reuse and
// show the model the agent really has, not the blueprint's.
func TestFolderOfferRosterReusesASavedAgentOfTheChosenName(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeRosterTemplate(t, handler, "offer-roster", `{
		"name":"Offer Roster",
		"agents":[{"name":"Development Assistant","role":"general","system_prompt":"understand and plan"}]
	}`)
	if err := handler.agentStore.CreateAgent("Robin", &agentstore.CreateAgentConfig{
		SystemPrompt: "saved and unchanged", LLMProvider: "openai", Model: "saved-model",
	}); err != nil {
		t.Fatalf("create saved agent: %v", err)
	}

	roster, err := handler.FolderOfferRoster("offer-roster", "Robin")
	if err != nil {
		t.Fatalf("FolderOfferRoster: %v", err)
	}
	if roster.Assistant != "Robin" || roster.Action != "reuse" {
		t.Fatalf("a saved agent's name did not resolve to reuse: %+v", roster)
	}
	provider, model, found := handler.SavedAgentModel(" Robin ")
	if !found || provider != "openai" || model != "saved-model" {
		t.Fatalf("SavedAgentModel = %q %q %v", provider, model, found)
	}
	if _, _, found := handler.SavedAgentModel("Nobody"); found {
		t.Error("an agent that is not saved was reported as found")
	}
	saved, _ := handler.agentStore.GetAgent("Robin")
	if saved.Settings.SystemPrompt != "saved and unchanged" || saved.Settings.Model != "saved-model" {
		t.Fatalf("reading the roster changed the saved agent: %+v", saved.Settings)
	}
}

func TestFolderOfferRosterRefusesABlueprintThatCannotStaffAWorkspace(t *testing.T) {
	handler, _, _, cleanup := templateTestEnv(t)
	defer cleanup()
	writeRosterTemplate(t, handler, "no-agents", `{"name":"No Agents","agents":[]}`)
	writeRosterTemplate(t, handler, "offer-roster", `{
		"name":"Offer Roster",
		"agents":[{"name":"Development Assistant","role":"general","system_prompt":"understand and plan"}]
	}`)
	for name, call := range map[string]func() (FolderOfferRoster, error){
		"no template id":     func() (FolderOfferRoster, error) { return handler.FolderOfferRoster(" ", "") },
		"unknown template":   func() (FolderOfferRoster, error) { return handler.FolderOfferRoster("not-installed", "") },
		"template no roster": func() (FolderOfferRoster, error) { return handler.FolderOfferRoster("no-agents", "") },
		"name with a slash":  func() (FolderOfferRoster, error) { return handler.FolderOfferRoster("offer-roster", "bad/name") },
		"nil handler":        func() (FolderOfferRoster, error) { return (*Handler)(nil).FolderOfferRoster("offer-roster", "") },
	} {
		if roster, err := call(); !errors.Is(err, ErrFolderOfferRoster) {
			t.Errorf("%s => %+v, %v; want ErrFolderOfferRoster", name, roster, err)
		}
	}
}
