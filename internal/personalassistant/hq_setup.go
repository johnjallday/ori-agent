package personalassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/secondbrain"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
)

// hqPayloadVersion is the normalized HQ setup payload's shape version. It is
// part of the hashed payload so a future field change cannot make an old stored
// request look like a new one.
const hqPayloadVersion = 1

// HQSetupRequest is one confirmed Build My HQ submission.
//
// It carries no assistant, profile, workspace, or instance identity: those come
// from the server's own relationship record. A client that could name them could
// aim this operation at someone else's records.
type HQSetupRequest struct {
	RequestID string
	IfVersion int64

	HQName        string
	Timezone      string
	ScheduleDays  []string
	ScheduleTime  string
	Scope         string
	SelectedIDs   []string
	IncludeFuture bool
	NotifyOnReady bool

	// Notes is the reviewed notes choice (assistant-hq-onboarding 3.4). nil —
	// every older client — authorizes nothing about notes; Include false is an
	// explicit "set up later" and writes nothing.
	Notes *HQNotesChoice
}

// HQNotesChoice is what the HQ review said about notes. Witness is the folder
// the review showed for the default place; SelectionToken and Name name a
// folder the user chose instead, through the trusted folder picker.
type HQNotesChoice struct {
	Include        bool
	Witness        string
	SelectionToken string
	Name           string
}

// HQNotesSetup is the notes service, as the HQ coordinator uses it. It is bound
// late: the notes service is built after the coordinator.
type HQNotesSetup interface {
	ResolveSetupDestination(witness, selectionToken, name string) (SecondBrainSetupDestination, error)
	PrepareSetup(ctx context.Context, userID, operationID, hqRequestID string, dest SecondBrainSetupDestination) error
	RunSetup(ctx context.Context, userID, operationID string) (SecondBrainSetupResult, error)
	SetupStatus(ctx context.Context, userID string) (SecondBrainSetupResult, error)
}

// HQSetupResult is the canonical outcome of a completed setup.
type HQSetupResult struct {
	State       *State
	BriefConfig *dailybrief.Config
	// Resumed is true when this call finished (or re-returned) an operation that
	// a previous attempt had already started.
	Resumed bool
	// Notes is the notes half, independent of HQ: nil when the request carried
	// no notes choice. HQ is active whatever it says.
	Notes *SecondBrainSetupResult
}

// PartialHQSetupError reports a durable partial result and the safe step a retry
// should continue from. It never exposes the underlying provider or database
// message to a client.
type PartialHQSetupError struct {
	Step  RepairStep
	State *State
	Err   error
}

func (e *PartialHQSetupError) Error() string {
	return fmt.Sprintf("personal assistant: partial hq setup at %s: %v", e.Step, e.Err)
}

func (e *PartialHQSetupError) Unwrap() error { return e.Err }

// HQSetupCoordinator turns one confirmed Build My HQ request into exactly one
// canonical Personal HQ built around the already-hired assistant.
//
// Every side effect goes through the existing canonical services: the PAF
// template path creates the workspace, personalhq.Service designates it, and
// the Daily Brief service owns the schedule. This coordinator only sequences
// them, records where it got to, and refuses to guess.
//
// The ordering is deliberate. The operation claim is persisted BEFORE anything
// is created, and the returned workspace and entry-instance IDs are persisted at
// the first safe checkpoint, so a crash at any point leaves a resumable record
// rather than an invisible orphan workspace.
type HQSetupCoordinator struct {
	store    Store
	creator  personalhq.AssistantWorkspaceCreator
	hq       HireHQManager
	briefs   HireBriefManager
	profiles ProfileReader
	now      func() time.Time

	// A single-process lock keeps two same-user submits out of the creator at
	// once. Durable request IDs and workspace provenance still provide the
	// restart-safe half of idempotency.
	mu sync.Mutex

	// notes is the late-bound notes service; nil until wired.
	notesMu sync.RWMutex
	notes   HQNotesSetup
}

