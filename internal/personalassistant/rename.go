package personalassistant

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/systemassistant"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

var assistantDisplayNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_\- ]+$`)

// AgentProfileStore is the narrow global profile boundary needed for an
// identity-preserving rename.
type AgentProfileStore interface {
	ListAgents() []string
	GetAgent(name string) (*agent.Agent, bool)
	RenameAgent(oldName, newName string) error
}

// RenameCoordinator journals and resumes the canonical rename steps: global
// profile, stable HQ instance, session metadata, then PAF presentation fields.
type AssistantSessionRenamer interface {
	RenameSessionsByAgent(ctx context.Context, oldName, newName string) (int, error)
}

type RenameCoordinator struct {
	continuity *ContinuityService
	profiles   AgentProfileStore
	workspaces workspace.Store
	sessions   AssistantSessionRenamer
}

func NewRenameCoordinator(continuity *ContinuityService, profiles AgentProfileStore, workspaces workspace.Store) *RenameCoordinator {
	return &RenameCoordinator{continuity: continuity, profiles: profiles, workspaces: workspaces}
}

func (c *RenameCoordinator) SetSessionRenamer(sessions AssistantSessionRenamer) {
	if c != nil {
		c.sessions = sessions
	}
}

func (c *RenameCoordinator) Rename(ctx context.Context, userID, newName string, ifVersion int64) (*Projection, error) {
	if c == nil || c.continuity == nil || c.profiles == nil || c.workspaces == nil || c.sessions == nil {
		return nil, errors.New("personal assistant: rename service is unavailable")
	}
	newName, err := validateAssistantRenameName(newName)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	state, err := c.continuity.store.GetState(ctx, strings.TrimSpace(userID))
	if err != nil {
		return nil, err
	}
	if state.StateVersion != ifVersion {
		return nil, fmt.Errorf("%w: expected state version %d", ErrConflict, ifVersion)
	}
	if state.Status == StatusAwaitingHQ || state.Status == StatusProvisioningHQ {
		return nil, ErrNeedsHQ
	}
	if state.Status != StatusActive && state.Status != StatusPaused {
		return nil, ErrRepairNeeded
	}

	if state.RenameStep == RenameNone {
		if state.GlobalAgentProfileName == newName {
			return c.continuity.project(ctx, state.UserID)
		}
		if err := c.validateNewRename(state, newName); err != nil {
			return nil, err
		}
		state.RenameFromName = state.GlobalAgentProfileName
		state.RenameToName = newName
		state.RenameStep = RenameProfilePending
		state, err = c.continuity.store.UpdateState(ctx, state, ifVersion)
		if err != nil {
			return nil, err
		}
	} else if state.RenameToName != newName {
		return nil, fmt.Errorf("%w: retry the durable rename to %q before starting another", ErrConflict, state.RenameToName)
	}

	fromName, toName := state.RenameFromName, state.RenameToName
	if state.RenameStep == RenameProfilePending {
		fromAgent, fromOK := c.profiles.GetAgent(fromName)
		_, toOK := c.profiles.GetAgent(toName)
		switch {
		case toOK && !fromOK:
			// A prior process completed the move before recording the next step.
		case fromOK && !toOK && fromAgent != nil:
			if err := c.profiles.RenameAgent(fromName, toName); err != nil {
				return nil, fmt.Errorf("personal assistant: rename profile: %w", err)
			}
		default:
			return nil, fmt.Errorf("%w: global profile rename state is ambiguous", ErrRepairNeeded)
		}
		state.RenameStep = RenameHQPending
		state, err = c.continuity.store.UpdateState(ctx, state, state.StateVersion)
		if err != nil {
			return nil, err
		}
	}

	if state.RenameStep == RenameHQPending {
		ws, getErr := c.workspaces.Get(state.HQWorkspaceID)
		if getErr != nil || ws == nil {
			return nil, fmt.Errorf("%w: Personal HQ cannot be loaded for rename", ErrRepairNeeded)
		}
		found := false
		changed := false
		for i := range ws.AgentInstances {
			instance := &ws.AgentInstances[i]
			if instance.ID != state.HQEntryAgentInstanceID || !instance.EntryPoint {
				continue
			}
			found = true
			switch instance.Name {
			case toName:
				// Prior save completed.
			case fromName:
				instance.Name = toName
				changed = true
			default:
				return nil, fmt.Errorf("%w: HQ entry identity changed during rename", ErrRepairNeeded)
			}
		}
		if !found {
			return nil, fmt.Errorf("%w: HQ entry identity is missing", ErrRepairNeeded)
		}
		if changed {
			if saveErr := c.workspaces.Save(ws); saveErr != nil {
				return nil, fmt.Errorf("personal assistant: save HQ rename: %w", saveErr)
			}
		}
		state.RenameStep = RenameSessionsPending
		state, err = c.continuity.store.UpdateState(ctx, state, state.StateVersion)
		if err != nil {
			return nil, err
		}
	}

	if state.RenameStep == RenameSessionsPending {
		if _, sessionErr := c.sessions.RenameSessionsByAgent(ctx, fromName, toName); sessionErr != nil {
			return nil, fmt.Errorf("personal assistant: preserve sessions during rename: %w", sessionErr)
		}
		state.RenameStep = RenameStatePending
		state, err = c.continuity.store.UpdateState(ctx, state, state.StateVersion)
		if err != nil {
			return nil, err
		}
	}

	if state.RenameStep != RenameStatePending {
		return nil, ErrRepairNeeded
	}
	state.DisplayName = toName
	state.GlobalAgentProfileName = toName
	state.RenameFromName = ""
	state.RenameToName = ""
	state.RenameStep = RenameNone
	if _, err := c.continuity.store.UpdateState(ctx, state, state.StateVersion); err != nil {
		return nil, err
	}
	return c.continuity.project(ctx, state.UserID)
}

func (c *RenameCoordinator) validateNewRename(state *State, newName string) error {
	for _, existing := range c.profiles.ListAgents() {
		if strings.EqualFold(existing, newName) && !strings.EqualFold(existing, state.GlobalAgentProfileName) {
			return fmt.Errorf("%w: an agent profile already uses that name", ErrConflict)
		}
	}
	ids, err := c.workspaces.List()
	if err != nil {
		return err
	}
	for _, id := range ids {
		ws, getErr := c.workspaces.Get(id)
		if getErr != nil || ws == nil {
			return fmt.Errorf("personal assistant: inspect attached agent profiles: %w", getErr)
		}
		for _, instance := range ws.AgentInstances {
			if !strings.EqualFold(instance.Name, state.GlobalAgentProfileName) {
				continue
			}
			if ws.ID != state.HQWorkspaceID || instance.ID != state.HQEntryAgentInstanceID {
				return fmt.Errorf("%w: the assistant profile is attached outside Personal HQ", ErrConflict)
			}
		}
	}
	return nil
}

func validateAssistantRenameName(value string) (string, error) {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "", errors.New("assistant name is required")
	}
	if len([]rune(value)) > MaxDisplayNameLen {
		return "", fmt.Errorf("assistant name is capped at %d characters", MaxDisplayNameLen)
	}
	if !assistantDisplayNamePattern.MatchString(value) {
		return "", errors.New("assistant name may use letters, numbers, spaces, underscores, and hyphens")
	}
	if strings.EqualFold(value, "Ori") || strings.EqualFold(value, systemassistant.CanonicalName) {
		return "", errors.New("the Ori name is reserved for the app guide")
	}
	return value, nil
}
