package agenthttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/agentcatalog"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/types"
	"github.com/johnjallday/ori-agent/internal/userprofile"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Hiring from the Agents page (agents-hire-with-a-brief): one request creates
// an agent from a catalog role, gives it a workspace whose goal is the user's
// brief, and records the hire so the assistant can report back on the agent's
// first look. Every hire from this path ends with an agent that has work; a
// hire that fails part-way removes exactly what it created.

// The steps of a hire, in order. A failure names the step it stopped at so the
// card can mark that line.
const (
	hireStepAgent     = "agent"
	hireStepWorkspace = "workspace"
	hireStepRecord    = "record"
)

// Bounds on what a hire request carries.
const (
	hireRequestIDMax = 128
	hireDomainMax    = 80
	// hireWorkspaceNameTries is how many " N" suffixes a hire tries for a
	// workspace name that is already taken.
	hireWorkspaceNameTries = 99
)

// hireBlueprint is the workspace a role's hire starts from, as the card names
// it: its id, its label, and how many of its declared roles stay open.
type hireBlueprint struct {
	ID        string
	Label     string
	OpenRoles int
}

// hireBlueprintFor is the role's default blueprint: a workspace with the hired
// agent as its only member.
func hireBlueprintFor(types.AgentRole) hireBlueprint {
	return hireBlueprint{ID: personalassistant.HireBlueprintBlank, Label: "Blank"}
}

// hireRequest is POST /api/agents/hire.
type hireRequest struct {
	// RequestID is fresh per opened card; a retry with the same id returns the
	// first hire rather than hiring twice.
	RequestID     string             `json:"request_id"`
	Role          string             `json:"role"`
	Name          string             `json:"name"`
	Domain        string             `json:"domain,omitempty"`
	Appearance    *appearanceRequest `json:"appearance,omitempty"`
	Brief         string             `json:"brief"`
	WorkspaceName string             `json:"workspace_name,omitempty"`
}

// hireResult is what a hire made.
type hireResult struct {
	HireID         string `json:"hire_id"`
	AgentName      string `json:"agent_name"`
	Role           string `json:"role"`
	WorkspaceID    string `json:"workspace_id"`
	WorkspaceName  string `json:"workspace_name,omitempty"`
	WorkspaceRoute string `json:"workspace_route,omitempty"`
	Brief          string `json:"brief"`
	Blueprint      string `json:"blueprint"`
	LookState      string `json:"look_state"`
	// Replayed is a repeated request id answered with the first hire.
	Replayed              bool   `json:"replayed,omitempty"`
	ModelCategoryFallback bool   `json:"model_category_fallback,omitempty"`
	Notice                string `json:"notice,omitempty"`
}

// hirePlan is a validated hire request.
type hirePlan struct {
	entry         agentcatalog.Entry
	name          string
	domain        string
	appearance    *types.AgentAppearance
	brief         string
	workspaceName string
}

// SetHireRecords wires the store the hire records itself in. Without it the
// hire endpoint answers 503: a hire nobody can report on is not offered.
func (h *Handler) SetHireRecords(records personalassistant.HireRecordStore) {
	h.hireRecords = records
}

// writeHireFailure answers a refused or failed hire. code is stable for the
// card; message is for the person; step is the step that failed, if any.
func writeHireFailure(w http.ResponseWriter, status int, code, message, step string) {
	body := map[string]any{"success": false, "error": code, "message": message}
	if step != "" {
		body["step"] = step
	}
	_ = orihttp.RespondJSON(w, status, body)
}

