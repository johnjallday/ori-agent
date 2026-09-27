package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
)

// The model's reply to one build turn (FR14). Strict structured output needs
// every property present, so "not set" is carried by Patch.Set — the list of
// patch fields this reply sets — rather than by absent keys, and inputs are a
// list of {id, value} rather than a map. An empty Ask.Question is "no
// question". The host validates everything here before any of it reaches the
// session (FR15); the model has no other way to change the form.
type buildReply struct {
	Say          string                `json:"say" jsonschema_description:"What you say to the user now, plain text, at most 600 characters. Describe what you just did on the form."`
	Ask          buildReplyAsk         `json:"ask" jsonschema_description:"One clear question, or an empty question when you are not asking anything."`
	Patch        buildReplyPatch       `json:"patch" jsonschema_description:"The form fields this reply sets. List each one you set in patch.set."`
	Why          []buildReplyWhy       `json:"why" jsonschema_description:"One short reason per section you changed."`
	Alternatives []buildReplyAlternate `json:"alternatives" jsonschema_description:"Up to two other blueprints that could fit, or an empty list."`
	Ready        bool                  `json:"ready" jsonschema_description:"True when blueprint, name, description and team are all decided."`
	CreateNow    bool                  `json:"create_now" jsonschema_description:"True only when the user has just told you to create the workspace."`
}

type buildReplyAsk struct {
	Question      string             `json:"question" jsonschema_description:"The question, or empty for none."`
	Choices       []buildReplyChoice `json:"choices" jsonschema_description:"Up to four short answers the user can tap. Empty when the question needs typing."`
	AllowFreeText bool               `json:"allow_free_text" jsonschema_description:"Whether the user may also type an answer."`
}

type buildReplyChoice struct {
	ID    string `json:"id" jsonschema_description:"A short id for this choice."`
	Label string `json:"label" jsonschema_description:"The answer as the user would say it, at most 60 characters."`
}

type buildReplyPatch struct {
	Set         []string          `json:"set" jsonschema_description:"Names of the fields below that this reply sets: blueprint_id, name, description, inputs, parent_id, ask_folder, team, tags, color."`
	BlueprintID string            `json:"blueprint_id" jsonschema_description:"A blueprint id from the catalog, or empty for Blank."`
	Name        string            `json:"name" jsonschema_description:"The workspace name, at most 80 characters."`
	Description string            `json:"description" jsonschema_description:"What the workspace is for, one or two sentences."`
	Inputs      []buildReplyInput `json:"inputs" jsonschema_description:"Values for the blueprint's declared inputs."`
	ParentID    string            `json:"parent_id" jsonschema_description:"A group id from the list of groups, or empty for no group."`
	AskFolder   bool              `json:"ask_folder" jsonschema_description:"True to offer the user a folder picker for an existing project."`
	Team        buildReplyTeam    `json:"team" jsonschema_description:"The team."`
	Tags        []string          `json:"tags" jsonschema_description:"Short tags."`
	Color       string            `json:"color" jsonschema_description:"One of the offered colors, or empty for the default."`
}

type buildReplyInput struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

type buildReplyTeam struct {
	Mode        string            `json:"mode" jsonschema_description:"staffed, or agentless for a Blank workspace with no agents."`
	Agents      []buildReplyAgent `json:"agents" jsonschema_description:"For blueprints that declare agents: create the blueprint's agent, or reuse a saved agent for it."`
	Roles       []buildReplyRole  `json:"roles" jsonschema_description:"For blueprints that declare roles: fill a role by creating an agent or assigning a saved one."`
	SavedAgents []string          `json:"saved_agents" jsonschema_description:"Names of saved agents to add as extra teammates."`
}

type buildReplyAgent struct {
	Name         string `json:"name" jsonschema_description:"The blueprint agent's name."`
	Action       string `json:"action" jsonschema_description:"create or reuse."`
	Rename       string `json:"rename" jsonschema_description:"For create, a new name, or empty. For reuse, the saved agent's name."`
	Model        string `json:"model" jsonschema_description:"Empty to keep the blueprint's model."`
	Provider     string `json:"provider" jsonschema_description:"Empty to keep the blueprint's provider."`
	SystemPrompt string `json:"system_prompt" jsonschema_description:"Empty to keep the blueprint's prompt."`
}

type buildReplyRole struct {
	RoleID    string `json:"role_id"`
	Mode      string `json:"mode" jsonschema_description:"create or assign."`
	AgentName string `json:"agent_name"`
}

