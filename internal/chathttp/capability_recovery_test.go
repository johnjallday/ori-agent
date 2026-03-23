package chathttp

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/types"
)

type capabilityRecoverySkillsManagerStub struct {
	list []skills.Skill
}

func (s *capabilityRecoverySkillsManagerStub) GetSkill(string, string) (*skills.Skill, bool, error) {
	return nil, false, nil
}

func (s *capabilityRecoverySkillsManagerStub) ListSkills(string) ([]skills.Skill, error) {
	return append([]skills.Skill(nil), s.list...), nil
}

func (s *capabilityRecoverySkillsManagerStub) ListEnabledSkillsWithPrompts(string) ([]skills.Skill, error) {
	return append([]skills.Skill(nil), s.list...), nil
}

func TestDetectCapabilityRecoveryIntent_SendNoteToRecipient(t *testing.T) {
	intent, ok := detectCapabilityRecoveryIntent("send this note to Lani.")
	if !ok {
		t.Fatal("expected communication recovery intent to be detected")
	}
	if intent.Kind != "send_communication" {
		t.Fatalf("expected send_communication intent, got %q", intent.Kind)
	}
	if intent.Recipient != "Lani" {
		t.Fatalf("expected recipient Lani, got %q", intent.Recipient)
	}
}

func TestDetectCapabilityRecoveryIntent_OpenInbox(t *testing.T) {
	intent, ok := detectCapabilityRecoveryIntent("open my inbox")
	if !ok {
		t.Fatal("expected inbox intent to be detected")
	}
	if intent.Kind != "open_inbox" {
		t.Fatalf("expected open_inbox intent, got %q", intent.Kind)
	}
}

func TestDetectCapabilityRecoveryIntent_IgnoresStructuredPlanningSubmission(t *testing.T) {
	query := strings.Join([]string{
		"Structured planning form submission:",
		"{",
		`  "original_request": "let's plan a trip to Spain",`,
		`  "answers": [{"id":"date_details","type":"textarea","display_value":"5/11 Lisbon arrival"}]`,
		"}",
	}, "\n")

	if intent, ok := detectCapabilityRecoveryIntent(query); ok {
		t.Fatalf("expected no capability recovery intent for planning submission, got %#v", intent)
	}
}

