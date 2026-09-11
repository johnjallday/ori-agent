package sessionhttp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

const (
	workspaceTeamIntentVersion = 1
	workspaceTeamModeStaffed   = "staffed"
	workspaceTeamModeAgentless = "agentless"
)

type workspaceTeamIntent struct {
	Version      int    `json:"version"`
	Mode         string `json:"mode"`
	PlanRevision string `json:"plan_revision"`
}

type workspaceTeamRoleReadiness struct {
	RoleID   string `json:"role_id"`
	Label    string `json:"label"`
	Scope    string `json:"scope"`
	Required bool   `json:"required"`
	Primary  bool   `json:"primary"`
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
}

type workspaceTeamReadinessProgress struct {
	Required int `json:"required"`
	Filled   int `json:"filled"`
	Missing  int `json:"missing"`
}

type workspaceTeamReadinessError struct {
	Status    int
	Code      string
	Message   string
	Roles     []workspaceTeamRoleReadiness
	Progress  workspaceTeamReadinessProgress
	FreshPlan *templateAgentPlan
}

func (e *workspaceTeamReadinessError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func invalidWorkspaceTeamIntent(message string) error {
	return &workspaceTeamReadinessError{
		Status:  http.StatusBadRequest,
		Code:    "team_intent_invalid",
		Message: message,
	}
}

func workspaceTeamConflict(message string, roles []workspaceTeamRoleReadiness, fresh *templateAgentPlan) error {
	progress := workspaceTeamReadinessProgress{}
	for _, role := range roles {
		if !role.Required {
			continue
		}
		progress.Required++
		if role.State == "filled" {
			progress.Filled++
		} else {
			progress.Missing++
		}
	}
	return &workspaceTeamReadinessError{
		Status: http.StatusConflict, Code: "team_readiness", Message: message,
		Roles: roles, Progress: progress, FreshPlan: fresh,
	}
}

func respondWorkspaceTeamReadinessError(w http.ResponseWriter, err error) bool {
	readiness, ok := err.(*workspaceTeamReadinessError)
	if !ok || readiness == nil {
		return false
	}
	conflict := map[string]any{
		"type": readiness.Code,
	}
	if len(readiness.Roles) > 0 {
		conflict["roles"] = readiness.Roles
		conflict["progress"] = readiness.Progress
	}
	if readiness.FreshPlan != nil {
		conflict["fresh_plan"] = readiness.FreshPlan
	}
	_ = orihttp.RespondJSON(w, readiness.Status, map[string]any{
		"error":    readiness.Message,
		"code":     readiness.Code,
		"conflict": conflict,
	})
	return true
}

func parseWorkspaceTeamIntent(raw json.RawMessage) (*workspaceTeamIntent, error) {
	if raw == nil {
		return nil, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '{' {
		return nil, invalidWorkspaceTeamIntent("team_intent must be a versioned object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var intent workspaceTeamIntent
	if err := decoder.Decode(&intent); err != nil {
		return nil, invalidWorkspaceTeamIntent("team_intent contains unsupported or malformed fields")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, invalidWorkspaceTeamIntent("team_intent must contain exactly one object")
	}
	intent.Mode = strings.ToLower(strings.TrimSpace(intent.Mode))
	intent.PlanRevision = strings.TrimSpace(intent.PlanRevision)
	if intent.Version != workspaceTeamIntentVersion {
		return nil, invalidWorkspaceTeamIntent("team_intent version is unsupported")
	}
	if intent.Mode != workspaceTeamModeStaffed && intent.Mode != workspaceTeamModeAgentless {
		return nil, invalidWorkspaceTeamIntent("team_intent mode must be staffed or agentless")
	}
	if intent.PlanRevision == "" {
		return nil, invalidWorkspaceTeamIntent("team_intent plan_revision is required")
	}
	return &intent, nil
}

func validateWorkspaceTeamIntentEnvelope(req *createWorkspaceRequest) error {
	if req == nil || !req.teamIntentPresent {
		return nil
	}
	if _, err := parseWorkspaceTeamIntent(req.TeamIntent); err != nil {
		return err
	}
	if !req.roleStaffingPresent || req.roleStaffingNull {
		return invalidWorkspaceTeamIntent("strict team intent requires an explicit role_staffing array")
	}
	return nil
}

// validateWorkspaceTeamReadiness is the strict wizard gate. It runs after the
// selected source has been read but before group claims, agent creation,
// workspace persistence, folders, grants, plugins, watchers, or automations.
// Absence of team_intent is the deliberate legacy compatibility branch.
func (h *Handler) validateWorkspaceTeamReadiness(
	req *createWorkspaceRequest,
	tpl projecttemplates.Template,
	templateResolved bool,
	kind string,
) (*workspaceTeamIntent, error) {
	if req == nil {
		return nil, invalidWorkspaceTeamIntent("workspace request is unavailable")
	}
	if !req.teamIntentPresent {
		return nil, nil
	}
	intent, err := parseWorkspaceTeamIntent(req.TeamIntent)
	if err != nil || intent == nil {
		return intent, err
	}
	if !req.roleStaffingPresent || req.roleStaffingNull {
		return nil, invalidWorkspaceTeamIntent("strict team intent requires an explicit role_staffing array")
	}
	if kind == "group" {
		return nil, invalidWorkspaceTeamIntent("strict project team intent cannot create a group workspace")
	}

	if intent.Mode == workspaceTeamModeAgentless {
		if !req.Blank || strings.TrimSpace(req.TemplateID) != "" || strings.TrimSpace(req.TemplatePath) != "" {
			return nil, invalidWorkspaceTeamIntent("agentless mode is available only with the Blank blueprint")
		}
		if len(req.RoleStaffing) > 0 || len(req.ExistingAgentNames) > 0 || strings.TrimSpace(req.EntryAgentName) != "" ||
			len(req.TemplateAgentOverrides) > 0 || req.TemplateAgentReview != nil ||
			(req.CreateTemplateAgents != nil && *req.CreateTemplateAgents) {
			return nil, invalidWorkspaceTeamIntent("agentless mode cannot include staffing or agent seeding choices")
		}
		plan := h.buildTemplateAgentPlan(blankWorkspaceTemplate())
		if intent.PlanRevision != plan.Revision {
			return nil, workspaceTeamConflict("The Blank team plan changed; review it again.", nil, &plan)
		}
		return intent, nil
	}

	if len(req.TemplateAgentOverrides) > 0 || req.TemplateAgentReview != nil {
		return nil, invalidWorkspaceTeamIntent("strict role staffing cannot be combined with whole-roster template customization")
	}
	if !templateResolved {
		return nil, workspaceTeamConflict("The selected blueprint is no longer available; choose it again.", nil, nil)
	}

	plan := h.buildTemplateAgentPlan(tpl)
	roles := workspaceTeamRoles(tpl, plan)
	if len(roles) > 0 && strings.TrimSpace(req.EntryAgentName) != "" {
		return nil, workspaceTeamConflict("The declared primary role determines this workspace's entry agent.", roles, nil)
	}
	if plan.AssistantProgram != nil {
		for index := range roles {
			if roles[index].Scope != "home" || roles[index].State != "filled" {
				continue
			}
			declared := plan.AssistantProgram.Roles[index]
			if !h.assistantInheritedRoleVerified(tpl, declared.ID, declared.AgentName) {
				roles[index].State = "empty"
				roles[index].Reason = "inherited holder could not be verified"
			}
		}
	}
	if intent.PlanRevision != plan.Revision {
		return nil, workspaceTeamConflict("The blueprint team changed; review the current roles again.", roles, &plan)
	}
	for _, item := range req.RoleStaffing {
		if strings.TrimSpace(item.Mode) == "" {
			return nil, workspaceTeamConflict("Every strict role fill must say whether to create or assign an agent.", roles, nil)
		}
	}
	staffing, normalizeErr := normalizeRoleStaffing(req.RoleStaffing)
	if normalizeErr != nil {
		return nil, workspaceTeamConflict(normalizeErr.Error(), roles, nil)
	}

	roleIndex := make(map[string]int, len(roles))
	for index := range roles {
		roleIndex[roles[index].RoleID] = index
		if roles[index].Reason == "verified inherited holder" {
			roles[index].State = "filled"
		}
	}

	extraNames := make(map[string]struct{}, len(req.ExistingAgentNames))
	for index, requested := range req.ExistingAgentNames {
		canonical, attachErr := h.validateAttachableWorkspaceAgent(requested)
		if attachErr != nil {
			return nil, workspaceTeamConflict(fmt.Sprintf("saved teammate %q no longer exists or cannot be attached", requested), roles, nil)
		}
		req.ExistingAgentNames[index] = canonical
		key := strings.ToLower(strings.TrimSpace(canonical))
		if _, duplicate := extraNames[key]; duplicate {
			return nil, workspaceTeamConflict(fmt.Sprintf("saved teammate %q was included twice", canonical), roles, nil)
		}
		extraNames[key] = struct{}{}
	}

	normalized := make([]roleStaffingInput, 0, len(req.RoleStaffing))
	for _, raw := range req.RoleStaffing {
		item := staffing[strings.ToLower(strings.TrimSpace(raw.RoleID))]
		index, declared := roleIndex[item.RoleID]
		if !declared {
			return nil, workspaceTeamConflict(fmt.Sprintf("role %q is no longer declared by this blueprint", item.RoleID), roles, nil)
		}
		if roles[index].Scope == "home" {
			roles[index].Reason = "owned by the group workspace"
			return nil, workspaceTeamConflict(fmt.Sprintf("role %q must be staffed from its group workspace", item.RoleID), roles, nil)
		}
		nameKey := strings.ToLower(item.Name)
		if _, duplicate := extraNames[nameKey]; duplicate {
			roles[index].Reason = "the same agent is also selected as an extra teammate"
			return nil, workspaceTeamConflict(fmt.Sprintf("agent %q cannot be both a role holder and an extra teammate", item.Name), roles, nil)
		}
		switch item.Mode {
		case roleStaffingModeAssign:
			canonical, attachErr := h.validateAttachableWorkspaceAgent(item.Name)
			if attachErr != nil {
				roles[index].Reason = "assigned saved agent is unavailable"
				return nil, workspaceTeamConflict(fmt.Sprintf("the agent %q assigned to %q no longer exists or cannot be attached", item.Name, roles[index].Label), roles, nil)
			}
			item.Name = canonical
		case roleStaffingModeCreate:
			if h == nil || h.agentStore == nil {
				roles[index].Reason = "agent storage is unavailable"
				return nil, workspaceTeamConflict("Agent storage is unavailable; retry after restoring it.", roles, nil)
			}
			if canonical, lookupErr := h.validateAttachableWorkspaceAgent(item.Name); lookupErr == nil {
				roles[index].Reason = "create name now belongs to a saved agent"
				return nil, workspaceTeamConflict(fmt.Sprintf("you already have an agent named %q; assign it or choose another name", canonical), roles, nil)
			}
		}
		roles[index].State = "filled"
		roles[index].Reason = "explicit role fill"
		normalized = append(normalized, item)
	}
	req.RoleStaffing = normalized

	if plan.AssistantProgram != nil && len(normalized) > 0 && h.assistantRoleStaffer == nil {
		return nil, workspaceTeamConflict("Assistant Program staffing is unavailable; restore it before creating this workspace.", roles, nil)
	}
	missing := false
	for index := range roles {
		if roles[index].Required && roles[index].State != "filled" {
			if roles[index].Reason == "" {
				roles[index].Reason = "required role has no verified holder"
			}
			missing = true
		}
	}
	if missing {
		return nil, workspaceTeamConflict("Fill every required role before creating this workspace.", roles, nil)
	}
	return intent, nil
}

func (h *Handler) assistantInheritedRoleVerified(tpl projecttemplates.Template, roleID, name string) bool {
	if h == nil || h.agentStore == nil || h.workspaceTaskStore == nil || tpl.PluginOwner == nil {
		return false
	}
	if _, err := h.validateAttachableWorkspaceAgent(name); err != nil {
		return false
	}
	key := agentworkspace.AssistantProgramKey{
		OwnerUserID: "local", PluginID: tpl.PluginOwner.PluginID,
		ProgramID: tpl.AssistantProgram.ID,
	}
	station, err := agentworkspace.NewAssistantProgramStore(h.workspaceTaskStore).FindStation(key)
	if err != nil || station == nil {
		return false
	}
	state := station.GetAssistantProgramState()
	if state == nil {
		return false
	}
	bindings := append([]agentworkspace.AssistantRoleBinding(nil), state.HomeBindings.Bindings...)
	if state.Declaration != nil && state.Declaration.SchemaVersion < 2 {
		bindings = append(bindings, state.Roster...)
	}
	for _, binding := range bindings {
		if binding.RoleID != roleID || !strings.EqualFold(strings.TrimSpace(binding.AgentName), strings.TrimSpace(name)) || strings.TrimSpace(binding.AgentInstanceID) == "" {
			continue
		}
		instanceFound := false
		for _, instance := range station.GetAgentInstances() {
			if instance.ID == binding.AgentInstanceID && strings.EqualFold(strings.TrimSpace(instance.Name), strings.TrimSpace(name)) {
				instanceFound = true
				break
			}
		}
		if !instanceFound {
			return false
		}
		_, snapshotFound, snapshotErr := h.workspaceTaskStore.GetWorkspaceAgent(station.ID, binding.AgentName)
		return snapshotErr == nil && snapshotFound
	}
	return false
}

func workspaceTeamRoles(tpl projecttemplates.Template, plan templateAgentPlan) []workspaceTeamRoleReadiness {
	if plan.AssistantProgram != nil {
		roles := make([]workspaceTeamRoleReadiness, 0, len(plan.AssistantProgram.Roles))
		for _, role := range plan.AssistantProgram.Roles {
			state := "empty"
			reason := ""
			if role.Scope == "home" && strings.TrimSpace(role.AgentName) != "" {
				state = "filled"
				reason = "verified inherited holder"
			}
			roles = append(roles, workspaceTeamRoleReadiness{
				RoleID: role.ID, Label: role.Label, Scope: role.Scope,
				Required: role.Required, Primary: role.Primary, State: state, Reason: reason,
			})
		}
		return roles
	}
	ids := projecttemplates.AgentRoleIDs(tpl.Agents)
	roles := make([]workspaceTeamRoleReadiness, 0, len(tpl.Agents))
	for index, spec := range tpl.Agents {
		roles = append(roles, workspaceTeamRoleReadiness{
			RoleID: ids[index], Label: strings.TrimSpace(spec.Name), Scope: "project",
			Required: index == 0, Primary: index == 0, State: "empty",
		})
	}
	return roles
}
