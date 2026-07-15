package sessionhttp

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
)

// EnsureSpecialists implements personalhq.SpecialistProvisioner. It attaches the
// requested Personal HQ specialist roles to an existing designated HQ by reusing
// the same template roster + agent-seeding primitives as workspace creation
// (templateAgentCreateConfig + attachWorkspaceSpecialist), so Build My HQ and
// Upgrade converge on one provisioning path rather than a second constructor
// (task 2.9).
//
// It is idempotent: a role whose instance already exists is skipped, never
// duplicated. It never disturbs the workspace's existing entry agent,
// user-edited agents, tasks, or settings (task 2.6) — specialists are always
// attached as non-entry instances. Per-agent tools (when a role declares them)
// are bound to that agent only, never workspace-wide (task 2.8).
//
// On a mid-run agent-create failure it returns the roles added so far with an
// error, so the UpgradeCoordinator records a partial outcome and a later retry
// finishes the rest.
func (h *Handler) EnsureSpecialists(ctx context.Context, workspaceID string, roles []personalhq.SpecialistRole) ([]string, error) {
	if h == nil || h.store == nil || h.agentStore == nil {
		return nil, fmt.Errorf("session handler is not configured for specialist provisioning")
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" || len(roles) == 0 {
		return nil, nil
	}

	tpl, err := h.personalHQTemplate()
	if err != nil {
		return nil, err
	}
	specByName := make(map[string]projecttemplates.AgentSpec, len(tpl.Agents))
	for _, spec := range tpl.Agents {
		specByName[strings.ToLower(strings.TrimSpace(spec.Name))] = spec
	}

	ws, err := h.store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	var added []string
	var created []createdAgent
	for _, role := range roles {
		if _, exists := findWorkspaceInstanceByName(ws, role.AgentName); exists {
			continue // idempotent: role already present
		}
		spec, ok := specByName[strings.ToLower(strings.TrimSpace(role.AgentName))]
		if !ok {
			// The role is not declared by the personal-ops template. Skip rather
			// than invent an agent from nothing.
			logger.Warn("personal hq provisioner: role has no template spec; skipped",
				logger.Fields{"workspace": workspaceID, "role": role.AgentName})
			continue
		}
		if _, exists := h.agentStore.GetAgent(spec.Name); !exists {
			cfg, _ := h.templateAgentCreateConfig(spec)
			if err := h.agentStore.CreateAgent(spec.Name, cfg); err != nil {
				// Persist whatever we already attached before surfacing the
				// partial failure, so a retry sees the current roster.
				if len(added) > 0 {
					_ = h.store.UpdateWorkspace(ctx, ws)
				}
				return added, fmt.Errorf("failed to create specialist agent %q: %w", spec.Name, err)
			}
		}
		attachWorkspaceSpecialist(ws, spec.Name)
		added = append(added, spec.Name)
		if !spec.Tools.IsEmpty() {
			created = append(created, createdAgent{Name: spec.Name, Tools: spec.Tools})
		}
	}

	if len(added) == 0 {
		return nil, nil
	}
	if err := h.store.UpdateWorkspace(ctx, ws); err != nil {
		return added, fmt.Errorf("failed to persist specialist roster: %w", err)
	}
	// Per-agent tool binding (explicit per-agent access entries; never
	// workspace-wide authorization — task 2.8). No-op until a specialist role
	// declares tools.
	if warnings := h.bindSeededAgentTools(workspaceID, created); len(warnings) > 0 {
		logger.Info("personal hq provisioner: some specialist tools were skipped",
			logger.Fields{"workspace": workspaceID, "warnings": strings.Join(warnings, "; ")})
	}
	return added, nil
}

// personalHQTemplate loads the canonical personal-ops template from the library,
// the single source of truth for specialist prompts/roles.
func (h *Handler) personalHQTemplate() (projecttemplates.Template, error) {
	if h.templatesRootResolver == nil {
		return projecttemplates.Template{}, fmt.Errorf("templates library is not configured")
	}
	return projecttemplates.FindLibraryTemplate(h.templatesRootResolver(), personalhq.PersonalHQTemplateID)
}

func findWorkspaceInstanceByName(ws *session.Workspace, name string) (*session.AgentInstance, bool) {
	name = strings.TrimSpace(name)
	for i := range ws.AgentInstances {
		if strings.EqualFold(strings.TrimSpace(ws.AgentInstances[i].Name), name) {
			return &ws.AgentInstances[i], true
		}
	}
	return nil, false
}
