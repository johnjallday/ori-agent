package server

import (
	"github.com/johnjallday/ori-agent/internal/actioncenterhttp"
	"github.com/johnjallday/ori-agent/internal/agent"
	agenthttp "github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/calendarhttp"
	"github.com/johnjallday/ori-agent/internal/characterhttp"
	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/cliagent"
	"github.com/johnjallday/ori-agent/internal/cliagenthttp"
	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/connectionshttp"
	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/dailybriefhttp"
	"github.com/johnjallday/ori-agent/internal/devicehttp"
	"github.com/johnjallday/ori-agent/internal/downloadsjanitorhttp"
	"github.com/johnjallday/ori-agent/internal/evolutionhttp"
	"github.com/johnjallday/ori-agent/internal/externalagentshttp"
	"github.com/johnjallday/ori-agent/internal/fileshttp"
	"github.com/johnjallday/ori-agent/internal/gateway"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/location"
	"github.com/johnjallday/ori-agent/internal/locationhttp"
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
	"github.com/johnjallday/ori-agent/internal/progressionhttp"
	"github.com/johnjallday/ori-agent/internal/reviewhttp"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
	"github.com/johnjallday/ori-agent/internal/setupwizardhttp"
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
	"github.com/johnjallday/ori-agent/internal/workspacecapabilityhttp"
	"github.com/johnjallday/ori-agent/internal/workspacerun"
)

// CoreSystemFacade manages core system dependencies (LLM, client factory, config)
type CoreSystemFacade struct {
	ClientFactory *client.Factory
	LLMFactory    *llm.Factory
	ConfigManager *config.Manager
	CostTracker   *llm.CostTracker
	Gateway       *gateway.Service
}

// StorageSystemFacade manages all storage and state dependencies
type StorageSystemFacade struct {
	AgentStore      store.Store
	AgentStorePath  string
	WorkspaceStore  workspace.Store
	SessionStore    session.HybridStore
	UserStore       userprofile.UserStore
	UserProvider    userprofile.UserProvider
	OnboardingMgr   *onboarding.Manager
	LocationManager *location.Manager
	// PersonalHQ is the raw domain service (not the HTTP handler), so
	// non-HTTP callers like serveIndex's first-run classification can read
	// onboarding status directly.
	PersonalHQ *personalhq.Service
}

// WorkflowSystemFacade manages workspace orchestration dependencies
type WorkflowSystemFacade struct {
	TaskExecutor          *workspace.TaskExecutor
	StepExecutor          *workspace.StepExecutor
	TaskScheduler         *workspace.TaskScheduler
	EventBus              *workspace.EventBus
	NotificationService   *workspace.NotificationService
	DirectorySync         *workspace.DirectorySyncManager
	WorkspaceOrchestrator *workspace.Orchestrator
	// TriggerService owns event-trigger file watches; closed on Shutdown.
	// Assigned post-construction by the builder (not a constructor arg).
	TriggerService *trigger.Service
	// DailyBriefScheduler polls for due scheduled Daily Brief generations.
	// Assigned post-construction by the builder (not a constructor arg).
	DailyBriefScheduler *dailybrief.Scheduler
}

// IntegrationSystemFacade manages external integrations (MCP, updates)
type IntegrationSystemFacade struct {
	MCPRegistry      *mcp.Registry
	MCPConfigManager *mcp.ConfigManager
	UpdateManager    *updatemanager.Manager
	PrivateServices  privateservices.Client
}

// UISystemFacade manages UI rendering and web-related dependencies
type UISystemFacade struct {
	TemplateRenderer *web.TemplateRenderer
}

// HandlerFacade manages all HTTP handlers (API endpoints)
type HandlerFacade struct {
	ActivityLogger   *agenthttp.ActivityLogger
	Settings         *settingshttp.Handler
	Chat             *chathttp.Handler
	Onboarding       *onboardinghttp.Handler
	Device           *devicehttp.Handler
	Orchestration    *orchestrationhttp.Handler
	AutoTask         *orchestrationhttp.AutoTaskHandler
	Workspace        *workspace.HTTPHandler
	Usage            *usagehttp.Handler
	MCP              *mcphttp.Handler
	CalendarOps      *calendarhttp.Handler
	Plugin           *pluginhttp.Handler
	Location         *locationhttp.Handler
	Workflow         *workflowhttp.Handler
	ModelCategory    *modelcategoryhttp.Handler
	AutoCategorize   *modelcategoryhttp.AutoCategorizeHandler
	Reset            *settingshttp.ResetHandler
	AutoConfig       *agenthttp.AutoConfigHandler
	SmartOnboarding  *onboardinghttp.SmartOnboardingHandler
	Speech           *speechhttp.Handler
	Session          *sessionhttp.Handler
	AutoClassify     *sessionhttp.AutoClassifyHandler
	SmartInput       *sessionhttp.SmartInputHandler
	Note             *notehttp.Handler
	Progression      *progressionhttp.Handler
	SessionFiles     *fileshttp.Handler
	Review           *reviewhttp.Handler
	Evolution        *evolutionhttp.Handler
	Vault            *vaulthttp.Handler
	Connections      *connectionshttp.Handler
	ExternalAgents   *externalagentshttp.Handler
	Skills           *skillshttp.Handler
	CLIAgents        *cliagenthttp.Handler
	CLIAgentRegistry *cliagent.CLIAgentRegistry
	WorkspaceRuns    *workspacerun.Handler
	ActionCenter     *actioncenterhttp.Handler
	Triggers         *triggerhttp.Handler
	WorkspaceMemory  *memoryhttp.Handler
	DownloadsJanitor *downloadsjanitorhttp.Handler
	// WorkspaceCapabilities serves the built-in Workspace Capability catalog
	// and install lifecycle. One set of routes serves every capability; there
	// is no per-capability lifecycle API.
	WorkspaceCapabilities *workspacecapabilityhttp.Handler
	// SetupWizard serves the shared blueprint Setup Wizard for every
	// wizard-enabled workspace, whichever blueprint it came from.
	SetupWizard *setupwizardhttp.Handler
	User        *userhttp.Handler
	PersonalHQ  *personalhqhttp.Handler
	DailyBrief  *dailybriefhttp.Handler
	// Characters serves the read-only curated character catalog. It holds no
	// store and exposes no mutation route; identity assignment is validated by
	// the agent endpoints against the same catalog.
	Characters *characterhttp.Handler
}

