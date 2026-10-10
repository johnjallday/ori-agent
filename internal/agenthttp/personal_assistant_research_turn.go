package agenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/assistantdiscovery"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/mcp/mcpregistry"
)

type researchTurnKey struct{}
type assistantResearchTurn struct {
	handler       *HomeAssistantAskHandler
	turn          *assistantWorkspaceTurn
	conversation  *openConversation
	refs          HomeAssistantRouteContext
	budget        *assistantdiscovery.TurnBudget
	proposed      *assistantdiscovery.Lookup
	result        *assistantdiscovery.Result
	approvedScope *assistantdiscovery.Scope
	folder        bool
	folderRef     *HomeAssistantFolderRef
}

func researchTurnFromContext(ctx context.Context) *assistantResearchTurn {
	turn, _ := ctx.Value(researchTurnKey{}).(*assistantResearchTurn)
	return turn
}

type AssistantAuthorizedDiscovery interface {
	ReadAuthorized(context.Context, *assistantdiscovery.Authorization, *assistantdiscovery.TurnBudget) (assistantdiscovery.Result, error)
}

func (r *assistantResearchTurn) revalidate(ctx context.Context) error {
	if err := r.handler.revalidateWorkspaceTurn(ctx, r.turn); err != nil {
		return assistantdiscovery.ErrReviewRefused
	}
	if r.approvedScope != nil {
		return (assistantResearchScopeValidator{r.handler}).ValidateResearchScope(ctx, *r.approvedScope)
	}
	work, user, err := r.handler.researchRelationship(ctx)
	if err != nil || work.StateVersion != r.turn.relationshipVersion || work.HQWorkspaceID != r.turn.hq || work.ConversationAgent != r.turn.profile || user != r.turn.userID {
		return assistantdiscovery.ErrReviewRefused
	}
	if r.conversation.id != "" {
		reader, ok := r.handler.Conversations.(PersonalAssistantConversationOwnerReader)
		if !ok {
			return assistantdiscovery.ErrReviewRefused
		}
		record, err := reader.ReadConversationOwner(ctx, r.conversation.id, r.turn.saveOwner())
		if err != nil || record.ID != r.conversation.id || record.WorkspaceID != r.turn.hq || !strings.EqualFold(record.AgentName, r.turn.profile) {
			return assistantdiscovery.ErrReviewRefused
		}
		revision := researchConversationRevision(record)
		if r.turn.researchRevision != "" && r.turn.researchRevision != revision {
			return assistantdiscovery.ErrReviewRefused
		}
		r.turn.researchRevision = revision
	}
	return nil
}
func (r *assistantResearchTurn) approve(ctx context.Context, review assistantdiscovery.Review) error {
	if r.folder && r.folderRef == nil {
		return assistantdiscovery.ErrReviewRefused
	}
	reader, ok := r.handler.Discovery.(AssistantAuthorizedDiscovery)
	if !ok {
		return assistantdiscovery.ErrLookupUnsupported
	}
	scope, err := r.handler.ResolveResearchScope(ctx, &HomeAssistantConversationRef{ID: r.conversation.id}, &r.refs, r.folderRef)
	if err != nil {
		return err
	}
	approval, err := r.handler.ResearchReviews.Approve(ctx, scope, review)
	if err != nil {
		return err
	}
	r.approvedScope = &scope
	r.turn.researchRevision = scope.ConversationRevision
	result, err := reader.ReadAuthorized(ctx, approval, r.budget)
	if err != nil {
		return err
	}
	r.turn.ledger.recordResearch(&result)
	r.result = &result
	return r.revalidate(ctx)
}
func (r *assistantResearchTurn) finalize(ctx context.Context, response *HomeAssistantAskResponse) {
	if r == nil {
		return
	}
	if r.result != nil {
		response.ResearchResult = r.result
	}
	if r.proposed == nil || response.ModelUnavailable || response.Conversation == nil || !response.Conversation.Stored {
		return
	}
	refs := r.refs
	// The model's accepted named subject is distinct from the page location.
	if r.turn.projection.Subject != nil {
		refs.SubjectWorkspaceID = r.turn.projection.Subject.ID
	}
	var folderRef *HomeAssistantFolderRef
	if r.folderRef != nil {
		copy := *r.folderRef
		copy.FocusIDs = append([]string(nil), copy.FocusIDs...)
		copy.DraftID = ""
		copy.Historical = true
		if response.FolderContext == nil {
			return
		}
		copy.Revision = response.FolderContext.Revision
		folderRef = &copy
	}
	scope, err := r.handler.ResolveResearchScope(ctx, &HomeAssistantConversationRef{ID: response.Conversation.ID}, &refs, folderRef)
	if err != nil {
		return
	}
	review, err := r.handler.ResearchReviews.Prepare(ctx, scope, *r.proposed)
	if err != nil {
		return
	}
	response.ResearchReview = &review
	response.ResearchContext = &refs
	response.ResearchFolderContext = folderRef
}

