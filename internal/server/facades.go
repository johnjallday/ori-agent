package server

import (
	"github.com/johnjallday/ori-agent/internal/agent"
	agenthttp "github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/cliagent"
	"github.com/johnjallday/ori-agent/internal/cliagenthttp"
	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/devicehttp"
	"github.com/johnjallday/ori-agent/internal/evolutionhttp"
	"github.com/johnjallday/ori-agent/internal/externalagentshttp"
	"github.com/johnjallday/ori-agent/internal/fileshttp"
	"github.com/johnjallday/ori-agent/internal/gateway"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/location"
	"github.com/johnjallday/ori-agent/internal/locationhttp"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcphttp"
	"github.com/johnjallday/ori-agent/internal/modelcategoryhttp"
	"github.com/johnjallday/ori-agent/internal/notehttp"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/onboardinghttp"
	"github.com/johnjallday/ori-agent/internal/orchestrationhttp"
	"github.com/johnjallday/ori-agent/internal/privateservices"
	"github.com/johnjallday/ori-agent/internal/reviewhttp"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
	"github.com/johnjallday/ori-agent/internal/skillshttp"
	"github.com/johnjallday/ori-agent/internal/speechhttp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/updatemanager"
	"github.com/johnjallday/ori-agent/internal/usagehttp"
	"github.com/johnjallday/ori-agent/internal/vaulthttp"
	web "github.com/johnjallday/ori-agent/internal/web"
	"github.com/johnjallday/ori-agent/internal/workflowhttp"
	"github.com/johnjallday/ori-agent/internal/workspace"
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
	OnboardingMgr   *onboarding.Manager
	LocationManager *location.Manager
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
	SessionFiles     *fileshttp.Handler
	Review           *reviewhttp.Handler
	Evolution        *evolutionhttp.Handler
	Vault            *vaulthttp.Handler
	ExternalAgents   *externalagentshttp.Handler
	Skills           *skillshttp.Handler
	CLIAgents        *cliagenthttp.Handler
	CLIAgentRegistry *cliagent.CLIAgentRegistry
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
	onboardingMgr *onboarding.Manager,
	locationManager *location.Manager,
) *StorageSystemFacade {
	return &StorageSystemFacade{
		AgentStore:      agentStore,
		AgentStorePath:  agentStorePath,
		WorkspaceStore:  workspaceStore,
		OnboardingMgr:   onboardingMgr,
		LocationManager: locationManager,
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

// NewHandlerFacade creates a new handler facade
func NewHandlerFacade(
	activityLogger *agenthttp.ActivityLogger,
	settings *settingshttp.Handler,
	chat *chathttp.Handler,
	onboarding *onboardinghttp.Handler,
	device *devicehttp.Handler,
	orchestration *orchestrationhttp.Handler,
	autoTask *orchestrationhttp.AutoTaskHandler,
	workspaceHandler *workspace.HTTPHandler,
	usage *usagehttp.Handler,
	mcp *mcphttp.Handler,
	location *locationhttp.Handler,
	workflow *workflowhttp.Handler,
	modelCategory *modelcategoryhttp.Handler,
	autoCategorize *modelcategoryhttp.AutoCategorizeHandler,
	reset *settingshttp.ResetHandler,
	autoConfig *agenthttp.AutoConfigHandler,
	smartOnboarding *onboardinghttp.SmartOnboardingHandler,
	speech *speechhttp.Handler,
	session *sessionhttp.Handler,
	autoClassify *sessionhttp.AutoClassifyHandler,
	smartInput *sessionhttp.SmartInputHandler,
	note *notehttp.Handler,
	sessionFiles *fileshttp.Handler,
	review *reviewhttp.Handler,
	evolutionHandler *evolutionhttp.Handler,
	vaultHandler *vaulthttp.Handler,
	externalAgents *externalagentshttp.Handler,
	skills *skillshttp.Handler,
) *HandlerFacade {
	return &HandlerFacade{
		ActivityLogger:  activityLogger,
		Settings:        settings,
		Chat:            chat,
		Onboarding:      onboarding,
		Device:          device,
		Orchestration:   orchestration,
		AutoTask:        autoTask,
		Workspace:       workspaceHandler,
		Usage:           usage,
		MCP:             mcp,
		Location:        location,
		Workflow:        workflow,
		ModelCategory:   modelCategory,
		AutoCategorize:  autoCategorize,
		Reset:           reset,
		AutoConfig:      autoConfig,
		SmartOnboarding: smartOnboarding,
		Speech:          speech,
		Session:         session,
		AutoClassify:    autoClassify,
		SmartInput:      smartInput,
		Note:            note,
		SessionFiles:    sessionFiles,
		Review:          review,
		Evolution:       evolutionHandler,
		Vault:           vaultHandler,
		ExternalAgents:  externalAgents,
		Skills:          skills,
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
	if w.NotificationService != nil {
		w.NotificationService.Shutdown()
	}
	if w.EventBus != nil {
		w.EventBus.Shutdown()
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
