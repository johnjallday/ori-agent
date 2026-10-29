package sessionhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/grouprequirements"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

func observePolicyCreation(t *testing.T, handler *Handler, reference map[string]any) (int, map[string]any) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ObserveWorkspaceGroupCreation(response, httptest.NewRequest(http.MethodPost, "/api/workspaces/group-requirements/observe", bytes.NewBufferString(groupTemplateHomeBody(t, reference))))
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return response.Code, body
}

// The shared review treats a refusal received during Claim as definite only
// when the original operation was never recorded. Pin both facts it reads.
func TestWorkspaceGroupCreationObservation_StaleClaimRefusalLeavesNoOperation(t *testing.T) {
	template := policyTemplate(projecttemplates.GroupPolicyRequired)
	handler, store, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	homeID := preparePolicyHome(t, handler)
	payload := reviewPayload("Stale Claim Project", false)
	code, body := postPolicyWorkspace(t, handler, payload)
	if code != http.StatusOK {
		t.Fatalf("review status=%d body=%v", code, body)
	}
	review := body["group_requirement_review"].(map[string]any)
	template.Revision = strings.Repeat("c", 64)
	delete(payload, "group_requirement_review")
	payload["group_review_token"] = review["review_token"]
	payload["idempotency_key"] = "stale-claim"
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/workspaces", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.HandleWorkspaces(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale commit status=%d body=%s", response.Code, response.Body.String())
	}
	if phase := response.Header().Get("X-Ori-Group-Commit-Phase"); phase != "outcome_unknown" {
		t.Fatalf("stale Claim phase=%q, want outcome_unknown", phase)
	}
	code, observed := observePolicyCreation(t, handler, map[string]any{
		"group_review_token": review["review_token"],
		"idempotency_key":    "stale-claim",
		"input_digest":       review["input_digest"],
	})
	if code != http.StatusNotFound {
		t.Fatalf("observation status=%d body=%v, want 404 for an unrecorded operation", code, observed)
	}
	ids, _ := store.List()
	if len(ids) != 1 || ids[0] != homeID {
		t.Fatalf("stale project commit changed Home-only state: %v", ids)
	}
}

func TestWorkspaceGroupCreationObservation_ChangedPolicyCannotFallThroughToOrdinaryCreation(t *testing.T) {
	template := policyTemplate(projecttemplates.GroupPolicyNone)
	handler, store, cleanup := newPolicyHandler(t, &template)
	defer cleanup()
	payload := map[string]any{"name": "Reviewed Project", "template_id": template.ID, "group_composition": "standalone", "group_requirement_review": true}
	code, body := postPolicyWorkspace(t, handler, payload)
	if code != http.StatusOK {
		t.Fatalf("review = %d %v", code, body)
	}
	review := body["group_requirement_review"].(map[string]any)
	delete(payload, "group_requirement_review")
	payload["group_review_token"], payload["idempotency_key"] = review["review_token"], "original-policy-operation"
	template.GroupRequirement = nil // trusted source changed after the inert review
	if code, body := postPolicyWorkspace(t, handler, payload); code != http.StatusConflict {
		t.Fatalf("removed policy = %d %v", code, body)
	}
	if code, _ := postPolicyWorkspace(t, handler, map[string]any{"name": "Not a Blank confirmation", "blank": true, "group_requirement_review": true}); code != http.StatusConflict {
		t.Fatalf("inert group review without a template = %d", code)
	}
	if ids, err := store.List(); err != nil || len(ids) != 0 {
		t.Fatalf("old group intent created a substitute: %v %v", ids, err)
	}
	// A separately submitted ordinary request still uses its supported owner.
	if code, body := postPolicyWorkspace(t, handler, map[string]any{"name": "Separately Confirmed Ordinary", "template_id": template.ID}); code != http.StatusCreated {
		t.Fatalf("fresh ordinary request = %d %v", code, body)
	}
}

