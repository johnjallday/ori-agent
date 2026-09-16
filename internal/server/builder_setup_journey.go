package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/hostquests"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/reviewedintegration"
	"github.com/johnjallday/ori-agent/internal/samplelibrary"
	"github.com/johnjallday/ori-agent/internal/samplelibraryhttp"
	"github.com/johnjallday/ori-agent/internal/sessionhttp"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/setupjourneyhttp"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// allowlistLocallyCreatedWorkspace records that this data directory owns a
// workspace a host service just created, so the startup agent wipe keeps its
// staffed agents. It mirrors the session handler's helper for the generic
// workspace API; services outside that handler, like guided setup, reach the
// same allowlist through here. Best-effort: a failure only affects later agent
// hydration, never the creation itself.
func (b *ServerBuilder) allowlistLocallyCreatedWorkspace(workspaceID string) {
	if b == nil || b.workspaceAllowlist == nil || strings.TrimSpace(workspaceID) == "" {
		return
	}
	if err := b.workspaceAllowlist.Add(workspaceID); err != nil {
		logger.Warn("Failed to allowlist created workspace", logger.Fields{"id": workspaceID, "error": err.Error()})
	}
}

// readSetupSummary supplies only closed navigation/continuation offers. The
// journey reconciler, not this reader, decides when all owners are ready and
// these actions may be exposed.
func readSetupSummary(_ context.Context, scope setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
	actions := []setupjourney.ActionID{setupjourney.ActionReviewSetup}
	if scope.ProjectWorkspaceID != "" {
		actions = append(actions, setupjourney.ActionOpenProject, setupjourney.ActionOpenLiveSetup)
	}
	if scope.HomeWorkspaceID != "" {
		actions = append(actions, setupjourney.ActionOpenHome, setupjourney.ActionOpenSampleLibrarySetup)
	}
	if scope.RunKind == setupjourney.RunKindRoot && scope.HomeWorkspaceID != "" && scope.ProjectWorkspaceID != "" {
		actions = append(actions, setupjourney.ActionConnectAnotherProject)
	}
	return setupjourney.CanonicalStepRead{AvailableActions: actions}, nil
}

