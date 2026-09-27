package sessionhttp

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// WorkspaceBuildCatalogEntry is one blueprint the user can build from, with
// the readiness the Create gate would see.
type WorkspaceBuildCatalogEntry struct {
	Template  projecttemplates.Template
	Readiness blueprintreadiness.Readiness
}

// Rejection reasons the model sees on its next turn (FR15).
const (
	buildReasonNotAvailable   = "not_available"
	buildReasonNameTaken      = "name_taken"
	buildReasonTooLong        = "too_long"
	buildReasonEmpty          = "empty"
	buildReasonNoInputs       = "this blueprint asks for no inputs"
	buildReasonNotAGroup      = "not one of the user's groups"
	buildReasonPlacementFixed = "this blueprint sets up its own group at Create"
	buildReasonNoFolder       = "this blueprint cannot link an existing folder"
	buildReasonNoBlueprint    = "choose a blueprint first"
	buildReasonUnknownAgent   = "not one of the user's saved agents"
	buildReasonUnknownRole    = "not one of this blueprint's roles"
	buildReasonAgentless      = "only a Blank workspace can have no agents"
	buildReasonModel          = "that model is not available"
	buildReasonColor          = "not one of the offered colors"
)

// unknownAgentReason names the agent the assistant tried to assign, so its
// next turn can correct the name instead of repeating it.
func unknownAgentReason(name string) string {
	name = truncateRunes(name, 60)
	if name == "" {
		return buildReasonUnknownAgent + ": no name given"
	}
	return buildReasonUnknownAgent + ": " + name
}

// buildColors are the swatches the wizard offers; "" is the default.
var buildColors = map[string]bool{
	"": true, "#ef4444": true, "#f59e0b": true, "#22c55e": true, "#3b82f6": true, "#8b5cf6": true, "#ec4899": true,
}

const (
	buildMaxName        = 80
	buildMaxDescription = 1000
	buildMaxTags        = 8
	buildMaxTag         = 32
	buildMaxAgentName   = 100
	buildMaxPrompt      = 4000
)

// buildValidation is what the host checks the assistant's proposal against:
// this user's catalog, saved agents, and groups, the create path's own name
// rule, and the models the app can resolve.
type buildValidation struct {
	catalog   []WorkspaceBuildCatalogEntry
	agents    []buildSavedAgent
	groups    []buildGroup
	nameTaken func(name string) bool
	modelOK   func(provider, model string) bool
}

// lookup finds a catalog blueprint by id, including a plugin's namespaced id
// named by its bare suffix, as the wizard's deep links do.
func (v buildValidation) lookup(id string) (WorkspaceBuildCatalogEntry, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return WorkspaceBuildCatalogEntry{}, false
	}
	for _, entry := range v.catalog {
		if entry.Template.ID == id {
			return entry, true
		}
	}
	for _, entry := range v.catalog {
		if strings.HasSuffix(entry.Template.ID, ":"+id) {
			return entry, true
		}
	}
	return WorkspaceBuildCatalogEntry{}, false
}

func (v buildValidation) savedAgent(name string) (string, bool) {
	name = strings.TrimSpace(name)
	for _, agent := range v.agents {
		if strings.EqualFold(agent.Name, name) {
			return agent.Name, true
		}
	}
	return "", false
}

func (v buildValidation) group(id string) (buildGroup, bool) {
	for _, group := range v.groups {
		if group.ID == strings.TrimSpace(id) {
			return group, true
		}
	}
	return buildGroup{}, false
}

// offerable reports whether a blueprint may be chosen: known to this user and
// not blocked by readiness. Without this check an unknown template id would
// silently create a Blank workspace.
func (v buildValidation) offerable(entry WorkspaceBuildCatalogEntry) bool {
	if entry.Template.Retired {
		return false
	}
	return !blueprintCreationBlocked(entry.Template, entry.Readiness)
}

// buildApplyResult is what one accepted proposal did to the session.
type buildApplyResult struct {
	applied    []string
	rejections []personalassistant.BuildRejection
	askFolder  bool
	teamPatch  *personalassistant.BuildTeamPatch
}

func (r *buildApplyResult) reject(field, reason string) {
	r.rejections = append(r.rejections, personalassistant.BuildRejection{Field: field, Reason: reason})
}

