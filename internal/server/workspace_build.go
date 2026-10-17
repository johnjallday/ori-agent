package server

import (
	"context"
	"strings"
	"sync"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/foldercontext"
	"github.com/johnjallday/ori-agent/internal/githubhttp"
	"github.com/johnjallday/ori-agent/internal/linkcontext"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/store"
)

var (
	linkObserverOnce sync.Once
	linkObserver     *linkcontext.Observer
)

// sharedLinkObserver is the one observer every attach path shares: the build
// pane's Add link and the assistant composer's. It holds no per-user state.
func sharedLinkObserver() *linkcontext.Observer {
	linkObserverOnce.Do(func() { linkObserver = newLinkObserver() })
	return linkObserver
}

// newLinkObserver reads GitHub repositories through the REST API (with the
// user's stored GitHub token when one is connected and the vault is unlocked,
// anonymously otherwise), and every other address through the bounded public
// page reader.
func newLinkObserver() *linkcontext.Observer {
	token := func(ctx context.Context) (string, bool) {
		value, ok, err := mcp.LoadStaticBearerToken(ctx, githubhttp.MCPServerConfig())
		if err != nil || !ok {
			return "", false
		}
		return value, true
	}
	return linkcontext.NewObserver(linkcontext.NewGitHubResolver(nil, token), linkcontext.NewHTTPPages(nil))
}

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
				if !snapshot.Active[entry.ID] {
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
	// The pane's Add folder is the composer's folder observation service,
	// which a later phase attaches to the storage facade; it is looked up on
	// every call. Without a folder digest there is no chooser to offer.
	if b.personalAssistantFolderDigest != nil {
		deps.Folders = buildFolderObservations{builder: b}
	}
	if b.linkObserver == nil {
		b.linkObserver = sharedLinkObserver()
	}
	deps.Links = b.linkObserver
	b.sessionHandler.SetWorkspaceBuild(deps)
}

// buildFolderObservations hands build sessions the composer's folder
// observation service, resolved at call time because it is wired after the
// build endpoints are.
type buildFolderObservations struct{ builder *ServerBuilder }

func (o buildFolderObservations) service() *personalassistant.FolderObservationService {
	if o.builder == nil || o.builder.server == nil {
		return nil
	}
	return o.builder.server.Storage.PersonalAssistantFolderObservations
}

func (o buildFolderObservations) Choices(ctx context.Context, userID string) (personalassistant.FolderDigestView, error) {
	service := o.service()
	if service == nil {
		return personalassistant.FolderDigestView{}, personalassistant.ErrFolderSelection
	}
	return service.Choices(ctx, userID)
}

func (o buildFolderObservations) Observe(ctx context.Context, target foldercontext.Target, mode, choice string) (*foldercontext.Observation, error) {
	service := o.service()
	if service == nil {
		return nil, personalassistant.ErrFolderSelection
	}
	return service.Observe(ctx, target, mode, choice)
}

func (o buildFolderObservations) ResolveRoot(ctx context.Context, target foldercontext.Target, id string) (string, error) {
	service := o.service()
	if service == nil {
		return "", personalassistant.ErrFolderSelection
	}
	return service.ResolveRoot(ctx, target, id)
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
