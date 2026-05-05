// Package server provides workflow initialization methods for the ServerBuilder.
// This file contains methods for workspace, events, task execution, and orchestration.
package server

import (
	"os"
	"path/filepath"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/gateway"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/orchestration/templates"
	"github.com/johnjallday/ori-agent/internal/orchestrationhttp"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/toolapi"
	"github.com/johnjallday/ori-agent/internal/workflowhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
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
	return func(workspaceID string) []toolapi.Tool {
		provider := chathttp.NewWorkspaceToolProvider(sessionStore, workspaceStore, workspaceID)
		if fileStore != nil {
			provider.SetFileStore(fileStore)
		}
		return provider.Tools()
	}
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
		if b.sessionHandler != nil {
			b.sessionHandler.SetWorkspaceStore(fileStore)
		}
		if b.chatHandler != nil {
			b.chatHandler.SetFileStore(fileStore)
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
		if fileStore != nil {
			workspace.RestoreAllWorkspaceAgents(fileStore, b.st)
		}
		ws = workspace.NewAgentSnapshotStore(ws, b.st)
		// One-shot startup repair: restore imported workspace-local agent
		// snapshots into this environment, then refresh snapshots for globally
		// available agents referenced by primary or folder-only workspaces.
		workspace.RestoreAllWorkspaceAgents(ws, b.st)
		workspace.SnapshotAllWorkspaces(ws, b.st)
		if fileStore != nil {
			workspace.SnapshotAllWorkspaces(fileStore, b.st)
		}
	}

	b.workspaceStore = ws

	// Set workspace store on chat handler (uses SyncStore when available)
	b.chatHandler.SetWorkspaceStore(ws)

	return nil
}

// initializeEventSystem creates the event bus and notification service.
func (b *ServerBuilder) initializeEventSystem() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	b.eventBus = workspace.DefaultEventBus()
	if verbose {
		logger.Info("Event bus initialized", logger.Fields{})
	}

	b.notificationService = workspace.NewNotificationService(b.eventBus, 500)
	if verbose {
		logger.Info("Notification service initialized", logger.Fields{})
	}

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

	return nil
}

// initializeTaskExecution creates task handler, executor, step executor, and scheduler.
func (b *ServerBuilder) initializeTaskExecution() error {
	b.taskHandler = workspace.NewLLMTaskHandler(b.st, b.llmFactory, b.workspaceStore)
	b.taskHandler.SetEventBus(b.eventBus)
	b.taskHandler.SetMCPRegistry(b.mcpRegistry)
	b.taskHandler.SetUtilityToolProvider(b.utilityToolRegistry)
	runtimeResolver := workspace.NewAgentRuntimeResolver(b.st, b.workspaceStore, b.mcpRegistry, b.mcpConfigManager)
	if b.skillsManager != nil {
		runtimeResolver.SetSkillResolver(newSkillResolverAdapter(b.skillsManager))
	}
	b.taskHandler.SetRuntimeResolver(runtimeResolver)
	b.chatHandler.SetRuntimeResolver(runtimeResolver)
	if b.sessionStore != nil {
		b.taskHandler.SetContextStore(session.NewWorkspaceTaskContextAdapter(b.sessionStore))
	}
	if fn := b.buildWorkspaceToolFactory(); fn != nil {
		b.taskHandler.SetWorkspaceToolFactory(fn)
	}

	b.taskExecutor = workspace.NewTaskExecutor(b.workspaceStore, b.taskHandler, workspace.ExecutorConfig{
		PollInterval:  10 * time.Second,
		MaxConcurrent: 5,
	})
	b.taskExecutor.SetEventBus(b.eventBus)

	b.stepExecutor = workspace.NewStepExecutor(b.workspaceStore, b.taskHandler, workspace.StepExecutorConfig{
		PollInterval: 5 * time.Second,
	})

	b.taskScheduler = workspace.NewTaskScheduler(b.workspaceStore, workspace.SchedulerConfig{
		PollInterval: 1 * time.Minute,
	})
	b.taskScheduler.SetEventBus(b.eventBus)

	return nil
}

// initializeOrchestration creates orchestrators and handlers.
func (b *ServerBuilder) initializeOrchestration() error {
	communicator := agentcomm.NewCommunicator(b.workspaceStore)

	var history gateway.ConversationStore
	if b.sessionStore != nil {
		history = session.NewGatewaySessionStore(b.sessionStore)
	}

	orch := orchestration.NewOrchestrator(b.st, b.workspaceStore, history, communicator, b.llmFactory, b.configManager, b.eventBus)

	// Wire gateway to orchestrator if initialized
	if b.gateway != nil {
		orch.SetGateway(b.gateway)
		b.gateway.SetRouter(orch.HandleGatewayMessage)
	}

	taskHandler := workspace.NewLLMTaskHandler(b.st, b.llmFactory, b.workspaceStore)
	taskHandler.SetEventBus(b.eventBus)
	taskHandler.SetMCPRegistry(b.mcpRegistry)
	taskHandler.SetUtilityToolProvider(b.utilityToolRegistry)
	taskHandler.SetRuntimeResolver(workspace.NewAgentRuntimeResolver(b.st, b.workspaceStore, b.mcpRegistry, b.mcpConfigManager))
	if b.sessionStore != nil {
		taskHandler.SetContextStore(session.NewWorkspaceTaskContextAdapter(b.sessionStore))
	}
	if fn := b.buildWorkspaceToolFactory(); fn != nil {
		taskHandler.SetWorkspaceToolFactory(fn)
	}
	b.orchestrationTaskHandler = taskHandler

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
		// TemplateManager: nil - loaded later in initializeTemplateManager
	})
	if err != nil {
		return err
	}
	b.orchestrationHandler = handler

	// Initialize auto-task handler for natural language task creation
	b.autoTaskHandler = orchestrationhttp.NewAutoTaskHandler(b.st, b.workspaceStore, b.llmFactory, b.configManager)

	// Store orchestrator for chat handler injection
	b.chatHandler.SetOrchestrator(orch)

	return nil
}

// initializeWorkspaceOrchestrator creates the workspace orchestrator.
func (b *ServerBuilder) initializeWorkspaceOrchestrator() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	llmAdapter := workspace.NewLLMFactoryAdapter(b.llmFactory, "openai")
	b.workspaceOrchestrator = workspace.NewOrchestrator(b.workspaceStore, b.st, llmAdapter, b.eventBus)
	if b.taskHandler != nil {
		b.workspaceOrchestrator.SetTaskHandler(b.taskHandler)
	}
	if verbose {
		logger.Info("Workspace orchestrator initialized", logger.Fields{})
	}

	b.workspaceHandler = workspace.NewHTTPHandler(b.workspaceStore, b.workspaceOrchestrator, b.eventBus)
	if verbose {
		logger.Info("Workspace HTTP handler initialized", logger.Fields{})
	}

	return nil
}

// initializeTemplateManager loads workflow templates and injects into orchestration handler.
func (b *ServerBuilder) initializeTemplateManager() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"
	templatesDir := resolveWorkflowTemplatesDir()

	templateManager := templates.NewTemplateManager(templatesDir)
	if err := templateManager.LoadTemplates(); err != nil {
		if verbose {
			logger.Error("Warning: failed to load workflow templates", logger.Fields{"err": err})
		}
		return nil // Non-critical
	}

	if verbose {
		logger.Info("Loaded workflow templates", logger.Fields{"listtemplates())": len(templateManager.ListTemplates())})
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

	return nil
}
