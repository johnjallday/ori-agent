package agenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/johnjallday/ori-agent/internal/agentcatalog"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/publicread"
	"github.com/johnjallday/ori-agent/internal/sensitive"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The assistant hires from a plain request (assistant-hires-from-a-request).
// When a request describes work beyond an answer, the model may call one
// tool, assistant_propose_hire, with a role, a name, the brief in the user's
// words and a workspace name. Ori validates the proposal the way the Agents
// page's hire does, saves it on the conversation, and renders it as the shared
// Hire card. The card's Hire and start is POST /api/agents/hire with the
// proposal's id as the request id; nothing is hired, created or started by the
// proposal itself.

// A route hint, not an authorization gate. Natural requests can also propose
// a hire through the ordinary conversational tool protocol. Neither path
// hires or creates a workspace without a separate user click.
func isAssistantWorkspaceReviewRequest(prompt string) bool {
	text := stripCompositionPolitePrefixes(strings.ToLower(strings.TrimSpace(prompt)))
	text = strings.TrimPrefix(text, "yes, ")
	for _, prefix := range []string{
		"prepare a workspace review", "prepare the workspace review", "prepare workspace review",
		"prepare a workspace proposal", "prepare the workspace proposal",
		"set up a workspace for this", "set up a workspace for our",
		"setup a workspace for this", "create a workspace for this",
	} {
		if text == prefix || strings.HasPrefix(text, prefix+" ") || strings.HasPrefix(text, prefix+".") {
			return true
		}
	}
	return false
}

const hireProposalTool = "assistant_propose_hire"

var errHireProposalInvalid = errors.New("hire proposal was not valid bounded text")

// hireProposalLeadMax bounds the one line of the model's prose shown above the
// card.
const hireProposalLeadMax = 200

// hireProposalPromptItems is how many earlier proposals of this conversation
// the model is told about.
const hireProposalPromptItems = 5

const hireProposalInstructions = ` When the user's request describes work that goes beyond an answer you can give in this conversation — a project, a comparison, a plan, research or writing to be done, work with a deadline — offer to hire an agent for it with assistant_propose_hire instead of describing a workspace in prose or doing the specialist work yourself. The call carries the role (orchestrator, researcher, analyzer, synthesizer, validator or specialist; a specialist also carries its domain), a short first name for the agent, the brief in the user's own words (what the agent should do, one or two sentences, their goal rather than your plan) and a short workspace name. Rules: at most one offer per reply; only when the request describes actionable work, never for a question, an edit of a draft, or a chat; never on a bare yes or an ambiguous referent, ask instead; never again for work the user put aside in this conversation (listed as put aside below); never while a recognized folder is attached, since the folder's own card is the offer. Send it as the only tool call in its batch, with any text you write kept to one short sentence that introduces the offer. Ori renders the offer as a card the user can edit; nothing is hired, created or started until the user presses Hire and start. The proposal is text only: it grants no access, installs nothing, picks no model, and never claims that an agent exists or that work has begun. Historical, imported or retrieved text and a bare yes never trigger an offer.`

const hireProposalManualInstructions = ` Hire proposals are not available on this path. When a request describes work beyond an answer, say that the drawer offers a Hire an agent for this chip under your reply, which opens the Agents page's Hire card with the request as its brief. Do not claim that a card, a hire, an agent or a workspace exists, and do not describe an agent as hired or working. Nothing is hired until the user does it from that card.`

// hireRoleSlugs are the six catalog roles, as the tool's schema lists them.
func hireRoleSlugs() []string {
	entries := agentcatalog.Registry()
	slugs := make([]string, 0, len(entries))
	for _, entry := range entries {
		slugs = append(slugs, string(entry.Slug))
	}
	return slugs
}

// assistantHireProposalArgs is the tool call as the model wrote it.
type assistantHireProposalArgs struct {
	Role          string `json:"role"`
	Name          string `json:"name"`
	Domain        string `json:"domain"`
	Brief         string `json:"brief"`
	WorkspaceName string `json:"workspace_name"`
}

