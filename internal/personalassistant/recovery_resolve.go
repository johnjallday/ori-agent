package personalassistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/dailybrief"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalhq"
	"github.com/johnjallday/ori-agent/internal/types"
)

// RecoveryIssue names the first recovery check that failed. It is what turns
// "records do not agree" into a specific, fixable statement.
type RecoveryIssue string

const (
	// RecoveryIssueProfileMissing: a Personal HQ exists but no agent profile
	// carries the assistant's ownership marker.
	RecoveryIssueProfileMissing RecoveryIssue = "profile_missing"
	// RecoveryIssueProfileDuplicate: more than one profile claims to be the
	// assistant.
	RecoveryIssueProfileDuplicate RecoveryIssue = "profile_duplicate"
	// RecoveryIssueProfileIncomplete: the one marked profile's markers are
	// partial, repeated or empty.
	RecoveryIssueProfileIncomplete RecoveryIssue = "profile_incomplete"
	// RecoveryIssueProfileRole: the marked profile is not an orchestrator.
	RecoveryIssueProfileRole RecoveryIssue = "profile_role"
	// RecoveryIssueDesignationWithoutHQ: a workspace is designated Personal HQ
	// but no workspace carries the assistant's HQ marker.
	RecoveryIssueDesignationWithoutHQ RecoveryIssue = "designation_without_hq"
	// RecoveryIssueHQDuplicate: more than one workspace carries the HQ marker.
	RecoveryIssueHQDuplicate RecoveryIssue = "hq_duplicate"
	// RecoveryIssueHQMarkerInvalid: the HQ marker is unreadable or incomplete.
	RecoveryIssueHQMarkerInvalid RecoveryIssue = "hq_marker_invalid"
	// RecoveryIssueHQForeignOwner: the marked HQ belongs to another user.
	RecoveryIssueHQForeignOwner RecoveryIssue = "hq_foreign_owner"
	// RecoveryIssueAssistantMismatch: the profile and the HQ marker name
	// different assistants, typically an HQ kept from an earlier hire.
	RecoveryIssueAssistantMismatch RecoveryIssue = "assistant_mismatch"
	// RecoveryIssueDesignationMismatch: the marked HQ is not the designated
	// Personal HQ (nothing designated, or another workspace is).
	RecoveryIssueDesignationMismatch RecoveryIssue = "designation_mismatch"
	// RecoveryIssueEntryMismatch: the HQ's lead agent is not the assistant.
	RecoveryIssueEntryMismatch RecoveryIssue = "entry_mismatch"
	// RecoveryIssueBriefMissing: the HQ has no Daily Brief settings.
	RecoveryIssueBriefMissing RecoveryIssue = "brief_missing"
	// RecoveryIssueBriefMismatch: the HQ's Daily Brief settings belong to
	// another user or workspace.
	RecoveryIssueBriefMismatch RecoveryIssue = "brief_mismatch"
)

// RecoveryFixKind is one narrow, reviewed edit that can clear an issue. Every
// kind touches only ownership markers, the HQ designation, or a new Daily Brief
// configuration: never a prompt, model, tool, conversation or file, and never a
// deletion.
type RecoveryFixKind string

const (
	// RecoveryFixLinkHQ writes the profile's assistant into the HQ's marker.
	RecoveryFixLinkHQ RecoveryFixKind = "link_hq"
	// RecoveryFixKeepProfile keeps one marked profile and removes the
	// assistant markers from the others, which stay as ordinary agents.
	RecoveryFixKeepProfile RecoveryFixKind = "keep_profile"
	// RecoveryFixKeepHQ keeps one marked HQ (designating it) and removes the HQ
	// marker from the others, which stay as ordinary workspaces.
	RecoveryFixKeepHQ RecoveryFixKind = "keep_hq"
	// RecoveryFixDesignateHQ makes the marked HQ the designated Personal HQ.
	RecoveryFixDesignateHQ RecoveryFixKind = "designate_hq"
	// RecoveryFixAdoptEntry marks the HQ's lead agent as the assistant.
	RecoveryFixAdoptEntry RecoveryFixKind = "adopt_entry_profile"
	// RecoveryFixRestampProfile rewrites a profile's damaged markers to name
	// the HQ's assistant.
	RecoveryFixRestampProfile RecoveryFixKind = "restamp_profile"
	// RecoveryFixCreateBrief creates Daily Brief settings, schedule off.
	RecoveryFixCreateBrief RecoveryFixKind = "create_brief"
	// RecoveryFixClearDesignation stops treating a marker-less workspace as the
	// Personal HQ. The workspace itself is untouched.
	RecoveryFixClearDesignation RecoveryFixKind = "clear_designation"
)

