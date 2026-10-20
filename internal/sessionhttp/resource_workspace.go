package sessionhttp

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// Resource workspace states on a Home. "available" and "not_created" need a
// usable provider; the provider_* states say what the owner can fix. An
// existing workspace is returned with every state so it can still be opened.
const (
	resourceWorkspaceAvailable            = "available"
	resourceWorkspaceNotCreated           = "not_created"
	resourceWorkspaceProviderMissing      = "provider_missing"
	resourceWorkspaceProviderDisabled     = "provider_disabled"
	resourceWorkspaceProviderIncompatible = "provider_incompatible"
	resourceWorkspaceHomeUnavailable      = "home_unavailable"
)

// respondResourceWorkspaceExists answers a second create of a Home's one
// resource workspace with the one that exists, so the caller opens it.
func respondResourceWorkspaceExists(w http.ResponseWriter, workspaceID string) {
	summary := "This Home already has it."
	_ = orihttp.RespondJSON(w, http.StatusConflict, map[string]any{
		"error": summary, "code": "resource_workspace_exists", "existing_workspace_id": workspaceID,
		"group_requirement": map[string]any{
			"state": grouprequirements.StateResourceWorkspaceExists, "reason": "resource_workspace_exists", "summary": summary,
			"existing_workspace_id": workspaceID,
			"actions":               []grouprequirements.Action{grouprequirements.ActionOpenExistingWorkspace},
		},
	})
}

type resourceAttachmentView struct {
	ProviderPluginID string `json:"provider_plugin_id"`
	BlueprintID      string `json:"blueprint_id"`
	Label            string `json:"label"`
	Description      string `json:"description"`
}

type resourceWorkspaceRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type resourceWorkspaceSummary struct {
	Attachment resourceAttachmentView `json:"attachment"`
	State      string                 `json:"state"`
	// TemplateID is the blueprint the Create Workspace flow opens for setup.
	// It is present only while the provider can create one.
	TemplateID string                `json:"template_id,omitempty"`
	Workspace  *resourceWorkspaceRef `json:"workspace,omitempty"`
}

// GetAssistantResourceWorkspaces handles
// GET /api/workspaces/{workspaceID}/assistant-program/resource-workspaces: the
// Home's resource attachments, whether each can be set up, and the one that
// exists. It reads the installed plugins and the Home's own children only;
// it creates, inspects and calls nothing.
func (h *Handler) GetAssistantResourceWorkspaces(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.workspaceTaskStore == nil || h.currentUserID == nil || h.installedPluginLister == nil {
		_ = orihttp.RespondNotFound(w, "This Home is unavailable")
		return
	}
	owner, err := h.currentUserID(r.Context())
	homeID := strings.TrimSpace(r.PathValue("workspaceID"))
	if err != nil || strings.TrimSpace(owner) == "" || homeID == "" || r.URL.RawQuery != "" {
		_ = orihttp.RespondNotFound(w, "This Home is unavailable")
		return
	}
	home, err := h.workspaceTaskStore.Get(homeID)
	state := homeState(home, err)
	if state == nil || state.Key.Normalize().OwnerUserID != (workspace.AssistantProgramKey{OwnerUserID: owner}).Normalize().OwnerUserID {
		_ = orihttp.RespondNotFound(w, "This Home is unavailable")
		return
	}
	installed, err := h.installedPluginLister.List()
	if err != nil {
		installed = nil
	}
	summaries, err := resourceWorkspaceSummaries(workspace.NewAssistantProgramStore(h.workspaceTaskStore), installed, home, state)
	if err != nil {
		_ = orihttp.RespondInternalError(w, "The Home's workspaces could not be read")
		return
	}
	_ = orihttp.RespondSuccess(w, map[string]any{"resource_workspaces": summaries})
}

func homeState(home *workspace.Workspace, err error) *workspace.AssistantProgramState {
	if err != nil || home == nil || home.Status == workspace.StatusTrashed || home.Status == workspace.StatusMissing {
		return nil
	}
	return home.GetAssistantProgramState()
}

