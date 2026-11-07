package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/rosterstaffing"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/store"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

var errStaffingReviewChanged = errors.New("the selected staffing recommendation changed; review the current team or choose an agent manually")

func staffingDraftContext(owner string, build *personalassistant.WorkspaceBuildSession, draft personalassistant.BuildDraft, revision string) (string, error) {
	// Team edits are choices within this context, not a new purpose/target.
	draft.RoleStaffing, draft.TeamIntent = nil, nil
	draft.TemplateAgentOverrides, draft.TemplateAgentReview = nil, nil
	draft.ExistingAgentNames, draft.EntryAgentName, draft.CreateTemplateAgents = nil, "", nil
	// Use the creation owner's typed representation for nested options. The
	// browser keeps empty bootstrap fields, whereas the request DTO omits them;
	// those equivalent encodings must not stale an otherwise exact selection.
	material, err := json.Marshal(draft)
	if err != nil {
		return "", err
	}
	var request createWorkspaceRequest
	if err := json.Unmarshal(material, &request); err != nil {
		return "", err
	}
	material, err = json.Marshal(request)
	if err != nil {
		return "", err
	}
	var normalized personalassistant.BuildDraft
	if err := json.Unmarshal(material, &normalized); err != nil {
		return "", err
	}
	return digestJSON(struct {
		Owner, BuildID, Revision string
		ContextVersion           int64
		Draft                    personalassistant.BuildDraft
	}{owner, build.ID, revision, build.ContextVersion, normalized})
}

