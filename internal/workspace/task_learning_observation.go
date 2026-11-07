package workspace

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

const (
	TaskContextLearningStart       = "learning_reviewed_start"
	TaskContextLearningObservation = "learning_observation_binding"
)

// LearningObservationBinding is correlation only. Local learning authority must
// separately verify the exact owner/scope/grant/start/Run; imports strip it.
type LearningObservationBinding struct {
	OwnerID     string `json:"owner_id"`
	OperationID string `json:"operation_id"`
	StartToken  string `json:"start_token"`
}

func LearningReviewedStart(task Task) string {
	token, _ := task.Context[TaskContextLearningStart].(string)
	if _, err := uuid.Parse(token); err != nil {
		return ""
	}
	return token
}

func LearningObservation(task Task) (LearningObservationBinding, bool) {
	var binding LearningObservationBinding
	data, err := json.Marshal(task.Context[TaskContextLearningObservation])
	if err != nil || len(data) > 2048 || json.Unmarshal(data, &binding) != nil || len(binding.OwnerID) == 0 || len(binding.OwnerID) > 128 ||
		len(binding.OperationID) == 0 || len(binding.OperationID) > 128 || binding.StartToken == "" || binding.StartToken != LearningReviewedStart(task) {
		return LearningObservationBinding{}, false
	}
	return binding, true
}

// BindLearningObservation cannot refresh an approval or change the work. It is
// called only after the winning owner-facing reviewed Start creates its token.
func (s *TicketService) BindLearningObservation(workspaceID, ticketID string, binding LearningObservationBinding) error {
	return s.store.Update(workspaceID, func(ws *Workspace) error {
		return ws.MutateTask(ticketID, func(task *Task) error {
			if FirstTaskRecipe(task) != LearningDraftRecipe || binding.StartToken == "" || binding.StartToken != LearningReviewedStart(*task) || task.Context[TaskContextReviewedExecutor] == nil {
				return fmt.Errorf("reviewed start changed")
			}
			task.Context[TaskContextLearningObservation] = binding
			return nil
		})
	})
}
