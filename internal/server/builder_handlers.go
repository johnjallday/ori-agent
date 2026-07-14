// Package server provides HTTP handler initialization methods for the ServerBuilder.
// This file contains the method for initializing all HTTP handlers.
package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	agenthttp "github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/cliagent"
	"github.com/johnjallday/ori-agent/internal/cliagenthttp"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/devicehttp"
	"github.com/johnjallday/ori-agent/internal/evolution"
	"github.com/johnjallday/ori-agent/internal/evolutionhttp"
	"github.com/johnjallday/ori-agent/internal/externalagents"
	"github.com/johnjallday/ori-agent/internal/externalagentshttp"
	"github.com/johnjallday/ori-agent/internal/featureflags"
	"github.com/johnjallday/ori-agent/internal/fileshttp"
	"github.com/johnjallday/ori-agent/internal/filewatcher"
	"github.com/johnjallday/ori-agent/internal/locationhttp"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/macwake"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcphttp"
	"github.com/johnjallday/ori-agent/internal/modelcategoryhttp"
	"github.com/johnjallday/ori-agent/internal/notehttp"
	"github.com/johnjallday/ori-agent/internal/onboardinghttp"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/personalhqhttp"
	"github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/pluginworkspace"
	"github.com/johnjallday/ori-agent/internal/reapersetup"
	"github.com/johnjallday/ori-agent/internal/review"
	"github.com/johnjallday/ori-agent/internal/reviewhttp"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/skillshttp"
	"github.com/johnjallday/ori-agent/internal/speechhttp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/usagehttp"
	"github.com/johnjallday/ori-agent/internal/userhttp"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/vault"
	"github.com/johnjallday/ori-agent/internal/vaulthttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacerun"
)