// selectBuildStaffing is called only by the explicit draft PATCH, never a model
// reply. A selection keeps its own source witness after advisory state becomes
// stale from a Team edit. Purpose, placement, blueprint or source changes still
// invalidate it. Clearing/choosing manually removes the selected intent.
func (h *Handler) selectBuildStaffing(ctx context.Context, owner string, build *personalassistant.WorkspaceBuildSession, draft personalassistant.BuildDraft, validation buildValidation) (*personalassistant.BuildStaffingSelection, error) {
	var roles []roleStaffingInput
	if len(draft.RoleStaffing) == 0 {
		return nil, nil
	}
	if json.Unmarshal(draft.RoleStaffing, &roles) != nil {
		return nil, errStaffingReviewChanged
	}
	bearing := false
	for _, role := range roles {
		bearing = bearing || role.Recommendation != ""
	}
	if !bearing {
		return nil, nil
	}
	normalized, err := normalizeRoleStaffing(roles)
	if err != nil {
		return nil, errStaffingReviewChanged
	}
	if build.SelectedStaffing != nil && build.SelectedStaffing.OwnerUserID != owner {
		return nil, errStaffingReviewChanged
	}
	// An edit can keep a selected identity without renewing its authority.
	// Preserve its original context/source witness so goal/placement/source
	// changes save the user's draft, but the next review refuses stale intent.
	if prior := build.SelectedStaffing; prior != nil && prior.OwnerUserID == owner {
		carried := *prior
		carried.Choices = nil
		allCarried := true
		for _, fill := range roles {
			if fill.Recommendation == "" {
				continue
			}
			found := false
			for _, choice := range prior.Choices {
				if fill.RoleID == choice.RoleID && fill.Name == choice.Name && fill.Recommendation == choice.Handle {
					carried.Choices = append(carried.Choices, choice)
					found = true
					break
				}
			}
			allCarried = allCarried && found
		}
		if allCarried {
			return &carried, nil
		}
	}
	template, found := validation.currentTemplate(draft)
	// Managed/Home choices are integrated through their own review owner, not
	// silently downgraded into ordinary creation during this first consumer.
	if !found || template.AssistantProgram != nil || template.AssistantProject != nil || template.GroupRequirement != nil {
		return nil, errStaffingReviewChanged
	}
	revision, err := digestJSON(template)
	if err != nil {
		return nil, errStaffingReviewChanged
	}
	contextDigest, err := staffingDraftContext(owner, build, draft, revision)
	if err != nil {
		return nil, errStaffingReviewChanged
	}
	priorContext, err := staffingDraftContext(owner, build, build.Draft, revision)
	if err != nil || priorContext != contextDigest {
		return nil, errStaffingReviewChanged
	}
	if prior := build.SelectedStaffing; prior != nil && (prior.ContextDigest != contextDigest || prior.TemplateRevision != revision) {
		// Adding a new slot must not renew an unchanged old selection merely
		// because fresh advice happens to include that same profile again.
		for _, fill := range roles {
			for _, choice := range prior.Choices {
				if fill.Recommendation != "" && fill.RoleID == choice.RoleID && fill.Name == choice.Name && fill.Recommendation == choice.Handle {
					return nil, errStaffingReviewChanged
				}
			}
		}
	}
	selected := &personalassistant.BuildStaffingSelection{Version: 1, OwnerUserID: owner, ContextDigest: contextDigest, TemplateRevision: revision}
	declared := map[string]bool{}
	for _, role := range buildRoleDeclarations(template) {
		declared[role.RoleID] = true
	}
	for _, requested := range roles {
		if requested.Recommendation == "" {
			continue
		}
		requested = normalized[requested.RoleID]
		if !declared[requested.RoleID] {
			return nil, errStaffingReviewChanged
		}
		var chosen *personalassistant.BuildStaffingSelectedRole
		if prior := build.SelectedStaffing; prior != nil && prior.OwnerUserID == owner && prior.ContextDigest == contextDigest && prior.TemplateRevision == revision {
			for _, role := range prior.Choices {
				if role.RoleID == requested.RoleID && role.Name == requested.Name && role.Handle == requested.Recommendation {
					copy := role
					chosen = &copy
					break
				}
			}
		}
		advice := build.Staffing
		if chosen == nil && advice != nil && advice.ContextVersion == build.ContextVersion && advice.TemplateID == draft.TemplateID && advice.TemplateRevision == revision &&
			(advice.State == rosterstaffing.Recommended || advice.State == rosterstaffing.NeedsChoice) {
			for _, role := range advice.Roles {
				if role.RoleID != requested.RoleID || role.Scope != "workspace" || (role.State != rosterstaffing.Recommended && role.State != rosterstaffing.NeedsChoice) {
					continue
				}
				for _, choice := range role.Choices {
					if choice.Handle == requested.Recommendation && choice.Name == requested.Name && choice.Configured {
						chosen = &personalassistant.BuildStaffingSelectedRole{RoleID: role.RoleID, Scope: role.Scope, Handle: choice.Handle, Name: choice.Name, SourceWitness: choice.SourceWitness}
					}
				}
			}
		}
		if chosen == nil {
			return nil, errStaffingReviewChanged
		}
		selected.Choices = append(selected.Choices, *chosen)
	}
	if err := h.checkSelectedStaffingSources(ctx, selected); err != nil {
		return nil, err
	}
	return selected, nil
}

func staffingSourceSelections(selected *personalassistant.BuildStaffingSelection) []store.StaffingSelection {
	var sources []store.StaffingSelection
	if selected != nil {
		for _, role := range selected.Choices {
			sources = append(sources, store.StaffingSelection{Name: role.Name, Witness: role.SourceWitness})
		}
	}
	return sources
}

func (h *Handler) checkSelectedStaffingSources(ctx context.Context, selected *personalassistant.BuildStaffingSelection) error {
	if selected == nil {
		return nil
	}
	if h.workspaceBuild == nil || h.workspaceBuild.ProfileModelConfigured == nil {
		return errStaffingReviewChanged
	}
	// Registry-only readiness is checked outside the source fence. No model,
	// credential discovery or other external work is permitted in its callback.
	profiles := store.ReadStaffingSnapshot(ctx, h.agentStore)
	for _, choice := range selected.Choices {
		found := false
		for _, profile := range profiles.Profiles {
			if profile.Name == choice.Name && profile.Witness == choice.SourceWitness && !profile.Paused && h.workspaceBuild.ProfileModelConfigured(profile.Provider, profile.Model) {
				found = true
				break
			}
		}
		if !found {
			return errStaffingReviewChanged
		}
	}
	return store.WithStaffingProfiles(ctx, h.agentStore, staffingSourceSelections(selected), func(map[string]*agent.Agent) error { return nil })
}