// RecoveryDiagnosis explains a relationship that cannot be reconnected as it
// stands: what was found, the first check that failed, and the fixes that are
// safe for exactly that failure. Digest identifies the evidence the fixes were
// computed from; applying a fix requires it, so a fix is never applied to
// records other than the ones the user reviewed.
type RecoveryDiagnosis struct {
	Issue       RecoveryIssue           `json:"issue,omitempty"`
	Profiles    []RecoveryProfileFact   `json:"profiles"`
	HQs         []RecoveryHQFact        `json:"hqs"`
	Designation RecoveryDesignationFact `json:"designation"`
	Fixes       []RecoveryFix           `json:"fixes"`
	Digest      string                  `json:"digest"`
}

// RecoveryProfileFact is one agent profile carrying an assistant marker.
type RecoveryProfileFact struct {
	Name         string     `json:"name"`
	AssistantID  string     `json:"assistant_id,omitempty"`
	MarkerValid  bool       `json:"marker_valid"`
	Orchestrator bool       `json:"orchestrator"`
	CreatedAt    *time.Time `json:"created_at,omitempty"`
}

// RecoveryHQFact is one workspace carrying a Personal HQ marker.
type RecoveryHQFact struct {
	WorkspaceID string   `json:"workspace_id"`
	Name        string   `json:"name"`
	AssistantID string   `json:"assistant_id,omitempty"`
	MarkerValid bool     `json:"marker_valid"`
	OwnedByUser bool     `json:"owned_by_user"`
	EntryAgents []string `json:"entry_agents"`
	Designated  bool     `json:"designated"`
}

// RecoveryDesignationFact is the workspace Ori currently treats as the
// Personal HQ, if any.
type RecoveryDesignationFact struct {
	WorkspaceID    string `json:"workspace_id,omitempty"`
	WorkspaceName  string `json:"workspace_name,omitempty"`
	Valid          bool   `json:"valid"`
	EntryAgentName string `json:"entry_agent_name,omitempty"`
}

// RecoveryFix is one offered fix. ID is opaque to clients: they send back the
// ID they were shown, never a target of their own.
type RecoveryFix struct {
	ID            string          `json:"id"`
	Kind          RecoveryFixKind `json:"kind"`
	ProfileName   string          `json:"profile_name,omitempty"`
	WorkspaceID   string          `json:"workspace_id,omitempty"`
	WorkspaceName string          `json:"workspace_name,omitempty"`
	Recommended   bool            `json:"recommended,omitempty"`
}

// RecoveryResolution is the outcome of one applied fix: either the
// relationship was reconnected (State), or the records still disagree about
// something else and Diagnosis explains the next step.
type RecoveryResolution struct {
	Applied   RecoveryFixKind    `json:"applied"`
	State     *State             `json:"-"`
	Diagnosis *RecoveryDiagnosis `json:"diagnosis,omitempty"`
}

// RecoveryRecordWriter performs the marker edits a fix needs. Implementations
// write the workspace marker to both the database and the workspace folder, and
// change only the named tags on a profile.
type RecoveryRecordWriter interface {
	// SetPersonalHQMarker names assistantID (and requestID) in the workspace's
	// HQ marker, creating the marker when it is absent and keeping its other
	// fields when it exists.
	SetPersonalHQMarker(ctx context.Context, workspaceID, assistantID, requestID string) error
	// ClearPersonalHQMarker removes the HQ marker from the workspace.
	ClearPersonalHQMarker(ctx context.Context, workspaceID string) error
	// SetProfileMarkers replaces any assistant markers on the profile with
	// exactly this pair.
	SetProfileMarkers(name, assistantID, hireRequestID string) error
	// ClearProfileMarkers removes every assistant marker from the profile.
	ClearProfileMarkers(name string) error
}

