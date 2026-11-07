package learningmissionshttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/learningmissions"
)

func (s *testSource) LearningScope(ctx context.Context, owner, id string) (string, error) {
	candidate, err := s.Candidate(ctx, owner, id)
	return candidate.Incarnation, err
}

func TestPreferenceRoutesRejectCallerOwnedConsentIdentityAndRecoverOneMutation(t *testing.T) {
	f := newFixture(t)
	body := `{"operation_id":"preferences","expected_revision":0,"mode":"on_request","workspace_ids":["workspace"],"observe":true,"guidance":"basic","focus":"Workshop","effort":"small","consent_version":1}`
	for _, extra := range []string{`"scope_bindings":{"workspace":"forged"}`, `"observation_since_ms":1`, `"observation_revision":1`, `"suggestion_since_ms":1`, `"suggestion_revision":1`, `"exclusions":[]`, `"owner":"foreign"`, `"actor":"user"`} {
		forged := strings.TrimSuffix(body, "}") + "," + extra + "}"
		if w := request(f, http.MethodPost, "/api/learning-missions/preferences", forged); w.Code != 400 {
			t.Fatalf("forged consent accepted: %d %s", w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest(http.MethodPost, "http://localhost:8931/api/learning-missions/preferences", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://localhost:1")
	denied := httptest.NewRecorder()
	f.mux.ServeHTTP(denied, r)
	if denied.Code != 403 || f.source.calls != 0 {
		t.Fatal("foreign origin or malformed consent reached the source")
	}
	for range 2 {
		if w := request(f, http.MethodPost, "/api/learning-missions/preferences", body); w.Code != 200 {
			t.Fatalf("preference write/replay: %d %s", w.Code, w.Body.String())
		}
	}
	var status learningmissions.Status
	if err := json.Unmarshal(request(f, http.MethodGet, "/api/learning-missions", "").Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Owner.Revision != 1 || !status.Owner.Preferences.Observes("workspace", "incarnation") || len(status.Instances) != 0 || f.source.calls != 1 {
		t.Fatalf("preferences replay/enrollment: %+v", status)
	}
	if w := request(f, http.MethodPost, "/api/learning-missions/clear", `{"operation_id":"clear-preferences","expected_revision":1,"scope":"preferences"}`); w.Code != 200 {
		t.Fatalf("clear: %d %s", w.Code, w.Body.String())
	}
	status = learningmissions.Status{}
	if err := json.Unmarshal(request(f, http.MethodGet, "/api/learning-missions", "").Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Owner.Preferences.Observe || len(status.Owner.Preferences.WorkspaceIDs) != 0 || len(status.Owner.Preferences.ScopeBindings) != 0 {
		t.Fatal("clear retained implicit observation consent")
	}
}