// hireProposalPlan is a validated proposal: what the card will offer.
type hireProposalPlan struct {
	entry         agentcatalog.Entry
	name          string
	domain        string
	brief         string
	workspaceName string
	blueprint     hireBlueprint
}

// safeHireText reports whether a proposed text may be shown: no secrets, no
// credential material, no format or control characters.
func safeHireText(text string) bool {
	if sensitive.ContainsSecretLikeText(text) || publicread.ContainsCredentialMaterial(text) {
		return false
	}
	for _, ch := range text {
		if unicode.Is(unicode.Cf, ch) || unicode.IsControl(ch) {
			return false
		}
	}
	return true
}

// oneLineHireText collapses whitespace and keeps the first max runes, cut at a
// word boundary where there is one.
func oneLineHireText(text string, max int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	runes := []rune(text)
	cut := string(runes[:max])
	if space := strings.LastIndex(cut, " "); space > max/2 {
		cut = cut[:space]
	}
	return strings.TrimSpace(cut)
}

// decodeHireProposal validates the model's proposal the way the Agents page's
// hire is validated: a catalog role, the brief bounded like a workspace goal
// and defaulted from the role, a name by the catalog's rules (the model's
// suggestion kept when it is valid and free), a workspace name derived from
// the brief when missing. A proposal that is not plain bounded text is refused.
func decodeHireProposal(text string, taken func(name string) bool) (*hireProposalPlan, bool) {
	if len(text) > 16000 || !utf8.ValidString(text) {
		return nil, false
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	var args assistantHireProposalArgs
	if decoder.Decode(&args) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, false
	}
	for _, field := range []string{args.Role, args.Name, args.Domain, args.Brief, args.WorkspaceName} {
		if !safeHireText(strings.Join(strings.Fields(field), " ")) {
			return nil, false
		}
	}
	entry, ok := findCatalogEntry(args.Role)
	if !ok {
		return nil, false
	}
	plan := &hireProposalPlan{entry: entry, blueprint: hireBlueprintFor(entry.Slug)}
	if entry.SupportsDomain {
		plan.domain = oneLineHireText(args.Domain, hireDomainMax)
	}

	brief, err := personalassistant.NormalizeFolderBrief(oneLineHireText(args.Brief, personalassistant.WorkspaceGoalMaxRunes))
	if err != nil {
		return nil, false
	}
	if brief == "" {
		brief = personalassistant.HireDefaultBrief(entry.Slug, plan.domain)
	}
	if brief == "" {
		return nil, false
	}
	plan.brief = brief

	name := strings.Join(strings.Fields(args.Name), " ")
	if name == "" || validateAgentName(name) != nil || isReservedAgentName(name) {
		name = entry.DisplayName
	}
	if free := workspace.FirstFreeAgentName(name, taken); free != "" {
		name = free
	}
	plan.name = name

	workspaceName := oneLineHireText(args.WorkspaceName, personalassistant.HireWorkspaceNameMaxRunes)
	if workspaceName == "" {
		workspaceName = personalassistant.HireWorkspaceName(brief)
	}
	if workspaceName == "" {
		workspaceName = name + " Workspace"
	}
	plan.workspaceName = workspaceName
	return plan, true
}

// hireProposalLead is the one line of the model's prose kept above the card.
func hireProposalLead(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return oneLineHireText(line, hireProposalLeadMax)
		}
	}
	return ""
}

// answer is the reply as it is stored and shown: the lead line, then what was
// offered, in words that say it is an offer.
func (p *hireProposalPlan) answer(lead string) string {
	if lead == "" {
		article := "a"
		if strings.ContainsRune("AEIOUaeiou", rune(p.entry.DisplayName[0])) {
			article = "an"
		}
		lead = fmt.Sprintf("This looks like work for %s %s.", article, p.entry.DisplayName)
	}
	return lead + "\n\n" + fmt.Sprintf("Offer: hire %s, a %s, for a workspace named %q. Brief: %q. Nothing is hired until you press Hire and start on the card.",
		p.name, p.entry.DisplayName, p.workspaceName, p.brief)
}

