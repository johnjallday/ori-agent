package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projectlibrary"
	"github.com/johnjallday/ori-agent/internal/skills"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
	"github.com/johnjallday/ori-agent/internal/workspacearrangement"
)

func (b *ServerBuilder) wireLibraryArrangementPlanning() {
	if b.sessionHandler == nil {
		return
	}
	b.sessionHandler.SetArrangementContext(func(ctx context.Context, owner, homeID string) (projectlibrary.ArrangementPlanningContext, error) {
		library := projectlibrary.NewStore(b.workspaceStore).WithProviderEvidence(b.sessionHandler.AssistantLibraryProviderEvidence)
		return library.ReadArrangementPlanningContext(ctx, owner, homeID, b.resolveArrangementRuntime)
	})
	b.sessionHandler.SetArrangementPlanning(func(ctx context.Context, in projectlibrary.ArrangementPlanningRequest) (projectlibrary.ArrangementPlanningResult, error) {
		if b.server == nil || b.server.Core == nil || b.server.Core.ArrangementPlanner == nil {
			return projectlibrary.ArrangementPlanningResult{}, projectlibrary.ErrUnavailable
		}
		library := projectlibrary.NewStore(b.workspaceStore).WithProviderEvidence(b.sessionHandler.AssistantLibraryProviderEvidence)
		return library.PlanArrangement(ctx, b.server.Core.ArrangementPlanner, b.resolveArrangementRuntime, in)
	})
}

// arrangementSkillBinding reads access without resolving/materializing the
// ordinary agent runtime. An explicit Toolbox supersedes legacy access; broken
// pins, learned-skill substitutions and ambiguous bindings never fall back.
func arrangementSkillBinding(home *workspace.Workspace, instanceID, name string) (workspace.SkillBinding, error) {
	fail := workspace.SkillBinding{}
	key := workspace.NormalizeToolboxCapabilityID(name)
	if home == nil || instanceID == "" || key == "" {
		return fail, projectlibrary.ErrUnavailable
	}
	var matches []workspace.SkillBinding
	for _, binding := range home.GetSkillBindings() {
		if workspace.NormalizeToolboxCapabilityID(binding.SkillName) == key {
			matches = append(matches, binding)
		}
	}
	if len(matches) != 1 || !matches[0].Enabled || !matches[0].Trusted || len(matches[0].Config) != 0 {
		return fail, projectlibrary.ErrUnavailable
	}
	binding := matches[0]
	definition, recipe, assigned, err := home.ResolveAssignedToolbox(instanceID)
	if err != nil {
		return fail, projectlibrary.ErrUnavailable
	}
	if assigned {
		if definition.Status != workspace.ToolboxStatusActive {
			return fail, projectlibrary.ErrUnavailable
		}
		count := 0
		for _, ref := range recipe.Skills {
			if workspace.NormalizeToolboxCapabilityID(ref.CapabilityID) == key {
				if ref.Source != workspace.ToolboxSourceWorkspaceProvided || !strings.EqualFold(strings.TrimSpace(ref.BindingID), binding.ID) {
					return fail, projectlibrary.ErrUnavailable
				}
				count++
			}
		}
		if count != 1 {
			return fail, projectlibrary.ErrUnavailable
		}
	} else if access, ok := home.GetAgentSkillAccess(instanceID); ok {
		allowed := false
		for _, id := range access.EnabledBindingIDs {
			allowed = allowed || strings.EqualFold(strings.TrimSpace(id), binding.ID)
		}
		if !allowed {
			return fail, projectlibrary.ErrUnavailable
		}
	}
	return binding, nil
}

