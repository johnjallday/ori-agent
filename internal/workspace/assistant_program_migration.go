package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	assistantMigrationReviewTTL = 10 * time.Minute
	assistantMigrationMax       = 16
)

var (
	ErrAssistantMigrationNotRequired = errors.New("assistant program migration is not required")
	ErrAssistantMigrationAmbiguous   = errors.New("assistant program migration is ambiguous")
	ErrAssistantMigrationConflict    = errors.New("assistant program migration state changed")
	ErrAssistantMigrationExpired     = errors.New("assistant program migration review expired")
	assistantMigrationMu             sync.Mutex
)

type AssistantMigrationReviewReceipt struct {
	Token         string     `json:"token"`
	InputDigest   string     `json:"input_digest"`
	StateRevision int64      `json:"state_revision"`
	ProjectCount  int        `json:"project_count"`
	TargetSchema  int        `json:"target_schema"`
	ExpiresAt     time.Time  `json:"expires_at"`
	ConsumedAt    *time.Time `json:"consumed_at,omitempty"`
}

type AssistantMigrationOperationReceipt struct {
	Token         string    `json:"token"`
	InputDigest   string    `json:"input_digest"`
	ProjectCount  int       `json:"project_count"`
	TargetSchema  int       `json:"target_schema"`
	StateRevision int64     `json:"state_revision"`
	RecordedAt    time.Time `json:"recorded_at"`
}

type AssistantMigrationState struct {
	ReviewReceipts    []AssistantMigrationReviewReceipt    `json:"review_receipts,omitempty"`
	OperationReceipts []AssistantMigrationOperationReceipt `json:"operation_receipts,omitempty"`
}

type AssistantLegacyMigrationReview struct {
	Token               string    `json:"token"`
	ExpiresAt           time.Time `json:"expires_at"`
	StationWorkspaceID  string    `json:"station_workspace_id"`
	StateRevision       int64     `json:"state_revision"`
	TargetSchema        int       `json:"target_schema"`
	LegacyRosterCount   int       `json:"legacy_roster_count"`
	ProjectWorkspaceIDs []string  `json:"project_workspace_ids"`
	Impact              []string  `json:"impact"`
}

type AssistantLegacyMigrationReceipt struct {
	StationWorkspaceID string    `json:"station_workspace_id"`
	StateRevision      int64     `json:"state_revision"`
	MigratedProjects   int       `json:"migrated_projects"`
	LegacyRosterCount  int       `json:"legacy_roster_count"`
	RecordedAt         time.Time `json:"recorded_at"`
	Replayed           bool      `json:"replayed,omitempty"`
}

func (service *AssistantProgramStore) ReviewLegacyMigration(stationID string, expectedStateRevision int64) (*AssistantLegacyMigrationReview, error) {
	assistantMigrationMu.Lock()
	defer assistantMigrationMu.Unlock()
	station, state, declaration, projects, digest, err := service.legacyMigrationResources(stationID)
	if err != nil {
		return nil, err
	}
	if state.StateRevision != expectedStateRevision {
		return nil, ErrAssistantMigrationConflict
	}
	now := service.now().UTC()
	receipt := AssistantMigrationReviewReceipt{Token: uuid.NewString(), InputDigest: digest, StateRevision: state.StateRevision, ProjectCount: len(projects), TargetSchema: declaration.SchemaVersion, ExpiresAt: now.Add(assistantMigrationReviewTTL)}
	if err := service.store.Update(station.ID, func(current *Workspace) error {
		currentState := current.GetAssistantProgramState()
		if currentState == nil || currentState.StateRevision != expectedStateRevision || currentState.SchemaVersion != AssistantProgramLegacyStateSchemaVersion {
			return ErrAssistantMigrationConflict
		}
		currentState.Migration.ReviewReceipts = appendAssistantMigrationReview(currentState.Migration.ReviewReceipts, receipt, now)
		current.SetAssistantProgramState(currentState)
		return nil
	}); err != nil {
		return nil, ErrAssistantMigrationConflict
	}
	ids := make([]string, 0, len(projects))
	for _, project := range projects {
		ids = append(ids, project.ID)
	}
	return &AssistantLegacyMigrationReview{
		Token: receipt.Token, ExpiresAt: receipt.ExpiresAt, StationWorkspaceID: station.ID,
		StateRevision: state.StateRevision, TargetSchema: declaration.SchemaVersion,
		LegacyRosterCount: len(state.Roster), ProjectWorkspaceIDs: NormalizeAssistantProjectIDs(ids),
		Impact: []string{
			"The existing shared roster will remain readable as legacy state and will not be renamed, cloned, reassigned, or deleted.",
			"Home and project role bindings will start empty and remain separate until each staffing action is reviewed.",
			"Only exact linked managed-workspace projections may be nested under Home; external project folders will not move.",
		},
	}, nil
}

