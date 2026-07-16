// Package server provides the HTTP server for the Ori Agent application.
// This file implements the ServerBuilder pattern for constructing server instances
// with proper dependency injection and testability.
package server

import (
	"fmt"
	"os"

	"github.com/johnjallday/ori-agent/internal/actioncenterhttp"
	agenthttp "github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/cliagent"
	"github.com/johnjallday/ori-agent/internal/cliagenthttp"
	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/dailybriefhttp"
	"github.com/johnjallday/ori-agent/internal/devicehttp"
	"github.com/johnjallday/ori-agent/internal/evolution"
	"github.com/johnjallday/ori-agent/internal/evolutionhttp"
	"github.com/johnjallday/ori-agent/internal/externalagents"
	"github.com/johnjallday/ori-agent/internal/externalagentshttp"
	"github.com/johnjallday/ori-agent/internal/fileshttp"
	"github.com/johnjallday/ori-agent/internal/filewatcher"
	"github.com/johnjallday/ori-agent/internal/gateway"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/location"
	"github.com/johnjallday/ori-agent/internal/locationhttp"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/macwake"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcphttp"
	"github.com/johnjallday/ori-agent/internal/memoryhttp"
	"github.com/johnjallday/ori-agent/internal/modelcategoryhttp"
	"github.com/johnjallday/ori-agent/internal/notehttp"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/onboardinghttp"
	"github.com/johnjallday/ori-agent/internal/orchestrationhttp"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/personalhqhttp"
	"github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/privateservices"
	"github.com/johnjallday/ori-agent/internal/progression"
	"github.com/johnjallday/ori-agent/internal/progressionhttp"
	"github.com/johnjallday/ori-agent/internal/reviewhttp"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionfiles"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/skillshttp"
	"github.com/johnjallday/ori-agent/internal/speechhttp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/trigger"
	"github.com/johnjallday/ori-agent/internal/triggerhttp"
	"github.com/johnjallday/ori-agent/internal/updatemanager"
	"github.com/johnjallday/ori-agent/internal/usagehttp"
	"github.com/johnjallday/ori-agent/internal/userhttp"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/vaulthttp"
	web "github.com/johnjallday/ori-agent/internal/web"
	"github.com/johnjallday/ori-agent/internal/workflowhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacerun"
)