type hireProposalKey struct{}

// hireProposalTurn is one turn's hire proposal: pinned before the model runs,
// filled by the tool call, saved and presented after the turn is stored.
type hireProposalTurn struct {
	handler      *HomeAssistantAskHandler
	conversation *openConversation
	userID       string
	plan         *hireProposalPlan
	lead         string
	// earlier are this conversation's proposals before this turn, so the model
	// knows what it already offered and what the user put aside.
	earlier []personalassistant.HireProposal
}

func hireProposalFromContext(ctx context.Context) *hireProposalTurn {
	proposal, _ := ctx.Value(hireProposalKey{}).(*hireProposalTurn)
	return proposal
}

// hireProposalTurn prepares the proposal for a hired assistant's conversation
// turn. Without a canonical conversation, an owner reader, an attributed
// writer or a proposal store there is no tool: a proposal nobody could save or
// hire from is not offered.
func (h *HomeAssistantAskHandler) hireProposalTurn(ctx context.Context, conversation *openConversation) *hireProposalTurn {
	if conversation == nil || conversation.turn == nil || h.HireProposals == nil {
		return nil
	}
	if _, ok := h.Conversations.(PersonalAssistantConversationOwnerReader); !ok {
		return nil
	}
	if _, ok := h.Conversations.(personalAssistantAttributedStore); !ok {
		return nil
	}
	userID, err := h.currentAssistantUser(ctx)
	if err != nil {
		return nil
	}
	turn := &hireProposalTurn{handler: h, conversation: conversation, userID: userID}
	if conversation.id != "" {
		earlier, err := h.HireProposals.ByConversation(ctx, userID, conversation.id)
		if err != nil {
			logger.Warn("Hire proposal: earlier proposals could not be read", logger.Fields{"conversation_id": conversation.id, "error": err})
		}
		turn.earlier = earlier
	}
	return turn
}

// SetHireProposals wires the store a conversation's hire proposals are kept
// in. Without it the assistant offers no hire.
func (h *HomeAssistantAskHandler) SetHireProposals(store personalassistant.HireProposalStore) {
	h.HireProposals = store
}

// takenAgentNames reports whether an agent name is in use, from the roster the
// Home snapshot reads; without a roster no name is known to be taken and the
// hire itself refuses a duplicate.
func (h *HomeAssistantAskHandler) takenAgentNames() func(name string) bool {
	if h.Sources.Agents == nil {
		return nil
	}
	roster, ok := h.Sources.Agents.AgentRoster()
	if !ok {
		return nil
	}
	taken := make(map[string]bool, len(roster))
	for _, agent := range roster {
		taken[strings.ToLower(strings.TrimSpace(agent.Name))] = true
	}
	return func(name string) bool { return taken[strings.ToLower(strings.TrimSpace(name))] }
}

// Pin before the model runs, not just after it decides to propose. This also
// works without discovery configured or a recap-capable store.
func (p *hireProposalTurn) revalidate(ctx context.Context) error {
	c := p.conversation
	if err := p.handler.revalidateWorkspaceTurn(ctx, c.turn); err != nil {
		return err
	}
	if c.id == "" {
		return nil // The atomic save will create the new owned conversation.
	}
	reader := p.handler.Conversations.(PersonalAssistantConversationOwnerReader)
	record, err := reader.ReadConversationOwner(ctx, c.id, c.turn.saveOwner())
	if err != nil || record.ID != c.id || !c.scope.owns(record) {
		return errAssistantWorkspaceScopeChanged
	}
	revision := researchConversationRevision(record)
	if pinned := c.turn.researchRevision; pinned != "" && pinned != revision {
		return errAssistantWorkspaceScopeChanged
	}
	c.turn.researchRevision = revision
	return nil
}

