package personalassistant

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

var (
	// ErrHQFolderAvailable means the saved folder is no longer blocked; the
	// user can resume the original request without changing its target.
	ErrHQFolderAvailable = errors.New("personal hq: saved folder is now available")
	// ErrHQFolderDirectoryChanged refuses to apply a confirmed alternate to a
	// different workspace directory.
	ErrHQFolderDirectoryChanged = errors.New("personal hq: workspace directory changed after folder confirmation")
)

// HQFolderChange is a separate confirmation. It cannot change the assistant,
// display name, rhythm, Notes choice or supply a path.
type HQFolderChange struct {
	RequestID  string `json:"request_id"`
	IfVersion  int64  `json:"if_version"`
	FolderSlug string `json:"folder_slug"`
	Review     string `json:"review"`
}

func (r HQFolderChange) valid() bool {
	id, err := validateOpaqueID("hq request id", r.RequestID, true)
	if err != nil || id != r.RequestID || r.IfVersion < 1 ||
		!workspace.IsCanonicalWorkspaceSlug(r.FolderSlug) || workspace.IsReservedTopLevelSlug(r.FolderSlug) ||
		len(r.Review) != 64 {
		return false
	}
	_, err = hex.DecodeString(r.Review)
	return err == nil
}

// After success keep only a typed-by-prefix receipt of the last accepted
// confirmation. An exact duplicate can observe the result; an old Build My HQ
// submit or a different change cannot start anything.
func (r HQFolderChange) receiptHash() string {
	payload, _ := json.Marshal(r) // All fields are JSON primitives.
	return "folder-" + PayloadHash(payload)
}

type hqFolderReviewer interface {
	ReviewPersonalAssistantHQFolder(ctx context.Context, name string, options personalhq.AssistantCreationOptions) (*personalhq.FolderConflict, error)
}

// ReviewFolder observes the currently blocked folder of a saved build. This
// does not resume it or change its version; a reload needs no browser draft.
func (c *HQSetupCoordinator) ReviewFolder(ctx context.Context, userID string) (*State, *personalhq.FolderConflict, error) {
	if c == nil || c.store == nil || c.hq == nil {
		return nil, nil, errors.New("personal assistant: hq folder review is not configured")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := c.store.GetState(ctx, userID)
	if errors.Is(err, ErrNotFound) || (err == nil && !canReviewHQFolder(state)) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	stored, err := decodeStoredHQRequest(state)
	if err != nil {
		return nil, nil, ErrRepairNeeded
	}
	conflict, err := c.reviewHQFolder(ctx, state, stored)
	if errors.Is(err, ErrConflict) {
		return nil, nil, nil
	}
	return state, conflict, err
}

func canReviewHQFolder(state *State) bool {
	return state.Status == StatusProvisioningHQ && state.HQPayloadJSON != "" &&
		state.RepairStep == RepairHQCreation && state.HQWorkspaceID == "" && state.HQEntryAgentInstanceID == ""
}

func (c *HQSetupCoordinator) reviewHQFolder(ctx context.Context, state *State, stored normalizedHQSetupRequest) (*personalhq.FolderConflict, error) {
	if err := c.validateOwnedProfile(state); err != nil {
		return nil, err
	}
	if status, err := c.hq.Status(ctx, state.UserID); err != nil {
		return nil, err
	} else if status.HasDesignation() {
		return nil, ErrConflict
	}
	reviewer, ok := c.creator.(hqFolderReviewer)
	if !ok {
		return nil, errors.New("personal hq: folder review is unavailable")
	}
	return reviewer.ReviewPersonalAssistantHQFolder(ctx, stored.HQName, hqCreationOptions(state, stored))
}

// ChangeFolder resumes a saved HQ build after the user explicitly confirms
// the suggested folder. It is the only path that edits the folder target.
func (c *HQSetupCoordinator) ChangeFolder(ctx context.Context, userID string, change HQFolderChange) (*HQSetupResult, error) {
	if c == nil || c.store == nil || c.creator == nil || c.hq == nil || c.briefs == nil {
		return nil, errors.New("personal assistant: hq setup coordinator is not configured")
	}
	userID, err := validateOpaqueID("user id", userID, true)
	if err != nil || !change.valid() {
		return nil, ErrValidation
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	state, err := c.store.GetState(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	if state.LastHQRequestID != change.RequestID {
		return nil, ErrConflict
	}
	if err := c.validateOwnedProfile(state); err != nil {
		return nil, err
	}
	if (state.Status == StatusActive || state.Status == StatusPaused) && state.HQPayloadHash == change.receiptHash() {
		return c.finishedHQ(ctx, state), nil
	}
	if state.Status != StatusProvisioningHQ || state.HQPayloadJSON == "" {
		return nil, ErrConflict
	}
	stored, err := decodeStoredHQRequest(state)
	if err != nil {
		return nil, ErrRepairNeeded
	}
	if stored.FolderChange != nil && *stored.FolderChange == change {
		// The CAS already accepted this exact change. Resume it without a new
		// edit, including rediscovery if a workspace has since been created.
		return c.finishSetup(ctx, state, stored, true, storedNotesChoice(stored.Notes))
	}
	if state.StateVersion != change.IfVersion || !canReviewHQFolder(state) {
		return nil, ErrConflict
	}
	conflict, err := c.reviewHQFolder(ctx, state, stored)
	if err != nil {
		return nil, err
	}
	if conflict == nil {
		return nil, ErrHQFolderAvailable
	}
	if conflict.SuggestedSlug != change.FolderSlug || conflict.RootWitness != change.Review {
		// The review expired, e.g. someone used the suggested name in the
		// meantime. No state changed. Show the updated review, not an
		// automatically chosen folder.
		return nil, &PartialHQSetupError{Step: RepairHQCreation, State: state, Err: conflict}
	}
	stored.FolderChange = &change
	payload, err := json.Marshal(stored)
	if err != nil {
		return nil, err
	}
	stored.Hash, err = stored.canonicalHash()
	if err != nil {
		return nil, err
	}
	next := state.Clone()
	next.HQPayloadJSON = string(payload)
	next.HQPayloadHash = stored.Hash
	claimed, err := c.store.UpdateState(ctx, next, state.StateVersion)
	if err != nil {
		return nil, fmt.Errorf("save personal hq folder choice: %w", err)
	}
	return c.finishSetup(ctx, claimed, stored, true, storedNotesChoice(stored.Notes))
}

func hqCreationOptions(state *State, request normalizedHQSetupRequest) personalhq.AssistantCreationOptions {
	options := personalhq.AssistantCreationOptions{
		AssistantID: state.AssistantID, RequestID: request.RequestID,
		DisplayName: state.GlobalAgentProfileName,
		Appearance:  state.Appearance.Clone(),
		Role:        types.RoleOrchestrator, SystemPromptFragment: PersonalAssistantPromptFragment,
	}
	if request.FolderChange != nil {
		options.FolderSlug, options.FolderRootWitness = request.FolderChange.FolderSlug, request.FolderChange.Review
	}
	return options
}