// RecoveryDesignationWriter changes the Personal HQ designation.
type RecoveryDesignationWriter interface {
	Replace(ctx context.Context, userID, workspaceID string) (*personalhq.Status, error)
	Clear(ctx context.Context, userID string) (*personalhq.Status, error)
}

// RecoveryProfileFinder reads any profile by name, marked or not, so a fix can
// offer the HQ's lead agent as the assistant.
type RecoveryProfileFinder interface {
	PersonalAssistantRecoveryProfileByName(name string) (RecoveryProfile, bool)
}

type recoveryResolver struct {
	records      RecoveryRecordWriter
	designations RecoveryDesignationWriter
	briefs       BriefConfigManager
	profiles     RecoveryProfileFinder
}

// WithResolver enables fixes. Without it, Diagnose still explains a blocked
// recovery but offers nothing to apply.
func (c *RecoveryCoordinator) WithResolver(
	records RecoveryRecordWriter,
	designations RecoveryDesignationWriter,
	briefs BriefConfigManager,
	profiles RecoveryProfileFinder,
) *RecoveryCoordinator {
	if c == nil || records == nil || designations == nil || briefs == nil || profiles == nil {
		return c
	}
	c.resolver = &recoveryResolver{records: records, designations: designations, briefs: briefs, profiles: profiles}
	return c
}

// RelationshipRecoveryResolver explains a blocked recovery and applies one
// reviewed fix at a time.
type RelationshipRecoveryResolver interface {
	Diagnose(ctx context.Context, userID string) (*RecoveryDiagnosis, error)
	Resolve(ctx context.Context, userID, fixID, digest string) (*RecoveryResolution, error)
}

var _ RelationshipRecoveryResolver = (*RecoveryCoordinator)(nil)

