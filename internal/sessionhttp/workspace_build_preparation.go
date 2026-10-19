package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/agent"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/session"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type buildPreparationRequest struct {
	Version        int64  `json:"version"`
	Monitoring     bool   `json:"monitoring"`
	ReviewRevision string `json:"review_revision,omitempty"`
}

type buildPreparationContextKey struct{}
type buildPreparationContext struct {
	owner   string
	build   personalassistant.WorkspaceBuildSession
	request buildPreparationRequest
	confirm bool
	claim   *personalassistant.BuildPreparationClaim
	janitor *filejanitor.Service
}

func buildPreparationContextFrom(ctx context.Context) *buildPreparationContext {
	value, _ := ctx.Value(buildPreparationContextKey{}).(*buildPreparationContext)
	return value
}

func (h *Handler) buildPreparationOwner(ctx context.Context) (string, error) {
	resolve := h.currentUserID
	if h.workspaceBuild != nil && h.workspaceBuild.CurrentUserID != nil {
		resolve = h.workspaceBuild.CurrentUserID
	}
	if resolve == nil {
		return userprofile.LocalUserID, nil
	}
	owner, err := resolve(ctx)
	if err != nil || strings.TrimSpace(owner) == "" {
		return "", errors.New("owner unavailable")
	}
	return strings.TrimSpace(owner), nil
}

func respondBuildPreparationError(w http.ResponseWriter, code, message string, status int, details map[string]any) {
	w.Header().Set("Cache-Control", "no-store")
	_ = orihttp.RespondAPIError(w, status, &orihttp.APIError{Code: code, Message: message, Details: details})
}

// An ordinary create naming an accepted build may not race/recreate its exact
// operation. Unknown/ordinary settled builds retain their legacy behavior.
func (h *Handler) refuseClaimedBuildCreate(ctx context.Context, id string) error {
	if h.workspaceBuild == nil {
		return nil
	}
	owner, err := h.buildPreparationOwner(ctx)
	if err != nil {
		return err
	}
	doc, err := h.workspaceBuild.Store.Read(ctx, owner)
	if err != nil {
		return err
	}
	if build := doc.Session(id); build != nil && build.Preparation != nil {
		return errors.New("creation accepted")
	}
	return nil
}

