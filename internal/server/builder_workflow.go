// Package server provides workflow initialization methods for the ServerBuilder.
// This file contains methods for workspace, events, task execution, and orchestration.
package server

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/johnjallday/ori-agent/internal/actioncenterhttp"
	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/gateway"
	"github.com/johnjallday/ori-agent/internal/githubhttp"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/orchestration/templates"
	"github.com/johnjallday/ori-agent/internal/orchestrationhttp"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/trigger"
	"github.com/johnjallday/ori-agent/internal/triggerhttp"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workflowhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacepolicy"
	"github.com/johnjallday/ori-agent/internal/workspacerun"
)

// buildWorkspaceToolFactory returns a factory that exposes workspace-scoped
// context tools (notes, tasks, sessions, files, directories) to the task
// executor. Without these tools, task agents only see the truncated workspace
// snapshot embedded in the prompt and cannot fetch full note content on demand.
//
// Returns nil when the session store is unavailable; callers should treat a
// nil factory as "no workspace tools" and fall back to snapshot-only context.
func (b *ServerBuilder) buildWorkspaceToolFactory() workspace.WorkspaceToolFactory {
	if b.sessionStore == nil {
		return nil
	}
	sessionStore := b.sessionStore
	workspaceStore := b.workspaceStore
	fileStore := b.workspaceFileStore
	configManager := b.configManager
	eventBus := b.eventBus
	userStore := b.userStore
	userProvider := b.userProvider
	return func(workspaceID, agentName string) []toolapi.Tool {
		provider := chathttp.NewWorkspaceToolProvider(sessionStore, workspaceStore, workspaceID, b.hqVisibilityDeps())
		provider.SetExecutingAgent(agentName)
		if fileStore != nil {
			provider.SetFileStore(fileStore)
		}
		if userStore != nil {
			provider.SetUserProfileDeps(userStore, userProvider)
		}
		provider.SetProjectTemplateDeps(func() string {
			return resolveTemplatesRoot(configManager)
		}, eventBus)
		if b.mailboxAccess != nil {
			provider.SetMailboxAccess(b.mailboxAccess)
		}
		if b.mailDrafter != nil {
			provider.SetMailDrafter(b.mailDrafter)
		}
		// Only workspaces with a bound repository get the proposal tool.
		// Exposing it without a binding would let an agent propose changes
		// the broker would then refuse, which reads to a model as a broken
		// tool rather than as a workspace that is not set up yet.
		if b.githubHandler != nil && b.githubHandler.Broker() != nil {
			resolver := githubhttp.NewRepoResolver(b.githubWorkspaceStore)
			if _, bound := resolver.BoundRepo(workspaceID); bound {
				provider.SetGitHubProposer(newGitHubProposer(b.githubHandler.Broker(), resolver))
			}
		}
		return provider.Tools()
	}
}

// hqVisibilityDeps creates closures rather than retaining a snapshot of the
// builder's stores. Workspace storage and the folder projection are wired in
// different builder phases, so resolving dependencies only at tool-call time
// avoids a partially initialized HQ overview.
func (b *ServerBuilder) hqVisibilityDeps() chathttp.HQVisibilityDeps {
	return chathttp.HQVisibilityDeps{
		SnapshotSources: b.watchtowerSnapshotSources,
		IsDesignatedHQ: func(ctx context.Context, workspaceID string) (bool, error) {
			if b.personalHQService == nil {
				return false, nil
			}
			return b.personalHQService.IsWorkspaceDesignatedPersonalHQ(ctx, userprofile.LocalUserID, workspaceID)
		},
		FolderPath: func(workspaceID string) string {
			if b.workspaceFileStore == nil {
				return ""
			}
			path, err := b.workspaceFileStore.GetFolderPath(workspaceID)
			if err != nil {
				return ""
			}
			return path
		},
		UserID: userprofile.LocalUserID,
	}
}

// watchtowerSnapshotSources builds the live cross-workspace read projection
// shared by the HQ tool and Watchtower endpoint. It is a method rather than a
// captured value because the Personal HQ handler is constructed before the
// workspace and folder stores exist.
func (b *ServerBuilder) watchtowerSnapshotSources() dailybrief.SnapshotSources {
	workspaceStore := b.workspaceStore
	sources := dailybrief.SnapshotSources{Workspaces: workspaceStore}
	if workspaceStore != nil {
		sources.Opportunities = workspace.NewOpportunityStore(workspaceStore)
	}
	if b.sessionStore != nil {
		sources.Sessions = &sessionSourceAdapter{store: b.sessionStore}
	}
	return sources
}

