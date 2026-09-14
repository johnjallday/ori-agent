package sessionhttp

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/johnjallday/ori-agent/internal/blueprintreadiness"
	orihttp "github.com/johnjallday/ori-agent/internal/http"
	"github.com/johnjallday/ori-agent/internal/logger"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// GroupTemplateCatalogEntry is one trusted catalog source supplied by the
// server. Active carries the lifecycle decision (enabled plugin contribution,
// ready variant source) so it is never reconstructed from display fields.
type GroupTemplateCatalogEntry struct {
	Template  projecttemplates.Template
	Readiness blueprintreadiness.Readiness
	Active    bool
}

// GroupTemplateCatalog lists the current owner's effective blueprint catalog.
// dependencyStateUnavailable reports that installed-plugin state could not be
// read, so plugin-backed entries may be missing rather than absent.
type GroupTemplateCatalog func(ownerUserID string) (entries []GroupTemplateCatalogEntry, dependencyStateUnavailable bool, err error)

// SetGroupTemplateCatalog wires the read-only catalog lister.
func (h *Handler) SetGroupTemplateCatalog(catalog GroupTemplateCatalog) {
	h.groupTemplateCatalog = catalog
}

const (
	groupTemplateAvailabilityCreatable           = "creatable"
	groupTemplateAvailabilityReusable            = "reusable"
	groupTemplateAvailabilityExistingOnlyMissing = "existing_only_missing"
	groupTemplateAvailabilityUnavailable         = "unavailable"
	groupTemplateAvailabilityConflict            = "conflict"
	groupTemplateAvailabilityAmbiguous           = "ambiguous"
	groupTemplateAvailabilityIncompatible        = "incompatible"

	groupTemplateHomeAbsent       = "absent"
	groupTemplateHomeExists       = "exists"
	groupTemplateHomeAmbiguous    = "ambiguous"
	groupTemplateHomeIncompatible = "incompatible"
	groupTemplateHomeUnreadable   = "unreadable"

	groupTemplateActionReviewCreate = "review_create_group"
	groupTemplateActionOpenGroup    = "open_group"
	groupTemplateActionOpenRoles    = "open_group_roles"
	groupTemplateActionManage       = "manage_plugins"
	groupTemplateActionRetry        = "retry"
)

type groupTemplateAvailability struct {
	State   string   `json:"state"`
	Reason  string   `json:"reason,omitempty"`
	Actions []string `json:"actions,omitempty"`
}