// resolveArrangementRuntime is deliberately NOT ResolveAgentForWorkspace: that
// resolver can fall back to global agents and materialize native MCP bindings.
// This resolver only reads the exact local Manager and pinned installed package.
func (b *ServerBuilder) resolveArrangementRuntime(ctx context.Context, authority projectlibrary.ManagerAuthority) (projectlibrary.ArrangementRuntime, error) {
	type capture struct {
		provider                       llm.Provider
		model, guidance, pin, skillPin string
	}
	read := func(ctx context.Context) (capture, error) {
		fail := capture{}
		if err := ctx.Err(); err != nil {
			return fail, err
		}
		if b == nil || b.workspaceStore == nil || b.sessionHandler == nil || b.pluginHandler == nil || b.skillsManager == nil || b.llmFactory == nil {
			return fail, projectlibrary.ErrUnavailable
		}
		library := projectlibrary.NewStore(b.workspaceStore).WithProviderEvidence(b.sessionHandler.AssistantLibraryProviderEvidence)
		if !library.CanReadAsManager(authority) {
			return fail, projectlibrary.ErrUnavailable
		}
		home, err := b.workspaceStore.Get(authority.HomeID)
		if err != nil || home == nil {
			return fail, projectlibrary.ErrUnavailable
		}
		state := home.GetAssistantProgramState()
		if state == nil {
			return fail, projectlibrary.ErrUnavailable
		}
		owner := state.HomeProvider
		if owner == nil && state.GroupTemplate != nil {
			owner = state.GroupTemplate.ProgramHomeOwner
		}
		installed, err := b.pluginHandler.Manager().List()
		if err != nil || !plugin.IndependentHomeProviderEvidenceAvailable(installed, owner) {
			return fail, projectlibrary.ErrUnavailable
		}
		packaged, declaration, ok := plugin.PinnedHomeDeclaration(installed, owner)
		if !ok || !packaged.Enabled {
			return fail, projectlibrary.ErrUnavailable
		}
		var instance *workspace.AgentInstance
		for _, candidate := range home.GetAgentInstances() {
			if candidate.ID == authority.AgentInstanceID {
				copy := candidate
				instance = &copy
			}
		}
		if instance == nil || instance.Name != authority.AgentName {
			return fail, projectlibrary.ErrUnavailable
		}
		var role *workspace.AssistantProgramRoleSpec
		for _, candidate := range declaration.AssistantProgram().Roles {
			if candidate.ID == instance.RoleID && candidate.Primary && candidate.Required && candidate.Scope == workspace.AssistantRoleScopeHome {
				copy := candidate
				role = &copy
			}
		}
		if role == nil {
			return fail, projectlibrary.ErrUnavailable
		}
		local, found, err := b.workspaceStore.GetWorkspaceAgent(home.ID, authority.AgentName)
		if err != nil || !found || local == nil || local.Status == types.AgentStatusDisabled {
			return fail, projectlibrary.ErrUnavailable
		}
		model := strings.TrimSpace(local.Settings.Model)
		providerName := libraryManagerProviderName(local.Settings.Provider, model, b.llmFactory)
		if model == "" || providerName == "" {
			return fail, projectlibrary.ErrNoManagerModel
		}
		provider, err := b.llmFactory.GetProvider(providerName)
		if err != nil || !workspacearrangement.SupportsMetadataPlanning(provider) {
			return fail, projectlibrary.ErrNoManagerModel
		}
		pointer := reflect.ValueOf(provider)
		if pointer.Kind() != reflect.Pointer || pointer.IsNil() {
			return fail, projectlibrary.ErrNoManagerModel
		}
		guidance := role.SystemPrompt
		type skillPin struct {
			Binding                            workspace.SkillBinding
			Name, Plugin, Path, Digest, Prompt string
		}
		var pins []skillPin
		seen := map[string]bool{}
		for _, name := range role.Skills {
			key := workspace.NormalizeToolboxCapabilityID(name)
			if seen[key] {
				return fail, projectlibrary.ErrUnavailable
			}
			seen[key] = true
			binding, err := arrangementSkillBinding(home, instance.ID, name)
			if err != nil {
				return fail, err
			}
			skill, found, err := b.skillsManager.ResolvePluginSkillByName(name)
			if err != nil || !found || skill == nil || skill.Source != skills.SourcePlugin || skill.Plugin != packaged.Name || len(skill.ValidationErrors) != 0 {
				return fail, projectlibrary.ErrUnavailable
			}
			digest, err := plugin.SkillTreeDigest(filepath.Dir(skill.Path))
			if err != nil {
				return fail, projectlibrary.ErrUnavailable
			}
			again, found, err := b.skillsManager.ResolvePluginSkillByName(name)
			if err != nil || !found || !reflect.DeepEqual(skill, again) {
				return fail, workspacearrangement.ErrStale
			}
			pins = append(pins, skillPin{Binding: binding, Name: skill.Name, Plugin: skill.Plugin, Path: skill.Path, Digest: digest, Prompt: skill.Prompt})
			guidance += "\n\n" + skill.Prompt
		}
		if len(guidance) > 16<<10 {
			return fail, workspacearrangement.ErrLimit
		}
		guidanceRaw, err := json.Marshal(struct {
			Owner       *workspace.AssistantProgramHomeOwner
			Role        *workspace.AssistantProgramRoleSpec
			Skills      []skillPin
			Generation  uint64
			InstalledAt time.Time
		}{owner, role, pins, packaged.Generation, packaged.InstalledAt})
		if err != nil {
			return fail, projectlibrary.ErrUnavailable
		}
		skillHash := sha256.Sum256(guidanceRaw)
		access, _ := home.GetAgentSkillAccess(instance.ID)
		assignment, _ := home.GetToolboxAssignment(instance.ID)
		raw, err := json.Marshal(struct {
			Settings   types.Settings
			ConfigID   string
			Instance   *workspace.AgentInstance
			Bindings   workspace.AssistantRoleBindingSet
			Access     *workspace.AgentSkillAccess
			Assignment *workspace.AgentToolboxAssignment
			Provider   string
			Pointer    uintptr
			Guidance   string
		}{local.Settings, local.WorkspaceLocalConfigID, instance, state.HomeBindings, access, assignment, providerName, pointer.Pointer(), hex.EncodeToString(skillHash[:])})
		if err != nil {
			return fail, projectlibrary.ErrUnavailable
		}
		hash := sha256.Sum256(raw)
		if !library.CanReadAsManager(authority) {
			return fail, workspacearrangement.ErrStale
		}
		return capture{provider: provider, model: model, guidance: guidance, pin: hex.EncodeToString(hash[:]), skillPin: hex.EncodeToString(skillHash[:])}, ctx.Err()
	}
	initial, err := read(ctx)
	if err != nil {
		return projectlibrary.ArrangementRuntime{}, err
	}
	return projectlibrary.ArrangementRuntime{Provider: initial.provider, Model: initial.model, Guidance: initial.guidance, ProviderFingerprint: initial.pin, SkillDigest: initial.skillPin, Revalidate: func(ctx context.Context) error {
		current, err := read(ctx)
		if err != nil {
			return err
		}
		if current.pin != initial.pin || current.skillPin != initial.skillPin || current.model != initial.model || current.guidance != initial.guidance {
			return workspacearrangement.ErrStale
		}
		return nil
	}}, nil
}
