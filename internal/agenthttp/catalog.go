package agenthttp

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agentcatalog"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
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
		"hire":    hireCatalog(),
	})
}

// hireCatalog is what the Hire card needs beside the roles: each role's
// default brief and the blueprint its workspace starts from, and the bounds
// the hire enforces, so the card states exactly what the server will do.
func hireCatalog() map[string]any {
	roles := map[string]any{}
	for _, entry := range agentcatalog.Registry() {
		blueprint := hireBlueprintFor(entry.Slug)
		roles[string(entry.Slug)] = map[string]any{
			"default_brief":   personalassistant.HireDefaultBrief(entry.Slug, ""),
			"blueprint":       blueprint.ID,
			"blueprint_label": blueprint.Label,
			"open_roles":      blueprint.OpenRoles,
		}
	}
	return map[string]any{
		"roles":              roles,
		"domain_brief":       personalassistant.HireDomainBrief(),
		"brief_max":          personalassistant.WorkspaceGoalMaxRunes,
		"workspace_name_max": personalassistant.HireWorkspaceNameMaxRunes,
	}
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
	entry, ok := findCatalogEntry(catalogRole)
	if !ok {
		orihttp.BadRequest(w, fmt.Sprintf("unknown catalog role %q", catalogRole))
		return
	}

	created, err := h.createCatalogAgent(name, entry, domain, nil, nil)
	if err != nil {
		orihttp.BadRequest(w, err.Error())
		return
	}

	resp := map[string]any{
		"success":      true,
		"message":      "Agent '" + name + "' created successfully",
		"catalog_role": string(entry.Slug),
	}
	if !created.ModelResolved {
		resp["model_category_fallback"] = true
		resp["notice"] = created.ModelNotice()
	}
	orihttp.Success(w, resp)
}

// findCatalogEntry reads a Role Catalog entry by its slug, as a request names it.
func findCatalogEntry(catalogRole string) (agentcatalog.Entry, bool) {
	return agentcatalog.Find(types.AgentRole(strings.ToLower(strings.TrimSpace(catalogRole))))
}

// catalogAgent is an agent created from a Role Catalog entry, with the model
// its entry's tier resolved to.
type catalogAgent struct {
	Entry         agentcatalog.Entry
	Model         string
	ModelResolved bool
}

// ModelNotice says why a catalog agent uses the default model.
func (c catalogAgent) ModelNotice() string {
	return fmt.Sprintf("No model is configured for the %q tier yet; used your default model instead.", c.Entry.ModelTier)
}

// RunnableModelChooser picks the provider and model a hired agent runs on: the
// tier's model on a configured provider that offers it, else the system model,
// so the hire's first look can run. It returns the tier's model unchanged, with
// no provider, when nothing configured can run.
type RunnableModelChooser func(tierModel string) (provider, model string)

// SetRunnableModelChooser wires the hire's model choice (server: the LLM
// factory and the system model). Catalog create never uses it.
func (h *Handler) SetRunnableModelChooser(choose RunnableModelChooser) {
	h.runnableModel = choose
}

// createCatalogAgent creates the agent a catalog entry describes and applies
// its role, resolved model, starter prompt and starter skills, and (Specialist
// only) its domain. appearance, when set, is the agent's validated look; nil
// leaves the store's default. Catalog create and the hire share it so an agent
// hired from the Agents page is the one catalog create would make, except that
// a hire passes choose so its agent runs on a model that is set up.
func (h *Handler) createCatalogAgent(name string, entry agentcatalog.Entry, domain string, appearance *types.AgentAppearance, choose RunnableModelChooser) (catalogAgent, error) {
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
	provider := ""
	if choose != nil {
		var chosen string
		provider, chosen = choose(resolvedModel)
		modelResolved = modelResolved && chosen == resolvedModel
		resolvedModel = chosen
	}

	config := &store.CreateAgentConfig{
		Role:         entry.Slug,
		Model:        resolvedModel, // "" falls back to the store's default model
		LLMProvider:  provider,
		SystemPrompt: systemPrompt,
		Appearance:   appearance,
	}

	if err := h.State.CreateAgent(name, config); err != nil {
		logger.Error("CatalogCreateAgent error", logger.Fields{"error": err})
		return catalogAgent{}, err
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

	return catalogAgent{Entry: entry, Model: resolvedModel, ModelResolved: modelResolved}, nil
}