type groupTemplateHomeView struct {
	State       string `json:"state"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Name        string `json:"name,omitempty"`
}

type groupTemplateView struct {
	projecttemplates.GroupTemplate
	Availability      *groupTemplateAvailability                 `json:"availability,omitempty"`
	Home              *groupTemplateHomeView                     `json:"home,omitempty"`
	RequiredHomeRoles *templateGroupRequirementRequiredRolesPlan `json:"required_home_roles,omitempty"`

	programKey *workspace.AssistantProgramKey
}

type groupTemplateListing struct {
	Templates                  []groupTemplateView
	CatalogUnavailable         bool
	DependencyStateUnavailable bool
}

// ListGroupTemplates handles GET /api/workspaces/group-templates. Listing is
// inert: it resolves canonical Home state and verified staffing by reads only.
// General stays available when managed sources cannot be verified, but no
// managed entry is ever fabricated without the trusted catalog and owner.
func (h *Handler) ListGroupTemplates(w http.ResponseWriter, r *http.Request) {
	listing := h.groupTemplateListing(r.Context())
	_ = orihttp.RespondSuccess(w, map[string]any{
		"group_templates":              listing.Templates,
		"catalog_unavailable":          listing.CatalogUnavailable,
		"dependency_state_unavailable": listing.DependencyStateUnavailable,
	})
}

func (h *Handler) groupTemplateListing(ctx context.Context) groupTemplateListing {
	general := groupTemplateListing{
		Templates:          []groupTemplateView{{GroupTemplate: projecttemplates.GeneralGroupTemplate()}},
		CatalogUnavailable: true,
	}
	if h == nil || h.groupTemplateCatalog == nil || h.currentUserID == nil || h.workspaceTaskStore == nil {
		return general
	}
	ownerUserID, err := h.currentUserID(ctx)
	ownerUserID = strings.TrimSpace(ownerUserID)
	if err != nil || ownerUserID == "" {
		return general
	}
	entries, dependencyUnavailable, err := h.groupTemplateCatalog(ownerUserID)
	if err != nil {
		logger.Warn("Group templates could not read the blueprint catalog", logger.Fields{"error": err.Error()})
		return general
	}

	candidates := make([]projecttemplates.GroupTemplateCandidate, 0, len(entries))
	for _, entry := range entries {
		candidate := projecttemplates.GroupTemplateCandidate{
			Template: entry.Template,
			Usable:   entry.Active && !blueprintCreationBlocked(entry.Template, entry.Readiness),
		}
		if !candidate.Usable {
			candidate.UnavailableReason = string(entry.Readiness.Reason)
			if candidate.UnavailableReason == "" {
				candidate.UnavailableReason = "source_inactive"
			}
		}
		candidates = append(candidates, candidate)
	}

	projected := projecttemplates.ProjectGroupTemplates(candidates)
	listing := groupTemplateListing{
		Templates:                  make([]groupTemplateView, 0, len(projected)),
		DependencyStateUnavailable: dependencyUnavailable,
	}
	for _, template := range projected {
		listing.Templates = append(listing.Templates, h.groupTemplateView(ownerUserID, template))
	}
	return listing
}

func (h *Handler) groupTemplateView(ownerUserID string, template projecttemplates.GroupTemplate) groupTemplateView {
	view := groupTemplateView{GroupTemplate: template}
	if template.Kind != projecttemplates.GroupTemplateKindManaged {
		return view
	}
	if template.SourceState == projecttemplates.GroupTemplateSourceConflict || template.Representative == nil {
		view.Availability = &groupTemplateAvailability{
			State: groupTemplateAvailabilityConflict, Reason: template.SourceReason,
			Actions: []string{groupTemplateActionRetry, groupTemplateActionManage},
		}
		return view
	}

	representative := *template.Representative
	key, ok := templateAssistantProgramKey(ownerUserID, representative)
	if !ok {
		view.Availability = &groupTemplateAvailability{State: groupTemplateAvailabilityUnavailable, Reason: "owner_unavailable", Actions: []string{groupTemplateActionRetry}}
		return view
	}
	view.programKey = &key
	home, homeState := h.resolveGroupTemplateHome(key, representative.AssistantProgram)
	view.Home = &groupTemplateHomeView{State: homeState}

	sourceReady := template.SourceState == projecttemplates.GroupTemplateSourceReady
	switch homeState {
	case groupTemplateHomeExists:
		view.Home.WorkspaceID, view.Home.Name = home.ID, home.Name
		roles := h.verifiedRequiredHomeRoles(representative, &key, home)
		view.RequiredHomeRoles = &roles
		if !sourceReady {
			view.Availability = unavailableGroupTemplate(template.SourceReason)
			view.Availability.Actions = append(view.Availability.Actions, groupTemplateActionOpenGroup)
			return view
		}
		view.Availability = &groupTemplateAvailability{State: groupTemplateAvailabilityReusable, Actions: []string{groupTemplateActionOpenGroup}}
		if roles.Verification == templateGroupRoleVerificationVerified && roles.Missing != nil && *roles.Missing > 0 {
			view.Availability.Actions = append(view.Availability.Actions, groupTemplateActionOpenRoles)
		}
	case groupTemplateHomeAbsent:
		roles := absentRequiredHomeRoles(representative)
		view.RequiredHomeRoles = &roles
		switch {
		case !sourceReady:
			view.Availability = unavailableGroupTemplate(template.SourceReason)
		case template.MissingHome == projecttemplates.MissingHomeExistingOnly:
			view.Availability = &groupTemplateAvailability{State: groupTemplateAvailabilityExistingOnlyMissing, Reason: "existing_home_required"}
		default:
			view.Availability = &groupTemplateAvailability{State: groupTemplateAvailabilityCreatable, Actions: []string{groupTemplateActionReviewCreate}}
		}
	case groupTemplateHomeAmbiguous:
		view.Availability = &groupTemplateAvailability{State: groupTemplateAvailabilityAmbiguous, Reason: "home_ambiguous", Actions: []string{groupTemplateActionRetry}}
	case groupTemplateHomeIncompatible:
		view.Home.WorkspaceID, view.Home.Name = home.ID, home.Name
		view.Availability = &groupTemplateAvailability{State: groupTemplateAvailabilityIncompatible, Reason: "home_incompatible"}
		if home.Status != workspace.StatusTrashed && home.Status != workspace.StatusMissing {
			view.Availability.Actions = []string{groupTemplateActionOpenGroup}
		}
	default:
		view.Availability = &groupTemplateAvailability{State: groupTemplateAvailabilityUnavailable, Reason: "home_unreadable", Actions: []string{groupTemplateActionRetry}}
	}
	if view.RequiredHomeRoles == nil {
		roles := unavailableRequiredHomeRoles()
		view.RequiredHomeRoles = &roles
	}
	return view
}

func unavailableGroupTemplate(reason string) *groupTemplateAvailability {
	availability := &groupTemplateAvailability{State: groupTemplateAvailabilityUnavailable, Reason: reason}
	switch blueprintreadiness.Reason(reason) {
	case blueprintreadiness.ReasonDependencyStateUnknown:
		availability.Actions = []string{groupTemplateActionRetry}
	case blueprintreadiness.ReasonPluginInstallRequired, blueprintreadiness.ReasonPluginEnableRequired,
		blueprintreadiness.ReasonPluginUpdateRequired, blueprintreadiness.ReasonPlatformUnsupported,
		blueprintreadiness.ReasonProtocolIncompatible, "source_inactive":
		availability.Actions = []string{groupTemplateActionManage}
	}
	return availability
}

// resolveGroupTemplateHome resolves the one canonical Home for a key by stable
// persisted identity only; names, slugs, parents, and agents never participate.
func (h *Handler) resolveGroupTemplateHome(key workspace.AssistantProgramKey, declaration *workspace.AssistantProgramDeclaration) (*workspace.Workspace, string) {
	home, err := workspace.NewAssistantProgramStore(h.workspaceTaskStore).FindStation(key)
	switch {
	case errors.Is(err, workspace.ErrAssistantStationNotFound):
		return nil, groupTemplateHomeAbsent
	case errors.Is(err, workspace.ErrAssistantStationAmbiguous):
		return nil, groupTemplateHomeAmbiguous
	case err != nil || home == nil:
		return nil, groupTemplateHomeUnreadable
	}
	state := home.GetAssistantProgramState()
	if home.Kind != "group" || home.Status == workspace.StatusTrashed || home.Status == workspace.StatusMissing ||
		declaration == nil || state == nil || state.SchemaVersion != workspace.AssistantProgramStateSchemaVersion ||
		state.Key.Normalize() != key.Normalize() || state.Declaration == nil ||
		state.Declaration.SchemaVersion != declaration.SchemaVersion || state.Declaration.ID != declaration.ID {
		return home, groupTemplateHomeIncompatible
	}
	return home, groupTemplateHomeExists
}
