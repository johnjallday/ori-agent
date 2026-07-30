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
	"time"

	agenthttp "github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/calendarhttp"
	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/cliagent"
	"github.com/johnjallday/ori-agent/internal/cliagenthttp"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/connections"
	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	"github.com/johnjallday/ori-agent/internal/devicehttp"
	"github.com/johnjallday/ori-agent/internal/downloadsjanitor"
	"github.com/johnjallday/ori-agent/internal/downloadsjanitorhttp"
	"github.com/johnjallday/ori-agent/internal/evolution"
	"github.com/johnjallday/ori-agent/internal/evolutionhttp"
	"github.com/johnjallday/ori-agent/internal/externalagents"
	"github.com/johnjallday/ori-agent/internal/externalagentshttp"
	"github.com/johnjallday/ori-agent/internal/featureflags"
	"github.com/johnjallday/ori-agent/internal/fileshttp"
	"github.com/johnjallday/ori-agent/internal/filewatcher"
	"github.com/johnjallday/ori-agent/internal/followup"
	"github.com/johnjallday/ori-agent/internal/locationhttp"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/macwake"
	"github.com/johnjallday/ori-agent/internal/mailbox"
	"github.com/johnjallday/ori-agent/internal/mailboxvault"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcphttp"
	"github.com/johnjallday/ori-agent/internal/meetingprep"
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
	"github.com/johnjallday/ori-agent/internal/wakecoord"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacerun"
)

