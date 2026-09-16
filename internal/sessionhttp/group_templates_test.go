package sessionhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	agentworkspace "github.com/johnjallday/ori-agent/internal/workspace"
)

type groupTemplateListResponse struct {
	GroupTemplates []struct {
		ID                string                                     `json:"id"`
		Kind              string                                     `json:"kind"`
		Name              string                                     `json:"name"`
		Revision          string                                     `json:"revision"`
		ProposedGroupName string                                     `json:"proposed_group_name"`
		HomeRoles         []projecttemplates.GroupTemplateRole       `json:"home_roles"`
		ProjectRoles      []string                                   `json:"project_roles_note"`
		Provider          *projecttemplates.GroupTemplateProvider    `json:"provider"`
		Availability      *groupTemplateAvailability                 `json:"availability"`
		Home              *groupTemplateHomeView                     `json:"home"`
		RequiredHomeRoles *templateGroupRequirementRequiredRolesPlan `json:"required_home_roles"`
	} `json:"group_templates"`
	CatalogUnavailable bool `json:"catalog_unavailable"`
}

func getGroupTemplatesRaw(t *testing.T, handler *Handler) string {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ListGroupTemplates(response, httptest.NewRequest(http.MethodGet, "/api/workspaces/group-templates", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", response.Code, response.Body.String())
	}
	return response.Body.String()
}

func getGroupTemplates(t *testing.T, handler *Handler) groupTemplateListResponse {
	t.Helper()
	raw := getGroupTemplatesRaw(t, handler)
	var body groupTemplateListResponse
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return body
}

func TestListGroupTemplates_OffersOnlyGeneralWithoutTrustedCatalog(t *testing.T) {
	handler, cleanup := createTestHandler(t)
	defer cleanup()

	body := getGroupTemplates(t, handler)
	if !body.CatalogUnavailable || len(body.GroupTemplates) != 1 || body.GroupTemplates[0].ID != projecttemplates.GroupTemplateGeneralID ||
		body.GroupTemplates[0].Kind != string(projecttemplates.GroupTemplateKindOrdinary) || body.GroupTemplates[0].Availability != nil {
		t.Fatalf("unverified catalog response = %+v", body)
	}
}

func TestListGroupTemplates_ProjectsExactHomeLifecycleWithoutMutation(t *testing.T) {
	template := groupedPlanTemplate(projecttemplates.GroupPolicyRequired)
	template.AssistantProgram.DefaultPrimaryName = "Neutral Coordinator"
	handler, workspaceStore, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	installPlanAgentStore(t, handler)
	active := true
	handler.SetGroupTemplateCatalog(func(ownerUserID string) ([]GroupTemplateCatalogEntry, bool, error) {
		if ownerUserID != "local" {
			t.Fatalf("catalog owner = %q, want server-derived local", ownerUserID)
		}
		return []GroupTemplateCatalogEntry{{Template: template, Readiness: handler.revalidateBlueprintReadiness(template), Active: active}}, false, nil
	})

	absent := getGroupTemplates(t, handler)
	if absent.CatalogUnavailable || len(absent.GroupTemplates) != 2 {
		t.Fatalf("catalog response = %+v", absent)
	}
	entry := absent.GroupTemplates[1]
	if entry.Kind != string(projecttemplates.GroupTemplateKindManaged) || entry.Name != "Neutral Program Home" ||
		entry.ProposedGroupName != "Neutral Program Home" || entry.Provider == nil || entry.Provider.PluginID != "neutral" ||
		len(entry.HomeRoles) != 2 || len(entry.ProjectRoles) != 1 {
		t.Fatalf("managed entry = %+v", entry)
	}
	// The creator proposes names only: the primary role gets the declared
	// default primary name, any other role its label, and no prompt rides along.
	if entry.HomeRoles[0].RoleID != "home-lead" || entry.HomeRoles[0].DefaultName != "Neutral Coordinator" ||
		entry.HomeRoles[1].RoleID != "home-addon" || entry.HomeRoles[1].DefaultName != "Home Add-on" {
		t.Fatalf("Home role default names = %+v", entry.HomeRoles)
	}
	if raw := getGroupTemplatesRaw(t, handler); strings.Contains(raw, "Coordinate the Home.") ||
		strings.Contains(raw, "Organize reviewed records.") || strings.Contains(raw, "system_prompt") {
		t.Fatalf("catalog discloses a Home prompt: %s", raw)
	}
	if entry.Availability == nil || entry.Availability.State != groupTemplateAvailabilityCreatable ||
		entry.Home == nil || entry.Home.State != groupTemplateHomeAbsent || entry.Home.WorkspaceID != "" ||
		entry.RequiredHomeRoles == nil || entry.RequiredHomeRoles.Verification != templateGroupRoleVerificationAbsent {
		t.Fatalf("absent Home projection = availability %+v home %+v roles %+v", entry.Availability, entry.Home, entry.RequiredHomeRoles)
	}
	requirePlanCounts(t, *entry.RequiredHomeRoles, 0, 1)
	if ids, err := workspaceStore.List(); err != nil || len(ids) != 0 {
		t.Fatalf("listing created workspaces: ids=%v err=%v", ids, err)
	}

	homeID := preparePolicyHome(t, handler)
	if err := workspaceStore.Update(homeID, func(home *agentworkspace.Workspace) error {
		home.Name = "Renamed Neutral Home"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reusable := getGroupTemplates(t, handler).GroupTemplates[1]
	if reusable.ID != entry.ID || reusable.Revision != entry.Revision || reusable.ProposedGroupName != entry.ProposedGroupName ||
		reusable.Availability.State != groupTemplateAvailabilityReusable || reusable.Home.State != groupTemplateHomeExists ||
		reusable.Home.WorkspaceID != homeID || reusable.Home.Name != "Renamed Neutral Home" ||
		reusable.RequiredHomeRoles.Verification != templateGroupRoleVerificationVerified {
		t.Fatalf("renamed exact Home projection = %+v / %+v / %+v", reusable.Availability, reusable.Home, reusable.RequiredHomeRoles)
	}
	requirePlanCounts(t, *reusable.RequiredHomeRoles, 0, 1)
	if !slicesContainString(reusable.Availability.Actions, groupTemplateActionOpenGroup) ||
		!slicesContainString(reusable.Availability.Actions, groupTemplateActionOpenRoles) ||
		slicesContainString(reusable.Availability.Actions, groupTemplateActionReviewCreate) {
		t.Fatalf("reusable actions = %v", reusable.Availability.Actions)
	}

	active = false
	inactive := getGroupTemplates(t, handler).GroupTemplates[1]
	if inactive.Availability.State != groupTemplateAvailabilityUnavailable || inactive.Home.WorkspaceID != homeID ||
		!slicesContainString(inactive.Availability.Actions, groupTemplateActionOpenGroup) ||
		slicesContainString(inactive.Availability.Actions, groupTemplateActionReviewCreate) || inactive.ProposedGroupName != "" {
		t.Fatalf("inactive source projection = %+v / %+v", inactive.Availability, inactive.Home)
	}
	if ids, err := workspaceStore.List(); err != nil || len(ids) != 1 || ids[0] != homeID {
		t.Fatalf("listing changed canonical topology: ids=%v err=%v", ids, err)
	}
}

func slicesContainString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
