package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/samplelibrary"
	"github.com/johnjallday/ori-agent/internal/samplelibraryhttp"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/setupjourneyhttp"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

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
	readers := make(map[specialist.SetupStepKind]setupjourney.CanonicalReader, specialist.SetupJourneyRequiredSteps)
	for _, kind := range []specialist.SetupStepKind{
		specialist.SetupStepAssistantProgramStaffing,
	} {
		readers[kind] = setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
			return setupjourney.CanonicalStepRead{BlockedReason: setupjourney.ReasonOwnerUnavailable}, nil
		})
	}
	readers[specialist.SetupStepSummary] = setupjourney.CanonicalReaderFunc(readSetupSummary)
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
			projectAdapter = setupjourney.NewProjectConnectionAdapter(
				projectconnection.NewService(connectionStore, b.pathSelectionStore),
				installedProjectTemplateResolver{manager: b.pluginHandler.Manager()},
			)
			projectAdapter.CheckPrerequisites = func(ctx context.Context, template projecttemplates.Template) (bool, error) {
				if template.RuntimeRequirements == nil || b.runtimeCapabilityRegistry == nil {
					return false, errors.New("runtime prerequisites unavailable")
				}
				checked := false
				for _, requirement := range template.RuntimeRequirements.Requirements {
					adapter, ok := b.runtimeCapabilityRegistry.Lookup(requirement.Adapter)
					if !ok {
						return false, errors.New("runtime prerequisites unavailable")
					}
					checker, ok := adapter.(interface {
						CheckPrerequisites(context.Context) (bool, error)
					})
					if !ok {
						return false, errors.New("runtime prerequisites unavailable")
					}
					ready, err := checker.CheckPrerequisites(ctx)
					if err != nil || !ready {
						return false, err
					}
					checked = true
				}
				return checked, nil
			}
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
			func() (string, string) {
				if b.configManager == nil {
					return "", ""
				}
				return b.configManager.GetSystemModel()
			},
			func(providerName, modelName string) error {
				providerName = strings.ToLower(strings.TrimSpace(providerName))
				modelName = strings.TrimSpace(modelName)
				if providerName == "" || b.llmFactory == nil {
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
			},
		)
		readers[specialist.SetupStepAssistantProgramStaffing] = staffingAdapter
		if b.sessionHandler != nil {
			b.sessionHandler.SetAssistantReviewedStaffer(staffingAdapter.StaffFromReviewedWorkspaceSetup)
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

type installedProjectTemplateResolver struct {
	manager *plugin.Manager
}

func (r installedProjectTemplateResolver) ResolveProjectTemplate(_ context.Context, scope setupjourney.ReadScope) (projecttemplates.Template, error) {
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
