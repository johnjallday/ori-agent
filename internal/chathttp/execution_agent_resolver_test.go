package chathttp

import (
	"context"
	"testing"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/session"
)

type executionAgentSessionLookupStub struct {
	sessions map[string]*session.Session
}

func (s *executionAgentSessionLookupStub) GetSession(_ context.Context, id string) (*session.Session, error) {
	if s == nil {
		return nil, session.ErrSessionNotFound
	}
	if sess, ok := s.sessions[id]; ok {
		return sess, nil
	}
	return nil, session.ErrSessionNotFound
}

func TestResolveExecutionAgentName_PrefersSessionBinding(t *testing.T) {
	sessionLookup := &executionAgentSessionLookupStub{
		sessions: map[string]*session.Session{
			"sess-1": {AgentName: "Session Agent"},
		},
	}
	agentStore := newPreflightStore("Fallback Agent", nil)

	resolution := resolveExecutionAgentName(context.Background(), sessionLookup, agentStore, "sess-1", "Requested Agent")

	if resolution.Name != "Session Agent" {
		t.Fatalf("expected session-bound agent, got %q", resolution.Name)
	}
	if resolution.Source != executionAgentSourceSessionBinding {
		t.Fatalf("expected source %q, got %q", executionAgentSourceSessionBinding, resolution.Source)
	}
	if resolution.usesCompatibilityFallback() {
		t.Fatal("did not expect compatibility fallback for session binding")
	}
}

func TestResolveExecutionAgentName_UsesRequestOverrideBeforeCompatibilityFallback(t *testing.T) {
	agentStore := newPreflightStore("Fallback Agent", nil)

	resolution := resolveExecutionAgentName(context.Background(), nil, agentStore, "", "Requested Agent")

	if resolution.Name != "Requested Agent" {
		t.Fatalf("expected request agent, got %q", resolution.Name)
	}
	if resolution.Source != executionAgentSourceRequestOverride {
		t.Fatalf("expected source %q, got %q", executionAgentSourceRequestOverride, resolution.Source)
	}
	if resolution.usesCompatibilityFallback() {
		t.Fatal("did not expect compatibility fallback for request override")
	}
}

func TestResolveExecutionAgentName_PrefersAssistantDefaultOverCurrentAgent(t *testing.T) {
	agentStore := &preflightStore{
		agents: map[string]*agent.Agent{
			assistantExecutionAgentName: {},
			"Specialist":                {},
		},
		names: []string{"Specialist", assistantExecutionAgentName},
	}

	resolution := resolveExecutionAgentName(context.Background(), nil, agentStore, "", "")

	if resolution.Name != assistantExecutionAgentName {
		t.Fatalf("expected Assistant runtime %q, got %q", assistantExecutionAgentName, resolution.Name)
	}
	if resolution.Source != executionAgentSourceAssistantDefault {
		t.Fatalf("expected source %q, got %q", executionAgentSourceAssistantDefault, resolution.Source)
	}
	if !resolution.isAssistantMode() {
		t.Fatal("expected Assistant default to be treated as Assistant mode")
	}
}

func TestResolveExecutionAgentName_ReturnsUnresolvedWhenAssistantIsMissing(t *testing.T) {
	agentStore := newPreflightStore("Fallback Agent", nil)

	resolution := resolveExecutionAgentName(context.Background(), nil, agentStore, "", "")

	if resolution.isResolved() {
		t.Fatalf("expected unresolved result, got %q", resolution.Name)
	}
	if resolution.Source != executionAgentSourceUnavailable {
		t.Fatalf("expected source %q, got %q", executionAgentSourceUnavailable, resolution.Source)
	}
}

func TestResolveExecutionAgentName_ReturnsUnresolvedWhenNoAgentIsAvailable(t *testing.T) {
	resolution := resolveExecutionAgentName(context.Background(), nil, nil, "", "")

	if resolution.isResolved() {
		t.Fatalf("expected unresolved result, got %q", resolution.Name)
	}
	if resolution.Source != executionAgentSourceUnavailable {
		t.Fatalf("expected source %q, got %q", executionAgentSourceUnavailable, resolution.Source)
	}
}