// initializeHandlers creates all HTTP handlers and wires up dependencies.
func (b *ServerBuilder) initializeHandlers() {
	b.locationHandler = locationhttp.NewHandler(b.locationManager)
	b.usageHandler = usagehttp.NewHandler(b.costTracker)
	b.mcpHandler = mcphttp.NewHandler(b.mcpRegistry, b.mcpConfigManager)
	b.macWakeService = macwake.NewService(b.configManager)
	// Ori owns one system wake event and this service is the only thing that
	// programs it. The shared coordinator is how other Ori processes — today
	// the Herdr devflow helper's Overnight Runs — ask for one without ever
	// calling pmset themselves.
	if dir, err := wakecoord.DefaultDir(); err == nil {
		b.macWakeService.UseCoordinator(wakecoord.New(dir))
	}
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
		// Personal HQ needs the concrete SQLite store (not the narrower
		// userprofile.UserStore interface) for its focused designation/
		// onboarding-state methods; see internal/personalhq.ProfileStore.
		// The setup coordinator reuses b.sessionHandler's exact production
		// workspace-creation path (internal/personalhq.WorkspaceCreator) so
		// Build My HQ never duplicates that logic.
		b.personalHQService = personalhq.NewService(userProfileStore, sessionStore)
		personalHQSetup := personalhq.NewSetupCoordinator(b.personalHQService, b.sessionHandler, sessionStore)
		// The upgrade coordinator reuses b.sessionHandler as the specialist
		// provisioner (EnsureSpecialists), so Build My HQ and Upgrade converge on
		// one provisioning path (task 2.9).
		personalHQUpgrade := personalhq.NewUpgradeCoordinator(b.personalHQService, sessionStore, b.sessionHandler)
		b.personalHQHandler = personalhqhttp.NewHandler(b.personalHQService, personalHQSetup, personalHQUpgrade, b.userProvider)
		// The workspace stores are constructed in a later phase. Passing the
		// method value keeps Watchtower reads live once those stores are ready.
		b.personalHQHandler.SetWatchtowerSources(b.watchtowerSnapshotSources)
		// Email Ops resolution needs template provenance, a folder-store field
		// the SQLite-primary store drops — so it reads through the FileStore.
		// Lazy: the FileStore is constructed in a later phase (Phase 18).
		b.personalHQHandler.SetEmailOpsSource(func() workspace.EmailOpsWorkspaceSource {
			if b.workspaceFileStore == nil {
				return nil
			}
			return b.workspaceFileStore
		})
		// Structured follow-ups (Group 6): a dedicated SQLite domain over the
		// shared database.
		b.followUpService = followup.NewService(followup.NewSQLiteStore(sessionStore.DB()))
		// Calendar Ops meeting-prep event-to-note links (Group 6): a dedicated
		// SQLite domain over the shared database, same pattern as follow-ups.
		b.meetingPrepStore = meetingprep.NewSQLiteStore(sessionStore.DB())
		b.personalHQHandler.SetFollowUps(b.followUpService)
		// End-of-day journal (Group 7): grounded on the day's closed follow-ups,
		// saved as a dated Personal HQ note (never MEMORY.md by default).
		b.personalHQHandler.SetJournal(personalhq.NewJournalService(
			b.personalHQService,
			&journalSnapshotBuilder{followups: b.followUpService},
			sessionStore,
		))
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
		// Wire remote MCP OAuth credential persistence now that the vault
		// store exists; the MCP registry/handler were constructed earlier
		// (initializeMCP) without it, matching the lazy-store pattern used
		// for workspaceStore elsewhere in this file.
		mcp.ConfigureRemoteOAuth(newVaultMCPCredentialStore(vaultStore), mcpOAuthUserID)
		if b.mcpHandler != nil {
			b.mcpHandler.SetVaultOAuthStore(vaultStore)
		}
		// The legacy in-app Personal HQ email OAuth settings are gone: Google
		// Account is now the only supported Gmail connection path, configured with
		// ORI_GOOGLE_CONNECTION_CLIENT_ID/_SECRET (FR 61, 62). Nothing overrides
		// the vault's per-provider OAuth client any more.
		b.settingsHandler.SetVaultRootUpdater(vaultStore.SetManagedVaultRoot)
		if b.workspaceHandler != nil {
			b.workspaceHandler.SetEmailAccountStore(vaultStore)
		}
		// The mailbox read/link/send runtime depends on b.workspaceStore, which is
		// still nil at this phase (Phase 17). Stash the vault store and defer that
		// wiring to wireMailboxRuntime, called after the workspace store exists
		// (Phase 18) — same reason wireReaperSetup is deferred.
		b.vaultStore = vaultStore
		logger.Info("Vault system initialized", logger.Fields{})
	}

	// Google Account connection (identity connect flow). Identity-only in this
	// group — product grants arrive later. The verifier is lazy so startup does
	// no network call; client credentials come from env in dev and are baked in
	// for official builds.
	{
		// Precedence: operator env vars → official-build embedded client → none.
		clientID, clientSecret, clientSource, clientVerdict := connections.ResolveOAuthClientChecked()
		// A self-hosted operator who pasted the wrong value (classically their own
		// Google address) learns at startup rather than mid-browser-flow (FR 63-65).
		// The verdict carries no secret, and the secret is never logged.
		if clientSource.Configured() && !clientVerdict.OK() {
			logger.Warn("Google connection OAuth client is not usable", logger.Fields{
				"problem":  string(clientVerdict.Problem),
				"guidance": clientVerdict.Message(),
			})
		}
		connStore := connections.NewStore(config.DefaultDataDir())
		b.connStore = connStore
		connFlow := connections.NewIdentityFlow(
			connections.OAuthConfig{ClientID: clientID, ClientSecret: clientSecret, Verdict: clientVerdict},
			connections.NewStateStore(10*time.Minute),
			connStore,
			connections.NewLazyGoogleVerifier(clientID),
		)
		// Enabling Gmail stores its OAuth credential as a vault EmailAccount, so
		// the native mailbox reuses it (FR 39); the same adapter also links the
		// grant to workspaces without re-auth (FR 47, 54). Requires the vault
		// store (Phase 17).
		connDeps := connectionshttp.Deps{
			Flow:           connFlow,
			Store:          connStore,
			Guard:          connectionshttp.NewOriginGuard(),
			Impacts:        connectionImpactEnumerator{b: b},
			Teardown:       connectionProductTeardown{b: b},
			Health:         connectionGrantHealth{b: b},
			HealthNotifier: connectionHealthNotifier{b: b},
			Consent:        connections.NewConsentLog(config.DefaultDataDir()),
		}
		if b.vaultStore != nil {
			sink := newGmailCredentialSink(b.vaultStore)
			b.gmailSink = sink
			connFlow.WithCredentialSink(sink)
			// Resolve + verify the destination vault BEFORE opening Google, and
			// re-verify it at callback time, so a locked or missing vault becomes an
			// explicit unlock/choose prompt instead of a failure after authorization
			// (FR 1, 3-9, 12-14).
			catalog := newConnectionVaultCatalog(b.vaultStore)
			connFlow.WithVaultCatalog(catalog)
			connDeps.Vaults = catalog
			connDeps.Linker = sink
			connDeps.Migrator = sink
		}
		b.connectionsHandler = connectionshttp.NewHandler(connDeps)
		// When a Google MCP server (Calendar/Drive) authorizes, verify the ID
		// token and attach the grant to this connection (FR 23, 40).
		mcp.SetGoogleMCPIdentityHook(b.googleMCPIdentityHook)
		mcp.SetGoogleMCPLoginHint(b.googleConnectionEmail)
		// Cap Google Drive to its fail-closed read-only tool allowlist, enforced
		// server-side at both listing and execution (FR 66, 67). Independent of
		// whether a Google account is connected — a manually added Drive MCP
		// server is capped too.
		mcp.SetToolExposureHook(b.mcpToolExposureAllowed)
		// Fence + bound untrusted Google Drive result content before it reaches
		// the LLM (FR 71, 73).
		mcp.SetToolResultTextHook(b.sanitizeDriveResultText)
		logger.Info("Google connection handler initialized", logger.Fields{"configured": clientSource.Configured(), "client_source": string(clientSource)})
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
	// Enforce stage-based active-skill slot caps (PRD section C). Reads the
	// agent's stage + expert flag through the store on each check, no caching.
	b.skillsManager.SetLoadoutResolver(newLoadoutResolverAdapter(b.st))
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

// wireCalendarOpsSetup constructs the Calendar Ops guided-setup handler. Like
// wireReaperSetup, this must run AFTER the workspace store exists (Phase 18),
// not during initializeHandlers (Phase 17) where b.workspaceStore is still
// nil -- the handler needs it as its FolderStore.
//
// b.workspaceStore is statically typed as workspace.Store, which does not
// declare GetFolderWorkspace -- calendarhttp.FolderStore requires it
// specifically so template-provenance reads bypass the SQLite-primary Get
// (which always returns TemplateProvenance nil; see sync_store.go's Save
// rehydration comment for the write-side half of this). A runtime type
// assertion is required here since Go checks interface-to-interface
// assignment statically; both concrete stores actually wired in
// initializeWorkspaceStore (*SyncStore, *FileStore) implement it.
func (b *ServerBuilder) wireCalendarOpsSetup() {
	if b.workspaceStore == nil || b.sessionStore == nil {
		return
	}
	folders, ok := b.workspaceStore.(calendarhttp.FolderStore)
	if !ok {
		logger.Warn("Calendar Ops setup handler not wired: workspace store lacks GetFolderWorkspace", logger.Fields{})
		return
	}
	b.calendarOpsHandler = calendarhttp.NewHandler(folders, b.sessionStore, b.mcpRegistry, b.mcpConfigManager, b.userProvider)
	b.calendarOpsHandler.SetNotes(b.sessionStore)
	if b.meetingPrepStore != nil {
		b.calendarOpsHandler.SetMeetingPreps(b.meetingPrepStore)
	}
}

// wireDownloadsJanitor constructs the Downloads Janitor service and handler.
// Like wireReaperSetup/wireCalendarOpsSetup it runs in the workspace-store
// phase (18) rather than initializeHandlers (17): the service needs the
// composed workspace store to record the approved folder's directory reference
// and read-only MCP binding, and the folder store to resolve where each
// workspace's Janitor state lives on disk.
func (b *ServerBuilder) wireDownloadsJanitor() {
	if b.workspaceStore == nil || b.workspaceFileStore == nil {
		return
	}
	service := downloadsjanitor.NewService(downloadsjanitor.NewStore(b.workspaceFileStore), b.workspaceStore)
	b.downloadsJanitorService = service
	b.downloadsJanitorHandler = downloadsjanitorhttp.NewHandler(service, b.workspaceStore, b.userProvider)
}

// wireDownloadsJanitorMover gives the Janitor its execution mechanism: the
// workspace's own root-scoped filesystem MCP binding.
//
// Split from wireDownloadsJanitor because the runtime resolver is built later
// in the same phase. Until this runs the service has no mover, and an apply
// fails loudly rather than pretending — which is the right failure: a Janitor
// that cannot move files must say so, not silently report success.
func (b *ServerBuilder) wireDownloadsJanitorMover() {
	if b.downloadsJanitorService == nil || b.runtimeResolver == nil || b.mcpRegistry == nil {
		return
	}
	// The Trash mechanism is Ori's own recoverable-Trash abstraction, never
	// filesystem MCP: delete_file unlinks, and an unlinked file has no restore
	// token and no way back.
	b.downloadsJanitorService.SetTrash(downloadsjanitor.NewPlatformTrash())
	mover := downloadsjanitor.NewMCPMover(
		b.workspaceStore,
		b.runtimeResolver,
		janitorToolCaller{registry: b.mcpRegistry},
	)
	// The connector is a process; it may not be running when the first approved
	// move arrives. Lazy-start it the same way the chat path does.
	mover.SetStarter(b.mcpRegistry)
	b.downloadsJanitorService.SetMover(mover)
}

// janitorToolCaller adapts the MCP registry to the narrow caller the Janitor
// needs: whether the tool reported an error, not what it returned. What
// actually happened to the file is decided against the filesystem.
type janitorToolCaller struct{ registry *mcp.Registry }

func (c janitorToolCaller) CallTool(ctx context.Context, serverName, toolName string, arguments map[string]any) (bool, error) {
	result, err := c.registry.CallTool(ctx, serverName, toolName, arguments)
	if err != nil {
		return false, err
	}
	if result == nil {
		return true, nil
	}
	return result.IsError, nil
}

// wireCalendarOpsPrepTaskExecutor gives the already-constructed Calendar Ops
// handler its task executor. Split out from wireCalendarOpsSetup because
// b.workspaceOrchestrator does not exist until initializeWorkspaceOrchestrator
// (Phase 22), which runs after the workspace-store phase (18) where
// wireCalendarOpsSetup itself is called -- same later-phase-dependency
// pattern as wireReaperSetup/wireCalendarOpsSetup's own doc comment describes.
func (b *ServerBuilder) wireCalendarOpsPrepTaskExecutor() {
	if b.calendarOpsHandler == nil || b.workspaceOrchestrator == nil {
		return
	}
	b.calendarOpsHandler.SetTaskExecutor(b.workspaceOrchestrator)
}

// wireMailboxRuntime wires the Personal HQ / workspace mailbox read-link-send
// runtime: a Gmail provider over the Vault-backed credential resolver, the
// access gate, the HQ + workspace email linkers, the Daily Brief email source,
// and the confirm-gated send broker.
//
// It must be called AFTER the workspace store exists (Phase 18), not during
// initializeHandlers (Phase 17) where b.workspaceStore is still nil — otherwise
// the whole block was silently skipped and every email endpoint reported
// "unavailable". Downstream consumers capture valid state: the workspace tool
// factory is built at Phase 20 and the Daily Brief at Phase 22.6, both after
// this runs.
func (b *ServerBuilder) wireMailboxRuntime() {
	if b.workspaceStore == nil || b.vaultStore == nil {
		return
	}
	gmailProvider := mailbox.NewGmailProvider(mailboxvault.NewResolver(b.vaultStore))
	cachedProvider := mailbox.NewCachingProvider(gmailProvider)
	b.mailboxAccess = newMailboxAccess(b.workspaceStore, b.vaultStore, cachedProvider)
	// Credential teardown and consolidation invalidate cached reads through this.
	b.mailboxInvalidator = cachedProvider
	// The Gmail credential sink was built in Phase 17 without a workspace store;
	// proven consolidation needs one, so it is attached here.
	if b.gmailSink != nil {
		b.gmailSink.lifecycle = newCredentialLifecycle(b.vaultStore, b.workspaceStore, cachedProvider)
	}
	if b.chatHandler != nil {
		b.chatHandler.SetMailboxAccess(b.mailboxAccess)
	}
	// Email connect/disconnect: manage an email MCP binding on the target
	// workspace, with disconnect cache invalidation. The same service backs both
	// the HQ-scoped endpoints (designated HQ) and the workspace-scoped endpoints
	// (Email Ops and other owned workspaces).
	// Deterministic email readiness: setup state and task preconditions come from
	// the actual connection/vault/binding state, never from an agent's report of
	// what it thinks it set up (FR 32, 33).
	var vaultCatalog connections.VaultCatalog
	if b.vaultStore != nil {
		vaultCatalog = newConnectionVaultCatalog(b.vaultStore)
	}
	readiness := newEmailReadinessEvaluator(b.connStore, vaultCatalog, b.workspaceStore, b.vaultStore)
	// The orchestration task handler that consumes this is built later
	// (Phase 21), so stash it rather than wiring a handler that is still nil.
	b.emailReadiness = readiness
	if b.personalHQHandler != nil && b.personalHQService != nil {
		linker := newMailboxLinkerService(b.personalHQService, b.workspaceStore, b.vaultStore, cachedProvider)
		linker.readiness = readiness
		b.personalHQHandler.SetMailboxLinker(linker)
		b.personalHQHandler.SetWorkspaceMailboxLinker(linker)
	}
	// Grounded email attention for the Daily Brief: reads the connected account
	// (Email Ops workspace first, legacy HQ binding as fallback) through the
	// same cached provider. No second brief service/scheduler. The Email Ops
	// resolver reads through the FileStore so it sees template provenance.
	if b.personalHQService != nil {
		emailOpsSource := func() workspace.EmailOpsWorkspaceSource {
			if b.workspaceFileStore == nil {
				return nil
			}
			return b.workspaceFileStore
		}
		b.dailyBriefMailbox = newDailyBriefMailboxSource(b.personalHQService, b.workspaceStore, b.vaultStore, cachedProvider, emailOpsSource)
	}
	// Confirm-gated send broker: the ONLY send path. The raw (uncached) Gmail
	// provider is the sender; sends re-authorize via the send policy and emit
	// metadata-only audit events.
	if b.personalHQService != nil {
		broker := mailbox.NewBroker(
			gmailProvider,
			&sendAuthorizer{hq: b.personalHQService, workspaces: b.workspaceStore},
			logAuditSink{},
		)
		replies := newReplyService(b.personalHQService, b.workspaceStore, b.vaultStore, cachedProvider, broker)
		if b.personalHQHandler != nil {
			b.personalHQHandler.SetReplyService(replies)
		}
		// The reply service is also the agent-facing drafter (mail_draft_reply).
		b.mailDrafter = replies
		if b.chatHandler != nil {
			b.chatHandler.SetMailDrafter(replies)
		}
	}
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
