// Package server provides workflow initialization methods for the ServerBuilder.
// This file contains methods for workspace, events, task execution, orchestration, and studio.
package server

import (
	"os"
	"path/filepath"
	"time"

	"github.com/johnjallday/ori-agent/internal/agentcomm"
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
	if b.server.sessionStore != nil {
		adapter := session.NewWorkspaceStoreAdapter(b.server.sessionStore)
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

	b.server.workspaceStore = ws

	// Now update chat handler with workspace store
	b.server.chatHandler.SetWorkspaceStore(ws)

	return nil
}

// initializeEventSystem creates the event bus and notification service.
func (b *ServerBuilder) initializeEventSystem() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	b.server.eventBus = workspace.DefaultEventBus()
	if verbose {
		logger.Info("Event bus initialized", logger.Fields{})
	}

	b.server.notificationService = workspace.NewNotificationService(b.server.eventBus, 500)
	if verbose {
		logger.Info("Notification service initialized", logger.Fields{})
	}

	return nil
}

// initializeTaskExecution creates task handler, executor, step executor, and scheduler.
func (b *ServerBuilder) initializeTaskExecution() error {
	s := b.server

	taskHandler := workspace.NewLLMTaskHandler(s.st, s.llmFactory, s.workspaceStore)
	taskHandler.SetEventBus(s.eventBus)

	s.taskExecutor = workspace.NewTaskExecutor(s.workspaceStore, taskHandler, workspace.ExecutorConfig{
		PollInterval:  10 * time.Second,
		MaxConcurrent: 5,
	})
	s.taskExecutor.SetEventBus(s.eventBus)

	s.stepExecutor = workspace.NewStepExecutor(s.workspaceStore, taskHandler, workspace.StepExecutorConfig{
		PollInterval: 5 * time.Second,
	})

	s.taskScheduler = workspace.NewTaskScheduler(s.workspaceStore, workspace.SchedulerConfig{
		PollInterval: 1 * time.Minute,
	})
	s.taskScheduler.SetEventBus(s.eventBus)

	return nil
}

// initializeOrchestration creates orchestrators and handlers.
func (b *ServerBuilder) initializeOrchestration() error {
	s := b.server

	communicator := agentcomm.NewCommunicator(s.workspaceStore)
	orch := orchestration.NewOrchestrator(s.st, s.workspaceStore, communicator, s.llmFactory, s.configManager, s.eventBus)
	taskHandler := workspace.NewLLMTaskHandler(s.st, s.llmFactory, s.workspaceStore)

	// Create session store adapter for orchestration handler
	var sessionStoreAdapter orchestrationhttp.SessionStore
	if s.sessionStore != nil {
		sessionStoreAdapter = session.NewOrchestrationSessionStore(s.sessionStore)
	}

	// Create orchestration handler with all available dependencies
	// Note: TemplateManager is added later via SetTemplateManager in initializeTemplateManager
	handler, err := orchestrationhttp.NewHandler(orchestrationhttp.HandlerConfig{
		AgentStore:          s.st,
		WorkspaceStore:      s.workspaceStore,
		EventBus:            s.eventBus,
		Orchestrator:        orch,
		NotificationService: s.notificationService,
		TaskHandler:         taskHandler,
		SessionStore:        sessionStoreAdapter,
		// TemplateManager: nil - loaded later in initializeTemplateManager
	})
	if err != nil {
		return err
	}
	s.orchestrationHandler = handler

	// Initialize auto-task handler for natural language task creation
	s.autoTaskHandler = orchestrationhttp.NewAutoTaskHandler(s.st, s.workspaceStore, s.llmFactory, s.configManager)

	// Store orchestrator for chat handler injection
	b.server.chatHandler.SetOrchestrator(orch)

	return nil
}

// initializeStudioOrchestrator creates the autonomous agent studio orchestrator.
func (b *ServerBuilder) initializeStudioOrchestrator() error {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	llmAdapter := workspace.NewLLMFactoryAdapter(b.server.llmFactory, "openai")
	b.server.studioOrchestrator = workspace.NewOrchestrator(b.server.workspaceStore, b.server.st, llmAdapter, b.server.eventBus)
	if verbose {
		logger.Info("Agent Studio orchestrator initialized", logger.Fields{})
	}

	b.server.studioHandler = workspace.NewHTTPHandler(b.server.workspaceStore, b.server.studioOrchestrator, b.server.eventBus)
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

	b.server.orchestrationHandler.SetTemplateManager(templateManager)

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
	b.server.workflowHandler = workflowhttp.NewHandler(customWorkflowManager, b.server.workspaceStore)

	return nil
}
