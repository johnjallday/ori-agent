package server

import (
	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/agentstudio"
	"github.com/johnjallday/ori-agent/internal/client"
	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/location"
	"github.com/johnjallday/ori-agent/internal/mcp"
	"github.com/johnjallday/ori-agent/internal/onboarding"
	"github.com/johnjallday/ori-agent/internal/plugindownloader"
	"github.com/johnjallday/ori-agent/internal/pluginmanager"
	"github.com/johnjallday/ori-agent/internal/registry"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/updatemanager"
	web "github.com/johnjallday/ori-agent/internal/web"
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
	VersionManager      *pluginmanager.VersionManager
	NotificationManager *pluginmanager.NotificationManager
	BackupManager       *pluginmanager.BackupManager
}

// StorageSystemFacade manages all storage and state dependencies
type StorageSystemFacade struct {
	AgentStore      store.Store
	AgentStorePath  string
	WorkspaceStore  agentstudio.Store
	OnboardingMgr   *onboarding.Manager
	LocationManager *location.Manager
}

// WorkflowSystemFacade manages agent studio and orchestration dependencies
type WorkflowSystemFacade struct {
	TaskExecutor        *agentstudio.TaskExecutor
	StepExecutor        *agentstudio.StepExecutor
	TaskScheduler       *agentstudio.TaskScheduler
	EventBus            *agentstudio.EventBus
	NotificationService *agentstudio.NotificationService
	StudioOrchestrator  *agentstudio.Orchestrator
}

// IntegrationSystemFacade manages external integrations (MCP, updates)
type IntegrationSystemFacade struct {
	MCPRegistry      *mcp.Registry
	MCPConfigManager *mcp.ConfigManager
	UpdateManager    *updatemanager.Manager
}

// UISystemFacade manages UI rendering and web-related dependencies
type UISystemFacade struct {
	TemplateRenderer *web.TemplateRenderer
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
	versionManager *pluginmanager.VersionManager,
	notificationManager *pluginmanager.NotificationManager,
	backupManager *pluginmanager.BackupManager,
) *PluginSystemFacade {
	return &PluginSystemFacade{
		RegistryManager:     registryManager,
		PluginRegistry:      pluginRegistry,
		PluginDownloader:    pluginDownloader,
		CategoryManager:     categoryManager,
		PermissionManager:   permissionManager,
		VersionManager:      versionManager,
		NotificationManager: notificationManager,
		BackupManager:       backupManager,
	}
}

// NewStorageSystemFacade creates a new storage system facade
func NewStorageSystemFacade(
	agentStore store.Store,
	agentStorePath string,
	workspaceStore agentstudio.Store,
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
	taskExecutor *agentstudio.TaskExecutor,
	stepExecutor *agentstudio.StepExecutor,
	taskScheduler *agentstudio.TaskScheduler,
	eventBus *agentstudio.EventBus,
	notificationService *agentstudio.NotificationService,
	studioOrchestrator *agentstudio.Orchestrator,
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
) *IntegrationSystemFacade {
	return &IntegrationSystemFacade{
		MCPRegistry:      mcpRegistry,
		MCPConfigManager: mcpConfigManager,
		UpdateManager:    updateManager,
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
