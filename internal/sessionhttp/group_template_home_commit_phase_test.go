package sessionhttp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
)

type failHomeReceiptConsumption struct{ *grouprequirements.MemoryStore }

func (store *failHomeReceiptConsumption) ConsumeReceipt(context.Context, string, time.Time) error {
	return grouprequirements.ErrUnavailable
}

func TestGroupTemplateHome_CommitRefusalPhaseDistinguishesPartialEffects(t *testing.T) {
	handler, store, id, revision, cleanup := newGroupTemplateHomeHandler(t)
	defer cleanup()
	receipts := &failHomeReceiptConsumption{grouprequirements.NewMemoryStore()}
	handler.SetGroupRequirementService(grouprequirements.NewService(store, receipts), func(context.Context) (string, error) { return "local", nil })
	selection := map[string]any{"group_template_id": id, "revision": revision, "name": "Original Home"}
	code, body := postGroupTemplateHome(t, handler, false, groupTemplateHomeBody(t, selection))
	if code != http.StatusOK {
		t.Fatalf("review = %d %v", code, body)
	}
	review := body["group_template_review"].(map[string]any)
	request := map[string]any{"group_template_id": id, "revision": revision, "name": "Original Home", "group_review_token": review["review_token"], "idempotency_key": "original-home-operation"}
	post := func(fields map[string]any) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.CommitGroupTemplateHome(response, httptest.NewRequest(http.MethodPost, "/api/workspaces/group-templates/commit", bytes.NewBufferString(groupTemplateHomeBody(t, fields))))
		return response
	}
	stale := map[string]any{"group_template_id": id, "revision": "stale", "name": "Original Home", "group_review_token": review["review_token"], "idempotency_key": "never-accepted"}
	refused := post(stale)
	if refused.Code != http.StatusConflict || refused.Header().Get("X-Ori-Group-Commit-Phase") != "not_accepted" {
		t.Fatalf("pre-effect refusal = %d %s %s", refused.Code, refused.Header(), refused.Body)
	}
	if ids, err := store.List(); err != nil || len(ids) != 0 {
		t.Fatalf("pre-effect refusal created resources: %v %v", ids, err)
	}
	partial := post(request)
	if partial.Code != http.StatusServiceUnavailable || partial.Header().Get("X-Ori-Group-Commit-Phase") != "outcome_unknown" {
		t.Fatalf("post-effect failure = %d %s %s", partial.Code, partial.Header(), partial.Body)
	}
	ids, err := store.List()
	if err != nil || len(ids) != 1 {
		t.Fatalf("partial operation resource = %v, %v", ids, err)
	}
	for i := 0; i < 2; i++ {
		if code, observed := observeGroupHome(t, handler, request); code != http.StatusAccepted || observed["state"] != "home_ready" || observed["layout_subject_new"] != nil {
			t.Fatalf("partial read = %d %v", code, observed)
		}
	}
	op, err := receipts.GetOperation(t.Context(), "local", grouprequirements.OperationPrepareHome, "original-home-operation")
	if err != nil || op.Status != grouprequirements.OperationHomeReady || !op.HomeCreated || op.HomeWorkspaceID != ids[0] {
		t.Fatalf("observation resumed partial effects or lost creation evidence: %#v %v", op, err)
	}
}