// delegationCapsFromEnv returns the delegation loop caps, allowing operators to
// tune the safe defaults (3 iterations / 8 subtasks / 10m) without code changes:
//
//	ORI_DELEGATION_MAX_ITERATIONS, ORI_DELEGATION_MAX_SUBTASKS (positive ints)
//	ORI_DELEGATION_TIMEOUT (Go duration, e.g. "15m")
func delegationCapsFromEnv() workspace.DelegationCaps {
	caps := workspace.DefaultDelegationCaps()
	if v := os.Getenv("ORI_DELEGATION_MAX_ITERATIONS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			caps.MaxIterations = n
		}
	}
	if v := os.Getenv("ORI_DELEGATION_MAX_SUBTASKS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			caps.MaxSubtasks = n
		}
	}
	if v := os.Getenv("ORI_DELEGATION_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			caps.Timeout = d
		}
	}
	return caps
}

// initializeWorkspaceStore creates the workspace storage system.
// Uses the session HybridStore as the underlying storage via an adapter,
// which unifies workspace data between the Sessions sidebar and Workspaces page.
// A SyncStore wrapper ensures every Save also writes workspace.json to disk.
func (b *ServerBuilder) initializeWorkspaceStore() error {
	var ws workspace.Store
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	// Use the session store adapter if available (preferred for unified workspace data)
	if b.sessionStore != nil {
		adapter := session.NewWorkspaceStoreAdapter(b.sessionStore)
		ws = adapter
		if verbose {
			logger.Info("Workspace store initialized using session store adapter (SQLite)", logger.Fields{})
		}
	} else {
		// Fall back to file-based store if session store is not available
		workspaceDir := resolveWorkspaceDir()
		fileStore, err := createWorkspaceStore(workspaceDir)
		if err != nil {
			return err
		}
		ws = fileStore
		if verbose {
			logger.Info("Workspace store initialized using file store (fallback)", logger.Fields{"dir": workspaceDir})
		}
	}

	// Always create the folder-based FileStore alongside the primary store.
	// The FileStore manages workspace folders on disk (workspace.json, files/, notes/, etc.)
	//
	// Priority for workspace root:
	// 1. Settings workspace_root (user-configured)
	// 2. WORKSPACE_DIR env var
	// 3. Default: ~/Ori Workspaces
	workspaceDir := resolveWorkspaceRoot(b.configManager)
	startupMaintenanceApproved := shouldRunWorkspaceStartupMaintenance(b.configManager)
	fileStore, err := workspace.NewFileStore(workspaceDir)
	if err != nil {
		logger.Warn("Failed to create folder-based workspace store", logger.Fields{"error": err})
	} else {
		// When SQLite is the primary store, wrap with SyncStore so every
		// Save() also writes workspace.json to disk. This keeps MCP configs,
		// skills, schedules, tasks, and all other workspace data portable.
		if b.sessionStore != nil {
			ws = workspace.NewSyncStore(ws, fileStore)
			if verbose {
				logger.Info("Workspace SyncStore enabled (SQLite → disk write-through)", logger.Fields{"dir": workspaceDir})
			}
		}

		b.workspaceFileStore = fileStore
		if startupMaintenanceApproved {
			// The template-intake engine is gone; remove any of its per-workspace
			// session sidecars left on disk. Best-effort and non-fatal.
			cleanupLegacyTemplateOnboardingSidecars(fileStore)
		}
		if b.sessionHandler != nil {
			b.sessionHandler.SetWorkspaceStore(fileStore)
			// A saved Workspace Directory must take effect in this process, not
			// only on the next start. Wire it here (Phase 18) rather than with
			// the other settings callbacks (Phase 17), where the folder store
			// does not exist yet and the callback would capture nil.
			//
			// Wired regardless of startup-maintenance consent: on first run the
			// live store is pointed at the unconfirmed staging root, and this is
			// exactly the callback that re-points it the moment onboarding or
			// Build My HQ confirms a real directory.
			b.wireWorkspaceRootUpdater()
			if startupMaintenanceApproved {
				// Disk is the source of truth for grouping: reconcile the session
				// store's structure with the on-disk layout once at startup so
				// groups that arrived via git/cloud sync show up without a manual
				// rescan. Non-fatal.
				if err := b.sessionHandler.ReconcileWorkspacesFromDisk(context.Background()); err != nil {
					logger.Warn("Startup workspace reconcile from disk failed", logger.Fields{"error": err.Error()})
				}
				// Upgrade groups created before they supported direct work with
				// the scoped scaffolding (files/, notes/, directory reference,
				// workspace-files MCP binding). Idempotent; non-fatal.
				if err := b.sessionHandler.BackfillGroupScaffolding(context.Background()); err != nil {
					logger.Warn("Startup group scaffolding backfill failed", logger.Fields{"error": err.Error()})
				}
				// Backfill BACKLOG.md for every managed workspace created before
				// this feature shipped (PRD workspace-backlog FR68, 99).
				// Idempotent; non-fatal — a pre-existing unmanaged collision is
				// left untouched by design, not an error.
				if written, errs := workspace.BackfillBacklogMarkdownForAllWorkspaces(fileStore); len(errs) > 0 {
					logger.Warn("Startup BACKLOG.md backfill had errors", logger.Fields{"written": written, "error_count": len(errs), "first_error": errs[0].Error()})
				} else {
					logger.Debug("Startup BACKLOG.md backfill complete", logger.Fields{"written": written})
				}
			} else {
				logger.Info("Skipping workspace startup maintenance until root confirmation", logger.Fields{"dir": workspaceDir})
			}
			// Personal HQ designation projection: the folder store only exists
			// now (Phase 18), after personalHQService was constructed (Phase
			// 17), so wire the syncer here and reconcile the workspace-side
			// Designation field against the authoritative designation records.
			// Idempotent; non-fatal. (Builder wiring-order gotcha — the syncer
			// reads b.sessionHandler's folder store lazily at call time, set
			// just above via SetWorkspaceStore.)
			if b.personalHQService != nil {
				b.personalHQService.SetDesignationSyncer(b.sessionHandler)
				b.personalHQService.SetDesignationReader(fileStore)
				if startupMaintenanceApproved {
					if designated, err := b.personalHQService.DesignatedWorkspaceIDs(context.Background()); err != nil {
						logger.Warn("Startup designation backfill: failed to resolve designated workspaces", logger.Fields{"error": err.Error()})
					} else if err := b.sessionHandler.BackfillWorkspaceDesignations(context.Background(), designated); err != nil {
						logger.Warn("Startup workspace designation backfill failed", logger.Fields{"error": err.Error()})
					}
				}
			}
		}
		if b.chatHandler != nil {
			b.chatHandler.SetFileStore(fileStore)
			b.chatHandler.SetHQVisibilityDeps(b.hqVisibilityDeps())
		}
		if b.resetHandler != nil {
			b.resetHandler.SetWorkspaceStore(fileStore)
		}
		if verbose {
			logger.Info("Folder-based workspace store initialized", logger.Fields{"dir": workspaceDir})
		}
	}

	// Wrap with AgentSnapshotStore so that every workspace Save() also writes
	// workspace-local snapshots of any referenced agent that exists in the
	// global agent registry. The snapshots make a workspace folder
	// self-contained for export/import.
	if b.st != nil {
		// Load the per-data-dir allowlist. A missing file yields an empty
		// allowlist, which means nothing in the shared workspaces tree will
		// auto-hydrate into this data directory. Workspaces are added to the
		// allowlist when explicitly imported via the workspace import API.
		allowlistPath := resolveAllowlistPath()
		allowlist, err := workspace.LoadAllowlist(allowlistPath)
		if err != nil {
			logger.Warn("Failed to load workspace allowlist", logger.Fields{"error": err.Error()})
			allowlist = workspace.NewAllowlist(allowlistPath)
		}
		b.workspaceAllowlist = allowlist
		if b.sessionHandler != nil {
			b.sessionHandler.SetWorkspaceAllowlist(allowlist)
		}

		// Treat the local workspace tree (~/Ori Workspaces) as owned by this data
		// directory: backfill every workspace physically present there into the
		// allowlist so agents from workspaces created locally are restored (and
		// not wiped) on startup. Foreign workspaces that are not in the local
		// folder tree stay gated, preserving cross-worktree isolation. Runs before
		// the wipe/restore below.
		if fileStore != nil && startupMaintenanceApproved {
			workspace.BackfillLocalWorkspacesIntoAllowlist(fileStore, allowlist)
		}

		// First wipe agents whose only source is a non-allowlisted workspace
		// snapshot — keeps cross-worktree contamination from lingering after
		// the user revokes (or never granted) an import.
		if fileStore != nil && startupMaintenanceApproved {
			workspace.WipeNonAllowlistedAgentSnapshots(fileStore, b.st, allowlist)
		}
		workspace.WipeNonAllowlistedAgentSnapshots(ws, b.st, allowlist)

		// Restore only allowlisted workspaces' agent snapshots.
		if fileStore != nil && startupMaintenanceApproved {
			workspace.RestoreAllowlistedWorkspaceAgents(fileStore, b.st, allowlist)
		}
		ws = workspace.NewAgentSnapshotStore(ws, b.st)
		workspace.RestoreAllowlistedWorkspaceAgents(ws, b.st, allowlist)
		workspace.SnapshotAllWorkspaces(ws, b.st)
		if fileStore != nil && startupMaintenanceApproved {
			workspace.SnapshotAllWorkspaces(fileStore, b.st)
		}
	}

	b.workspaceStore = ws

	// Evolve persisted Tasks into canonical Tickets, once, before anything
	// reads them (tasks/prd-workspace-ticket-management.md FR-105, FR-106).
	//
	// Each workspace migrates in its own transaction, so one failure leaves
	// the others migrated and the failing one untouched. It is idempotent and
	// version-gated: on every subsequent boot this is a no-op that cannot
	// renumber anything.
	runTicketMigration(ws)

	// Set workspace store on chat handler (uses SyncStore when available)
	b.chatHandler.SetWorkspaceStore(ws)

	// Plan materialization writes tasks through this store, so it can only be
	// wired now that the store exists.
	b.attachWorkspacePlanMaterializer()

	// Ori Guide reads workspace names to resolve a destination the user asked
	// for by name. Read-only; the guide has no write path.
	b.oriGuideHandler.SetWorkspaceStore(ws)

	// Give the session handler the primary store for task mutations (the
	// entry-agent claim sweep) so claimed tasks are written through the same
	// store orchestration reads from, not just the raw folder store.
	if b.sessionHandler != nil {
		b.sessionHandler.SetWorkspaceTaskStore(ws)
	}

	// Effective planning policy needs the store to read settings and to find
	// the folder whose version control the enforced controls are about. Like
	// the plan materializer and executor, it is wired here rather than during
	// handler construction, when the store is still nil.
	b.workspacePlanPolicy = workspacepolicy.NewResolver(ws)
	if b.sessionHandler != nil {
		b.sessionHandler.SetPlanningPolicyResolver(b.workspacePlanPolicy)
	}

	// Now that the workspace store (SyncStore) exists, wire REAPER readiness /
	// preview / repair. Done here rather than in initializeHandlers because the
	// store is created in this phase (Phase 18), after the handlers.
	b.wireReaperSetup()
	b.wireCalendarOpsSetup()
	b.wireDownloadsJanitor()
	// After the domain services above: the capability registry binds their
	// runtimes, and the wizard registers their adapters.
	b.wireWorkspaceCapabilities()
	b.wireRuntimeCapabilities()
	b.wireSetupWizard()
	// The coordinate map resolves node ownership through the composed workspace
	// store, so it wires here for the same reason (#292 FR-99).
	b.wireWorkspaceMap()

	// Same reason: the mailbox read/link/send runtime depends on the workspace
	// store, so it is wired here rather than in initializeHandlers (Phase 17).
	b.wireMailboxRuntime()

	return nil
}

