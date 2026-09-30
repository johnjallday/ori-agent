package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/homeupgrade"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeHomeUpgrades struct {
	status    *homeupgrade.Operation
	review    homeupgrade.Review
	reviewErr error
	commit    homeupgrade.Operation
	commitErr error
	calls     []string
}

func (f *fakeHomeUpgrades) Status(_ context.Context, owner, pluginID string) (*homeupgrade.Operation, error) {
	f.calls = append(f.calls, "status "+owner+" "+pluginID)
	return f.status, nil
}

func (f *fakeHomeUpgrades) Review(_ context.Context, owner, pluginID string) (homeupgrade.Review, error) {
	f.calls = append(f.calls, "review "+owner+" "+pluginID)
	return f.review, f.reviewErr
}

func (f *fakeHomeUpgrades) Commit(_ context.Context, owner, pluginID, token string) (homeupgrade.Operation, error) {
	f.calls = append(f.calls, "commit "+owner+" "+pluginID+" "+token)
	return f.commit, f.commitErr
}

func providerUpgradeRequest(t *testing.T, handler func(http.ResponseWriter, *http.Request), method, homeID, suffix, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/workspaces/"+homeID+"/assistant-program/provider-upgrade"+suffix, strings.NewReader(body))
	req.SetPathValue("workspaceID", homeID)
	rr := httptest.NewRecorder()
	handler(rr, req)
	return rr
}