// changed reports whether the proposal did any work on the form.
func (r buildApplyResult) changed() bool {
	return len(r.applied) > 0 || r.askFolder
}

// currentTemplate is the blueprint the draft names, if it is in the catalog.
func (v buildValidation) currentTemplate(draft personalassistant.BuildDraft) (projecttemplates.Template, bool) {
	if draft.Blank || strings.TrimSpace(draft.TemplateID) == "" {
		return projecttemplates.Template{}, false
	}
	entry, ok := v.lookup(draft.TemplateID)
	return entry.Template, ok
}

// clearBlueprintDerived forgets everything that belonged to the previous
// blueprint before the next model turn sees the draft (FR15).
func clearBlueprintDerived(session *personalassistant.WorkspaceBuildSession, template projecttemplates.Template, blank bool) {
	draft := &session.Draft
	draft.BlueprintInputs = nil
	draft.TeamIntent = nil
	draft.RoleStaffing = nil
	draft.TemplateAgentOverrides = nil
	draft.TemplateAgentReview = nil
	draft.EntryAgentName = ""
	draft.CreateTemplateAgents = nil
	session.TeamPatch = nil
	session.TeamState = nil
	if blank || !supportsExistingProject(template) {
		draft.ProjectConnection = nil
	}
	if !blank && needsHome(template) {
		draft.ParentID = ""
	}
}

// applyBuildReply validates the assistant's proposal field by field and
// applies what passes. A refused field is dropped, never the whole proposal.
func applyBuildReply(session *personalassistant.WorkspaceBuildSession, patch buildReplyPatch, v buildValidation) buildApplyResult {
	var result buildApplyResult
	draft := &session.Draft

	if patch.sets(buildFieldBlueprint) {
		id := strings.TrimSpace(patch.BlueprintID)
		switch {
		case id == "" || strings.EqualFold(id, "blank"):
			if !draft.Blank || draft.TemplateID != "" {
				draft.Blank, draft.TemplateID = true, ""
				clearBlueprintDerived(session, projecttemplates.Template{}, true)
			}
			result.applied = append(result.applied, "blueprint")
		default:
			entry, ok := v.lookup(id)
			if !ok || !v.offerable(entry) {
				result.reject(buildFieldBlueprint, buildReasonNotAvailable)
				break
			}
			if draft.Blank || draft.TemplateID != entry.Template.ID {
				draft.Blank, draft.TemplateID = false, entry.Template.ID
				clearBlueprintDerived(session, entry.Template, false)
			}
			result.applied = append(result.applied, "blueprint")
		}
	}
	template, hasTemplate := v.currentTemplate(*draft)
	session.NeedsHome = nil
	if hasTemplate && needsHome(template) {
		label := strings.TrimSpace(template.GroupRequirement.DefaultHomeName)
		if label == "" && template.AssistantProgram != nil {
			label = strings.TrimSpace(template.AssistantProgram.StationName)
		}
		if label == "" {
			label = "its own group"
		}
		session.NeedsHome = &personalassistant.BuildNeedsHome{Label: label}
	}

	if patch.sets(buildFieldName) {
		name := strings.TrimSpace(patch.Name)
		switch {
		case name == "":
			result.reject(buildFieldName, buildReasonEmpty)
		case utf8.RuneCountInString(name) > buildMaxName:
			result.reject(buildFieldName, buildReasonTooLong)
		case !strings.EqualFold(name, draft.Name) && v.nameTaken != nil && v.nameTaken(name):
			result.reject(buildFieldName, buildReasonNameTaken)
		default:
			draft.Name = name
			result.applied = append(result.applied, "name")
		}
	}

	if patch.sets(buildFieldDescription) {
		description := strings.TrimSpace(patch.Description)
		if utf8.RuneCountInString(description) > buildMaxDescription {
			result.reject(buildFieldDescription, buildReasonTooLong)
		} else {
			draft.Description = description
			result.applied = append(result.applied, "description")
		}
	}

	if patch.sets(buildFieldInputs) && len(patch.Inputs) > 0 {
		applied := applyBuildInputs(draft, template, hasTemplate, patch.Inputs, &result)
		if applied {
			result.applied = append(result.applied, "inputs")
		}
	}

	if patch.sets(buildFieldParent) {
		id := strings.TrimSpace(patch.ParentID)
		switch {
		case id == "":
			draft.ParentID = ""
			result.applied = append(result.applied, "parent")
		case hasTemplate && needsHome(template):
			result.reject(buildFieldParent, buildReasonPlacementFixed)
		default:
			if _, ok := v.group(id); !ok {
				result.reject(buildFieldParent, buildReasonNotAGroup)
				break
			}
			draft.ParentID = id
			result.applied = append(result.applied, "parent")
		}
	}

	if patch.sets(buildFieldAskFolder) && patch.AskFolder {
		if hasTemplate && supportsExistingProject(template) {
			result.askFolder = true
		} else {
			result.reject(buildFieldAskFolder, buildReasonNoFolder)
		}
	}

	if patch.sets(buildFieldTeam) {
		if team := validateBuildTeam(*draft, template, hasTemplate, patch.Team, v, &result); team != nil {
			session.TeamPatch = team
			result.teamPatch = team
			result.applied = append(result.applied, "team")
		}
	}

	if patch.sets(buildFieldTags) {
		tags := make([]string, 0, len(patch.Tags))
		for _, tag := range patch.Tags {
			tag = strings.TrimSpace(tag)
			if tag == "" || utf8.RuneCountInString(tag) > buildMaxTag || len(tags) == buildMaxTags {
				continue
			}
			tags = append(tags, tag)
		}
		draft.Tags = tags
		result.applied = append(result.applied, "tags")
	}

	if patch.sets(buildFieldColor) {
		color := strings.ToLower(strings.TrimSpace(patch.Color))
		if !buildColors[color] {
			result.reject(buildFieldColor, buildReasonColor)
		} else {
			draft.Color = color
			result.applied = append(result.applied, "color")
		}
	}
	return result
}