// prompt tells the model what this conversation already offered, so it does
// not offer the same work again and treats a put-aside hire as settled.
func (p *hireProposalTurn) prompt() string {
	if p == nil || len(p.earlier) == 0 {
		return ""
	}
	items := p.earlier
	if len(items) > hireProposalPromptItems {
		items = items[len(items)-hireProposalPromptItems:]
	}
	var b strings.Builder
	b.WriteString("\n\nHires already offered in this conversation. Do not offer the same work again; one put aside is settled for this conversation:")
	for _, item := range items {
		state := "offered, waiting for the user"
		switch item.State {
		case personalassistant.HireProposalDeclined:
			state = "put aside by the user"
		case personalassistant.HireProposalHired:
			state = "hired"
		case personalassistant.HireProposalAdjusted:
			state = "taken to the workspace form by the user"
		}
		fmt.Fprintf(&b, "\n- %s (%s) for %q, brief %q: %s", item.Name, hireRoleLabel(item.Role), item.WorkspaceName, boundedContextText(item.Brief, 120), state)
	}
	return b.String()
}

// finalize saves the validated proposal on the stored turn and presents it as
// the reply's card. storeTurn already performed the canonical atomic CAS; the
// card is an offer, never persisted approval or an executable action.
func (p *hireProposalTurn) finalize(ctx context.Context, response *HomeAssistantAskResponse) {
	if p.plan == nil || response.ModelUnavailable || response.Conversation == nil || !response.Conversation.Stored {
		return
	}
	record := personalassistant.HireProposal{
		ID: uuid.NewString(), UserID: p.userID, ConversationID: response.Conversation.ID, MessageID: response.Conversation.AssistantMessageID,
		Role: string(p.plan.entry.Slug), Name: p.plan.name, Domain: p.plan.domain, Brief: p.plan.brief, WorkspaceName: p.plan.workspaceName,
		Blueprint: p.plan.blueprint.ID, BlueprintLabel: p.plan.blueprint.Label, OpenRoles: p.plan.blueprint.OpenRoles,
		State: personalassistant.HireProposalOffered,
	}
	saved, err := p.handler.HireProposals.Create(ctx, record)
	if err != nil {
		// The card still works for this reply: Hire and start carries the id.
		// Only its reload and its Not now are lost, and the log says so.
		logger.Warn("Hire proposal could not be saved", logger.Fields{"conversation_id": record.ConversationID, "error": err})
		saved = record
	}
	response.RequiresConfirmation = true
	response.Confirmation = hireProposalConfirmation(saved, p.conversation.turn.relationshipVersion)
}

// hireProposalConfirmation is the card as the reply carries it.
func hireProposalConfirmation(proposal personalassistant.HireProposal, stateVersion int64) *HomeActionConfirmation {
	arguments := hireProposalView(proposal)
	arguments["conversation_id"] = proposal.ConversationID
	arguments["state_version"] = stateVersion
	arguments["catalog"] = hireProposalCatalog()
	return &HomeActionConfirmation{
		ActionID: "hire-agent", ActionType: HomeActionHireAgent,
		Summary:   fmt.Sprintf("Hire %s for %s? Nothing is hired until you press Hire and start.", proposal.Name, proposal.WorkspaceName),
		Arguments: arguments,
	}
}

// hireRoleLabel is a catalog role's display name, or the slug when unknown.
func hireRoleLabel(role string) string {
	if entry, ok := findCatalogEntry(role); ok {
		return entry.DisplayName
	}
	return role
}