// ServerBuilder builds a Server instance through a series of initialization phases.
// It provides a structured, testable way to construct a server with all its dependencies.
//
// # Initialization Phase Groups
//
// The server initialization is organized into logical phase groups:
//
//	GROUP 1: CORE (Phases 1-4)
//	  - Configuration, Registry, Client Factory, LLM Factory
//	  - These are foundational and have no dependencies
//
//	GROUP 2: STORAGE (Phases 5-6)
//	  - Storage, Activity Logger
//	  - Depends on: Configuration
//
//	GROUP 3: SERVICES (Phases 7-16)
//	  - Health, Location, Updates, Templates, Onboarding, Cost, MCP
//	  - Depends on: Core, Storage
//
//	GROUP 4: HANDLERS (Phase 17)
//	  - HTTP Handlers for all API endpoints
//	  - Depends on: Core, Storage, Services
//
//	GROUP 5: ORCHESTRATION (Phases 18-23)
//	  - Workspace, Events, Tasks, Orchestration, Templates
//	  - Depends on: Handlers, Services
//
//	GROUP 6: FINALIZATION (Phases 24-25)
//	  - Domain Facades
//	  - Depends on: All previous phases
//
// # Usage
//
//	builder, err := NewServerBuilder()
//	if err != nil {
//	    return nil, err
//	}
//	server, err := builder.Build()
//
// # Testing
//
// Use With* methods to inject mock dependencies:
//
//	builder.WithStore(mockStore).WithLLMFactory(mockFactory)
type ServerBuilder struct {
	server *Server

	// Internal fields populated during initialization
	clientFactory            *client.Factory
	llmFactory               *llm.Factory
	st                       store.Store
	agentStorePath           string
	configManager            *config.Manager
	privateServicesClient    privateservices.Client
	templateRenderer         *web.TemplateRenderer
	updateMgr                *updatemanager.Manager
	workspaceStore           workspace.Store
	workspaceFileStore       *workspace.FileStore
	workspaceAllowlist       *workspace.Allowlist
	runtimeResolver          *workspace.AgentRuntimeResolver
	taskHandler              *workspace.LLMTaskHandler
	orchestrationTaskHandler *workspace.LLMTaskHandler
	runBackedTaskHandler     workspace.TaskHandler
	taskExecutor             *workspace.TaskExecutor
	stepExecutor             *workspace.StepExecutor
	taskScheduler            *workspace.TaskScheduler
	macWakeService           *macwake.Service
	eventBus                 *workspace.EventBus
	notificationService      *workspace.NotificationService
	directorySyncManager     *workspace.DirectorySyncManager
	workspaceOrchestrator    *workspace.Orchestrator
	costTracker              *llm.CostTracker
	mcpRegistry              *mcp.Registry
	mcpConfigManager         *mcp.ConfigManager
	locationManager          *location.Manager
	onboardingMgr            *onboarding.Manager
	userStore                userprofile.UserStore
	userProvider             userprofile.UserProvider
	gateway                  *gateway.Service
	evolutionService         *evolution.Service

	// Handlers
	activityLogger         *agenthttp.ActivityLogger
	settingsHandler        *settingshttp.Handler
	chatHandler            *chathttp.Handler
	utilityToolRegistry    *chathttp.UtilityToolRegistry
	onboardingHandler      *onboardinghttp.Handler
	deviceHandler          *devicehttp.Handler
	orchestrationHandler   *orchestrationhttp.Handler
	autoTaskHandler        *orchestrationhttp.AutoTaskHandler
	workspaceHandler       *workspace.HTTPHandler
	usageHandler           *usagehttp.Handler
	mcpHandler             *mcphttp.Handler
	pluginHandler          *pluginhttp.Handler
	locationHandler        *locationhttp.Handler
	workflowHandler        *workflowhttp.Handler
	modelCategoryStore     store.ModelCategoryStore
	modelCategoryHandler   *modelcategoryhttp.Handler
	autoCategorizeHandler  *modelcategoryhttp.AutoCategorizeHandler
	resetHandler           *settingshttp.ResetHandler
	autoConfigHandler      *agenthttp.AutoConfigHandler
	smartOnboardingHandler *onboardinghttp.SmartOnboardingHandler
	speechHandler          *speechhttp.Handler

	// Session management
	sessionStore        session.HybridStore
	sessionHandler      *sessionhttp.Handler
	autoClassifyHandler *sessionhttp.AutoClassifyHandler
	smartInputHandler   *sessionhttp.SmartInputHandler

	// Note generation
	noteHandler *notehttp.Handler

	// Onboarding progression (quest log)
	progressionEngine  *progression.Engine
	progressionHandler *progressionhttp.Handler

	// Session files management
	sessionFilesStore   *sessionfiles.Store
	sessionFilesWatcher *filewatcher.Watcher
	sessionFilesHandler *fileshttp.Handler

	// Review system
	reviewHandler    *reviewhttp.Handler
	evolutionHandler *evolutionhttp.Handler
	vaultHandler     *vaulthttp.Handler

	// External agents (Claude Code, Codex)
	externalAgentsCache   *externalagents.Cache
	externalAgentsHandler *externalagentshttp.Handler

	// CLI agent adapter (delegatable CLI agents)
	cliAgentRegistry *cliagent.CLIAgentRegistry
	cliAgentExecutor *cliagent.MicroStepExecutor
	cliAgentLogger   *cliagent.EventLogger
	cliAgentHandler  *cliagenthttp.Handler

	// Workspace Runs harness
	workspaceRunStore     workspacerun.Store
	workspaceRunService   *workspacerun.Service
	workspaceRunHandler   *workspacerun.Handler
	workspaceRunExecutors *workspacerun.ExecutorRegistry

	// Skills (local + external)
	skillsManager *skills.Manager
	skillsHandler *skillshttp.Handler

	// Action Center — cross-workspace mission opportunity triage.
	actionCenterHandler *actioncenterhttp.Handler

	// Event triggers — webhooks + file-watch that fire missions/tasks.
	missionBridge  *workspacerun.MissionBridge
	triggerService *trigger.Service
	triggerHandler *triggerhttp.Handler

	// User profile API
	userHandler *userhttp.Handler

	// Personal HQ designation and onboarding state
	personalHQService *personalhq.Service
	personalHQHandler *personalhqhttp.Handler

	// mailboxAccess gates the read-only Personal HQ mail tools; nil until the
	// vault system initializes it (internal/server/mailbox_access.go).
	mailboxAccess *mailboxAccess

	// dailyBriefMailbox feeds grounded email attention into the Daily Brief; nil
	// until the vault system initializes it (internal/server/dailybrief_mailbox.go).
	dailyBriefMailbox dailybrief.MailboxSource

	// mailDrafter creates local reply proposals for the mail_draft_reply tool;
	// nil until the vault system initializes it.
	mailDrafter chathttp.MailDrafter

	// Daily Brief configuration, generation, and scheduling
	dailyBriefService   *dailybrief.Service
	dailyBriefHandler   *dailybriefhttp.Handler
	dailyBriefScheduler *dailybrief.Scheduler
}