// SetNotesSetup binds the notes service once it exists.
func (c *HQSetupCoordinator) SetNotesSetup(notes HQNotesSetup) {
	if c == nil {
		return
	}
	c.notesMu.Lock()
	c.notes = notes
	c.notesMu.Unlock()
}

func (c *HQSetupCoordinator) notesSetup() HQNotesSetup {
	c.notesMu.RLock()
	defer c.notesMu.RUnlock()
	return c.notes
}

// NewHQSetupCoordinator constructs the post-hire HQ setup coordinator.
func NewHQSetupCoordinator(
	store Store,
	creator personalhq.AssistantWorkspaceCreator,
	hq HireHQManager,
	briefs HireBriefManager,
	profiles ProfileReader,
) *HQSetupCoordinator {
	return &HQSetupCoordinator{
		store: store, creator: creator, hq: hq, briefs: briefs,
		profiles: profiles, now: time.Now,
	}
}

// normalizedHQSetupRequest is the bounded, canonicalized payload that gets
// hashed and journalled. Every field is normalized before any side effect, so a
// replay compares like with like.
type normalizedHQSetupRequest struct {
	Version       int      `json:"version"`
	RequestID     string   `json:"request_id"`
	HQName        string   `json:"hq_name"`
	Timezone      string   `json:"timezone"`
	ScheduleDays  []string `json:"schedule_days"`
	ScheduleTime  string   `json:"schedule_time"`
	Scope         string   `json:"scope"`
	SelectedIDs   []string `json:"selected_workspace_ids"`
	IncludeFuture bool     `json:"include_future_workspaces"`
	NotifyOnReady bool     `json:"notify_on_ready"`
	// Notes is omitted for a request without a notes choice, so every older
	// payload keeps its exact bytes and hash and carries no notes effect.
	Notes *normalizedHQNotes `json:"notes,omitempty"`
	Hash  string             `json:"-"`
}

// normalizedHQNotes is the confirmed notes choice as journalled and hashed.
// The resolved folder is recorded by the notes service before the claim, not
// here, so a replay of the same request compares like with like.
type normalizedHQNotes struct {
	Include bool   `json:"include"`
	Witness string `json:"witness,omitempty"`
	Chosen  bool   `json:"chosen,omitempty"`
	Name    string `json:"name,omitempty"`
	// Parent and Folder are the reviewed folder, resolved once on the fresh
	// claim and journalled with it, so a resume makes exactly that folder. They
	// are not part of the request hash: a replay of the same confirmation
	// compares the user's choice, not a resolution made later.
	Parent string `json:"parent,omitempty"`
	Folder string `json:"folder,omitempty"`
}

// canonicalHash is the hash of what the user confirmed: the journalled
// payload without the server's resolution of the notes folder.
func (r normalizedHQSetupRequest) canonicalHash() (string, error) {
	copied := r
	if r.Notes != nil {
		notes := *r.Notes
		notes.Parent, notes.Folder = "", ""
		copied.Notes = &notes
	}
	payload, err := json.Marshal(copied)
	if err != nil {
		return "", err
	}
	return PayloadHash(payload), nil
}

func (r normalizedHQSetupRequest) wantsNotes() bool { return r.Notes != nil && r.Notes.Include }