// HandleHire serves POST /api/agents/hire: create the agent as catalog create
// does, create its workspace with the brief as the goal and the agent as its
// only member and entry agent, and record the hire. The workspace is placed on
// the Map by the Map's own layout, since it has no saved position.
func (h *Handler) HandleHire(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		orihttp.MethodNotAllowed(w)
		return
	}
	var req hireRequest
	if !parseAgentRequest(w, r, &req) {
		return
	}
	if h.hireRecords == nil || h.workspaceStore == nil {
		writeHireFailure(w, http.StatusServiceUnavailable, "hire_unavailable", "Hiring is not available right now.", "")
		return
	}
	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" || len(requestID) > hireRequestIDMax || strings.IndexFunc(requestID, unicode.IsControl) >= 0 {
		writeHireFailure(w, http.StatusBadRequest, "invalid_hire", "The hire needs a request id of at most 128 characters.", "")
		return
	}
	ctx := r.Context()
	userID, err := h.hireUserID(ctx)
	if err != nil {
		writeHireFailure(w, http.StatusInternalServerError, "hire_unavailable", "Could not tell who is hiring.", "")
		return
	}

	// One hire at a time: a double click or a retry racing the first request
	// finds the first hire's record instead of hiring again.
	h.hireMu.Lock()
	defer h.hireMu.Unlock()

	if record, err := h.hireRecords.ByRequestID(ctx, requestID); err == nil {
		if record.UserID != userID {
			writeHireFailure(w, http.StatusConflict, "request_id_conflict", "That hire request belongs to someone else.", "")
			return
		}
		result := h.hireResultOf(record)
		result.Replayed = true
		_ = orihttp.RespondJSON(w, http.StatusOK, result)
		return
	} else if !errors.Is(err, personalassistant.ErrHireNotFound) {
		logger.Error("Hire: reading the request's record failed", logger.Fields{"err": err})
		writeHireFailure(w, http.StatusInternalServerError, "hire_unavailable", "Could not check whether this hire already happened.", "")
		return
	}

	if ready, err := h.assistantReadyForHire(ctx, userID); err != nil {
		logger.Error("Hire: reading the personal assistant failed", logger.Fields{"err": err})
		writeHireFailure(w, http.StatusInternalServerError, "hire_unavailable", "Could not read your personal assistant.", "")
		return
	} else if !ready {
		writeHireFailure(w, http.StatusConflict, "assistant_required",
			"Hire your personal assistant first; every other hire reports back to it.", "")
		return
	}

	plan, status, code, message := h.planHire(req)
	if code != "" {
		writeHireFailure(w, status, code, message, "")
		return
	}

	// (a) The agent, exactly as catalog create makes it. Only this request's
	// agent is ever removed again: the name was checked free just above, so
	// a pre-existing agent is never the one created here.
	created, err := h.createCatalogAgent(plan.name, plan.entry, plan.domain, plan.appearance)
	if err != nil {
		writeHireFailure(w, http.StatusInternalServerError, "hire_failed", "Could not create "+plan.name+": "+err.Error(), hireStepAgent)
		return
	}
	rollbackAgent := func() {
		if err := h.performAgentDeletion(ctx, plan.name); err != nil {
			logger.Error("Hire rollback: could not remove the agent it created", logger.Fields{"agent": plan.name, "err": err})
		}
	}

	// (b) and (c) The workspace, with the brief as its goal and the agent as
	// its only member and entry agent, written in one save.
	ws, err := h.createHireWorkspace(ctx, plan)
	if err != nil {
		rollbackAgent()
		writeHireFailure(w, http.StatusInternalServerError, "hire_failed", "Could not create the workspace: "+err.Error(), hireStepWorkspace)
		return
	}

	// (d) Placement: the Map lays out a workspace with no saved position, so
	// there is nothing to save here.

	// (e) The record the assistant reports from.
	record, err := h.hireRecords.Create(ctx, personalassistant.HireRecord{
		ID: uuid.NewString(), UserID: userID, RequestID: requestID,
		AgentName: plan.name, WorkspaceID: ws.ID, Brief: plan.brief,
		Role: string(plan.entry.Slug), Blueprint: hireBlueprintFor(plan.entry.Slug).ID,
		State: personalassistant.HireStateLooking,
	})
	if err != nil {
		if delErr := h.workspaceStore.Delete(ws.ID); delErr != nil {
			logger.Error("Hire rollback: could not remove the workspace it created", logger.Fields{"workspace": ws.ID, "err": delErr})
		}
		rollbackAgent()
		writeHireFailure(w, http.StatusInternalServerError, "hire_failed", "Could not record the hire: "+err.Error(), hireStepRecord)
		return
	}

	logger.Info("Agent hired", logger.Fields{"agent": plan.name, "role": string(plan.entry.Slug), "workspace": ws.ID, "hire": record.ID})
	result := h.hireResultOf(record)
	result.WorkspaceName, result.WorkspaceRoute = ws.Name, workspaceRoute(ws)
	if !created.ModelResolved {
		result.ModelCategoryFallback, result.Notice = true, created.ModelNotice()
	}
	_ = orihttp.RespondJSON(w, http.StatusCreated, result)
}