// NewCoreSystemFacade creates a new core system facade
func NewCoreSystemFacade(
	clientFactory *client.Factory,
	llmFactory *llm.Factory,
	configManager *config.Manager,
	costTracker *llm.CostTracker,
	gw *gateway.Service,
) *CoreSystemFacade {
	return &CoreSystemFacade{
		ClientFactory: clientFactory,
		LLMFactory:    llmFactory,
		ConfigManager: configManager,
		CostTracker:   costTracker,
		Gateway:       gw,
	}
}

// NewStorageSystemFacade creates a new storage system facade
func NewStorageSystemFacade(
	agentStore store.Store,
	agentStorePath string,
	workspaceStore workspace.Store,
	sessionStore session.HybridStore,
	userStore userprofile.UserStore,
	userProvider userprofile.UserProvider,
	onboardingMgr *onboarding.Manager,
	locationManager *location.Manager,
	personalHQ *personalhq.Service,
) *StorageSystemFacade {
	return &StorageSystemFacade{
		AgentStore:      agentStore,
		AgentStorePath:  agentStorePath,
		WorkspaceStore:  workspaceStore,
		SessionStore:    sessionStore,
		UserStore:       userStore,
		UserProvider:    userProvider,
		OnboardingMgr:   onboardingMgr,
		LocationManager: locationManager,
		PersonalHQ:      personalHQ,
	}
}

// NewWorkflowSystemFacade creates a new workflow system facade
func NewWorkflowSystemFacade(
	taskExecutor *workspace.TaskExecutor,
	stepExecutor *workspace.StepExecutor,
	taskScheduler *workspace.TaskScheduler,
	eventBus *workspace.EventBus,
	notificationService *workspace.NotificationService,
	directorySync *workspace.DirectorySyncManager,
	workspaceOrchestrator *workspace.Orchestrator,
) *WorkflowSystemFacade {
	return &WorkflowSystemFacade{
		TaskExecutor:          taskExecutor,
		StepExecutor:          stepExecutor,
		TaskScheduler:         taskScheduler,
		EventBus:              eventBus,
		NotificationService:   notificationService,
		DirectorySync:         directorySync,
		WorkspaceOrchestrator: workspaceOrchestrator,
	}
}

// NewIntegrationSystemFacade creates a new integration system facade
func NewIntegrationSystemFacade(
	mcpRegistry *mcp.Registry,
	mcpConfigManager *mcp.ConfigManager,
	updateManager *updatemanager.Manager,
	privateServices privateservices.Client,
) *IntegrationSystemFacade {
	return &IntegrationSystemFacade{
		MCPRegistry:      mcpRegistry,
		MCPConfigManager: mcpConfigManager,
		UpdateManager:    updateManager,
		PrivateServices:  privateServices,
	}
}

// NewUISystemFacade creates a new UI system facade
func NewUISystemFacade(templateRenderer *web.TemplateRenderer) *UISystemFacade {
	return &UISystemFacade{
		TemplateRenderer: templateRenderer,
	}
}

// Start starts all workflow system background services
func (w *WorkflowSystemFacade) Start() {
	if w.TaskExecutor != nil {
		w.TaskExecutor.Start()
	}
	if w.StepExecutor != nil {
		w.StepExecutor.Start()
	}
	if w.TaskScheduler != nil {
		w.TaskScheduler.Start()
	}
	if w.DirectorySync != nil {
		w.DirectorySync.Start()
	}
	if w.DailyBriefScheduler != nil {
		w.DailyBriefScheduler.Start()
	}
}

// Shutdown gracefully shuts down all workflow system background services
func (w *WorkflowSystemFacade) Shutdown() {
	if w.DirectorySync != nil {
		w.DirectorySync.Stop()
	}
	if w.TaskExecutor != nil {
		w.TaskExecutor.Stop()
	}
	if w.StepExecutor != nil {
		w.StepExecutor.Stop()
	}
	if w.TaskScheduler != nil {
		w.TaskScheduler.Stop()
	}
	if w.DailyBriefScheduler != nil {
		w.DailyBriefScheduler.Stop()
	}
	if w.NotificationService != nil {
		w.NotificationService.Shutdown()
	}
	if w.EventBus != nil {
		w.EventBus.Shutdown()
	}
	if w.TriggerService != nil {
		w.TriggerService.Close()
	}
}

// GetAgentByName retrieves an agent by name from the storage system
func (s *StorageSystemFacade) GetAgentByName(name string) (*agent.Agent, bool) {
	return s.AgentStore.GetAgent(name)
}

// ListAgents returns all agent names.
func (s *StorageSystemFacade) ListAgents() []string {
	return s.AgentStore.ListAgents()
}