type buildReplyWhy struct {
	Section string `json:"section" jsonschema_description:"blueprint, details, team, or placement."`
	Text    string `json:"text"`
}

type buildReplyAlternate struct {
	BlueprintID string `json:"blueprint_id"`
	Reason      string `json:"reason"`
}

var buildReplySchema = llm.GenerateSchema[buildReply]()

// The patch fields a reply may name in Patch.Set.
const (
	buildFieldBlueprint   = "blueprint_id"
	buildFieldName        = "name"
	buildFieldDescription = "description"
	buildFieldInputs      = "inputs"
	buildFieldParent      = "parent_id"
	buildFieldAskFolder   = "ask_folder"
	buildFieldTeam        = "team"
	buildFieldTags        = "tags"
	buildFieldColor       = "color"
)

// sets reports whether the reply names field in Patch.Set. A reply from the
// plain chat path that left Set empty is read by what it filled in instead.
func (p buildReplyPatch) sets(field string) bool {
	if len(p.Set) > 0 {
		for _, name := range p.Set {
			if strings.EqualFold(strings.TrimSpace(name), field) {
				return true
			}
		}
		return false
	}
	switch field {
	case buildFieldBlueprint:
		return strings.TrimSpace(p.BlueprintID) != ""
	case buildFieldName:
		return strings.TrimSpace(p.Name) != ""
	case buildFieldDescription:
		return strings.TrimSpace(p.Description) != ""
	case buildFieldInputs:
		return len(p.Inputs) > 0
	case buildFieldParent:
		return strings.TrimSpace(p.ParentID) != ""
	case buildFieldAskFolder:
		return p.AskFolder
	case buildFieldTeam:
		return p.Team.Mode != "" || len(p.Team.Agents) > 0 || len(p.Team.Roles) > 0 || len(p.Team.SavedAgents) > 0
	case buildFieldTags:
		return len(p.Tags) > 0
	case buildFieldColor:
		return strings.TrimSpace(p.Color) != ""
	}
	return false
}

// WorkspaceBuildModel is the provider and model a build session talks to.
type WorkspaceBuildModel struct {
	Provider     llm.Provider
	ProviderName string
	Model        string
}

// ErrWorkspaceBuildNoModel means neither the assistant's own model nor the
// system model resolves (FR30).
var ErrWorkspaceBuildNoModel = errors.New("workspace build: no model is available")

// workspaceBuildModelTimeout bounds one model call (FR13).
const workspaceBuildModelTimeout = 60 * time.Second

// buildCatalogLimit bounds the blueprints the prompt lists (FR32).
const buildCatalogLimit = 60

// buildContextBudget bounds the transcript text sent to the model. The oldest
// user/assistant pairs leave the model's context first; the stored transcript
// keeps them until its own cap.
const buildContextBudget = 12000

// buildPromptInput is everything one model call is allowed to see (FR33):
// what the user typed in the pane, the draft, and catalog, roster, and group
// metadata. Never folder contents, notes, or another workspace's data.
type buildPromptInput struct {
	AssistantName string
	Session       *personalassistant.WorkspaceBuildSession
	Catalog       []WorkspaceBuildCatalogEntry
	Agents        []buildSavedAgent
	Groups        []buildGroup
	Nudge         bool
}

type buildSavedAgent struct {
	Name  string `json:"name"`
	Role  string `json:"role,omitempty"`
	Model string `json:"model,omitempty"`
}

type buildGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// buildCatalogPromptEntry is one blueprint as the model sees it.
type buildCatalogPromptEntry struct {
	ID                      string                    `json:"id"`
	Name                    string                    `json:"name"`
	Tagline                 string                    `json:"tagline,omitempty"`
	Description             string                    `json:"description,omitempty"`
	Tags                    []string                  `json:"tags,omitempty"`
	Inputs                  []buildCatalogPromptInput `json:"inputs,omitempty"`
	Roles                   []buildCatalogPromptRole  `json:"roles,omitempty"`
	SupportsExistingProject bool                      `json:"supports_existing_project"`
	NeedsHome               bool                      `json:"needs_home"`
}