func (service *AssistantProgramStore) CommitLegacyMigration(stationID, token string) (*AssistantLegacyMigrationReceipt, error) {
	assistantMigrationMu.Lock()
	defer assistantMigrationMu.Unlock()
	token = strings.TrimSpace(token)
	if token == "" || len(token) > 160 || service == nil || service.store == nil {
		return nil, ErrAssistantMigrationConflict
	}
	if current, getErr := service.store.Get(strings.TrimSpace(stationID)); getErr == nil && current != nil {
		currentState := current.GetAssistantProgramState()
		if currentState != nil && currentState.SchemaVersion >= AssistantProgramStateSchemaVersion {
			for _, operation := range currentState.Migration.OperationReceipts {
				if operation.Token == token {
					return &AssistantLegacyMigrationReceipt{StationWorkspaceID: current.ID, StateRevision: operation.StateRevision, MigratedProjects: operation.ProjectCount, LegacyRosterCount: len(currentState.Roster), RecordedAt: operation.RecordedAt, Replayed: true}, nil
				}
			}
			return nil, ErrAssistantMigrationNotRequired
		}
	}
	station, state, declaration, projects, digest, err := service.legacyMigrationResources(stationID)
	if err != nil {
		return nil, err
	}
	for _, operation := range state.Migration.OperationReceipts {
		if operation.InputDigest == digest && operation.TargetSchema == declaration.SchemaVersion {
			return &AssistantLegacyMigrationReceipt{StationWorkspaceID: station.ID, StateRevision: operation.StateRevision, MigratedProjects: operation.ProjectCount, LegacyRosterCount: len(state.Roster), RecordedAt: operation.RecordedAt, Replayed: true}, nil
		}
	}
	var review *AssistantMigrationReviewReceipt
	for index := range state.Migration.ReviewReceipts {
		if state.Migration.ReviewReceipts[index].Token == token {
			copy := state.Migration.ReviewReceipts[index]
			review = &copy
			break
		}
	}
	now := service.now().UTC()
	if review == nil || review.ConsumedAt != nil || !now.Before(review.ExpiresAt) {
		return nil, ErrAssistantMigrationExpired
	}
	if review.StateRevision != state.StateRevision || review.InputDigest != digest || review.ProjectCount != len(projects) || review.TargetSchema != declaration.SchemaVersion {
		return nil, ErrAssistantMigrationConflict
	}
	type migratedLink struct {
		projectID string
		legacy    *AssistantProjectLink
	}
	migrated := make([]migratedLink, 0, len(projects))
	rollbackLinks := func() {
		for _, item := range migrated {
			_ = service.store.Update(item.projectID, func(current *Workspace) error {
				current.SetAssistantProjectLink(item.legacy)
				return nil
			})
		}
	}
	for _, project := range projects {
		legacy := project.GetAssistantProjectLink()
		if err := service.store.Update(project.ID, func(current *Workspace) error {
			link := current.GetAssistantProjectLink()
			if link == nil || link.SchemaVersion != AssistantProjectLinkLegacySchemaVersion || link.StationWorkspaceID != station.ID || link.Key.Normalize() != state.Key.Normalize() {
				return ErrAssistantMigrationConflict
			}
			link.SchemaVersion = AssistantProjectLinkSchemaVersion
			link.DeclarationVersion = declaration.SchemaVersion
			link.ProjectBindings = AssistantRoleBindingSet{}
			link.StateRevision++
			current.SetAssistantProjectLink(link)
			return nil
		}); err != nil {
			rollbackLinks()
			return nil, ErrAssistantMigrationConflict
		}
		migrated = append(migrated, migratedLink{projectID: project.ID, legacy: legacy})
	}
	operation := AssistantMigrationOperationReceipt{Token: token, InputDigest: digest, ProjectCount: len(projects), TargetSchema: declaration.SchemaVersion, StateRevision: state.StateRevision + 1, RecordedAt: now}
	if err := service.store.Update(station.ID, func(current *Workspace) error {
		currentState := current.GetAssistantProgramState()
		if currentState == nil || currentState.StateRevision != state.StateRevision || currentState.SchemaVersion != AssistantProgramLegacyStateSchemaVersion {
			return ErrAssistantMigrationConflict
		}
		current.Kind = "group"
		currentState.SchemaVersion = AssistantProgramStateSchemaVersion
		currentState.Declaration = CloneAssistantProgramDeclaration(declaration)
		currentState.HomeBindings = AssistantRoleBindingSet{}
		for index := range currentState.Migration.ReviewReceipts {
			if currentState.Migration.ReviewReceipts[index].Token == token {
				consumed := now
				currentState.Migration.ReviewReceipts[index].ConsumedAt = &consumed
			}
		}
		currentState.Migration.OperationReceipts = appendAssistantMigrationOperation(currentState.Migration.OperationReceipts, operation)
		currentState.StateRevision++
		current.SetAssistantProgramState(currentState)
		return nil
	}); err != nil {
		rollbackLinks()
		return nil, ErrAssistantMigrationConflict
	}
	for _, project := range projects {
		locator, locatorErr := GetProjectEntryLocator(project.SharedData)
		if locatorErr == nil && locator != nil && locator.Kind == ProjectEntryManagedWorkspace {
			_ = service.ensureProjectNesting(project.ID, station.ID)
		}
	}
	return &AssistantLegacyMigrationReceipt{StationWorkspaceID: station.ID, StateRevision: operation.StateRevision, MigratedProjects: len(projects), LegacyRosterCount: len(state.Roster), RecordedAt: now}, nil
}