// initializeEventSystem creates the event bus and notification service.
func (b *ServerBuilder) initializeEventSystem() {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	b.eventBus = workspace.DefaultEventBus()
	if verbose {
		logger.Info("Event bus initialized", logger.Fields{})
	}

	b.notificationService = workspace.NewNotificationService(b.eventBus, 500)
	if verbose {
		logger.Info("Notification service initialized", logger.Fields{})
	}

	// The session and chat handlers are built before the event system
	// (Phase 17 vs 19), so their project-template wiring lands here.
	if b.sessionHandler != nil {
		b.sessionHandler.SetEventBus(b.eventBus)
	}
	if b.chatHandler != nil {
		b.chatHandler.SetProjectTemplateDeps(func() string {
			return resolveTemplatesRoot(b.configManager)
		}, b.eventBus)
	}

	// Onboarding progression: engine, event subscription, backfill.
	b.initializeProgression()

	if b.workspaceStore != nil {
		syncMgr, err := workspace.NewDirectorySyncManager(b.workspaceStore, b.eventBus, workspace.DefaultDirectorySyncConfig())
		if err != nil {
			logger.Warn("Failed to initialize directory sync manager", logger.Fields{"error": err})
		} else {
			b.directorySyncManager = syncMgr
			if verbose {
				logger.Info("Directory sync manager initialized", logger.Fields{})
			}
		}
	}
}

