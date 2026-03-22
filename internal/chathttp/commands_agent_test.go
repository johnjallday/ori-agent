package chathttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/types"
)

func newCommandHandlerForAgentTests() *CommandHandler {
	st := &preflightStore{
		agents: map[string]*agent.Agent{
			assistantExecutionAgentName: {
				Settings: types.Settings{
					Model:       "gpt-5-nano",
					Temperature: 0.3,
				},
			},
			"Reviewer": {
				Settings: types.Settings{
					Model:       "gpt-5-mini",
					Temperature: 0.2,
				},
			},
		},
		names: []string{assistantExecutionAgentName, "Reviewer"},
	}
	return NewCommandHandler(st)
}

type commandsAgentSkillsManagerStub struct {
	list []skills.Skill
}

func (s *commandsAgentSkillsManagerStub) GetSkill(string, string) (*skills.Skill, bool, error) {
	return nil, false, nil
}

func (s *commandsAgentSkillsManagerStub) ListSkills(string) ([]skills.Skill, error) {
	return append([]skills.Skill(nil), s.list...), nil
}

func TestHandleSwitch_Deprecated(t *testing.T) {
	ch := newCommandHandlerForAgentTests()

	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	rr := httptest.NewRecorder()
	ch.HandleSwitch(rr, req, "Reviewer")

	response := decodeSystemCommandResponse(t, rr)
	if !strings.Contains(response, "deprecated") {
		t.Fatalf("expected deprecation message, got %q", response)
	}
}

func TestHandleAgentsList_MarksAssistantWithoutCurrentMarker(t *testing.T) {
	ch := newCommandHandlerForAgentTests()

	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	rr := httptest.NewRecorder()
	ch.HandleAgentsList(rr, req)

	response := decodeSystemCommandResponse(t, rr)
	if !strings.Contains(response, "(Assistant)") {
		t.Fatalf("expected Assistant marker, got %q", response)
	}
	if strings.Contains(response, "(current)") {
		t.Fatalf("did not expect current-agent marker, got %q", response)
	}
}

func TestHandleAgentStatus_UsesAssistantTerminology(t *testing.T) {
	ch := newCommandHandlerForAgentTests()

	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	rr := httptest.NewRecorder()
	ch.HandleAgentStatus(rr, req, executionAgentResolution{
		Name:   assistantExecutionAgentName,
		Source: executionAgentSourceAssistantDefault,
	})

	response := decodeSystemCommandResponse(t, rr)
	if !strings.Contains(response, "Assistant Status") {
		t.Fatalf("expected Assistant status heading, got %q", response)
	}
	if !strings.Contains(response, "Execution Agent:** Assistant (`Ori`)") {
		t.Fatalf("expected Assistant execution agent label, got %q", response)
	}
	if strings.Contains(response, "Current Agent") {
		t.Fatalf("did not expect current-agent wording, got %q", response)
	}
}

func TestHandleSkillsList_UsesAssistantRuntime(t *testing.T) {
	ch := newCommandHandlerForAgentTests()
	ch.SetSkillsManager(&commandsAgentSkillsManagerStub{
		list: []skills.Skill{{Name: "mail-helper", Description: "Handles email drafts"}},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	rr := httptest.NewRecorder()
	ch.HandleSkillsList(rr, req, executionAgentResolution{
		Name:   assistantExecutionAgentName,
		Source: executionAgentSourceAssistantDefault,
	})

	response := decodeSystemCommandResponse(t, rr)
	if !strings.Contains(response, "These skills are available to Assistant via `Ori`.") {
		t.Fatalf("expected Assistant runtime context, got %q", response)
	}
	if !strings.Contains(response, "mail-helper") {
		t.Fatalf("expected skill listing, got %q", response)
	}
}