func (service *AssistantProgramStore) legacyMigrationResources(stationID string) (*Workspace, *AssistantProgramState, *AssistantProgramDeclaration, []*Workspace, string, error) {
	if service == nil || service.store == nil {
		return nil, nil, nil, nil, "", ErrAssistantMigrationConflict
	}
	station, err := service.store.Get(strings.TrimSpace(stationID))
	if err != nil || station == nil {
		return nil, nil, nil, nil, "", ErrAssistantStationNotFound
	}
	state := station.GetAssistantProgramState()
	if state == nil {
		return nil, nil, nil, nil, "", ErrAssistantStationNotFound
	}
	if state.SchemaVersion >= AssistantProgramStateSchemaVersion {
		return nil, nil, nil, nil, "", ErrAssistantMigrationNotRequired
	}
	projects := make([]*Workspace, 0, len(state.LinkedProjectIDs))
	var target *AssistantProgramDeclaration
	var targetJSON []byte
	for _, projectID := range NormalizeAssistantProjectIDs(state.LinkedProjectIDs) {
		project, getErr := service.store.Get(projectID)
		if getErr != nil || project == nil {
			return nil, nil, nil, nil, "", ErrAssistantMigrationAmbiguous
		}
		link := project.GetAssistantProjectLink()
		provenance := project.GetTemplateProvenance()
		if link == nil || link.ID != AssistantProjectLinkID(station.ID, project.ID) || link.SchemaVersion != AssistantProjectLinkLegacySchemaVersion || link.StationWorkspaceID != station.ID || link.Key.Normalize() != state.Key.Normalize() || provenance == nil || provenance.PluginOwner == nil || provenance.AssistantProgram == nil || provenance.AssistantProgram.SchemaVersion < AssistantProgramSchemaVersion || !strings.EqualFold(provenance.PluginOwner.PluginID, state.Key.PluginID) || provenance.AssistantProgram.ID != state.Key.ProgramID {
			return nil, nil, nil, nil, "", ErrAssistantMigrationAmbiguous
		}
		encoded, marshalErr := json.Marshal(provenance.AssistantProgram)
		if marshalErr != nil {
			return nil, nil, nil, nil, "", ErrAssistantMigrationAmbiguous
		}
		if target == nil {
			target = provenance.AssistantProgram
			targetJSON = encoded
		} else if string(targetJSON) != string(encoded) {
			return nil, nil, nil, nil, "", ErrAssistantMigrationAmbiguous
		}
		projects = append(projects, project)
	}
	if target == nil || len(projects) == 0 {
		return nil, nil, nil, nil, "", ErrAssistantMigrationAmbiguous
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(station.ID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(targetJSON)
	for _, project := range projects {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(project.ID))
		link := project.GetAssistantProjectLink()
		_, _ = hash.Write([]byte(time.Unix(link.StateRevision, 0).UTC().Format(time.RFC3339Nano)))
	}
	return station, state, target, projects, hex.EncodeToString(hash.Sum(nil)), nil
}

func appendAssistantMigrationReview(values []AssistantMigrationReviewReceipt, value AssistantMigrationReviewReceipt, now time.Time) []AssistantMigrationReviewReceipt {
	out := make([]AssistantMigrationReviewReceipt, 0, assistantMigrationMax)
	for _, existing := range values {
		if existing.ConsumedAt == nil && now.Before(existing.ExpiresAt) {
			out = append(out, existing)
		}
	}
	out = append(out, value)
	if len(out) > assistantMigrationMax {
		out = out[len(out)-assistantMigrationMax:]
	}
	return out
}

func appendAssistantMigrationOperation(values []AssistantMigrationOperationReceipt, value AssistantMigrationOperationReceipt) []AssistantMigrationOperationReceipt {
	out := append(append([]AssistantMigrationOperationReceipt(nil), values...), value)
	if len(out) > assistantMigrationMax {
		out = out[len(out)-assistantMigrationMax:]
	}
	return out
}