// applyBuildInputs checks each value with the same validator creation uses
// and keeps the ones it accepts.
func applyBuildInputs(draft *personalassistant.BuildDraft, template projecttemplates.Template, hasTemplate bool, inputs []buildReplyInput, result *buildApplyResult) bool {
	if !hasTemplate || !template.HasInputs() || template.HasInvalidInputs() {
		result.reject(buildFieldInputs, buildReasonNoInputs)
		return false
	}
	if len(draft.ProjectConnection) > 0 {
		result.reject(buildFieldInputs, "an existing project keeps its own settings")
		return false
	}
	fields := map[string]projecttemplates.InputField{}
	for _, field := range template.Inputs.Fields {
		fields[field.ID] = field
	}
	applied := false
	for _, input := range inputs {
		id := strings.TrimSpace(input.ID)
		field, ok := fields[id]
		if !ok {
			result.reject("inputs."+id, "not one of this blueprint's inputs")
			continue
		}
		raw, err := buildInputRaw(field, input.Value)
		if err == nil {
			_, err = projecttemplates.ResolveInputValues(template.Inputs, map[string]json.RawMessage{id: raw})
		}
		if err != nil {
			result.reject("inputs."+id, blueprintInputMessage(err).Error())
			continue
		}
		if draft.BlueprintInputs == nil {
			draft.BlueprintInputs = map[string]json.RawMessage{}
		}
		draft.BlueprintInputs[id] = raw
		applied = true
	}
	return applied
}

// buildInputRaw turns the model's text into the JSON the create request
// carries: a number for a number field, a string otherwise.
func buildInputRaw(field projecttemplates.InputField, value string) (json.RawMessage, error) {
	value = strings.TrimSpace(value)
	if field.Type == projecttemplates.InputFieldNumber {
		number, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, fmt.Errorf("%s must be a number", field.Label)
		}
		return json.RawMessage(strconv.FormatFloat(number, 'f', -1, 64)), nil
	}
	encoded, err := json.Marshal(value)
	return json.RawMessage(encoded), err
}