// initializeSetupJourney wires the durable generic shell before individual
// canonical adapters are added. Missing owners fail closed; later adapter tasks
// replace these readers with plugin/workspace/runtime/Assistant Program reads.
func (b *ServerBuilder) initializeSetupJourney() {
	if b == nil || b.sessionStore == nil || b.personalAssistantStore == nil {
		return
	}
	readers := make(map[specialist.SetupStepKind]setupjourney.CanonicalReader, len(specialist.SetupStepKinds()))
	// Every compiled kind starts fail-closed; owners that exist in this build
	// replace their reader below. A build without the mailbox runtime keeps the
	// account steps owner_unavailable rather than guessing readiness.
	for _, kind := range []specialist.SetupStepKind{
		specialist.SetupStepAssistantProgramStaffing,
		specialist.SetupStepWorkspaceCreate,
		specialist.SetupStepAccountConnect,
		specialist.SetupStepAccountLink,
	} {
		readers[kind] = setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
			return setupjourney.CanonicalStepRead{BlockedReason: setupjourney.ReasonOwnerUnavailable}, nil
		})
	}
	var pluginQuestCatalog setupjourney.QuestCatalog
	if b.pluginHandler != nil {
		pluginQuestCatalog = setupjourney.NewInstalledQuestCatalog(b.pluginHandler.Manager())
	}
	readers[specialist.SetupStepSummary] = setupSummaryReader{
		modelAvailable: b.systemModelAvailable, pluginQuests: pluginQuestCatalog,
	}
	mailboxAdapter := b.emailOpsQuestReaders(readers)
	var integrationAdapter *setupjourney.ReviewedIntegrationAdapter
	var projectAdapter *setupjourney.ProjectConnectionAdapter
	var workspaceSetupAdapter *setupjourney.WorkspaceSetupAdapter
	var staffingAdapter *setupjourney.AssistantStaffingAdapter
	if connectionStore, ok := b.workspaceStore.(interface {
		workspace.Store
		GetFolderPath(string) (string, error)
	}); ok && b.setupWizardService != nil {
		workspaceSetupAdapter = setupjourney.NewWorkspaceSetupAdapter(
			b.setupWizardService, workspaceProjectFileReadiness{store: connectionStore},
		)
		readers[specialist.SetupStepWorkspaceSetup] = workspaceSetupAdapter
	}
	if readers[specialist.SetupStepWorkspaceSetup] == nil {
		readers[specialist.SetupStepWorkspaceSetup] = setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
			return setupjourney.CanonicalStepRead{BlockedReason: setupjourney.ReasonOwnerUnavailable}, nil
		})
	}
	if b.pluginHandler != nil {
		integrationAdapter = setupjourney.NewReviewedIntegrationAdapterForDevelopment(
			b.pluginHandler.Manager(), os.Getenv("ORI_REVIEWED_INTEGRATION_DEV_SOURCE"),
		)
		readers[specialist.SetupStepIntegrationInstall] = integrationAdapter
		if connectionStore, ok := b.workspaceStore.(interface {
			workspace.Store
			GetFolderPath(string) (string, error)
		}); ok {
			if b.pathSelectionStore == nil {
				b.pathSelectionStore = pathselection.NewStore()
			}
			userTemplates := configuredUserTemplateQuestLibrary{
				config:  b.configManager,
				catalog: templateRuntimeCatalog{capabilities: b.workspaceCapabilityRegistry, runtimes: b.runtimeCapabilityRegistry},
			}
			connectionService := projectconnection.NewService(connectionStore, b.pathSelectionStore)
			connectionService.SetGroupRequirementService(b.groupRequirements)
			connectionService.SetCreatedWorkspaceRecorder(b.allowlistLocallyCreatedWorkspace)
			projectAdapter = setupjourney.NewProjectConnectionAdapter(
				connectionService,
				installedProjectTemplateResolver{manager: b.pluginHandler.Manager(), userTemplates: userTemplates},
			)
			readers[specialist.SetupStepProjectConnect] = projectAdapter
		}
	} else {
		readers[specialist.SetupStepIntegrationInstall] = setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
			return setupjourney.CanonicalStepRead{BlockedReason: setupjourney.ReasonOwnerUnavailable}, nil
		})
	}
	if readers[specialist.SetupStepProjectConnect] == nil {
		readers[specialist.SetupStepProjectConnect] = setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
			return setupjourney.CanonicalStepRead{BlockedReason: setupjourney.ReasonOwnerUnavailable}, nil
		})
	}
	if b.workspaceStore != nil && b.st != nil {
		staffingAdapter = setupjourney.NewAssistantStaffingAdapter(
			b.workspaceStore, b.st, serverStaffingToolGrants{builder: b},
			b.systemModel, b.validateModel,
		)
		readers[specialist.SetupStepAssistantProgramStaffing] = staffingAdapter
		if b.sessionHandler != nil {
			b.sessionHandler.SetAssistantReviewedStaffer(staffingAdapter.StaffFromReviewedWorkspaceSetup)
			// Keep final project batches separate from one exact live workspace-role
			// route. Both callbacks adapt here so sessionhttp has no setupjourney
			// dependency, but only the route callback derives authority from one
			// Home/project target and requires exactly one fill.
			adaptRoleFills := func(fills []sessionhttp.RoleStaffingFill) []setupjourney.RoleFill {
				roles := make([]setupjourney.RoleFill, 0, len(fills))
				for _, fill := range fills {
					mode := setupjourney.StaffingModeCreate
					if fill.Mode == "assign" {
						mode = setupjourney.StaffingModeBind
					}
					roles = append(roles, setupjourney.RoleFill{
						RoleID: fill.RoleID, Mode: mode, Name: fill.Name,
						Provider: fill.Provider, Model: fill.Model,
					})
				}
				return roles
			}
			b.sessionHandler.SetAssistantRoleStaffer(
				func(ctx context.Context, workspaceID string, fills []sessionhttp.RoleStaffingFill) error {
					return staffingAdapter.StaffRolesFromReviewedWorkspaceSetup(ctx, workspaceID, adaptRoleFills(fills))
				},
			)
			b.sessionHandler.SetAssistantWorkspaceRoleStaffer(
				func(ctx context.Context, workspaceID string, fills []sessionhttp.RoleStaffingFill) error {
					return staffingAdapter.StaffRoleOnWorkspace(ctx, workspaceID, adaptRoleFills(fills))
				},
			)
			b.sessionHandler.SetAssistantRoleUnstaffer(staffingAdapter.UnstaffRoleFromWorkspace)
		}
	}
	registry, err := setupjourney.NewReaderRegistry(readers)
	if err != nil {
		panic("invalid built-in setup journey reader registry")
	}
	b.setupJourneyStore = setupjourney.NewSQLiteStore(b.sessionStore.DB())
	b.setupJourneyService, err = setupjourney.NewService(
		b.setupJourneyStore, b.personalAssistantStore, registry,
	)
	if err != nil {
		panic("invalid built-in setup journey service")
	}
	if integrationAdapter != nil {
		if err := b.setupJourneyService.SetActionAdapter(specialist.SetupStepIntegrationInstall, integrationAdapter); err != nil {
			panic("invalid built-in setup journey integration action adapter")
		}
	}
	if projectAdapter != nil {
		if err := b.setupJourneyService.SetActionAdapter(specialist.SetupStepProjectConnect, projectAdapter); err != nil {
			panic("invalid built-in setup journey project action adapter")
		}
	}
	if workspaceSetupAdapter != nil {
		if err := b.setupJourneyService.SetActionAdapter(specialist.SetupStepWorkspaceSetup, workspaceSetupAdapter); err != nil {
			panic("invalid built-in setup journey workspace setup adapter")
		}
	}
	if staffingAdapter != nil {
		if err := b.setupJourneyService.SetActionAdapter(specialist.SetupStepAssistantProgramStaffing, staffingAdapter); err != nil {
			panic("invalid built-in setup journey staffing adapter")
		}
	}
	if mailboxAdapter != nil {
		if err := b.setupJourneyService.SetActionAdapter(specialist.SetupStepAccountLink, mailboxAdapter); err != nil {
			panic("invalid built-in setup journey mailbox link adapter")
		}
	}
	// Host-compiled quests for built-in templates need no plugin or template
	// library, so they are always served.
	questCatalogs := []setupjourney.QuestCatalog{setupjourney.NewHostQuestCatalog(hostquests.All())}
	if b.pluginHandler != nil {
		// The generated install quests come before the plugin quests they hand
		// off into; both read the same installed plugin store.
		questCatalogs = append(questCatalogs,
			setupjourney.NewIntegrationInstallQuestCatalog(reviewedintegration.All, b.pluginHandler.Manager()),
			pluginQuestCatalog,
		)
	}
	if b.configManager != nil {
		// Without a plugin store no integration can be installed, so the catalog
		// lists no user-template quest (its lookups still resolve).
		var installedPlugins interface {
			List() ([]plugin.InstalledPlugin, error)
		}
		if b.pluginHandler != nil {
			installedPlugins = b.pluginHandler.Manager()
		}
		questCatalogs = append(questCatalogs, setupjourney.NewUserTemplateQuestCatalog(configuredUserTemplateQuestLibrary{
			config:  b.configManager,
			catalog: templateRuntimeCatalog{capabilities: b.workspaceCapabilityRegistry, runtimes: b.runtimeCapabilityRegistry},
		}, installedPlugins))
	}
	b.setupJourneyService.SetQuestCatalog(setupjourney.CombineQuestCatalogs(questCatalogs...))
	b.setupJourneyHandler = setupjourneyhttp.NewHandler(b.setupJourneyService, b.userProvider)
	if b.workspaceStore != nil {
		if b.pathSelectionStore == nil {
			b.pathSelectionStore = pathselection.NewStore()
		}
		sampleStore := samplelibrary.NewStore(b.sessionStore.DB())
		b.sampleLibraryService = samplelibrary.NewService(sampleStore, b.workspaceStore, b.pathSelectionStore)
		b.sampleLibraryHandler = samplelibraryhttp.New(b.sampleLibraryService)
		if b.sessionHandler != nil {
			b.sessionHandler.SetAssistantHomeRemoved(b.sampleLibraryService.OnHomeRemoved)
		}
		if b.workspaceCapabilityRegistry != nil {
			if err := b.workspaceCapabilityRegistry.BindRuntime(workspace.CapabilitySampleLibrary, samplelibrary.NewCapabilityRuntime(b.sampleLibraryService)); err != nil {
				panic("invalid sample library runtime binding")
			}
		}
	}
}

