package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	assistantTopologyReviewTTL  = 10 * time.Minute
	assistantTopologyReceiptMax = 32
	AssistantTopologyDisconnect = "disconnect_project"
	AssistantTopologyRemoveHome = "remove_home"
)

var (
	ErrAssistantTopologyInvalid       = errors.New("assistant topology request is invalid")
	ErrAssistantTopologyConflict      = errors.New("assistant topology changed")
	ErrAssistantTopologyReviewExpired = errors.New("assistant topology review expired")
	ErrAssistantTopologyIdempotency   = errors.New("assistant topology idempotency conflict")
	assistantTopologyMu               sync.Mutex
)

type AssistantTopologyReviewReceipt struct {
	Token              string     `json:"token"`
	Action             string     `json:"action"`
	ProjectWorkspaceID string     `json:"project_workspace_id"`
	LinkID             string     `json:"link_id"`
	StateRevision      int64      `json:"state_revision"`
	LinkRevision       int64      `json:"link_revision"`
	InputDigest        string     `json:"input_digest"`
	ExpiresAt          time.Time  `json:"expires_at"`
	ConsumedAt         *time.Time `json:"consumed_at,omitempty"`
}

type AssistantTopologyOperationReceipt struct {
	IdempotencyKey     string    `json:"idempotency_key"`
	Action             string    `json:"action"`
	ProjectWorkspaceID string    `json:"project_workspace_id"`
	LinkID             string    `json:"link_id"`
	InputDigest        string    `json:"input_digest"`
	RecordedAt         time.Time `json:"recorded_at"`
}

type AssistantTopologyState struct {
	ReviewReceipts    []AssistantTopologyReviewReceipt    `json:"review_receipts,omitempty"`
	OperationReceipts []AssistantTopologyOperationReceipt `json:"operation_receipts,omitempty"`
}

type AssistantDisconnectReview struct {
	Token              string    `json:"token"`
	ExpiresAt          time.Time `json:"expires_at"`
	StationWorkspaceID string    `json:"station_workspace_id"`
	ProjectWorkspaceID string    `json:"project_workspace_id"`
	LinkID             string    `json:"link_id"`
	StateRevision      int64     `json:"state_revision"`
	LinkRevision       int64     `json:"link_revision"`
	Impact             []string  `json:"impact"`
}

type AssistantDisconnectReceipt struct {
	StationWorkspaceID string    `json:"station_workspace_id"`
	ProjectWorkspaceID string    `json:"project_workspace_id"`
	LinkID             string    `json:"link_id"`
	RecordedAt         time.Time `json:"recorded_at"`
	Replayed           bool      `json:"replayed,omitempty"`
}

type AssistantHomeRemovalReview struct {
	Token              string    `json:"token"`
	ExpiresAt          time.Time `json:"expires_at"`
	StationWorkspaceID string    `json:"station_workspace_id"`
	StateRevision      int64     `json:"state_revision"`
	LinkedProjectCount int       `json:"linked_project_count"`
	HomeRoleCount      int       `json:"home_role_count"`
	HasSampleLibrary   bool      `json:"has_sample_library"`
	Impact             []string  `json:"impact"`
}

type AssistantHomeRemovalReceipt struct {
	StationWorkspaceID string    `json:"station_workspace_id"`
	RetainedProjects   int       `json:"retained_projects"`
	RecordedAt         time.Time `json:"recorded_at"`
}

type assistantRetainedLink struct {
	projectID string
	parentID  string
	link      *AssistantProjectLink
}

