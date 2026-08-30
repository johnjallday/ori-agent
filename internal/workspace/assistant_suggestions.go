package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrAssistantSuggestionUnavailable = errors.New("assistant suggestions are unavailable at the current stage")
	ErrAssistantSuggestionNotFound    = errors.New("assistant suggestion not found")
	assistantSuggestionMutationMu     sync.Mutex
)

type AssistantSuggestionService struct {
	store     Store
	learnings *AssistantLearningStore
	now       func() time.Time
}

func NewAssistantSuggestionService(store Store, learnings *AssistantLearningStore) *AssistantSuggestionService {
	return &AssistantSuggestionService{store: store, learnings: learnings, now: func() time.Time { return time.Now().UTC() }}
}

func assistantCollaboratorStage(state *AssistantProgramState) bool {
	if state == nil || state.Declaration == nil || !state.Hired || !state.PluginAvailable {
		return false
	}
	for index, stage := range state.Declaration.Stages {
		if stage.ID == state.StageID {
			return index > 0
		}
	}
	return false
}

func (service *AssistantSuggestionService) stationAndProject(stationID, projectID string) (*AssistantProgramState, *Workspace, error) {
	if service == nil || service.store == nil || service.learnings == nil {
		return nil, nil, ErrAssistantSuggestionUnavailable
	}
	station, err := service.store.Get(stationID)
	if err != nil {
		return nil, nil, err
	}
	state := station.GetAssistantProgramState()
	if !assistantCollaboratorStage(state) {
		return nil, nil, ErrAssistantSuggestionUnavailable
	}
	project, err := service.store.Get(projectID)
	if err != nil {
		return nil, nil, err
	}
	link := project.GetAssistantProjectLink()
	if link == nil || link.StationWorkspaceID != station.ID || link.Key.Normalize() != state.Key.Normalize() {
		return nil, nil, ErrAssistantProgramUnavailable
	}
	return state, project, nil
}

// Generate creates inert suggestions only from current approved learning
// revisions, then upserts each into the generic Action Center opportunity
// store. It never calls a model, writes project files, or creates a task.
func (service *AssistantSuggestionService) Generate(stationID, projectID string, expectedVersion int64) (AssistantLearningDocument, error) {
	state, project, err := service.stationAndProject(stationID, projectID)
	if err != nil {
		return AssistantLearningDocument{}, err
	}
	document, err := service.learnings.Update(stationID, expectedVersion, func(document *AssistantLearningDocument) error {
		known := make(map[string]struct{}, len(document.Suggestions))
		for _, suggestion := range document.Suggestions {
			known[suggestion.Fingerprint] = struct{}{}
		}
		created := 0
		for _, learning := range CurrentAssistantLearnings(*document) {
			if created >= 3 {
				break
			}
			revision, ok := learning.Current()
			if !ok {
				continue
			}
			projects := make(map[string]struct{})
			for _, evidence := range revision.Evidence {
				projects[evidence.ProjectID] = struct{}{}
			}
			if len(projects) < state.Declaration.Reflection.MinimumProjects {
				continue
			}
			hash := sha256.Sum256([]byte(project.ID + "\x00" + revision.ID))
			fingerprint := hex.EncodeToString(hash[:])
			if _, duplicate := known[fingerprint]; duplicate {
				continue
			}
			text := fmt.Sprintf("Consider this approved cross-project pattern for %s: %s", project.Name, revision.Text)
			document.Suggestions = append(document.Suggestions, AssistantSuggestion{
				ID: "suggestion-" + uuid.NewString(), Version: 1, Fingerprint: fingerprint,
				ProjectID: project.ID, LearningID: learning.ID, LearningRevisionID: revision.ID,
				Text: text, Rationale: "This recommendation comes from an approved learning supported by multiple linked projects.",
				Evidence: cloneEvidence(revision.Evidence), CreatedAt: service.now(),
			})
			known[fingerprint] = struct{}{}
			created++
		}
		return nil
	})
	if err != nil {
		return AssistantLearningDocument{}, err
	}
	return service.ensureActionCenterOpportunities(stationID, state, project, document)
}