// resourceWorkspaceSummaries projects every resource attachment the Home's
// pinned declaration names. When the pin cannot be resolved (provider
// disabled, removed or not yet upgraded), the Home's existing resource
// workspaces are still listed so they can be opened.
func resourceWorkspaceSummaries(programs *workspace.AssistantProgramStore, installed []plugin.InstalledPlugin, home *workspace.Workspace, state *workspace.AssistantProgramState) ([]resourceWorkspaceSummary, error) {
	summaries := []resourceWorkspaceSummary{}
	_, declaration, pinned := plugin.PinnedHomeDeclaration(installed, state.HomeProvider)
	if !pinned {
		linked, err := programs.LinkedProjects(home.ID)
		if err != nil {
			return nil, err
		}
		for _, child := range linked {
			link := child.GetAssistantProjectLink()
			if !workspace.AssistantProjectLinkIsResource(link) {
				continue
			}
			summaries = append(summaries, resourceWorkspaceSummary{
				Attachment: resourceAttachmentView{ProviderPluginID: link.ProjectProvider.PluginID, BlueprintID: link.ProjectProvider.BlueprintID, Label: child.Name},
				State:      resourceWorkspaceHomeUnavailable, Workspace: resourceWorkspaceReference(child),
			})
		}
		return summaries, nil
	}
	for _, attachment := range declaration.AllowedProjectAttachments {
		if !attachment.IsResource() {
			continue
		}
		summary := resourceWorkspaceSummary{Attachment: resourceAttachmentView{
			ProviderPluginID: attachment.ProviderPluginID, BlueprintID: attachment.BlueprintID,
			Label: attachment.Label, Description: attachment.Description,
		}}
		identity := &workspace.AssistantProjectProviderOwner{
			PluginID: attachment.ProviderPluginID, BlueprintID: attachment.BlueprintID,
			ProjectTeamID: attachment.ProjectTeamID, WorkspaceKind: workspace.AssistantWorkspaceKindResource,
		}
		existing, err := programs.ActiveResourceWorkspace(home.ID, identity)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			summary.Workspace = resourceWorkspaceReference(existing)
		}
		providerState, templateID := resourceProviderState(installed, attachment)
		switch {
		case providerState != "":
			summary.State = providerState
		case existing != nil:
			summary.State = resourceWorkspaceAvailable
		default:
			summary.State, summary.TemplateID = resourceWorkspaceNotCreated, templateID
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

// resourceProviderState says why the attachment's provider cannot create
// one, or returns the creation template when it can. It checks only declared
// identity and kind; the Create Workspace review then performs the full
// reciprocal join.
func resourceProviderState(installed []plugin.InstalledPlugin, attachment projecttemplates.AssistantProgramAllowedProjectAttachment) (string, string) {
	found, enabled := false, false
	for _, candidate := range installed {
		if !strings.EqualFold(candidate.Name, attachment.ProviderPluginID) {
			continue
		}
		found = true
		if !candidate.Enabled {
			continue
		}
		enabled = true
		for _, blueprint := range candidate.ResolvedBlueprints {
			project := blueprint.Template.AssistantProject
			if blueprint.ID == attachment.BlueprintID && project.IsResource() &&
				attachment.Accepts(candidate.Name, blueprint.ID, project.ID, project.SchemaVersion, project.Version, project.WorkspaceKind) {
				return "", blueprint.Template.ID
			}
		}
	}
	switch {
	case !found:
		return resourceWorkspaceProviderMissing, ""
	case !enabled:
		return resourceWorkspaceProviderDisabled, ""
	default:
		return resourceWorkspaceProviderIncompatible, ""
	}
}

func resourceWorkspaceReference(child *workspace.Workspace) *resourceWorkspaceRef {
	ref := &resourceWorkspaceRef{ID: child.ID, Name: child.Name}
	if slug := strings.TrimSpace(child.FolderSlug); slug != "" {
		ref.URL = "/workspaces/" + url.PathEscape(slug)
	}
	return ref
}