// validateBuildTeam turns the proposal into role fills the Team step can
// apply: blueprint agents to create or reuse, declared roles to fill, saved
// teammates to add, or no agents for Blank.
func validateBuildTeam(draft personalassistant.BuildDraft, template projecttemplates.Template, hasTemplate bool, team buildReplyTeam, v buildValidation, result *buildApplyResult) *personalassistant.BuildTeamPatch {
	if !draft.HasBlueprint() {
		result.reject(buildFieldTeam, buildReasonNoBlueprint)
		return nil
	}
	out := &personalassistant.BuildTeamPatch{Mode: "staffed"}
	if strings.EqualFold(strings.TrimSpace(team.Mode), "agentless") {
		if !draft.Blank {
			result.reject("team.mode", buildReasonAgentless)
			return nil
		}
		return &personalassistant.BuildTeamPatch{Mode: "agentless"}
	}
	roles := []buildCatalogPromptRole{}
	if hasTemplate {
		roles = buildRoleDeclarations(template)
	}
	roleByID := map[string]buildCatalogPromptRole{}
	for _, role := range roles {
		roleByID[role.RoleID] = role
	}
	filled := map[string]bool{}
	addRole := func(role personalassistant.BuildRole) {
		if filled[role.RoleID] {
			return
		}
		filled[role.RoleID] = true
		out.Roles = append(out.Roles, role)
	}

	if hasTemplate && template.AssistantProgram == nil {
		ids := projecttemplates.AgentRoleIDs(template.Agents)
		for _, agent := range team.Agents {
			index := -1
			for i, spec := range template.Agents {
				if strings.EqualFold(spec.Name, strings.TrimSpace(agent.Name)) {
					index = i
					break
				}
			}
			if index < 0 {
				result.reject("team.agents."+agent.Name, buildReasonUnknownRole)
				continue
			}
			role := personalassistant.BuildRole{RoleID: ids[index]}
			switch strings.ToLower(strings.TrimSpace(agent.Action)) {
			case "reuse", "assign":
				name := strings.TrimSpace(agent.Rename)
				if name == "" {
					name = agent.Name
				}
				canonical, ok := v.savedAgent(name)
				if !ok {
					result.reject("team.agents."+agent.Name, unknownAgentReason(name))
					continue
				}
				role.Mode, role.AgentName = "assign", canonical
			default:
				name := strings.TrimSpace(agent.Rename)
				if name == "" {
					name = template.Agents[index].Name
				}
				if utf8.RuneCountInString(name) > buildMaxAgentName {
					result.reject("team.agents."+agent.Name, buildReasonTooLong)
					continue
				}
				role.Mode, role.AgentName = "create", name
				if provider, model := strings.TrimSpace(agent.Provider), strings.TrimSpace(agent.Model); provider != "" || model != "" {
					if v.modelOK == nil || !v.modelOK(provider, model) {
						result.reject("team.agents."+agent.Name+".model", buildReasonModel)
					} else {
						role.Provider, role.Model = provider, model
					}
				}
				if prompt := strings.TrimSpace(agent.SystemPrompt); prompt != "" && utf8.RuneCountInString(prompt) <= buildMaxPrompt {
					role.SystemPrompt = prompt
				}
			}
			addRole(role)
		}
	} else if len(team.Agents) > 0 {
		result.reject("team.agents", buildReasonUnknownRole)
	}

	for _, proposed := range team.Roles {
		id := strings.TrimSpace(proposed.RoleID)
		if _, ok := roleByID[id]; !ok {
			result.reject("team.roles."+id, buildReasonUnknownRole)
			continue
		}
		name := strings.TrimSpace(proposed.AgentName)
		switch strings.ToLower(strings.TrimSpace(proposed.Mode)) {
		case "assign", "reuse":
			canonical, ok := v.savedAgent(name)
			if !ok {
				result.reject("team.roles."+id, unknownAgentReason(name))
				continue
			}
			addRole(personalassistant.BuildRole{RoleID: id, Mode: "assign", AgentName: canonical})
		default:
			if name == "" {
				name = roleByID[id].Label
			}
			if utf8.RuneCountInString(name) > buildMaxAgentName {
				result.reject("team.roles."+id, buildReasonTooLong)
				continue
			}
			addRole(personalassistant.BuildRole{RoleID: id, Mode: "create", AgentName: name})
		}
	}

	for _, name := range team.SavedAgents {
		canonical, ok := v.savedAgent(name)
		if !ok {
			result.reject("team.saved_agents."+strings.TrimSpace(name), unknownAgentReason(name))
			continue
		}
		out.SavedAgents = append(out.SavedAgents, canonical)
	}
	if len(out.Roles) == 0 && len(out.SavedAgents) == 0 {
		return nil
	}
	return out
}