func (h *Handler) postWorkspaceBuildPreparation(w http.ResponseWriter, r *http.Request, id string, confirm bool) {
	w.Header().Set("Cache-Control", "no-store")
	var request buildPreparationRequest
	if !decodeBuildBody(w, r, &request) {
		return
	}
	if request.Version < 1 || (confirm && len(request.ReviewRevision) != 64) || (!confirm && request.ReviewRevision != "") {
		respondBuildPreparationError(w, "invalid_review", "Review this build before confirming preparation.", http.StatusBadRequest, nil)
		return
	}
	deps := h.workspaceBuild
	if deps == nil || deps.FileJanitor == nil || h.workspaceTaskStore == nil {
		respondBuildPreparationError(w, "preparation_unavailable", "Reviewed File Janitor creation is unavailable. Keep the draft and use ordinary setup.", http.StatusServiceUnavailable, nil)
		return
	}
	janitor, automation := deps.FileJanitor()
	if janitor == nil {
		respondBuildPreparationError(w, "preparation_unavailable", "File Janitor is unavailable.", http.StatusServiceUnavailable, nil)
		return
	}
	owner, err := h.buildPreparationOwner(r.Context())
	if err != nil {
		respondBuildPreparationError(w, "owner_unavailable", "Your workspace identity is unavailable. Nothing was created.", http.StatusServiceUnavailable, nil)
		return
	}
	h.buildPreparationMu.Lock()
	defer h.buildPreparationMu.Unlock()
	doc, err := deps.Store.Read(r.Context(), owner)
	if err != nil {
		respondBuildPreparationError(w, "build_unavailable", "This build cannot be read. Keep it and try again.", http.StatusServiceUnavailable, nil)
		return
	}
	build := doc.Session(id)
	if build == nil {
		respondBuildPreparationError(w, "not_found", "Build not found.", http.StatusNotFound, nil)
		return
	}
	if claim := build.Preparation; claim != nil {
		if build.Status == personalassistant.WorkspaceBuildAbandoned {
			respondBuildPreparationError(w, "preparation_deferred", "This preparation was explicitly deferred. Open the workspace's current settings; its old confirmation cannot resume or create anything.", http.StatusConflict, map[string]any{"workspace_id": claim.WorkspaceID})
			return
		}
		if !confirm {
			_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "review": map[string]any{
				"state": "accepted", "version": claim.BuildVersion, "review_revision": claim.ReviewRevision,
				"monitoring": claim.Monitoring, "workspace_id": claim.WorkspaceID, "folder_slug": h.preparedBuildFolderSlug(owner, *build),
			}})
			return
		}
		if request.Version != claim.BuildVersion || request.ReviewRevision != claim.ReviewRevision || request.Monitoring != claim.Monitoring {
			respondBuildPreparationError(w, "review_changed", "Another review was accepted. Open the accepted workspace instead of creating again.", http.StatusConflict, nil)
			return
		}
		h.prepareCreatedBuild(w, r, owner, *build, janitor, automation)
		return
	}
	if build.Status != personalassistant.WorkspaceBuildOpen || build.Version != request.Version {
		respondBuildPreparationError(w, "review_changed", "The draft changed. Review its current name, team and folder.", http.StatusConflict, nil)
		return
	}
	if build.Folder == nil || !isReviewedJanitorBlueprint(build.Draft.TemplateID) || build.Draft.Blank {
		respondBuildPreparationError(w, "folder_review_unavailable", "Attach a folder to a built-in File Janitor draft, or continue with ordinary creation.", http.StatusConflict, nil)
		return
	}
	// Only a supported built-in with no Home-creation contract can enter this
	// preview hook: earlier normal creator gates may otherwise claim a Home.
	tpl, err := h.resolveProjectTemplate(build.Draft.TemplateID, "")
	if err != nil || !tpl.Builtin || tpl.GroupRequirement != nil {
		respondBuildPreparationError(w, "blueprint_changed", "This blueprint no longer supports reviewed File Janitor creation.", http.StatusConflict, nil)
		return
	}
	payload, err := buildPreparationCreatePayload(*build)
	if err != nil {
		respondBuildPreparationError(w, "invalid_draft", "The current draft cannot be created.", http.StatusBadRequest, nil)
		return
	}
	preparation := &buildPreparationContext{owner: owner, build: cloneBuildSession(*build), request: request, confirm: confirm, janitor: janitor}
	ctx := context.WithValue(r.Context(), buildPreparationContextKey{}, preparation)
	createRequest := r.Clone(ctx)
	createRequest.Body = io.NopCloser(bytes.NewReader(payload))
	createRequest.ContentLength = int64(len(payload))
	recorder := httptest.NewRecorder()
	h.createWorkspace(recorder, createRequest)
	if !confirm || preparation.claim == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
		return
	}
	// A claim was accepted before any create write. Do not infer failure or
	// allocate a new identity from a lost/failed response; observe its marker.
	build.Preparation = preparation.claim
	if recorder.Code >= 400 {
		respondBuildPreparationError(w, "creation_incomplete", "Creation stopped after acceptance. Resume this same build; do not create another workspace.", http.StatusConflict,
			map[string]any{"workspace_id": preparation.claim.WorkspaceID, "stage": "creation"})
		return
	}
	h.prepareCreatedBuild(w, r, owner, *build, janitor, automation)
}

func isReviewedJanitorBlueprint(id string) bool {
	return id == "file-janitor" || id == "downloads-janitor"
}

func buildPreparationCreatePayload(build personalassistant.WorkspaceBuildSession) ([]byte, error) {
	data, err := json.Marshal(build.Draft)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	// An explicitly cleared goal and an explicitly empty saved roster must not
	// acquire template/legacy defaults just because BuildDraft uses omitempty.
	payload["mission"] = build.Draft.Mission
	if build.Draft.ExistingAgentNames != nil {
		payload["existing_agent_names"] = build.Draft.ExistingAgentNames
	}
	payload["build_session_id"], payload["entry_point"] = build.ID, build.EntryPoint
	return json.Marshal(payload)
}

