package chathttp

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type chatSessionLookup interface {
	GetSession(ctx context.Context, id string) (*session.Session, error)
}

// workspaceEntryLookup resolves a workspace by ID and reads its on-disk agent
// snapshots so the chat resolver can default to the workspace's entry agent.
// Implemented by workspace.Store.
type workspaceEntryLookup interface {
	Get(id string) (*workspace.Workspace, error)
	GetWorkspaceAgent(workspaceID, agentName string) (*agent.Agent, bool, error)
}

type executionAgentSource string

const (
	assistantExecutionAgentName                               = "Ori"
	executionAgentSourceSessionBinding   executionAgentSource = "session_binding"
	executionAgentSourceRequestOverride  executionAgentSource = "request_override"
	executionAgentSourceWorkspaceEntry   executionAgentSource = "workspace_entry"
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

// isAssistantMode reports whether the resolution represents the generic system
// assistant rather than a concrete agent the user is talking to directly. A
// workspace entry agent is a real agent, so it is never assistant mode — this
// keeps workspace chats from being intercepted by the global assistant→
// specialist handoff router.
func (r executionAgentResolution) isAssistantMode() bool {
	if r.Source == executionAgentSourceWorkspaceEntry {
		return false
	}
	if !r.isResolved() {
		return true
	}
	if r.Source == executionAgentSourceAssistantDefault {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(r.Name), assistantExecutionAgentName)
}

// isWorkspaceEntryDefault reports whether the resolution defaulted to the
// workspace's entry agent (as opposed to an explicit session/request binding).
func (r executionAgentResolution) isWorkspaceEntryDefault() bool {
	return r.Source == executionAgentSourceWorkspaceEntry
}

func agentExists(agentStore store.Store, agentName string) bool {
	if agentStore == nil {
		return true
	}

	ag, ok := agentStore.GetAgent(strings.TrimSpace(agentName))
	return ok && ag != nil
}

// resolveWorkspaceEntryAgentName returns the workspace's entry agent name when
// it resolves to a runnable agent in the agent store, otherwise "".
func resolveWorkspaceEntryAgentName(
	workspaceLookup workspaceEntryLookup,
	agentStore store.Store,
	workspaceID string,
) string {
	if workspaceLookup == nil {
		return ""
	}

	workspaceID = strings.TrimSpace(workspaceID)

	ws, err := workspaceLookup.Get(workspaceID)
	if err != nil || ws == nil {
		return ""
	}

	entry := strings.TrimSpace(ws.EntryAgentName())
	if entry == "" {
		return ""
	}

	// The entry agent is runnable if the global registry knows it, or if the
	// workspace carries an on-disk agent snapshot. The chat runtime resolver
	// (like task execution) resolves workspace agents snapshot-first, so a
	// snapshot-only entry agent still answers — mirror that here rather than
	// blocking the chat as "missing".
	if agentExists(agentStore, entry) {
		return entry
	}
	if _, ok, err := workspaceLookup.GetWorkspaceAgent(workspaceID, entry); err == nil && ok {
		return entry
	}

	return ""
}

func resolveExecutionAgentName(
	ctx context.Context,
	sessionLookup chatSessionLookup,
	agentStore store.Store,
	workspaceLookup workspaceEntryLookup,
	sessionID string,
	requestedAgentName string,
	workspaceID string,
) executionAgentResolution {
	// 1. Honor an explicit session<->agent binding. This covers Direct agent
	//    chat and workspace sessions already bound to their entry agent at
	//    creation time.
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

	// 2. Honor an explicit per-request agent override.
	if requested := strings.TrimSpace(requestedAgentName); requested != "" {
		if agentExists(agentStore, requested) {
			return executionAgentResolution{
				Name:   requested,
				Source: executionAgentSourceRequestOverride,
			}
		}
	}

	// 3. Inside a workspace, the default conversational partner is the
	//    workspace's entry agent. The generic system assistant is never used as
	//    a workspace default: if the entry agent can't be resolved we leave the
	//    resolution unresolved so the caller surfaces an actionable
	//    "add an entry agent" error rather than silently answering as Ori.
	if strings.TrimSpace(workspaceID) != "" {
		if entry := resolveWorkspaceEntryAgentName(workspaceLookup, agentStore, workspaceID); entry != "" {
			return executionAgentResolution{
				Name:   entry,
				Source: executionAgentSourceWorkspaceEntry,
			}
		}
		return executionAgentResolution{Source: executionAgentSourceUnavailable}
	}

	// 4. Outside any workspace, fall back to the global system assistant (Ori).
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
	workspaceID string,
) executionAgentResolution {
	if h == nil {
		return executionAgentResolution{Source: executionAgentSourceUnavailable}
	}
	return resolveExecutionAgentName(
		ctx,
		h.sessionStore,
		h.store,
		h.workspaceStore,
		sessionID,
		requestedAgentName,
		workspaceID,
	)
}