// initializeTaskExecution creates task handler, executor, step executor, and scheduler.
func (b *ServerBuilder) initializeTaskExecution() {
	b.taskHandler = workspace.NewLLMTaskHandler(b.st, b.llmFactory, b.workspaceStore)
	b.taskHandler.SetEventBus(b.eventBus)
	b.taskHandler.SetMCPRegistry(b.mcpRegistry)
	b.taskHandler.SetUtilityToolProvider(b.utilityToolRegistry)
	if b.configManager != nil {
		if secs := b.configManager.GetNativeMCPExecTimeoutSeconds(); secs > 0 {
			b.taskHandler.SetNativeMCPExecTimeout(time.Duration(secs) * time.Second)
		}
	}
	runtimeResolver := workspace.NewAgentRuntimeResolver(b.st, b.workspaceStore, b.mcpRegistry, b.mcpConfigManager)
	if b.skillsManager != nil {
		runtimeResolver.SetSkillResolver(newSkillResolverAdapter(b.skillsManager))
	}
	b.runtimeResolver = runtimeResolver
	b.wireRuntimeGrantFoundation()

	// Make every existing agent's implicit capability set explicit before the
	// resolver serves its first request (PRD FR-28–FR-35). This runs here
	// because it is the first point where the agent store, the skills manager,
	// and the workspace store all exist; both halves are idempotent and
	// non-fatal, so a failure leaves pre-migration behavior intact.
	var migrationSkillSource workspace.ToolboxMigrationSkillSource
	if b.skillsManager != nil {
		migrationSkillSource = newSkillResolverAdapter(b.skillsManager)
	}
	migrateAgentDefaultToolboxes(b.st, b.skillsManager)
	migrateWorkspaceToolboxes(b.workspaceStore, migrationSkillSource, newLoadoutResolverAdapter(b.st))

	// The Janitor's mover needs the runtime resolver, which only exists here.
	b.wireDownloadsJanitorMover()
	b.taskHandler.SetRuntimeResolver(runtimeResolver)
	b.taskHandler.SetExecutionScopeResolver(b.runtimeCapabilityService)
	b.chatHandler.SetRuntimeResolver(runtimeResolver)
	if b.calendarOpsHandler != nil {
		b.chatHandler.SetCalendarOpsPreference(b.calendarOpsHandler)
	}
	if b.sessionStore != nil {
		b.taskHandler.SetContextStore(session.NewWorkspaceTaskContextAdapter(b.sessionStore))
	}
	if b.userStore != nil {
		b.taskHandler.SetUserProfileStore(b.userStore)
	}
	if fn := b.buildWorkspaceToolFactory(); fn != nil {
		b.taskHandler.SetWorkspaceToolFactory(fn)
	}

	taskExecutionHandler := workspace.TaskHandler(b.taskHandler)
	if b.workspaceRunExecutors != nil && b.workspaceRunStore != nil && b.workspaceRunService != nil {
		oriExecutor := workspacerun.NewOriAgentExecutor(b.taskHandler)
		if b.workspaceFileStore != nil {
			// Snapshot workspace memory before/after each run to record what it learned.
			oriExecutor.SetWorkspaceFolderResolver(b.workspaceFileStore)
		}
		b.workspaceRunExecutors.Register(workspacerun.ExecutorKindOriAgent, oriExecutor)
		bridge := workspacerun.NewTaskRunBridge(b.workspaceRunStore, b.workspaceRunService, b.workspaceStore)
		b.runBackedTaskHandler = bridge
		// Kept as the concrete type as well, because plan execution dispatches
		// through it directly rather than through the TaskHandler interface.
		b.workspaceRunBridge = bridge
		taskExecutionHandler = bridge
	}

	// Plan execution dispatches through the bridge above and mutates tasks
	// through the workspace store, so it can only be wired once both exist.
	b.attachWorkspacePlanExecutor()

	b.taskExecutor = workspace.NewTaskExecutor(b.workspaceStore, taskExecutionHandler, workspace.ExecutorConfig{
		PollInterval:  10 * time.Second,
		MaxConcurrent: 5,
	})
	b.taskExecutor.SetEventBus(b.eventBus)
	// The execution handler may be a run bridge that does not itself resolve
	// provider profiles, so wire the LLM task handler directly for scheduling
	// decisions (per-provider concurrency, local timeouts) — WS6.
	b.taskExecutor.SetProviderResolver(b.taskHandler)
	// Only wire when non-nil: b.evolutionService is a concrete *evolution.Service
	// and passing a nil pointer through the TaskXPAwarder interface would leave
	// a non-nil interface with a nil value, defeating the executor's nil check.
	if b.evolutionService != nil {
		b.taskExecutor.SetEvolutionAwarder(b.evolutionService)
	}

	b.stepExecutor = workspace.NewStepExecutor(b.workspaceStore, taskExecutionHandler, workspace.StepExecutorConfig{
		PollInterval: 5 * time.Second,
	})

	b.taskScheduler = workspace.NewTaskScheduler(b.workspaceStore, workspace.SchedulerConfig{
		PollInterval:  1 * time.Minute,
		WakeScheduler: b.macWakeService,
	})
	b.taskScheduler.SetEventBus(b.eventBus)
}

