package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/pathselection"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectconnection"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/setupjourney"
	"github.com/johnjallday/ori-agent/internal/setupjourneyhttp"
	"github.com/johnjallday/ori-agent/internal/specialist"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

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
		specialist.SetupStepSummary,
	} {
		readers[kind] = setupjourney.CanonicalReaderFunc(func(context.Context, setupjourney.ReadScope) (setupjourney.CanonicalStepRead, error) {
			return setupjourney.CanonicalStepRead{BlockedReason: setupjourney.ReasonOwnerUnavailable}, nil
		})
	}
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
		readers[specialist.SetupStepIntegrationInstall] = setupjourney.NewReviewedIntegrationAdapter(b.pluginHandler.Manager())
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
	if b.pluginHandler != nil {
		adapter := setupjourney.NewReviewedIntegrationAdapter(b.pluginHandler.Manager())
		if err := b.setupJourneyService.SetActionAdapter(specialist.SetupStepIntegrationInstall, adapter); err != nil {
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
