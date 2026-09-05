package samplelibrary

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type QuestionDelegation struct {
	MessageID       string `json:"message_id"`
	HomeWorkspaceID string `json:"home_workspace_id"`
	FromRoleID      string `json:"from_role_id"`
	ToRoleID        string `json:"to_role_id"`
	Recorded        bool   `json:"recorded"`
	Replayed        bool   `json:"replayed,omitempty"`
}

// DelegateQuestion records one bounded same-Home question. It starts no task or
// model execution and grants neither participant a capability action.
func (s *Service) DelegateQuestion(_ context.Context, homeID, question, idempotency string) (QuestionDelegation, error) {
	if err := s.authorizeHome(homeID); err != nil {
		return QuestionDelegation{}, err
	}
	question = sanitize(question, 1000)
	idempotency = strings.TrimSpace(idempotency)
	if question == "" || idempotency == "" || len(idempotency) > 160 {
		return QuestionDelegation{}, ErrOperationFailed
	}
	home, err := s.workspaces.Get(homeID)
	if err != nil {
		return QuestionDelegation{}, ErrOperationFailed
	}
	state := home.GetAssistantProgramState()
	if state == nil || state.Declaration == nil {
		return QuestionDelegation{}, ErrOperationFailed
	}
	var fromRole, toRole workspace.AssistantProgramRoleSpec
	for _, role := range state.Declaration.Roles {
		if role.Scope != workspace.AssistantRoleScopeHome {
			continue
		}
		if role.Required && role.Primary {
			fromRole = role
		}
		if !role.Required && workspace.NormalizeCapabilityID(role.CapabilityID) == workspace.CapabilitySampleLibrary {
			toRole = role
		}
	}
	bindings := map[string]workspace.AssistantRoleBinding{}
	for _, binding := range state.HomeBindings.Bindings {
		bindings[binding.RoleID] = binding
	}
	from, fromOK := bindings[fromRole.ID]
	to, toOK := bindings[toRole.ID]
	if fromRole.ID == "" || toRole.ID == "" || !fromOK || !toOK || from.AgentName == "" || to.AgentName == "" {
		return QuestionDelegation{}, ErrOperationFailed
	}
	messageID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(strings.Join([]string{homeID, idempotency, from.RoleID, to.RoleID}, "\x00"))).String()
	replayed := false
	err = s.workspaces.Update(homeID, func(current *workspace.Workspace) error {
		currentState := current.GetAssistantProgramState()
		if currentState == nil || currentState.HomeBindings.StateRevision != state.HomeBindings.StateRevision {
			return ErrRevisionConflict
		}
		for _, message := range current.Messages {
			if message.ID == messageID {
				replayed = true
				if message.Content != question || message.From != from.AgentName || message.To != to.AgentName {
					return ErrIdempotencyConflict
				}
				return nil
			}
		}
		return current.AddMessage(workspace.AgentMessage{ID: messageID, From: from.AgentName, To: to.AgentName, Type: workspace.MessageQuestion, Content: question, Metadata: map[string]any{"purpose": "catalog_question"}})
	})
	if err != nil {
		return QuestionDelegation{}, err
	}
	return QuestionDelegation{MessageID: messageID, HomeWorkspaceID: homeID, FromRoleID: fromRole.ID, ToRoleID: toRole.ID, Recorded: true, Replayed: replayed}, nil
}
