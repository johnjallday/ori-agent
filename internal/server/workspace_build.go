package server

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/store"
)

// wireWorkspaceBuild wires "Build with your assistant": build sessions kept
// beside the assistant's knowledge under the HQ, the same creation catalog
// GET /api/project-templates serves, and the assistant's own model.
func (b *ServerBuilder) wireWorkspaceBuild(knowledge *personalassistant.KnowledgeStore) {
	if b == nil || knowledge == nil || b.sessionHandler == nil || b.personalAssistantService == nil {
		return
	}
	buildStore := personalassistant.NewWorkspaceBuildStore(knowledge)
	b.workspaceBuildStore = buildStore
	deps := sessionhttp.WorkspaceBuildDeps{
		Store:     buildStore,
		Assistant: b.personalAssistantService.Get,
		// Resolved on every call: plugins enabled after startup change what
		// the user can build from, exactly as they change the catalog.
		Catalog: func(_ context.Context, userID string) ([]sessionhttp.WorkspaceBuildCatalogEntry, error) {
			var plugins installedPluginSource
			if b.pluginHandler != nil {
				plugins = b.pluginHandler.Manager()
			}
			snapshot, err := buildBlueprintCatalogSnapshot(resolveTemplatesRoot(b.configManager), templateRuntimeCatalog{
				capabilities: b.workspaceCapabilityRegistry, runtimes: b.runtimeCapabilityRegistry,
			}, plugins, userID)
			if err != nil {
				return nil, err
			}
			entries := make([]sessionhttp.WorkspaceBuildCatalogEntry, 0, len(snapshot.Entries))
			for _, entry := range snapshot.Entries {
				if !snapshot.Active[entry.Template.ID] {
					continue
				}
				entries = append(entries, sessionhttp.WorkspaceBuildCatalogEntry{Template: entry.Template, Readiness: entry.Readiness})
			}
			return entries, nil
		},
		ResolveModel: func(_ context.Context, profileName string) (sessionhttp.WorkspaceBuildModel, error) {
			return resolveWorkspaceBuildModel(b.st, b.configManager, b.llmFactory, profileName)
		},
		ModelAvailable: func(provider, model string) bool {
			_, ok := workspaceBuildModelFor(b.llmFactory, provider, model)
			return ok
		},
	}
	if b.userProvider != nil {
		deps.CurrentUserID = b.userProvider.CurrentUserID
	}
	b.sessionHandler.SetWorkspaceBuild(deps)
}

// resolveWorkspaceBuildModel picks the model a build talks to (FR30): the
// Personal Assistant's own agent profile when its provider and model
// resolve, otherwise the system model.
func resolveWorkspaceBuildModel(agents store.Store, configManager *config.Manager, factory *llm.Factory, profileName string) (sessionhttp.WorkspaceBuildModel, error) {
	if factory == nil {
		return sessionhttp.WorkspaceBuildModel{}, sessionhttp.ErrWorkspaceBuildNoModel
	}
	if name := strings.TrimSpace(profileName); name != "" && agents != nil {
		if agent, ok := agents.GetAgent(name); ok && agent != nil {
			provider := workspaceBuildProviderName(factory, agent.Settings.Provider, agent.Settings.Model)
			if model, ok := workspaceBuildModelFor(factory, provider, agent.Settings.Model); ok {
				return model, nil
			}
		}
	}
	if configManager != nil && configManager.IsSystemModelConfigured() {
		provider, model := configManager.GetSystemModel()
		if resolved, ok := workspaceBuildModelFor(factory, provider, model); ok {
			return resolved, nil
		}
	}
	return sessionhttp.WorkspaceBuildModel{}, sessionhttp.ErrWorkspaceBuildNoModel
}

// workspaceBuildProviderName names an agent's provider the way chat does: the
// "anthropic" alias is Claude, and an empty provider is inferred only from a
// model namespace that cannot be mistaken ("claude-…", "gemini-…", or a model a
// local server has loaded). An empty provider is never taken to mean OpenAI.
func workspaceBuildProviderName(factory *llm.Factory, provider, model string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "anthropic" {
		return "claude"
	}
	if provider != "" {
		return provider
	}
	lower := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(lower, "claude-"):
		return "claude"
	case strings.HasPrefix(lower, "gemini-"):
		return "gemini"
	}
	if factory != nil && lower != "" {
		return llm.FindLocalProviderByModel(factory, model)
	}
	return ""
}

// workspaceBuildModelFor resolves one provider/model pair the way the system
// model is resolved, including a local provider's check that the model is
// actually loaded.
func workspaceBuildModelFor(factory *llm.Factory, provider, model string) (sessionhttp.WorkspaceBuildModel, bool) {
	provider, model = strings.TrimSpace(provider), strings.TrimSpace(model)
	if factory == nil || provider == "" || model == "" {
		return sessionhttp.WorkspaceBuildModel{}, false
	}
	result, err := factory.GetSystemModelProvider(provider, model)
	if err != nil || result == nil || result.Provider == nil {
		return sessionhttp.WorkspaceBuildModel{}, false
	}
	if checker, ok := result.Provider.(llm.ModelPresenceChecker); ok && !checker.HasModel(result.Model) {
		return sessionhttp.WorkspaceBuildModel{}, false
	}
	return sessionhttp.WorkspaceBuildModel{Provider: result.Provider, ProviderName: provider, Model: result.Model}, true
}