// emailOpsQuestReaders installs the Email Ops host quest's readers for the
// owners this build has and returns the mailbox link adapter when every
// dependency of the link exists. The dependencies are all wired in Phase 18
// (wireMailboxRuntime), before this runs in Phase 22.6.
func (b *ServerBuilder) emailOpsQuestReaders(readers map[specialist.SetupStepKind]setupjourney.CanonicalReader) *emailOpsMailboxLinkAdapter {
	if b.workspaceFileStore == nil {
		return nil
	}
	// Provenance lives only in the folder store; see emailOpsWorkspaceCreateReader.
	readers[specialist.SetupStepWorkspaceCreate] = emailOpsWorkspaceCreateReader{source: b.workspaceFileStore}
	if b.emailReadiness == nil || b.emailReadiness.connections == nil {
		return nil
	}
	readers[specialist.SetupStepAccountConnect] = emailOpsAccountConnectReader{
		readiness: b.emailReadiness, clientConfigured: defaultOAuthClientConfigured,
	}
	readers[specialist.SetupStepAccountLink] = emailOpsAccountLinkReader{
		readiness: b.emailReadiness, workspaces: b.workspaceFileStore,
	}
	if b.gmailSink == nil || b.mailboxLinker == nil {
		return nil
	}
	adapter := &emailOpsMailboxLinkAdapter{
		readiness: b.emailReadiness, resolver: b.workspaceFileStore, workspaces: b.workspaceFileStore,
		sink: b.gmailSink, linker: b.mailboxLinker,
	}
	if b.setupWizardService != nil {
		adapter.wizard = b.setupWizardService
	}
	return adapter
}

