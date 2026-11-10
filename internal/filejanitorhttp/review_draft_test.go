package filejanitorhttp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
)

func draftRequest(t *testing.T, h *Handler, batchID, action, groupID string) filejanitor.ReviewDraftEdit {
	t.Helper()
	rec, body := serve(t, h, http.MethodGet, pathFor("file-janitor", "ws-1", "/batches/"+batchID+"/draft"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("read draft: %d %s", rec.Code, rec.Body.String())
	}
	draft := body["draft"].(map[string]any)
	return filejanitor.ReviewDraftEdit{ExpectedRevision: draft["revision"].(string), ExpectedGroupRevision: draft["group_revision"].(string), Action: action, GroupID: groupID}
}

func TestReviewDraftHTTP_Exact206SelectionAndAliasParity(t *testing.T) {
	h, _ := groupHandler(t, 206)
	review, card := readGroupCard(t, h)
	batchID, groupID := review["batch_id"].(string), card["id"].(string)
	request := draftRequest(t, h, batchID, "select_group", groupID)
	data, _ := json.Marshal(request)
	for i, prefix := range routePrefixPair {
		target := pathFor(prefix.prefix, "ws-1", "/batches/"+batchID+"/draft")
		if i == 0 {
			rec, body := serve(t, h, http.MethodPost, target, string(data))
			if rec.Code != http.StatusOK {
				t.Fatalf("group select: %d %s", rec.Code, rec.Body.String())
			}
			draft := body["draft"].(map[string]any)
			if len(draft["choices"].([]any)) != 206 {
				t.Fatal("selected visible page instead of exact group")
			}
			for _, private := range []string{"fingerprint", "token", "scope_digest", "root_path"} {
				if strings.Contains(rec.Body.String(), private) {
					t.Fatalf("draft leaked %s", private)
				}
			}
		} else {
			rec, _ := serve(t, h, http.MethodPost, target, string(data))
			if rec.Code != http.StatusConflict {
				t.Fatal("legacy alias bypassed intent CAS")
			}
		}
	}
	canonical, canonicalBody := serve(t, h, http.MethodGet, pathFor("file-janitor", "ws-1", "/batches/"+batchID+"/draft"), "")
	legacy, legacyBody := serve(t, h, http.MethodGet, pathFor("downloads-janitor", "ws-1", "/batches/"+batchID+"/draft"), "")
	if canonical.Code != http.StatusOK || legacy.Code != http.StatusOK || !reflect.DeepEqual(canonicalBody, legacyBody) {
		t.Fatal("draft alias reads diverged")
	}
	if canonical.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("saved choices can be cached by shared intermediaries")
	}
}

func TestReviewDraftHTTP_ClosedBoundedEditsAreAtomic(t *testing.T) {
	h, _ := groupHandler(t, 2)
	review, card := readGroupCard(t, h)
	batchID := review["batch_id"].(string)
	valid := draftRequest(t, h, batchID, "select_group", card["id"].(string))
	encoded, _ := json.Marshal(valid)
	_, members := serve(t, h, http.MethodGet, pathFor("file-janitor", "ws-1", "/batches/"+batchID), "")
	id := candidateIDs(t, members)[0]
	statePath := filepath.Join(h.lookup.(*fakeStore).root, "ws-1", "downloads-janitor", "scan-state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range routePrefixPair {
		for _, tc := range []struct {
			name, body string
			status     int
		}{
			{"unknown field", strings.TrimSuffix(string(encoded), "}") + `,"destination":"outside"}`, http.StatusBadRequest},
			{"multiple objects", string(encoded) + `{}`, http.StatusBadRequest},
			{"unknown action", strings.Replace(string(encoded), "select_group", "select_future_matches", 1), http.StatusBadRequest},
			{"duplicate IDs", fmt.Sprintf(`{"expected_revision":%q,"expected_group_revision":%q,"action":"select_files","candidate_ids":[%q,%q]}`, valid.ExpectedRevision, valid.ExpectedGroupRevision, id, id), http.StatusBadRequest},
			{"bad category", strings.TrimSuffix(string(encoded), "}") + `,"category":"../../outside"}`, http.StatusBadRequest},
			{"large body", strings.Repeat(" ", filejanitor.MaxReviewEditBytes+1), http.StatusRequestEntityTooLarge},
		} {
			t.Run(prefix.name+"/"+tc.name, func(t *testing.T) {
				rec, _ := serve(t, h, http.MethodPost, pathFor(prefix.prefix, "ws-1", "/batches/"+batchID+"/draft"), tc.body)
				if rec.Code != tc.status {
					t.Fatalf("%d, want %d: %s", rec.Code, tc.status, rec.Body.String())
				}
				after, _ := os.ReadFile(statePath)
				if string(after) != string(before) {
					t.Fatal("invalid edit partially persisted")
				}
			})
		}
	}
	request := valid
	request.Action, request.GroupID = "select_files", ""
	request.CandidateIDs = make([]string, filejanitor.MaxReviewChoicesPerBatch+1)
	for i := range request.CandidateIDs {
		request.CandidateIDs[i] = fmt.Sprintf("c-%d", i)
	}
	data, _ := json.Marshal(request)
	rec, _ := serve(t, h, http.MethodPost, pathFor("file-janitor", "ws-1", "/batches/"+batchID+"/draft"), string(data))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatal("selection input cap not enforced")
	}
	after, _ := os.ReadFile(statePath)
	if string(after) != string(before) {
		t.Fatal("oversized selection truncated instead of rejected")
	}
}

func TestReviewDraftHTTP_OversizedGroupRefusesWithoutPartialSelection(t *testing.T) {
	h, _ := groupHandler(t, 1001)
	review, card := readGroupCard(t, h)
	batchID := review["batch_id"].(string)
	request := draftRequest(t, h, batchID, "select_group", card["id"].(string))
	data, _ := json.Marshal(request)
	target := pathFor("file-janitor", "ws-1", "/batches/"+batchID+"/draft")
	rec, body := serve(t, h, http.MethodPost, target, string(data))
	if rec.Code != http.StatusRequestEntityTooLarge || body["code"] != "review_bounds" {
		t.Fatalf("oversized group: %d %s", rec.Code, rec.Body.String())
	}
	_, fresh := serve(t, h, http.MethodGet, target, "")
	if len(fresh["draft"].(map[string]any)["choices"].([]any)) != 0 {
		t.Fatal("oversized group partially selected")
	}
}

func TestReviewDraftHTTP_ReadDoesNotPersistDraft(t *testing.T) {
	h, _ := groupHandler(t, 2)
	review, _ := readGroupCard(t, h)
	statePath := filepath.Join(h.lookup.(*fakeStore).root, "ws-1", "downloads-janitor", "scan-state.json")
	before, _ := os.ReadFile(statePath)
	rec, body := serve(t, h, http.MethodGet, pathFor("file-janitor", "ws-1", "/batches/"+review["batch_id"].(string)+"/draft"), "")
	if rec.Code != http.StatusOK || len(body["draft"].(map[string]any)["choices"].([]any)) != 0 {
		t.Fatal("new draft read invented selection")
	}
	after, _ := os.ReadFile(statePath)
	if string(before) != string(after) {
		t.Fatal("GET created a saved draft")
	}
}