// Setup runs or resumes one confirmed HQ setup operation.
func (c *HQSetupCoordinator) Setup(ctx context.Context, userID string, request HQSetupRequest) (*HQSetupResult, error) {
	if c == nil || c.store == nil || c.creator == nil || c.hq == nil || c.briefs == nil {
		return nil, errors.New("personal assistant: hq setup coordinator is not configured")
	}
	normalized, err := normalizeHQSetupRequest(request)
	if err != nil {
		return nil, err
	}
	userID, err = validateOpaqueID("user id", userID, true)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	state, resumed, canonical, err := c.claim(ctx, userID, request.IfVersion, normalized, request.Notes)
	if err != nil {
		return nil, err
	}
	normalized = canonical

	// Already finished: return the canonical records rather than building again.
	if state.Status == StatusActive || state.Status == StatusPaused {
		config, _ := c.briefs.GetConfig(ctx, state.HQWorkspaceID)
		return &HQSetupResult{
			State: state.Clone(), BriefConfig: cloneBriefConfig(config), Resumed: true,
			Notes: c.runNotes(ctx, userID, normalized, request.Notes),
		}, nil
	}

	recordEvent(EventHQSetupStarted, EventData{
		AssistantID: state.AssistantID, State: string(state.Status),
	})
	startedAt := c.now()

	state, err = c.ensureWorkspace(ctx, state, normalized)
	if err != nil {
		return nil, err
	}
	if err := c.ensureDesignation(ctx, userID, state.HQWorkspaceID); err != nil {
		return nil, c.partial(ctx, state, RepairDesignation, err)
	}
	config, err := c.ensureBriefConfig(ctx, userID, state.HQWorkspaceID, normalized)
	if err != nil {
		return nil, c.partial(ctx, state, RepairDailyBriefConfig, err)
	}
	if _, err := c.hq.SetOnboardingState(ctx, userID, userprofile.HQOnboardingCompleted); err != nil {
		return nil, c.partial(ctx, state, RepairFinalization, err)
	}

	final := state.Clone()
	final.Status = StatusActive
	final.RepairStep = RepairNone
	// The provisional payload is reduced to its receipt: the canonical Daily
	// Brief config owns the schedule from here on, and keeping a copy would only
	// be a stale duplicate. normalizeState enforces this too.
	final.HQPayloadJSON = ""
	updated, updateErr := c.store.UpdateState(ctx, final, state.StateVersion)
	if updateErr != nil {
		return nil, c.partial(ctx, state, RepairFinalization, updateErr)
	}

	recordEvent(EventHQActivated, EventData{
		AssistantID: updated.AssistantID, WorkspaceID: updated.HQWorkspaceID,
		State: string(updated.Status), DurationMS: c.now().Sub(startedAt).Milliseconds(),
	})
	return &HQSetupResult{
		State: updated.Clone(), BriefConfig: cloneBriefConfig(config), Resumed: resumed,
		Notes: c.runNotes(ctx, userID, normalized, request.Notes),
	}, nil
}

// Resume continues only the durable, previously confirmed HQ setup for this
// user. The browser supplies no request identity or setup fields.
func (c *HQSetupCoordinator) Resume(ctx context.Context, userID string) (*HQSetupResult, error) {
	if c == nil || c.store == nil {
		return nil, errors.New("personal assistant: hq setup coordinator is not configured")
	}
	state, err := c.store.GetState(ctx, userID)
	if err != nil {
		return nil, err
	}
	if state.Status != StatusProvisioningHQ || state.HQPayloadJSON == "" {
		return nil, fmt.Errorf("%w: no resumable personal hq setup", ErrConflict)
	}
	stored, err := decodeStoredHQRequest(state)
	if err != nil {
		return nil, ErrRepairNeeded
	}
	return c.Setup(ctx, userID, HQSetupRequest{
		RequestID: stored.RequestID, IfVersion: state.StateVersion,
		HQName: stored.HQName, Timezone: stored.Timezone,
		ScheduleDays: stored.ScheduleDays, ScheduleTime: stored.ScheduleTime,
		Scope: stored.Scope, SelectedIDs: stored.SelectedIDs,
		IncludeFuture: stored.IncludeFuture, NotifyOnReady: stored.NotifyOnReady,
		Notes: storedNotesChoice(stored.Notes),
	})
}

