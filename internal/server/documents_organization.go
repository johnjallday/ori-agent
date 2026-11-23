package server

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/johnjallday/ori-agent/internal/folderagent"
	"github.com/johnjallday/ori-agent/internal/llm"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Resolve only a named model on the lead's explicit Claude API route. No
// model discovery, fallback, or CLI/agent tools participate in the review.
func (b *ServerBuilder) documentsReviewModel(id, lead string) (folderagent.ReviewProcessor, folderagent.ReviewChat) {
	var out folderagent.ReviewProcessor
	if b.llmFactory == nil {
		return out, nil
	}
	local, found, err := b.workspaceStore.GetWorkspaceAgent(id, lead)
	if err != nil || !found || local == nil {
		return out, nil
	}
	s := local.Settings
	out.Model = strings.TrimSpace(s.Model)
	if out.Model == "" || !(strings.EqualFold(s.Provider, "anthropic") || strings.EqualFold(s.Provider, "claude")) {
		return out, nil
	}
	p, err := b.llmFactory.GetProvider("claude")
	if err != nil {
		return out, nil
	}
	direct, ok := p.(*llm.ClaudeProvider)
	if !ok || !p.Capabilities().SupportsIsolatedChat {
		return out, nil
	}
	out.Name, out.Pin = direct.DirectTextDestination()
	if out.Pin == "" {
		return out, nil
	}
	return out, direct
}

// Resolve from one current workspace snapshot. A directory reference, a
// matching display name, or the Curator's metadata-only binding isn't a read grant.
func (b *ServerBuilder) documentsOrganizationAccess(owner, id string) (folderagent.DocumentsAccess, error) {
	ws, err := b.workspaceStore.Get(id)
	if err != nil || ws == nil || owner == "" || ws.OwnerUserID != owner {
		return folderagent.DocumentsAccess{}, folderagent.ErrNotOwner
	}
	if ws.GetStatus() == workspace.StatusTrashed || !ws.IsAnchored() {
		return folderagent.DocumentsAccess{}, folderagent.ErrWrongState
	}
	lead, ok := ws.FindUniqueAgentInstance(ws.EntryAgentName(), "")
	if !ok {
		return folderagent.DocumentsAccess{}, folderagent.ErrWrongState
	}
	ref, err := ws.GetDirectoryReference(ws.AnchorDirectoryReferenceID())
	if err != nil || ref == nil || !filepath.IsAbs(ref.Path) {
		return folderagent.DocumentsAccess{}, folderagent.ErrWrongState
	}
	access, restricted := ws.GetAgentMCPAccess(lead.ID)
	for _, grant := range ws.GetMCPBindings() {
		if !grant.Enabled || grant.ID == "" || grant.ServerName != "filesystem" || !grant.ToolAllowed("list_directory") || !(grant.ToolAllowed("read_file") || grant.ToolAllowed("read_text_file")) || (restricted && !slices.Contains(access.EnabledBindingIDs, grant.ID)) {
			continue
		}
		var root string
		switch roots := grant.Config["roots"].(type) {
		case []any:
			if len(roots) == 1 {
				root, _ = roots[0].(string)
			}
		case []string:
			if len(roots) == 1 {
				root = roots[0]
			}
		}
		if root != "" && filepath.Clean(root) == filepath.Clean(ref.Path) {
			return folderagent.DocumentsAccess{Root: ref.Path, Lead: lead.Name, Binding: strings.Join([]string{lead.ID, ref.ID, grant.ID}, "\x00")}, nil
		}
	}
	return folderagent.DocumentsAccess{}, folderagent.ErrWrongState
}
