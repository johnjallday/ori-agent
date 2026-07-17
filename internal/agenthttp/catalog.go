package agenthttp

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agentcatalog"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
)

// ModelCategoryReader resolves a catalog entry's model tier to the current
// model-category assignments. Satisfied by store.ModelCategoryStore; kept
// narrow so agenthttp only depends on what it needs.
type ModelCategoryReader interface {
	GetAllModelAssignments() map[string][]string
}

// CatalogHandler serves GET /api/agents/catalog: the static Role Catalog, so
// the UI never hard-codes entries (PRD FR A.3).
func CatalogHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		orihttp.MethodNotAllowed(w)
		return
	}
	orihttp.WriteJSON(w, map[string]any{
		"entries": agentcatalog.Registry(),
	})
}

// SetModelCategoryStore wires model-category resolution for catalog-create
// (fast/balanced/deep -> a concrete configured model).
func (h *Handler) SetModelCategoryStore(s ModelCategoryReader) {
	h.modelCategoryStore = s
}

// SetSkillsManager wires the skills manager so catalog-create can enable a
// catalog entry's starter skills.
func (h *Handler) SetSkillsManager(m *skills.Manager) {
	h.skillsManager = m
}

// handleCatalogCreate creates an agent from a Role Catalog entry: role,
// resolved model, starter prompt, and starter skills are applied, and (for
// the Specialist entry only) an optional domain feeds RoutingProfile.Domains
// and the starter prompt (PRD FR A.4-6, FR "Resolved: yes" on Specialist
// domain).
func (h *Handler) handleCatalogCreate(w http.ResponseWriter, name, catalogRole, domain string) {
	entry, ok := agentcatalog.Find(types.AgentRole(strings.ToLower(strings.TrimSpace(catalogRole))))
	if !ok {
		orihttp.BadRequest(w, fmt.Sprintf("unknown catalog role %q", catalogRole))
		return
	}

	systemPrompt := entry.StarterPrompt
	domain = strings.TrimSpace(domain)
	if entry.SupportsDomain && domain != "" {
		systemPrompt = fmt.Sprintf("%s Your domain focus is: %s.", systemPrompt, domain)
	}

	var modelAssignments map[string][]string
	if h.modelCategoryStore != nil {
		modelAssignments = h.modelCategoryStore.GetAllModelAssignments()
	}
	resolvedModel, modelResolved := agentcatalog.ResolveModel(entry.ModelTier, modelAssignments)

	config := &store.CreateAgentConfig{
		Role:         entry.Slug,
		Model:        resolvedModel, // "" falls back to the store's default model
		SystemPrompt: systemPrompt,
	}

	if err := h.State.CreateAgent(name, config); err != nil {
		logger.Error("CatalogCreateAgent error", logger.Fields{"error": err})
		orihttp.BadRequest(w, err.Error())
		return
	}

	ag, ok := h.State.GetAgent(name)
	if ok && ag != nil {
		if entry.SupportsDomain && domain != "" {
			if ag.Metadata == nil {
				ag.Metadata = &types.AgentMetadata{}
			}
			ag.Metadata.RoutingProfile = &types.AgentRoutingProfile{Domains: []string{domain}}
			if err := h.State.SetAgent(name, ag); err != nil {
				logger.Error("Failed to set catalog agent domain metadata", logger.Fields{"err": err})
			}
		}
	}

	if h.skillsManager != nil {
		for _, skillName := range entry.StarterSkills {
			if err := h.skillsManager.SetSkillEnabled(name, skillName, true); err != nil {
				logger.Error("Failed to enable starter skill", logger.Fields{"skill": skillName, "err": err})
			}
		}
	}

	logger.Info("Agent created from catalog", logger.Fields{"agent": name, "catalog_role": string(entry.Slug)})

	if h.ActivityLogger != nil {
		details := map[string]any{
			"catalog_role": string(entry.Slug),
			"model":        resolvedModel,
		}
		if err := h.ActivityLogger.LogActivity(name, types.ActivityEventCreated, details, ""); err != nil {
			logger.Error("Failed to log activity", logger.Fields{"err": err})
		}
	}

	resp := map[string]any{
		"success":      true,
		"message":      "Agent '" + name + "' created successfully",
		"catalog_role": string(entry.Slug),
	}
	if !modelResolved {
		resp["model_category_fallback"] = true
		resp["notice"] = fmt.Sprintf("No model is configured for the %q tier yet; used your default model instead.", entry.ModelTier)
	}
	orihttp.Success(w, resp)
}