// NewServerBuilder creates a new ServerBuilder instance with an empty Server.
func NewServerBuilder() (*ServerBuilder, error) {
	return &ServerBuilder{
		server: &Server{
			Core:        &CoreSystemFacade{},
			Storage:     &StorageSystemFacade{},
			Workflow:    &WorkflowSystemFacade{},
			Integration: &IntegrationSystemFacade{},
			UI:          &UISystemFacade{},
			Handlers:    &HandlerFacade{},
		},
	}, nil
}

// Build executes all initialization phases in order and returns the fully constructed Server.
// Returns an error if any phase fails. See ServerBuilder documentation for phase groups.
func (b *ServerBuilder) Build() (*Server, error) {
	verbose := os.Getenv("ORI_VERBOSE") == "true"

	// ═══════════════════════════════════════════════════════════════════════════
	// GROUP 1: CORE - Foundational components with no dependencies
	// ═══════════════════════════════════════════════════════════════════════════

	if err := b.initializeConfiguration(); err != nil { // Phase 1
		return nil, fmt.Errorf("configuration phase failed: %w", err)
	}
	b.initializeClientFactory()                      // Phase 3 (deprecated)
	if err := b.initializeLLMFactory(); err != nil { // Phase 4
		return nil, fmt.Errorf("LLM factory phase failed: %w", err)
	}
	b.initializeGateway() // Phase 4.1

	// ═══════════════════════════════════════════════════════════════════════════
	// GROUP 2: STORAGE - Persistence layer (depends on: Core)
	// ═══════════════════════════════════════════════════════════════════════════

	if err := b.initializeStorage(); err != nil { // Phase 5
		return nil, fmt.Errorf("storage phase failed: %w", err)
	}
	b.initializeActivityLogger() // Phase 6

	// ═══════════════════════════════════════════════════════════════════════════
	// GROUP 3: SERVICES - Business logic layer (depends on: Core, Storage)
	// ═══════════════════════════════════════════════════════════════════════════

	b.initializeLocationManager()                          // Phase 8
	b.initializeUpdateManager()                            // Phase 10
	if err := b.initializeTemplateRenderer(); err != nil { // Phase 13
		return nil, fmt.Errorf("template renderer phase failed: %w", err)
	}
	b.initializeOnboardingManager() // Phase 14
	b.initializeCostTracker()       // Phase 15
	b.initializeMCP()               // Phase 16

	// ═══════════════════════════════════════════════════════════════════════════
	// GROUP 4: HANDLERS - HTTP API layer (depends on: Core, Storage, Services)
	// ═══════════════════════════════════════════════════════════════════════════

	b.initializeHandlers()    // Phase 17
	b.initializeMCPRegistry() // Phase 17.1 — wire MCP browser registry store

	// ═══════════════════════════════════════════════════════════════════════════
	// GROUP 5: ORCHESTRATION - Multi-agent coordination (depends on: Handlers)
	// ═══════════════════════════════════════════════════════════════════════════

	if err := b.initializeWorkspaceStore(); err != nil { // Phase 18
		return nil, fmt.Errorf("workspace store phase failed: %w", err)
	}
	b.initializeEventSystem()                           // Phase 19
	b.initializeTaskExecution()                         // Phase 20
	if err := b.initializeOrchestration(); err != nil { // Phase 21
		return nil, fmt.Errorf("orchestration phase failed: %w", err)
	}
	b.initializeWorkspaceOrchestrator() // Phase 22
	b.initializeMissionBridge()         // Phase 22.5 — wire mission cadence → run lifecycle
	b.initializeDailyBrief()            // Phase 22.6 — wire personal hq daily brief storage/scheduling/synthesis
	b.initializeTemplateManager()       // Phase 23

	// ═══════════════════════════════════════════════════════════════════════════
	// GROUP 6: FINALIZATION - Wire everything together (depends on: All)
	// ═══════════════════════════════════════════════════════════════════════════

	b.createDomainFacades() // Phase 25

	// Log success
	if !verbose {
		logger.Info("Server initialized successfully", logger.Fields{})
	}

	return b.server, nil
}