func (service *AssistantProgramStore) ReviewDisconnect(stationID, projectID string, expectedStateRevision int64) (*AssistantDisconnectReview, error) {
	assistantTopologyMu.Lock()
	defer assistantTopologyMu.Unlock()
	station, project, state, link, err := service.disconnectResources(stationID, projectID)
	if err != nil {
		return nil, err
	}
	if expectedStateRevision < 0 || state.StateRevision != expectedStateRevision {
		return nil, ErrAssistantTopologyConflict
	}
	now := service.now().UTC()
	digest := assistantDisconnectDigest(station.ID, project.ID, link.ID, state.StateRevision, link.StateRevision)
	receipt := AssistantTopologyReviewReceipt{
		Token: uuid.NewString(), Action: AssistantTopologyDisconnect,
		ProjectWorkspaceID: project.ID, LinkID: link.ID, StateRevision: state.StateRevision,
		LinkRevision: link.StateRevision, InputDigest: digest, ExpiresAt: now.Add(assistantTopologyReviewTTL),
	}
	if err := service.store.Update(station.ID, func(current *Workspace) error {
		currentState := current.GetAssistantProgramState()
		if currentState == nil || currentState.StateRevision != expectedStateRevision {
			return ErrAssistantTopologyConflict
		}
		currentState.Topology.ReviewReceipts = appendBoundedTopologyReviews(currentState.Topology.ReviewReceipts, receipt, now)
		current.SetAssistantProgramState(currentState)
		return nil
	}); err != nil {
		return nil, ErrAssistantTopologyConflict
	}
	return &AssistantDisconnectReview{
		Token: receipt.Token, ExpiresAt: receipt.ExpiresAt, StationWorkspaceID: station.ID,
		ProjectWorkspaceID: project.ID, LinkID: link.ID, StateRevision: state.StateRevision,
		LinkRevision: link.StateRevision,
		Impact: []string{
			"The project will stop appearing in this Home's portfolio.",
			"Home handoffs to this project will stop.",
			"The project workspace, project team, tasks, files, and copied assets will be preserved.",
		},
	}, nil
}

