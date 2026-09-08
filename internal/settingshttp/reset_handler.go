package settingshttp

import (
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/settingsreset"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const maxResetRequestSize = 1024

// ResetRequest describes legacy selection only. It is not executable without a
// reviewed, server-held preview; booleans never become a Start Fresh request.
type ResetRequest struct {
	Settings     bool   `json:"settings"`
	Agents       bool   `json:"agents"`
	Sessions     bool   `json:"sessions"`
	Onboarding   bool   `json:"onboarding"`
	Confirmation string `json:"confirmation"`
}

type ResetResponse struct {
	Success         bool                     `json:"success"`
	ResetItems      []string                 `json:"reset_items"`
	Errors          []string                 `json:"errors,omitempty"`
	RequiresRestart bool                     `json:"requires_restart"`
	Code            string                   `json:"code,omitempty"`
	Message         string                   `json:"message,omitempty"`
	Operation       *settingsreset.Operation `json:"operation,omitempty"`
}

// ResetHandler has no file-deletion or live-cache clearing capability.
// Runtime construction attaches the planner and, only when safe, a coordinator.
type ResetHandler struct {
	onboardingMgr  *onboarding.Manager
	store          store.Store
	workspaceStore *workspace.FileStore
	dataDir        string
	planner        *settingsreset.Planner
	coordinator    *settingsreset.Coordinator
}

func NewResetHandler(mgr *onboarding.Manager, agents store.Store, dataDir string) *ResetHandler {
	h := &ResetHandler{onboardingMgr: mgr, store: agents, dataDir: dataDir}
	h.planner = settingsreset.NewPlanner(func() settingsreset.Owners {
		return settingsreset.Owners{DataDir: h.dataDir, Agents: h.store, Setup: h.onboardingMgr, Workspaces: h.workspaceStore}
	})
	return h
}

func (h *ResetHandler) DataDir() string                           { return h.dataDir }
func (h *ResetHandler) SetWorkspaceStore(ws *workspace.FileStore) { h.workspaceStore = ws }

// SetCoordinator is initialization-only. Alternate hosts without a complete
// lifecycle return unavailable, never fall back to legacy live deletion.
func (h *ResetHandler) SetCoordinator(c *settingsreset.Coordinator) { h.coordinator = c }