// initializeHandlers creates all HTTP handlers and wires up dependencies.
func (b *ServerBuilder) initializeHandlers() {
	b.locationHandler = locationhttp.NewHandler(b.locationManager)
	b.usageHandler = usagehttp.NewHandler(b.costTracker)
	b.mcpHandler = mcphttp.NewHandler(b.mcpRegistry, b.mcpConfigManager)
	b.macWakeService = macwake.NewService(b.configManager)
	b.settingsHandler = settingshttp.NewHandler(b.st, b.configManager, b.clientFactory, b.llmFactory)
	b.settingsHandler.SetMacWakeService(b.macWakeService)
	b.speechHandler = speechhttp.NewHandler(b.configManager)

	b.chatHandler = chathttp.NewHandler(b.st, b.clientFactory)
	b.chatHandler.SetLLMFactory(b.llmFactory)
	b.chatHandler.SetCostTracker(b.costTracker)
	b.chatHandler.SetMCPRegistry(b.mcpRegistry)
	b.chatHandler.SetMCPConfigManager(b.mcpConfigManager)
	b.chatHandler.SetWorkspaceStore(b.workspaceStore) // Will be set later

	applyUtilitySettings := func() {
		cfg := b.configManager.Get()
		b.utilityToolRegistry = buildUtilityToolRegistry(cfg.Utility)
		b.chatHandler.SetUtilityToolRegistry(b.utilityToolRegistry)
		if b.taskHandler != nil {
			b.taskHandler.SetUtilityToolProvider(b.utilityToolRegistry)
		}
		if b.orchestrationTaskHandler != nil {
			b.orchestrationTaskHandler.SetUtilityToolProvider(b.utilityToolRegistry)
		}
		b.chatHandler.SetBrowserMCPPreference(cfg.Utility.BrowserControlProvider)
		b.syncPlaywrightBrowserSettings(cfg.Utility)
	}
	applyUtilitySettings()
	b.settingsHandler.SetUtilitySettingsReloader(applyUtilitySettings)

	b.usageHandler.SetUtilityTelemetry(b.chatHandler.UtilityTelemetry())
	if featureflags.EvolutionEnabled() {
		b.evolutionService = evolution.NewService(b.st, b.onboardingMgr, nil)
		b.evolutionService.SetActivityLogger(b.activityLogger)
		b.chatHandler.SetEvolutionService(b.evolutionService)
		logger.Info("Evolution feature enabled", logger.Fields{})
	} else {
		b.evolutionService = nil
		b.chatHandler.SetEvolutionService(nil)
		logger.Info("Evolution feature disabled via ORI_EVOLUTION_ENABLED", logger.Fields{})
	}
	b.chatHandler.SetShutdownFunc(func() {
		logger.Info("Shutting down ori-agent server", logger.Fields{})
		b.server.Shutdown()
		logger.Info("Server shut down complete, exiting", logger.Fields{})
		os.Exit(0)
	})

	if b.evolutionService != nil {
		b.evolutionHandler = evolutionhttp.NewHandler(b.st, b.onboardingMgr, b.evolutionService)
	} else {
		b.evolutionHandler = nil
	}
	b.onboardingHandler = onboardinghttp.NewHandler(b.onboardingMgr)
	b.deviceHandler = devicehttp.NewHandler(b.onboardingMgr)
	b.resetHandler = settingshttp.NewResetHandler(b.onboardingMgr, b.st, ".")

	// Initialize auto-config handler for agent creation
	b.autoConfigHandler = agenthttp.NewAutoConfigHandler(b.llmFactory, b.configManager)

	// Initialize smart onboarding handler
	systemProvider, systemModel := b.configManager.GetSystemModel()
	b.smartOnboardingHandler = onboardinghttp.NewSmartOnboardingHandler(b.st, b.llmFactory, b.onboardingMgr, systemProvider, systemModel)

	// Initialize model category store and handler
	modelCategoryStore, err := store.NewFileModelCategoryStore("model_categories.json")
	if err != nil {
		logger.Error("Failed to create model category store", logger.Fields{"error": err})
		// Non-fatal: continue without model categories
	} else {
		b.modelCategoryStore = modelCategoryStore
		b.modelCategoryHandler = modelcategoryhttp.NewHandler(modelCategoryStore)
		b.autoCategorizeHandler = modelcategoryhttp.NewAutoCategorizeHandler(modelCategoryStore, b.llmFactory, b.configManager)
	}

	// Initialize session store and handler
	ctx := context.Background()
	sessionStore, err := session.NewHybridStore(ctx, session.DefaultHybridStoreConfig())
	if err != nil {
		logger.Error("Failed to create session store", logger.Fields{"error": err})
		// Non-fatal: continue without session management
	} else {
		b.sessionStore = sessionStore
		b.userProvider = userprofile.LocalUserProvider{}
		userProfileStore := userprofile.NewSQLiteStore(sessionStore.DB())
		b.userStore = userProfileStore
		b.onboardingMgr.SetUserStore(b.userStore)
		if err := b.onboardingMgr.SeedLocalUserProfile(ctx); err != nil {
			logger.Warn("Failed to seed local user profile", logger.Fields{"error": err})
		}
		b.userHandler = userhttp.NewHandler(b.userStore, b.userProvider)
		// Personal HQ needs the concrete SQLite store (not the narrower
		// userprofile.UserStore interface) for its focused designation/
		// onboarding-state methods; see internal/personalhq.ProfileStore.
		b.personalHQService = personalhq.NewService(userProfileStore, sessionStore)
		b.personalHQHandler = personalhqhttp.NewHandler(b.personalHQService, b.userProvider)
		b.chatHandler.SetUserProfileDeps(b.userStore, b.userProvider)
		b.sessionHandler = sessionhttp.New(sessionStore)
		b.sessionHandler.SetWorkspaceRootResolver(func() string {
			return resolveWorkspaceRoot(b.configManager)
		})
		b.sessionHandler.SetTemplatesRootResolver(func() string {
			return resolveTemplatesRoot(b.configManager)
		})
		b.sessionHandler.SetAgentStore(b.st)
		if b.configManager != nil {
			b.sessionHandler.SetSystemModelReader(b.configManager)
		}
		// Initialize auto-classify handler for session classification
		b.autoClassifyHandler = sessionhttp.NewAutoClassifyHandler(sessionStore, b.st, b.llmFactory, b.configManager)
		// Initialize smart input handler for Workspace Hub classification
		b.smartInputHandler = sessionhttp.NewSmartInputHandler(sessionStore, b.llmFactory, b.configManager)
		// Initialize note generation handler
		b.noteHandler = notehttp.NewHandler(b.llmFactory, b.configManager, b.st)
		// Wire session store to chat handler for multi-tab support
		b.chatHandler.SetSessionStore(sessionStore)
		// Wire tool call store for conversation review
		b.chatHandler.SetToolCallStore(sessionStore.ToolCallStore())
	}

	// Initialize session files store and handler
	sessionFilesPath := filepath.Join(".", "session_files")
	sessionFilesStore, err := sessionfiles.NewStore(sessionFilesPath)
	if err != nil {
		logger.Error("Failed to create session files store", logger.Fields{"error": err})
		// Non-fatal: continue without session files management
	} else {
		b.sessionFilesStore = sessionFilesStore

		// Create file watcher
		watcher, err := filewatcher.NewWatcher(filewatcher.DefaultWatcherConfig())
		if err != nil {
			logger.Error("Failed to create file watcher", logger.Fields{"error": err})
		} else {
			b.sessionFilesWatcher = watcher
			watcher.Start()
		}

		// Create files HTTP handler
		b.sessionFilesHandler = fileshttp.NewHandler(sessionFilesStore, b.sessionFilesWatcher)
		logger.Info("Session files management initialized", logger.Fields{"path": sessionFilesPath})
	}

	// Initialize review system
	if b.sessionStore != nil {
		reviewStore := review.NewSQLiteStore(b.sessionStore.DB())
		reviewRunner := review.NewRunner(
			reviewStore,
			b.sessionStore,
			b.sessionStore.ToolCallStore(),
			review.DefaultDetectionConfig(),
		)
		// Wire up agent store for per-agent review settings
		if b.st != nil {
			reviewRunner.SetAgentStore(b.st)
		}
		b.reviewHandler = reviewhttp.NewHandler(reviewRunner, reviewStore)
		logger.Info("Review system initialized", logger.Fields{})

		vaultStore := vault.NewStore(b.sessionStore.DB(), vault.StoreOptions{
			ManagedVaultRoot: resolveVaultRoot(b.configManager),
		})
		b.vaultHandler = vaulthttp.NewHandler(vaultStore)
		b.settingsHandler.SetVaultRootUpdater(vaultStore.SetManagedVaultRoot)
		if b.workspaceHandler != nil {
			b.workspaceHandler.SetEmailAccountStore(vaultStore)
		}
		logger.Info("Vault system initialized", logger.Fields{})
	}

	// Initialize external agents (Claude Code, Codex)
	claudeReader := externalagents.NewClaudeReader("")
	codexReader := externalagents.NewCodexReader("")
	b.externalAgentsCache = externalagents.NewCache(claudeReader, codexReader)
	if err := b.externalAgentsCache.Load(); err != nil {
		logger.Warn("Failed to load external agents cache", logger.Fields{"error": err})
		// Non-fatal: continue without external agents
	}
	// claudeCLIDetected is evaluated lazily so it reflects the CLI registry,
	// which is created just below (after this handler). By request time the
	// registry has run AutoDetect.
	claudeCLIDetected := func() bool {
		return b.cliAgentRegistry != nil && b.cliAgentRegistry.IsAvailable(cliagent.BackendClaude)
	}
	codexCLIDetected := func() bool {
		return b.cliAgentRegistry != nil && b.cliAgentRegistry.IsAvailable(cliagent.BackendCodex)
	}
	b.externalAgentsHandler = externalagentshttp.New(b.externalAgentsCache, b.configManager, claudeCLIDetected, codexCLIDetected)
	logger.Info("External agents support initialized", logger.Fields{})

	// Initialize CLI agent adapter (delegatable CLI agents)
	b.cliAgentRegistry = cliagent.NewRegistry()
	b.cliAgentRegistry.AutoDetect()
	b.cliAgentLogger = cliagent.NewEventLogger(b.agentStorePath)

	// Create step planner using system model if available
	var cliPlanner *cliagent.StepPlanner
	if b.llmFactory != nil && b.configManager != nil {
		sysProvider, sysModel := b.configManager.GetSystemModel()
		if sysProvider != "" && sysModel != "" {
			if p, err := b.llmFactory.GetProvider(sysProvider); err == nil {
				cliPlanner = cliagent.NewStepPlanner(p, sysModel)
			}
		}
	}
	if cliPlanner == nil && b.llmFactory != nil {
		// Fallback: try any available provider
		for _, info := range b.llmFactory.ListProviders() {
			if p, err := b.llmFactory.GetProvider(info.Name); err == nil {
				models := p.DefaultModels()
				model := ""
				if len(models) > 0 {
					model = models[0]
				}
				cliPlanner = cliagent.NewStepPlanner(p, model)
				break
			}
		}
	}

	b.cliAgentExecutor = cliagent.NewMicroStepExecutor(
		b.cliAgentRegistry,
		cliPlanner,
		b.cliAgentLogger,
		cliagent.NewDiffDetector(),
		b.costTracker,
	)
	b.cliAgentHandler = cliagenthttp.NewHandler(b.cliAgentExecutor, b.cliAgentRegistry, b.cliAgentLogger)
	logger.Info("CLI agent adapter initialized", logger.Fields{
		"backends": len(b.cliAgentRegistry.List()),
	})

	if b.sessionStore != nil {
		b.workspaceRunStore = workspacerun.NewSQLiteStore(b.sessionStore.DB())
	} else {
		b.workspaceRunStore = workspacerun.NewMemoryStore()
	}

	runProfiles := workspacerun.NewProfileRegistry()
	b.workspaceRunExecutors = workspacerun.NewExecutorRegistry()
	nativeCLIExecutor := workspacerun.NewNativeCLIExecutor(b.cliAgentRegistry)
	if b.workspaceFileStore != nil {
		// Inject workspace memory into native-CLI run prompts (read-only context).
		nativeCLIExecutor.SetWorkspaceFolderResolver(b.workspaceFileStore)
	}
	b.workspaceRunExecutors.Register(workspacerun.ExecutorKindNativeCLI, nativeCLIExecutor)
	b.workspaceRunExecutors.Register(workspacerun.ExecutorKindOriAgent, workspacerun.NewOriAgentExecutor())
	runEnv := workspacerun.NewLocalEnvironmentManager("")
	runValidator := workspacerun.NewValidator()
	resolveRunRoots := func(workspaceID string) []string {
		root := resolveWorkspaceRoot(b.configManager)
		if strings.TrimSpace(root) == "" {
			return nil
		}
		return []string{root}
	}
	b.workspaceRunService = workspacerun.NewService(b.workspaceRunStore, runProfiles, b.workspaceRunExecutors, runEnv, runValidator, resolveRunRoots)
	if b.workspaceStore != nil {
		b.workspaceRunService.SetTaskReferenceURLResolver(func(_ context.Context, workspaceID, taskID string) (string, error) {
			ws, err := b.workspaceStore.Get(workspaceID)
			if err != nil || ws == nil {
				return "", err
			}
			for _, task := range ws.Tasks {
				if task.ID == taskID {
					return task.ReferenceURL, nil
				}
			}
			return "", nil
		})
	}
	b.workspaceRunHandler = workspacerun.NewHandler(b.workspaceRunStore, b.workspaceRunService)
	b.registerWorkspaceRunTaskValidationMirror()
	logger.Info("Workspace Runs initialized", logger.Fields{
		"durable": b.sessionStore != nil,
	})

	// Initialize skills manager and handler (local + external)
	personalSkillsDir := ""
	if homeDir, err := os.UserHomeDir(); err == nil {
		personalSkillsDir = filepath.Join(homeDir, ".agents", "skills")
	}
	b.skillsManager = skills.NewManager(skills.ManagerConfig{
		AgentStorePath:    b.agentStorePath,
		PersonalSkillsDir: personalSkillsDir,
		ExternalAgents:    b.externalAgentsCache,
		ConfigManager:     b.configManager,
	})
	b.skillsHandler = skillshttp.New(b.skillsManager, b.st, b.llmFactory, b.configManager)
	b.chatHandler.SetSkillsManager(b.skillsManager)

	// Plugin installer (Claude Code- and Codex-compatible bundles): wired over the
	// MCP registry + the personal skills dir; installed plugins live under ./plugins.
	if b.mcpConfigManager != nil && b.mcpRegistry != nil {
		b.pluginHandler = pluginhttp.NewHandler(b.mcpConfigManager, b.mcpRegistry, personalSkillsDir, "plugins")
	}

	// Let workspaces created from a template bind its declared default tools
	// (skills / MCP servers / plugins) at creation, applying only what is present.
	// The applier reads the SyncStore + MCP config lazily at request time.
	if b.sessionHandler != nil {
		b.sessionHandler.SetTemplateToolApplier(makeTemplateToolApplier(b))
		b.sessionHandler.SetAgentToolApplier(makeAgentToolApplier(b))
	}
}

