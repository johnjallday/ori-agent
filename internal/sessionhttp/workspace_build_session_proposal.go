package sessionhttp

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
)

const buildIntentSuggestFolder = "suggest_folder"

func validBuildOperationID(id string) bool {
	_, err := uuid.Parse(id)
	return err == nil
}

func validBuildTurnRequest(req buildTurnRequest) bool {
	if req.Intent == buildIntentSuggestFolder {
		_, err := uuid.Parse(req.ProposalID)
		return err == nil && req.Version > 0 && req.Attempt > 0 && req.Attempt <= personalassistant.BuildProposalMaxAttempts &&
			req.Text == "" && req.ChoiceID == "" && !req.Auto
	}
	return req.Intent == "" && req.ProposalID == "" && req.Attempt == 0 && !req.RetryProposal && (req.Text == "") != (req.ChoiceID == "")
}

// stageBuildProposal runs inside the existing turn admission mutation. A replay
// observes the admitted operation even when its original version is now stale.
func stageBuildProposal(s *personalassistant.WorkspaceBuildSession, req buildTurnRequest, now time.Time) (bool, error) {
	p := s.Proposal
	if p == nil || p.ID != req.ProposalID || s.Folder == nil || p.FolderID != s.Folder.Observation.ID {
		return false, errBuildTurn{http.StatusConflict, "proposal"}
	}
	if p.Status == "running" && now.Sub(p.StartedAt) > personalassistant.BuildProposalTimeout {
		p.Status = "failed"
		s.InferenceID = ""
		s.CreateNow = false
		s.Touch(now)
	}
	if req.Attempt == p.Attempt {
		return false, nil
	}
	if req.Version != s.Version {
		return false, errBuildTurn{http.StatusConflict, "version"}
	}
	if req.RetryProposal != (p.Status != "pending") || req.Attempt != p.Attempt+1 || req.Attempt > personalassistant.BuildProposalMaxAttempts ||
		(p.Status != "pending" && p.Status != "failed" && p.Status != "invalidated") ||
		s.FolderRequestID != "" || s.LinkRequestID != "" || s.Setup != nil || s.Preparation != nil {
		return false, errBuildTurn{http.StatusConflict, "proposal"}
	}
	p.Attempt, p.Status, p.StartedAt, p.ContextVersion = req.Attempt, "running", now, s.ContextVersion
	s.CreateNow, s.Ready = false, false
	s.Retry, s.PendingQuestion, s.Applied = nil, nil, nil
	return true, nil
}

// Attachment changes keep a not-yet-admitted folder proposal pending. A late
// link may enrich that first snapshot, but never automatically rerun a model.
func invalidateBuildAttachment(s *personalassistant.WorkspaceBuildSession) {
	pending := s.Proposal != nil && s.Proposal.Status == "pending" && s.Proposal.Attempt == 0
	s.InvalidateBuildInference(false)
	if pending {
		s.Proposal.Status = "pending"
		s.Proposal.ContextVersion = s.ContextVersion
	}
}

func (h *Handler) claimBuildAttachment(ctx context.Context, userID, id string, version int64, claim string, folder bool) (string, error) {
	if claim == "" {
		claim = uuid.NewString()
	}
	_, err := h.workspaceBuild.Store.Mutate(ctx, userID, func(doc *personalassistant.WorkspaceBuildDocument) error {
		s := doc.Session(id)
		if s == nil {
			return errBuildTurn{http.StatusNotFound, "not_found"}
		}
		if s.Status != personalassistant.WorkspaceBuildOpen {
			return errBuildTurn{http.StatusConflict, "closed"}
		}
		if s.Version != version {
			return errBuildTurn{http.StatusConflict, "version"}
		}
		invalidateBuildAttachment(s)
		if folder {
			s.FolderRequestID = claim
		} else {
			s.LinkRequestID = claim
		}
		s.Touch(h.workspaceBuild.Store.Now())
		return nil
	})
	return claim, err
}

// An observation error/cancellation releases only its own claim. Use a bounded
// independent context so a disconnected request cannot leave the form busy.
func (h *Handler) releaseBuildAttachment(ctx context.Context, userID, id, claim string, folder bool) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = h.workspaceBuild.Store.Mutate(ctx, userID, func(doc *personalassistant.WorkspaceBuildDocument) error {
		s := doc.Session(id)
		if s == nil {
			return nil
		}
		if folder && s.FolderRequestID == claim {
			s.FolderRequestID = ""
			s.Touch(h.workspaceBuild.Store.Now())
		} else if !folder && s.LinkRequestID == claim {
			s.LinkRequestID = ""
			s.Touch(h.workspaceBuild.Store.Now())
		}
		return nil
	})
}

func genericBuildFolder(s *personalassistant.WorkspaceBuildSession) bool {
	if s == nil || s.Folder == nil {
		return false
	}
	r := s.Folder.Recognition
	return r == nil || r.Verdict == personalassistant.BuildFolderUnrecognized
}

func (h *Handler) buildProposalSelectionCurrent(ctx context.Context, userID string, projection *personalassistant.Projection, s *personalassistant.WorkspaceBuildSession) bool {
	if !genericBuildFolder(s) || h.workspaceBuild.Folders == nil {
		return false
	}
	root, err := h.workspaceBuild.Folders.ResolveRoot(ctx, buildFolderTarget(userID, projection, s.ID), s.Folder.Observation.ID)
	return err == nil && strings.TrimSpace(root) != ""
}