type buildCatalogPromptInput struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Type    string   `json:"type"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	Options []string `json:"options,omitempty"`
}

type buildCatalogPromptRole struct {
	RoleID   string `json:"role_id"`
	Label    string `json:"label"`
	Required bool   `json:"required,omitempty"`
}

func truncateRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}

// supportsExistingProject mirrors the wizard's blueprintDetailsProfile: the
// blueprint can attach a folder the user already has.
func supportsExistingProject(template projecttemplates.Template) bool {
	if template.ProjectConnection == nil {
		return false
	}
	for _, mode := range template.ProjectConnection.SupportedModes {
		if string(mode) == "existing_project" {
			return true
		}
	}
	return false
}

func needsHome(template projecttemplates.Template) bool {
	return template.GroupRequirement != nil && template.GroupRequirement.Policy == projecttemplates.GroupPolicyRequired
}

// buildRoleDeclarations are the roles a blueprint's Team step offers: an
// assistant program's project roles, or one role per declared agent.
func buildRoleDeclarations(template projecttemplates.Template) []buildCatalogPromptRole {
	if program := template.AssistantProgram; program != nil {
		roles := make([]buildCatalogPromptRole, 0, len(program.Roles))
		for _, role := range program.Roles {
			if string(role.Scope) == "home" {
				continue
			}
			roles = append(roles, buildCatalogPromptRole{RoleID: role.ID, Label: role.Label, Required: role.Required})
		}
		return roles
	}
	ids := projecttemplates.AgentRoleIDs(template.Agents)
	roles := make([]buildCatalogPromptRole, 0, len(template.Agents))
	for i, agent := range template.Agents {
		roles = append(roles, buildCatalogPromptRole{RoleID: ids[i], Label: agent.Name, Required: i == 0})
	}
	return roles
}

func catalogPromptEntry(entry WorkspaceBuildCatalogEntry) buildCatalogPromptEntry {
	template := entry.Template
	out := buildCatalogPromptEntry{
		ID:                      template.ID,
		Name:                    template.Name,
		Tagline:                 truncateRunes(template.Tagline, 120),
		Description:             truncateRunes(template.Description, 200),
		Tags:                    template.Tags,
		SupportsExistingProject: supportsExistingProject(template),
		NeedsHome:               needsHome(template),
	}
	if template.Inputs != nil && template.InputsError == "" {
		for _, field := range template.Inputs.Fields {
			input := buildCatalogPromptInput{ID: field.ID, Label: field.Label, Type: string(field.Type)}
			if field.Type == projecttemplates.InputFieldNumber {
				minValue, maxValue := field.Min, field.Max
				input.Min, input.Max = &minValue, &maxValue
			}
			for _, option := range field.Options {
				input.Options = append(input.Options, option.Value)
			}
			out.Inputs = append(out.Inputs, input)
		}
	}
	// Every blueprint's team is offered as roles with ids, one vocabulary for
	// the model whichever kind of blueprint it is.
	out.Roles = buildRoleDeclarations(template)
	return out
}

// relevantCatalog orders the offerable blueprints by how many words of the
// conversation their name, tags, and tagline share, and keeps the first 60.
func relevantCatalog(catalog []WorkspaceBuildCatalogEntry, conversation string) []WorkspaceBuildCatalogEntry {
	words := buildWords(conversation)
	type scored struct {
		entry WorkspaceBuildCatalogEntry
		score int
		index int
	}
	ranked := make([]scored, 0, len(catalog))
	for i, entry := range catalog {
		ranked = append(ranked, scored{entry: entry, score: blueprintMatchScore(entry.Template, words), index: i})
	}
	sort.SliceStable(ranked, func(a, b int) bool {
		if ranked[a].score != ranked[b].score {
			return ranked[a].score > ranked[b].score
		}
		return ranked[a].index < ranked[b].index
	})
	out := make([]WorkspaceBuildCatalogEntry, 0, min(len(ranked), buildCatalogLimit))
	for _, item := range ranked {
		if len(out) == buildCatalogLimit {
			break
		}
		out = append(out, item.entry)
	}
	return out
}

func buildWords(text string) map[string]bool {
	words := map[string]bool{}
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if len(word) >= 3 {
			words[word] = true
			words[strings.TrimSuffix(word, "s")] = true
		}
	}
	return words
}

// blueprintMatchScore counts conversation words in a blueprint's name and tags
// (weighted) and its tagline and description.
func blueprintMatchScore(template projecttemplates.Template, words map[string]bool) int {
	score := 0
	for word := range buildWords(template.Name + " " + template.ID) {
		if words[word] {
			score += 3
		}
	}
	for _, tag := range template.Tags {
		for word := range buildWords(tag) {
			if words[word] {
				score += 3
			}
		}
	}
	for word := range buildWords(template.Tagline + " " + template.Description) {
		if words[word] {
			score++
		}
	}
	return score
}

// buildSystemPrompt states the job, the rules, and everything the assistant
// may choose from (FR32).
func buildSystemPrompt(input buildPromptInput) string {
	session := input.Session
	var conversation strings.Builder
	for _, entry := range session.Transcript {
		if entry.Role != personalassistant.BuildRoleAssistant {
			conversation.WriteString(entry.Text)
			conversation.WriteString(" ")
		}
	}
	catalog := relevantCatalog(input.Catalog, conversation.String())
	entries := make([]buildCatalogPromptEntry, 0, len(catalog))
	for _, entry := range catalog {
		entries = append(entries, catalogPromptEntry(entry))
	}
	catalogJSON, _ := json.Marshal(entries)
	agentsJSON, _ := json.Marshal(input.Agents)
	groupsJSON, _ := json.Marshal(input.Groups)
	draftJSON, _ := json.Marshal(buildPromptDraft(session))
	rejectionsJSON, _ := json.Marshal(session.Rejections)

	name := strings.TrimSpace(input.AssistantName)
	if name == "" {
		name = "the assistant"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s, the user's Personal Assistant in Ori. ", name)
	b.WriteString("Right now you are setting up a new workspace for the user by filling in the Create Workspace form shown beside this chat. ")
	b.WriteString("The user watches each field change as you set it, then checks the form and creates the workspace themselves.\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("- Every reply must set at least one field (and list it in patch.set) or ask one clear question. Prefer doing both. Never reply with talk alone.\n")
	b.WriteString("- After the user's first description, set blueprint_id, name, and description in that same reply.\n")
	b.WriteString("- Ask one question at a time. Prefer short choices (at most four) for: assigning a saved agent or creating one, another blueprint, and which group when the user has more than one.\n")
	b.WriteString("- To offer linking a folder the user already has, set patch.ask_folder to true and ask about it; the form shows the folder choices. Only blueprints with supports_existing_project can link a folder.\n")
	b.WriteString("- Staff the team only through patch.team.roles, using the chosen blueprint's role ids: mode \"assign\" with the exact name of one of the user's saved agents, or mode \"create\" with a name for a new agent (usually the role's label). Leave patch.team.agents empty. Ask before assigning a saved agent the user has not mentioned.\n")
	b.WriteString("- Use only the blueprint ids, input ids and option values, agent names, role ids, and group ids listed below. The form refuses anything else, and you will see each refusal next turn.\n")
	b.WriteString("- A blueprint with needs_home lives inside a group it sets up when the user creates the workspace. Say so when you choose one.\n")
	b.WriteString("- Never claim the workspace is created. When the user tells you to create it, set create_now to true.\n")
	b.WriteString("- When blueprint, name, description, and team are decided, set ready to true and say: Ready. Check the review on the left, or just tell me \"create it\".\n")
	b.WriteString("- When the user changed the form themselves, acknowledge it in a few words and re-decide what depended on it (a new blueprint needs a new team).\n")
	b.WriteString("- \"say\" is plain text, at most 600 characters, no markdown. Say what you did, not what you will do. Put your question only in ask.question, never in say as well; the form shows them together.\n")
	b.WriteString("- Choice labels are short answers (at most 60 characters), such as \"Assign Luna\" or \"Make a new one\".\n\n")
	fmt.Fprintf(&b, "Blueprints (id \"\" is Blank):\n%s\n\n", catalogJSON)
	fmt.Fprintf(&b, "The user's saved agents:\n%s\n\n", agentsJSON)
	fmt.Fprintf(&b, "The user's groups:\n%s\n\n", groupsJSON)
	fmt.Fprintf(&b, "The form now:\n%s\n\n", draftJSON)
	if len(session.Rejections) > 0 {
		fmt.Fprintf(&b, "The form refused these from your last reply:\n%s\n\n", rejectionsJSON)
	}
	if input.Nudge {
		b.WriteString("Your last reply neither set a field nor asked a question. This time, set at least one field or ask one clear question.\n\n")
	}
	return b.String()
}

// buildPromptDraft is the form as the model sees it: the fields it can set,
// plus a plain summary of the team.
func buildPromptDraft(session *personalassistant.WorkspaceBuildSession) map[string]any {
	draft := session.Draft
	out := map[string]any{
		"blueprint_id": draft.TemplateID,
		"blank":        draft.Blank,
		"name":         draft.Name,
		"description":  draft.Description,
		"parent_id":    draft.ParentID,
		"tags":         draft.Tags,
		"color":        draft.Color,
	}
	if len(draft.BlueprintInputs) > 0 {
		inputs := map[string]string{}
		for id, raw := range draft.BlueprintInputs {
			inputs[id] = strings.Trim(string(raw), "\"")
		}
		out["inputs"] = inputs
	}
	if team := session.TeamPatch; team != nil {
		out["team"] = team
	}
	if len(draft.RoleStaffing) > 0 {
		out["role_staffing"] = json.RawMessage(draft.RoleStaffing)
	}
	if len(draft.ExistingAgentNames) > 0 {
		out["saved_agents"] = draft.ExistingAgentNames
	}
	if len(draft.ProjectConnection) > 0 {
		out["folder_linked"] = true
	}
	return out
}

// buildMessages is the conversation as chat messages, newest last, within the
// context budget. Fixed host lines (a model failure) are left out; the user's
// own form edits are passed as short user notes.
func buildMessages(session *personalassistant.WorkspaceBuildSession) []llm.Message {
	messages := make([]llm.Message, 0, len(session.Transcript))
	for _, entry := range session.Transcript {
		switch entry.Role {
		case personalassistant.BuildRoleUser:
			messages = append(messages, llm.Message{Role: llm.RoleUser, Content: entry.Text})
		case personalassistant.BuildRoleForm:
			messages = append(messages, llm.Message{Role: llm.RoleUser, Content: "[I edited the form myself] " + entry.Text})
		case personalassistant.BuildRoleAssistant:
			if entry.Fixed {
				continue
			}
			messages = append(messages, llm.Message{Role: llm.RoleAssistant, Content: entry.Text})
		}
	}
	total := 0
	for _, message := range messages {
		total += len(message.Content)
	}
	for total > buildContextBudget && len(messages) > 1 {
		total -= len(messages[0].Content)
		messages = messages[1:]
	}
	// A conversation must start with the user.
	for len(messages) > 1 && messages[0].Role != llm.RoleUser {
		messages = messages[1:]
	}
	return messages
}

// callBuildModel runs one model call with the structured-output path when the
// provider has it, and a JSON-only chat otherwise (FR31). The reply is decoded
// strictly: an unknown field is a failed reply, not a partial one.
func callBuildModel(ctx context.Context, model WorkspaceBuildModel, system string, messages []llm.Message) (buildReply, error) {
	if model.Provider == nil {
		return buildReply{}, ErrWorkspaceBuildNoModel
	}
	ctx, cancel := context.WithTimeout(ctx, workspaceBuildModelTimeout)
	defer cancel()
	var content string
	if structured, ok := model.Provider.(llm.StructuredOutputProvider); ok {
		resp, err := structured.ChatWithStructuredOutput(ctx, llm.StructuredOutputRequest{
			Model:        model.Model,
			Messages:     messages,
			SystemPrompt: system,
			SchemaName:   "workspace_build_reply",
			Schema:       buildReplySchema,
		})
		if err != nil {
			return buildReply{}, err
		}
		content = resp.Content
	} else {
		resp, err := model.Provider.Chat(ctx, llm.ChatRequest{
			Model:        model.Model,
			Messages:     messages,
			SystemPrompt: system + buildJSONInstruction,
			Temperature:  0.2,
			MaxTokens:    2000,
		})
		if err != nil {
			return buildReply{}, err
		}
		content = resp.Content
	}
	return decodeBuildReply(content)
}

// buildJSONInstruction is appended for providers without structured output.
const buildJSONInstruction = `
Reply with one JSON object only, no prose around it, with exactly these keys:
{"say":"","ask":{"question":"","choices":[{"id":"","label":""}],"allow_free_text":true},
"patch":{"set":[],"blueprint_id":"","name":"","description":"","inputs":[{"id":"","value":""}],"parent_id":"","ask_folder":false,
"team":{"mode":"","agents":[{"name":"","action":"","rename":"","model":"","provider":"","system_prompt":""}],"roles":[{"role_id":"","mode":"","agent_name":""}],"saved_agents":[]},
"tags":[],"color":""},"why":[{"section":"","text":""}],"alternatives":[{"blueprint_id":"","reason":""}],"ready":false,"create_now":false}`

func decodeBuildReply(content string) (buildReply, error) {
	payload := strings.TrimSpace(llm.StripCodeFence(content))
	if payload == "" {
		return buildReply{}, errors.New("workspace build: empty model reply")
	}
	var reply buildReply
	decoder := json.NewDecoder(bytes.NewReader([]byte(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reply); err != nil {
		return buildReply{}, fmt.Errorf("workspace build: undecodable model reply: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return buildReply{}, errors.New("workspace build: trailing data after the model reply")
	}
	return reply, nil
}
