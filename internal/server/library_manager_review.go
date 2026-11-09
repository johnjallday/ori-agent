package server

import (
	"context"
	"errors"
	"strings"

	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/folderlook"
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
// Home-local agent settings. A provider that cannot round-trip tool calls is
// not a model for this turn; the CLI providers can, through the brokered
// protocol.
func (b *ServerBuilder) libraryManagerModel(_ context.Context, homeID, agentName string) (projectlibrary.ManagerChat, string, error) {
	chat, ref, err := b.agentToolModel(homeID, agentName)
	if err != nil {
		return nil, "", projectlibrary.ErrNoManagerModel
	}
	return chat, ref.Model, nil
}

// agentToolModel resolves an agent's own tool-capable model in a workspace,
// as libraryManagerModel does, but says why there is none (PRD FR24): no
// model chosen, a provider that is not configured (an API provider without
// its key) or not available (a CLI provider that is not on this Mac), or a
// model that cannot call tools. A CLI provider that refuses a call is not
// signed in. Names only: the underlying error is wrapped for the log.
func (b *ServerBuilder) agentToolModel(workspaceID, agentName string) (projectlibrary.ManagerChat, folderlook.ModelRef, error) {
	noModel := func(err error) (projectlibrary.ManagerChat, folderlook.ModelRef, error) {
		return nil, folderlook.ModelRef{}, &folderlook.ModelError{Cause: folderlook.Cause{Kind: folderlook.CauseNoModel}, Err: err}
	}
	if b.workspaceStore == nil || b.llmFactory == nil {
		return noModel(errors.New("models are not wired"))
	}
	local, found, err := b.workspaceStore.GetWorkspaceAgent(workspaceID, agentName)
	if err != nil || !found || local == nil {
		if err == nil {
			err = errors.New("the agent is not in this workspace")
		}
		return noModel(err)
	}
	model := strings.TrimSpace(local.Settings.Model)
	if model == "" {
		return noModel(errors.New("the agent has no model"))
	}
	providerName := libraryManagerProviderName(local.Settings.Provider, model, b.llmFactory)
	ref := folderlook.ModelRef{Provider: providerDisplayName(providerName, local.Settings.Provider), Model: model}
	failed := func(kind string, err error) (projectlibrary.ManagerChat, folderlook.ModelRef, error) {
		return nil, ref, &folderlook.ModelError{Cause: folderlook.Cause{Kind: kind, Provider: ref.Provider, Model: model}, Err: err}
	}
	if providerName == "" {
		return failed(folderlook.CauseNotConfigured, errors.New("no provider resolves this model"))
	}
	provider, err := b.llmFactory.GetProvider(providerName)
	if err != nil || provider == nil {
		if err == nil {
			err = errors.New("the provider is not registered")
		}
		if cliProvider(providerName) {
			return failed(folderlook.CauseNotAvailable, err)
		}
		return failed(folderlook.CauseNotConfigured, err)
	}
	if !provider.Capabilities().SupportsTools {
		return failed(folderlook.CauseNoTools, errors.New("the provider cannot call tools"))
	}
	chat := provider.Chat
	if cliProvider(providerName) {
		chat = func(ctx context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
			response, err := provider.Chat(ctx, request)
			if err != nil {
				if classified := llm.ClassifyProviderError(providerName, err, 0, ""); classified != nil &&
					(classified.Category == llm.CategoryAuthentication || classified.Category == llm.CategoryNotConfigured) {
					return nil, &folderlook.ModelError{Cause: folderlook.Cause{Kind: folderlook.CauseNotSignedIn, Provider: ref.Provider, Model: model}, Err: err}
				}
			}
			return response, err
		}
	}
	return chat, ref, nil
}

// cliProvider is a provider that runs a CLI on this Mac under its own login.
func cliProvider(name string) bool {
	return name == "codex" || name == "claude_code"
}

// providerDisplayName is a provider's name as a person knows it.
func providerDisplayName(resolved, configured string) string {
	switch resolved {
	case "claude":
		return "Anthropic"
	case "openai":
		return "OpenAI"
	case "codex":
		return "Codex"
	case "claude_code":
		return "Claude Code"
	case "gemini":
		return "Gemini"
	case "ollama":
		return "Ollama"
	case "lmstudio":
		return "LM Studio"
	case "mlx_lm":
		return "MLX"
	}
	if name := strings.TrimSpace(configured); name != "" && len(name) <= 40 && !strings.ContainsAny(name, "/\\\x00\r\n") {
		return name
	}
	return "The provider"
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