// createDomainFacades organizes dependencies into domain-specific facades
func (b *ServerBuilder) createDomainFacades() {
	// Core System Facade
	b.server.Core = NewCoreSystemFacade(
		b.clientFactory,
		b.llmFactory,
		b.configManager,
		b.costTracker,
		b.gateway,
	)

	// Storage System Facade
	b.server.Storage = NewStorageSystemFacade(
		b.st,
		b.agentStorePath,
		b.workspaceStore,
		b.sessionStore,
		b.userStore,
		b.userProvider,
		b.onboardingMgr,
		b.locationManager,
		b.personalHQService,
	)

	// Workflow System Facade
	b.server.Workflow = NewWorkflowSystemFacade(
		b.taskExecutor,
		b.stepExecutor,
		b.taskScheduler,
		b.eventBus,
		b.notificationService,
		b.directorySyncManager,
		b.workspaceOrchestrator,
	)
	// Trigger service shares the workflow facade's lifecycle (started during
	// mission-bridge init; stopped on Shutdown).
	b.server.Workflow.TriggerService = b.triggerService
	b.server.Workflow.DailyBriefScheduler = b.dailyBriefScheduler

	// Integration System Facade
	b.server.Integration = NewIntegrationSystemFacade(
		b.mcpRegistry,
		b.mcpConfigManager,
		b.updateMgr,
		b.privateServicesClient,
	)

	// UI System Facade
	b.server.UI = NewUISystemFacade(
		b.templateRenderer,
	)

	// Handler Facade. Built as a named-field literal rather than a positional
	// constructor: with ~37 same-shaped handler fields, a swapped pair of
	// arguments compiles fine but wires the wrong handler — named fields make
	// that mistake impossible, and late-built handlers (CLI agents, plugin,
	// triggers, ...) attach in the same place as everything else.
	handlers := &HandlerFacade{
		ActivityLogger:   b.activityLogger,
		Settings:         b.settingsHandler,
		Chat:             b.chatHandler,
		Onboarding:       b.onboardingHandler,
		Device:           b.deviceHandler,
		Orchestration:    b.orchestrationHandler,
		AutoTask:         b.autoTaskHandler,
		Workspace:        b.workspaceHandler,
		Usage:            b.usageHandler,
		MCP:              b.mcpHandler,
		Location:         b.locationHandler,
		Workflow:         b.workflowHandler,
		ModelCategory:    b.modelCategoryHandler,
		AutoCategorize:   b.autoCategorizeHandler,
		Reset:            b.resetHandler,
		AutoConfig:       b.autoConfigHandler,
		SmartOnboarding:  b.smartOnboardingHandler,
		Speech:           b.speechHandler,
		Session:          b.sessionHandler,
		AutoClassify:     b.autoClassifyHandler,
		SmartInput:       b.smartInputHandler,
		Note:             b.noteHandler,
		Progression:      b.progressionHandler,
		SessionFiles:     b.sessionFilesHandler,
		Review:           b.reviewHandler,
		Evolution:        b.evolutionHandler,
		Vault:            b.vaultHandler,
		ExternalAgents:   b.externalAgentsHandler,
		Skills:           b.skillsHandler,
		User:             b.userHandler,
		PersonalHQ:       b.personalHQHandler,
		DailyBrief:       b.dailyBriefHandler,
		CLIAgents:        b.cliAgentHandler,
		CLIAgentRegistry: b.cliAgentRegistry,
		WorkspaceRuns:    b.workspaceRunHandler,
		ActionCenter:     b.actionCenterHandler,
		Plugin:           b.pluginHandler,
		// initializeMissionBridge (which builds the trigger handler) runs
		// before this facade is rebuilt, so it must be attached here too.
		Triggers: b.triggerHandler,
	}
	if b.workspaceFileStore != nil {
		handlers.WorkspaceMemory = memoryhttp.NewHandler(b.workspaceFileStore, b.workspaceFileStore)
	}
	b.server.Handlers = handlers
}

// WithLLMFactory injects a custom LLM factory (for testing).
func (b *ServerBuilder) WithLLMFactory(f *llm.Factory) *ServerBuilder {
	b.llmFactory = f
	if b.server.Core == nil {
		b.server.Core = &CoreSystemFacade{}
	}
	b.server.Core.LLMFactory = f
	return b
}

// WithConfigManager injects a custom config manager (for testing).
func (b *ServerBuilder) WithConfigManager(c *config.Manager) *ServerBuilder {
	b.configManager = c
	if b.server.Core == nil {
		b.server.Core = &CoreSystemFacade{}
	}
	b.server.Core.ConfigManager = c
	return b
}

// WithStore injects a custom store (for testing).
func (b *ServerBuilder) WithStore(s store.Store) *ServerBuilder {
	b.st = s
	if b.server.Storage == nil {
		b.server.Storage = &StorageSystemFacade{}
	}
	b.server.Storage.AgentStore = s
	return b
}

// WithWorkspaceStore injects a custom workspace store (for testing).
func (b *ServerBuilder) WithWorkspaceStore(ws workspace.Store) *ServerBuilder {
	b.workspaceStore = ws
	if b.server.Storage == nil {
		b.server.Storage = &StorageSystemFacade{}
	}
	b.server.Storage.WorkspaceStore = ws
	return b
}
