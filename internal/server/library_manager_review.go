package server

import (
	"context"
	"strings"

	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// wireLibraryManagerReview subscribes the bounded Manager scan-review turn to
// library.scan_completed and closes receipts a previous process left started.
// Contract: docs/architecture/project-library.md, "Bounded Manager review turn".
func (b *ServerBuilder) wireLibraryManagerReview() {
	if b.eventBus == nil || b.workspaceStore == nil || b.sessionHandler == nil {
		return
	}
	library := projectlibrary.NewStore(b.workspaceStore).
		WithProviderEvidence(b.sessionHandler.AssistantLibraryProviderEvidence)
	runner := projectlibrary.NewManagerRunner(library, projectlibrary.ManagerRunHost{
		Tools: b.libraryManagerReviewTools,
		Model: b.libraryManagerModel,
		TokenBudget: func() int {
			if b.configManager == nil {
				return 0
			}
			return b.configManager.GetLibraryManagerTokenBudget()
		},
	})
	b.eventBus.Subscribe(runner.HandleScanCompleted, func(event workspace.Event) bool {
		return event.Type == workspace.EventLibraryScanCompleted
	})
	if closed := runner.SweepInterrupted(); closed > 0 {
		logger.Info("Closed interrupted Library Manager scan reviews", logger.Fields{"count": closed})
	}
}

// libraryManagerReviewTools builds the tools for exactly the authority the
// runner resolved, with its run context set so saved proposals carry
// provenance. The runner keeps only its allowlist of reads and proposals.
func (b *ServerBuilder) libraryManagerReviewTools(authority projectlibrary.ManagerAuthority) []toolapi.Tool {
	if b.sessionStore == nil || b.workspaceStore == nil || b.sessionHandler == nil {
		return nil
	}
	provider := chathttp.NewWorkspaceToolProvider(b.sessionStore, b.workspaceStore, authority.HomeID)
	provider.SetExecutingAgent(authority.AgentName)
	provider.SetExecutingInstanceID(authority.AgentInstanceID)
	provider.SetProjectLibraryEvidence(b.sessionHandler.AssistantLibraryProviderEvidence)
	provider.SetManagerRun(authority.Run)
	return provider.Tools()
}

// libraryManagerModel resolves the bound Manager's own model from its
// Home-local agent settings. A provider that cannot round-trip tool calls
// (for example a CLI provider) is not a model for this turn.
func (b *ServerBuilder) libraryManagerModel(_ context.Context, homeID, agentName string) (projectlibrary.ManagerChat, string, error) {
	if b.workspaceStore == nil || b.llmFactory == nil {
		return nil, "", projectlibrary.ErrNoManagerModel
	}
	local, found, err := b.workspaceStore.GetWorkspaceAgent(homeID, agentName)
	if err != nil || !found || local == nil {
		return nil, "", projectlibrary.ErrNoManagerModel
	}
	model := strings.TrimSpace(local.Settings.Model)
	providerName := libraryManagerProviderName(local.Settings.Provider, model, b.llmFactory)
	if providerName == "" || model == "" {
		return nil, "", projectlibrary.ErrNoManagerModel
	}
	provider, err := b.llmFactory.GetProvider(providerName)
	if err != nil || provider == nil || !provider.Capabilities().SupportsTools {
		return nil, "", projectlibrary.ErrNoManagerModel
	}
	return provider.Chat, model, nil
}

// libraryManagerProviderName mirrors the chat runtime's provider resolution:
// an explicit provider wins, a few model namespaces are unambiguous, and an
// empty provider is never assumed to be OpenAI.
func libraryManagerProviderName(provider, model string, factory *llm.Factory) string {
	switch name := strings.ToLower(strings.TrimSpace(provider)); name {
	case "anthropic":
		return "claude"
	case "openai", "codex", "claude_code", "claude", "gemini", "ollama", "lmstudio", "mlx_lm":
		return name
	case "":
	default:
		return ""
	}
	lower := strings.ToLower(model)
	switch {
	case lower == "":
		return ""
	case strings.HasPrefix(lower, "claude-"):
		return "claude"
	case strings.HasPrefix(lower, "gemini-"):
		return "gemini"
	}
	return llm.FindLocalProviderByModel(factory, model)
}
