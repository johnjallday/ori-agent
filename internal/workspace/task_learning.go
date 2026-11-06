package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	LearningDraftRecipe    = "learning_draft"
	TaskContextLearning    = "learning_mission_binding"
	LearningDraftMaxTokens = 1200
)

// LearningTicketBinding is correlation, not permission. A portable marker or
// an actor label cannot stand in for the learning owner's local operation.
type LearningTicketBinding struct {
	OwnerID     string `json:"owner_id"`
	InstanceID  string `json:"instance_id"`
	OperationID string `json:"operation_id"`
	Epoch       int64  `json:"epoch"`
	InputDigest string `json:"input_digest"`
}

func LearningBinding(task Task) (LearningTicketBinding, bool) {
	var binding LearningTicketBinding
	raw, ok := task.Context[TaskContextLearning]
	if !ok {
		return binding, false
	}
	data, err := json.Marshal(raw)
	if err != nil || len(data) > 2048 || json.Unmarshal(data, &binding) != nil ||
		binding.OwnerID == "" || binding.InstanceID == "" || binding.OperationID == "" || binding.Epoch < 1 || len(binding.InputDigest) != 64 {
		return LearningTicketBinding{}, false
	}
	return binding, true
}

// ReviewLearningExecutor records only the exact executor/text disclosure. It
// starts nothing, grants no tools, and must be called by the owner HTTP action.
func (h *LLMTaskHandler) ReviewLearningExecutor(ctx context.Context, workspaceID, ticketID, assignee, expected string) error {
	if h == nil || h.workspaceStore == nil || expected == "" {
		return fmt.Errorf("review the current Ticket executor and input")
	}
	if err := RequireWorkspaceExecution(ctx, h.workspaceStore, workspaceID, false); err != nil {
		return err
	}
	// Resolve outside Update: runtime resolution can read the same store.
	ws, err := h.workspaceStore.Get(workspaceID)
	if err != nil {
		return err
	}
	task, err := ws.GetTask(ticketID)
	if err != nil {
		return err
	}
	if FirstTaskRecipe(task) != LearningDraftRecipe || (assignee != "" && assignee != task.To) || h.RecipeReadiness(*task) != "" {
		return fmt.Errorf("review the current Ticket executor and input")
	}
	chosen, blocked := h.resolveRecipeProvider(task.To, *task, LearningDraftRecipe)
	if blocked != nil {
		return blocked
	}
	if recipeExecutor(*task, task.To, chosen).Digest != expected {
		return fmt.Errorf("execution changed")
	}
	return h.workspaceStore.Update(workspaceID, func(current *Workspace) error {
		return current.MutateTask(ticketID, func(saved *Task) error {
			if recipeExecutor(*saved, saved.To, chosen).Digest != expected {
				return fmt.Errorf("execution changed")
			}
			saved.Context[TaskContextReviewedExecutor] = expected
			return nil
		})
	})
}

// consumeLearningReview prevents schedulers, retries and copied payloads from
// reusing a prior execution approval. A failed/unknown call needs a fresh Start.
func (h *LLMTaskHandler) consumeLearningReview(task Task, chosen recipeProvider) error {
	if h.workspaceStore == nil {
		return fmt.Errorf("canonical review unavailable")
	}
	expected := recipeExecutor(task, task.To, chosen).Digest
	return h.workspaceStore.Update(task.WorkspaceID, func(ws *Workspace) error {
		return ws.MutateTask(task.ID, func(saved *Task) error {
			if saved.Context[TaskContextReviewedExecutor] != expected || recipeExecutor(*saved, saved.To, chosen).Digest != expected {
				return fmt.Errorf("execution changed")
			}
			delete(saved.Context, TaskContextReviewedExecutor)
			return nil
		})
	})
}

type LearningDraftInput struct {
	TicketID    string
	WorkspaceID string
	Title       string
	Input       string
	Assignee    string
	Binding     LearningTicketBinding
}

// CreateLearningDraft is a compiled-owner seam, never a public create input.
// The learning owner reserves TicketID before calling it; unknown outcomes are
// reconciled by ID rather than invoking this method to recreate missing work.
func (s *TicketService) CreateLearningDraft(input LearningDraftInput) (*Ticket, error) {
	input.Input = strings.TrimSpace(input.Input)
	sum := sha256.Sum256([]byte(input.Input))
	if input.Binding.InputDigest != hex.EncodeToString(sum[:]) {
		return nil, invalidTicketField("learning", "reviewed input changed")
	}
	if strings.TrimSpace(input.TicketID) == "" || len(input.Input) > 4096 || strings.TrimSpace(input.Input) == "" ||
		input.Binding.OwnerID == "" || input.Binding.InstanceID == "" || input.Binding.OperationID == "" || input.Binding.Epoch < 1 || len(input.Binding.InputDigest) != 64 {
		return nil, invalidTicketField("learning", "invalid reviewed draft")
	}
	create := TicketCreateInput{WorkspaceID: input.WorkspaceID, State: TicketStateReady, Title: input.Title,
		Description: input.Input, Assignee: input.Assignee, Source: TicketSourceManual,
		SourceID: "learning:" + input.Binding.OperationID, Actor: TicketActorUser}
	task, err := buildTicketRecord(input.WorkspaceID, TicketStateReady, create)
	if err != nil {
		return nil, err
	}
	task.ID = input.TicketID
	var persisted Task
	created := false
	err = s.store.Update(input.WorkspaceID, func(ws *Workspace) error {
		owner := ws.OwnerUserID
		if owner == "" {
			owner = "local" // legacy single-owner workspaces, not arbitrary callers
		}
		if owner != input.Binding.OwnerID {
			return ErrTicketNotFound
		}
		if previous, err := ws.GetTask(input.TicketID); err == nil {
			binding, ok := LearningBinding(*previous)
			if !ok || binding != input.Binding || FirstTaskRecipe(previous) != LearningDraftRecipe {
				return ErrTicketSourceConflict
			}
			persisted = *previous
			return nil
		}
		// No different ID may already carry this operation's source key.
		for _, previous := range ws.Tasks {
			if previous.SourceID == create.SourceID {
				return ErrTicketSourceConflict
			}
		}
		task.TicketNumber = allocateTicketNumber(ws)
		task.StateRank = nextTicketRank(ws, TicketStateReady)
		if err := assignCreatedTicket(ws, &task, create); err != nil {
			return err
		}
		if _, err := ws.AddRecipeTask(task, LearningDraftRecipe, 1); err != nil {
			return err
		}
		if err := ws.MutateTask(task.ID, func(saved *Task) error {
			saved.Context[TaskContextLearning] = input.Binding
			saved.AwaitingExecutionIntent = true
			persisted = *saved
			return nil
		}); err != nil {
			return fmt.Errorf("record reviewed draft binding: %w", err)
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	ticket := NewTicket(&persisted, input.WorkspaceID, s.WorkspaceName(input.WorkspaceID), s.WorkspaceSlug(input.WorkspaceID))
	if created {
		s.publishTicket(EventTicketCreated, ticket, nil)
		s.publishLegacyTaskEvent(EventTaskCreated, persisted)
	}
	return &ticket, nil
}