// systemModel returns the configured system provider and model, if any.
func (b *ServerBuilder) systemModel() (string, string) {
	if b == nil || b.configManager == nil {
		return "", ""
	}
	return b.configManager.GetSystemModel()
}

// validateModel reports whether a provider is registered and, when a model is
// named, whether that provider offers it. Staffing and the setup summary share
// this one check so "a model can run" means the same thing in both.
func (b *ServerBuilder) validateModel(providerName, modelName string) error {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	modelName = strings.TrimSpace(modelName)
	if providerName == "" || b == nil || b.llmFactory == nil {
		return fmt.Errorf("model provider is unavailable")
	}
	provider, err := b.llmFactory.GetProvider(providerName)
	if err != nil {
		return err
	}
	if modelName == "" {
		return nil
	}
	for _, available := range provider.DefaultModels() {
		if available == modelName {
			return nil
		}
	}
	return fmt.Errorf("model is unavailable")
}

// systemModelAvailable reports whether the configured system model can run.
func (b *ServerBuilder) systemModelAvailable() bool {
	provider, model := b.systemModel()
	return strings.TrimSpace(provider) != "" && b.validateModel(provider, model) == nil
}

type serverStaffingToolGrants struct {
	builder *ServerBuilder
}

func (g serverStaffingToolGrants) Available(skillName string) bool {
	if g.builder == nil || g.builder.skillsManager == nil {
		return false
	}
	_, found, err := g.builder.skillsManager.ResolveSkillByName(strings.TrimSpace(skillName))
	return err == nil && found
}

func (g serverStaffingToolGrants) Grant(agentName, skillName string) error {
	if !g.Available(skillName) {
		return fmt.Errorf("staffing tool grant is unavailable")
	}
	return g.builder.skillsManager.SetSkillEnabled(agentName, skillName, true)
}

func (g serverStaffingToolGrants) Revoke(agentName, skillName string) error {
	if g.builder == nil || g.builder.skillsManager == nil {
		return nil
	}
	return g.builder.skillsManager.ClearSkillState(agentName, skillName)
}

type workspaceProjectFileReadiness struct {
	store interface {
		workspace.Store
		GetFolderPath(string) (string, error)
	}
}