// wireReaperSetup wires the normalized REAPER readiness resolver, pre-create
// preview lister, shared reconciler, and repairer onto the session handler so the
// create modal, workspace UI, and repair all read one truthful model backed by
// the configured plugin manager and synchronized workspace store.
//
// It must be called AFTER the workspace store exists (Phase 18), not during
// initializeHandlers (Phase 17) where b.workspaceStore is still nil — otherwise
// every REAPER endpoint stays nil-guarded and the create preview always reports
// plugin_missing.
func (b *ServerBuilder) wireReaperSetup() {
	if b.sessionHandler == nil || b.pluginHandler == nil || b.workspaceStore == nil {
		return
	}
	reconciler := pluginworkspace.New(b.pluginHandler.Manager(), b.workspaceStore)
	resolver := reapersetup.NewResolver(b.workspaceStore, reconciler)
	repairer := reapersetup.NewRepairer(b.workspaceStore, reconciler, resolver)
	b.sessionHandler.SetReaperSetup(resolver, b.pluginHandler.Manager(), reconciler, repairer)
}

func (b *ServerBuilder) registerWorkspaceRunTaskValidationMirror() {
	store := b.workspaceRunStore
	if store == nil {
		workspace.SetTaskValidationMirror(nil)
		return
	}
	workspace.SetTaskValidationMirror(func(workspaceID, taskID, runID string, validation workspace.TaskValidationResult) {
		output := taskValidationToWorkspaceRunOutput(taskID, validation)
		if err := store.SetTaskOutput(context.Background(), workspaceID, runID, output); err != nil {
			logger.Warn("Failed to mirror task output validation to workspace run", logger.Fields{
				"workspace_id": workspaceID,
				"task_id":      taskID,
				"run_id":       runID,
				"error":        err,
			})
		}
		mirrorTaskOutputArtifacts(context.Background(), store, workspaceID, runID, taskID, validation)
	})
}

