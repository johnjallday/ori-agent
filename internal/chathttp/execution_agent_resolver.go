package chathttp

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
)

type chatSessionLookup interface {
	GetSession(ctx context.Context, id string) (*session.Session, error)
}

type executionAgentSource string

const (
	assistantExecutionAgentName                               = "Ori"
	executionAgentSourceSessionBinding   executionAgentSource = "session_binding"
	executionAgentSourceRequestOverride  executionAgentSource = "request_override"
	executionAgentSourceAssistantDefault executionAgentSource = "assistant_default"
	executionAgentSourceFallbackFirst    executionAgentSource = "fallback_first_available"
	executionAgentSourceUnavailable      executionAgentSource = "unresolved"
)

type executionAgentResolution struct {
	Name   string
	Source executionAgentSource
}

func (r executionAgentResolution) usesCompatibilityFallback() bool {
	return r.Source == executionAgentSourceFallbackFirst
}

func (r executionAgentResolution) isResolved() bool {
	return strings.TrimSpace(r.Name) != ""
}

func (r executionAgentResolution) isAssistantMode() bool {
	if !r.isResolved() {
		return true
	}
	if r.Source == executionAgentSourceAssistantDefault {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(r.Name), assistantExecutionAgentName)
}

func agentExists(agentStore store.Store, agentName string) bool {
	if agentStore == nil {
		return true
	}

	ag, ok := agentStore.GetAgent(strings.TrimSpace(agentName))
	return ok && ag != nil
}

func resolveExecutionAgentName(
	ctx context.Context,
	sessionLookup chatSessionLookup,
	agentStore store.Store,
	sessionID string,
	requestedAgentName string,
) executionAgentResolution {
	if sessionLookup != nil && strings.TrimSpace(sessionID) != "" {
		if sess, err := sessionLookup.GetSession(ctx, sessionID); err == nil && sess != nil {
			if sessionAgent := strings.TrimSpace(sess.AgentName); sessionAgent != "" {
				if agentExists(agentStore, sessionAgent) {
					return executionAgentResolution{
						Name:   sessionAgent,
						Source: executionAgentSourceSessionBinding,
					}
				}
			}
		}
	}

	if requested := strings.TrimSpace(requestedAgentName); requested != "" {
		if agentExists(agentStore, requested) {
			return executionAgentResolution{
				Name:   requested,
				Source: executionAgentSourceRequestOverride,
			}
		}
	}

	if agentStore == nil {
		return executionAgentResolution{Source: executionAgentSourceUnavailable}
	}

	if assistant, ok := agentStore.GetAgent(assistantExecutionAgentName); ok && assistant != nil {
		return executionAgentResolution{
			Name:   assistantExecutionAgentName,
			Source: executionAgentSourceAssistantDefault,
		}
	}

	return executionAgentResolution{Source: executionAgentSourceUnavailable}
}

func (h *Handler) resolveExecutionAgentName(
	ctx context.Context,
	sessionID string,
	requestedAgentName string,
) executionAgentResolution {
	if h == nil {
		return executionAgentResolution{Source: executionAgentSourceUnavailable}
	}
	return resolveExecutionAgentName(ctx, h.sessionStore, h.store, sessionID, requestedAgentName)
}
