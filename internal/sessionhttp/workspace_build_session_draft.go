package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

type patchBuildDraftRequest struct {
	Draft     personalassistant.BuildDraft `json:"draft"`
	TeamState json.RawMessage              `json:"team_state,omitempty"`
	Version   int64                        `json:"version"`
	// Step is the wizard step the user has reached, for resuming there.
	Step int `json:"step,omitempty"`
	// Sync marks a write that only reports what applying the assistant's own
	// turn did to the form (the team keys it derived, the step it reached).
	// It is recorded, but never said back as the user's edit.
	Sync bool `json:"sync,omitempty"`
}

const buildMaxTeamState = 32 * 1024

// patchWorkspaceBuildDraft records the user's own edits to the form (FR16).
// The draft is the wizard's create payload, so the session and the form stay
// one draft. Each change is said once, in plain words, in the transcript,
// where the assistant sees it on its next turn (FR17).
func (h *Handler) patchWorkspaceBuildDraft(w http.ResponseWriter, r *http.Request, id string) {
	var req patchBuildDraftRequest
	if !decodeBuildBody(w, r, &req) {
		return
	}
	if len(req.TeamState) > buildMaxTeamState || req.Step < 0 || req.Step > 4 {
		_ = orihttp.RespondBadRequest(w, "Invalid build draft")
		return
	}
	if !json.Valid(req.TeamState) && len(req.TeamState) > 0 {
		_ = orihttp.RespondBadRequest(w, "Invalid build draft")
		return
	}
	ctx := r.Context()
	userID := h.workspaceBuildUserID(ctx)
	deps := h.workspaceBuild
	if deps == nil {
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	catalog, _ := deps.Catalog(ctx, userID)
	validation := buildValidation{catalog: catalog, groups: h.workspaceBuildGroups(ctx, userID)}
	draft, err := normalizeFormDraft(req.Draft, validation)
	if err != nil {
		_ = orihttp.RespondBadRequest(w, err.Error())
		return
	}

	var result personalassistant.WorkspaceBuildSession
	blueprintChanged := false
	_, err = deps.Store.Mutate(ctx, userID, func(doc *personalassistant.WorkspaceBuildDocument) error {
		session := doc.Session(id)
		if session == nil {
			return errBuildTurn{http.StatusNotFound, "not_found"}
		}
		if session.Status != personalassistant.WorkspaceBuildOpen {
			return errBuildTurn{http.StatusConflict, "closed"}
		}
		if session.Version != req.Version {
			return errBuildTurn{http.StatusConflict, "version"}
		}
		now := deps.Store.Now()
		before := session.Draft
		var lines []string
		if !req.Sync {
			lines = describeFormEdit(before, draft, validation)
			blueprintChanged = before.Blank != draft.Blank || before.TemplateID != draft.TemplateID
		}
		session.Draft = draft
		if len(req.TeamState) > 0 {
			session.TeamState = append(json.RawMessage(nil), req.TeamState...)
		}
		if blueprintChanged {
			// The assistant's team belonged to the previous blueprint.
			session.TeamPatch = nil
			template, ok := validation.currentTemplate(draft)
			session.NeedsHome = nil
			if ok && needsHome(template) {
				session.NeedsHome = &personalassistant.BuildNeedsHome{Label: needsHomeLabel(template.GroupRequirement.DefaultHomeName)}
			}
		}
		session.FurthestStep = max(session.FurthestStep, req.Step)
		if len(lines) > 0 {
			session.Append(personalassistant.BuildTranscriptEntry{
				Role: personalassistant.BuildRoleForm, Text: joinFormLines(lines), At: now,
			})
		}
		session.Applied = nil
		session.Touch(now)
		result = *session
		return nil
	})
	if err != nil {
		var turnErr errBuildTurn
		if errors.As(err, &turnErr) {
			_ = orihttp.RespondJSON(w, turnErr.status, map[string]any{"error": buildErrorText(turnErr.code), "code": turnErr.code})
			return
		}
		logger.Warn("Workspace build draft could not be saved", logger.Fields{"session_id": id, "error": err})
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	respondBuildSession(w, http.StatusOK, &result, map[string]any{"blueprint_changed": blueprintChanged})
}

func needsHomeLabel(name string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	return "its own group"
}

// normalizeFormDraft bounds the user's draft the way the store requires and
// keeps a blueprint only if this user can create from it.
func normalizeFormDraft(draft personalassistant.BuildDraft, validation buildValidation) (personalassistant.BuildDraft, error) {
	draft.Name = strings.TrimSpace(draft.Name)
	draft.Description = strings.TrimSpace(draft.Description)
	if utf8.RuneCountInString(draft.Name) > 200 || utf8.RuneCountInString(draft.Description) > personalassistant.WorkspaceBuildMaxText {
		return draft, errors.New("The draft is too long")
	}
	if draft.TemplateID = strings.TrimSpace(draft.TemplateID); draft.TemplateID != "" {
		if _, ok := validation.lookup(draft.TemplateID); !ok {
			// A blueprint this user cannot see is not kept.
			draft.TemplateID = ""
		}
	}
	if len(draft.Tags) > buildMaxTags {
		draft.Tags = draft.Tags[:buildMaxTags]
	}
	for _, raw := range []json.RawMessage{draft.WorkspaceBootstrap, draft.ProjectConnection, draft.TeamIntent, draft.RoleStaffing, draft.TemplateAgentOverrides, draft.TemplateAgentReview} {
		if len(raw) > 0 && !json.Valid(raw) {
			return draft, errors.New("Invalid build draft")
		}
	}
	return draft, nil
}

// describeFormEdit says what the user changed, in plain words: "You switched
// the blueprint to Code Project", "You renamed it to Field Notes".
func describeFormEdit(before, after personalassistant.BuildDraft, validation buildValidation) []string {
	lines := []string{}
	if before.Blank != after.Blank || before.TemplateID != after.TemplateID {
		switch {
		case after.Blank:
			lines = append(lines, "You switched the blueprint to Blank")
		case after.TemplateID != "":
			name := after.TemplateID
			if entry, ok := validation.lookup(after.TemplateID); ok {
				name = entry.Template.Name
			}
			lines = append(lines, "You switched the blueprint to "+name)
		}
	}
	if before.Name != after.Name && after.Name != "" {
		lines = append(lines, fmt.Sprintf("You renamed it to %s", after.Name))
	}
	if before.Description != after.Description {
		lines = append(lines, "You edited the description")
	}
	if before.ParentID != after.ParentID {
		if after.ParentID == "" {
			lines = append(lines, "You took it out of its group")
		} else {
			name := "a group"
			if group, ok := validation.group(after.ParentID); ok {
				name = group.Name
			}
			lines = append(lines, "You put it in "+name)
		}
	}
	if !sameRawMap(before.BlueprintInputs, after.BlueprintInputs) {
		lines = append(lines, "You changed the blueprint's settings")
	}
	if (len(before.ProjectConnection) > 0) != (len(after.ProjectConnection) > 0) {
		if len(after.ProjectConnection) > 0 {
			lines = append(lines, "You linked a folder")
		} else {
			lines = append(lines, "You unlinked the folder")
		}
	} else if !bytes.Equal(before.ProjectConnection, after.ProjectConnection) {
		lines = append(lines, "You chose a different folder")
	}
	lines = append(lines, describeTeamEdit(before, after)...)
	if before.Color != after.Color {
		lines = append(lines, "You changed the color")
	}
	if strings.Join(before.Tags, "\x00") != strings.Join(after.Tags, "\x00") {
		lines = append(lines, "You changed the tags")
	}
	return lines
}

type staffingLine struct {
	RoleID string `json:"role_id"`
	Mode   string `json:"mode"`
	Name   string `json:"name"`
}

func staffingNames(raw json.RawMessage) map[string]string {
	var entries []staffingLine
	_ = json.Unmarshal(raw, &entries)
	out := map[string]string{}
	for _, entry := range entries {
		out[entry.RoleID] = entry.Name
	}
	return out
}

// describeTeamEdit names people added or removed from the team, falling back
// to "You changed the team".
func describeTeamEdit(before, after personalassistant.BuildDraft) []string {
	if bytes.Equal(before.RoleStaffing, after.RoleStaffing) &&
		strings.Join(before.ExistingAgentNames, "\x00") == strings.Join(after.ExistingAgentNames, "\x00") {
		return nil
	}
	previous := map[string]bool{}
	for _, name := range staffingNames(before.RoleStaffing) {
		previous[strings.ToLower(name)] = true
	}
	for _, name := range before.ExistingAgentNames {
		previous[strings.ToLower(name)] = true
	}
	current := map[string]bool{}
	names := []string{}
	for _, name := range staffingNames(after.RoleStaffing) {
		current[strings.ToLower(name)] = true
		names = append(names, name)
	}
	for _, name := range after.ExistingAgentNames {
		current[strings.ToLower(name)] = true
		names = append(names, name)
	}
	sort.Strings(names)
	lines := []string{}
	for _, name := range names {
		if !previous[strings.ToLower(name)] {
			lines = append(lines, "You added "+name)
		}
	}
	removed := 0
	for name := range previous {
		if !current[name] {
			removed++
		}
	}
	if removed > 0 || len(lines) == 0 {
		lines = append(lines, "You changed the team")
	}
	return lines
}

func sameRawMap(a, b map[string]json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if !bytes.Equal(bytes.TrimSpace(value), bytes.TrimSpace(b[key])) {
			return false
		}
	}
	return true
}

// joinFormLines reads as one sentence: "You renamed it to Field Notes and
// edited the description."
func joinFormLines(lines []string) string {
	if len(lines) == 1 {
		return lines[0] + "."
	}
	rest := make([]string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		rest = append(rest, strings.TrimPrefix(line, "You "))
	}
	last := rest[len(rest)-1]
	if len(rest) == 1 {
		return fmt.Sprintf("%s and %s.", lines[0], last)
	}
	return fmt.Sprintf("%s, %s, and %s.", lines[0], strings.Join(rest[:len(rest)-1], ", "), last)
}

type abandonBuildRequest struct {
	Version int64 `json:"version,omitempty"`
}

// abandonWorkspaceBuildSession ends a build without creating anything: Start
// over, or Discard from Today. Abandoning twice is harmless.
func (h *Handler) abandonWorkspaceBuildSession(w http.ResponseWriter, r *http.Request, id string) {
	var req abandonBuildRequest
	if !decodeBuildBody(w, r, &req) {
		return
	}
	ctx := r.Context()
	deps := h.workspaceBuild
	if deps == nil {
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	var result personalassistant.WorkspaceBuildSession
	_, err := deps.Store.Mutate(ctx, h.workspaceBuildUserID(ctx), func(doc *personalassistant.WorkspaceBuildDocument) error {
		session := doc.Session(id)
		if session == nil {
			return errBuildTurn{http.StatusNotFound, "not_found"}
		}
		if session.Status == personalassistant.WorkspaceBuildOpen {
			session.Status = personalassistant.WorkspaceBuildAbandoned
			session.PendingQuestion = nil
			session.Touch(deps.Store.Now())
		}
		result = *session
		return nil
	})
	if err != nil {
		var turnErr errBuildTurn
		if errors.As(err, &turnErr) {
			_ = orihttp.RespondJSON(w, turnErr.status, map[string]any{"error": buildErrorText(turnErr.code), "code": turnErr.code})
			return
		}
		respondBuildUnavailable(w, buildReasonAssistantNotReady)
		return
	}
	logger.Info("Workspace build abandoned", logger.Fields{"session_id": id})
	respondBuildSession(w, http.StatusOK, &result, nil)
}

func workspaceSlugFor(name string) string {
	return agentworkspace.Slugify(name)
}

// workspaceSlugOccupied applies the same occupancy the create path's
// folder_slug conflict uses: registered workspaces and the folder store's.
func (h *Handler) workspaceSlugOccupied(ctx context.Context, slug, excludeID string) bool {
	_, taken := h.occupiedWorkspaceSlugs(ctx, excludeID)[strings.ToLower(slug)]
	return taken
}