func requestHasStaffingRecommendation(req createWorkspaceRequest) bool {
	for _, role := range req.RoleStaffing {
		if role.Recommendation != "" {
			return true
		}
	}
	return false
}

func selectedStaffingMatchesRequest(owner string, build *personalassistant.WorkspaceBuildSession, req createWorkspaceRequest, template projecttemplates.Template) bool {
	if build == nil || build.SelectedStaffing == nil {
		return !requestHasStaffingRecommendation(req)
	}
	selected := build.SelectedStaffing
	revision, err := digestJSON(template)
	if err != nil || revision != selected.TemplateRevision || selected.OwnerUserID != owner || req.TemplateID != build.Draft.TemplateID {
		return false
	}
	contextDigest, err := staffingDraftContext(owner, build, build.Draft, revision)
	if err != nil || contextDigest != selected.ContextDigest {
		return false
	}
	material, err := json.Marshal(req)
	var requestedDraft personalassistant.BuildDraft
	if err != nil || json.Unmarshal(material, &requestedDraft) != nil {
		return false
	}
	requestedContext, err := staffingDraftContext(owner, build, requestedDraft, revision)
	if err != nil || requestedContext != contextDigest {
		return false
	}
	var expected []roleStaffingInput
	if json.Unmarshal(build.Draft.RoleStaffing, &expected) != nil {
		return false
	}
	left, err := normalizeRoleStaffing(expected)
	if err != nil {
		return false
	}
	right, err := normalizeRoleStaffing(req.RoleStaffing)
	if err != nil || !reflect.DeepEqual(left, right) {
		return false
	}
	if req.Name != build.Draft.Name || req.Description != build.Draft.Description || req.ParentID != build.Draft.ParentID ||
		req.Mission == nil || *req.Mission != build.Draft.Mission || req.Kind == "group" || req.Blank || req.GroupRoster ||
		req.TemplatePath != "" || req.Location != "" || req.AnchorPath != "" || req.ProjectPath != "" || req.FolderSlug != "" ||
		len(req.ExistingAgentNames) != 0 || req.EntryAgentName != "" || req.TemplateAgentReview != nil || len(req.TemplateAgentOverrides) != 0 ||
		!reflect.DeepEqual(req.CreateTemplateAgents, build.Draft.CreateTemplateAgents) {
		return false
	}
	count := 0
	for _, role := range selected.Choices {
		fill, ok := right[role.RoleID]
		if !ok || fill.Mode != roleStaffingModeAssign || fill.Name != role.Name || fill.Recommendation != role.Handle || role.Scope != "workspace" {
			return false
		}
		count++
	}
	for _, role := range right {
		if role.Recommendation != "" {
			count--
		}
	}
	return count == 0
}

// Capture immutable server-held sources before acceptance invalidates late
// inference. The final source check and canonical binding write share a fence.
func (h *Handler) reviewedCreationStaffing(ctx context.Context, req createWorkspaceRequest, template projecttemplates.Template) (*personalassistant.BuildStaffingSelection, error) {
	if !requestHasStaffingRecommendation(req) {
		return nil, nil
	}
	if req.BuildSessionID == "" || req.BuildReviewVersion < 1 || creationOperationFrom(ctx) == nil || h.workspaceBuild == nil {
		return nil, errStaffingReviewChanged
	}
	owner, err := h.creationOperationOwner(ctx)
	if err != nil {
		return nil, errStaffingReviewChanged
	}
	doc, err := h.workspaceBuild.Store.Read(ctx, owner)
	if err != nil {
		return nil, errStaffingReviewChanged
	}
	build := doc.Session(req.BuildSessionID)
	if !ordinaryCreationBuildMatches(build, req) || !selectedStaffingMatchesRequest(owner, build, req, template) {
		return nil, errStaffingReviewChanged
	}
	if err := h.checkSelectedStaffingSources(ctx, build.SelectedStaffing); err != nil {
		return nil, errStaffingReviewChanged
	}
	for _, role := range req.RoleStaffing {
		if role.Mode != roleStaffingModeAssign {
			if _, strict := h.agentStore.(store.StaffingProfileCreator); !strict {
				return nil, errStaffingReviewChanged
			}
		}
	}
	return build.SelectedStaffing, nil
}