// initializeOrchestration creates orchestrators and handlers.
func (b *ServerBuilder) initializeOrchestration() error {
	communicator := agentcomm.NewCommunicator(b.workspaceStore)

	var history gateway.ConversationStore
	if b.sessionStore != nil {
		history = session.NewGatewaySessionStore(b.sessionStore)
	}

	orch := orchestration.NewOrchestrator(b.st, b.workspaceStore, history, communicator, b.llmFactory, b.configManager, b.eventBus)
	b.multiAgentOrchestrator = orch

	// Proposed multi-agent work becomes a durable Plan draft rather than an
	// ephemeral stash on the workspace, so the thing the user approves is a
	// versioned record (FR-59, FR-149).
	if b.workspacePlanService != nil {
		orch.SetPlanDrafter(orchestratorPlanDrafter{
			service: b.workspacePlanService,
			agents:  b.resolvePlanAvailability,
		})
	}

	// Wire gateway to orchestrator if initialized
	if b.gateway != nil {
		orch.SetGateway(b.gateway)
		b.gateway.SetRouter(orch.HandleGatewayMessage)
	}

	b.orchestrationTaskHandler = b.taskHandler
	taskHandler := workspace.TaskHandler(b.taskHandler)
	if b.runBackedTaskHandler != nil {
		taskHandler = b.runBackedTaskHandler
	}

	// Create session store adapter for orchestration handler
	var sessionStoreAdapter orchestrationhttp.SessionStore
	if b.sessionStore != nil {
		sessionStoreAdapter = session.NewOrchestrationSessionStore(b.sessionStore)
	}

	// Create orchestration handler with all available dependencies
	// Note: TemplateManager is added later via SetTemplateManager in initializeTemplateManager
	handler, err := orchestrationhttp.NewHandler(orchestrationhttp.HandlerConfig{
		AgentStore:          b.st,
		WorkspaceStore:      b.workspaceStore,
		EventBus:            b.eventBus,
		Orchestrator:        orch,
		NotificationService: b.notificationService,
		TaskHandler:         taskHandler,
		SessionStore:        sessionStoreAdapter,
		FileWatcher:         b.sessionFilesWatcher,
		DirectorySync:       b.directorySyncManager,
		FolderStore:         b.workspaceFileStore,
		// TemplateManager: nil - loaded later in initializeTemplateManager
	})
	if err != nil {
		return err
	}
	b.orchestrationHandler = handler

	// Wire Note validation into the canonical Ticket service so a Ticket can
	// only link Notes that exist in its own workspace
	// (tasks/prd-workspace-ticket-management.md FR-17, FR-71). Without this,
	// link operations are refused rather than accepting unvalidated IDs.
	if b.sessionStore != nil {
		handler.SetTicketNoteLookup(session.NewTicketNoteLookup(b.sessionStore))
	}

	// Stop a task whose declared connection/runtime preconditions are unmet
	// before its run starts. Evaluators explicitly claim keys, so Email keeps its
	// existing behavior, runtime requirements compose beside it, and ordinary
	// planning/toolbox vocabulary remains ungated.
	gate := workspace.NewCompositeTaskCapabilityGate()
	if b.emailReadiness != nil {
		gate.Register(b.emailReadiness)
	}
	if b.runtimeCapabilityService != nil {
		gate.Register(b.runtimeCapabilityService)
		handler.SetTaskCapabilityValidator(b.runtimeCapabilityService)
	}
	b.taskCapabilityGate = gate
	handler.SetTaskCapabilityGate(gate)

	// Template-setup first-open auto-start runs seeded tasks through the same
	// execution path as the manual execute endpoint.
	if b.sessionHandler != nil {
		b.sessionHandler.SetTemplateSetupTaskStarter(handler.StartTaskAsync)
	}

	// Initialize auto-task handler for natural language task creation
	b.autoTaskHandler = orchestrationhttp.NewAutoTaskHandler(b.st, b.workspaceStore, b.llmFactory, b.configManager, b.eventBus)

	// Store orchestrator for chat handler injection
	b.chatHandler.SetOrchestrator(orch)

	return nil
}

