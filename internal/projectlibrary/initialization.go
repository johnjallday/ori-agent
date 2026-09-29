package projectlibrary

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

const maxInitReviews = 32

// InitializationReview is a human-facing, inert preview. No marker, project
// link, root, workspace or team is created by reviewing.
type InitializationReview struct {
	Token          string                `json:"token"`
	HomeID         string                `json:"home_id"`
	LinkedCount    int                   `json:"linked_count"`
	EditedProjects []InitializationEntry `json:"edited_projects"`
	ExpiresAt      time.Time             `json:"expires_at"`
	Revision       int64                 `json:"revision"`
}

type InitializationEntry struct {
	ProjectWorkspaceID string `json:"project_workspace_id"`
	ProjectName        string `json:"project_name"`
	LinkID             string `json:"link_id"`
	Fields             Fields `json:"fields"`
}

type legacyProject struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	LinkID       string    `json:"link_id"`
	LinkRevision int64     `json:"link_revision"`
	Edited       *Fields   `json:"edited,omitempty"`
	LinkedAt     time.Time `json:"linked_at"`
}

type legacySnapshot struct {
	Scope             Scope           `json:"scope"`
	ProgramRevision   int64           `json:"program_revision"`
	PortfolioRevision int64           `json:"portfolio_revision"`
	Projects          []legacyProject `json:"projects"`
}

func (s *Store) legacySnapshot(scope Scope, home *workspace.Workspace, state *workspace.AssistantProgramState) (legacySnapshot, error) {
	if state == nil || !state.PluginAvailable || len(state.LinkedProjectIDs) > maxEntries ||
		state.SchemaVersion < workspace.AssistantProgramStateSchemaVersion {
		return legacySnapshot{}, ErrUnavailable
	}
	station, err := workspace.NewAssistantProgramStore(s.workspaces).FindStation(state.Key)
	if err != nil || station == nil || station.ID != scope.HomeID {
		return legacySnapshot{}, ErrConflict // ambiguous/foreign Home identity
	}
	snapshot := legacySnapshot{Scope: scope, ProgramRevision: state.StateRevision,
		PortfolioRevision: state.Portfolio.StateRevision}
	linkedIDs := append([]string(nil), state.LinkedProjectIDs...)
	sort.Strings(linkedIDs)
	seen := map[string]bool{}
	for _, projectID := range linkedIDs {
		if projectID == "" || seen[projectID] || projectID == home.ID {
			return legacySnapshot{}, ErrConflict
		}
		seen[projectID] = true
		child, err := s.workspaces.Get(projectID)
		if err != nil || child == nil || child.Status == workspace.StatusTrashed || child.Status == workspace.StatusMissing ||
			(workspace.AssistantProgramKey{OwnerUserID: child.OwnerUserID}).Normalize().OwnerUserID != scope.OwnerUserID {
			return legacySnapshot{}, ErrConflict
		}
		link := child.GetAssistantProjectLink()
		if link == nil || link.ID != workspace.AssistantProjectLinkID(home.ID, child.ID) ||
			link.StationWorkspaceID != home.ID || link.Key.Normalize() != state.Key.Normalize() ||
			link.StateRevision < 1 {
			return legacySnapshot{}, ErrConflict
		}
		item := legacyProject{ID: child.ID, Name: child.Name, LinkID: link.ID,
			LinkRevision: link.StateRevision, LinkedAt: link.LinkedAt}
		if !validText(child.Name, 160) || child.Name == "" {
			return legacySnapshot{}, ErrConflict
		}
		for _, prior := range state.Portfolio.Projects {
			if prior.ProjectWorkspaceID != child.ID {
				continue
			}
			if item.Edited != nil || prior.LinkID != link.ID {
				return legacySnapshot{}, ErrConflict
			}
			fields, err := fieldsFromLegacy(prior, state.Portfolio.StateRevision)
			if err != nil {
				return legacySnapshot{}, err
			}
			item.Edited = &fields
		}
		snapshot.Projects = append(snapshot.Projects, item)
	}
	// A stale edited row is history but cannot be silently matched to a
	// same-named or differently owned project during the authority switch.
	if len(state.Portfolio.Projects) != editedProjectCount(snapshot.Projects) {
		return legacySnapshot{}, ErrConflict
	}
	return snapshot, nil
}

