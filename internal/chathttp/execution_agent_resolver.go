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

// isAssistantMode reports whether the resolution is the global conversation
// partner outside any workspace — the configured Personal Assistant — rather
// than an agent the person addressed directly. A workspace entry agent is a
// real agent, so it is never assistant mode — this keeps workspace chats from
// being intercepted by the global assistant→specialist handoff router. A name
// never makes a resolution assistant mode.
func (r executionAgentResolution) isAssistantMode() bool {
	if r.Source == executionAgentSourceWorkspaceEntry {
		return false
	}
	if !r.isResolved() {
		return true
	}
	return r.Source == executionAgentSourceAssistantDefault
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

	// Resolved rather than looked up exactly, so a session or roster row written
	// before the identity migration still finds the assistant (FR57).
	return store.AgentExists(agentStore, agentName)
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

// foreignWorkspaceManager reports whether name is a workspace manager created
// for a workspace other than workspaceID and not attached to workspaceID's
// roster. Outside any workspace nothing is foreign: direct chat with a saved
// agent stays available. A manager the person deliberately assigned to this
// workspace's roster is theirs to address here too.
func foreignWorkspaceManager(
	agentStore store.Store,
	workspaceLookup workspaceEntryLookup,
	name string,
	workspaceID string,
) bool {
	workspaceID = strings.TrimSpace(workspaceID)
	if agentStore == nil || workspaceID == "" {
		return false
	}
	ag, ok := agentStore.GetAgent(strings.TrimSpace(name))
	if !ok || ag == nil || ag.WorkspaceManager == nil || ag.WorkspaceManager.ManagesWorkspace(workspaceID) {
		return false
	}
	if workspaceLookup != nil {
		if ws, err := workspaceLookup.Get(workspaceID); err == nil && ws != nil {
			for _, instance := range ws.AgentInstances {
				if strings.EqualFold(strings.TrimSpace(instance.Name), strings.TrimSpace(name)) {
					return false
				}
			}
		}
	}
	return true
}

func resolveExecutionAgentName(
	ctx context.Context,
	sessionLookup chatSessionLookup,
	agentStore store.Store,
	workspaceLookup workspaceEntryLookup,
	sessionID string,
	requestedAgentName string,
	workspaceID string,
	personalAssistant string,
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

	// 2. Honor an explicit per-request agent override — except that inside a
	//    workspace a request cannot pick another workspace's manager, which
	//    would answer with this workspace's tools under a front door the person
	//    never staffed here.
	if requested := strings.TrimSpace(requestedAgentName); requested != "" {
		if agentExists(agentStore, requested) &&
			!foreignWorkspaceManager(agentStore, workspaceLookup, requested, workspaceID) {
			return executionAgentResolution{
				Name:   requested,
				Source: executionAgentSourceRequestOverride,
			}
		}
	}

	// 3. Inside a workspace, the default conversational partner is the
	//    workspace's entry agent — its own manager for a Blank workspace. No
	//    other agent stands in: if the entry agent can't be resolved we leave
	//    the resolution unresolved so the caller surfaces an actionable
	//    "add an entry agent" error.
	if strings.TrimSpace(workspaceID) != "" {
		if entry := resolveWorkspaceEntryAgentName(workspaceLookup, agentStore, workspaceID); entry != "" {
			return executionAgentResolution{
				Name:   entry,
				Source: executionAgentSourceWorkspaceEntry,
			}
		}
		return executionAgentResolution{Source: executionAgentSourceUnavailable}
	}

	// 4. Outside any workspace, cross-workspace work belongs to the configured
	//    Personal Assistant. Without one the resolution stays unresolved and
	//    the caller says how to set one up — there is no hidden default agent.
	if name := strings.TrimSpace(personalAssistant); name != "" && agentExists(agentStore, name) {
		return executionAgentResolution{
			Name:   name,
			Source: executionAgentSourceAssistantDefault,
		}
	}

	return executionAgentResolution{Source: executionAgentSourceUnavailable}
}

// PersonalAssistantProfileReader names the configured Personal Assistant's
// saved profile, or "" when none is hired. Implemented in server wiring over
// the personal assistant service.
type PersonalAssistantProfileReader func(ctx context.Context) string

// SetPersonalAssistantProfileReader wires how chat outside any workspace finds
// the configured Personal Assistant.
func (h *Handler) SetPersonalAssistantProfileReader(reader PersonalAssistantProfileReader) {
	h.personalAssistantProfile = reader
}

func (h *Handler) configuredPersonalAssistant(ctx context.Context) string {
	if h == nil || h.personalAssistantProfile == nil {
		return ""
	}
	return strings.TrimSpace(h.personalAssistantProfile(ctx))
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
	personalAssistant := ""
	if strings.TrimSpace(workspaceID) == "" {
		personalAssistant = h.configuredPersonalAssistant(ctx)
	}
	return resolveExecutionAgentName(
		ctx,
		h.sessionStore,
		h.store,
		h.workspaceStore,
		sessionID,
		requestedAgentName,
		workspaceID,
		personalAssistant,
	)
}