func mirrorTaskOutputArtifacts(ctx context.Context, store workspacerun.Store, workspaceID, runID, taskID string, validation workspace.TaskValidationResult) {
	if store == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(runID) == "" {
		return
	}
	baseMetadata := map[string]any{
		"task_id":           taskID,
		"contract_version":  validation.ContractVersion,
		"validation_status": string(validation.ValidationStatus),
		"storage_status":    string(validation.StorageStatus),
	}
	if validation.NormalizedRow != nil {
		if data, err := json.Marshal(validation.NormalizedRow); err == nil {
			_, _ = store.AddArtifact(ctx, workspaceID, runID, workspacerun.NewArtifact(
				runID,
				workspacerun.ArtifactTaskNormalizedRow,
				workspacerun.ArtifactInline(data),
				workspacerun.ArtifactMetadata(baseMetadata),
			))
		}
	}
	if data, err := json.Marshal(validation); err == nil {
		_, _ = store.AddArtifact(ctx, workspaceID, runID, workspacerun.NewArtifact(
			runID,
			workspacerun.ArtifactTaskOutputValidation,
			workspacerun.ArtifactInline(data),
			workspacerun.ArtifactMetadata(baseMetadata),
		))
	}
	if strings.TrimSpace(validation.RepairStatus) != "" {
		repair := map[string]any{
			"task_id":           taskID,
			"contract_version":  validation.ContractVersion,
			"repair_status":     validation.RepairStatus,
			"validation_status": validation.ValidationStatus,
			"error_count":       len(validation.Errors),
		}
		if data, err := json.Marshal(repair); err == nil {
			_, _ = store.AddArtifact(ctx, workspaceID, runID, workspacerun.NewArtifact(
				runID,
				workspacerun.ArtifactTaskOutputRepair,
				workspacerun.ArtifactInline(data),
				workspacerun.ArtifactMetadata(baseMetadata),
			))
		}
	}
	receipt := map[string]any{
		"task_id":           taskID,
		"contract_version":  validation.ContractVersion,
		"storage_status":    validation.StorageStatus,
		"validation_status": validation.ValidationStatus,
		"error_count":       len(validation.Errors),
	}
	if data, err := json.Marshal(receipt); err == nil {
		_, _ = store.AddArtifact(ctx, workspaceID, runID, workspacerun.NewArtifact(
			runID,
			workspacerun.ArtifactTaskStorageReceipt,
			workspacerun.ArtifactInline(data),
			workspacerun.ArtifactMetadata(baseMetadata),
		))
	}
}