func (service *AssistantProgramStore) CommitDisconnect(stationID, token, idempotencyKey string) (*AssistantDisconnectReceipt, error) {
	assistantTopologyMu.Lock()
	defer assistantTopologyMu.Unlock()
	token, idempotencyKey = strings.TrimSpace(token), strings.TrimSpace(idempotencyKey)
	if token == "" || idempotencyKey == "" || len(token) > 160 || len(idempotencyKey) > 160 {
		return nil, ErrAssistantTopologyInvalid
	}
	station, err := service.store.Get(strings.TrimSpace(stationID))
	if err != nil || station == nil {
		return nil, ErrAssistantStationNotFound
	}
	state := station.GetAssistantProgramState()
	if state == nil {
		return nil, ErrAssistantStationNotFound
	}
	for _, operation := range state.Topology.OperationReceipts {
		if operation.IdempotencyKey != idempotencyKey {
			continue
		}
		if operation.Action != AssistantTopologyDisconnect {
			return nil, ErrAssistantTopologyIdempotency
		}
		return &AssistantDisconnectReceipt{StationWorkspaceID: station.ID, ProjectWorkspaceID: operation.ProjectWorkspaceID, LinkID: operation.LinkID, RecordedAt: operation.RecordedAt, Replayed: true}, nil
	}
	var review *AssistantTopologyReviewReceipt
	for index := range state.Topology.ReviewReceipts {
		if state.Topology.ReviewReceipts[index].Token == token {
			copy := state.Topology.ReviewReceipts[index]
			review = &copy
			break
		}
	}
	now := service.now().UTC()
	if review == nil || review.Action != AssistantTopologyDisconnect || review.ConsumedAt != nil || !now.Before(review.ExpiresAt) {
		return nil, ErrAssistantTopologyReviewExpired
	}
	_, project, currentState, link, err := service.disconnectResources(station.ID, review.ProjectWorkspaceID)
	if err != nil || currentState.StateRevision != review.StateRevision || link.StateRevision != review.LinkRevision || link.ID != review.LinkID {
		return nil, ErrAssistantTopologyConflict
	}
	digest := assistantDisconnectDigest(station.ID, project.ID, link.ID, currentState.StateRevision, link.StateRevision)
	if digest != review.InputDigest {
		return nil, ErrAssistantTopologyConflict
	}
	originalParent := project.ParentID
	if err := service.store.Update(project.ID, func(current *Workspace) error {
		currentLink := current.GetAssistantProjectLink()
		if currentLink == nil || currentLink.ID != link.ID || currentLink.StateRevision != link.StateRevision {
			return ErrAssistantTopologyConflict
		}
		current.SetAssistantProjectLink(nil)
		current.ParentID = ""
		return nil
	}); err != nil {
		return nil, err
	}
	if err := service.moveDisconnectedProject(project.ID); err != nil {
		_ = service.store.Update(project.ID, func(current *Workspace) error {
			current.SetAssistantProjectLink(link)
			current.ParentID = originalParent
			return nil
		})
		return nil, err
	}
	operation := AssistantTopologyOperationReceipt{IdempotencyKey: idempotencyKey, Action: AssistantTopologyDisconnect, ProjectWorkspaceID: project.ID, LinkID: link.ID, InputDigest: digest, RecordedAt: now}
	if err := service.store.Update(station.ID, func(current *Workspace) error {
		currentState := current.GetAssistantProgramState()
		if currentState == nil || currentState.StateRevision != review.StateRevision {
			return ErrAssistantTopologyConflict
		}
		filtered := currentState.LinkedProjectIDs[:0]
		for _, id := range currentState.LinkedProjectIDs {
			if id != project.ID {
				filtered = append(filtered, id)
			}
		}
		currentState.LinkedProjectIDs = NormalizeAssistantProjectIDs(filtered)
		portfolio := currentState.Portfolio.Projects[:0]
		for _, item := range currentState.Portfolio.Projects {
			if item.LinkID != link.ID {
				portfolio = append(portfolio, item)
			}
		}
		currentState.Portfolio.Projects = portfolio
		for index := range currentState.Topology.ReviewReceipts {
			if currentState.Topology.ReviewReceipts[index].Token == token {
				consumed := now
				currentState.Topology.ReviewReceipts[index].ConsumedAt = &consumed
			}
		}
		currentState.Topology.OperationReceipts = appendBoundedTopologyOperations(currentState.Topology.OperationReceipts, operation)
		currentState.StateRevision++
		current.SetAssistantProgramState(currentState)
		return nil
	}); err != nil {
		_ = service.store.Update(project.ID, func(current *Workspace) error {
			current.SetAssistantProjectLink(link)
			current.ParentID = originalParent
			return nil
		})
		return nil, ErrAssistantTopologyConflict
	}
	return &AssistantDisconnectReceipt{StationWorkspaceID: station.ID, ProjectWorkspaceID: project.ID, LinkID: link.ID, RecordedAt: now}, nil
}

func (service *AssistantProgramStore) ReviewHomeRemoval(stationID string, expectedStateRevision int64) (*AssistantHomeRemovalReview, error) {
	assistantTopologyMu.Lock()
	defer assistantTopologyMu.Unlock()
	if service == nil || service.store == nil || expectedStateRevision < 0 {
		return nil, ErrAssistantTopologyInvalid
	}
	station, err := service.store.Get(strings.TrimSpace(stationID))
	if err != nil || station == nil {
		return nil, ErrAssistantStationNotFound
	}
	state := station.GetAssistantProgramState()
	if state == nil || state.StateRevision != expectedStateRevision {
		return nil, ErrAssistantTopologyConflict
	}
	now := service.now().UTC()
	digest := assistantHomeRemovalDigest(station.ID, state.StateRevision, state.LinkedProjectIDs)
	receipt := AssistantTopologyReviewReceipt{Token: uuid.NewString(), Action: AssistantTopologyRemoveHome, StateRevision: state.StateRevision, InputDigest: digest, ExpiresAt: now.Add(assistantTopologyReviewTTL)}
	if err := service.store.Update(station.ID, func(current *Workspace) error {
		currentState := current.GetAssistantProgramState()
		if currentState == nil || currentState.StateRevision != expectedStateRevision {
			return ErrAssistantTopologyConflict
		}
		currentState.Topology.ReviewReceipts = appendBoundedTopologyReviews(currentState.Topology.ReviewReceipts, receipt, now)
		current.SetAssistantProgramState(currentState)
		return nil
	}); err != nil {
		return nil, ErrAssistantTopologyConflict
	}
	return &AssistantHomeRemovalReview{
		Token: receipt.Token, ExpiresAt: receipt.ExpiresAt, StationWorkspaceID: station.ID,
		StateRevision: state.StateRevision, LinkedProjectCount: len(state.LinkedProjectIDs),
		HomeRoleCount: len(state.HomeBindings.Bindings), HasSampleLibrary: station.HasInstalledCapability(CapabilitySampleLibrary),
		Impact: []string{
			"The Home, its Home-scoped roles, portfolio rollup, and optional add-on state will be removed.",
			"Every linked project, project-scoped team, task, file, and confirmed copied asset will be preserved as a standalone workspace.",
			"External project and sample folders will not be moved, changed, or deleted.",
		},
	}, nil
}

