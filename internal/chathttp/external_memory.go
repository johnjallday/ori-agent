package chathttp

import (
	"context"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/externalmemory"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The external memory mirror (what the user's other coding agents remember)
// is read only by the personal assistant (PRD decision 1A). On the chat path
// that is the assistant answering in its own Personal HQ: the responding
// instance must be the binding's entry instance, verified by the personal
// assistant's own store, never by anything the browser sends. Task runs build
// their tools elsewhere and never reach this file.

// ExternalMemoryReader is the assistant's read-only view of the mirror.
type ExternalMemoryReader interface {
	ContextBlock(budget int) string
	Recall(q externalmemory.Query) string
}

// PersonalAssistantGate verifies that an instance answering in a workspace is
// the user's personal assistant in its Personal HQ.
type PersonalAssistantGate interface {
	IsPersonalAssistant(ctx context.Context, userID, workspaceID, entryInstanceID string) bool
}

// externalMemoryGateTimeout bounds the gate when no request context is at
// hand (tool attachment happens while the agent is resolved).
const externalMemoryGateTimeout = 2 * time.Second

// SetExternalMemory wires the mirror and the gate. Without both, no chat turn
// gets the block or the tool.
func (h *Handler) SetExternalMemory(reader ExternalMemoryReader, gate PersonalAssistantGate) {
	if h != nil {
		h.externalMemory = reader
		h.externalMemoryGate = gate
	}
}

// respondingInstance returns the one agent instance named agentName in ws. A
// missing or ambiguous name is no principal.
func respondingInstance(ws *workspace.Workspace, agentName string) string {
	agentName = strings.TrimSpace(agentName)
	if ws == nil || agentName == "" {
		return ""
	}
	principal := ""
	for _, inst := range ws.AgentInstances {
		if !strings.EqualFold(strings.TrimSpace(inst.Name), agentName) {
			continue
		}
		if principal != "" || strings.TrimSpace(inst.ID) == "" {
			return ""
		}
		principal = strings.TrimSpace(inst.ID)
	}
	return principal
}

// isPersonalAssistantTurn reports whether the agent answering on this
// workspace route is the user's personal assistant. instanceID, when the
// runtime resolver supplied one, is used as is; otherwise the instance is the
// unique one named like the responding agent.
func (h *Handler) isPersonalAssistantTurn(ctx context.Context, routeCtx normalizedChatRouteContext, agentName, instanceID string) bool {
	if h == nil || h.externalMemory == nil || h.externalMemoryGate == nil || h.userProvider == nil || h.workspaceStore == nil ||
		!shouldAttachWorkspaceSnapshot(routeCtx) {
		return false
	}
	ws, err := h.workspaceStore.Get(routeCtx.WorkspaceID)
	if err != nil || ws == nil {
		return false
	}
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		instanceID = respondingInstance(ws, agentName)
	}
	if instanceID == "" {
		return false
	}
	userID, err := h.userProvider.CurrentUserID(ctx)
	if err != nil || strings.TrimSpace(userID) == "" {
		return false
	}
	return h.externalMemoryGate.IsPersonalAssistant(ctx, userID, ws.ID, instanceID)
}

// externalMemorySection is the injected block for the personal assistant's
// own turns, or "".
func (h *Handler) externalMemorySection(ctx context.Context, routeCtx normalizedChatRouteContext) string {
	if !h.isPersonalAssistantTurn(ctx, routeCtx, routeCtx.AgentName, "") {
		return ""
	}
	return h.externalMemory.ContextBlock(workspace.MemoryPromptTokenBudget * 4)
}

// attachExternalMemoryRecall offers memory_recall on the personal assistant's
// chat turns only.
func (h *Handler) attachExternalMemoryRecall(wtp *WorkspaceToolProvider, agentName string, routeCtx normalizedChatRouteContext, instanceID string) {
	if wtp == nil || h == nil || h.externalMemory == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), externalMemoryGateTimeout)
	defer cancel()
	if h.isPersonalAssistantTurn(ctx, routeCtx, agentName, instanceID) {
		wtp.SetExternalMemoryRecall(h.externalMemory)
	}
}