type buildPreparationMember struct {
	Name     string `json:"name"`
	Action   string `json:"action"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

type buildPreparationSummary struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Mission     string   `json:"mission"`
	ParentID    string   `json:"parent_id"`
	Color       string   `json:"color"`
	Tags        []string `json:"tags"`
}

// Called by the normal creator AFTER its team/placement/selection gates and
// BEFORE its first agent/workspace write. A preview ends here with no writes.
func (h *Handler) preflightBuildPreparation(w http.ResponseWriter, r *http.Request, req createWorkspaceRequest, tpl projecttemplates.Template, folder *createWorkspaceBuildFolderPlan, p *buildPreparationContext) (AssistantSetupCreationDescriptor, bool) {
	var descriptor AssistantSetupCreationDescriptor
	refuse := func(code, message string) (AssistantSetupCreationDescriptor, bool) {
		respondBuildPreparationError(w, code, message, http.StatusConflict, nil)
		return descriptor, false
	}
	if !tpl.Builtin || !isReviewedJanitorBlueprint(tpl.ID) || folder == nil || folder.sessionID != p.build.ID {
		return refuse("selection_expired", "The attached selection is unavailable. Attach the folder again before creating anything.")
	}
	if !req.teamIntentPresent || !req.roleStaffingPresent {
		return refuse("team_review_required", "Review the actual team on Edit setup before preparing this workspace.")
	}
	root, err := p.janitor.ReviewSelectedSetup("", folder.root, tpl.AutomationRecipes)
	if err != nil {
		return refuse("folder_unavailable", "The attached folder is unavailable or already managed. Choose another folder before creating anything.")
	}
	members := []buildPreparationMember{}
	profileDigests, savedDigests := map[string]string{}, map[string]string{}
	staffing, err := normalizeRoleStaffing(req.RoleStaffing)
	if err != nil {
		return refuse("team_changed", "The effective team cannot be reviewed.")
	}
	roles := projecttemplates.AgentRoleIDs(tpl.Agents)
	savedNames := append([]string(nil), req.ExistingAgentNames...)
	// Use the SAME effective spec/config functions as seedRoleStaffedAgents,
	// not the blueprint's default roster. Vacancies create nothing; assignment
	// uses the saved profile's own configuration rather than template defaults.
	for i, spec := range tpl.Agents {
		requested, filled := staffing[roles[i]]
		if !filled {
			continue
		}
		if requested.Mode == roleStaffingModeAssign {
			savedNames = append(savedNames, requested.Name)
			continue
		}
		config, _ := h.templateAgentCreateConfig(roleStaffedSpec(spec, requested))
		digest, err := digestJSON(config)
		if err != nil {
			return refuse("team_changed", "The effective team cannot be reviewed.")
		}
		profileDigests[requested.Name] = digest
		members = append(members, buildPreparationMember{Name: requested.Name, Action: "create", Provider: config.LLMProvider, Model: config.Model})
	}
	// Bind saved members' live definitions as well as their names. Merely
	// replaying an old roster/name must not conceal a changed configuration.
	for _, name := range savedNames {
		profile, found := h.agentStore.GetAgent(name)
		if !found || profile == nil {
			return refuse("team_changed", "A saved team member is no longer available.")
		}
		digest, err := reviewedBuildProfileDigest(profile)
		if err != nil {
			return refuse("team_changed", "The saved team cannot be reviewed.")
		}
		savedDigests[name] = digest
		members = append(members, buildPreparationMember{Name: name, Action: "attach", Provider: profile.Settings.Provider, Model: profile.Settings.Model})
	}
	termsRevision, err := digestJSON(root.Terms)
	if err != nil {
		return refuse("review_changed", "The permission terms cannot be reviewed.")
	}
	revision, err := digestJSON([]any{p.owner, p.build.ID, p.build.Version, req, tpl, profileDigests, savedDigests, root.Revision, root.Terms, p.request.Monitoring, filejanitor.SetupDisclosureVersion})
	if err != nil {
		return refuse("review_changed", "The current draft cannot be reviewed.")
	}
	if !p.confirm {
		placementLabel := "Workspace root"
		if req.ParentID != "" {
			parent, err := h.store.GetWorkspace(r.Context(), req.ParentID)
			if err != nil || parent == nil {
				return refuse("placement_changed", "The reviewed group is unavailable.")
			}
			placementLabel = parent.Name
		}
		_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "review": map[string]any{
			"state": "review", "version": p.build.Version, "review_revision": revision,
			"monitoring": p.request.Monitoring, "disclosure_version": filejanitor.SetupDisclosureVersion, "placement_label": placementLabel,
			"workspace": buildPreparationSummary{Name: req.Name, Description: req.Description, Mission: createWorkspaceGoal(req, session.WorkspaceKindWorkspace, tpl, true), ParentID: req.ParentID, Color: req.Color, Tags: req.Tags},
			"team":      members, "folder": map[string]string{"name": folder.name, "path": root.Path}, "terms": root.Terms,
		}})
		return descriptor, false
	}
	if revision != p.request.ReviewRevision {
		return refuse("review_changed", "The draft, team, folder or permission terms changed. Review them again before creating anything.")
	}
	claim := &personalassistant.BuildPreparationClaim{ID: uuid.NewString(), WorkspaceID: uuid.NewString(), BuildVersion: p.build.Version,
		ReviewRevision: revision, RootRevision: root.Revision, TermsRevision: termsRevision, Monitoring: p.request.Monitoring, AcceptedAt: h.workspaceBuild.Store.Now()}
	_, err = h.workspaceBuild.Store.Mutate(r.Context(), p.owner, func(doc *personalassistant.WorkspaceBuildDocument) error {
		current := doc.Session(p.build.ID)
		if current == nil || current.Status != personalassistant.WorkspaceBuildOpen || current.Version != p.build.Version || current.Preparation != nil {
			return errors.New("review changed")
		}
		current.Preparation, current.Status = claim, personalassistant.WorkspaceBuildPreparing
		current.CreateNow, current.Ready = false, false
		current.Touch(h.workspaceBuild.Store.Now())
		return nil
	})
	if err != nil {
		return refuse("review_changed", "The build changed before confirmation. Nothing was created; review it again.")
	}
	p.claim = claim
	descriptor = AssistantSetupCreationDescriptor{RunID: p.build.ID, OperationID: claim.ID, OwnerUserID: p.owner,
		WorkspaceID: claim.WorkspaceID, ReviewDigest: claim.ReviewRevision, ProfileProvenanceID: uuid.NewString(), ProfileConfigDigests: profileDigests, AssignedProfileDigests: savedDigests}
	return descriptor, true
}

func reviewedBuildProfileDigest(profile *agent.Agent) (string, error) {
	if profile == nil {
		return "", errors.New("profile unavailable")
	}
	return digestJSON([]any{profile.Settings, profile.Role, profile.Capabilities, profile.Appearance})
}

func (h *Handler) validateBuildPreparationSavedProfiles(ctx context.Context) error {
	descriptor, found := assistantSetupCreationFromContext(ctx)
	if !found || descriptor.AssignedProfileDigests == nil {
		return nil
	}
	for name, expected := range descriptor.AssignedProfileDigests {
		profile, found := h.agentStore.GetAgent(name)
		if !found {
			return errors.New("a reviewed saved profile is no longer available")
		}
		digest, err := reviewedBuildProfileDigest(profile)
		if err != nil || digest != expected {
			return errors.New("a reviewed saved profile changed before creation")
		}
	}
	return nil
}

// Hash the actual attached roles and their effective definitions, excluding
// volatile runtime statistics/messages. No configuration text leaves this call.
func (h *Handler) preparedWorkspaceTeamDigest(ws *workspace.Workspace) (string, error) {
	profiles := map[string]any{}
	for _, instance := range ws.AgentInstances {
		profile, found := h.agentStore.GetAgent(instance.Name)
		if !found || profile == nil {
			return "", errors.New("team profile unavailable")
		}
		profiles[instance.Name] = []any{profile.Settings, profile.Role, profile.Capabilities, profile.Appearance}
	}
	instances := append([]workspace.AgentInstance(nil), ws.AgentInstances...)
	for i := range instances {
		// The folder mirror canonicalizes canvas node IDs during normal
		// creation. Layout identity is not a change to the reviewed team.
		instances[i].NodeID = ""
	}
	return digestJSON([]any{instances, profiles})
}

func (h *Handler) prepareCreatedBuild(w http.ResponseWriter, r *http.Request, owner string, build personalassistant.WorkspaceBuildSession, janitor *filejanitor.Service, automation filejanitor.PreparationAutomation) {
	claim := build.Preparation
	incomplete := func(code, message, stage string) {
		respondBuildPreparationError(w, code, message, http.StatusConflict, map[string]any{"workspace_id": claim.WorkspaceID, "folder_slug": h.preparedBuildFolderSlug(owner, build), "stage": stage})
	}
	primary, primaryErr := h.workspaceTaskStore.Get(claim.WorkspaceID)
	if primaryErr != nil || primary == nil {
		incomplete("creation_unconfirmed", "The accepted workspace cannot be confirmed. Keep this build for reconciliation; it will not create another workspace.", "creation")
		return
	}
	if (primary.Status != "" && primary.Status != workspace.StatusActive) ||
		(primary.OwnerUserID != "" && primary.OwnerUserID != owner) {
		incomplete("creation_changed", "The accepted workspace is no longer active or owned by you. No setup was resumed.", "creation")
		return
	}
	ws, err := h.readReviewedWorkspace(claim.WorkspaceID)
	if err != nil || ws == nil {
		incomplete("creation_unconfirmed", "The accepted workspace cannot be confirmed. Keep this build for reconciliation; it will not create another workspace.", "creation")
		return
	}
	workspaceOwner := strings.TrimSpace(ws.OwnerUserID)
	if workspaceOwner == "" {
		workspaceOwner = userprofile.LocalUserID
	}
	prov := ws.GetTemplateProvenance()
	if workspaceOwner != owner || (ws.Status != "" && ws.Status != workspace.StatusActive) || prov == nil || prov.AssistantSetup == nil ||
		prov.AssistantSetup.RunID != build.ID || prov.AssistantSetup.OperationID != claim.ID || prov.AssistantSetup.ReviewDigest != claim.ReviewRevision {
		incomplete("creation_changed", "The accepted workspace is unavailable or its authority changed. No setup was resumed.", "creation")
		return
	}
	marker := prov.AssistantSetup
	teamRevision, teamErr := h.preparedWorkspaceTeamDigest(ws)
	if teamErr != nil || teamRevision != marker.TeamRevision {
		incomplete("team_changed", "The saved workspace team changed. Open the workspace and review its current setup.", "folder")
		return
	}
	install, found := ws.GetInstalledCapability(workspace.CapabilityFileJanitor)
	install.OwnedResources = nil
	capabilityRevision, digestErr := digestJSON(install)
	if !found || digestErr != nil || capabilityRevision != marker.CapabilityRevision {
		incomplete("setup_changed", "File Janitor was removed or changed. Open the workspace's current settings; the old review cannot restore access.", "folder")
		return
	}
	ref, err := ws.GetDirectoryReference(marker.FolderReferenceID)
	if err != nil || ref == nil {
		incomplete("folder_changed", "The accepted folder link changed. Open this workspace and review its current folder.", "folder")
		return
	}
	root, err := janitor.ReviewSelectedSetup(ws.ID, ref.Path, prov.AutomationRecipes)
	termsRevision, digestErr := digestJSON(root.Terms)
	if err != nil || digestErr != nil || root.Revision != claim.RootRevision || termsRevision != claim.TermsRevision {
		incomplete("folder_changed", "The folder or monitoring terms changed. Open this workspace for a fresh permission review.", "folder")
		return
	}
	if r.Context().Err() != nil {
		incomplete("preparation_stopped", "The workspace was created. Preparation stopped before the next step; resume this workspace.", "folder")
		return
	}
	if claim.LinkedReviewRevision == "" {
		review, err := janitor.ReviewLinkedSetup(ws.ID, owner, marker.FolderReferenceID, claim.Monitoring)
		if err != nil || review.State != "review" {
			incomplete("setup_changed", "Workspace setup changed. Open its current settings; the original review will not overwrite them.", "folder")
			return
		}
		revision := review.Revision
		_, err = h.workspaceBuild.Store.Mutate(r.Context(), owner, func(doc *personalassistant.WorkspaceBuildDocument) error {
			current := doc.Session(build.ID)
			if current == nil || current.Preparation == nil || current.Preparation.ID != claim.ID {
				return errors.New("creation authority changed")
			}
			if current.Preparation.LinkedReviewRevision != "" && current.Preparation.LinkedReviewRevision != revision {
				return errors.New("folder review changed")
			}
			current.Preparation.LinkedReviewRevision = revision
			return nil
		})
		if err != nil {
			incomplete("preparation_incomplete", "The workspace was created, but its preparation receipt could not be saved. Retry this workspace.", "folder")
			return
		}
		claim.LinkedReviewRevision = revision
	}
	prepared, err := janitor.PrepareLinkedSetup(filejanitor.LinkedPreparationRequest{
		Context: r.Context(), WorkspaceID: ws.ID, OwnerUserID: owner, ReferenceID: marker.FolderReferenceID,
		Monitoring: claim.Monitoring, ReviewRevision: claim.LinkedReviewRevision,
	}, automation)
	if err != nil {
		code, message := filejanitor.PreparationFailureSummary(prepared.Stage)
		var domainError *filejanitor.SetupError
		if errors.As(err, &domainError) {
			code, message = domainError.Code, domainError.Message
			if domainError.Code == filejanitor.CodeFolderConflict {
				message = "Another workspace already manages an overlapping folder. Open this workspace's current settings to choose another folder."
			}
		}
		respondBuildPreparationError(w, code, message, http.StatusConflict,
			map[string]any{"workspace_id": ws.ID, "folder_slug": h.preparedBuildFolderSlug(owner, build), "preparation": prepared})
		return
	}
	// Reconcile a crash after the canonical creation marker but before the
	// build's normal finish. This records provenance, never re-runs creation.
	h.finishWorkspaceBuild(r.Context(), build.ID, ws.ID, build.Draft)
	slug := h.preparedBuildFolderSlug(owner, build)
	if slug == "" {
		incomplete("workspace_route_unconfirmed", "The scan result was saved, but this workspace's page route could not be confirmed. Open it from Home; do not create again.", "result")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"success": true, "workspace_id": ws.ID, "folder_slug": slug, "preparation": prepared})
}