func (service *AssistantProgramStore) CommitHomeRemoval(stationID, token string) (*AssistantHomeRemovalReceipt, error) {
	assistantTopologyMu.Lock()
	defer assistantTopologyMu.Unlock()
	token = strings.TrimSpace(token)
	if service == nil || service.store == nil || token == "" || len(token) > 160 {
		return nil, ErrAssistantTopologyInvalid
	}
	station, err := service.store.Get(strings.TrimSpace(stationID))
	if err != nil || station == nil {
		return nil, ErrAssistantStationNotFound
	}
	state := station.GetAssistantProgramState()
	if state == nil {
		return nil, ErrAssistantStationNotFound
	}
	var review *AssistantTopologyReviewReceipt
	for index := range state.Topology.ReviewReceipts {
		if state.Topology.ReviewReceipts[index].Token == token {
			copy := state.Topology.ReviewReceipts[index]
			review = &copy
			break
		}
	}
	now := service.now().UTC()
	if review == nil || review.Action != AssistantTopologyRemoveHome || review.ConsumedAt != nil || !now.Before(review.ExpiresAt) {
		return nil, ErrAssistantTopologyReviewExpired
	}
	if state.StateRevision != review.StateRevision || assistantHomeRemovalDigest(station.ID, state.StateRevision, state.LinkedProjectIDs) != review.InputDigest {
		return nil, ErrAssistantTopologyConflict
	}
	retained := make([]assistantRetainedLink, 0, len(state.LinkedProjectIDs))
	for _, projectID := range state.LinkedProjectIDs {
		project, getErr := service.store.Get(projectID)
		if getErr != nil || project == nil {
			return nil, ErrAssistantTopologyConflict
		}
		link := project.GetAssistantProjectLink()
		if link == nil || link.StationWorkspaceID != station.ID || link.Key.Normalize() != state.Key.Normalize() {
			return nil, ErrAssistantTopologyConflict
		}
		retained = append(retained, assistantRetainedLink{projectID: project.ID, parentID: project.ParentID, link: link})
	}
	for index, item := range retained {
		if updateErr := service.store.Update(item.projectID, func(current *Workspace) error {
			current.SetAssistantProjectLink(nil)
			current.ParentID = ""
			return nil
		}); updateErr != nil {
			service.restoreRemovedHomeProjects(station.ID, retained[:index])
			return nil, updateErr
		}
		if moveErr := service.moveDisconnectedProject(item.projectID); moveErr != nil {
			service.restoreRemovedHomeProjects(station.ID, retained[:index+1])
			return nil, moveErr
		}
	}
	station.AssistantProgramState = nil
	if err := service.store.Save(station); err != nil {
		service.restoreRemovedHomeProjects(station.ID, retained)
		return nil, err
	}
	if err := service.store.Delete(station.ID); err != nil {
		station.SetAssistantProgramState(state)
		_ = service.store.Save(station)
		service.restoreRemovedHomeProjects(station.ID, retained)
		return nil, err
	}
	return &AssistantHomeRemovalReceipt{StationWorkspaceID: station.ID, RetainedProjects: len(retained), RecordedAt: now}, nil
}