func taskValidationToWorkspaceRunOutput(taskID string, validation workspace.TaskValidationResult) workspacerun.TaskOutputSummary {
	errors := make([]workspacerun.TaskOutputValidationError, 0, len(validation.Errors))
	for _, item := range validation.Errors {
		errors = append(errors, workspacerun.TaskOutputValidationError{
			Code:     item.Code,
			Column:   item.Column,
			Message:  item.Message,
			Expected: append([]string(nil), item.Expected...),
			Actual:   append([]string(nil), item.Actual...),
		})
	}
	return workspacerun.TaskOutputSummary{
		TaskID:           taskID,
		ValidationStatus: string(validation.ValidationStatus),
		StorageStatus:    string(validation.StorageStatus),
		ContractVersion:  validation.ContractVersion,
		ValidatedAt:      validation.ValidatedAt,
		ErrorCount:       len(validation.Errors),
		Errors:           errors,
		RawOutputRef:     validation.RawOutputRef,
		NormalizedRowRef: validation.NormalizedRowRef,
		RepairStatus:     validation.RepairStatus,
		ManualApproval:   validation.ManualApproval != nil || validation.ValidationStatus == workspace.TaskValidationManuallyApproved,
	}
}

func (b *ServerBuilder) syncPlaywrightBrowserSettings(utility config.UtilitySettings) {
	if b == nil || b.mcpConfigManager == nil || b.mcpRegistry == nil {
		return
	}

	current, err := b.mcpConfigManager.GetServer("playwright")
	if err != nil || current == nil {
		return // Playwright MCP is not configured.
	}

	desired := *current
	desired.Env = resolvePlaywrightEnv(desired.Env, utility)
	if stringMapEqual(current.Env, desired.Env) {
		return
	}

	if err := b.mcpConfigManager.UpdateServer(desired); err != nil {
		logger.Warn("failed to persist Playwright MCP browser settings", logger.Fields{"error": err})
		return
	}

	wasRunning := false
	status, statusErr := b.mcpRegistry.GetServerStatus("playwright")
	if statusErr == nil {
		wasRunning = status == mcp.StatusRunning || status == mcp.StatusStarting || status == mcp.StatusRestarting
	}

	if err := b.mcpRegistry.RemoveServer("playwright"); err != nil && !strings.Contains(strings.ToLower(err.Error()), "not found") {
		logger.Warn("failed to reload Playwright MCP server after settings update", logger.Fields{"error": err})
		return
	}

	if err := b.mcpRegistry.AddServer(desired); err != nil {
		logger.Warn("failed to re-register Playwright MCP server after settings update", logger.Fields{"error": err})
		return
	}

	if wasRunning {
		if err := b.mcpRegistry.StartServer("playwright"); err != nil {
			logger.Warn("failed to restart Playwright MCP server after settings update", logger.Fields{"error": err})
		}
	}
}