func (h *Handler) createWorkspaceWithStaffing(ctx context.Context, ws *session.Workspace, selected *personalassistant.BuildStaffingSelection, templateID string, owned []store.StaffingSelection) error {
	if selected == nil {
		// Manual creation keeps its existing selection rules, but canonical
		// root/legacy bindings share the cleanup fence too. Private-only manual
		// sources remain outside reusable-roster authority.
		var sources []store.StaffingSelection
		snapshot := store.ReadStaffingSnapshot(ctx, h.agentStore)
		seen := map[string]bool{}
		for _, instance := range ws.AgentInstances {
			if seen[instance.Name] {
				continue
			}
			seen[instance.Name] = true
			if _, exists := h.agentStore.GetAgent(instance.Name); !exists {
				return errStaffingReviewChanged
			}
			ownedSource := false
			for _, created := range owned {
				if created.Name == instance.Name {
					sources = append(sources, created)
					ownedSource = true
					break
				}
			}
			if ownedSource {
				continue
			}
			for _, profile := range snapshot.Profiles {
				if profile.Name == instance.Name {
					sources = append(sources, store.StaffingSelection{Name: profile.Name, Witness: profile.Witness})
					break
				}
			}
		}
		if len(sources) == 0 {
			return h.store.CreateWorkspace(ctx, ws)
		}
		err := store.WithStaffingProfiles(ctx, h.agentStore, sources, func(map[string]*agent.Agent) error { return h.store.CreateWorkspace(ctx, ws) })
		if errors.Is(err, store.ErrStaffingSourceChanged) {
			return errStaffingReviewChanged
		}
		return err
	}
	if ws.OwnerUserID != selected.OwnerUserID {
		return errStaffingReviewChanged
	}
	// Seeding a separate new helper may have taken time. Recheck the current
	// declaration and registry readiness immediately before taking the source
	// fence; neither resolver is invoked from its no-reentry callback.
	currentTemplate, templateErr := h.resolveProjectTemplate(templateID, "")
	revision, digestErr := digestJSON(currentTemplate)
	if templateErr != nil || digestErr != nil || revision != selected.TemplateRevision || h.checkSelectedStaffingSources(ctx, selected) != nil {
		return errStaffingReviewChanged
	}
	for _, choice := range selected.Choices {
		matches := 0
		for _, instance := range ws.AgentInstances {
			if instance.RoleID == choice.RoleID && instance.Name == choice.Name && instance.RoleSource == workspace.RoleSourceAssigned {
				matches++
			}
		}
		if matches != 1 {
			return errStaffingReviewChanged
		}
	}
	sources := staffingSourceSelections(selected)
	seen := map[string]bool{}
	for _, source := range sources {
		seen[source.Name] = true
	}
	for _, source := range owned {
		if seen[source.Name] {
			return errStaffingReviewChanged
		}
		sources = append(sources, source)
		seen[source.Name] = true
	}
	// Other explicitly manual assignments in a mixed team retain their manual
	// choice contract, but still serialize binding against profile cleanup.
	snapshot := store.ReadStaffingSnapshot(ctx, h.agentStore)
	for _, instance := range ws.AgentInstances {
		if seen[instance.Name] {
			continue
		}
		if _, exists := h.agentStore.GetAgent(instance.Name); !exists {
			return errStaffingReviewChanged
		}
		for _, profile := range snapshot.Profiles {
			if profile.Name == instance.Name {
				sources = append(sources, store.StaffingSelection{Name: profile.Name, Witness: profile.Witness})
				seen[instance.Name] = true
				break
			}
		}
	}
	err := store.WithStaffingProfiles(ctx, h.agentStore, sources, func(map[string]*agent.Agent) error {
		return h.store.CreateWorkspace(ctx, ws)
	})
	if errors.Is(err, store.ErrStaffingSourceChanged) {
		return errStaffingReviewChanged
	}
	return err
}

func respondStaffingReviewChanged(w http.ResponseWriter) {
	creationOperationError(w, http.StatusConflict, "staffing_review_changed", errStaffingReviewChanged.Error())
}
