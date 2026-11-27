package sessionhttp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/johnjallday/ori-agent/internal/diridentity"
	"github.com/johnjallday/ori-agent/internal/filejanitor"
	"github.com/johnjallday/ori-agent/internal/folderagent"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// The ordinary Files look keeps using its reference ID. This namespace selects
// the stricter receipt-bound path on every use, including after a restart.
const documentsLinkPrefix = folderagent.GuidedDocumentsLinkPrefix

func (h *Handler) SetGuidedDocumentsLook(local func(*http.Request, string) bool, witness func(string, time.Time) string) {
	h.guidedLookAllowed, h.documentsWitness = local, witness
}

// SetGuidedFilingRoot resolves Filing's independently approved, current grant.
func (h *Handler) SetGuidedFilingRoot(resolve func(string, string) (filejanitor.ManagedRoot, error)) {
	h.managedFilingRoot = resolve
}

type guidedLook struct {
	op, root, agent, link, witness, verification string
	filingBinding                                string // populated only for the metadata-only Filing conversion
}

// Inspect only the anchor directory and local authority records. No listing,
// profile writes, search service, or ticket is needed to answer the offer.
func (h *Handler) guidedDocumentsLook(ctx context.Context, ws *workspace.Workspace) (guidedLook, bool) {
	return h.checkGuidedDocumentsLook(ctx, ws, folderagent.ListingPinAllowed)
}

// ReleasedDocumentsAdviceReceipt is display-only: the caller gets neither a
// root nor access. The saved creation receipt, lead, binding and physical
// witness must still match. Only the old, unassigned host recipe differs.
func (h *Handler) ReleasedDocumentsAdviceReceipt(ctx context.Context, owner, id, agent, link string) bool {
	if h.workspaceStore == nil || owner == "" || !strings.HasPrefix(link, documentsLinkPrefix) {
		return false
	}
	ws, err := h.workspaceStore.Get(id)
	if err != nil || ws == nil || ws.ID != id || ws.OwnerUserID != owner {
		return false
	}
	look, ok := h.checkGuidedDocumentsLook(ctx, ws, folderagent.ReleasedFirstLookRecipe)
	return ok && look.agent == agent && look.link == link && look.verification == "" && look.filingBinding == ""
}

// GuidedStation verifies the original workspace creation, not a folder report or its
// file count. The result can review a new metadata-only grant; it is never
// accepted by the manager's look, Documents review, or content tools.
func (h *Handler) GuidedStation(owner, id string) (filejanitor.StationLook, bool) {
	var no filejanitor.StationLook
	if h.workspaceStore == nil || owner == "" {
		return no, false
	}
	ws, err := h.workspaceStore.Get(id)
	if err != nil || ws == nil || ws.ID != id || ws.OwnerUserID != owner {
		return no, false
	}
	ref, err := ws.GetDirectoryReference(ws.AnchorDirectoryReferenceID())
	if err != nil || ref == nil {
		return no, false
	}
	info, err := os.Lstat(ref.Path) // #nosec G304 G703 -- stored workspace anchor; metadata only, verified against the original creation below.
	if err != nil || diridentity.Of(info) == "" {
		return no, false
	}
	look, ok := h.checkGuidedDocumentsLook(context.Background(), ws, func(w *workspace.Workspace, lead, binding string) bool {
		return folderagent.ListingPinAllowed(w, lead, binding) || folderagent.ReleasedFirstLookRecipe(w, lead, binding)
	})
	if !ok || look.verification != "" || look.filingBinding != "" || look.root != ref.Path {
		return no, false
	}
	hash := sha256.Sum256([]byte(strings.Join([]string{owner, id, look.op, look.agent, look.link, look.witness}, "\x00")))
	return filejanitor.StationLook{ReferenceID: ref.ID, Manager: look.agent, Witness: diridentity.Of(info), Revision: hex.EncodeToString(hash[:])}, true
}

type guidedPinCheck func(*workspace.Workspace, string, string) bool