func (service *AssistantSuggestionService) ensureActionCenterOpportunities(stationID string, state *AssistantProgramState, project *Workspace, document AssistantLearningDocument) (AssistantLearningDocument, error) {
	opportunities := NewOpportunityStore(service.store)
	resolved := make(map[string]string)
	for _, suggestion := range document.Suggestions {
		if suggestion.ProjectID != project.ID || suggestion.OpportunityID != "" || suggestion.DismissedAt != nil {
			continue
		}
		opportunity, _, err := opportunities.Upsert(Opportunity{
			WorkspaceID: project.ID, SourceRunID: suggestion.ID,
			SourceType: OpportunitySourceAssistantSuggestion, SourceID: suggestion.ID,
			SourceLabel: state.PrimaryName, SourceURL: "/workspaces/" + project.FolderSlug + "/assistant",
			Title: suggestion.Text, Summary: suggestion.Rationale,
			Evidence: assistantSuggestionEvidenceSummary(suggestion),
			Priority: "medium", Confidence: "high", Status: OpportunityNew,
			RecommendedAction: "Review this cross-project recommendation and add it to Backlog only if it fits the current project.",
		})
		if err != nil {
			return AssistantLearningDocument{}, err
		}
		resolved[suggestion.ID] = opportunity.ID
	}
	if len(resolved) == 0 {
		return document, nil
	}
	return service.learnings.Update(stationID, document.Version, func(current *AssistantLearningDocument) error {
		for index := range current.Suggestions {
			if opportunityID := resolved[current.Suggestions[index].ID]; opportunityID != "" && current.Suggestions[index].OpportunityID == "" {
				current.Suggestions[index].OpportunityID = opportunityID
				current.Suggestions[index].Version++
			}
		}
		return nil
	})
}

func findAssistantSuggestion(document AssistantLearningDocument, suggestionID string) (AssistantSuggestion, bool) {
	for _, suggestion := range document.Suggestions {
		if suggestion.ID == suggestionID {
			return suggestion, true
		}
	}
	return AssistantSuggestion{}, false
}

func (service *AssistantSuggestionService) Accept(stationID, suggestionID string, expectedVersion int64) (AssistantSuggestion, error) {
	assistantSuggestionMutationMu.Lock()
	defer assistantSuggestionMutationMu.Unlock()

	document, err := service.learnings.Read(stationID)
	if err != nil {
		return AssistantSuggestion{}, err
	}
	suggestion, ok := findAssistantSuggestion(document, suggestionID)
	if !ok || suggestion.DismissedAt != nil || suggestion.OpportunityID == "" {
		return AssistantSuggestion{}, ErrAssistantSuggestionNotFound
	}
	if suggestion.AcceptedAt == nil && document.Version != expectedVersion {
		return AssistantSuggestion{}, ErrAssistantLearningConflict
	}
	state, project, err := service.stationAndProject(stationID, suggestion.ProjectID)
	if err != nil {
		return AssistantSuggestion{}, err
	}
	opportunities := NewOpportunityStore(service.store)
	opportunity, err := opportunities.Get(project.ID, suggestion.OpportunityID)
	if err != nil {
		return AssistantSuggestion{}, err
	}
	if opportunity.Status == OpportunityDismissed {
		return AssistantSuggestion{}, ErrAssistantSuggestionNotFound
	}
	taskID := opportunity.LinkedTaskID
	if opportunity.Status != OpportunityPlanned || taskID == "" {
		task, createErr := NewBacklogService(service.store).Create(BacklogCreateInput{
			WorkspaceID: project.ID, Description: opportunity.Title,
			Details:  opportunity.Summary + "\n\nEvidence:\n" + opportunity.Evidence + "\n\nRecommended action:\n" + opportunity.RecommendedAction,
			Priority: 3, SourceType: BacklogSourceActionCenter, SourceID: opportunity.ID,
		})
		if createErr != nil {
			return AssistantSuggestion{}, createErr
		}
		taskID = task.ID
		if err := opportunities.MarkPlanned(project.ID, opportunity.ID, taskID, project.ID); err != nil {
			return AssistantSuggestion{}, err
		}
	}
	if err := service.attachSuggestionTaskPolicy(project.ID, taskID, state, suggestion); err != nil {
		return AssistantSuggestion{}, err
	}
	if suggestion.AcceptedAt == nil || suggestion.TaskID != taskID {
		document, err = service.learnings.Update(stationID, expectedVersion, func(current *AssistantLearningDocument) error {
			for index := range current.Suggestions {
				candidate := &current.Suggestions[index]
				if candidate.ID != suggestionID {
					continue
				}
				if candidate.AcceptedAt == nil {
					now := service.now()
					candidate.AcceptedAt = &now
				}
				candidate.TaskID = taskID
				candidate.Version++
				return nil
			}
			return ErrAssistantSuggestionNotFound
		})
		if err != nil {
			return AssistantSuggestion{}, err
		}
		suggestion, _ = findAssistantSuggestion(document, suggestionID)
	}
	return suggestion, nil
}