func TestWorkspaceGroupCreationObservation_OriginalReceiptAndCanonicalIdentityOnly(t *testing.T) {
	for _, policy := range []projecttemplates.GroupPolicy{projecttemplates.GroupPolicyRequired, projecttemplates.GroupPolicyNone} {
		t.Run(string(policy), func(t *testing.T) {
			template := policyTemplate(policy)
			handler, store, cleanup := newPolicyHandler(t, &template)
			defer cleanup()
			receipts := grouprequirements.NewMemoryStore()
			handler.SetGroupRequirementService(grouprequirements.NewService(store, receipts), func(context.Context) (string, error) { return "local", nil })
			homeID, composition, ordinaryParent := "", "standalone", ""
			if policy == projecttemplates.GroupPolicyNone {
				// Canonical parent fixture only: this test exercises the project
				// owner/reader, not another UI Group Manager creation journey.
				parent := workspace.NewWorkspace(workspace.CreateWorkspaceParams{Name: "Explicit Ordinary Parent"})
				parent.Kind, parent.OwnerUserID = "group", "local"
				if err := store.Save(parent); err != nil {
					t.Fatal(err)
				}
				ordinaryParent = parent.ID
			}
			if policy == projecttemplates.GroupPolicyRequired {
				homeID = preparePolicyHome(t, handler)
				composition = "grouped"
			}
			payload := map[string]any{"name": "Original Project", "template_id": template.ID, "group_composition": composition, "group_requirement_review": true}
			if ordinaryParent != "" {
				payload["parent_id"] = ordinaryParent
			}
			code, body := postPolicyWorkspace(t, handler, payload)
			if code != http.StatusOK {
				t.Fatalf("review = %d %v", code, body)
			}
			review := body["group_requirement_review"].(map[string]any)
			reference := map[string]any{"group_review_token": review["review_token"], "input_digest": review["input_digest"], "idempotency_key": "original-project-operation"}
			if review["input_digest"] == nil || review["input_digest"] == "" {
				t.Fatal("review omitted its opaque recovery witness")
			}
			if code, _ := observePolicyCreation(t, handler, reference); code != http.StatusNotFound {
				t.Fatalf("unaccepted observation = %d", code)
			}
			delete(payload, "group_requirement_review")
			payload["group_review_token"], payload["idempotency_key"] = reference["group_review_token"], reference["idempotency_key"]
			code, body = postPolicyWorkspace(t, handler, payload)
			if code != http.StatusCreated {
				t.Fatalf("commit = %d %v", code, body)
			}
			projectID := body["folder"].(map[string]any)["id"].(string)
			before, err := store.Get(projectID)
			if err != nil {
				t.Fatal(err)
			}
			handler.SetProjectTemplateResolver(func(string, string) (projecttemplates.Template, error) {
				panic("observation resolved a creation source")
			})
			for i := 0; i < 3; i++ {
				code, observed := observePolicyCreation(t, handler, reference)
				outcome, _ := observed["group_requirement_operation"].(map[string]any)
				folder, _ := observed["folder"].(map[string]any)
				if code != http.StatusOK || outcome["state"] != "complete" || outcome["workspace_id"] != projectID || outcome["home_workspace_id"] != homeID || outcome["home_created"] != false || folder["id"] != projectID {
					t.Fatalf("observation = %d %v", code, observed)
				}
			}
			after, err := store.Get(projectID)
			if err != nil {
				t.Fatal(err)
			}
			beforeJSON, _ := json.Marshal(before)
			afterJSON, _ := json.Marshal(after)
			if !bytes.Equal(beforeJSON, afterJSON) {
				t.Fatal("reading creation changed its resource")
			}
			for _, change := range []map[string]any{{"input_digest": "wrong"}, {"group_review_token": "another-review"}, {"idempotency_key": "another-operation"}, {"name": "unsupported material field"}} {
				changed := make(map[string]any, len(reference)+1)
				for key, value := range reference {
					changed[key] = value
				}
				for key, value := range change {
					changed[key] = value
				}
				if code, _ := observePolicyCreation(t, handler, changed); code == http.StatusOK {
					t.Fatalf("changed/forged reference observed original: %v", change)
				}
			}
			handler.currentUserID = func(context.Context) (string, error) { return "foreign", nil }
			if code, _ := observePolicyCreation(t, handler, reference); code == http.StatusOK {
				t.Fatal("foreign owner observed original creation")
			}
			handler.currentUserID = func(context.Context) (string, error) { return "local", nil }
			operation, err := receipts.GetOperation(t.Context(), "local", grouprequirements.OperationCreateWorkspace, "original-project-operation")
			if err != nil {
				t.Fatal(err)
			}
			if err := handler.groupRequirements.Mark(t.Context(), operation, grouprequirements.OperationReconcileRequired); err != nil {
				t.Fatal(err)
			}
			partial, err := receipts.GetOperation(t.Context(), "local", grouprequirements.OperationCreateWorkspace, "original-project-operation")
			if err != nil {
				t.Fatal(err)
			}
			if code, _ := observePolicyCreation(t, handler, reference); code != http.StatusAccepted {
				t.Fatalf("partial read = %d", code)
			}
			unchanged, err := receipts.GetOperation(t.Context(), "local", grouprequirements.OperationCreateWorkspace, "original-project-operation")
			if err != nil || !reflect.DeepEqual(partial, unchanged) {
				t.Fatalf("read advanced partial operation: %#v %v", unchanged, err)
			}
			if err := handler.groupRequirements.Mark(t.Context(), operation, grouprequirements.OperationSucceeded); err != nil {
				t.Fatal(err)
			}
			originalProvenance := before.GetTemplateProvenance()
			if err := store.Update(projectID, func(project *workspace.Workspace) error {
				changed := project.GetTemplateProvenance()
				changed.GroupRequirement.OperationDigest = strings.Repeat("f", 64)
				project.SetTemplateProvenance(changed)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if code, _ := observePolicyCreation(t, handler, reference); code != http.StatusConflict {
				t.Fatalf("changed provenance read = %d", code)
			}
			if err := store.Update(projectID, func(project *workspace.Workspace) error {
				project.SetTemplateProvenance(originalProvenance)
				project.Status = workspace.StatusTrashed
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if code, _ := observePolicyCreation(t, handler, reference); code != http.StatusConflict {
				t.Fatalf("trashed result read = %d", code)
			}
			ids, err := store.List()
			want := 1
			if homeID != "" || ordinaryParent != "" {
				want++
			}
			if err != nil || len(ids) != want {
				t.Fatalf("observation changed resource count: %v %v", ids, err)
			}
		})
	}
}