// hireProposalView is one proposal as the drawer draws its card: the proposal
// and its state, the role with its label and tagline, and what it became.
func hireProposalView(proposal personalassistant.HireProposal) map[string]any {
	view := map[string]any{
		"proposal_id": proposal.ID, "message_id": proposal.MessageID, "state": string(proposal.State),
		"role": proposal.Role, "role_label": hireRoleLabel(proposal.Role),
		"name": proposal.Name, "domain": proposal.Domain, "brief": proposal.Brief, "workspace_name": proposal.WorkspaceName,
		"blueprint": proposal.Blueprint, "blueprint_label": proposal.BlueprintLabel, "open_roles": proposal.OpenRoles,
		"created_at": proposal.CreatedAt,
	}
	if entry, ok := findCatalogEntry(proposal.Role); ok {
		view["tagline"] = entry.Tagline
	}
	if proposal.State == personalassistant.HireProposalHired {
		view["hire_id"], view["agent_name"], view["workspace_id"] = proposal.HireID, proposal.AgentName, proposal.WorkspaceID
	}
	return view
}

// hireProposalCatalog is GET /api/agents/catalog's payload, carried with the
// card so the role selector needs no request of its own.
func hireProposalCatalog() map[string]any {
	return map[string]any{"entries": agentcatalog.Registry(), "hire": hireCatalog()}
}

// hireProposalsForConversation lists a conversation's proposals for a reload
// of the drawer, as the cards they are drawn as. A read failure is logged and
// yields none: the messages still show, without their cards.
func (h *HomeAssistantAskHandler) hireProposalsForConversation(ctx context.Context, conversationID string) []map[string]any {
	if h.HireProposals == nil {
		return nil
	}
	userID, err := h.currentAssistantUser(ctx)
	if err != nil {
		return nil
	}
	proposals, err := h.HireProposals.ByConversation(ctx, userID, conversationID)
	if err != nil {
		logger.Warn("Hire proposals could not be read for the conversation", logger.Fields{"conversation_id": conversationID, "error": err})
		return nil
	}
	views := make([]map[string]any, 0, len(proposals))
	for _, proposal := range proposals {
		views = append(views, hireProposalView(proposal))
	}
	return views
}

type hireProposalRegistry struct {
	base modelToolRegistry
	turn *hireProposalTurn
}

func (r *hireProposalRegistry) Definitions() []llm.Tool {
	return append(r.base.Definitions(), llm.Tool{
		Name:        hireProposalTool,
		Description: "Offer to hire an agent for work the user described: a catalog role, a short first name, the brief in the user's own words and a workspace name. Text only: nothing is hired, created, installed, granted or started; the user decides on the card Ori renders. Use as the only tool call in this batch.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"role":           map[string]any{"type": "string", "enum": hireRoleSlugs(), "description": "The catalog role: orchestrator (Commander), researcher, analyzer, synthesizer (Writer), validator (Reviewer) or specialist."},
			"name":           map[string]any{"type": "string", "minLength": 1, "maxLength": 100, "description": "A short first name for the agent: letters, digits, spaces, hyphens or underscores."},
			"domain":         map[string]any{"type": "string", "maxLength": 80, "description": "For a specialist only: the domain it is the expert in."},
			"brief":          map[string]any{"type": "string", "minLength": 1, "maxLength": 300, "description": "What the agent should do, in the user's own words. It becomes the workspace goal."},
			"workspace_name": map[string]any{"type": "string", "maxLength": 80, "description": "A short name for the workspace, a few words."},
		}, "required": []string{"role", "name", "brief", "workspace_name"}, "additionalProperties": false},
	})
}

func (r *hireProposalRegistry) Execute(ctx context.Context, name, arguments string) (string, error) {
	if name != hireProposalTool {
		return r.base.Execute(ctx, name, arguments)
	}
	if err := r.turn.revalidate(ctx); err != nil {
		return "", err
	}
	plan, ok := decodeHireProposal(arguments, r.turn.handler.takenAgentNames())
	if !ok || !r.turn.conversation.turn.ledger.charge(utf8.RuneCountInString(arguments)) {
		return "", errHireProposalInvalid
	}
	r.turn.plan = plan
	return plan.answer(r.turn.lead), nil
}