/*
claim validates that this relationship may build HQ, then records the operation
before anything is created.

Ownership is checked here rather than later so a request aimed at the wrong
relationship never reaches the creator. The claim write is what makes a crash
mid-creation recoverable: without it, a workspace could exist that no persisted
state points at.
*/
func (c *HQSetupCoordinator) claim(
	ctx context.Context, userID string, ifVersion int64, request normalizedHQSetupRequest, notesChoice *HQNotesChoice,
) (*State, bool, normalizedHQSetupRequest, error) {
	state, err := c.store.GetState(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return nil, false, request, fmt.Errorf("%w: no personal assistant is hired", ErrConflict)
	}
	if err != nil {
		return nil, false, request, err
	}

	switch state.Status {
	case StatusAwaitingHQ, StatusProvisioningHQ:
		// The two states this operation exists to serve.
	case StatusActive, StatusPaused:
		// Terminal. Replay checks below still apply so a stale submit cannot
		// silently rebuild HQ around a live relationship.
	default:
		return nil, false, request, fmt.Errorf(
			"%w: personal assistant is not ready to build hq", ErrConflict)
	}

	// The relationship must own a real profile before a workspace is built around
	// it, and ownership is proven by durable marker, never by display name.
	if err := c.validateOwnedProfile(state); err != nil {
		return nil, false, request, err
	}

	// A different in-flight request ID means two confirmed submits are competing.
	if existing := strings.TrimSpace(state.LastHQRequestID); existing != "" && existing != request.RequestID {
		if state.Status == StatusProvisioningHQ {
			return nil, false, request, fmt.Errorf(
				"%w: another hq setup request is already in progress", ErrConflict)
		}
		if state.Status == StatusActive || state.Status == StatusPaused {
			return nil, false, request, fmt.Errorf("%w: personal hq already exists", ErrConflict)
		}
	}

	// Same request ID, different payload: the user confirmed one thing and this
	// call is asking for another. Never silently apply the second.
	if strings.TrimSpace(state.LastHQRequestID) == request.RequestID &&
		state.HQPayloadHash != "" && state.HQPayloadHash != request.Hash {
		return nil, false, request, fmt.Errorf(
			"%w: the hq setup request payload changed", ErrConflict)
	}

	if state.Status == StatusActive || state.Status == StatusPaused {
		return state, true, request, nil
	}

	// Resuming: the stored payload is authoritative, so a browser that lost its
	// form state cannot quietly change the rhythm the user actually confirmed.
	if state.Status == StatusProvisioningHQ && state.HQPayloadJSON != "" {
		stored, storedErr := decodeStoredHQRequest(state)
		if storedErr != nil {
			return nil, false, request, ErrRepairNeeded
		}
		return state, true, stored, nil
	}

	if ifVersion != state.StateVersion {
		return nil, false, request, fmt.Errorf("%w: stale relationship version", ErrConflict)
	}

	// A fresh claim with notes: fix the reviewed folder and record the notes
	// operation BEFORE the claim, so nothing is created that a crash could
	// leave unrecorded. Replays and resumes never get here, so a changed
	// Workspace Directory or an expired selection cannot redirect them.
	if request.wantsNotes() {
		dest, err := c.resolveNotes(notesChoice)
		if err != nil {
			return nil, false, request, err
		}
		notes := *request.Notes
		notes.Parent, notes.Folder = dest.Parent, dest.Name
		request.Notes = &notes
	}

	payload, marshalErr := json.Marshal(request)
	if marshalErr != nil {
		return nil, false, request, marshalErr
	}
	next := state.Clone()
	next.Status = StatusProvisioningHQ
	next.LastHQRequestID = request.RequestID
	next.HQPayloadHash = request.Hash
	next.HQPayloadJSON = string(payload)
	next.RepairStep = RepairNone
	claimed, claimErr := c.store.UpdateState(ctx, next, state.StateVersion)
	if claimErr != nil {
		return nil, false, request, claimErr
	}
	return claimed, state.Status == StatusProvisioningHQ, request, nil
}

// storedNotesChoice rebuilds the journalled notes choice so a resume hashes
// exactly as the confirmed request did. A resume takes the stored payload and
// never resolves the destination again (the notes operation already holds
// it), so the chosen-folder token is only a stand-in for "a folder was chosen".
func storedNotesChoice(notes *normalizedHQNotes) *HQNotesChoice {
	if notes == nil {
		return nil
	}
	choice := &HQNotesChoice{Include: notes.Include, Witness: notes.Witness, Name: notes.Name}
	if notes.Chosen {
		choice.SelectionToken = "stored"
	}
	return choice
}