func TestAssistantProviderUpgradeHTTP(t *testing.T) {
	handler, store, station, _ := assistantPortfolioHTTPFixture(t)
	handler.currentUserID = func(context.Context) (string, error) { return station.OwnerUserID, nil }
	pin := workspace.AssistantProgramHomeOwner{PluginID: "music-project-management", PluginVersion: "0.1.0", ProgramID: "portfolio-guide",
		HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: strings.Repeat("a", 64), PluginGeneration: 1, ComponentFingerprint: strings.Repeat("b", 64)}
	if err := store.Update(station.ID, func(current *workspace.Workspace) error {
		state := current.GetAssistantProgramState()
		state.HomeProvider = &pin
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Without the service the Home has no upgrade surface at all.
	if rr := providerUpgradeRequest(t, handler.GetAssistantProviderUpgrade, http.MethodGet, station.ID, "", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("unwired status = %d %s", rr.Code, rr.Body.String())
	}

	fake := &fakeHomeUpgrades{
		review: homeupgrade.Review{Token: "review-1", ExpiresAt: time.Now().Add(time.Minute), Plan: homeupgrade.Plan{
			PluginID: pin.PluginID, FromVersion: "0.1.0", ToVersion: "0.1.1",
			Programs: []homeupgrade.PlanProgram{{ProgramID: "portfolio-guide", RolePrompts: []projecttemplates.HomeRolePromptChange{
				{RoleID: "guide", Label: "Guide", Old: "Coordinate.", New: "Coordinate the library."},
			}}},
			Homes: []homeupgrade.PlanHome{{HomeID: station.ID, Name: "Portfolio Home", Projects: []homeupgrade.PlanProject{{ID: "p1", Name: "Song One"}},
				Agents: []homeupgrade.PlanAgent{{RoleID: "guide", RoleLabel: "Guide", AgentName: "Guide", Profile: homeupgrade.AgentReplace, HomeCopy: homeupgrade.AgentReplace}}}},
		}, Trust: plugin.TrustReport{Skills: []string{"music-project-management"}}},
		commit: homeupgrade.Operation{ID: "op-1", Status: homeupgrade.StatusSucceeded, Plan: homeupgrade.Plan{FromVersion: "0.1.0", ToVersion: "0.1.1"}},
	}
	handler.SetHomePackageUpgrades(fake, func() plugin.UpdateSnapshot {
		return plugin.UpdateSnapshot{Updates: []plugin.UpdateAvailability{{Name: pin.PluginID, InstalledVersion: "0.1.0", AvailableVersion: "0.1.1", Available: true}}}
	})

	rr := providerUpgradeRequest(t, handler.GetAssistantProviderUpgrade, http.MethodGet, station.ID, "", "")
	var status providerUpgradeView
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &status) != nil || !status.Available ||
		status.AvailableVersion != "0.1.1" || status.InstalledVersion != "0.1.0" || status.Operation != nil {
		t.Fatalf("status = %d %s", rr.Code, rr.Body.String())
	}
	current, _ := store.Get(station.ID)
	summary, err := handler.buildAssistantProgramSummary(current, nil)
	if err != nil || summary.ProviderUpgrade == nil || !summary.ProviderUpgrade.Available {
		t.Fatalf("Home summary lacks the upgrade: %#v, %v", summary.ProviderUpgrade, err)
	}

	rr = providerUpgradeRequest(t, handler.ReviewAssistantProviderUpgrade, http.MethodPost, station.ID, "/review", "{}")
	var review providerUpgradeReviewView
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &review) != nil || review.Token != "review-1" ||
		len(review.Roles) != 1 || review.Roles[0].New != "Coordinate the library." || len(review.Homes) != 1 ||
		review.Homes[0].Projects[0] != "Song One" || review.Skills[0] != "music-project-management" {
		t.Fatalf("review = %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), station.ID) {
		t.Fatalf("review disclosed workspace IDs: %s", rr.Body.String())
	}
	if rr := providerUpgradeRequest(t, handler.ReviewAssistantProviderUpgrade, http.MethodPost, station.ID, "/review", `{"extra":1}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown review field accepted: %d", rr.Code)
	}

	if rr := providerUpgradeRequest(t, handler.CommitAssistantProviderUpgrade, http.MethodPost, station.ID, "/commit", `{"token":""}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("empty token = %d", rr.Code)
	}
	rr = providerUpgradeRequest(t, handler.CommitAssistantProviderUpgrade, http.MethodPost, station.ID, "/commit", `{"token":"review-1"}`)
	var operation providerUpgradeOperationView
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &operation) != nil || operation.Status != homeupgrade.StatusSucceeded || operation.ToVersion != "0.1.1" {
		t.Fatalf("commit = %d %s", rr.Code, rr.Body.String())
	}
	if last := fake.calls[len(fake.calls)-1]; last != "commit "+station.OwnerUserID+" "+pin.PluginID+" review-1" {
		t.Fatalf("commit reached the service as %q", last)
	}

	// Refusals are coded; a stopped operation is returned with its ID.
	fake.reviewErr = homeupgrade.ErrPinMismatch
	rr = providerUpgradeRequest(t, handler.ReviewAssistantProviderUpgrade, http.MethodPost, station.ID, "/review", "{}")
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"code":"home_upgrade_pin_mismatch"`) {
		t.Fatalf("pin mismatch = %d %s", rr.Code, rr.Body.String())
	}
	fake.commit = homeupgrade.Operation{ID: "op-2", Status: homeupgrade.StatusReconcileRequired, Outcome: homeupgrade.Outcome{Reason: "The plugin replacement stopped partway."}}
	fake.commitErr = errors.Join(homeupgrade.ErrReconcileRequired, errors.New("disk full"))
	rr = providerUpgradeRequest(t, handler.CommitAssistantProviderUpgrade, http.MethodPost, station.ID, "/commit", `{"token":"review-2"}`)
	if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"code":"home_upgrade_reconcile_required"`) ||
		!strings.Contains(rr.Body.String(), `"id":"op-2"`) || strings.Contains(rr.Body.String(), "disk full") {
		t.Fatalf("reconcile = %d %s", rr.Code, rr.Body.String())
	}
	fake.status = &fake.commit
	rr = providerUpgradeRequest(t, handler.GetAssistantProviderUpgrade, http.MethodGet, station.ID, "", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"status":"reconcile_required"`) {
		t.Fatalf("status during reconciliation = %d %s", rr.Code, rr.Body.String())
	}

	// Another owner cannot see or drive this Home's upgrade.
	handler.currentUserID = func(context.Context) (string, error) { return "someone-else", nil }
	if rr := providerUpgradeRequest(t, handler.ReviewAssistantProviderUpgrade, http.MethodPost, station.ID, "/review", "{}"); rr.Code != http.StatusNotFound {
		t.Fatalf("other owner = %d", rr.Code)
	}
}
