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
			"Atlas": {
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
		names: []string{"Atlas", "Reviewer"},
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

func TestHandleAgentsList_ListsEveryAgentPlainly(t *testing.T) {
	ch := newCommandHandlerForAgentTests()

	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	rr := httptest.NewRecorder()
	ch.HandleAgentsList(rr, req)

	response := decodeSystemCommandResponse(t, rr)
	for _, want := range []string{"- Atlas\n", "- Reviewer\n"} {
		if !strings.Contains(response, want) {
			t.Fatalf("expected %q in %q", want, response)
		}
	}
	// No agent is singled out as a built-in assistant or a current agent.
	if strings.Contains(response, "(Assistant)") || strings.Contains(response, "(current)") {
		t.Fatalf("did not expect a special marker, got %q", response)
	}
}

func TestCommandSessionModeLabel(t *testing.T) {
	cases := []struct {
		name string
		res  executionAgentResolution
		want string
	}{
		{"assistant default", executionAgentResolution{Name: "Atlas", Source: executionAgentSourceAssistantDefault}, "Personal Assistant"},
		{"workspace entry default", executionAgentResolution{Name: "Workspace Manager", Source: executionAgentSourceWorkspaceEntry}, "Workspace entry agent"},
		{"pinned via session binding", executionAgentResolution{Name: "Specialist", Source: executionAgentSourceSessionBinding}, "Pinned specialist"},
		{"pinned via request override", executionAgentResolution{Name: "Specialist", Source: executionAgentSourceRequestOverride}, "Pinned specialist"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := commandSessionModeLabel(tc.res); got != tc.want {
				t.Fatalf("commandSessionModeLabel(%s) = %q, want %q", tc.res.Source, got, tc.want)
			}
		})
	}
}

func TestHandleAgentStatus_UsesAssistantTerminology(t *testing.T) {
	ch := newCommandHandlerForAgentTests()

	req := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	rr := httptest.NewRecorder()
	ch.HandleAgentStatus(rr, req, executionAgentResolution{
		Name:   "Atlas",
		Source: executionAgentSourceAssistantDefault,
	})

	response := decodeSystemCommandResponse(t, rr)
	if !strings.Contains(response, "Assistant Status") {
		t.Fatalf("expected Assistant status heading, got %q", response)
	}
	if !strings.Contains(response, "Execution Agent:** Personal Assistant (`Atlas`)") {
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
		Name:   "Atlas",
		Source: executionAgentSourceAssistantDefault,
	})

	response := decodeSystemCommandResponse(t, rr)
	if !strings.Contains(response, "These skills are available to your Personal Assistant via `Atlas`.") {
		t.Fatalf("expected Assistant runtime context, got %q", response)
	}
	if !strings.Contains(response, "mail-helper") {
		t.Fatalf("expected skill listing, got %q", response)
	}
}