// planHire validates a request into a hire. A non-empty code is a refusal with
// its status and message; nothing has been created yet.
func (h *Handler) planHire(req hireRequest) (hirePlan, int, string, string) {
	refuse := func(message string) (hirePlan, int, string, string) {
		return hirePlan{}, http.StatusBadRequest, "invalid_hire", message
	}
	entry, ok := findCatalogEntry(req.Role)
	if !ok {
		return refuse(fmt.Sprintf("Unknown role %q.", strings.TrimSpace(req.Role)))
	}
	plan := hirePlan{entry: entry, name: strings.TrimSpace(req.Name)}
	if isReservedAgentName(plan.name) || h.isCLIAgent(plan.name) {
		return refuse(fmt.Sprintf("%q is a reserved name; choose another.", plan.name))
	}
	if err := validateAgentName(plan.name); err != nil {
		return refuse(err.Error())
	}
	if _, exists := h.State.GetAgent(plan.name); exists {
		return hirePlan{}, http.StatusConflict, "agent_exists", fmt.Sprintf("You already have an agent named %q; choose another name.", plan.name)
	}
	if entry.SupportsDomain {
		plan.domain = strings.Join(strings.Fields(req.Domain), " ")
		if utf8.RuneCountInString(plan.domain) > hireDomainMax || strings.IndexFunc(plan.domain, unicode.IsControl) >= 0 {
			return refuse(fmt.Sprintf("The domain is capped at %d characters.", hireDomainMax))
		}
	}
	appearance, err := applyAppearanceRequest(nil, req.Appearance)
	if err != nil {
		return refuse(err.Error())
	}
	plan.appearance = appearance

	brief, err := personalassistant.NormalizeFolderBrief(req.Brief)
	if err != nil {
		return refuse(fmt.Sprintf("What it should do is capped at %d characters, in plain words.", personalassistant.WorkspaceGoalMaxRunes))
	}
	if brief == "" {
		brief = personalassistant.HireDefaultBrief(entry.Slug, plan.domain)
	}
	if brief == "" {
		return refuse("Say what the agent should do.")
	}
	plan.brief = brief

	name := strings.Join(strings.Fields(req.WorkspaceName), " ")
	if utf8.RuneCountInString(name) > personalassistant.HireWorkspaceNameMaxRunes || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return refuse(fmt.Sprintf("The workspace name is capped at %d characters.", personalassistant.HireWorkspaceNameMaxRunes))
	}
	if name == "" {
		name = personalassistant.HireWorkspaceName(brief)
	}
	if name == "" {
		name = plan.name + " Workspace"
	}
	plan.workspaceName = name
	return plan, 0, "", ""
}

