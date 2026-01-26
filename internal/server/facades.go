package server

import (
	"github.com/johnjallday/ori-agent/internal/agent"
	agenthttp "github.com/johnjallday/ori-agent/internal/agenthttp"
	"github.com/johnjallday/ori-agent/internal/chathttp"
	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/devicehttp"
	"github.com/johnjallday/ori-agent/internal/externalagentshttp"
	"github.com/johnjallday/ori-agent/internal/fileshttp"
	"github.com/johnjallday/ori-agent/internal/healthhttp"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/location"
	"github.com/johnjallday/ori-agent/internal/locationhttp"
	"github.com/johnjallday/ori-agent/internal/marketplacehttp"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/mcphttp"
	"github.com/johnjallday/ori-agent/internal/modelcategoryhttp"
	"github.com/johnjallday/ori-agent/internal/notehttp"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/onboardinghttp"
	"github.com/johnjallday/ori-agent/internal/orchestrationhttp"
	"github.com/johnjallday/ori-agent/internal/plugindownloader"
	pluginhttp "github.com/johnjallday/ori-agent/internal/pluginhttp"
	"github.com/johnjallday/ori-agent/internal/pluginmanager"
	"github.com/johnjallday/ori-agent/internal/pluginupdate"
	"github.com/johnjallday/ori-agent/internal/pluginupdateservice"
	"github.com/johnjallday/ori-agent/internal/privateservices"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/reviewhttp"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/settingshttp"
	"github.com/johnjallday/ori-agent/internal/skillshttp"
	"github.com/johnjallday/ori-agent/internal/speechhttp"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/updatemanager"
	"github.com/johnjallday/ori-agent/internal/usagehttp"
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
}

// PluginSystemFacade manages all plugin-related dependencies
type PluginSystemFacade struct {
	RegistryManager     *registry.Manager
	PluginRegistry      types.PluginRegistry
	PluginDownloader    *plugindownloader.PluginDownloader
	CategoryManager     *pluginmanager.CategoryManager
	PermissionManager   *pluginmanager.PermissionManager
	NotificationManager *pluginmanager.NotificationManager
	BackupManager       *pluginmanager.BackupManager
	PluginUpdateService *pluginupdateservice.Service
}

// StorageSystemFacade manages all storage and state dependencies
type StorageSystemFacade struct {
	AgentStore      store.Store
	AgentStorePath  string
	WorkspaceStore  workspace.Store
	OnboardingMgr   *onboarding.Manager
	LocationManager *location.Manager
}

// WorkflowSystemFacade manages agent studio and orchestration dependencies
type WorkflowSystemFacade struct {
	TaskExecutor        *workspace.TaskExecutor
	StepExecutor        *workspace.StepExecutor
	TaskScheduler       *workspace.TaskScheduler
	EventBus            *workspace.EventBus
	NotificationService *workspace.NotificationService
	StudioOrchestrator  *workspace.Orchestrator
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
	HealthManager   *healthhttp.Manager
	ActivityLogger  *agenthttp.ActivityLogger
	Settings        *settingshttp.Handler
	Chat            *chathttp.Handler
	Plugin          *pluginhttp.Handler
	PluginRegistry  *pluginhttp.RegistryHandler
	PluginInit      *pluginhttp.InitHandler
	Health          *healthhttp.Handler
	PluginUpdate    *pluginupdate.Handler
	Onboarding      *onboardinghttp.Handler
	Device          *devicehttp.Handler
	WebPage         *pluginhttp.WebPageHandler
	Orchestration   *orchestrationhttp.Handler
	AutoTask        *orchestrationhttp.AutoTaskHandler
	Studio          *workspace.HTTPHandler
	Usage           *usagehttp.Handler
	MCP             *mcphttp.Handler
	AgentMCP        *agenthttp.MCPHandler
	Location        *locationhttp.Handler
	PluginsPage     *pluginhttp.PluginsPageHandler
	Permissions     *pluginhttp.PermissionsHandler
	Backup          *pluginhttp.BackupHandler
	Notifications   *pluginhttp.NotificationsHandler
	Workflow        *workflowhttp.Handler
	Marketplace     *marketplacehttp.Handler
	ModelCategory   *modelcategoryhttp.Handler
	AutoCategorize  *modelcategoryhttp.AutoCategorizeHandler
	Reset           *settingshttp.ResetHandler
	AutoConfig      *agenthttp.AutoConfigHandler
	SmartOnboarding *onboardinghttp.SmartOnboardingHandler
	Speech          *speechhttp.Handler
	Session         *sessionhttp.Handler
	AutoClassify    *sessionhttp.AutoClassifyHandler
	SmartInput      *sessionhttp.SmartInputHandler
	Note            *notehttp.Handler
	SessionFiles    *fileshttp.Handler
	Review          *reviewhttp.Handler
	ExternalAgents  *externalagentshttp.Handler
	Skills          *skillshttp.Handler
}