func TestMaybeHandleCapabilityRecovery_SuggestsNearbyActionsAndMarketplaceSkill(t *testing.T) {
	h := NewHandler(newPreflightStore("Ori", &agent.Agent{}), nil)
	ag := &resolvedChatAgent{Agent: &agent.Agent{}}

	previousSearch := capabilityRecoveryMarketplaceSearchFn
	capabilityRecoveryMarketplaceSearchFn = func(context.Context, string) ([]capabilityRecoveryMarketplaceSkill, error) {
		return []capabilityRecoveryMarketplaceSkill{
			{Package: "vercel-labs/skills@email-helper", URL: "https://skills.sh/vercel-labs/skills/email-helper"},
		}, nil
	}
	defer func() { capabilityRecoveryMarketplaceSearchFn = previousSearch }()

	rec := httptest.NewRecorder()
	handled := h.maybeHandleCapabilityRecovery(
		rec,
		context.Background(),
		ag,
		"Ori",
		"send this note to Lani",
		"",
		[]llm.Tool{{Name: "browser"}},
		nil,
	)
	if !handled {
		t.Fatal("expected capability recovery to handle missing communication capability")
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if payload["capability_recovery"] != true {
		t.Fatalf("expected capability_recovery flag, got %#v", payload["capability_recovery"])
	}

	responseText, _ := payload["response"].(string)
	for _, want := range []string{
		`"Lani"`,
		"`/openapp Mail`",
		"`open gmail.com`",
		"vercel-labs/skills@email-helper",
		"`npx skills init email-assistant`",
	} {
		if !strings.Contains(responseText, want) {
			t.Fatalf("expected response to contain %q, got %q", want, responseText)
		}
	}
}

func TestMaybeHandleCapabilityRecovery_UsesInstalledSkillsWithoutMarketplaceSearch(t *testing.T) {
	h := NewHandler(newPreflightStore("Ori", &agent.Agent{}), nil)
	h.SetSkillsManager(&capabilityRecoverySkillsManagerStub{
		list: []skills.Skill{
			{Name: "gmail-helper", Description: "Drafts Gmail replies"},
		},
	})
	ag := &resolvedChatAgent{Agent: &agent.Agent{}}

	previousSearch := capabilityRecoveryMarketplaceSearchFn
	searchCalls := 0
	capabilityRecoveryMarketplaceSearchFn = func(context.Context, string) ([]capabilityRecoveryMarketplaceSkill, error) {
		searchCalls++
		return nil, nil
	}
	defer func() { capabilityRecoveryMarketplaceSearchFn = previousSearch }()

	rec := httptest.NewRecorder()
	handled := h.maybeHandleCapabilityRecovery(
		rec,
		context.Background(),
		ag,
		"Ori",
		"email this to Sam",
		"",
		nil,
		nil,
	)
	if !handled {
		t.Fatal("expected capability recovery to handle missing communication capability")
	}
	if searchCalls != 0 {
		t.Fatalf("expected installed skill match to skip marketplace search, got %d calls", searchCalls)
	}
	if !strings.Contains(rec.Body.String(), "gmail-helper") {
		t.Fatalf("expected installed skill to be surfaced, got %q", rec.Body.String())
	}
}

func TestMaybeHandleCapabilityRecovery_DoesNotInterceptWhenDirectToolExistsOnToolCapableProvider(t *testing.T) {
	h := NewHandler(newPreflightStore("Ori", &agent.Agent{}), nil)
	ag := &resolvedChatAgent{Agent: &agent.Agent{}}

	handled := h.maybeHandleCapabilityRecovery(
		httptest.NewRecorder(),
		context.Background(),
		ag,
		"Ori",
		"send this note to Lani",
		"",
		[]llm.Tool{{Name: "send_email"}},
		nil,
	)
	if handled {
		t.Fatal("expected direct communication tool to bypass capability recovery")
	}
}

func TestMaybeHandleCapabilityRecovery_InterceptsWhenProviderCannotCallTools(t *testing.T) {
	h := NewHandler(newPreflightStore("Ori", &agent.Agent{}), nil)
	ag := &resolvedChatAgent{
		Agent: &agent.Agent{
			Settings: types.Settings{
				Provider: "codex",
				Model:    "gpt-5.3-codex",
			},
		},
	}

	previousSearch := capabilityRecoveryMarketplaceSearchFn
	capabilityRecoveryMarketplaceSearchFn = func(context.Context, string) ([]capabilityRecoveryMarketplaceSkill, error) {
		return nil, nil
	}
	defer func() { capabilityRecoveryMarketplaceSearchFn = previousSearch }()

	rec := httptest.NewRecorder()
	handled := h.maybeHandleCapabilityRecovery(
		rec,
		context.Background(),
		ag,
		"Ori",
		"send this note to Lani",
		"",
		[]llm.Tool{{Name: "send_email"}},
		nil,
	)
	if !handled {
		t.Fatal("expected recovery when provider cannot call otherwise-available tools")
	}
	if !strings.Contains(rec.Body.String(), "provider path can't call tools") {
		t.Fatalf("expected provider limitation to be surfaced, got %q", rec.Body.String())
	}
}

func TestBuildCapabilityRecoveryResponse_UsesAssistantSessionTerminology(t *testing.T) {
	response, _ := buildCapabilityRecoveryResponse(capabilityRecoveryIntent{Kind: "send_communication"}, capabilityRecoverySnapshot{})

	if !strings.Contains(response, "Assistant session") {
		t.Fatalf("expected Assistant-session wording, got %q", response)
	}
	if strings.Contains(response, "current agent") {
		t.Fatalf("did not expect current-agent wording, got %q", response)
	}
}