// resolveNotes fixes the reviewed notes folder before anything is claimed, so a
// review that no longer holds is refused while nothing exists yet.
func (c *HQSetupCoordinator) resolveNotes(choice *HQNotesChoice) (SecondBrainSetupDestination, error) {
	notes := c.notesSetup()
	if notes == nil {
		return SecondBrainSetupDestination{}, ErrSecondBrainUnavailable
	}
	var witness, token, name string
	if choice != nil {
		witness, token, name = choice.Witness, choice.SelectionToken, choice.Name
	}
	return notes.ResolveSetupDestination(witness, token, name)
}

const hqNotesUnavailableMessage = "Your Personal HQ is ready. Notes couldn't be set up right now; try again from Notes."

/*
runNotes finishes the notes half once HQ is active — the first moment the notes
record (stored with HQ) can be written. Its outcome is reported, never returned
as an HQ error: a notes failure leaves HQ built and usable, with a notes-only
retry.

The folder is the one resolved and journalled on the fresh claim. A replay of a
finished build no longer has that journal (it is reduced to its receipt on
activation), so if no notes operation was ever recorded — a crash between
activation and this step — the replay resolves the same review again: the
reviewed place still holds, or the notes are refused with guidance. Nothing is
ever built twice: the operation is idempotent by its id.
*/
func (c *HQSetupCoordinator) runNotes(
	ctx context.Context, userID string, request normalizedHQSetupRequest, choice *HQNotesChoice,
) *SecondBrainSetupResult {
	if !request.wantsNotes() {
		return nil
	}
	unavailable := &SecondBrainSetupResult{
		Status: SecondBrainSetupFailed, Reason: secondbrain.SetupReasonUnavailable, Message: hqNotesUnavailableMessage,
	}
	notes := c.notesSetup()
	if notes == nil {
		return unavailable
	}
	opID := SecondBrainSetupOperationID(request.RequestID)
	dest := SecondBrainSetupDestination{Parent: request.Notes.Parent, Name: request.Notes.Folder}
	if dest.Parent == "" {
		status, err := notes.SetupStatus(ctx, userID)
		if err != nil {
			return unavailable
		}
		switch {
		case status.Status == SecondBrainSetupPending:
			// Recorded but not finished: finish it below.
		case status.Status != SecondBrainSetupNone:
			return &status
		default:
			resolved, err := c.resolveNotes(choice)
			if err != nil {
				return &SecondBrainSetupResult{
					Status: SecondBrainSetupFailed, Reason: secondbrain.SetupReasonUnavailable,
					Message: "Your Personal HQ is ready. Notes were not set up because the reviewed location changed; set them up from Notes.",
				}
			}
			dest = resolved
		}
	}
	if dest.Parent != "" {
		if err := notes.PrepareSetup(ctx, userID, opID, request.RequestID, dest); err != nil {
			logger.Warn("A Personal HQ notes setup could not be recorded", logger.Fields{"error": err.Error()})
			return unavailable
		}
	}
	result, err := notes.RunSetup(ctx, userID, opID)
	if err != nil {
		logger.Warn("A Personal HQ notes setup did not finish", logger.Fields{"error": err.Error()})
		if status, statusErr := notes.SetupStatus(ctx, userID); statusErr == nil && status.Status != SecondBrainSetupNone {
			return &status
		}
		return unavailable
	}
	return &result
}

// validateOwnedProfile refuses to build a workspace around a profile this
// relationship cannot prove it owns.
func (c *HQSetupCoordinator) validateOwnedProfile(state *State) error {
	name := strings.TrimSpace(state.GlobalAgentProfileName)
	if name == "" {
		return fmt.Errorf("%w: the hired assistant has no profile", ErrConflict)
	}
	if c.profiles == nil {
		return errors.New("personal assistant: profile provenance is unavailable")
	}
	provenance, ok := c.profiles.PersonalAssistantProfileProvenance(name)
	if !ok {
		return fmt.Errorf("%w: the hired assistant's profile is missing", ErrRepairNeeded)
	}
	if !provenance.OwnedBy(state.AssistantID) {
		return fmt.Errorf("%w: the profile is not owned by this relationship", ErrConflict)
	}
	return nil
}