// NewCoreSystemFacade creates a new core system facade
func NewCoreSystemFacade(
	clientFactory *client.Factory,
	llmFactory *llm.Factory,
	configManager *config.Manager,
	costTracker *llm.CostTracker,
) *CoreSystemFacade {
	return &CoreSystemFacade{
		ClientFactory: clientFactory,
		LLMFactory:    llmFactory,
		ConfigManager: configManager,
		CostTracker:   costTracker,
	}
}

// NewPluginSystemFacade creates a new plugin system facade
func NewPluginSystemFacade(
	registryManager *registry.Manager,
	pluginRegistry types.PluginRegistry,
	pluginDownloader *plugindownloader.PluginDownloader,
	categoryManager *pluginmanager.CategoryManager,
	permissionManager *pluginmanager.PermissionManager,
	notificationManager *pluginmanager.NotificationManager,
	backupManager *pluginmanager.BackupManager,
	pluginUpdateService *pluginupdateservice.Service,
) *PluginSystemFacade {
	return &PluginSystemFacade{
		RegistryManager:     registryManager,
		PluginRegistry:      pluginRegistry,
		PluginDownloader:    pluginDownloader,
		CategoryManager:     categoryManager,
		PermissionManager:   permissionManager,
		NotificationManager: notificationManager,
		BackupManager:       backupManager,
		PluginUpdateService: pluginUpdateService,
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
	studioOrchestrator *workspace.Orchestrator,
) *WorkflowSystemFacade {
	return &WorkflowSystemFacade{
		TaskExecutor:        taskExecutor,
		StepExecutor:        stepExecutor,
		TaskScheduler:       taskScheduler,
		EventBus:            eventBus,
		NotificationService: notificationService,
		StudioOrchestrator:  studioOrchestrator,
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
	healthManager *healthhttp.Manager,
	activityLogger *agenthttp.ActivityLogger,
	settings *settingshttp.Handler,
	chat *chathttp.Handler,
	plugin *pluginhttp.Handler,
	pluginRegistry *pluginhttp.RegistryHandler,
	pluginInit *pluginhttp.InitHandler,
	health *healthhttp.Handler,
	pluginUpdate *pluginupdate.Handler,
	onboarding *onboardinghttp.Handler,
	device *devicehttp.Handler,
	webPage *pluginhttp.WebPageHandler,
	orchestration *orchestrationhttp.Handler,
	autoTask *orchestrationhttp.AutoTaskHandler,
	studio *workspace.HTTPHandler,
	usage *usagehttp.Handler,
	mcp *mcphttp.Handler,
	agentMCP *agenthttp.MCPHandler,
	location *locationhttp.Handler,
	pluginsPage *pluginhttp.PluginsPageHandler,
	permissions *pluginhttp.PermissionsHandler,
	backup *pluginhttp.BackupHandler,
	notifications *pluginhttp.NotificationsHandler,
	workflow *workflowhttp.Handler,
	marketplace *marketplacehttp.Handler,
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
	externalAgents *externalagentshttp.Handler,
	skills *skillshttp.Handler,
) *HandlerFacade {
	return &HandlerFacade{
		HealthManager:   healthManager,
		ActivityLogger:  activityLogger,
		Settings:        settings,
		Chat:            chat,
		Plugin:          plugin,
		PluginRegistry:  pluginRegistry,
		PluginInit:      pluginInit,
		Health:          health,
		PluginUpdate:    pluginUpdate,
		Onboarding:      onboarding,
		Device:          device,
		WebPage:         webPage,
		Orchestration:   orchestration,
		AutoTask:        autoTask,
		Studio:          studio,
		Usage:           usage,
		MCP:             mcp,
		AgentMCP:        agentMCP,
		Location:        location,
		PluginsPage:     pluginsPage,
		Permissions:     permissions,
		Backup:          backup,
		Notifications:   notifications,
		Workflow:        workflow,
		Marketplace:     marketplace,
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
}

// Shutdown gracefully shuts down all workflow system background services
func (w *WorkflowSystemFacade) Shutdown() {
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

// ListAgents returns all agents and the current agent name
func (s *StorageSystemFacade) ListAgents() ([]string, string) {
	return s.AgentStore.ListAgents()
}