func (h *Handler) checkGuidedDocumentsLook(ctx context.Context, ws *workspace.Workspace, checkAnchorPin guidedPinCheck) (guidedLook, bool) {
	var no guidedLook
	if h.documentsWitness == nil || h.store == nil || h.store.DB() == nil || ws == nil || ws.OwnerUserID == "" || !ws.IsAnchored() || ws.SharedData[guidedPurposeKey] != documentsPurpose || (ws.GetStatus() != "" && ws.GetStatus() != workspace.StatusActive) {
		return no, false
	}
	op, _ := ws.SharedData[creationOperationIDKey].(string)
	digest, _ := ws.SharedData[creationOperationDigestKey].(string)
	provenance := h.workspaceTemplateProvenance(ws)
	if !validCreationOperationID(op) || digest == "" || provenance == nil || !provenance.Builtin || provenance.TemplateID != blankWorkspaceTemplateID || h.workspaceIsAssistantProgram(ws) {
		return no, false
	}
	lead, ok := ws.FindUniqueAgentInstance(ws.EntryAgentName(), "")
	if !ok || lead.ID == "" || !lead.EntryPoint || lead.RoleID != blankManagerRoleID || h.agentStore == nil {
		return no, false
	}
	if ag, ok := h.agentStore.GetAgent(lead.Name); !ok || ag == nil {
		return no, false
	}
	ref, err := ws.GetDirectoryReference(ws.AnchorDirectoryReferenceID())
	if err != nil || ref == nil || ref.WorkspaceID != ws.ID || ref.Purpose != "" || ref.ID == "" {
		return no, false
	}
	// The creation journal is machine-local. A portable workspace.json or a
	// client-supplied purpose/path cannot attest the original role, link or grant.
	var raw string
	var saved, filesWitness sql.NullString
	err = h.store.DB().QueryRowContext(ctx, `SELECT op.response_json, op.documents_witness, link.witness
		FROM workspace_creation_operations op LEFT JOIN filename_workspace_links link
		ON link.workspace_id=op.workspace_id AND link.owner=op.owner_user_id AND link.reference_id=?
		WHERE op.id=? AND op.workspace_id=? AND op.owner_user_id=? AND op.request_digest=? AND op.state='complete'
		AND json_extract(op.request_json,'$.blank')=1 AND json_extract(op.request_json,'$.guided_purpose')='documents'`, ref.ID, op, ws.ID, ws.OwnerUserID, digest).Scan(&raw, &saved, &filesWitness)
	var receipt struct {
		Folder struct {
			Agents []workspace.AgentInstance      `json:"agent_instances"`
			Refs   []workspace.DirectoryReference `json:"directory_references"`
			Grants []workspace.MCPBinding         `json:"mcp_bindings"`
		} `json:"folder"`
	}
	if err != nil || json.Unmarshal([]byte(raw), &receipt) != nil {
		return no, false
	}
	original := receipt.Folder
	if !slices.ContainsFunc(original.Agents, func(a workspace.AgentInstance) bool {
		return a.ID == lead.ID && a.EntryPoint && a.RoleID == blankManagerRoleID
	}) || len(original.Refs) != 1 || original.Refs[0].ID != ref.ID || original.Refs[0].Path != ref.Path || len(original.Grants) != 1 {
		return no, false
	}
	grant := original.Grants[0].ID
	filingBinding := ""
	if !guidedListingAllowed(ws, lead.ID, grant, ref.Path, workspace.AnchorFolderMCPAlias, checkAnchorPin) {
		// Filing setup intentionally narrows the *same* binding to listing. A
		// different binding, an alias edit alone, or an unverified old receipt
		// cannot stand in for the independently approved Filing root.
		if !saved.Valid || h.managedFilingRoot == nil || !guidedListingAllowed(ws, lead.ID, grant, ref.Path, filejanitor.LegacyBindingAlias, folderagent.ListingPinAllowed) {
			return no, false
		}
		managed, err := h.managedFilingRoot(ws.OwnerUserID, ws.ID)
		if err != nil || managed.Owner != ws.OwnerUserID || managed.WorkspaceID != ws.ID || managed.Path != ref.Path || managed.DirectoryReferenceID != ref.ID || managed.BindingID != grant || managed.Generation == "" {
			return no, false
		}
		filingBinding = strings.Join([]string{lead.ID, ref.ID, grant}, "\x00")
	}
	at := time.Time{}
	if !saved.Valid {
		at = original.Refs[0].CreatedAt
		if at.IsZero() {
			return no, false
		}
	}
	current := h.documentsWitness(ref.Path, at)
	if current == "" || (saved.Valid && current != saved.String) || (filesWitness.Valid && current != filesWitness.String) {
		return no, false
	}
	result := guidedLook{op: op, root: ref.Path, agent: lead.Name, link: documentsLinkPrefix + ref.ID, witness: current, filingBinding: filingBinding}
	if !saved.Valid {
		hash := sha256.Sum256([]byte(strings.Join([]string{op, ref.ID, lead.ID, original.Grants[0].ID, current}, "\x00")))
		result.verification = hex.EncodeToString(hash[:])
	}
	return result, true
}

