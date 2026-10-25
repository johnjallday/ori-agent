package workspacecapability

import (
	"fmt"
	"strings"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// CompanionProvisioner creates or reuses a capability's companion agent.
//
// The capability layer decides WHETHER a companion is wanted and what it should
// be called; actually creating an agent belongs to the layer that owns the
// agent store. Keeping that split means this package never grants a tool.
type CompanionProvisioner interface {
	// EnsureCompanionAgent attaches a companion agent with the given display
	// name to the workspace, returning the workspace agent-instance ID and
	// whether it was newly created.
	//
	// It must append rather than replace: adding a Curator may not displace the
	// workspace's manager or any existing agent (FR-38).
	EnsureCompanionAgent(workspaceID, displayName string) (instanceID string, created bool, err error)
}

// Companion error codes.
const (
	// CodeCompanionUnavailable means the capability declares no companion, or
	// no provisioner is wired.
	CodeCompanionUnavailable = "companion_unavailable"
	// CodeCapabilityNotInstalled means the capability is not installed on this
	// workspace, so there is nothing to add a companion to.
	CodeCapabilityNotInstalled = "capability_not_installed"
	// CodeCompanionFailed means the companion could not be created.
	CodeCompanionFailed = "companion_failed"
)

// CompanionResult describes the outcome of adding a companion.
type CompanionResult struct {
	// AgentInstanceID is the workspace agent instance now associated with the
	// capability.
	AgentInstanceID string `json:"agent_instance_id"`
	// DisplayName is the name the companion was given.
	DisplayName string `json:"display_name"`
	// AlreadyPresent reports that the capability already had a companion, so
	// nothing was created (FR-39).
	AlreadyPresent bool `json:"already_present"`
}

// SetCompanionProvisioner wires companion creation. Without it, AddCompanion
// reports the companion as unavailable rather than failing obscurely.
func (s *Service) SetCompanionProvisioner(provisioner CompanionProvisioner) {
	if s != nil {
		s.companions = provisioner
	}
}

// CompanionDisplayName returns the name a new companion should carry.
//
// It prefers a folder-appropriate name when the capability's runtime can say
// which folder is managed — a workspace tidying Downloads gets a "Downloads
// Curator" (FR-40). Before setup there is no folder to name, so the neutral
// default applies. The name is presentation only: nothing keys off it, which is
// exactly why renaming the agent later cannot break anything.
func (s *Service) CompanionDisplayName(def Definition, workspaceID string) string {
	fallback := "Curator"
	if def.Companion != nil && strings.TrimSpace(def.Companion.DefaultDisplayName) != "" {
		fallback = def.Companion.DefaultDisplayName
	}
	runtime, ok := s.registry.Runtime(def.ID)
	if !ok {
		return fallback
	}
	status, err := runtime.CapabilityStatus(workspaceID)
	if err != nil {
		return fallback
	}
	folder := strings.TrimSpace(status.FolderDisplayName)
	if folder == "" {
		return fallback
	}
	return folder + " Curator"
}

// AddCompanion adds the capability's optional companion agent to a workspace
// (FR-36, FR-39, FR-40).
//
// It is idempotent through the install record's companion ASSOCIATION, not
// through the agent's display name. Name matching would be wrong in both
// directions: a user who renamed their Curator would get a second one, and a
// pre-existing agent that happened to be called "Downloads Curator" would be
// silently adopted as this capability's companion and become deletable by an
// uninstall that never created it (PRD §9.5).
func (s *Service) AddCompanion(workspaceID, capabilityID string) (CompanionResult, error) {
	def, err := s.resolveInstallable(capabilityID)
	if err != nil {
		return CompanionResult{}, err
	}
	if def.Companion == nil {
		return CompanionResult{}, &Error{
			Code:    CodeCompanionUnavailable,
			Message: fmt.Sprintf("%s has no companion agent to add.", def.Display.Name),
		}
	}
	if s.companions == nil {
		return CompanionResult{}, &Error{
			Code:    CodeCompanionUnavailable,
			Message: "Adding an agent is not available right now.",
		}
	}

	ws, err := s.loadWorkspace(workspaceID)
	if err != nil {
		return CompanionResult{}, err
	}
	record, installed := ws.GetInstalledCapability(def.ID)
	if !installed {
		return CompanionResult{}, &Error{
			Code:    CodeCapabilityNotInstalled,
			Message: fmt.Sprintf("%s is not installed in this workspace.", def.Display.Name),
		}
	}

	// Already has one: return it rather than creating a second (FR-39).
	existing := record.ResourcesOfKind(workspace.ResourceCompanionAgent)
	if len(existing) == 1 && s.companionNameFor(ws, existing[0].ID) != "" {
		return CompanionResult{
			AgentInstanceID: existing[0].ID,
			DisplayName:     s.companionNameFor(ws, existing[0].ID),
			AlreadyPresent:  true,
		}, nil
	}
	for _, resource := range existing {
		if s.companionNameFor(ws, resource.ID) != "" {
			return CompanionResult{}, &Error{Code: CodeCompanionFailed, Message: "The companion association is ambiguous. Choose the intended agent in File Janitor settings. Filing still works without an agent."}
		}
	}

	displayName := s.CompanionDisplayName(def, workspaceID)
	instanceID, created, err := s.companions.EnsureCompanionAgent(workspaceID, displayName)
	if err != nil || strings.TrimSpace(instanceID) == "" {
		return CompanionResult{}, &Error{
			Code:    CodeCompanionFailed,
			Message: "The agent could not be added. File Janitor still works without one.",
			Err:     err,
		}
	}

	// Record the association so a later uninstall knows whether it may remove
	// this agent. An agent we did NOT create is shared: it existed for the
	// user's own reasons and must survive removal (FR-27).
	if updateErr := s.store.Update(workspaceID, func(w *workspace.Workspace) error {
		if !w.HasInstalledCapability(def.ID) {
			return fmt.Errorf("capability was removed while adding the companion")
		}
		for _, stale := range existing {
			w.ForgetCapabilityResource(def.ID, workspace.ResourceCompanionAgent, stale.ID)
		}
		w.RecordCapabilityResource(def.ID, workspace.CapabilityResource{
			Kind:   workspace.ResourceCompanionAgent,
			ID:     instanceID,
			Shared: !created,
		})
		return nil
	}); updateErr != nil {
		return CompanionResult{}, &Error{Code: CodeCompanionFailed,
			Message: "The agent was attached, but its companion association could not be saved. Choose that agent in File Janitor settings to retry; filing is unaffected.", Err: updateErr}
	}

	return CompanionResult{AgentInstanceID: instanceID, DisplayName: displayName}, nil
}

// companionNameFor resolves an associated instance's current display name, so a
// repeat request reports what the agent is actually called now rather than what
// it was called when it was created.
func (s *Service) companionNameFor(ws *workspace.Workspace, instanceID string) string {
	name := ""
	count := 0
	for _, instance := range ws.AgentInstances {
		if instance.ID == instanceID {
			name = instance.Name
			count++
		}
	}
	if count != 1 {
		return ""
	}
	return name
}

// AssociateCompanion is explicit recovery for a legacy or ambiguous association.
// It replaces only the association, never an attachment, definition or tool grant.
func (s *Service) AssociateCompanion(workspaceID, capabilityID, instanceID string) (CompanionResult, error) {
	def, err := s.resolveInstallable(capabilityID)
	if err != nil {
		return CompanionResult{}, err
	}
	if def.Companion == nil {
		return CompanionResult{}, &Error{Code: CodeCompanionUnavailable, Message: "This capability has no companion."}
	}
	var result CompanionResult
	err = s.store.Update(workspaceID, func(w *workspace.Workspace) error {
		record, ok := w.GetInstalledCapability(def.ID)
		if !ok {
			return &Error{Code: CodeCapabilityNotInstalled, Message: "The capability is no longer installed."}
		}
		name := s.companionNameFor(w, instanceID)
		if name == "" {
			return &Error{Code: CodeCompanionFailed, Message: "Choose one current workspace agent. The selected attachment is unavailable or ambiguous."}
		}
		for _, resource := range record.ResourcesOfKind(workspace.ResourceCompanionAgent) {
			w.ForgetCapabilityResource(def.ID, workspace.ResourceCompanionAgent, resource.ID)
		}
		// Recovery never acquires ownership of an existing attachment.
		w.RecordCapabilityResource(def.ID, workspace.CapabilityResource{Kind: workspace.ResourceCompanionAgent, ID: instanceID, Shared: true})
		result = CompanionResult{AgentInstanceID: instanceID, DisplayName: name}
		return nil
	})
	return result, err
}
