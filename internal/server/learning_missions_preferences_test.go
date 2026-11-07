package server

import (
	"net/http"
	"testing"

	"github.com/johnjallday/ori-agent/internal/learningmissions"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestLearningMissionsProductionPreferencesResolveOwnedMetadataWithoutExecution(t *testing.T) {
	b, handler := newLearningTestServer(t)
	provider := &designerProvider{assessmentProvider: &assessmentProvider{}, isolation: true}
	b.llmFactory.Register("openai", provider)
	if err := b.configManager.SetSystemModel("openai", "gpt-assess"); err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Consent workshop"}) // deliberately no executor
	ws.OwnerUserID = "local"
	if err := b.workspaceStore.Save(ws); err != nil {
		t.Fatal(err)
	}
	var status learningmissions.Status
	learningRoute(t, handler, http.MethodGet, "/api/learning-missions", nil, 200, &status)
	body := map[string]any{"operation_id": "scope-consent", "expected_revision": status.Owner.Revision,
		"mode": "automatic", "workspace_ids": []string{ws.ID}, "observe": true, "guidance": "basic", "focus": "Workshop", "effort": "small",
		"consent_version": 1, "model_fingerprint": status.Model.Fingerprint}
	learningRoute(t, handler, http.MethodPost, "/api/learning-missions/preferences", body, 200, nil)
	learningRoute(t, handler, http.MethodPost, "/api/learning-missions/preferences", body, 200, nil)
	learningRoute(t, handler, http.MethodGet, "/api/learning-missions", nil, 200, &status)
	incarnation, err := learningmissions.NewSQLiteStore(b.sessionStore.DB()).SourceIncarnation(t.Context(), "local", ws.ID)
	if err != nil || !status.Owner.Preferences.Observes(ws.ID, incarnation) || status.Owner.Revision != 1 || provider.calls != 0 || len(status.Instances) != 0 {
		t.Fatalf("consent did not stay metadata-only: %+v %v", status, err)
	}
	fresh, err := b.workspaceStore.Get(ws.ID)
	if err != nil || len(fresh.Tasks) != 0 {
		t.Fatalf("preferences created work: %v", err)
	}
	body["operation_id"], body["expected_revision"], body["workspace_ids"] = "foreign-scope", status.Owner.Revision, []string{"unavailable"}
	learningRoute(t, handler, http.MethodPost, "/api/learning-missions/preferences", body, 404, nil)
	learningRoute(t, handler, http.MethodPost, "/api/learning-missions/clear", map[string]any{"operation_id": "clear-preferences", "expected_revision": status.Owner.Revision, "scope": "preferences"}, 200, nil)
	learningRoute(t, handler, http.MethodGet, "/api/learning-missions", nil, 200, &status)
	if status.Owner.Preferences.Observe || len(status.Owner.Preferences.ScopeBindings) != 0 || status.Owner.Preferences.Mode != "on_request" || provider.calls != 0 {
		t.Fatal("clear retained implicit consent or generated")
	}
}