// createHireWorkspace creates a top-level workspace whose only member and entry
// agent is the hired agent and whose goal is the brief, armed for its first
// goal run. A name already taken gets the first free " N" suffix.
func (h *Handler) createHireWorkspace(ctx context.Context, plan hirePlan) (*workspace.Workspace, error) {
	taken := h.takenWorkspaceSlugs()
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		name := freeHireWorkspaceName(plan.workspaceName, taken)
		ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{
			Name: name, Agents: []string{plan.name}, InitialData: map[string]any{},
		})
		if err := ws.SetEntryAgentName(plan.name); err != nil {
			return nil, err
		}
		ws.Mission = plan.brief
		workspace.ArmFirstGoalRun(ws)
		err := workspace.CreateNativeWorkspace(ctx, h.workspaceStore, ws)
		if err == nil {
			return ws, nil
		}
		// A folder the listing did not show (a trashed workspace's, say) is
		// taken too; try the next free name rather than failing the hire.
		var conflict *workspace.FolderSlugConflictError
		if !errors.As(err, &conflict) {
			return nil, err
		}
		taken[strings.ToLower(workspace.Slugify(name))] = true
		lastErr = err
	}
	return nil, lastErr
}

// takenWorkspaceSlugs are the top-level workspace folder names in use, with
// the Workspace Directory's reserved folders.
func (h *Handler) takenWorkspaceSlugs() map[string]bool {
	taken := map[string]bool{"agents": true, "skills": true}
	list, err := h.workspaceStore.ListActive()
	if err != nil {
		return taken
	}
	for _, ws := range list {
		if ws == nil || ws.ParentID != "" {
			continue
		}
		slug := ws.FolderSlug
		if slug == "" {
			slug = workspace.Slugify(ws.Name)
		}
		taken[strings.ToLower(slug)] = true
	}
	return taken
}

// freeHireWorkspaceName is name, or name with the first " N" whose folder is
// free, kept within the workspace name's bound.
func freeHireWorkspaceName(name string, taken map[string]bool) string {
	for n := 1; n <= hireWorkspaceNameTries; n++ {
		candidate := name
		if n > 1 {
			suffix := fmt.Sprintf(" %d", n)
			base := []rune(name)
			if limit := personalassistant.HireWorkspaceNameMaxRunes - len(suffix); len(base) > limit {
				base = base[:limit]
			}
			candidate = strings.TrimSpace(string(base)) + suffix
		}
		if !taken[strings.ToLower(workspace.Slugify(candidate))] {
			return candidate
		}
	}
	return name
}

// hireUserID is who is hiring; Ori's one local user unless a provider says.
func (h *Handler) hireUserID(ctx context.Context) (string, error) {
	if h.support.provider == nil {
		return userprofile.LocalUserID, nil
	}
	userID, err := h.support.provider.CurrentUserID(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(userID) == "" {
		return userprofile.LocalUserID, nil
	}
	return userID, nil
}

// assistantReadyForHire reports whether the user has hired a personal
// assistant: a project hire reports back to it, so it comes first. The
// assistant does not need its Personal HQ yet.
func (h *Handler) assistantReadyForHire(ctx context.Context, userID string) (bool, error) {
	if h.support.reader == nil {
		return false, nil
	}
	projection, err := h.support.reader.Get(ctx, userID)
	if err != nil || projection == nil {
		return false, err
	}
	switch projection.State {
	case personalassistant.APIStateNeedsHQ, personalassistant.APIStateProvisioningHQ,
		personalassistant.APIStateActive, personalassistant.APIStatePaused:
		return true, nil
	}
	return false, nil
}

// hireResultOf answers with what a recorded hire made; the workspace's name
// and route are read again, since it may have been renamed since.
func (h *Handler) hireResultOf(record personalassistant.HireRecord) hireResult {
	result := hireResult{
		HireID: record.ID, AgentName: record.AgentName, Role: record.Role,
		WorkspaceID: record.WorkspaceID, Brief: record.Brief, Blueprint: record.Blueprint,
		LookState: string(record.State),
	}
	if ws, err := h.workspaceStore.Get(record.WorkspaceID); err == nil && ws != nil {
		result.WorkspaceName, result.WorkspaceRoute = ws.Name, workspaceRoute(ws)
	}
	return result
}

func workspaceRoute(ws *workspace.Workspace) string {
	if ws == nil || strings.TrimSpace(ws.FolderSlug) == "" {
		return ""
	}
	return "/workspaces/" + url.PathEscape(ws.FolderSlug)
}
