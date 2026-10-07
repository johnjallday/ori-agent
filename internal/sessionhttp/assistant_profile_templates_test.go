package sessionhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/folderdigest"
	"github.com/johnjallday/ori-agent/internal/homeprofile"
	"github.com/johnjallday/ori-agent/internal/plugin"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// templatesHTTPFixture is a Home whose project plugin declares a facts
// operation; calls records each input the host sent it.
func templatesHTTPFixture(t *testing.T) (*homeProfileHTTP, *[]string) {
	t.Helper()
	f := homeProfileHTTPFixture(t, true)
	f.installed[1].WorkspaceSurfaces = &plugin.SurfaceContribution{
		HomeProfileFacts: &plugin.HomeProfileFactsRef{ServiceID: "reaper-service", Operation: "profile.read"},
	}
	var calls []string
	f.handler.SetHomeProfileFacts(func(_ context.Context, installed plugin.InstalledPlugin, input json.RawMessage) (json.RawMessage, error) {
		calls = append(calls, installed.Name+" "+string(input))
		if strings.Contains(string(input), "true") {
			return json.RawMessage(`{"app":"REAPER","installed":true,"version":"7.28","templates_available":true,"truncated":false,"templates":[
				{"name":"Band Session","kind":"project","file":"Band Session.RPP"},
				{"name":"Vocal Comp","kind":"project","file":"Vocal Comp.RPP"},
				{"name":"Drum Bus","kind":"track","file":"Drum Bus.RTrackTemplate"}]}`), nil
		}
		return json.RawMessage(`{"app":"REAPER","installed":true,"version":"7.28","templates_available":true,"truncated":false}`), nil
	})
	f.apps = []folderdigest.InstalledApp{{ToolID: "reaper", Name: "REAPER"}, {ToolID: "logic-pro", Name: "Logic Pro"}}
	return f, &calls
}

func TestAssistantProfileTemplatesHTTPReviewCommitForget(t *testing.T) {
	f, calls := templatesHTTPFixture(t)
	h := f.handler

	// Before anything is looked for there is nothing to read templates for.
	view := f.view(h.GetAssistantProfile, http.MethodGet, "")
	if view.Templates == nil || view.Templates.State != homeprofile.TemplatesDetectFirst || !view.FactsOperation || len(*calls) != 0 {
		t.Fatalf("first read: templates %+v facts %v calls %v", view.Templates, view.FactsOperation, *calls)
	}
	view = f.view(h.DetectAssistantProfile, http.MethodPost, `{"request_id":"detect-1"}`)
	if app, _ := view.Profile.App("reaper"); app.Version != "7.28" {
		t.Fatalf("version after detect = %+v", app)
	}
	if len(*calls) != 1 || (*calls)[0] != `reaper-plugin {"include_templates":false}` {
		t.Fatalf("detect calls = %v", *calls)
	}
	if view.Templates.State != homeprofile.TemplatesNotRead || view.Templates.AppName != "REAPER" ||
		strings.Join(view.Templates.Folders, ",") != "ProjectTemplates,TrackTemplates" {
		t.Fatalf("templates row = %+v", view.Templates)
	}

	status, review := f.call(h.ReviewAssistantProfileTemplates, http.MethodPost, f.home.ID, `{"request_id":"review-1"}`)
	if status != http.StatusOK || review["app_name"] != "REAPER" || review["review_id"] == "" ||
		!strings.Contains(review["sentence"].(string), "ProjectTemplates and TrackTemplates") {
		t.Fatalf("review = %d %v", status, review)
	}
	if len(*calls) != 1 {
		t.Fatalf("a review called the plugin: %v", *calls)
	}

	view = f.view(h.CommitAssistantProfileTemplates, http.MethodPost,
		`{"request_id":"commit-1","review_id":"`+review["review_id"].(string)+`"}`)
	templates := view.Profile.Templates
	if view.Templates.State != homeprofile.TemplatesListed || len(templates.Items) != 3 ||
		templates.Consent.Source != workspace.HomeProfileTemplatesHomeReview || templates.AppID != "reaper" {
		t.Fatalf("after commit: row %+v templates %+v", view.Templates, templates)
	}
	if len(*calls) != 2 || (*calls)[1] != `reaper-plugin {"include_templates":true}` {
		t.Fatalf("commit calls = %v", *calls)
	}
	// The stored list is inside the Home's own state.
	stored, _ := f.store.Get(f.home.ID)
	if profile := stored.GetAssistantProgramState().GetHomeProfile(); profile == nil || len(profile.Templates.Items) != 3 {
		t.Fatalf("stored templates = %+v", profile)
	}

	view = f.view(h.ForgetAssistantProfileTemplates, http.MethodPost, `{"request_id":"forget-1"}`)
	if view.Templates.State != homeprofile.TemplatesNotRead || len(view.Profile.Templates.Items) != 0 || view.Profile.Templates.Consent.Active() {
		t.Fatalf("after forget: row %+v templates %+v", view.Templates, view.Profile.Templates)
	}
	// The old review does not survive Forget.
	status, body := f.call(h.CommitAssistantProfileTemplates, http.MethodPost, f.home.ID,
		`{"request_id":"commit-2","review_id":"`+review["review_id"].(string)+`"}`)
	if status != http.StatusConflict || body["code"] != "home_profile_changed" || len(*calls) != 2 {
		t.Fatalf("stale review = %d %v calls %v", status, body, *calls)
	}
}