// GuidedFilingAccess identifies the original review after Filing narrows its
// grant. Its callers may project saved results or hand them to Filing, never
// read a source, open an original or approve a new review with this proof.
func (h *Handler) GuidedFilingAccess(owner, id, agent, link string) (folderagent.DocumentsAccess, error) {
	if h.workspaceStore != nil && owner != "" && strings.HasPrefix(link, documentsLinkPrefix) {
		ws, err := h.workspaceStore.Get(id)
		if err == nil && ws != nil && ws.ID == id && ws.OwnerUserID == owner {
			look, ok := h.guidedDocumentsLook(context.Background(), ws)
			if ok && look.agent == agent && look.link == link && look.filingBinding != "" {
				return folderagent.DocumentsAccess{Root: look.root, Lead: agent, Binding: look.filingBinding}, nil
			}
		}
	}
	return folderagent.DocumentsAccess{}, folderagent.ErrWrongState
}

func guidedListingAllowed(ws *workspace.Workspace, instance, binding, root, alias string, checkPin guidedPinCheck) bool {
	if binding == "" {
		return false
	}
	grants := ws.GetMCPBindings()
	var found bool
	for _, b := range grants {
		if b.Alias != alias {
			continue
		}
		if found || b.ID != binding || b.ServerName != "filesystem" || !b.Enabled || b.DefaultSideEffect != workspace.SideEffectRead || !slices.Contains(b.AllowedTools, "list_directory") {
			return false
		}
		switch roots := b.Config["roots"].(type) {
		case []any:
			found = len(roots) == 1 && roots[0] == root
		case []string:
			found = len(roots) == 1 && roots[0] == root
		}
		if !found {
			return false
		}
	}
	if access, limited := ws.GetAgentMCPAccess(instance); limited && !slices.Contains(access.EnabledBindingIDs, binding) {
		return false
	}
	return found && checkPin(ws, instance, binding)
}

func (h *Handler) guidedLookDraft(w http.ResponseWriter, r *http.Request, ws *workspace.Workspace) bool {
	if h.guidedLookAllowed == nil || !h.guidedLookAllowed(r, ws.OwnerUserID) || h.folderManager == nil {
		return false
	}
	look, ok := h.guidedDocumentsLook(r.Context(), ws)
	if !ok {
		return false
	}
	identity, ok := h.lookupRoleAgentIdentity(look.agent)
	if !ok {
		return false
	}
	d := map[string]any{"workspace_id": ws.ID, "owner": ws.OwnerUserID, "manager": identity, "brief": folderagent.WorkspaceManagerBrief("documents")}
	d["project_discovery"] = look.verification == "" && look.filingBinding == ""
	if look.verification != "" {
		d["verify_folder"] = look.verification
	} else if rec, e := h.folderManager.Record(ws.OwnerUserID, ws.ID); e == nil {
		d["look_state"] = rec.State
	} else if errors.Is(e, folderagent.ErrNoRecord) {
		d["look_link"] = look.link
		d["station_offer"] = h.folderManager.CanOfferGuidedStation(ws.OwnerUserID, ws.ID)
	} else {
		return false
	}
	orihttp.WriteJSON(w, d)
	return true
}

func (h *Handler) handleVerifyDocumentsFolder(w http.ResponseWriter, r *http.Request, id string) {
	if !orihttp.RequireMethod(w, r, http.MethodPost) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if h.workspaceStore == nil || h.guidedLookAllowed == nil {
		_ = orihttp.RespondForbidden(w, "Folder verification is unavailable here.")
		return
	}
	ws, err := h.workspaceStore.Get(id)
	if err != nil || ws == nil || ws.ID != id || !h.guidedLookAllowed(r, ws.OwnerUserID) {
		_ = orihttp.RespondForbidden(w, "Folder verification is unavailable here.")
		return
	}
	var input struct {
		Verification string `json:"verification"`
	}
	if !decodeBuildBody(w, r, &input) {
		return
	}
	look, ok := h.guidedDocumentsLook(r.Context(), ws)
	if ok && look.verification != "" && look.verification == input.Verification {
		res, err := h.store.DB().ExecContext(r.Context(), `UPDATE workspace_creation_operations SET documents_witness=? WHERE id=? AND owner_user_id=? AND workspace_id=? AND state='complete' AND documents_witness IS NULL`, look.witness, look.op, ws.OwnerUserID, id)
		if err == nil {
			if n, e := res.RowsAffected(); e == nil && n == 1 {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
	}
	_ = orihttp.RespondError(w, http.StatusConflict, "This folder could not be verified. Reopen the workspace to check its current folder.")
}