func (service *AssistantSuggestionService) attachSuggestionTaskPolicy(projectID, taskID string, state *AssistantProgramState, suggestion AssistantSuggestion) error {
	return service.store.Update(projectID, func(current *Workspace) error {
		return current.MutateTask(taskID, func(task *Task) error {
			if task.Context == nil {
				task.Context = make(map[string]any)
			}
			evidenceIDs := make([]string, 0, len(suggestion.Evidence))
			for _, evidence := range suggestion.Evidence {
				evidenceIDs = append(evidenceIDs, evidence.SourceID)
			}
			task.Context["assistant_suggestion_id"] = suggestion.ID
			task.Context["assistant_learning_id"] = suggestion.LearningID
			task.Context["evidence_source_ids"] = evidenceIDs
			task.Context["requires_confirmation"] = true
			if capabilities := state.Declaration.SuggestionRequiredCapabilities; len(capabilities) > 0 {
				task.Context["required_capabilities"] = append([]string(nil), capabilities...)
			}
			return nil
		})
	})
}

func (service *AssistantSuggestionService) Dismiss(stationID, suggestionID string, expectedVersion int64) error {
	assistantSuggestionMutationMu.Lock()
	defer assistantSuggestionMutationMu.Unlock()

	document, err := service.learnings.Read(stationID)
	if err != nil {
		return err
	}
	suggestion, ok := findAssistantSuggestion(document, suggestionID)
	if !ok || suggestion.AcceptedAt != nil || suggestion.OpportunityID == "" {
		return ErrAssistantSuggestionNotFound
	}
	if document.Version != expectedVersion {
		return ErrAssistantLearningConflict
	}
	if err := NewOpportunityStore(service.store).Dismiss(suggestion.ProjectID, suggestion.OpportunityID, DismissalNotUseful); err != nil {
		return err
	}
	_, err = service.learnings.Update(stationID, expectedVersion, func(current *AssistantLearningDocument) error {
		for index := range current.Suggestions {
			candidate := &current.Suggestions[index]
			if candidate.ID != suggestionID {
				continue
			}
			if candidate.DismissedAt == nil {
				now := service.now()
				candidate.DismissedAt = &now
				candidate.Version++
			}
			return nil
		}
		return ErrAssistantSuggestionNotFound
	})
	return err
}

func (service *AssistantSuggestionService) ListForProject(stationID, projectID string) ([]AssistantSuggestion, error) {
	if _, _, err := service.stationAndProject(stationID, projectID); err != nil {
		return nil, err
	}
	document, err := service.learnings.Read(stationID)
	if err != nil {
		return nil, err
	}
	out := make([]AssistantSuggestion, 0)
	for _, suggestion := range document.Suggestions {
		if suggestion.ProjectID == projectID {
			out = append(out, suggestion)
		}
	}
	return out, nil
}

func assistantSuggestionEvidenceSummary(suggestion AssistantSuggestion) string {
	values := make([]string, 0, len(suggestion.Evidence))
	for _, evidence := range suggestion.Evidence {
		values = append(values, strings.TrimSpace(evidence.Summary))
	}
	return strings.Join(values, "\n")
}
