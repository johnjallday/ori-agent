// Package server provides workflow initialization methods for the ServerBuilder.
// This file contains methods for workspace, events, task execution, orchestration, and studio.
package server

import (
	"os"
	"path/filepath"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
	"github.com/johnjallday/ori-agent/internal/gateway"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/orchestration"
	"github.com/johnjallday/ori-agent/internal/orchestration/templates"
	"github.com/johnjallday/ori-agent/internal/orchestrationhttp"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/workflowhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// initializeWorkspaceStore creates the workspace storage system.
// Uses the session HybridStore as the underlying storage via an adapter,
// which unifies workspace data between the Sessions sidebar and Studios page.
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

	b.workspaceStore = ws

	// Now update chat handler with workspace store
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
	taskHandler := workspace.NewLLMTaskHandler(b.st, b.llmFactory, b.workspaceStore)
	taskHandler.SetEventBus(b.eventBus)
	taskHandler.SetMCPRegistry(b.mcpRegistry)
	if b.sessionStore != nil {
		taskHandler.SetContextStore(session.NewWorkspaceTaskContextAdapter(b.sessionStore))
	}

	b.taskExecutor = workspace.NewTaskExecutor(b.workspaceStore, taskHandler, workspace.ExecutorConfig{
		PollInterval:  10 * time.Second,
		MaxConcurrent: 5,
	})
	b.taskExecutor.SetEventBus(b.eventBus)

	b.stepExecutor = workspace.NewStepExecutor(b.workspaceStore, taskHandler, workspace.StepExecutorConfig{
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
	taskHandler.SetMCPRegistry(b.mcpRegistry)
	if b.sessionStore != nil {
		taskHandler.SetContextStore(session.NewWorkspaceTaskContextAdapter(b.sessionStore))
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

// initializeStudioOrchestrator creates the autonomous agent studio orchestrator.
func (b *ServerBuilder) initializeStudioOrchestrator() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	llmAdapter := workspace.NewLLMFactoryAdapter(b.llmFactory, "openai")
	b.studioOrchestrator = workspace.NewOrchestrator(b.workspaceStore, b.st, llmAdapter, b.eventBus)
	if verbose {
		logger.Info("Agent Studio orchestrator initialized", logger.Fields{})
	}

	b.studioHandler = workspace.NewHTTPHandler(b.workspaceStore, b.studioOrchestrator, b.eventBus)
	if verbose {
		logger.Info("Agent Studio HTTP handler initialized", logger.Fields{})
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