// initializeWorkspaceOrchestrator creates the workspace orchestrator.
func (b *ServerBuilder) initializeWorkspaceOrchestrator() {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	llmAdapter := workspace.NewLLMFactoryAdapter(b.llmFactory, "openai")
	b.workspaceOrchestrator = workspace.NewOrchestrator(b.workspaceStore, b.st, llmAdapter, b.eventBus)
	var loopExecutor workspace.TaskHandler
	if b.runBackedTaskHandler != nil {
		loopExecutor = b.runBackedTaskHandler
		b.workspaceOrchestrator.SetTaskHandler(b.runBackedTaskHandler)
	} else if b.taskHandler != nil {
		loopExecutor = b.taskHandler
		b.workspaceOrchestrator.SetTaskHandler(b.taskHandler)
	}
	// Adaptive delegation loop (opt-in via ORI_DELEGATION_LOOP). Off by default so
	// task-failure behavior is unchanged unless explicitly enabled.
	if loopExecutor != nil && os.Getenv("ORI_DELEGATION_LOOP") == "true" {
		adapter := workspace.NewCoordinatorAdapter(b.workspaceStore, loopExecutor)
		loop := workspace.NewDelegationLoop(b.workspaceStore, loopExecutor, adapter, delegationCapsFromEnv())
		loop.SetEventBus(b.eventBus)
		if b.chatHandler != nil {
			if tracker := b.chatHandler.UtilityTelemetry(); tracker != nil {
				loop.SetTelemetry(tracker)
			}
		}
		b.workspaceOrchestrator.SetDelegationLoop(loop)
		logger.Info("Adaptive delegation loop enabled (ORI_DELEGATION_LOOP)", logger.Fields{})
	}
	if verbose {
		logger.Info("Workspace orchestrator initialized", logger.Fields{})
	}

	b.workspaceHandler = workspace.NewHTTPHandler(b.workspaceStore, b.workspaceOrchestrator, b.eventBus)
	b.workspaceHandler.SetDesktopOpener(b.desktopOpener)
	if b.workspaceFileStore != nil {
		b.workspaceHandler.SetFolderStore(b.workspaceFileStore)
	}
	// The Workshop editor's read sources: the agent's learned collection, Ori's
	// global capability library, and stage capacity (PRD FR-43, FR-55).
	//
	// Wired HERE, not in initializeTaskExecution, because workspaceHandler does
	// not exist until this phase — setting it earlier is a silent no-op that
	// leaves the editor showing no learned skills and no capacity.
	var workshopSkills workspace.ToolboxMigrationSkillSource
	if b.skillsManager != nil {
		workshopSkills = newSkillResolverAdapter(b.skillsManager)
	}
	b.workspaceHandler.SetToolboxInventoryDeps(
		workshopSkills,
		newToolboxLibraryAdapter(b.skillsManager, b.mcpConfigManager),
		newLoadoutResolverAdapter(b.st),
	)
	// The same inputs feed the Goal preflight that stops a cadence-driven run
	// before the model is invoked when its pinned toolbox is unusable (FR-105).
	if b.taskScheduler != nil {
		b.taskScheduler.SetToolboxPreflightDeps(workshopSkills, newLoadoutResolverAdapter(b.st))
	}
	if verbose {
		logger.Info("Workspace HTTP handler initialized", logger.Fields{})
	}
}

