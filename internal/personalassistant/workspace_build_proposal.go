package personalassistant

import (
	"fmt"
	"strings"
	"time"
)

// BuildProposal is draft-only system activity, never a user turn or consent.
// IDs and revisions bind a bounded logical attempt; no paths or extra metadata
// are retained. Replaying an admitted attempt must not spend another model call.
type BuildProposal struct {
	ID             string    `json:"id"`
	FolderID       string    `json:"folder_id"`
	ContextVersion int64     `json:"context_version"`
	Status         string    `json:"status"`
	Attempt        int       `json:"attempt"`
	StartedAt      time.Time `json:"started_at,omitempty"`
}

const (
	BuildProposalMaxAttempts = 3
	// Two bounded 60-second provider calls plus completion allowance.
	BuildProposalTimeout = 150 * time.Second
)

// ExpireBuildProposal makes an interrupted attempt recoverable, not replayable.
func (s *WorkspaceBuildSession) ExpireBuildProposal(now time.Time) bool {
	if s.Proposal == nil || s.Proposal.Status != "running" || now.Sub(s.Proposal.StartedAt) <= BuildProposalTimeout {
		return false
	}
	s.Proposal.Status = "failed"
	s.InferenceID = ""
	s.CreateNow = false
	s.Touch(now)
	return true
}

// BuildEditableFields uses the model patch vocabulary, not raw request keys.
// Team is one decision including modes, overrides, vacancies and saved agents.
var buildEditableFields = []string{"blueprint_id", "name", "description", "goal", "inputs", "parent_id", "team", "tags", "color"}

func ValidBuildEditField(field string) bool {
	for _, allowed := range buildEditableFields {
		if field == allowed {
			return true
		}
	}
	return false
}

// ProtectLegacyBuildDraft is deliberately conservative: old sessions did not
// record whether a blank value was an edit. Explicit chat/manual edits still
// work; automatic proposals must not guess ownership from populated values.
func (s *WorkspaceBuildSession) ProtectLegacyBuildDraft() {
	s.EditedFields = make(map[string]int64, len(buildEditableFields))
	for _, field := range buildEditableFields {
		s.EditedFields[field] = s.Version
	}
	s.CreateNow = false
}

// InvalidateBuildInference fences late replies without discarding the draft.
// Lifecycle/purpose/real-turn changes also fence outstanding picker/fetch work.
func (s *WorkspaceBuildSession) InvalidateBuildInference(cancelAttachments bool) {
	s.ContextVersion++
	s.InferenceID = ""
	s.CreateNow, s.Ready = false, false
	s.Retry = nil
	if s.Proposal != nil {
		s.Proposal.Status = "invalidated"
	}
	if cancelAttachments {
		s.FolderRequestID, s.LinkRequestID = "", ""
	}
}

func buildOpaqueID(value string) bool {
	return value != "" && boundedText(value, 128) && !strings.ContainsAny(value, "/\\\r\n\t ")
}

func validateBuildProposalState(s WorkspaceBuildSession) error {
	invalid := func() error { return fmt.Errorf("%w: proposal state", ErrWorkspaceBuildInvalid) }
	if s.ContextVersion < 0 || len(s.EditedFields) > len(buildEditableFields) {
		return invalid()
	}
	for field, revision := range s.EditedFields {
		if !ValidBuildEditField(field) || revision < 1 || revision > s.Version {
			return invalid()
		}
	}
	for _, id := range []string{s.InferenceID, s.FolderRequestID, s.LinkRequestID} {
		if id != "" && !buildOpaqueID(id) {
			return invalid()
		}
	}
	p := s.Proposal
	if p == nil {
		return nil
	}
	if !buildOpaqueID(p.ID) || !buildOpaqueID(p.FolderID) || p.ContextVersion < 0 || p.ContextVersion > s.ContextVersion ||
		p.Attempt < 0 || p.Attempt > BuildProposalMaxAttempts {
		return invalid()
	}
	switch p.Status {
	case "pending":
		if p.Attempt != 0 || !p.StartedAt.IsZero() {
			return invalid()
		}
	case "running", "ready", "clarifying", "failed":
		if p.Attempt == 0 || p.StartedAt.IsZero() {
			return invalid()
		}
	case "invalidated":
	default:
		return invalid()
	}
	if p.Status == "running" && (s.InferenceID == "" || p.ContextVersion != s.ContextVersion || s.Status != WorkspaceBuildOpen) {
		return invalid()
	}
	return nil
}