func (r workspaceProjectFileReadiness) FilesConnected(projectID string) bool {
	project, err := r.store.Get(projectID)
	if err != nil || project == nil {
		return false
	}
	if canonical, ok := r.store.(interface {
		GetFolderWorkspace(string) (*workspace.Workspace, error)
	}); ok {
		if current, currentErr := canonical.GetFolderWorkspace(projectID); currentErr == nil && current != nil {
			project = current
		}
	}
	root, err := r.store.GetFolderPath(projectID)
	if err != nil {
		return false
	}
	_, err = workspace.ResolveProjectEntry(project, root)
	return err == nil
}

type configuredUserTemplateQuestLibrary struct {
	config  *config.Manager
	catalog projecttemplates.RuntimeCatalog
}

func (l configuredUserTemplateQuestLibrary) ListUserSetupQuestTemplates(_ context.Context) ([]projecttemplates.Template, error) {
	if l.config == nil {
		return nil, errors.New("user template library is unavailable")
	}
	return projecttemplates.ListLibraryWithCatalog(resolveTemplatesRoot(l.config), l.catalog)
}

func (l configuredUserTemplateQuestLibrary) FindUserSetupQuestTemplate(_ context.Context, templateID string) (projecttemplates.Template, error) {
	if l.config == nil {
		return projecttemplates.Template{}, errors.New("user template library is unavailable")
	}
	return projecttemplates.FindLibraryTemplateWithCatalog(resolveTemplatesRoot(l.config), templateID, l.catalog)
}

func (l configuredUserTemplateQuestLibrary) WithUserSetupQuestMutationLock(_ context.Context, operation func() error) error {
	if l.config == nil {
		return errors.New("user template library is unavailable")
	}
	return projecttemplates.WithLibraryMutationLock(resolveTemplatesRoot(l.config), operation)
}

type installedProjectTemplateResolver struct {
	manager       *plugin.Manager
	userTemplates setupjourney.UserTemplateQuestLibrary
}

func (r installedProjectTemplateResolver) ResolveProjectTemplate(ctx context.Context, scope setupjourney.ReadScope) (projecttemplates.Template, error) {
	if scope.QuestSource == setupjourney.QuestSourceUserTemplate {
		if r.userTemplates == nil || scope.UserTemplateID == "" {
			return projecttemplates.Template{}, errors.New("project template owner is unavailable")
		}
		template, err := r.userTemplates.FindUserSetupQuestTemplate(ctx, scope.UserTemplateID)
		if err != nil || template.UserSetupQuest == nil || template.AssistantProgram == nil ||
			template.ID != scope.ExpectedBlueprintID || template.AssistantProgram.ID != scope.ExpectedAssistantProgramID {
			return projecttemplates.Template{}, errors.New("project template owner is unavailable")
		}
		return template, nil
	}
	if r.manager == nil || scope.IntegrationPluginID == "" || scope.IntegrationVersion == "" ||
		scope.ExpectedBlueprintID == "" || scope.ExpectedAssistantProgramID == "" {
		return projecttemplates.Template{}, errors.New("project template owner is unavailable")
	}
	installed, err := r.manager.List()
	if err != nil {
		return projecttemplates.Template{}, errors.New("project template owner is unavailable")
	}
	var found *projecttemplates.Template
	for _, candidate := range installed {
		if candidate.Name != scope.IntegrationPluginID || candidate.Version != scope.IntegrationVersion || !pluginBlueprintsActive(candidate) {
			continue
		}
		for _, resolved := range candidate.ResolvedBlueprints {
			template := resolved.Template
			if template.PluginOwner == nil || template.PluginOwner.BlueprintID != scope.ExpectedBlueprintID ||
				template.AssistantProgram == nil || template.AssistantProgram.ID != scope.ExpectedAssistantProgramID {
				continue
			}
			if found != nil {
				return projecttemplates.Template{}, errors.New("project template owner is ambiguous")
			}
			template.Path = resolved.SkeletonRoot
			template.HasSkeleton = true
			copy := template
			found = &copy
		}
	}
	if found == nil {
		return projecttemplates.Template{}, errors.New("project template owner is unavailable")
	}
	return *found, nil
}