// initializeMissionBridge wires the workspace mission cadence into the run
// lifecycle. By the time this phase runs, the workspace store, workspace run
// service+store, agent store, and task scheduler all exist, so we can
// construct the MissionBridge and hand it to the scheduler + HTTP handler.
//
// Best-effort: if any prerequisite is missing (e.g. a test path that skipped
// the run service) the function logs and returns without panicking. Mission
// HTTP endpoints will return 503 in that mode, which is the intended UX.
func (b *ServerBuilder) initializeMissionBridge() {
	verbose := os.Getenv("ORI_VERBOSE") == "true"
	if b.taskScheduler == nil {
		if verbose {
			logger.Info("Mission bridge skipped: task scheduler not initialized", logger.Fields{})
		}
		return
	}
	if b.workspaceRunService == nil || b.workspaceRunStore == nil {
		if verbose {
			logger.Info("Mission bridge skipped: workspace run service not initialized", logger.Fields{})
		}
		return
	}
	if b.workspaceStore == nil {
		return
	}
	opportunityStore := workspace.NewOpportunityStore(b.workspaceStore)
	bridge := workspacerun.NewMissionBridge(workspacerun.MissionBridgeConfig{
		RunStore:         b.workspaceRunStore,
		Service:          b.workspaceRunService,
		WorkspaceStore:   b.workspaceStore,
		Agents:           b.st,
		OpportunityStore: opportunityStore,
	})
	if bridge == nil {
		logger.Warn("Mission bridge construction returned nil; mission triggers will not fire", logger.Fields{})
		return
	}
	b.taskScheduler.SetMissionTrigger(bridge)
	b.missionBridge = bridge
	if b.workspaceHandler != nil {
		b.workspaceHandler.SetScheduler(b.taskScheduler)
	}

	// Wire the Action Center handler with the same OpportunityStore so list,
	// dismiss, snooze, and resolve operations stay in lockstep with mission
	// runs that produce findings. Its own BacklogService instance (Add to
	// Backlog) shares the same store/event bus/file synchronizer as every
	// other capture surface (mirrors orchestrationhttp.Handler's own
	// construction — BacklogService is a stateless holder of those refs).
	actionCenterBacklogService := workspace.NewBacklogService(b.workspaceStore)
	actionCenterBacklogService.SetEventBus(b.eventBus)
	actionCenterBacklogService.SetSynchronizer(workspace.NewFileBacklogSynchronizer(b.workspaceStore))
	b.actionCenterHandler = actioncenterhttp.NewHandler(b.workspaceStore, opportunityStore, actionCenterBacklogService)

	// Event triggers reuse the same mission bridge (for mission_run actions)
	// and opportunity store (for failure findings).
	b.initializeTriggerService(opportunityStore)

	if verbose {
		logger.Info("Mission bridge initialized", logger.Fields{})
	}
}