func resolvePlaywrightEnv(existing map[string]string, utility config.UtilitySettings) map[string]string {
	next := make(map[string]string, len(existing))
	for k, v := range existing {
		next[k] = v
	}

	browserChoice := normalizePlaywrightBrowserChoice(utility.PlaywrightBrowser)
	executablePath := strings.TrimSpace(utility.PlaywrightExecutable)
	if browserChoice == "auto" && executablePath == "" {
		// No explicit override requested; preserve existing server-level configuration.
		return next
	}

	if browserChoice == "brave" {
		if executablePath == "" {
			executablePath = detectDefaultBraveExecutablePath()
		}
		browserChoice = "chrome"
	}

	delete(next, "PLAYWRIGHT_MCP_BROWSER")
	delete(next, "PLAYWRIGHT_MCP_EXECUTABLE_PATH")

	if browserChoice != "auto" {
		next["PLAYWRIGHT_MCP_BROWSER"] = browserChoice
	}
	if executablePath != "" {
		next["PLAYWRIGHT_MCP_EXECUTABLE_PATH"] = executablePath
	}

	return next
}

func normalizePlaywrightBrowserChoice(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "chrome", "firefox", "webkit", "msedge", "brave":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "auto"
	}
}

func detectDefaultBraveExecutablePath() string {
	candidates := []string{}

	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates, "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser")
	case "linux":
		candidates = append(candidates,
			"/usr/bin/brave-browser",
			"/usr/bin/brave-browser-stable",
			"/snap/bin/brave",
		)
	case "windows":
		programFiles := strings.TrimSpace(os.Getenv("ProgramFiles"))
		programFilesX86 := strings.TrimSpace(os.Getenv("ProgramFiles(x86)"))
		if programFiles != "" {
			candidates = append(candidates, filepath.Join(programFiles, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"))
		}
		if programFilesX86 != "" {
			candidates = append(candidates, filepath.Join(programFilesX86, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"))
		}
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return ""
}

func stringMapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
