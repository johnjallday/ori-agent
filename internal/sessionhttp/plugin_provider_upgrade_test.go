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
	"github.com/johnjallday/ori-agent/internal/workspace"
)

type fakeInstalledPlugins []plugin.InstalledPlugin

func (f fakeInstalledPlugins) List() ([]plugin.InstalledPlugin, error) { return f, nil }

func pluginUpgradeRequest(t *testing.T, handler func(http.ResponseWriter, *http.Request), method, name, suffix, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/plugins/"+name+"/home-upgrade"+suffix, strings.NewReader(body))
	req.SetPathValue("name", name)
	rr := httptest.NewRecorder()
	handler(rr, req)
	return rr
}

func TestPluginHomeUpgradeHTTP(t *testing.T) {
	handler, store, station, _ := assistantPortfolioHTTPFixture(t)
	owner := station.OwnerUserID
	handler.currentUserID = func(context.Context) (string, error) { return owner, nil }
	pin := workspace.AssistantProgramHomeOwner{PluginID: "studio-home", PluginVersion: "0.1.0", ProgramID: "portfolio-guide",
		HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: strings.Repeat("a", 64), PluginGeneration: 1, ComponentFingerprint: strings.Repeat("b", 64)}
	if err := store.Update(station.ID, func(current *workspace.Workspace) error {
		state := current.GetAssistantProgramState()
		state.HomeProvider = &pin
		current.SetAssistantProgramState(state)
		current.FolderSlug = "portfolio-home"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	get := func(name string) *httptest.ResponseRecorder {
		return pluginUpgradeRequest(t, handler.GetPluginHomeUpgrade, http.MethodGet, name, "", "")
	}
	review := func(name, body string) *httptest.ResponseRecorder {
		return pluginUpgradeRequest(t, handler.ReviewPluginHomeUpgrade, http.MethodPost, name, "/review", body)
	}

	if rr := get("studio-home"); rr.Code != http.StatusNotFound {
		t.Fatalf("unwired status = %d %s", rr.Code, rr.Body.String())
	}

	fake := &fakeHomeUpgrades{review: homeupgrade.Review{Token: "review-1", ExpiresAt: time.Now().Add(time.Minute), Plan: homeupgrade.Plan{
		PluginID: "studio-home", FromVersion: "0.1.0", ToVersion: "0.2.0",
		Homes: []homeupgrade.PlanHome{
			{HomeID: station.ID, Name: "Portfolio Home"},
			{HomeID: "removed-since", Name: "Removed Home"},
		},
	}}}
	handler.SetHomePackageUpgrades(fake, func() plugin.UpdateSnapshot {
		return plugin.UpdateSnapshot{Updates: []plugin.UpdateAvailability{{Name: "studio-home", InstalledVersion: "0.1.5", AvailableVersion: "0.2.0", Available: true}}}
	})
	// The installed record, not the Home's pin, is the installed version.
	handler.SetInstalledPluginLister(fakeInstalledPlugins{{Name: "studio-home", Version: "0.1.5"}, {Name: "unused", Version: "1.0.0"}})

	// The path is only a lookup key: a package that is not installed, and one
	// no Home of the owner uses, have no upgrade and reach no service.
	for _, name := range []string{"not-installed", "unused"} {
		if rr := get(name); rr.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d %s", name, rr.Code, rr.Body.String())
		}
		if rr := review(name, "{}"); rr.Code != http.StatusNotFound {
			t.Fatalf("%s review = %d %s", name, rr.Code, rr.Body.String())
		}
	}
	if len(fake.calls) != 0 {
		t.Fatalf("refused scopes reached the service: %v", fake.calls)
	}

	rr := get("studio-home")
	var status providerUpgradeView
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &status) != nil {
		t.Fatalf("status = %d %s", rr.Code, rr.Body.String())
	}
	if status.PluginID != "studio-home" || status.InstalledVersion != "0.1.5" || !status.Available || status.AvailableVersion != "0.2.0" ||
		len(status.Homes) != 1 || status.Homes[0] != (providerUpgradeHomeLink{Name: "Portfolio Home", Link: "/workspaces/portfolio-home/assistant"}) ||
		status.Operation != nil || status.LastOperation != nil || status.StatusUnavailable {
		t.Fatalf("status = %s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), station.ID) {
		t.Fatalf("status disclosed the Home's workspace ID: %s", rr.Body.String())
	}
	if fake.calls[0] != "status "+owner+" studio-home" {
		t.Fatalf("status reached the service as %q", fake.calls[0])
	}

	// A finished operation is history, an unfinished one holds the package,
	// and an unreadable status is said to be unavailable.
	decode := func(rr *httptest.ResponseRecorder) error {
		status = providerUpgradeView{}
		return json.Unmarshal(rr.Body.Bytes(), &status)
	}
	fake.status = &homeupgrade.Operation{ID: "op-1", Status: homeupgrade.StatusSucceeded, Plan: homeupgrade.Plan{FromVersion: "0.1.0", ToVersion: "0.1.5"}}
	if rr := get("studio-home"); decode(rr) != nil || status.Operation != nil ||
		status.LastOperation == nil || status.LastOperation.ID != "op-1" || status.LastOperation.Status != homeupgrade.StatusSucceeded {
		t.Fatalf("finished status = %s", rr.Body.String())
	}
	fake.status = &homeupgrade.Operation{ID: "op-2", Status: homeupgrade.StatusReconcileRequired}
	if rr := get("studio-home"); decode(rr) != nil || status.LastOperation != nil ||
		status.Operation == nil || status.Operation.Status != homeupgrade.StatusReconcileRequired {
		t.Fatalf("held status = %s", rr.Body.String())
	}
	fake.status, fake.statusErr = nil, errors.New("database is locked")
	if rr := get("studio-home"); rr.Code != http.StatusOK || decode(rr) != nil ||
		!status.StatusUnavailable || status.Operation != nil || strings.Contains(rr.Body.String(), "locked") {
		t.Fatalf("unreadable status = %d %s", rr.Code, rr.Body.String())
	}
	fake.statusErr = nil

	// The review is the service's plan for every Home, with each of the
	// owner's Homes linked by its page and none by its ID.
	rr = review("studio-home", "{}")
	var reviewed providerUpgradeReviewView
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &reviewed) != nil || reviewed.Token != "review-1" || len(reviewed.Homes) != 2 ||
		reviewed.Homes[0].Link != "/workspaces/portfolio-home/assistant" || reviewed.Homes[1].Name != "Removed Home" || reviewed.Homes[1].Link != "" {
		t.Fatalf("review = %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), station.ID) {
		t.Fatalf("review disclosed the Home's workspace ID: %s", rr.Body.String())
	}
	if last := fake.calls[len(fake.calls)-1]; last != "review "+owner+" studio-home" {
		t.Fatalf("review reached the service as %q", last)
	}
	if rr := review("studio-home", `{"plugin_id":"other"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("review accepted a request field: %d", rr.Code)
	}
	// The Home page's review is the same review, with the same links.
	rr = providerUpgradeRequest(t, handler.ReviewAssistantProviderUpgrade, http.MethodPost, station.ID, "/review", "{}")
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &reviewed) != nil || reviewed.Homes[0].Link != "/workspaces/portfolio-home/assistant" {
		t.Fatalf("Home review = %d %s", rr.Code, rr.Body.String())
	}

	// Another person sees neither the upgrade nor the Home, and reaches no
	// service on its behalf.
	handler.currentUserID = func(context.Context) (string, error) { return "someone-else", nil }
	before := len(fake.calls)
	for _, rr := range []*httptest.ResponseRecorder{get("studio-home"), review("studio-home", "{}")} {
		if rr.Code != http.StatusNotFound || strings.Contains(rr.Body.String(), "Portfolio Home") {
			t.Fatalf("other owner = %d %s", rr.Code, rr.Body.String())
		}
	}
	if len(fake.calls) != before {
		t.Fatalf("another owner reached the service: %v", fake.calls[before:])
	}

	// A Home in the Trash is not listed; it keeps its old release.
	handler.currentUserID = func(context.Context) (string, error) { return owner, nil }
	live, _ := store.Get(station.ID)
	trashed := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Trashed Home"})
	trashed.OwnerUserID, trashed.Status, trashed.FolderSlug = owner, workspace.StatusTrashed, "trashed-home"
	trashed.SetAssistantProgramState(live.GetAssistantProgramState())
	if err := store.Save(trashed); err != nil {
		t.Fatal(err)
	}
	if rr := get("studio-home"); rr.Code != http.StatusOK || decode(rr) != nil || len(status.Homes) != 1 || status.Homes[0].Name != "Portfolio Home" {
		t.Fatalf("status with a trashed Home = %d %s", rr.Code, rr.Body.String())
	}
}

// A client that lost a commit's response asks what became of its review, by
// the token in the body, from either page; the answer is that review's own
// operation or the review's state, never another operation.
func TestPluginHomeUpgradeOutcomeHTTP(t *testing.T) {
	handler, store, station, _ := assistantPortfolioHTTPFixture(t)
	owner := station.OwnerUserID
	handler.currentUserID = func(context.Context) (string, error) { return owner, nil }
	pin := workspace.AssistantProgramHomeOwner{PluginID: "studio-home", PluginVersion: "0.1.0", ProgramID: "portfolio-guide",
		HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: strings.Repeat("a", 64), PluginGeneration: 1, ComponentFingerprint: strings.Repeat("b", 64)}
	if err := store.Update(station.ID, func(current *workspace.Workspace) error {
		state := current.GetAssistantProgramState()
		state.HomeProvider = &pin
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeHomeUpgrades{}
	handler.SetHomePackageUpgrades(fake, nil)
	handler.SetInstalledPluginLister(fakeInstalledPlugins{{Name: "studio-home", Version: "0.1.0"}})
	fromPlugins := func(body string) *httptest.ResponseRecorder {
		return pluginUpgradeRequest(t, handler.OutcomePluginHomeUpgrade, http.MethodPost, "studio-home", "/outcome", body)
	}
	fromHome := func(body string) *httptest.ResponseRecorder {
		return providerUpgradeRequest(t, handler.OutcomeAssistantProviderUpgrade, http.MethodPost, station.ID, "/outcome", body)
	}

	for _, body := range []string{`{}`, `{"token":" "}`, `{"token":"review-1","operation_id":"op-1"}`} {
		if rr := fromPlugins(body); rr.Code != http.StatusBadRequest {
			t.Fatalf("outcome %s = %d %s", body, rr.Code, rr.Body.String())
		}
	}
	if len(fake.calls) != 0 {
		t.Fatalf("malformed outcome requests reached the service: %v", fake.calls)
	}

	fake.attempt = homeupgrade.Attempt{Review: homeupgrade.ReviewPending}
	for name, request := range map[string]func(string) *httptest.ResponseRecorder{"plugins": fromPlugins, "home": fromHome} {
		rr := request(`{"token":"review-1"}`)
		var view providerUpgradeAttemptView
		if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &view) != nil || view.Review != "pending" || view.Operation != nil {
			t.Fatalf("%s pending outcome = %d %s", name, rr.Code, rr.Body.String())
		}
		if last := fake.calls[len(fake.calls)-1]; last != "attempt "+owner+" studio-home review-1" {
			t.Fatalf("%s outcome reached the service as %q", name, last)
		}
	}

	fake.attempt = homeupgrade.Attempt{Operation: &homeupgrade.Operation{ID: "op-1", Status: homeupgrade.StatusSucceeded,
		Plan: homeupgrade.Plan{FromVersion: "0.1.0", ToVersion: "0.2.0"}}}
	rr := fromPlugins(`{"token":"review-1"}`)
	var view providerUpgradeAttemptView
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &view) != nil || view.Review != "" ||
		view.Operation == nil || view.Operation.ID != "op-1" || view.Operation.Status != homeupgrade.StatusSucceeded || view.Operation.ToVersion != "0.2.0" {
		t.Fatalf("finished outcome = %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "review-1") {
		t.Fatalf("outcome echoed the review token: %s", rr.Body.String())
	}

	// An unreadable outcome is said to be unavailable, never success.
	fake.attempt, fake.attemptErr = homeupgrade.Attempt{}, errors.Join(homeupgrade.ErrUnavailable, errors.New("database is locked"))
	if rr := fromPlugins(`{"token":"review-1"}`); rr.Code != http.StatusServiceUnavailable ||
		!strings.Contains(rr.Body.String(), `"code":"home_upgrade_unavailable"`) || strings.Contains(rr.Body.String(), "locked") {
		t.Fatalf("unreadable outcome = %d %s", rr.Code, rr.Body.String())
	}

	handler.currentUserID = func(context.Context) (string, error) { return "someone-else", nil }
	before := len(fake.calls)
	if rr := fromPlugins(`{"token":"review-1"}`); rr.Code != http.StatusNotFound {
		t.Fatalf("other owner outcome = %d %s", rr.Code, rr.Body.String())
	}
	if rr := fromHome(`{"token":"review-1"}`); rr.Code != http.StatusNotFound {
		t.Fatalf("other owner Home outcome = %d %s", rr.Code, rr.Body.String())
	}
	if len(fake.calls) != before {
		t.Fatalf("another owner reached the service: %v", fake.calls[before:])
	}
}

func TestPluginHomeUpgradeCommitHTTP(t *testing.T) {
	handler, store, station, _ := assistantPortfolioHTTPFixture(t)
	owner := station.OwnerUserID
	handler.currentUserID = func(context.Context) (string, error) { return owner, nil }
	pin := workspace.AssistantProgramHomeOwner{PluginID: "studio-home", PluginVersion: "0.1.0", ProgramID: "portfolio-guide",
		HomeSchemaVersion: 1, HomeVersion: 1, DeclarationDigest: strings.Repeat("a", 64), PluginGeneration: 1, ComponentFingerprint: strings.Repeat("b", 64)}
	if err := store.Update(station.ID, func(current *workspace.Workspace) error {
		state := current.GetAssistantProgramState()
		state.HomeProvider = &pin
		current.SetAssistantProgramState(state)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeHomeUpgrades{commit: homeupgrade.Operation{ID: "op-1", Status: homeupgrade.StatusSucceeded,
		Plan: homeupgrade.Plan{FromVersion: "0.1.0", ToVersion: "0.2.0"}}}
	handler.SetHomePackageUpgrades(fake, nil)
	handler.SetInstalledPluginLister(fakeInstalledPlugins{{Name: "studio-home", Version: "0.1.0"}})
	commit := func(name, body string) *httptest.ResponseRecorder {
		return pluginUpgradeRequest(t, handler.CommitPluginHomeUpgrade, http.MethodPost, name, "/commit", body)
	}

	// A commit names exactly one review by its token, and nothing else.
	for _, body := range []string{`{}`, `{"token":"  "}`, `{"token":"review-1","plugin_id":"other"}`, `not json`} {
		if rr := commit("studio-home", body); rr.Code != http.StatusBadRequest {
			t.Fatalf("commit %s = %d %s", body, rr.Code, rr.Body.String())
		}
	}
	if len(fake.calls) != 0 {
		t.Fatalf("malformed commits reached the service: %v", fake.calls)
	}

	rr := commit("studio-home", `{"token":"review-1"}`)
	var operation providerUpgradeOperationView
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &operation) != nil ||
		operation.ID != "op-1" || operation.Status != homeupgrade.StatusSucceeded || operation.ToVersion != "0.2.0" {
		t.Fatalf("commit = %d %s", rr.Code, rr.Body.String())
	}
	if last := fake.calls[len(fake.calls)-1]; last != "commit "+owner+" studio-home review-1" {
		t.Fatalf("commit reached the service as %q", last)
	}

	// The service's refusals keep their codes; a stopped operation is named.
	for _, refusal := range []struct {
		err       error
		operation homeupgrade.Operation
		code      string
	}{
		{homeupgrade.ErrReviewStale, homeupgrade.Operation{}, "home_upgrade_review_stale"},
		{homeupgrade.ErrCurrent, homeupgrade.Operation{}, "home_upgrade_current"},
		{homeupgrade.ErrOtherOwner, homeupgrade.Operation{}, "home_upgrade_other_owner"},
		{homeupgrade.ErrUpgradeActive, homeupgrade.Operation{}, "home_upgrade_active"},
		{errors.Join(homeupgrade.ErrUnavailable, errors.New("replace failed")),
			homeupgrade.Operation{ID: "op-2", Status: homeupgrade.StatusCancelled}, "home_upgrade_cancelled"},
		{errors.Join(homeupgrade.ErrReconcileRequired, errors.New("disk full")),
			homeupgrade.Operation{ID: "op-3", Status: homeupgrade.StatusReconcileRequired}, "home_upgrade_reconcile_required"},
	} {
		fake.commit, fake.commitErr = refusal.operation, refusal.err
		rr := commit("studio-home", `{"token":"review-2"}`)
		if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"code":"`+refusal.code+`"`) ||
			strings.Contains(rr.Body.String(), "disk full") || strings.Contains(rr.Body.String(), "replace failed") {
			t.Fatalf("%s = %d %s", refusal.code, rr.Code, rr.Body.String())
		}
		if refusal.operation.ID != "" && !strings.Contains(rr.Body.String(), `"id":"`+refusal.operation.ID+`"`) {
			t.Fatalf("%s did not name its operation: %s", refusal.code, rr.Body.String())
		}
	}

	// Another person cannot commit this owner's review, whatever token they hold.
	handler.currentUserID = func(context.Context) (string, error) { return "someone-else", nil }
	before := len(fake.calls)
	if rr := commit("studio-home", `{"token":"review-1"}`); rr.Code != http.StatusNotFound {
		t.Fatalf("other owner commit = %d %s", rr.Code, rr.Body.String())
	}
	if len(fake.calls) != before {
		t.Fatalf("another owner reached the service: %v", fake.calls[before:])
	}
}