// initializeTriggerService wires the event-trigger subsystem: webhook
// ingestion and file-watch triggers that fire missions or tasks. It depends
// on the workspace store (which must expose folder resolution so triggers can
// persist into each workspace folder) and reuses the mission bridge +
// opportunity store. Best-effort: if the store can't resolve folders, trigger
// support is skipped and the endpoints will 404.
func (b *ServerBuilder) initializeTriggerService(opportunityStore workspace.OpportunityStore) {
	// Triggers persist into each workspace's folder, so the source must be the
	// folder-based FileStore (List + GetFolderPath). The primary store may be a
	// SQLite-backed SyncStore that doesn't expose folder paths; prefer the
	// dedicated FileStore when present, otherwise fall back to a store that
	// happens to satisfy the interface.
	var source trigger.WorkspaceSource
	if b.workspaceFileStore != nil {
		source = b.workspaceFileStore
	} else if s, ok := b.workspaceStore.(trigger.WorkspaceSource); ok {
		source = s
	}
	if source == nil {
		logger.Warn("Trigger service skipped: no folder-based workspace store available", logger.Fields{})
		return
	}
	cfg := trigger.ServiceConfig{
		WorkspaceStore: b.workspaceStore,
		Source:         source,
		Opportunities:  opportunityStore,
	}
	if b.missionBridge != nil {
		cfg.Mission = b.missionBridge
	}
	svc, err := trigger.NewService(cfg)
	if err != nil {
		logger.Warn("Trigger service construction failed; event triggers disabled", logger.Fields{"error": err})
		return
	}
	if err := svc.Start(); err != nil {
		logger.Warn("Trigger service start failed; event triggers disabled", logger.Fields{"error": err})
		return
	}
	b.triggerService = svc
	b.triggerHandler = triggerhttp.NewHandler(svc)
	// The Janitor's watcher and daily catch-up need the trigger service, so
	// they are wired here rather than at handler-construction time.
	b.wireDownloadsJanitorAutomation()
	// Note: b.server.Handlers is rebuilt after this phase, so the handler is
	// attached to the facade in finalizeHandlers (alongside ActionCenter),
	// not here.
}

// initializeTemplateManager loads workflow templates and injects into orchestration handler.
func (b *ServerBuilder) initializeTemplateManager() {
	verbose := os.Getenv("ORI_VERBOSE") == "true"
	templatesDir := resolveWorkflowTemplatesDir()

	templateManager := templates.NewTemplateManager(templatesDir)
	if err := templateManager.LoadTemplates(); err != nil {
		if verbose {
			logger.Error("Warning: failed to load workflow templates", logger.Fields{"err": err})
		}
		return // Non-critical
	}

	if verbose {
		logger.Info("Loaded workflow templates", logger.Fields{"template_count": len(templateManager.ListTemplates())})
	}

	b.orchestrationHandler.SetTemplateManager(templateManager)

	// Initialize custom workflow manager
	customWorkflowsDir := filepath.Join(templatesDir, "custom")
	customWorkflowManager := templates.NewCustomWorkflowManager(customWorkflowsDir)
	if err := customWorkflowManager.LoadWorkflows(); err != nil {
		if verbose {
			logger.Error("Warning: failed to load custom workflows", logger.Fields{"err": err})
		}
	} else if verbose {
		logger.Info("Loaded custom workflows", logger.Fields{"count": len(customWorkflowManager.ListWorkflows())})
	}

	// Initialize workflow HTTP handler
	b.workflowHandler = workflowhttp.NewHandler(customWorkflowManager, b.workspaceStore)
}

// cleanupLegacyTemplateOnboardingSidecars removes the per-workspace
// template-onboarding.json session files the removed template-intake engine
// used to persist. Best-effort by design: failures are logged and never block
// startup, and workspaces without a sidecar are untouched.
func cleanupLegacyTemplateOnboardingSidecars(fileStore *workspace.FileStore) {
	if fileStore == nil {
		return
	}
	ids, err := fileStore.List()
	if err != nil {
		logger.Warn("Legacy template-onboarding cleanup: listing workspaces failed", logger.Fields{"error": err})
		return
	}
	removed := 0
	for _, id := range ids {
		folder, err := fileStore.GetFolderPath(id)
		if err != nil {
			continue
		}
		sidecar := filepath.Join(folder, "template-onboarding.json")
		if err := os.Remove(sidecar); err == nil {
			removed++
		} else if !os.IsNotExist(err) {
			logger.Warn("Legacy template-onboarding cleanup: remove failed", logger.Fields{"workspace_id": id, "error": err})
		}
	}
	if removed > 0 {
		logger.Info("Removed legacy template-onboarding session files", logger.Fields{"count": removed})
	}
}