// Diagnose reads the recovery evidence and explains it. It never writes. A
// diagnosis with no issue means the records agree and the ordinary reconnect
// applies. It returns ErrConflict when the relationship exists (there is
// nothing to recover) and ErrNotFound when there is no evidence at all.
func (c *RecoveryCoordinator) Diagnose(ctx context.Context, userID string) (*RecoveryDiagnosis, error) {
	if c == nil || c.store == nil {
		return nil, errors.New("personal assistant: recovery service is unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	diagnosis, _, err := c.diagnoseLocked(ctx, userID)
	return diagnosis, err
}

// diagnoseLocked also returns the evidence it judged, so a fix is applied to
// the very read its digest was checked against.
func (c *RecoveryCoordinator) diagnoseLocked(ctx context.Context, userID string) (*RecoveryDiagnosis, *recoveryEvidence, error) {
	if err := c.requireMissingRelationship(ctx, userID); err != nil {
		return nil, nil, err
	}
	_, evidence, issue, err := c.evaluate(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	return c.buildDiagnosis(evidence, issue), evidence, nil
}

func (c *RecoveryCoordinator) requireMissingRelationship(ctx context.Context, userID string) error {
	userID, err := validateOpaqueID("user id", userID, true)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if _, getErr := c.store.GetState(ctx, userID); getErr == nil {
		return fmt.Errorf("%w: a relationship already exists", ErrConflict)
	} else if !errors.Is(getErr, ErrNotFound) {
		return getErr
	}
	return nil
}

// Resolve applies the fix the user chose from the diagnosis whose digest they
// saw, then reconnects the relationship if the records now agree. Evidence that
// changed since the review is ErrConflict and nothing is written; a fix ID the
// current diagnosis does not offer is ErrValidation.
func (c *RecoveryCoordinator) Resolve(ctx context.Context, userID, fixID, digest string) (*RecoveryResolution, error) {
	if c == nil || c.store == nil {
		return nil, errors.New("personal assistant: recovery service is unavailable")
	}
	if c.resolver == nil {
		return nil, errors.New("personal assistant: recovery fixes are unavailable")
	}
	fixID = strings.TrimSpace(fixID)
	digest = strings.TrimSpace(digest)
	if fixID == "" || digest == "" {
		return nil, fmt.Errorf("%w: a fix and the reviewed evidence are required", ErrValidation)
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	diagnosis, evidence, err := c.diagnoseLocked(ctx, userID)
	if err != nil {
		return nil, err
	}
	if diagnosis.Digest != digest {
		return nil, fmt.Errorf("%w: the assistant records changed since they were reviewed", ErrConflict)
	}
	var chosen *RecoveryFix
	for i := range diagnosis.Fixes {
		if diagnosis.Fixes[i].ID == fixID {
			chosen = &diagnosis.Fixes[i]
			break
		}
	}
	if chosen == nil {
		return nil, fmt.Errorf("%w: that fix is not offered for these records", ErrValidation)
	}
	if err := c.applyFix(ctx, evidence, *chosen); err != nil {
		return nil, err
	}
	// Bounded, field-only: stable IDs and the fix kind, no names or content.
	logger.Info("personal assistant: recovery fix applied", logger.Fields{
		"issue": string(diagnosis.Issue), "fix": string(chosen.Kind), "workspace_id": chosen.WorkspaceID,
	})

	candidate, after, issue, err := c.evaluate(ctx, userID)
	if err != nil {
		return nil, err
	}
	result := &RecoveryResolution{Applied: chosen.Kind}
	if issue != "" {
		result.Diagnosis = c.buildDiagnosis(after, issue)
		return result, nil
	}
	state, err := c.createRecovered(ctx, evidence.userID, candidate)
	if err != nil {
		return nil, err
	}
	result.State = state
	return result, nil
}

func (c *RecoveryCoordinator) applyFix(ctx context.Context, evidence *recoveryEvidence, fix RecoveryFix) error {
	r := c.resolver
	switch fix.Kind {
	case RecoveryFixLinkHQ:
		if len(evidence.profiles) != 1 {
			return fmt.Errorf("%w: link needs exactly one assistant profile", ErrConflict)
		}
		requestID := ""
		if hq, ok := evidence.workspace(fix.WorkspaceID); ok {
			requestID = hq.HQRequestID
		}
		if requestID == "" {
			requestID = c.newID()
		}
		return r.records.SetPersonalHQMarker(ctx, fix.WorkspaceID, evidence.profiles[0].AssistantID, requestID)
	case RecoveryFixKeepProfile:
		for _, profile := range evidence.profiles {
			if profile.Name == fix.ProfileName {
				continue
			}
			if err := r.records.ClearProfileMarkers(profile.Name); err != nil {
				return err
			}
		}
		return nil
	case RecoveryFixKeepHQ:
		for _, hq := range evidence.workspaces {
			if hq.ID == fix.WorkspaceID {
				continue
			}
			if err := r.records.ClearPersonalHQMarker(ctx, hq.ID); err != nil {
				return err
			}
		}
		if strings.TrimSpace(evidence.status.WorkspaceID) == fix.WorkspaceID && evidence.status.Valid {
			return nil
		}
		_, err := r.designations.Replace(ctx, evidence.userID, fix.WorkspaceID)
		return err
	case RecoveryFixDesignateHQ:
		_, err := r.designations.Replace(ctx, evidence.userID, fix.WorkspaceID)
		return err
	case RecoveryFixAdoptEntry, RecoveryFixRestampProfile:
		hq, ok := evidence.workspace(fix.WorkspaceID)
		if !ok || hq.AssistantID == "" {
			return fmt.Errorf("%w: the Personal HQ marker changed", ErrConflict)
		}
		return r.records.SetProfileMarkers(fix.ProfileName, hq.AssistantID, c.newID())
	case RecoveryFixCreateBrief:
		config, err := dailybrief.NormalizeConfig(dailybrief.Config{
			WorkspaceID: fix.WorkspaceID, UserID: evidence.userID, ScheduleEnabled: false,
		})
		if err != nil {
			return err
		}
		_, err = r.briefs.UpdateConfig(ctx, config)
		return err
	case RecoveryFixClearDesignation:
		_, err := r.designations.Clear(ctx, evidence.userID)
		return err
	default:
		return fmt.Errorf("%w: unknown fix", ErrValidation)
	}
}

func (e *recoveryEvidence) workspace(id string) (RecoveryWorkspace, bool) {
	for _, hq := range e.workspaces {
		if hq.ID == id {
			return hq, true
		}
	}
	return RecoveryWorkspace{}, false
}

func (c *RecoveryCoordinator) buildDiagnosis(evidence *recoveryEvidence, issue RecoveryIssue) *RecoveryDiagnosis {
	diagnosis := &RecoveryDiagnosis{
		Issue: issue, Profiles: []RecoveryProfileFact{}, HQs: []RecoveryHQFact{}, Fixes: []RecoveryFix{},
	}
	status := evidence.status
	designatedID := ""
	if status != nil {
		designatedID = strings.TrimSpace(status.WorkspaceID)
		diagnosis.Designation = RecoveryDesignationFact{
			WorkspaceID: designatedID, Valid: status.Valid,
			EntryAgentName: strings.TrimSpace(status.EntryAgentName),
		}
		if status.Workspace != nil {
			diagnosis.Designation.WorkspaceName = strings.TrimSpace(status.Workspace.Name)
		}
	}
	for _, profile := range evidence.profiles {
		fact := RecoveryProfileFact{
			Name: profile.Name, AssistantID: profile.AssistantID,
			MarkerValid:  profile.AssistantID != "" && profile.HireRequestID != "",
			Orchestrator: profile.Role == types.RoleOrchestrator,
		}
		if !profile.CreatedAt.IsZero() {
			created := profile.CreatedAt.UTC()
			fact.CreatedAt = &created
		}
		diagnosis.Profiles = append(diagnosis.Profiles, fact)
	}
	for _, hq := range evidence.workspaces {
		entries := make([]string, 0, len(hq.EntryAgents))
		for _, entry := range hq.EntryAgents {
			entries = append(entries, strings.TrimSpace(entry.Name))
		}
		diagnosis.HQs = append(diagnosis.HQs, RecoveryHQFact{
			WorkspaceID: hq.ID, Name: hq.Name, AssistantID: hq.AssistantID,
			MarkerValid: hq.PresentationValid && hq.HQRequestID != "",
			OwnedByUser: hq.OwnerUserID == evidence.userID, EntryAgents: entries,
			Designated: hq.ID != "" && hq.ID == designatedID,
		})
	}
	if issue != "" && c.resolver != nil {
		diagnosis.Fixes = c.fixesFor(evidence, issue)
	}
	diagnosis.Digest = evidence.digest(issue)
	return diagnosis
}

// fixesFor lists the fixes that are safe for exactly this issue. An issue with
// no safe automatic fix returns none; the client explains what to do instead.
func (c *RecoveryCoordinator) fixesFor(evidence *recoveryEvidence, issue RecoveryIssue) []RecoveryFix {
	fixes := []RecoveryFix{}
	profile, oneProfile := evidence.soleProfile()
	status := evidence.status
	designatedID := strings.TrimSpace(status.WorkspaceID)
	switch issue {
	case RecoveryIssueAssistantMismatch, RecoveryIssueHQMarkerInvalid:
		if hq, ok := evidence.soleOwnHQ(); ok && oneProfile {
			fixes = append(fixes, linkFix(hq.ID, hq.Name, profile.Name))
		}
	case RecoveryIssueDesignationWithoutHQ:
		if oneProfile && status.Valid && status.Workspace != nil &&
			strings.EqualFold(strings.TrimSpace(status.EntryAgentName), profile.Name) {
			fixes = append(fixes, linkFix(designatedID, strings.TrimSpace(status.Workspace.Name), profile.Name))
		}
		fixes = append(fixes, RecoveryFix{
			ID: string(RecoveryFixClearDesignation), Kind: RecoveryFixClearDesignation,
			WorkspaceID: designatedID, WorkspaceName: evidence.designatedName(),
		})
	case RecoveryIssueDesignationMismatch:
		if hq, ok := evidence.soleOwnHQ(); ok {
			fixes = append(fixes, RecoveryFix{
				ID: string(RecoveryFixDesignateHQ) + ":" + hq.ID, Kind: RecoveryFixDesignateHQ,
				WorkspaceID: hq.ID, WorkspaceName: hq.Name, Recommended: true,
			})
		}
	case RecoveryIssueProfileDuplicate:
		hq, oneHQ := evidence.soleOwnHQ()
		for _, candidate := range evidence.profiles {
			if candidate.AssistantID == "" || candidate.HireRequestID == "" || candidate.Role != types.RoleOrchestrator {
				continue
			}
			fixes = append(fixes, RecoveryFix{
				ID: string(RecoveryFixKeepProfile) + ":" + candidate.Name, Kind: RecoveryFixKeepProfile,
				ProfileName: candidate.Name, Recommended: oneHQ && candidate.AssistantID == hq.AssistantID,
			})
		}
	case RecoveryIssueProfileMissing:
		if len(evidence.workspaces) > 1 {
			fixes = append(fixes, evidence.keepHQFixes(profile.AssistantID)...)
			break
		}
		if fix, ok := c.entryProfileFix(evidence, RecoveryFixAdoptEntry, ""); ok {
			fixes = append(fixes, fix)
		}
	case RecoveryIssueProfileIncomplete:
		if len(evidence.profiles) == 1 {
			if fix, ok := c.entryProfileFix(evidence, RecoveryFixRestampProfile, evidence.profiles[0].Name); ok {
				fixes = append(fixes, fix)
			}
		}
	case RecoveryIssueHQDuplicate:
		fixes = append(fixes, evidence.keepHQFixes(profile.AssistantID)...)
	case RecoveryIssueBriefMissing:
		if hq, ok := evidence.soleOwnHQ(); ok {
			fixes = append(fixes, RecoveryFix{
				ID: string(RecoveryFixCreateBrief) + ":" + hq.ID, Kind: RecoveryFixCreateBrief,
				WorkspaceID: hq.ID, WorkspaceName: hq.Name, Recommended: true,
			})
		}
	}
	if len(fixes) == 1 {
		fixes[0].Recommended = true
	}
	return fixes
}

func linkFix(workspaceID, workspaceName, profileName string) RecoveryFix {
	return RecoveryFix{
		ID: string(RecoveryFixLinkHQ) + ":" + workspaceID, Kind: RecoveryFixLinkHQ,
		ProfileName: profileName, WorkspaceID: workspaceID, WorkspaceName: workspaceName, Recommended: true,
	}
}

// entryProfileFix offers the sole HQ's lead agent as the assistant. With a
// required name, the lead agent must be that profile (a damaged marker is only
// rewritten to name the HQ its own profile already leads).
func (c *RecoveryCoordinator) entryProfileFix(evidence *recoveryEvidence, kind RecoveryFixKind, required string) (RecoveryFix, bool) {
	hq, ok := evidence.soleOwnHQ()
	if !ok || !hq.PresentationValid || hq.AssistantID == "" || len(hq.EntryAgents) != 1 {
		return RecoveryFix{}, false
	}
	name := strings.TrimSpace(hq.EntryAgents[0].Name)
	if name == "" || (required != "" && !strings.EqualFold(name, required)) {
		return RecoveryFix{}, false
	}
	profile, found := c.resolver.profiles.PersonalAssistantRecoveryProfileByName(name)
	if !found || profile.Role != types.RoleOrchestrator {
		return RecoveryFix{}, false
	}
	return RecoveryFix{
		ID: string(kind) + ":" + hq.ID, Kind: kind, ProfileName: strings.TrimSpace(profile.Name),
		WorkspaceID: hq.ID, WorkspaceName: hq.Name, Recommended: true,
	}, true
}

func (e *recoveryEvidence) soleProfile() (RecoveryProfile, bool) {
	if len(e.profiles) != 1 {
		return RecoveryProfile{}, false
	}
	p := e.profiles[0]
	if p.Name == "" || p.AssistantID == "" || p.HireRequestID == "" || p.Role != types.RoleOrchestrator {
		return RecoveryProfile{}, false
	}
	return p, true
}

func (e *recoveryEvidence) soleOwnHQ() (RecoveryWorkspace, bool) {
	if len(e.workspaces) != 1 {
		return RecoveryWorkspace{}, false
	}
	hq := e.workspaces[0]
	if hq.ID == "" || hq.OwnerUserID != e.userID {
		return RecoveryWorkspace{}, false
	}
	return hq, true
}

func (e *recoveryEvidence) designatedName() string {
	if e.status != nil && e.status.Workspace != nil {
		return strings.TrimSpace(e.status.Workspace.Name)
	}
	return ""
}

// keepHQFixes offers each of the user's own valid HQ folders. The designated
// one is recommended; failing that, the one naming the profile's assistant.
func (e *recoveryEvidence) keepHQFixes(assistantID string) []RecoveryFix {
	designatedID := strings.TrimSpace(e.status.WorkspaceID)
	fixes := []RecoveryFix{}
	recommended := ""
	for _, hq := range e.workspaces {
		if hq.ID != "" && hq.ID == designatedID {
			recommended = hq.ID
		}
	}
	if recommended == "" && assistantID != "" {
		for _, hq := range e.workspaces {
			if hq.AssistantID == assistantID {
				recommended = hq.ID
				break
			}
		}
	}
	for _, hq := range e.workspaces {
		if hq.ID == "" || hq.OwnerUserID != e.userID || !hq.PresentationValid {
			continue
		}
		fixes = append(fixes, RecoveryFix{
			ID: string(RecoveryFixKeepHQ) + ":" + hq.ID, Kind: RecoveryFixKeepHQ,
			WorkspaceID: hq.ID, WorkspaceName: hq.Name, Recommended: hq.ID == recommended,
		})
	}
	return fixes
}

// digest fingerprints every identity the verdict and its fixes depend on, so
// Resolve can refuse a fix reviewed against records that have since changed.
func (e *recoveryEvidence) digest(issue RecoveryIssue) string {
	type profileKey struct {
		Name, AssistantID, HireRequestID string
		Role                             types.AgentRole
	}
	type workspaceKey struct {
		ID, Name, OwnerUserID, AssistantID, HQRequestID string
		PresentationValid                               bool
		EntryAgents                                     []RecoveryEntryAgent
	}
	payload := struct {
		Issue       RecoveryIssue
		Profiles    []profileKey
		Workspaces  []workspaceKey
		Designation string
		Valid       bool
		EntryID     string
		BriefGone   bool
	}{Issue: issue, BriefGone: e.briefMissing}
	for _, p := range e.profiles {
		payload.Profiles = append(payload.Profiles, profileKey{p.Name, p.AssistantID, p.HireRequestID, p.Role})
	}
	sort.Slice(payload.Profiles, func(i, j int) bool { return payload.Profiles[i].Name < payload.Profiles[j].Name })
	for _, w := range e.workspaces {
		// A conversion, so a field added to RecoveryWorkspace fails to compile
		// here until the digest decides whether it covers it.
		payload.Workspaces = append(payload.Workspaces, workspaceKey(w))
	}
	sort.Slice(payload.Workspaces, func(i, j int) bool { return payload.Workspaces[i].ID < payload.Workspaces[j].ID })
	if e.status != nil {
		payload.Designation = strings.TrimSpace(e.status.WorkspaceID)
		payload.Valid = e.status.Valid
		payload.EntryID = strings.TrimSpace(e.status.EntryAgentInstanceID)
	}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