func TestAssistantProfileTemplatesHTTPOperationUnavailable(t *testing.T) {
	f, calls := templatesHTTPFixture(t)
	h := f.handler
	f.view(h.DetectAssistantProfile, http.MethodPost, `{"request_id":"detect-1"}`)

	// The installed project plugin is a release without the operation.
	f.installed[1].WorkspaceSurfaces = &plugin.SurfaceContribution{}
	*calls = nil
	view := f.view(h.GetAssistantProfile, http.MethodGet, "")
	if view.Templates.State != homeprofile.TemplatesUpdatePlugin || view.FactsOperation {
		t.Fatalf("row = %+v facts %v", view.Templates, view.FactsOperation)
	}
	for name, route := range map[string]struct {
		handler func(http.ResponseWriter, *http.Request)
		body    string
	}{
		"review": {h.ReviewAssistantProfileTemplates, `{"request_id":"review-1"}`},
		"commit": {h.CommitAssistantProfileTemplates, `{"request_id":"commit-1","review_id":"anything"}`},
	} {
		status, body := f.call(route.handler, http.MethodPost, f.home.ID, route.body)
		if status != http.StatusConflict || body["code"] != "plugin_operation_unavailable" {
			t.Errorf("%s = %d %v, want 409 plugin_operation_unavailable", name, status, body)
		}
	}
	// The plugin is not installed at all.
	f.installed = f.installed[:1]
	if view = f.view(h.GetAssistantProfile, http.MethodGet, ""); view.Templates.State != homeprofile.TemplatesPluginMissing {
		t.Fatalf("row without the plugin = %+v", view.Templates)
	}
	if len(*calls) != 0 {
		t.Fatalf("an unavailable operation was called: %v", *calls)
	}
}

func TestAssistantProfileTemplatesHTTPAPluginThatFailsIsRecordedNotRaised(t *testing.T) {
	f, _ := templatesHTTPFixture(t)
	h := f.handler
	f.view(h.DetectAssistantProfile, http.MethodPost, `{"request_id":"detect-1"}`)
	_, review := f.call(h.ReviewAssistantProfileTemplates, http.MethodPost, f.home.ID, `{"request_id":"review-1"}`)
	h.SetHomeProfileFacts(func(context.Context, plugin.InstalledPlugin, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("exit status 1")
	})
	view := f.view(h.CommitAssistantProfileTemplates, http.MethodPost,
		`{"request_id":"commit-1","review_id":"`+review["review_id"].(string)+`"}`)
	if view.Templates.State != homeprofile.TemplatesProblem || view.Templates.Problem != workspace.HomeProfileTemplatesFailed ||
		!view.Profile.Templates.Consent.Active() {
		t.Fatalf("after a failed read: row %+v templates %+v", view.Templates, view.Profile.Templates)
	}
	// The artifact disappeared between the card and the click.
	h.SetHomeProfileFacts(func(context.Context, plugin.InstalledPlugin, json.RawMessage) (json.RawMessage, error) {
		return nil, plugin.ErrHomeProfileFactsUnavailable
	})
	_, review = f.call(h.ReviewAssistantProfileTemplates, http.MethodPost, f.home.ID, `{"request_id":"review-2"}`)
	status, body := f.call(h.CommitAssistantProfileTemplates, http.MethodPost, f.home.ID,
		`{"request_id":"commit-2","review_id":"`+review["review_id"].(string)+`"}`)
	if status != http.StatusConflict || body["code"] != "plugin_operation_unavailable" {
		t.Fatalf("unavailable at call time = %d %v", status, body)
	}
}

func TestAssistantProfileTemplatesHTTPStrictAndOwnerOnly(t *testing.T) {
	f, calls := templatesHTTPFixture(t)
	h := f.handler
	f.view(h.DetectAssistantProfile, http.MethodPost, `{"request_id":"detect-1"}`)
	*calls = nil
	routes := map[string]struct {
		handler func(http.ResponseWriter, *http.Request)
		body    string
	}{
		"review": {h.ReviewAssistantProfileTemplates, `{"request_id":"r"}`},
		"commit": {h.CommitAssistantProfileTemplates, `{"request_id":"c","review_id":"x"}`},
		"forget": {h.ForgetAssistantProfileTemplates, `{"request_id":"f"}`},
	}
	for name, route := range routes {
		for _, id := range []string{f.project.ID, "missing"} {
			if status, body := f.call(route.handler, http.MethodPost, id, route.body); status != http.StatusNotFound {
				t.Errorf("%s on %q = %d %v, want 404", name, id, status, body)
			}
		}
		f.user = "someone-else"
		if status, body := f.call(route.handler, http.MethodPost, f.home.ID, route.body); status != http.StatusNotFound {
			t.Errorf("%s as another user = %d %v, want 404", name, status, body)
		}
		f.user = f.home.OwnerUserID
		for _, bad := range []string{``, `{}`, `{"request_id":"x","include_templates":true}`, `{"request_id":"x","path":"/Users/me"}`} {
			if status, body := f.call(route.handler, http.MethodPost, f.home.ID, bad); status != http.StatusBadRequest {
				t.Errorf("%s with %q = %d %v, want 400", name, bad, status, body)
			}
		}
	}
	if status, _ := f.call(h.CommitAssistantProfileTemplates, http.MethodPost, f.home.ID, `{"request_id":"c2"}`); status != http.StatusBadRequest {
		t.Errorf("commit without a review id = %d, want 400", status)
	}
	if len(*calls) != 0 {
		t.Fatalf("a refused request called the plugin: %v", *calls)
	}
}