func editedProjectCount(projects []legacyProject) int {
	count := 0
	for _, item := range projects {
		if item.Edited != nil {
			count++
		}
	}
	return count
}

func fieldsFromLegacy(project workspace.AssistantPortfolioProject, revision int64) (Fields, error) {
	priority := project.Priority // An explicitly saved zero is not an unset priority.
	fields := Fields{Status: project.Status, Priority: &priority,
		SessionDate: project.SessionDate, ReleaseDate: project.ReleaseDate,
		Blockers:           append([]string(nil), project.Blockers...),
		Deliverables:       append([]string(nil), project.Deliverables...),
		ArchiveReviewState: project.ArchiveReviewState,
		Revision:           revision, Source: "legacy_portfolio", UpdatedAt: project.UpdatedAt}
	if fields.Revision < 1 {
		fields.Revision = 1
	}
	for _, milestone := range project.Milestones {
		fields.Milestones = append(fields.Milestones, Milestone{ID: milestone.ID, Label: milestone.Label,
			DueDate: milestone.DueDate, Complete: milestone.Complete})
	}
	if !fields.valid() || fields.UpdatedAt.IsZero() {
		return Fields{}, ErrCorrupt
	}
	return fields, nil
}

func initializationDigest(snapshot legacySnapshot) (string, error) {
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return "", ErrCorrupt
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

// ReviewInitialize writes only a legacy-side review receipt. A library GET,
// provider refresh or root pick never implicitly crosses this authority gate.
func (s *Store) ReviewInitialize(scope Scope) (InitializationReview, error) {
	if s == nil || s.workspaces == nil || !scope.valid() {
		return InitializationReview{}, ErrUnavailable
	}
	home, err := s.workspaces.Get(scope.HomeID)
	if err != nil {
		return InitializationReview{}, ErrUnavailable
	}
	state, err := s.home(scope, home)
	if err != nil {
		return InitializationReview{}, err
	}
	if !s.providerWritable(scope, home) {
		return InitializationReview{}, ErrUnavailable
	}
	if len(state.ProjectLibrary) != 0 || len(state.ProjectLibraryInactiveRoots) != 0 {
		return InitializationReview{}, ErrConflict
	}
	snapshot, err := s.legacySnapshot(scope, home, state)
	if err != nil {
		return InitializationReview{}, err
	}
	digest, err := initializationDigest(snapshot)
	if err != nil {
		return InitializationReview{}, err
	}
	now := s.now().UTC()
	receipt := workspace.AssistantProjectLibraryInitReview{Token: uuid.NewString(), Digest: digest,
		PortfolioRevision: snapshot.PortfolioRevision, StateRevision: snapshot.ProgramRevision,
		ExpiresAt: now.Add(10 * time.Minute)}
	err = s.workspaces.Update(scope.HomeID, func(current *workspace.Workspace) error {
		currentState, checkErr := s.home(scope, current)
		if checkErr != nil {
			return checkErr
		}
		if len(currentState.ProjectLibrary) != 0 || len(currentState.ProjectLibraryInactiveRoots) != 0 ||
			!s.providerWritable(scope, current) {
			return ErrConflict
		}
		live, checkErr := s.legacySnapshot(scope, current, currentState)
		if checkErr != nil {
			return checkErr
		}
		liveDigest, checkErr := initializationDigest(live)
		if checkErr != nil || liveDigest != receipt.Digest {
			return ErrConflict
		}
		kept := currentState.ProjectLibraryInitReviews[:0]
		for _, prior := range currentState.ProjectLibraryInitReviews {
			if prior.ExpiresAt.After(now) && prior.ConsumedAt == nil {
				kept = append(kept, prior)
			}
		}
		if len(kept) >= maxInitReviews {
			return ErrLimit
		}
		currentState.ProjectLibraryInitReviews = append(kept, receipt)
		current.SetAssistantProgramState(currentState)
		return nil
	})
	if err != nil {
		return InitializationReview{}, err
	}
	preview := InitializationReview{Token: receipt.Token, HomeID: scope.HomeID,
		LinkedCount: len(snapshot.Projects), ExpiresAt: receipt.ExpiresAt, Revision: snapshot.PortfolioRevision}
	for _, project := range snapshot.Projects {
		if project.Edited != nil {
			preview.EditedProjects = append(preview.EditedProjects, InitializationEntry{
				ProjectWorkspaceID: project.ID, ProjectName: project.Name, LinkID: project.LinkID,
				Fields: *project.Edited})
		}
	}
	return preview, nil
}

// CommitInitialize is the only public initializer. The Home envelope's marker,
// copied fields, exact links and operation receipt are persisted in one Save.
func (s *Store) CommitInitialize(scope Scope, token, key string) (Document, bool, error) {
	if s == nil || s.workspaces == nil || !scope.valid() || token == "" || key == "" ||
		!validText(token, 160) || !validText(key, 160) {
		return Document{}, false, ErrConflict
	}
	var result Document
	var replay bool
	err := s.workspaces.Update(scope.HomeID, func(home *workspace.Workspace) error {
		state, err := s.home(scope, home)
		if err != nil {
			return err
		}
		var review *workspace.AssistantProjectLibraryInitReview
		for i := range state.ProjectLibraryInitReviews {
			if state.ProjectLibraryInitReviews[i].Token == token {
				review = &state.ProjectLibraryInitReviews[i]
				break
			}
		}
		if review == nil {
			return ErrConflict
		}
		if len(state.ProjectLibrary) != 0 {
			result, err = decodeDocument(state.ProjectLibrary, scope)
			if err != nil || !inactiveRootsValid(state, result) {
				return ErrCorrupt
			}
			for _, prior := range result.Operations {
				if prior.Key == key && prior.Action == "initialize" && prior.Digest == review.Digest {
					replay = true
					return errReplay // Avoid a no-op Save on an exact retry.
				}
			}
			return ErrConflict
		}
		if !s.providerWritable(scope, home) {
			return ErrUnavailable
		}
		if review.ConsumedAt != nil || !review.ExpiresAt.After(s.now().UTC()) ||
			len(state.ProjectLibraryInactiveRoots) != 0 || review.StateRevision != state.StateRevision ||
			review.PortfolioRevision != state.Portfolio.StateRevision {
			return ErrConflict
		}
		snapshot, err := s.legacySnapshot(scope, home, state)
		if err != nil {
			return err
		}
		digest, err := initializationDigest(snapshot)
		if err != nil || digest != review.Digest {
			return ErrConflict
		}
		now := s.now().UTC()
		result = Document{SchemaVersion: SchemaVersion, OwnerUserID: scope.OwnerUserID,
			HomeID: scope.HomeID, ProviderID: scope.ProviderID, ProgramID: scope.ProgramID, Revision: 1}
		for _, project := range snapshot.Projects {
			entry := Entry{ID: newID(), Revision: 1,
				Link: &ExactLink{WorkspaceID: project.ID, LinkID: project.LinkID, Revision: project.LinkRevision}}
			if project.Edited != nil {
				entry.Fields = *project.Edited
			}
			result.Entries = append(result.Entries, entry)
		}
		result.Operations = append(result.Operations, OperationReceipt{
			Key: key, Action: "initialize", Digest: review.Digest, ConsequenceID: scope.HomeID,
			Revision: 1, RecordedAt: now})
		if !result.valid(scope) {
			return ErrCorrupt
		}
		state.ProjectLibrary, err = json.Marshal(result)
		if err != nil || len(state.ProjectLibrary) > maxDocumentBytes {
			return ErrLimit
		}
		review.ConsumedAt = &now
		state.StateRevision++ // Invalidate reviews of the old Home program state.
		home.SetAssistantProgramState(state)
		return nil
	})
	if errors.Is(err, errReplay) && replay {
		return result, true, nil
	}
	if err != nil {
		return Document{}, false, err
	}
	return result, false, nil
}