/*
ensureWorkspace creates the HQ workspace, or resolves the one this operation
already created, and persists its canonical IDs at the first safe checkpoint.

The creator is idempotent by assistant+request provenance, so a retry after a
crash resolves the same workspace instead of making a second one.
*/
func (c *HQSetupCoordinator) ensureWorkspace(
	ctx context.Context, state *State, request normalizedHQSetupRequest,
) (*State, error) {
	if strings.TrimSpace(state.HQWorkspaceID) != "" &&
		strings.TrimSpace(state.HQEntryAgentInstanceID) != "" {
		return state, nil
	}

	created, err := c.creator.CreatePersonalAssistantHQ(ctx, request.HQName, personalhq.AssistantCreationOptions{
		AssistantID: state.AssistantID, RequestID: request.RequestID,
		DisplayName: state.GlobalAgentProfileName,
		Appearance:  state.Appearance.Clone(),
		Role:        types.RoleOrchestrator, SystemPromptFragment: PersonalAssistantPromptFragment,
	})
	if err != nil {
		// Nothing canonical came back, so the operation stays claimed and
		// resumable at the creation step.
		recordEvent(EventRecoverableFailure, EventData{
			AssistantID: state.AssistantID, State: string(state.Status),
			Recoverable: true, ReasonCode: string(RepairHQCreation),
		})
		return nil, c.partial(ctx, state, RepairHQCreation, err)
	}
	if created == nil || strings.TrimSpace(created.WorkspaceID) == "" ||
		strings.TrimSpace(created.EntryAgentInstanceID) == "" ||
		strings.TrimSpace(created.GlobalAgentProfileName) == "" {
		return nil, c.partial(ctx, state, RepairHQCreation,
			errors.New("creator returned incomplete canonical identity"))
	}

	next := state.Clone()
	next.HQWorkspaceID = created.WorkspaceID
	next.HQEntryAgentInstanceID = created.EntryAgentInstanceID
	next.GlobalAgentProfileName = created.GlobalAgentProfileName
	next.RepairStep = RepairDesignation
	updated, updateErr := c.store.UpdateState(ctx, next, state.StateVersion)
	if updateErr != nil {
		// The workspace exists but its IDs are not recorded. Report a bounded
		// partial: the creator will resolve the same workspace on retry through
		// its own provenance metadata, so nothing is orphaned or duplicated.
		return nil, c.partial(ctx, state, RepairHQCreation, updateErr)
	}
	return updated, nil
}

func (c *HQSetupCoordinator) ensureDesignation(ctx context.Context, userID, workspaceID string) error {
	status, err := c.hq.Status(ctx, userID)
	if err != nil {
		return err
	}
	if status != nil && status.Valid {
		if strings.TrimSpace(status.WorkspaceID) == strings.TrimSpace(workspaceID) {
			return nil
		}
		return fmt.Errorf("%w: another personal hq is already designated", ErrConflict)
	}
	_, err = c.hq.Designate(ctx, userID, workspaceID)
	return err
}

func (c *HQSetupCoordinator) ensureBriefConfig(
	ctx context.Context, userID, workspaceID string, request normalizedHQSetupRequest,
) (*dailybrief.Config, error) {
	desired, err := dailybrief.NormalizeConfig(dailybrief.Config{
		WorkspaceID: workspaceID, UserID: userID, Timezone: request.Timezone,
		ScheduleDays: append([]string(nil), request.ScheduleDays...),
		ScheduleTime: request.ScheduleTime, ScheduleEnabled: true,
		Scope:                   dailybrief.Scope(request.Scope),
		SelectedWorkspaceIDs:    append([]string(nil), request.SelectedIDs...),
		IncludeFutureWorkspaces: request.IncludeFuture,
		NotifyOnReady:           request.NotifyOnReady,
	})
	if err != nil {
		return nil, err
	}
	existing, err := c.briefs.GetConfig(ctx, workspaceID)
	if err == nil && equivalentBriefConfig(existing, &desired) {
		return existing, nil
	}
	if err != nil && !errors.Is(err, dailybrief.ErrConfigNotFound) {
		return nil, err
	}
	return c.briefs.UpdateConfig(ctx, desired)
}