func configuredPublicSearch(h *HomeAssistantAskHandler) bool {
	reader, ok := h.Discovery.(interface{ PublicSearchConfigured() bool })
	return ok && reader.PublicSearchConfigured()
}

func resolveResearchProposal(ctx context.Context, h *HomeAssistantAskHandler, lookup assistantdiscovery.Lookup) (assistantdiscovery.Lookup, error) {
	if lookup.Operation == "web_search" && lookup.SourceID == "" && lookup.SourceVersion == "" && lookup.URL == "" {
		reader, ok := h.Discovery.(interface {
			ResolvePublicSearchLookup(string) (assistantdiscovery.Lookup, error)
		})
		if !ok {
			return lookup, assistantdiscovery.ErrLookupUnsupported
		}
		var err error
		lookup, err = reader.ResolvePublicSearchLookup(lookup.Query)
		if err != nil {
			return lookup, err
		}
	}
	return lookup, validateResearchProposal(ctx, h, lookup)
}

type assistantResearchRegistry struct {
	base     modelToolRegistry
	research *assistantResearchTurn
}

func (r *assistantResearchRegistry) Definitions() []llm.Tool {
	tools := r.base.Definitions()
	operations := []string{"skills_catalog", "public_document", "mcp_catalog_refresh"}
	if configuredPublicSearch(r.research.handler) {
		operations = append(operations, "web_search")
	}
	query := map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "maxLength": 256}}, "additionalProperties": false}
	tools = append(tools, llm.Tool{Name: "assistant_installed_capabilities", Description: "Read bounded installed Skills-folder and registered MCP metadata. No prompt/scripts, enable/trust/grants or operational verification. Does not search externally.", Parameters: query}, llm.Tool{Name: "assistant_mcp_catalog", Description: "Search compiled curated/enabled-source cached MCP metadata only. Distinguish stale, disabled, unknown and listing-only results. No refresh or MCP start.", Parameters: query}, llm.Tool{Name: "assistant_public_registry_sources", Description: "List configured enabled public registry destinations and version IDs eligible for exact user review. No network or server start.", Parameters: map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}})
	_, folderReader := r.research.handler.Conversations.(PersonalAssistantResearchFolderReader)
	if !r.research.folder || folderReader {
		tools = append(tools, llm.Tool{Name: "assistant_propose_research_lookup", Description: "Propose ONE minimal skills_catalog query, public_document URL, configured mcp_catalog_refresh (source_id/version and exact URL), or web_search query ONLY when the configured operation is listed. Nothing is sent. The host presents a five-minute editable user review only after this discussion turn saves; user approval is required for every exact lookup and follow-up. Never include Profile/HQ/files/transcript or credentials. No install, native tools, broad web-search fallback or setup authority.", Parameters: map[string]any{"type": "object", "properties": map[string]any{"operation": map[string]any{"type": "string", "enum": operations}, "query": map[string]any{"type": "string", "maxLength": 256}, "url": map[string]any{"type": "string", "maxLength": 2000}, "source_id": map[string]any{"type": "string", "maxLength": 128}, "source_version": map[string]any{"type": "string", "maxLength": 64}}, "required": []string{"operation"}, "additionalProperties": false}})
	}
	return tools
}
func (r *assistantResearchRegistry) Execute(ctx context.Context, name, arguments string) (string, error) {
	if err := r.research.revalidate(ctx); err != nil {
		return "", err
	}
	switch name {
	case "assistant_installed_capabilities", "assistant_mcp_catalog", "assistant_public_registry_sources", "assistant_propose_research_lookup":
	default:
		return r.base.Execute(ctx, name, arguments)
	}
	if len(arguments) > 4096 {
		return "", errors.New("bounded research arguments required")
	}
	decoder := json.NewDecoder(strings.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if name == "assistant_propose_research_lookup" {
		var lookup assistantdiscovery.Lookup
		_, folderReader := r.research.handler.Conversations.(PersonalAssistantResearchFolderReader)
		if (r.research.folder && !folderReader) || decoder.Decode(&lookup) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
			return "", errors.New("invalid research proposal")
		}
		// Prepare on a synthetic identifier is forbidden. Validation is data-only
		// here; the actual canonical thread is checked after a successful save.
		var err error
		lookup, err = resolveResearchProposal(ctx, r.research.handler, lookup)
		if err != nil {
			return "", err
		}
		r.research.proposed = &lookup
		return `{"status":"review_required","sent":false,"note":"Only the latest proposed lookup will be offered after this turn saves. No lookup or setup has run."}`, nil
	}
	var args struct {
		Query string `json:"query,omitempty"`
	}
	if decoder.Decode(&args) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) || (name == "assistant_public_registry_sources" && args.Query != "") {
		return "", errors.New("invalid research arguments")
	}
	if args.Query != "" && assistantdiscovery.ValidatePublicQuery(args.Query) != nil {
		return "", errors.New("invalid metadata query")
	}
	started := time.Now()
	if name == "assistant_public_registry_sources" {
		reader, ok := r.research.handler.Discovery.(interface {
			RegistrySources(context.Context) []mcpregistry.PublicSource
		})
		if !ok {
			return `{"status":"unavailable","sources":[]}`, nil
		}
		sources := reader.RegistrySources(ctx)
		truncated := len(sources) > assistantdiscovery.MaxCandidates
		sources = sources[:min(len(sources), assistantdiscovery.MaxCandidates)]
		encoded, _ := json.Marshal(map[string]any{"status": "available", "sources": sources, "truncated": truncated})
		if !r.research.budget.ChargeLocal(assistantdiscovery.Result{Scope: string(encoded)}) {
			return "", assistantdiscovery.ErrResearchBudget
		}
		return string(encoded), r.research.revalidate(ctx)
	}
	var result assistantdiscovery.Result
	if name == "assistant_installed_capabilities" {
		result = r.research.handler.Discovery.Installed(ctx, args.Query)
	} else {
		result = r.research.handler.Discovery.MCPCatalog(ctx, args.Query)
	}
	if err := r.research.revalidate(ctx); err != nil {
		return "", err
	}
	for i := range result.Candidates {
		result.Candidates[i].Receipt.Key = "S999999999"
	}
	if !r.research.budget.ChargeLocal(result) {
		return "", assistantdiscovery.ErrResearchBudget
	}
	r.research.turn.ledger.recordResearch(&result)
	r.research.turn.ledger.observe(string(result.Availability), time.Since(started))
	r.research.result = &result
	encoded, err := json.Marshal(result)
	return string(encoded), err
}
func validateResearchProposal(ctx context.Context, h *HomeAssistantAskHandler, lookup assistantdiscovery.Lookup) error {
	if err := assistantdiscovery.ValidateLookup(lookup); err != nil {
		return err
	}
	if lookup.Operation == "mcp_catalog_refresh" || lookup.Operation == "web_search" {
		return (assistantResearchScopeValidator{h}).ValidateResearchLookup(ctx, lookup)
	}
	return nil
}