// Completion protects durable ownership as well as changes since admission.
// An owned team also protects its blueprint: changing purpose resets team keys.
func withoutOwnedBuildFields(outcome buildOutcome, before, after personalassistant.WorkspaceBuildSession, automatic bool) buildOutcome {
	if outcome.kind != "reply" {
		return outcome
	}
	blocked := map[string]bool{}
	for field, revision := range after.EditedFields {
		blocked[field] = automatic || revision > before.EditedFields[field]
	}
	if blocked[buildFieldTeam] || blocked[buildFieldInputs] || blocked[buildFieldParent] {
		blocked[buildFieldBlueprint] = true
	}
	kept := []string{}
	for _, field := range []string{buildFieldBlueprint, buildFieldName, buildFieldDescription, buildFieldGoal, buildFieldInputs, buildFieldParent, buildFieldAskFolder, buildFieldTeam, buildFieldTags, buildFieldColor} {
		if outcome.reply.Patch.sets(field) && !blocked[field] {
			kept = append(kept, field)
		}
	}
	if len(kept) == 0 {
		kept = []string{"none"}
	}
	outcome.reply.Patch.Set = kept
	return outcome
}

func changedBuildFields(before, after personalassistant.BuildDraft, beforeTeam, afterTeam any) []string {
	changed := map[string]bool{
		buildFieldBlueprint:   before.Blank != after.Blank || before.TemplateID != after.TemplateID,
		buildFieldName:        before.Name != after.Name,
		buildFieldDescription: before.Description != after.Description,
		buildFieldGoal:        before.Mission != after.Mission,
		buildFieldInputs:      !sameRawMap(before.BlueprintInputs, after.BlueprintInputs),
		buildFieldParent:      before.ParentID != after.ParentID,
		buildFieldTags:        !reflect.DeepEqual(before.Tags, after.Tags),
		buildFieldColor:       before.Color != after.Color,
		buildFieldTeam: !reflect.DeepEqual(beforeTeam, afterTeam) || !reflect.DeepEqual(
			[]any{before.TeamIntent, before.RoleStaffing, before.TemplateAgentOverrides, before.TemplateAgentReview, before.ExistingAgentNames, before.EntryAgentName, before.CreateTemplateAgents},
			[]any{after.TeamIntent, after.RoleStaffing, after.TemplateAgentOverrides, after.TemplateAgentReview, after.ExistingAgentNames, after.EntryAgentName, after.CreateTemplateAgents}),
	}
	fields := []string{}
	for field, yes := range changed {
		if yes {
			fields = append(fields, field)
		}
	}
	return fields
}

// Pause is scoped to what this tab saw. A delayed close cannot invalidate a
// newer proposal/turn from another tab, nor pause accepted setup/preparation.
func (h *Handler) pauseWorkspaceBuild(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		ProposalID     string `json:"proposal_id"`
		InferenceID    string `json:"inference_id"`
		Version        int64  `json:"version"`
		ContextVersion int64  `json:"context_version"`
		AttachmentID   string `json:"attachment_id,omitempty"`
		Attempt        int    `json:"attempt,omitempty"`
	}
	if !decodeBuildBody(w, r, &req) {
		return
	}
	if h.workspaceBuild == nil {
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	owner, err := h.buildPreparationOwner(r.Context())
	if err != nil {
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	var result personalassistant.WorkspaceBuildSession
	paused, draftChanged := false, false
	_, err = h.workspaceBuild.Store.Mutate(r.Context(), owner, func(doc *personalassistant.WorkspaceBuildDocument) error {
		paused, draftChanged = false, false
		s := doc.Session(id)
		if s == nil {
			return errBuildTurn{http.StatusNotFound, "not_found"}
		}
		for _, revision := range s.EditedFields {
			if revision > req.Version {
				draftChanged = true
			}
		}
		matches := req.Version == s.Version || (req.InferenceID != "" && req.InferenceID == s.InferenceID)
		if s.Proposal != nil && req.ProposalID == s.Proposal.ID && req.ContextVersion == s.ContextVersion && req.Attempt == s.Proposal.Attempt {
			matches = true // close may race the initial admission's response
		}
		if s.Status == personalassistant.WorkspaceBuildOpen {
			if matches {
				paused = true
				s.InvalidateBuildInference(true)
				s.Touch(h.workspaceBuild.Store.Now())
			} else if req.AttachmentID != "" && (req.AttachmentID == s.FolderRequestID || req.AttachmentID == s.LinkRequestID) {
				paused = true
				// The close may race observation admission, before its response
				// carries the new version. Cancel this tab's operation only.
				s.InvalidateBuildInference(false)
				if s.FolderRequestID == req.AttachmentID {
					s.FolderRequestID = ""
				}
				if s.LinkRequestID == req.AttachmentID {
					s.LinkRequestID = ""
				}
				s.Touch(h.workspaceBuild.Store.Now())
			}
		}
		result = cloneBuildSession(*s)
		result.CreateNow = false
		return nil
	})
	if err != nil {
		respondBuildMutationError(w, err, id, "paused")
		return
	}
	h.respondBuildSession(r.Context(), w, http.StatusOK, &result, map[string]any{"paused": paused, "draft_changed": draftChanged})
}