func (service *AssistantProgramStore) restoreRemovedHomeProjects(stationID string, retained []assistantRetainedLink) {
	for _, item := range retained {
		_ = service.store.Update(item.projectID, func(current *Workspace) error {
			current.SetAssistantProjectLink(item.link)
			current.ParentID = item.parentID
			return nil
		})
		if item.parentID == stationID {
			type workspaceMover interface {
				MoveWorkspaceFolder(string, string) ([]MovedWorkspace, error)
			}
			if mover, ok := service.store.(workspaceMover); ok {
				_, _ = mover.MoveWorkspaceFolder(item.projectID, stationID)
			}
		}
	}
}

func assistantHomeRemovalDigest(stationID string, revision int64, projectIDs []string) string {
	raw := strings.Join(append([]string{AssistantTopologyRemoveHome, stationID, time.Unix(revision, 0).UTC().Format(time.RFC3339Nano)}, NormalizeAssistantProjectIDs(projectIDs)...), "\x00")
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

func (service *AssistantProgramStore) disconnectResources(stationID, projectID string) (*Workspace, *Workspace, *AssistantProgramState, *AssistantProjectLink, error) {
	if service == nil || service.store == nil {
		return nil, nil, nil, nil, ErrAssistantTopologyInvalid
	}
	station, err := service.store.Get(strings.TrimSpace(stationID))
	if err != nil || station == nil {
		return nil, nil, nil, nil, ErrAssistantStationNotFound
	}
	state := station.GetAssistantProgramState()
	if state == nil {
		return nil, nil, nil, nil, ErrAssistantStationNotFound
	}
	project, err := service.store.Get(strings.TrimSpace(projectID))
	if err != nil || project == nil {
		return nil, nil, nil, nil, ErrAssistantTopologyConflict
	}
	link := project.GetAssistantProjectLink()
	if link == nil || link.StationWorkspaceID != station.ID || link.Key.Normalize() != state.Key.Normalize() || link.ID != AssistantProjectLinkID(station.ID, project.ID) {
		return nil, nil, nil, nil, ErrAssistantTopologyConflict
	}
	found := false
	for _, id := range state.LinkedProjectIDs {
		found = found || id == project.ID
	}
	if !found {
		return nil, nil, nil, nil, ErrAssistantTopologyConflict
	}
	return station, project, state, link, nil
}

func (service *AssistantProgramStore) moveDisconnectedProject(projectID string) error {
	type workspaceMover interface {
		MoveWorkspaceFolder(string, string) ([]MovedWorkspace, error)
	}
	if mover, ok := service.store.(workspaceMover); ok {
		_, err := mover.MoveWorkspaceFolder(projectID, "")
		return err
	}
	return nil
}

func assistantDisconnectDigest(stationID, projectID, linkID string, stateRevision, linkRevision int64) string {
	raw := strings.Join([]string{AssistantTopologyDisconnect, stationID, projectID, linkID, time.Unix(stateRevision, linkRevision).UTC().Format(time.RFC3339Nano)}, "\x00")
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

func appendBoundedTopologyReviews(values []AssistantTopologyReviewReceipt, value AssistantTopologyReviewReceipt, now time.Time) []AssistantTopologyReviewReceipt {
	out := make([]AssistantTopologyReviewReceipt, 0, assistantTopologyReceiptMax)
	for _, existing := range values {
		if existing.ConsumedAt == nil && now.Before(existing.ExpiresAt) && existing.Token != value.Token {
			out = append(out, existing)
		}
	}
	out = append(out, value)
	if len(out) > assistantTopologyReceiptMax {
		out = out[len(out)-assistantTopologyReceiptMax:]
	}
	return out
}

func appendBoundedTopologyOperations(values []AssistantTopologyOperationReceipt, value AssistantTopologyOperationReceipt) []AssistantTopologyOperationReceipt {
	out := append(append([]AssistantTopologyOperationReceipt(nil), values...), value)
	if len(out) > assistantTopologyReceiptMax {
		out = out[len(out)-assistantTopologyReceiptMax:]
	}
	return out
}