// partial records the safe continuation step and returns a bounded error.
//
// It deliberately does NOT move the relationship to repair_needed: the operation
// is still resumable through the same request ID, and telling the user their
// setup is broken when a retry would finish it would be a lie. The status stays
// provisioning_hq, which projects as a resumable setup.
func (c *HQSetupCoordinator) partial(
	ctx context.Context, state *State, step RepairStep, cause error,
) error {
	recordEvent(EventRecoverableFailure, EventData{
		AssistantID: state.AssistantID, WorkspaceID: state.HQWorkspaceID,
		State: string(StatusProvisioningHQ), Recoverable: true, ReasonCode: string(step),
	})
	next := state.Clone()
	next.Status = StatusProvisioningHQ
	next.RepairStep = step
	updated, err := c.store.UpdateState(ctx, next, state.StateVersion)
	if err != nil {
		return &PartialHQSetupError{Step: step, State: next, Err: cause}
	}
	return &PartialHQSetupError{Step: step, State: updated, Err: cause}
}

func decodeStoredHQRequest(state *State) (normalizedHQSetupRequest, error) {
	var request normalizedHQSetupRequest
	if state == nil || state.HQPayloadJSON == "" {
		return request, errors.New("personal assistant: invalid stored hq operation")
	}
	if err := json.Unmarshal([]byte(state.HQPayloadJSON), &request); err != nil {
		return request, err
	}
	// Every payload without notes is hashed byte for byte, as it always was. A
	// payload with notes also carries the resolved folder, which is outside the
	// hash, so it is checked against the hash of what the user confirmed.
	if PayloadHash([]byte(state.HQPayloadJSON)) != state.HQPayloadHash {
		canonical, err := request.canonicalHash()
		if err != nil || request.Notes == nil || canonical != state.HQPayloadHash {
			return normalizedHQSetupRequest{}, errors.New("personal assistant: invalid stored hq operation")
		}
	}
	request.Hash = state.HQPayloadHash
	if request.RequestID != state.LastHQRequestID {
		return request, errors.New("personal assistant: stored hq request id mismatch")
	}
	return request, nil
}

// MaxHQNameLen bounds the user-visible Personal HQ workspace name.
const MaxHQNameLen = 100

// DefaultHQName is the neutral editable default.
const DefaultHQName = "Personal HQ"

