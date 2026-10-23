package sessionhttp

import (
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentstore "github.com/johnjallday/ori-agent/internal/store"
)

func TestFolderOfferModelUsesApplicableConfigurationWithoutInference(t *testing.T) {
	h, cleanup := createTestHandler(t)
	defer cleanup()
	tpl := rosterTemplate(projecttemplates.AgentSpec{Name: "Readiness agent"})
	tpl.ID = "readiness"
	h.SetProjectTemplateResolver(func(string, string) (projecttemplates.Template, error) { return tpl, nil })
	read := func() FolderOfferHire {
		t.Helper()
		hire, err := h.FolderOfferHire(tpl.ID, "local", "standalone")
		if err != nil {
			t.Fatal(err)
		}
		return hire
	}
	h.SetSystemModelReader(nil)
	if got := read(); got.ModelChecked {
		t.Fatalf("unavailable configuration treated as checked: %+v", got)
	}
	h.SetSystemModelReader(fakeSystemModelReader{})
	if got := read(); !got.ModelChecked || got.Model != "" || got.ModelSource != "system" {
		t.Fatalf("known absent default: %+v", got)
	}
	h.SetSystemModelReader(fakeSystemModelReader{provider: "openai", model: "fixture-default"})
	if got := read(); !got.ModelChecked || got.Provider != "openai" || got.Model != "fixture-default" || got.ModelSource != "system" {
		t.Fatalf("default configuration: %+v", got)
	}
	tpl.Agents[0].Model = "fixture-template"
	if got := read(); got.Model != "fixture-template" || got.ModelSource != "template" {
		t.Fatalf("template configuration lost precedence: %+v", got)
	}
	if err := h.agentStore.CreateAgent("Readiness agent", &agentstore.CreateAgentConfig{Model: "fixture-bound", LLMProvider: "ollama"}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Mode != FolderOfferHireBind || got.Provider != "ollama" || got.Model != "fixture-bound" || got.ModelSource != "existing" || !got.ModelChecked {
		t.Fatalf("saved agent did not win over template/system: %+v", got)
	}
	if err := h.agentStore.UpdateAgent("Readiness agent", func(saved *agent.Agent) error {
		saved.Settings.Provider, saved.Settings.Model = "", ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := read(); !got.ModelChecked || got.ModelSource != "existing" || got.Model != "" || got.Provider != "" {
		t.Fatalf("missing bound configuration fell back to the system model: %+v", got)
	}
	// Reads above do not create any of the proposed agents or execute a model.
}