// normalizeHQSetupRequest validates and canonicalizes every user-controlled
// field before any persistence or provisioning consequence.
func normalizeHQSetupRequest(input HQSetupRequest) (normalizedHQSetupRequest, error) {
	var out normalizedHQSetupRequest
	out.Version = hqPayloadVersion
	if input.IfVersion < 0 {
		return out, fmt.Errorf("%w: if_version cannot be negative", ErrValidation)
	}
	var err error
	if out.RequestID, err = validateOpaqueID("hq request id", input.RequestID, true); err != nil {
		return out, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	name := strings.TrimSpace(input.HQName)
	if name == "" {
		name = DefaultHQName
	}
	if out.HQName, err = validateText("hq name", name, MaxHQNameLen, true); err != nil {
		return out, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if htmlLikeTagPattern.MatchString(out.HQName) {
		return out, fmt.Errorf("%w: hq name must be plain text", ErrValidation)
	}

	out.Timezone = strings.TrimSpace(input.Timezone)
	if out.Timezone == "" {
		out.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(out.Timezone); err != nil {
		return out, fmt.Errorf("%w: invalid IANA timezone", ErrValidation)
	}
	if out.ScheduleDays, err = normalizeHQScheduleDays(input.ScheduleDays); err != nil {
		return out, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	out.ScheduleTime = strings.TrimSpace(input.ScheduleTime)
	if out.ScheduleTime == "" {
		out.ScheduleTime = "08:00"
	}

	scope := strings.ToLower(strings.TrimSpace(input.Scope))
	if scope == "" {
		scope = string(dailybrief.ScopeAll)
	}
	switch dailybrief.Scope(scope) {
	case dailybrief.ScopeAll, dailybrief.ScopeSelected:
		out.Scope = scope
	default:
		return out, fmt.Errorf("%w: invalid daily brief scope", ErrValidation)
	}

	if out.SelectedIDs, err = normalizeHQWorkspaceIDs(input.SelectedIDs); err != nil {
		return out, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if dailybrief.Scope(out.Scope) == dailybrief.ScopeAll {
		// A selection is meaningless under all-workspace scope; dropping it keeps
		// the hash stable across clients that send one anyway.
		out.SelectedIDs = nil
	}
	out.IncludeFuture = input.IncludeFuture
	out.NotifyOnReady = input.NotifyOnReady
	if notes := input.Notes; notes != nil {
		out.Notes = &normalizedHQNotes{Include: notes.Include}
		if notes.Include {
			if strings.TrimSpace(notes.SelectionToken) != "" {
				out.Notes.Chosen = true
				out.Notes.Name = strings.TrimSpace(notes.Name)
				if out.Notes.Name == "" || len(out.Notes.Name) > 255 {
					return out, fmt.Errorf("%w: invalid notes folder name", ErrValidation)
				}
			} else {
				out.Notes.Witness = strings.TrimSpace(notes.Witness)
				if out.Notes.Witness == "" || len(out.Notes.Witness) > 4096 {
					return out, fmt.Errorf("%w: the reviewed notes location is missing", ErrValidation)
				}
			}
		}
	}

	// Validate the whole rhythm through the canonical validator before anything
	// is claimed, so a bad schedule fails before a workspace exists.
	if _, err := dailybrief.NormalizeConfig(dailybrief.Config{
		WorkspaceID: "validation", Timezone: out.Timezone,
		ScheduleDays: out.ScheduleDays, ScheduleTime: out.ScheduleTime,
		Scope: dailybrief.Scope(out.Scope), SelectedWorkspaceIDs: out.SelectedIDs,
	}); err != nil {
		return out, fmt.Errorf("%w: invalid Daily Brief rhythm", ErrValidation)
	}

	payload, err := json.Marshal(out)
	if err != nil {
		return out, fmt.Errorf("%w: could not normalize hq setup request", ErrValidation)
	}
	out.Hash = PayloadHash(payload)
	return out, nil
}

func normalizeHQScheduleDays(input []string) ([]string, error) {
	if len(input) == 0 {
		return []string{"mon", "tue", "wed", "thu", "fri"}, nil
	}
	allowed := map[string]bool{
		"mon": true, "tue": true, "wed": true, "thu": true,
		"fri": true, "sat": true, "sun": true,
	}
	seen := make(map[string]bool, 7)
	out := make([]string, 0, len(input))
	for _, raw := range input {
		day := strings.ToLower(strings.TrimSpace(raw))
		if !allowed[day] {
			return nil, fmt.Errorf("invalid schedule day %q", raw)
		}
		if !seen[day] {
			seen[day] = true
			out = append(out, day)
		}
	}
	return out, nil
}

// MaxHQSelectedWorkspaces bounds one Daily Brief workspace selection.
const MaxHQSelectedWorkspaces = 200

func normalizeHQWorkspaceIDs(input []string) ([]string, error) {
	if len(input) > MaxHQSelectedWorkspaces {
		return nil, fmt.Errorf("too many selected workspaces")
	}
	seen := make(map[string]bool, len(input))
	out := make([]string, 0, len(input))
	for _, raw := range input {
		id, err := validateOpaqueID("selected workspace id", raw, false)
		if err != nil {
			return nil, err
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
